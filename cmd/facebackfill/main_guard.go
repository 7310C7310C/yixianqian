package main

import (
	"errors"

	"github.com/7310C7310C/yixianqian/internal/config"
)

// 回填守卫：腾讯云合规判定的结论只落在 has_face 上，本地重扫无法重建
// “这张曾被判为动物/卡通”的信息，所以任何会跳过合规判定的重扫都必须
// 拒绝，而不是静默覆盖。
var (
	errAllWithoutCompliance = errors.New("拒绝执行 -all：未开启 TENCENT_FACE_BACKFILL=true 时只会用本地 YuNet 重扫，会把腾讯云已判为非人类（动物/卡通）的照片 has_face 翻回 true，静默撤销合规结论。请先设置 TENCENT_FACE_BACKFILL=true 并确认凭证；不带 -all 的日常增量不受影响")
	errMissingCredentials   = errors.New("缺少 TENCENT_SECRET_ID/TENCENT_SECRET_KEY：检测到 TENCENT_FACE_ENABLED 或 TENCENT_FACE_BACKFILL，但凭证不齐；拒绝运行回填，避免写入未经合规判定的 has_face")
)

// guardBackfill 校验本次回填是否会静默撤销或跳过合规结论。它必须在任何
// 数据库/R2 连接之前执行，保证拒绝时不产生副作用。
func guardBackfill(cfg config.Config, includeChecked bool) error {
	if includeChecked && !cfg.TencentFaceBackfill {
		return errAllWithoutCompliance
	}
	if (cfg.TencentFaceEnabled || cfg.TencentFaceBackfill) && !cfg.TencentFaceConfigured() {
		return errMissingCredentials
	}
	return nil
}
