package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

// mediaObject is the storage metadata needed to stream one media row. All
// fields come from the database row the caller already authorises; the object
// key is never exposed to the client.
type mediaObject struct {
	ID         string
	ObjectKey  string
	ObjectSize int64
	ObjectType string
	ThumbKey   string
	ThumbSize  int64
	Width      int
}

// serveMediaObject streams original or thumbnail bytes with a long private
// cache and an ETag. Authorisation must already have happened in the caller;
// this helper only handles storage, conditional requests and headers.
func (a *App) serveMediaObject(w http.ResponseWriter, r *http.Request, m mediaObject, thumb bool) {
	key, size, contentType, variant := m.ObjectKey, m.ObjectSize, m.ObjectType, "orig"
	maxAge := time.Hour
	if thumb {
		key, size, contentType, variant = m.ThumbKey, m.ThumbSize, "image/jpeg", "thumb"
		maxAge = 24 * time.Hour
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	etag := `"` + m.ID + "-" + variant + `"`

	// 缩略图走有界磁盘缓存（授权已在上游完成）：命中直接回字节，未命中读 R2
	// 后落盘。原图不缓存（体积大、流量低）。
	if thumb && a.thumbCache != nil {
		cacheKey := media.ThumbCacheKey(m.ID, m.ThumbKey, m.ThumbSize)
		if data, ok := a.thumbCache.Get(cacheKey); ok {
			a.thumbCacheHits.Add(1)
			writeMediaBytes(w, r, etag, contentType, int64(len(data)), data)
			return
		}
		a.thumbCacheMisses.Add(1)
		data, err := a.readStoreObject(w, r, m, key, size)
		if err != nil {
			return
		}
		if err := a.thumbCache.Put(cacheKey, data); err != nil {
			a.logger(r).Warn("thumb cache write failed", "error", err, "media_id", m.ID)
		}
		writeMediaBytes(w, r, etag, contentType, int64(len(data)), data)
		return
	}

	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		mediaFailure(w, r, http.StatusNotFound, "")
		return
	}
	if err != nil {
		a.logger(r).Error("open media object failed", "error", err, "media_id", m.ID)
		mediaFailure(w, r, http.StatusInternalServerError, "internal server error")
		return
	}
	defer obj.Close()

	// GetObject is lazy (single GET). Read the first chunk before writing
	// headers so a missing object can still return 404 and the old extra HEAD
	// request disappears.
	first := int64(32 << 10)
	if size > 0 && size < first {
		first = size
	}
	buf := make([]byte, first)
	n, readErr := io.ReadFull(obj, buf)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		if media.IsObjectNotFound(readErr) {
			mediaFailure(w, r, http.StatusNotFound, "")
			return
		}
		a.logger(r).Error("open media object failed", "error", readErr, "media_id", m.ID)
		mediaFailure(w, r, http.StatusInternalServerError, "internal server error")
		return
	}
	buf = buf[:n]

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(int64(maxAge.Seconds()), 10)+", immutable")
	if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	if n > 0 {
		_, _ = w.Write(buf)
	}
	if remaining := size - int64(n); remaining > 0 {
		if _, err := io.CopyN(w, obj, remaining); err != nil {
			a.logger(r).Debug("stream media interrupted", "error", err, "media_id", m.ID)
		}
	}
}

// serveMediaHero serves a small hero-only JPEG for the first-screen card.
// It is derived from the already-authorized thumbnail, cached separately from
// ?size=thumb, and keeps the same private/immutable semantics.
// serveThumbRequest serves ?size=thumb with an optional width variant.
// ?size=thumb (no w) keeps the legacy 512px thumbnail; w=768/1024/1280 serves
// the pre-generated variant when present and falls back to 512 otherwise.
func (a *App) serveThumbRequest(w http.ResponseWriter, r *http.Request, m mediaObject) {
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	width = effectiveThumbWidth(m.Width, width)
	switch width {
	case 768, 1024, 1280:
		a.serveThumbVariant(w, r, m, width)
	default:
		a.serveMediaObject(w, r, m, true)
	}
}

// effectiveThumbWidth caps a requested variant to the source's useful width.
// A 480px avatar asking for 1280 should serve 512 immediately instead of doing
// three R2 404 probes; a 736px source maps to 768; a 1080px source to 1024.
func effectiveThumbWidth(original, requested int) int {
	if original <= 0 || requested <= 512 {
		return requested
	}
	maxUseful := original * 115 / 100
	if requested <= maxUseful {
		return requested
	}
	switch {
	case maxUseful >= 1024:
		return 1024
	case maxUseful >= 768:
		return 768
	default:
		return 512
	}
}

func thumbVariantKey(base string, width int) string {
	if base == "" || width <= 0 {
		return base
	}
	dot := strings.LastIndex(base, ".")
	suffix := "_" + strconv.Itoa(width)
	if dot < 0 {
		return base + suffix
	}
	return base[:dot] + suffix + base[dot:]
}

// serveThumbVariant streams a pre-generated width variant. Variants live next
// to the base thumb: <name>_thumb_768.jpg etc. Missing variants fall back to the
// 512 thumb so old/incomplete backfills never break a card image.
func (a *App) serveThumbVariant(w http.ResponseWriter, r *http.Request, m mediaObject, width int) {
	// Try the requested width, then smaller variants. This keeps a missing
	// 1280 variant from falling all the way back to 512 on a DPR3 phone.
	for _, candidate := range []int{width, 1024, 768} {
		if candidate > width {
			continue
		}
		key := thumbVariantKey(m.ThumbKey, candidate)
		if key == "" || key == m.ThumbKey {
			continue
		}
		etag := `"` + m.ID + "-thumb-" + strconv.Itoa(candidate) + `"`
		cacheKey := media.ThumbCacheKey(m.ID, key, 0)
		if a.thumbCache != nil {
			if data, ok := a.thumbCache.Get(cacheKey); ok {
				a.thumbCacheHits.Add(1)
				writeMediaBytes(w, r, etag, "image/jpeg", int64(len(data)), data)
				return
			}
			a.thumbCacheMisses.Add(1)
		}
		obj, err := a.store.Get(r.Context(), key)
		if errors.Is(err, media.ErrObjectNotFound) {
			continue
		}
		if err != nil {
			a.logger(r).Error("open thumb variant failed", "error", err, "media_id", m.ID, "width", candidate)
			mediaFailure(w, r, http.StatusInternalServerError, "internal server error")
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(obj, 4<<20))
		_ = obj.Close()
		if readErr != nil || len(data) == 0 {
			continue
		}
		if a.thumbCache != nil {
			if err := a.thumbCache.Put(cacheKey, data); err != nil {
				a.logger(r).Warn("thumb variant cache write failed", "error", err, "media_id", m.ID, "width", candidate)
			}
		}
		writeMediaBytes(w, r, etag, "image/jpeg", int64(len(data)), data)
		return
	}
	a.serveMediaObject(w, r, m, true)
}

var thumbVariantWidths = []int{768, 1024, 1280}

// putThumbVariants is best-effort: card images fall back to 512 when a
// variant is absent, so a variant upload failure must not fail the photo save.
func (a *App) putThumbVariants(ctx context.Context, original []byte, contentType, thumbKey string, originalWidth int) {
	if contentType != "image/jpeg" || thumbKey == "" {
		return
	}
	for _, width := range thumbVariantWidths {
		// Allow a small upscale (<=15%) for near-miss sources such as 736px;
		// skip large fake upscales (480 -> 768/1024/1280).
		if originalWidth > 0 && width*100 > originalWidth*115 {
			continue
		}
		data, err := media.ResizeWidthJPEG(original, width, 82)
		if err != nil {
			a.log.Warn("thumb variant encode failed", "error", err, "width", width)
			continue
		}
		if err := a.store.Put(ctx, thumbVariantKey(thumbKey, width), data, "image/jpeg"); err != nil {
			a.log.Warn("thumb variant upload failed", "error", err, "width", width)
		}
	}
}

func (a *App) serveMediaHero(w http.ResponseWriter, r *http.Request, m mediaObject) {
	if m.ThumbKey == "" || m.ThumbSize <= 0 {
		a.serveMediaObject(w, r, m, true)
		return
	}
	etag := `"` + m.ID + "-hero" + `"`
	cacheKey := media.ThumbCacheKey(m.ID, m.ThumbKey+"|hero", m.ThumbSize)
	if a.thumbCache != nil {
		if data, ok := a.thumbCache.Get(cacheKey); ok {
			a.thumbCacheHits.Add(1)
			writeMediaBytes(w, r, etag, "image/jpeg", int64(len(data)), data)
			return
		}
		a.thumbCacheMisses.Add(1)
	}
	data, err := a.readStoreObject(w, r, m, m.ThumbKey, m.ThumbSize)
	if err != nil {
		return
	}
	hero, err := media.HeroJPEG(data, 420, 70)
	resized := err == nil
	if err != nil {
		a.logger(r).Warn("hero thumbnail resize failed", "error", err, "media_id", m.ID)
		hero = data
	}
	if resized && a.thumbCache != nil {
		if err := a.thumbCache.Put(cacheKey, hero); err != nil {
			a.logger(r).Warn("hero cache write failed", "error", err, "media_id", m.ID)
		}
	}
	writeMediaBytes(w, r, etag, "image/jpeg", int64(len(hero)), hero)
}

// activityPosterObject derives the cache identity from the poster media row, so
// replacing the poster changes the ETag (URLs are stable).
func activityPosterObject(p db.ActivityPoster) mediaObject {
	return mediaObject{
		ID:         "poster-" + p.MediaID,
		ObjectKey:  p.ObjectKey,
		ObjectSize: p.ByteSize,
		ObjectType: p.ContentType,
		ThumbKey:   p.ThumbKey,
		ThumbSize:  p.ThumbSize,
	}
}

// mediaFailure writes an uncacheable error response. Long-lived ETag/Cache
// headers must never survive a 404/500 (browsers would cache the failure).
func mediaFailure(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Del("ETag")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	http.Error(w, message, status)
}

// mediaETagMatches implements the If-None-Match comparison used by our media
// endpoints: weak prefix and surrounding spaces are ignored; "*" matches all.
func mediaETagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	for _, raw := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(raw)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// readStoreObject 读取整个对象到内存并处理 404/500，供缩略图缓存路径使用。
// 返回 nil 表示已经写过错误响应。
func (a *App) readStoreObject(w http.ResponseWriter, r *http.Request, m mediaObject, key string, size int64) ([]byte, error) {
	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		mediaFailure(w, r, http.StatusNotFound, "")
		return nil, err
	}
	if err != nil {
		a.logger(r).Error("open media object failed", "error", err, "media_id", m.ID)
		mediaFailure(w, r, http.StatusInternalServerError, "internal server error")
		return nil, err
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, size+1))
	if err != nil {
		if media.IsObjectNotFound(err) {
			mediaFailure(w, r, http.StatusNotFound, "")
			return nil, err
		}
		a.logger(r).Error("open media object failed", "error", err, "media_id", m.ID)
		mediaFailure(w, r, http.StatusInternalServerError, "internal server error")
		return nil, err
	}
	if size > 0 && int64(len(data)) > size {
		data = data[:size]
	}
	return data, nil
}

// writeMediaBytes 用与流式路径完全相同的头语义回写内存字节。
func writeMediaBytes(w http.ResponseWriter, r *http.Request, etag, contentType string, size int64, data []byte) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// purgeThumbs 在媒体删除后清理其缩略图缓存（缓存 key 含 object key，替换照片
// 会自然失效；删除需要显式清理）。
func (a *App) purgeThumbs(mediaID string) {
	if a.thumbCache == nil || mediaID == "" {
		return
	}
	a.thumbCache.RemoveMedia(mediaID)
	// 活动海报的缓存 key 是 poster-<mediaID>__…（ID 前缀不同），一并清理。
	a.thumbCache.RemoveMedia("poster-" + mediaID)
}
