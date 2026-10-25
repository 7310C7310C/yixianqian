package httpserver

import (
	"context"
	"errors"
	"hash/fnv"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
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
				// 15 分钟节流：last_seen_at 只用于在线状态，写放大比秒级精度更重要。
				// SQL 内的条件保证多实例/并发请求下仍最多每 15 分钟更新一次。
				if err := a.db.TouchSession(touchCtx, sess.ID, 15*time.Minute); err != nil {
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

type staticAsset struct {
	gz []byte
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	assets := make(map[string]staticAsset)
	_ = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || strings.HasSuffix(path, ".gz") {
			return nil
		}
		data, readErr := fs.ReadFile(sub, path+".gz")
		if readErr == nil {
			assets[path] = staticAsset{gz: data}
		}
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".gz") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/static/")
		versioned := r.URL.Query().Get("v") == assetVersion
		if versioned {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		asset, hasGzip := assets[path]
		useGzip := hasGzip && acceptsGzip(r.Header.Get("Accept-Encoding"))
		encoding := "identity"
		if useGzip {
			encoding = "gzip"
			w.Header().Set("Vary", "Accept-Encoding")
		} else if hasGzip {
			w.Header().Set("Vary", "Accept-Encoding")
		}
		etag := staticETag(path, encoding)
		w.Header().Set("ETag", etag)
		if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if useGzip {
			if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(asset.gz)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(asset.gz)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// staticETag is stable for one build: assetVersion changes with embedded files,
// the path hash separates files, and the encoding separates gzip/identity.
func staticETag(path, encoding string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return `"av` + assetVersion + `-` + strconv.FormatUint(uint64(h.Sum32()), 16) + `-` + encoding + `"`
}

// acceptsGzip reports whether the Accept-Encoding header allows gzip.
//
// The rule is deliberately order-independent and shared with the edge nginx
// map: every gzip token must accept it, and a single explicit refusal
// (q=0, q=0.000, a malformed q, or a negative q) wins. Only the exact "gzip"
// token counts, so legacy aliases such as "x-gzip" stay identity — the relay
// normalises the header it forwards ($yxq_upstream_ae), which keeps both
// layers deciding identically.
func acceptsGzip(header string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		quality := 1.0
		for _, param := range fields[1:] {
			key, value, found := strings.Cut(strings.TrimSpace(param), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return false
			}
			quality = parsed
			break
		}
		if quality <= 0 {
			return false
		}
		accepted = true
	}
	return accepted
}

// withReadCacheEpoch 在任何**成功的非 GET** `/legacy/api/*` 请求后递增读缓存世代号。
// 这样所有用户的 home/cards 缓存立即失效（不必逐用户追踪），GET/错误/4xx/5xx 不 bump。
func (a *App) withReadCacheEpoch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead ||
			!strings.HasPrefix(r.URL.Path, "/legacy/api/") {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 200 && status < 400 {
			a.bumpReadCacheEpoch()
		}
	})
}
