package face

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// maxOutputBytes 限制子进程输出，避免异常脚本无限输出占满内存。
const maxOutputBytes = 64 << 10

// PythonProbe 通过子进程运行 tools/face_probe.py（或兼容命令）。
// Command 是空格分隔的命令前缀，图片路径作为最后一个参数追加；
// 不要把用户可控内容放进 FACE_PROBE_CMD。
type PythonProbe struct {
	Command []string
	Timeout time.Duration
}

// NewPythonProbe 解析命令前缀。command 为空时使用 DefaultCommand；
// timeout<=0 时使用 DefaultTimeout。
func NewPythonProbe(command string, timeout time.Duration) (*PythonProbe, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		fields = strings.Fields(DefaultCommand)
	}
	if len(fields) == 0 {
		return nil, errors.New("face: empty probe command")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &PythonProbe{Command: fields, Timeout: timeout}, nil
}

// Probe 执行检测命令并解析 JSON。超时、退出码非零、输出非法都返回 error；
// 脚本返回的 error 码会转成 error，但仍不包含图片路径。
func (p *PythonProbe) Probe(ctx context.Context, imagePath string) (Result, error) {
	if p == nil || len(p.Command) == 0 {
		return Result{}, errors.New("face: probe is not configured")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := make([]string, 0, len(p.Command))
	args = append(args, p.Command[1:]...)
	args = append(args, imagePath)
	cmd := exec.CommandContext(probeCtx, p.Command[0], args...)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	cmd.WaitDelay = 2 * time.Second

	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: 4 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return Result{}, errors.New("face: probe timed out")
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}

	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) > 0 {
		var result Result
		if jsonErr := json.Unmarshal(raw, &result); jsonErr == nil {
			if result.Error != "" {
				return Result{}, fmt.Errorf("face: probe error: %s", safeErrorCode(result.Error))
			}
			if !result.Valid() || (result.HasFace && (result.Center == nil || result.Box == nil)) {
				return Result{}, errors.New("face: probe returned invalid geometry")
			}
			if err == nil {
				return result, nil
			}
			// 脚本已经给出有效结果但退出码非零：仍以结果为准。
			return result, nil
		}
	}
	if err != nil {
		if probeUnavailable(err) {
			// exec.Error / PathError 会带命令绝对路径，不能进日志。
			return Result{}, ErrProbeUnavailable
		}
		return Result{}, fmt.Errorf("face: probe failed: %w", err)
	}
	return Result{}, errors.New("face: probe returned invalid json")
}

// probeUnavailable 把“命令不存在/不可执行”归一成固定错误码，避免把
// FACE_PROBE_CMD 的绝对路径写进日志。
func probeUnavailable(err error) bool {
	if errors.Is(err, exec.ErrNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrPermission) {
		return true
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr)
}

// safeErrorCode 只保留脚本错误码的安全字符，防止日志注入或意外带出路径。
func safeErrorCode(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range raw {
		if b.Len() >= 80 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == ':', r == '.':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// limitedBuffer 是带容量上限的 bytes.Buffer；超过上限后丢弃后续内容。
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	// 始终报告全部写入，避免子进程因 EPIPE 提前退出。
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
