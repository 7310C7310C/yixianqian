package httpserver

import (
	"net/http"
	"strconv"
)

func init() { registerModule(registerLegacyLogout) }

func registerLegacyLogout(a *App, mux *http.ServeMux) {
	mux.HandleFunc("POST /legacy/api/logout", a.handleLegacyLogout)
}

// handleLegacyLogout mirrors the v1 SPA's session logout. The old client sends
// X-CSRF-Token from the yxq_csrf cookie, then navigates to the Go login page.
func (a *App) handleLegacyLogout(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if sess, ok := currentSession(r); ok {
		if err := a.db.RevokeSessionByID(r.Context(), sess.ID); err != nil {
			a.logger(r).Error("revoke legacy session failed", "error", err, "user_id", user.ID)
		}
	}
	a.audit(r, &user.ID, "auth.logout", "user", strconv.FormatInt(user.ID, 10), nil)
	a.clearSessionCookie(w)
	a.clearCSRFCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
