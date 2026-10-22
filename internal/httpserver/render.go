package httpserver

import (
	"net/http"
)

// render writes a complete HTML page. Output is buffered first; template
// failures become a plain 500 without leaking internals.
func (a *App) render(w http.ResponseWriter, r *http.Request, status int, page string, data PageData) {
	data.ActiveNav = canonicalNav(data.ActiveNav)
	if data.ActiveNav == "" {
		data.ActiveNav = navForPage(page)
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
	a.render(w, r, status, "error", PageData{
		Title: title,
		Error: message,
	})
}

// canonicalNav keeps the admin navigation names used by admin_nav.tmpl.
func canonicalNav(name string) string {
	switch name {
	case "admin", "users", "activities", "matching", "reports", "suggestions", "roles":
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
	case "admin_users":
		return "users"
	case "admin_activities", "admin_activity_edit", "admin_activity_detail":
		return "activities"
	case "admin_matching", "admin_matching_group", "admin_matching_onetoone":
		return "matching"
	case "admin_reports":
		return "reports"
	case "admin_suggestions":
		return "suggestions"
	case "admin_roles":
		return "roles"
	default:
		return ""
	}
}
