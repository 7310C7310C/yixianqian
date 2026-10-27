package face

import "context"

// ComplianceResult 是第三方照片合规判定的结论。Blocked 只表示「判定为非
// 人类照片」；Reason 必须是固定码（animal/cartoon），不得放厂商标签名。
// 任何判定失败都必须返回 Blocked=false（fail-open），避免外部服务抖动
// 阻断用户上传。
type ComplianceResult struct {
	Blocked bool
	Reason  string
}

// Compliance 对一张本地临时图片做照片合规判定。photoID 只用于日志关联，
// 实现不得记录图片路径、图片内容、凭证或响应体。
//
// 实现必须尊重 ctx 自行超时；调用方把零值（Blocked=false）视为放行。
type Compliance interface {
	CheckPhoto(ctx context.Context, photoID, imagePath string) ComplianceResult
}

// ApplyCompliance 把合规结论合并进本地检测结果：只有本地检出人脸、且未被
// 判定为非人类照片，has_face 才为 true。Center/Box 始终保留本地检测值，
// 不用第三方结果伪造几何。
func ApplyCompliance(local Result, compliance ComplianceResult) Result {
	if !compliance.Blocked {
		return local
	}
	local.Blocked = true
	local.Reason = compliance.Reason
	local.HasFace = false
	return local
}
