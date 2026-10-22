package httpserver

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
)

// probeProfilePhotoFace 在照片上传成功后同步运行本地人脸检测，并把结果写入
// profile_photos。任何失败都只记日志并放行上传；此时 face_checked 保持
// false，由 cmd/facebackfill 或下次重新检测补齐。
//
// 日志只带 media_id，不输出图片临时路径、R2 对象键或任何凭证。
func (a *App) probeProfilePhotoFace(ctx context.Context, userID int64, mediaID string) {
	if a.faceProbe == nil {
		return
	}
	media, err := a.db.GetMediaForUser(ctx, mediaID, userID)
	if err != nil {
		a.log.Warn("face probe media lookup failed", "error", err, "media_id", mediaID)
		return
	}

	timeout := a.cfg.FaceProbeTimeout
	if timeout <= 0 {
		timeout = face.DefaultTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	obj, err := a.store.Get(probeCtx, media.ObjectKey)
	if err != nil {
		// media 包的错误含对象键，故意不记录原始错误。
		a.log.Warn("face probe object unavailable", "media_id", mediaID)
		return
	}
	defer obj.Close()

	limit := a.cfg.MediaMaxBytes
	if limit <= 0 {
		limit = 16 << 20
	}
	tmp, err := os.CreateTemp("", "yxq-face-*"+faceImageExt(media.ContentType))
	if err != nil {
		a.log.Warn("face probe temp file failed", "media_id", mediaID)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, copyErr := io.Copy(tmp, io.LimitReader(obj, limit+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > limit {
		a.log.Warn("face probe image copy failed", "media_id", mediaID)
		return
	}

	result, err := a.faceProbe.Probe(probeCtx, tmpName)
	if err != nil {
		a.log.Warn("face probe failed", "error", err, "media_id", mediaID)
		return
	}

	row := db.ProfilePhotoFace{MediaID: mediaID, Checked: true, HasFace: result.HasFace}
	if len(result.Center) == 2 && len(result.Box) == 2 {
		centerX, centerY := result.Center[0], result.Center[1]
		boxW, boxH := result.Box[0], result.Box[1]
		row.CenterX, row.CenterY = &centerX, &centerY
		row.BoxW, row.BoxH = &boxW, &boxH
	}
	if err := a.db.SetProfilePhotoFace(ctx, mediaID, row); err != nil {
		a.log.Warn("face probe result persist failed", "error", err, "media_id", mediaID)
	}
}

func faceImageExt(contentType string) string {
	if strings.Contains(strings.ToLower(contentType), "png") {
		return ".png"
	}
	return ".jpg"
}
