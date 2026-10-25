package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SSE 推送节奏：通知写入通过 pg_notify 即时唤醒；30 秒兜底 ticker 只覆盖
// 监听连接重建/丢失通知的窗口，15 秒心跳让代理保持连接。没有事件时不做
// 任何业务查询（LISTEN 连接本身不查表）。
const (
	legacyNotificationHeartbeat            = 15 * time.Second
	legacyNotificationFallbackConnected    = 30 * time.Second
	legacyNotificationFallbackDisconnected = 15 * time.Second
	legacyNotificationStreamEventName      = "unread"
)

type legacyNotificationStreamPayload struct {
	Unread      int    `json:"unread"`
	LatestID    int64  `json:"latest_id"`
	LatestTitle string `json:"latest_title,omitempty"`
	LatestKind  string `json:"latest_kind,omitempty"`
}

// handleLegacyNotificationsStream keeps a v1-compatible SSE connection open so
// the SPA can update its unread badge without a refresh. PostgreSQL LISTEN/
// NOTIFY wakes the handler only when the owner's notifications change; a slow
// fallback ticker covers reconnects, and heartbeats keep proxies from closing
// the idle connection. The loop exits with the request context.
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

	// Subscribe before the first snapshot: a notification committed between the
	// snapshot and the loop would otherwise be invisible until the ticker.
	var notifications <-chan struct{}
	if a.notifier != nil {
		ch, cancel := a.notifier.Subscribe(user.ID)
		defer cancel()
		notifications = ch
	}

	last, err := snapshot()
	if err != nil {
		a.logger(r).Warn("legacy notification stream snapshot failed", "error", err, "user_id", user.ID)
		return
	}
	if err := emit(last); err != nil {
		return
	}

	fallback := legacyNotificationFallbackDisconnected
	if a.notifier != nil && a.notifier.Connected() {
		fallback = legacyNotificationFallbackConnected
	}
	ticker := time.NewTicker(fallback)
	heartbeat := time.NewTicker(legacyNotificationHeartbeat)
	defer ticker.Stop()
	defer heartbeat.Stop()

	refresh := func() bool {
		next, err := snapshot()
		if err != nil {
			a.logger(r).Warn("legacy notification stream snapshot failed", "error", err, "user_id", user.ID)
			return true
		}
		if next == last {
			return true
		}
		if err := emit(next); err != nil {
			return false
		}
		last = next
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-notifications:
			if !refresh() {
				return
			}
		case <-ticker.C:
			if !refresh() {
				return
			}
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
