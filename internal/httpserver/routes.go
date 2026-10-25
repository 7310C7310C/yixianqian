package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Handler builds the route table and middleware chain.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", a.handleHome)
	mux.HandleFunc("GET /app", a.handleLegacyApp)
	mux.HandleFunc("GET /login", a.handleLoginPage)
	mux.HandleFunc("POST /login", a.handleLogin)
	mux.HandleFunc("POST /logout", a.handleLogout)
	mux.HandleFunc("GET /activate", a.handleActivatePage)
	mux.HandleFunc("POST /activate", a.handleActivate)

	mux.HandleFunc("POST /media/upload", a.handleUploadForm)
	mux.HandleFunc("GET /media/{id}", a.handleMedia)
	mux.HandleFunc("POST /api/media", a.handleUploadAPI)

	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("/", a.handleNotFound)

	for _, register := range moduleRegistrars {
		register(a, mux)
	}

	var h http.Handler = mux
	h = a.withReadCacheEpoch(h)
	h = a.withSession(h)
	h = a.withRecover(h)
	h = a.withLogging(h)
	h = a.withClientIP(h)
	h = a.withRequestID(h)
	h = a.withSecurityHeaders(h)
	return h
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/legacy/api/") {
		// The bundled SPA's api() treats a non-JSON body as a network failure;
		// keep unknown legacy paths in the v1 {"error": "..."} envelope.
		legacyError(w, http.StatusNotFound, "找不到资源")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, apiError{
			Code:    "not_found",
			Message: "找不到资源。",
		})
		return
	}
	a.renderError(w, r, http.StatusNotFound, "找不到页面", "你造访的页面不存在。")
}

// apiError is the single JSON error shape.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{
		"error": {Code: code, Message: message},
	})
}
