package httpserver

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
)

func (a *App) sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     a.cfg.SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *App) setSessionCookie(w http.ResponseWriter, raw string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge <= 0 {
		maxAge = int(a.cfg.SessionTTL.Seconds())
	}
	http.SetCookie(w, a.sessionCookie(raw, expires, maxAge))
}

func (a *App) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, a.sessionCookie("", time.Unix(1, 0), -1))
}

func (a *App) clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   a.cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ensureCSRF returns a valid signed token and sets the double-submit cookie.
func (a *App) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(auth.CSRFCookieName); err == nil && a.csrf.Verify(c.Value) {
		return c.Value
	}
	token, err := a.csrf.Issue()
	if err != nil {
		a.logger(r).Error("issue csrf token failed", "error", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(a.csrf.TTL().Seconds()),
		HttpOnly: false,
		Secure:   a.cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

// rotateCSRF issues a fresh token, used after privilege changes (login).
func (a *App) rotateCSRF(w http.ResponseWriter, r *http.Request) {
	token, err := a.csrf.Issue()
	if err != nil {
		a.logger(r).Error("rotate csrf token failed", "error", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(a.csrf.TTL().Seconds()),
		HttpOnly: false,
		Secure:   a.cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// checkCSRF enforces the signed-cookie + submitted-token pair. The header path
// is the only accepted source for JSON API writes.
func (a *App) checkCSRF(r *http.Request, submitted string) bool {
	if submitted == "" {
		return false
	}
	c, err := r.Cookie(auth.CSRFCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(submitted)) != 1 {
		return false
	}
	return a.csrf.Verify(c.Value)
}

func (a *App) formCSRFValid(r *http.Request) bool {
	return a.checkCSRF(r, r.PostFormValue(auth.CSRFFormField))
}

func (a *App) headerCSRFValid(r *http.Request) bool {
	return a.checkCSRF(r, r.Header.Get(auth.CSRFHeader))
}
