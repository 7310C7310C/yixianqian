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

	w.Header().Set("Cache-Control", "no-store")
	body := map[string]string{
		"status": overall,
		"db":     dbStatus,
	}
	// 版本与时间只给本机直连（自动部署/运维探针）。公网请求经 nginx 一定带
	// X-Real-IP；不把构建指纹和服务器时间免费交给扫描器。
	if r.Header.Get("X-Real-IP") == "" {
		body["version"] = platform.Version
		body["time"] = time.Now().UTC().Format(time.RFC3339)
	}
	writeJSON(w, status, body)
}
