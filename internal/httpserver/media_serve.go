package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
}

// serveMediaObject streams original or thumbnail bytes with a long private
// cache and an ETag. Authorisation must already have happened in the caller;
// this helper only handles storage, conditional requests and headers.
func (a *App) serveMediaObject(w http.ResponseWriter, r *http.Request, m mediaObject, thumb bool) {
	key, size, contentType, variant := m.ObjectKey, m.ObjectSize, m.ObjectType, "orig"
	maxAge := 24 * time.Hour
	if thumb {
		key, size, contentType, variant = m.ThumbKey, m.ThumbSize, "image/jpeg", "thumb"
		maxAge = 7 * 24 * time.Hour
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	etag := `"` + m.ID + "-" + variant + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(int64(maxAge.Seconds()), 10)+", immutable")
	if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("open media object failed", "error", err, "media_id", m.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
			http.NotFound(w, r)
			return
		}
		a.logger(r).Error("open media object failed", "error", readErr, "media_id", m.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	buf = buf[:n]

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
