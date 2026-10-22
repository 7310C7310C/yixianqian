// Package face 提供本地人脸检测探针。检测完全在服务器本地完成，
// 不依赖任何第三方付费接口。
package face

import (
	"context"
	"errors"
	"time"
)

// DefaultCommand 是默认的人脸检测命令；可通过 FACE_PROBE_CMD 覆盖。
const DefaultCommand = "python3 tools/face_probe.py"

// DefaultTimeout 是单张照片检测的默认超时。
const DefaultTimeout = 8 * time.Second

// Result 是 tools/face_probe.py 的输出。Center 与 Box 为相对原图的比例
// （0~1）；无脸或检测失败时为 nil。Error 是脚本返回的固定错误码，
// 不包含图片路径或凭证。
type Result struct {
	HasFace bool      `json:"has_face"`
	Center  []float64 `json:"center"`
	Box     []float64 `json:"box"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
	Error   string    `json:"error"`
}

// Valid 报告检测结果中的几何数据是否可用于卡片人脸对准。
func (r Result) Valid() bool {
	if r.Width < 0 || r.Height < 0 {
		return false
	}
	if r.Center == nil && r.Box == nil {
		return true
	}
	if len(r.Center) != 2 || len(r.Box) != 2 {
		return false
	}
	return normalized(r.Center[0]) && normalized(r.Center[1]) &&
		r.Box[0] > 0 && r.Box[0] <= 1 && r.Box[1] > 0 && r.Box[1] <= 1
}

func normalized(v float64) bool { return v >= 0 && v <= 1 }

// Probe 对一张本地图片做检测。实现必须尊重 ctx，并在失败时返回错误而不是
// 阻塞调用方。调用方应把错误视为“本次未检测”，不要阻断上传。
type Probe interface {
	Probe(ctx context.Context, imagePath string) (Result, error)
}

// ProbeFunc 让调用方（含测试）用函数实现 Probe。
type ProbeFunc func(ctx context.Context, imagePath string) (Result, error)

// Probe 实现 Probe 接口。
func (f ProbeFunc) Probe(ctx context.Context, imagePath string) (Result, error) {
	if f == nil {
		return Result{}, errors.New("face: probe unavailable")
	}
	return f(ctx, imagePath)
}
