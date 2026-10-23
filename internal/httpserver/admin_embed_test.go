package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 独立的 SSR 管理台入口已废弃：浏览器顶层 document 导航一律 303 回 SPA；
// iframe 与测试/API（无 Sec-Fetch-Dest）保持旧行为。
func TestAdminHTMLRedirectsTopLevelDocument(t *testing.T) {
	app := &App{}
	handler := app.adminHTML("", func(w http.ResponseWriter, _ *http.Request, _ adminAccess) {
		w.WriteHeader(http.StatusOK)
	})

	documentCases := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/admin"},
		{http.MethodGet, "/admin/users?status=pending"},
		{http.MethodGet, "/admin?embed=1"}, // document 优先于 embed=1，顶层一律回 SPA
		{http.MethodPost, "/admin/users/1/approve"},
	}
	for _, tc := range documentCases {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s %s: status = %d, want 303", tc.method, tc.target, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/?admin=1" {
			t.Fatalf("%s %s: location = %q, want /?admin=1", tc.method, tc.target, loc)
		}
	}

	// iframe、?embed=1 与无 header（测试/API/CLI）：不触发 SPA 重定向。
	// 未登录时应照旧跳登录页，且 next 保留原路径——证明管理台仍可被 iframe 直接渲染。
	allowedCases := []struct {
		name   string
		target string
		dest   string
	}{
		{"iframe", "/admin/users", "iframe"},
		{"iframe embed", "/admin?embed=1", "iframe"},
		{"embed without header", "/admin?embed=1", ""},
		{"no header (tests/api)", "/admin", ""},
	}
	for _, tc := range allowedCases {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		if tc.dest != "" {
			req.Header.Set("Sec-Fetch-Dest", tc.dest)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, want login redirect", tc.name, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if loc == "/?admin=1" {
			t.Fatalf("%s: unexpectedly redirected to SPA (%q)", tc.name, loc)
		}
		if !strings.HasPrefix(loc, "/login?next=") {
			t.Fatalf("%s: location = %q, want /login?next=...", tc.name, loc)
		}
	}
}
