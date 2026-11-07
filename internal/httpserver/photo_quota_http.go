package httpserver

import (
	"context"
	"net/http"
	"time"
)

// releasePhotoQuota returns one daily-photo reservation after a failure that
// happened after TakePhotoQuota. It is shared by the SSR and legacy upload
// paths so the two cannot drift. The refund runs even if the client went away.
func (a *App) releasePhotoQuota(r *http.Request, userID int64) {
	if a.cfg.PhotoDailyLimit <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := a.db.ReleasePhotoQuota(ctx, userID, a.photoQuotaDay(a.clock.Now()), 1); err != nil {
		a.logger(r).Warn("release photo quota failed", "error", err, "user_id", userID)
	}
}
