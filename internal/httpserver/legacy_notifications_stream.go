package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SSE 轮询节奏：数据库每 4 秒检查一次未读数/最新通知，心跳每 15 秒一次。
// 未读数或最新通知变化时才推送；EventSource 断线由浏览器重连。
const (
	legacyNotificationStreamInterval  = 4 * time.Second
	legacyNotificationHeartbeat       = 15 * time.Second
	legacyNotificationStreamEventName = "unread"
)

type legacyNotificationStreamPayload struct {
	Unread      int    `json:"unread"`
	LatestID    int64  `json:"latest_id"`
	LatestTitle string `json:"latest_title,omitempty"`
	LatestKind  string `json:"latest_kind,omitempty"`
}

// handleLegacyNotificationsStream keeps a v1-compatible SSE connection open so
// the SPA can update its unread badge without a refresh. Each tick reads from
// PostgreSQL and only flushes when the snapshot changes; heartbeats keep proxies
// from closing the idle connection. The loop exits with the request context.
func (a *App) handleLegacyNotificationsStream(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	// logging middleware 包装了 ResponseWriter，不能直接断言 http.Flusher；
	// ResponseController 会沿 Unwrap 找到底层连接。
	rc := http.NewResponseController(w)
	// SSE 连接生命周期远超 server.WriteTimeout=120s；只对这条连接取消写超时，
	// 否则每两分钟会被切断一次。断线通过 ctx.Done() 清理。
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	snapshot := func() (legacyNotificationStreamPayload, error) {
		unread, err := a.db.UnreadNotificationCount(ctx, user.ID)
		if err != nil {
			return legacyNotificationStreamPayload{}, err
		}
		rows, err := a.db.ListNotifications(ctx, user.ID, 1)
		if err != nil {
			return legacyNotificationStreamPayload{}, err
		}
		payload := legacyNotificationStreamPayload{Unread: unread}
		if len(rows) > 0 {
			payload.LatestID = rows[0].ID
			payload.LatestTitle = rows[0].Title
			payload.LatestKind = rows[0].Kind
		}
		return payload, nil
	}
	emit := func(payload legacyNotificationStreamPayload) error {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", legacyNotificationStreamEventName, raw); err != nil {
			return err
		}
		return rc.Flush()
	}

	last, err := snapshot()
	if err != nil {
		a.logger(r).Warn("legacy notification stream snapshot failed", "error", err, "user_id", user.ID)
		return
	}
	if err := emit(last); err != nil {
		return
	}

	ticker := time.NewTicker(legacyNotificationStreamInterval)
	heartbeat := time.NewTicker(legacyNotificationHeartbeat)
	defer ticker.Stop()
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := snapshot()
			if err != nil {
				a.logger(r).Warn("legacy notification stream snapshot failed", "error", err, "user_id", user.ID)
				continue
			}
			if next == last {
				continue
			}
			if err := emit(next); err != nil {
				return
			}
			last = next
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
