package httpserver

import (
	"context"
	"errors"
	"hash/fnv"
	"io/fs"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
	webassets "github.com/7310C7310C/yixianqian/web"
)

func (a *App) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = platform.NewRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}

func (a *App) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", securityPolicyWithQuery(r.URL.Path, r.URL.Query()))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", frameOptionsWithQuery(r.URL.Path, r.URL.Query()))
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if a.cfg.SessionCookieSecure {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// securityPolicy relaxes inline script/style and eval only for the bundled v1
// SPA, whose Vue global build compiles in-DOM templates with new Function.
// base-uri must allow its <base> tag; every other route keeps the strict policy.
func securityPolicy(path string) string {
	return securityPolicyWithQuery(path, nil)
}

func securityPolicyWithQuery(path string, query url.Values) string {
	frameAncestors := "'none'"
	if embeddablePage(path, query) {
		// 账号安全页、管理台与活动管理 native SPA 由用户端 SPA 以同源 iframe 内嵌；
		// 只放宽 framing，其他 CSP 指令保持严格。
		frameAncestors = "'self'"
	}
	if path == "/" || path == "/app" {
		return "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors " + frameAncestors + "; " +
			"img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; " +
			"script-src 'self' 'unsafe-inline' 'unsafe-eval'; object-src 'none'"
	}
	return "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors " + frameAncestors + "; " +
		"img-src 'self' data:; style-src 'self'; font-src 'self' data:; script-src 'self'; object-src 'none'"
}

// embeddablePage lists server-rendered pages that the user SPA opens in a
// same-origin iframe. Keeping the allowlist narrow avoids weakening clickjacking
// protection for the rest of the app.
//
// 活动管理按 PM 决定复用 v1 原版 SPA：管理台 iframe 内的
// /app?native_admin=1&p=group_admin 只对同源 iframe 放宽 framing；
// 普通 / 与 /app 仍保持 frame-ancestors 'none' / X-Frame-Options: DENY。
func embeddablePage(path string, query url.Values) bool {
	if path == "/password" || path == "/admin" || strings.HasPrefix(path, "/admin/") {
		return true
	}
	return path == "/app" && query.Get("native_admin") == "1"
}

func frameOptions(path string) string {
	return frameOptionsWithQuery(path, nil)
}

func frameOptionsWithQuery(path string, query url.Values) string {
	if embeddablePage(path, query) {
		return "SAMEORIGIN"
	}
	return "DENY"
}

func (a *App) withClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := platform.ClientIP(r.RemoteAddr, r.Header, a.trusted)
		next.ServeHTTP(w, r.WithContext(withClientIP(r.Context(), ip)))
	})
}

func (a *App) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		log := a.logger(r).With(
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r),
		)
		switch {
		case status >= 500:
			log.Error("request")
		case status >= 400:
			log.Warn("request")
		default:
			log.Info("request")
		}
	})
}

func (a *App) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.logger(r).Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/static/") || path == "/healthz" || path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		cookie, err := r.Cookie(a.cfg.SessionCookieName)
		if err == nil && cookie.Value != "" {
			sess, user, err := a.db.GetSession(ctx, auth.HashToken(cookie.Value))
			switch {
			case err == nil:
				ctx = withSession(ctx, &sess)
				ctx = withUser(ctx, &user)

				touchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				if err := a.db.TouchSession(touchCtx, sess.ID, 5*time.Minute); err != nil {
					a.logger(r).Debug("touch session failed", "error", err)
				}
				cancel()

				if sess.MustChangePassword && !passwordChangePathAllowed(path) {
					if strings.HasPrefix(path, "/api/") {
						writeAPIError(w, http.StatusForbidden, "password_change_required",
							"请先修改临时密码。")
						return
					}
					http.Redirect(w, r, "/password?required=1", http.StatusSeeOther)
					return
				}
				if a.registrationGate(w, r, user) {
					return
				}
				if a.idCardGate(w, r, user) {
					return
				}
			case errors.Is(err, db.ErrNotFound):
				a.clearSessionCookie(w)
			default:
				a.logger(r).Error("load session failed", "error", err)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// passwordChangePathAllowed lists the only destinations reachable while a
// temporary password still has to be replaced.
func passwordChangePathAllowed(path string) bool {
	switch path {
	case "/password", "/logout", "/healthz", "/favicon.ico", "/legacy/api/password":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

func sanitizeRequestID(raw string) string {
	if raw == "" || len(raw) > 64 {
		return ""
	}
	for _, c := range raw {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return ""
	}
	return raw
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Versioned URLs (assetVersion query) are immutable for a year; bare
		// paths keep the previous one-hour policy so old links still update.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		etag := staticETag(r.URL.Path)
		w.Header().Set("ETag", etag)
		if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// staticETag is stable for one build: the assetVersion changes whenever the
// embedded files change, and the path hash keeps different files distinct.
func staticETag(path string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return `"av` + assetVersion + `-` + strconv.FormatUint(uint64(h.Sum32()), 16) + `"`
}
