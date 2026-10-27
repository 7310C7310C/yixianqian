package httpserver

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
)

// maxConcurrentFaceProbes 限制同时运行的本地人脸检测数。probe 是 CPU/IO
// 密集的子进程，但不该占用 mediaSem：上传解码完成后再检测，慢 probe
// 不会让其他上传排队到 429。
const maxConcurrentFaceProbes = 2

// maxRegistrationFaceProbeBudget 是注册响应前同步检测的总时间上限；注册
// 流程不能因为某个大图 probe 长时间不返回而卡住。超出预算的照片交给
// 后台补齐，注册本身不受影响。
const maxRegistrationFaceProbeBudget = 15 * time.Second

// faceProbeOutcome 描述一次照片检测是否已尝试，以及结果是否已落库。
type faceProbeOutcome int

const (
	// faceProbeChecked 检测完成且结果已写入 profile_photos。
	faceProbeChecked faceProbeOutcome = iota
	// faceProbeFailed 已尝试但失败（下载/解码/检测/写库），留给 backfill 重试。
	faceProbeFailed
	// faceProbeSkipped 因预算或信号量超时未尝试，调用方应安排后台重试。
	faceProbeSkipped
)

// probeProfilePhotoFace 在照片上传成功后同步运行本地人脸检测，并把结果写入
// profile_photos。任何失败都只记日志并放行上传；此时 face_checked 保持
// false，由 cmd/facebackfill 或下次重新检测补齐。
//
// 日志只带 media_id，不输出图片临时路径、R2 对象键或任何凭证。
func (a *App) probeProfilePhotoFace(ctx context.Context, userID int64, mediaID string) {
	_, _ = a.probePhotoFace(ctx, userID, mediaID)
}

// probePhotoFace 执行单张照片检测。outcome 为 faceProbeSkipped 时表示没有
// 真正尝试（预算/信号量耗尽），调用方可安全地安排后台重试；failed 表示已
// 尝试但失败，通常交给 facebackfill，避免在请求路径里无限重试。
func (a *App) probePhotoFace(ctx context.Context, userID int64, mediaID string) (face.Result, faceProbeOutcome) {
	if a.faceProbe == nil {
		return face.Result{}, faceProbeSkipped
	}
	timeout := a.cfg.FaceProbeTimeout
	if timeout <= 0 {
		timeout = face.DefaultTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 人脸检测使用独立信号量，且只在上传的 mediaSem 释放后调用。
	select {
	case a.faceSem <- struct{}{}:
		defer releaseGate(a.faceSem)
	case <-probeCtx.Done():
		a.log.Warn("face probe skipped: busy", "media_id", mediaID)
		return face.Result{}, faceProbeSkipped
	}

	media, err := a.db.GetMediaForUser(probeCtx, mediaID, userID)
	if err != nil {
		a.log.Warn("face probe media lookup failed", "error", err, "media_id", mediaID)
		return face.Result{}, faceProbeFailed
	}

	obj, err := a.store.Get(probeCtx, media.ObjectKey)
	if err != nil {
		// media 包的错误含对象键，故意不记录原始错误。
		a.log.Warn("face probe object unavailable", "media_id", mediaID)
		return face.Result{}, faceProbeFailed
	}
	defer obj.Close()

	limit := a.cfg.MediaMaxBytes
	if limit <= 0 {
		limit = 16 << 20
	}
	tmp, err := os.CreateTemp("", "yxq-face-*"+faceImageExt(media.ContentType))
	if err != nil {
		a.log.Warn("face probe temp file failed", "media_id", mediaID)
		return face.Result{}, faceProbeFailed
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, copyErr := io.Copy(tmp, io.LimitReader(obj, limit+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > limit {
		a.log.Warn("face probe image copy failed", "media_id", mediaID)
		return face.Result{}, faceProbeFailed
	}

	result, err := a.faceProbe.Probe(probeCtx, tmpName)
	if err != nil {
		a.log.Warn("face probe failed", "error", err, "media_id", mediaID)
		if probeCtx.Err() != nil {
			// 预算/信号量把本次检测截断了，交给后台重试。
			return face.Result{}, faceProbeSkipped
		}
		return face.Result{}, faceProbeFailed
	}

	// 腾讯云照片合规判定（标签 + 静默活体）：命中非人类照片时只把
	// has_face 置 false，几何仍用本地检测结果；任何失败都放行。
	if a.faceCompliance != nil {
		verdict := a.faceCompliance.CheckPhoto(probeCtx, mediaID, tmpName)
		if verdict.Blocked {
			a.log.Info("face compliance blocked", "media_id", mediaID, "reason", verdict.Reason)
		}
		result = face.ApplyCompliance(result, verdict)
	}

	row := db.ProfilePhotoFace{MediaID: mediaID, Checked: true, HasFace: result.HasFace}
	if len(result.Center) == 2 && len(result.Box) == 2 {
		centerX, centerY := result.Center[0], result.Center[1]
		boxW, boxH := result.Box[0], result.Box[1]
		row.CenterX, row.CenterY = &centerX, &centerY
		row.BoxW, row.BoxH = &boxW, &boxH
	}
	if err := a.db.SetProfilePhotoFace(probeCtx, mediaID, row); err != nil {
		a.log.Warn("face probe result persist failed", "error", err, "media_id", mediaID)
		if probeCtx.Err() != nil {
			// 预算在落库前耗尽：按“跳过”处理，调用方会交给后台重试，
			// 避免这类照片只能等外部 facebackfill 才补上。
			return face.Result{}, faceProbeSkipped
		}
		return face.Result{}, faceProbeFailed
	}
	return result, faceProbeChecked
}

// probeRegistrationPhotos 在注册照片写入 DB 后补检测。调用方必须先释放
// mediaSem；本函数只使用 faceSem，且有总时间上限，失败只记日志并留给
// facebackfill 重试，不阻断注册。
//
// 策略：
//   - 按上传顺序同步检测，确认第一张有脸后立即停止（门禁只需一张）；
//   - 预算内未检测完、或首张有脸后剩余的照片，交给后台补齐，最终会
//     checked；无脸用户也会在有界时间内检查完所有照片。
func (a *App) probeRegistrationPhotos(ctx context.Context, userID int64, mediaIDs []string) {
	if a.faceProbe == nil || len(mediaIDs) == 0 {
		return
	}
	budget := a.registrationFaceProbeBudget
	if budget <= 0 {
		budget = maxRegistrationFaceProbeBudget
	}
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()

	next := -1
	for i, mediaID := range mediaIDs {
		if probeCtx.Err() != nil {
			if next < 0 {
				next = i
			}
			break
		}
		result, outcome := a.probePhotoFace(probeCtx, userID, mediaID)
		if outcome != faceProbeChecked {
			// 预算/信号量截断，或已尝试但失败（含落库时预算耗尽）：先记下
			// 这张，继续用剩余预算找有脸照片；最终由后台从这张起补齐，避免
			// 留下只能等外部 facebackfill 的空洞。
			if next < 0 {
				next = i
			}
			continue
		}
		if result.HasFace {
			// 门禁已经满足；剩余照片转后台，避免注册响应等待全部大图。
			if next < 0 {
				next = i + 1
			}
			break
		}
	}
	if next >= 0 && next < len(mediaIDs) {
		a.startRegistrationFaceBackfill(userID, mediaIDs[next:])
	}
}

// startRegistrationFaceBackfill 在注册响应后异步检测剩余照片。它使用独立
// 于请求的 context，逐张检测并落库；某张失败不影响后续照片。进程退出时
// 未完成的照片保持 unchecked，由 facebackfill 兜底。
func (a *App) startRegistrationFaceBackfill(userID int64, mediaIDs []string) {
	if a.faceProbe == nil || len(mediaIDs) == 0 {
		return
	}
	timeout := a.cfg.FaceProbeTimeout
	if timeout <= 0 {
		timeout = face.DefaultTimeout
	}
	budget := time.Duration(len(mediaIDs))*timeout + 30*time.Second
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		for _, mediaID := range mediaIDs {
			if ctx.Err() != nil {
				return
			}
			if _, outcome := a.probePhotoFace(ctx, userID, mediaID); outcome == faceProbeSkipped {
				a.log.Warn("registration face backfill incomplete", "media_id", mediaID)
				return
			}
		}
	}()
}

func faceImageExt(contentType string) string {
	if strings.Contains(strings.ToLower(contentType), "png") {
		return ".png"
	}
	return ".jpg"
}
