package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := http.StatusOK
	overall, dbStatus := "ok", "ok"
	if err := a.db.Ping(ctx); err != nil {
		status = http.StatusServiceUnavailable
		overall, dbStatus = "degraded", "down"
		a.logger(r).Error("health check failed", "error", err)
	}

	writeJSON(w, status, map[string]string{
		"status":  overall,
		"db":      dbStatus,
		"version": platform.Version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}
