// Package tencentface 实现腾讯云「照片内容合规判定」：先做图像标签
// （tiia DetectLabel），标签命中动物/卡通后再做静默活体
// （iai DetectLiveFaceAccurate）；只有「标签命中 且 活体判定无人脸」才
// 视为非人类照片。判定语义照 v1 web/backend/tencent_face.py。
//
// 本包不引入 Python/SDK 运行时依赖，直接按 TC3-HMAC-SHA256 签名调用两个
// action；TLS 校验保持开启，超时默认 8s，不重试（避免重复计费）。
// 所有错误只暴露固定码，凭证与响应体不会出现在 error、日志或 JSON 中。
package tencentface

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/face"
)

// 放行（fail-open）原因码。所有外部失败都只记这些固定码。
const (
	CodeDisabled      = "disabled"
	CodeNoCredentials = "no_credentials"
)

// Verdict 是一次完整判定的结果。Skipped 非空表示未完成判定（放行）。
type Verdict struct {
	Blocked   bool
	Reason    string
	Animal    float64
	Cartoon   float64
	NoFace    bool
	Calls     int
	Skipped   string
	LatencyMS int64
}

// resultCode 是日志里的固定结果码（不含任何厂商字符串）。
func (v Verdict) resultCode() string {
	switch {
	case v.Skipped != "":
		return "skipped:" + v.Skipped
	case v.Blocked:
		return "blocked"
	default:
		return "pass"
	}
}

// Options 构造 Checker。SecretID/SecretKey 只来自环境变量。
type Options struct {
	SecretID     string
	SecretKey    string
	Region       string
	TiiaEndpoint string
	IaiEndpoint  string
	Timeout      time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
	HTTPClient   *http.Client
	MaxEdge      int
}

// Checker 串起「标签 -> 活体」判定，并在任何异常时放行。实现
// face.Compliance；调用方只需要关心 Blocked。
type Checker struct {
	client     *Client
	log        *slog.Logger
	timeout    time.Duration
	now        func() time.Time
	maxEdge    int
	configured bool
}

var _ face.Compliance = (*Checker)(nil)

// NewChecker 构造 Checker。凭证缺失不报错：Check 会返回
// skipped:no_credentials 并放行，避免外部配置缺失阻断上传。
func NewChecker(opt Options) (*Checker, error) {
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	client, err := NewClient(ClientOptions{
		SecretID:     opt.SecretID,
		SecretKey:    opt.SecretKey,
		Region:       opt.Region,
		TiiaEndpoint: opt.TiiaEndpoint,
		IaiEndpoint:  opt.IaiEndpoint,
		Timeout:      timeout,
		HTTPClient:   opt.HTTPClient,
		Now:          now,
	})
	if err != nil {
		return nil, err
	}
	secretID := strings.TrimSpace(opt.SecretID)
	secretKey := strings.TrimSpace(opt.SecretKey)
	return &Checker{
		client:     client,
		log:        logger,
		timeout:    timeout,
		now:        now,
		maxEdge:    opt.MaxEdge,
		configured: secretID != "" && secretKey != "",
	}, nil
}

// Configured 报告是否带齐凭证；未配置时所有判定都会放行。
func (c *Checker) Configured() bool { return c != nil && c.configured }

// CheckPhoto 实现 face.Compliance。任何失败都返回 Blocked=false。
func (c *Checker) CheckPhoto(ctx context.Context, photoID, imagePath string) face.ComplianceResult {
	verdict := c.Check(ctx, photoID, imagePath)
	return face.ComplianceResult{Blocked: verdict.Blocked, Reason: verdict.Reason}
}

// Check 执行完整判定：
//  1. 图像标签命中动物（>=30）或明确卡通（>=80）才调用静默活体；
//  2. 活体返回 NoFaceInPhoto -> 非人类照片；活体检出人脸 -> 放行；
//  3. 无凭证/解码失败/超时/鉴权失败/限频/5xx -> 放行，只记固定码。
//
// 日志只含 action/result/latency_ms/photo_id，不含图片内容、路径或凭证。
func (c *Checker) Check(ctx context.Context, photoID, imagePath string) (verdict Verdict) {
	if c == nil {
		return Verdict{Skipped: CodeDisabled}
	}
	started := c.now()
	defer func() {
		verdict.LatencyMS = c.now().Sub(started).Milliseconds()
		c.log.Info("tencent face", "photo_id", photoID, "action", "check",
			"result", verdict.resultCode(), "latency_ms", verdict.LatencyMS)
	}()
	if !c.configured {
		verdict.Skipped = CodeNoCredentials
		return verdict
	}

	raw, err := readBounded(imagePath, maxInputBytes)
	if err != nil {
		verdict.Skipped = CodeInvalidImage
		return verdict
	}
	payload, err := preparePayload(raw, c.maxEdge)
	if err != nil {
		verdict.Skipped = CodeInvalidImage
		return verdict
	}

	// 超时预算只覆盖外部调用；本地压缩有像素上限，不应吃掉 API 时间。
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	verdict.Calls++
	callStarted := c.now()
	labels, err := c.client.DetectLabel(ctx, payload)
	callMS := c.now().Sub(callStarted).Milliseconds()
	if err != nil {
		verdict.Skipped = skipCode(err)
		c.log.Debug("tencent face", "photo_id", photoID, "action", actionDetectLabel,
			"result", verdict.Skipped, "latency_ms", callMS)
		return verdict
	}
	animal, cartoon := labelConfidence(labels)
	verdict.Animal, verdict.Cartoon = animal, cartoon
	hit := labelHit(animal, cartoon)
	result := "no_hit"
	if hit {
		result = "hit"
	}
	c.log.Debug("tencent face", "photo_id", photoID, "action", actionDetectLabel,
		"result", result, "latency_ms", callMS)
	if !hit {
		return verdict
	}

	verdict.Calls++
	callStarted = c.now()
	err = c.client.DetectLiveFaceAccurate(ctx, payload)
	callMS = c.now().Sub(callStarted).Milliseconds()
	if err != nil {
		if errors.Is(err, ErrNoFaceInPhoto) {
			c.log.Debug("tencent face", "photo_id", photoID, "action", actionDetectLiveFace,
				"result", CodeNoFace, "latency_ms", callMS)
			verdict = Judge(labels, true)
			verdict.Calls = 2
			return verdict
		}
		verdict.Skipped = skipCode(err)
		c.log.Debug("tencent face", "photo_id", photoID, "action", actionDetectLiveFace,
			"result", verdict.Skipped, "latency_ms", callMS)
		return verdict
	}
	c.log.Debug("tencent face", "photo_id", photoID, "action", actionDetectLiveFace,
		"result", "face", "latency_ms", callMS)
	verdict = Judge(labels, false)
	verdict.Calls = 2
	return verdict
}

// skipCode 把任意错误压成固定放行码。
func skipCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		return apiErr.Code
	}
	return CodeAPIError
}
