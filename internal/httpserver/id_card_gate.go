package httpserver

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const idCardRequiredMessage = "身份证信息缺失或有误，请先在「修改资料」补填身份证号。"

// idCardGate is the server-side P0 identity gate. Observers bypass has_face (in
// the profile projection) but never the ID card rule (PM 2026-10-22).
//
// Allowed surfaces: home, profile (including photo edits), notifications,
// password change, logout, version/client telemetry, and the mutual-page
// wechat form. Everything else under /legacy/api/ plus the /app and /admin page
// entries returns 403 until id_card_valid becomes true.
func (a *App) idCardGate(w http.ResponseWriter, r *http.Request, user db.User) bool {
	// The middleware runs outside ServeMux, so canonicalise before matching an
	// allowlist prefix: /legacy/api/notifications/../cards must not look like a
	// notifications request here.
	requestPath := path.Clean(r.URL.Path)
	if !idCardGateApplies(requestPath) || idCardGateAllowed(requestPath) {
		return false
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		return false
	}
	if err != nil {
		a.logger(r).Error("id card gate lookup failed", "error", err, "user_id", user.ID)
		return false
	}
	// Observers are exempt from has_face, never from the ID card rule
	// (PM 2026-10-22). Do not add an observer bypass here.
	if viewer.IDCardValid {
		return false
	}
	w.Header().Set("X-Error-Code", "id_card_required")
	if strings.HasPrefix(requestPath, "/legacy/api/") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": idCardRequiredMessage,
			"code":  "id_card_required",
		})
		return true
	}
	a.renderError(w, r, http.StatusForbidden, "需要补填身份证", idCardRequiredMessage)
	return true
}

func idCardGateApplies(path string) bool {
	return strings.HasPrefix(path, "/legacy/api/") || path == "/app" ||
		path == "/admin" || strings.HasPrefix(path, "/admin/")
}

func idCardGateAllowed(path string) bool {
	switch path {
	case "/legacy/api/home",
		"/legacy/api/version",
		"/legacy/api/client-events",
		"/legacy/api/profile",
		"/legacy/api/profile/photo",
		"/legacy/api/profile/photo/cover",
		"/legacy/api/user/me",
		"/legacy/api/password",
		"/legacy/api/logout",
		"/legacy/api/mutual/wechat",
		"/legacy/api/notifications":
		return true
	}
	return strings.HasPrefix(path, "/legacy/api/notifications/")
}
