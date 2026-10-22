package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

func (a *App) handleActivityPoster(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "需要登录", http.StatusUnauthorized)
		return
	}
	if !a.requireActivityViewHTML(w, r, user) {
		return
	}
	poster, err := a.db.GetActivityPoster(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load activity poster failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	key, size, contentType := poster.ObjectKey, poster.ByteSize, poster.ContentType
	if r.URL.Query().Get("size") == "thumb" && poster.ThumbKey != "" {
		key, size, contentType = poster.ThumbKey, poster.ThumbSize, "image/jpeg"
	}
	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("open activity poster failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer obj.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, obj, size); err != nil {
		a.logger(r).Debug("stream activity poster interrupted", "error", err)
	}
}
