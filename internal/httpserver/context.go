package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/7310C7310C/yixianqian/internal/db"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeySession
	ctxKeyClientIP
	ctxKeyRequestID
)

func withUser(ctx context.Context, u *db.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

func withSession(ctx context.Context, s *db.Session) context.Context {
	return context.WithValue(ctx, ctxKeySession, s)
}

func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP, ip)
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// currentUser returns the authenticated user attached by session middleware.
func currentUser(r *http.Request) (db.User, bool) {
	u, ok := r.Context().Value(ctxKeyUser).(*db.User)
	if !ok || u == nil {
		return db.User{}, false
	}
	return *u, true
}

func currentSession(r *http.Request) (db.Session, bool) {
	s, ok := r.Context().Value(ctxKeySession).(*db.Session)
	if !ok || s == nil {
		return db.Session{}, false
	}
	return *s, true
}

func clientIP(r *http.Request) string {
	ip, _ := r.Context().Value(ctxKeyClientIP).(string)
	return ip
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxKeyRequestID).(string)
	return id
}

// logger returns a request-scoped logger. Never attach credentials or tokens.
func (a *App) logger(r *http.Request) *slog.Logger {
	return a.log.With("request_id", requestID(r))
}
