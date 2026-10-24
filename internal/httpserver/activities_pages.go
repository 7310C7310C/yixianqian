package httpserver

import (
	"errors"
	"net/http"

	"github.com/7310C7310C/yixianqian/internal/db"
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
	a.serveMediaObject(w, r, mediaObject{
		ID:         "poster-" + activityRefFromRequest(r),
		ObjectKey:  poster.ObjectKey,
		ObjectSize: poster.ByteSize,
		ObjectType: poster.ContentType,
		ThumbKey:   poster.ThumbKey,
		ThumbSize:  poster.ThumbSize,
	}, r.URL.Query().Get("size") == "thumb" && poster.ThumbKey != "")
}
