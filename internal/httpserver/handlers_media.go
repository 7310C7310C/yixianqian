package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

var errNoFile = errors.New("httpserver: no file field")

type mediaResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ThumbURL  string `json:"thumb_url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ByteSize  int64  `json:"byte_size"`
	CreatedAt string `json:"created_at"`
}

func (a *App) handleUploadForm(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserHTML(w, r)
	if !ok {
		return
	}
	if !a.allowUpload(w, r, user.ID, false) {
		return
	}
	if !a.acquireMedia(w, r, false) {
		return
	}
	defer a.releaseMedia()
	if err := a.parseMultipartRequest(w, r); err != nil {
		a.renderUploadParseError(w, r, err)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}

	if _, err := a.receiveUpload(r, user); err != nil {
		a.failUpload(w, r, user, err)
		return
	}
	http.Redirect(w, r, "/?uploaded=1", http.StatusSeeOther)
}

// handleUploadAPI handles JSON media uploads.
//
// 性能护栏（AGENTS.md「性能护栏」）：媒体上传/串流是热路径；缩略图磁盘缓存、
// ETag/304 与授权先于缓存都是首屏与重复查看的约束。改动必须附 make perf-bench 前后数字。
func (a *App) handleUploadAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if !a.allowUpload(w, r, user.ID, true) {
		return
	}
	if !a.acquireMedia(w, r, true) {
		return
	}
	defer a.releaseMedia()
	if err := a.parseMultipartRequest(w, r); err != nil {
		status, code, message := uploadParseErrorDetails(err)
		writeAPIError(w, status, code, message)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	saved, err := a.receiveUpload(r, user)
	if err != nil {
		status, code, message := uploadErrorDetails(err)
		if status >= 500 {
			a.logger(r).Error("upload failed", "error", err, "user_id", user.ID)
		}
		writeAPIError(w, status, code, message)
		return
	}
	writeJSON(w, http.StatusCreated, mediaResponse{
		ID:        saved.ID,
		URL:       "/media/" + saved.ID,
		ThumbURL:  "/media/" + saved.ID + "?size=thumb",
		Width:     saved.Width,
		Height:    saved.Height,
		ByteSize:  saved.ByteSize,
		CreatedAt: saved.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (a *App) parseMultipartRequest(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MediaMaxBytes+(1<<20))
	return r.ParseMultipartForm(4 << 20)
}

func uploadParseErrorDetails(err error) (status int, code, message string) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小上限。"
	}
	return http.StatusBadRequest, "invalid_multipart", "请以 multipart/form-data 重新送出。"
}

func (a *App) renderUploadParseError(w http.ResponseWriter, r *http.Request, err error) {
	status, _, message := uploadParseErrorDetails(err)
	if status == http.StatusRequestEntityTooLarge {
		http.Redirect(w, r, "/?upload_error=too_large", http.StatusSeeOther)
		return
	}
	a.renderError(w, r, status, "上传格式错误", message)
}

func (a *App) allowUpload(w http.ResponseWriter, r *http.Request, userID int64, api bool) bool {
	ok, retry := a.uploadLimiter.Allow(fmt.Sprintf("upload:user:%d", userID))
	if ok {
		return true
	}
	seconds := int(retry.Seconds()) + 1
	w.Header().Set("Retry-After", retryAfterSeconds(seconds))
	if api {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "上传过于频繁，请稍后再试。")
	} else {
		a.renderError(w, r, http.StatusTooManyRequests, "上传过于频繁", "请稍后再试。")
	}
	return false
}

// discardMedia removes a media row and its stored objects after an upload is
// rejected post-write (daily quota, attach failure). Shared by the profile and
// legacy upload handlers so the object keys are always purged together.
func (a *App) discardMedia(r *http.Request, userID int64, mediaID string) {
	m, removed, err := a.db.RemoveMediaOwned(r.Context(), userID, mediaID)
	if err != nil {
		a.logger(r).Warn("remove rejected media failed", "error", err, "user_id", userID, "media_id", mediaID)
		return
	}
	if !removed {
		return
	}
	a.removeObject(r, m.ID, m.ObjectKey)
	a.removeObject(r, m.ID, m.ThumbKey)
}

// heicOptions is shared by the photo and QR upload paths. The request context
// cancels a running converter when the client disconnects.
func (a *App) heicOptions(ctx context.Context) media.HEICOptions {
	return media.HEICOptions{
		Enabled:     a.cfg.HEICEnabled,
		ConvertPath: a.cfg.HEIFConvertPath,
		Timeout:     a.cfg.HEICTimeout,
		Context:     ctx,
	}
}

func (a *App) receiveUpload(r *http.Request, user db.User) (db.Media, error) {
	return a.receiveUploadField(r, user, "file")
}

// receiveUploadField is the shared upload pipeline; callers that embed a file
// input in a larger form can pick their own field name.
func (a *App) receiveUploadField(r *http.Request, user db.User, field string) (db.Media, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		return db.Media{}, errNoFile
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, a.cfg.MediaMaxBytes+1))
	if err != nil {
		return db.Media{}, fmt.Errorf("httpserver: read upload: %w", err)
	}
	if int64(len(data)) > a.cfg.MediaMaxBytes {
		return db.Media{}, media.ErrFileTooLarge
	}

	processed, err := media.ProcessWithLimitsHEIC(data, a.cfg.MediaThumbSize,
		a.cfg.MediaMaxDim, a.cfg.MediaMaxPixels, a.heicOptions(r.Context()))
	if err != nil {
		return db.Media{}, err
	}
	// A small but highly compressed source can expand past the limit when
	// normalized to PNG; enforce the same bound on the stored bytes.
	if int64(len(processed.Original)) > a.cfg.MediaMaxBytes {
		return db.Media{}, media.ErrFileTooLarge
	}

	id, err := platform.NewUUID()
	if err != nil {
		return db.Media{}, fmt.Errorf("httpserver: generate media id: %w", err)
	}
	ext := "jpg"
	if processed.ContentType == "image/png" {
		ext = "png"
	}
	key := fmt.Sprintf("media/%d/%s.%s", user.ID, id, ext)
	thumbKey := fmt.Sprintf("media/%d/%s_thumb.jpg", user.ID, id)
	ctx := r.Context()

	if err := a.store.Put(ctx, key, processed.Original, processed.ContentType); err != nil {
		return db.Media{}, err
	}
	if err := a.store.Put(ctx, thumbKey, processed.Thumbnail, "image/jpeg"); err != nil {
		a.removeObject(r, id, key)
		return db.Media{}, err
	}

	saved, err := a.db.CreateMedia(ctx, db.Media{
		ID:           id,
		UserID:       user.ID,
		OriginalName: sanitizeFilename(header.Filename),
		ObjectKey:    key,
		ThumbKey:     thumbKey,
		ContentType:  processed.ContentType,
		ByteSize:     int64(len(processed.Original)),
		ThumbSize:    int64(len(processed.Thumbnail)),
		Width:        processed.Width,
		Height:       processed.Height,
	})
	if err != nil {
		a.removeObject(r, id, key)
		a.removeObject(r, id, thumbKey)
		return db.Media{}, err
	}

	a.audit(r, &user.ID, "media.uploaded", "media", saved.ID, map[string]any{
		"content_type": saved.ContentType,
		"byte_size":    saved.ByteSize,
		"width":        saved.Width,
		"height":       saved.Height,
	})
	return saved, nil
}

// removeObject deletes one stored object. The R2 object key is never logged:
// only the media id may appear in logs and audit trails.
func (a *App) removeObject(r *http.Request, mediaID, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := a.store.Remove(ctx, key); err != nil {
		a.logger(r).Warn("remove media object failed", "error", err, "media_id", mediaID)
	}
	// 缩略图磁盘缓存按媒体 id 建前缀；对象删除后必须一并清理，否则删除的照片
	// 仍可能从本地缓存命中（授权已拦，但会占用配额且语义不干净）。
	a.purgeThumbs(mediaID)
}

func (a *App) failUpload(w http.ResponseWriter, r *http.Request, user db.User, err error) {
	status, code, message := uploadErrorDetails(err)
	if status >= 500 {
		a.logger(r).Error("upload failed", "error", err, "user_id", user.ID)
	}
	if status == http.StatusRequestEntityTooLarge {
		http.Redirect(w, r, "/?upload_error=too_large", http.StatusSeeOther)
		return
	}
	switch {
	case errors.Is(err, media.ErrHEIC), errors.Is(err, media.ErrHEICUnavailable),
		errors.Is(err, media.ErrHEICConvert):
		http.Redirect(w, r, "/?upload_error=heic", http.StatusSeeOther)
	case errors.Is(err, media.ErrUnsupportedImage), errors.Is(err, errNoFile):
		http.Redirect(w, r, "/?upload_error=unsupported", http.StatusSeeOther)
	case errors.Is(err, media.ErrImageDimensions):
		http.Redirect(w, r, "/?upload_error=dimensions", http.StatusSeeOther)
	default:
		a.logger(r).Warn("upload rejected", "code", code, "message", message)
		http.Redirect(w, r, "/?upload_error=server", http.StatusSeeOther)
	}
}

func uploadErrorDetails(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, media.ErrHEICUnavailable):
		return http.StatusBadRequest, "heic_unavailable", "HEIC 暂不可用，请先在相册转成 JPEG（或关闭「高效」格式）。"
	case errors.Is(err, media.ErrHEICConvert):
		return http.StatusBadRequest, "heic_conversion_failed", "HEIC 处理失败，请重试；仍失败请在相册转成 JPEG（或关闭「高效」格式）。"
	case errors.Is(err, media.ErrHEIC):
		return http.StatusBadRequest, "heic_not_supported", "HEIC 处理失败，请重试；仍失败请在相册转成 JPEG（或关闭「高效」格式）。"
	case errors.Is(err, media.ErrFileTooLarge):
		return http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小上限。"
	case errors.Is(err, media.ErrUnsupportedImage):
		return http.StatusBadRequest, "unsupported_type", "图片格式不支持，请使用 JPEG/PNG/WebP/GIF/BMP/TIFF/HEIC。"
	case errors.Is(err, media.ErrImageDimensions):
		return http.StatusBadRequest, "image_dimensions", "图片尺寸过大。"
	case errors.Is(err, errNoFile):
		return http.StatusBadRequest, "missing_file", "请选择要上传的文件。"
	default:
		return http.StatusInternalServerError, "upload_failed", "上传失败，请稍后再试。"
	}
}

func (a *App) handleMedia(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "需要登录", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if !validMediaID(id) {
		http.NotFound(w, r)
		return
	}

	m, err := a.db.GetMediaForViewer(r.Context(), id, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load media failed", "error", err, "media_id", id)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	switch r.URL.Query().Get("size") {
	case "hero":
		a.serveMediaHero(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		})
	case "thumb":
		a.serveMediaObject(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		}, true)
	default:
		a.serveMediaObject(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		}, false)
	}
}

func validMediaID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
