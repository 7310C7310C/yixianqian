package httpserver

import (
	"context"
	"net/http"
	"strings"
)

// adminAccessContextKey carries the authenticated admin's permissions to the
// renderer so shared navigation can hide entries the role cannot use. A
// package-private struct key avoids collisions with middleware context values.
type adminAccessContextKey struct{}

func withAdminAccess(ctx context.Context, access adminAccess) context.Context {
	return context.WithValue(ctx, adminAccessContextKey{}, access)
}

func adminAccessFromContext(ctx context.Context) (adminAccess, bool) {
	access, ok := ctx.Value(adminAccessContextKey{}).(adminAccess)
	return access, ok
}

// render writes a complete HTML page. Output is buffered first; template
// failures become a plain 500 without leaking internals.
func (a *App) render(w http.ResponseWriter, r *http.Request, status int, page string, data PageData) {
	if access, ok := adminAccessFromContext(r.Context()); ok {
		data.CanPoints = containsString(access.Permissions, adminPermPointsAdjust)
		data.CanAudit = containsString(access.Permissions, adminPermAudit)
	}
	data.ActiveNav = canonicalNav(data.ActiveNav)
	if data.ActiveNav == "" {
		data.ActiveNav = navForPage(page)
	}
	if data.FeishuURL == "" {
		data.FeishuURL = publicFeishuAppURL
	}
	data.CSRFToken = a.ensureCSRF(w, r)
	body := a.renderPage(w, page, data)
	if body == nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		a.logger(r).Debug("write html response failed", "error", err)
	}
}

func (a *App) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	// 管理台在用户端同源 iframe 内使用：错误页若落到用户端模板，唯一的
	// 「回到首页」链接会被 frame-ancestors 'none' 拒绝，iframe 变空白。
	// 管理路径统一渲染管理台外壳，提供「返回管理台 / 重新登录」。
	if isAdminPagePath(r.URL.Path) {
		a.renderAdminError(w, r, status, title, message)
		return
	}
	a.render(w, r, status, "error", PageData{
		Title: title,
		Error: message,
	})
}

// isAdminPagePath reports whether the request belongs to the admin console.
func isAdminPagePath(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/")
}

// adminErrorData carries error-page state to the admin_error template.
type adminErrorData struct {
	Relogin bool
}

// renderAdminError renders an error page inside the admin shell so iframe
// users keep navigation and never hit a user-facing page that refuses framing.
func (a *App) renderAdminError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	data := PageData{
		Title:     title,
		Error:     message,
		Data:      adminErrorData{Relogin: status == http.StatusUnauthorized},
		PageCSS:   "admin.css",
		ActiveNav: "admin",
	}
	if user, ok := currentUser(r); ok {
		data.User = &user
	}
	a.render(w, r, status, "admin_error", data)
}

// canonicalNav keeps the admin navigation names used by admin_nav.tmpl.
func canonicalNav(name string) string {
	switch name {
	case "admin", "users", "activities", "matching", "points", "reports", "suggestions", "roles", "audit", "admin-site-assets":
		return name
	default:
		return ""
	}
}

// navForPage maps an SSR page template to its admin navigation entry.
func navForPage(page string) string {
	switch page {
	case "admin":
		return "admin"
	case "admin_users", "admin_user_detail":
		return "users"
	case "admin_activities", "admin_activity_edit", "admin_activity_detail":
		return "activities"
	case "admin_matching", "admin_matching_group", "admin_matching_onetoone":
		return "matching"
	case "admin_points":
		return "points"
	case "admin_reports":
		return "reports"
	case "admin_suggestions":
		return "suggestions"
	case "admin_roles":
		return "roles"
	case "admin_audit":
		return "audit"
	case "admin_site_assets":
		return "admin-site-assets"
	default:
		return ""
	}
}
