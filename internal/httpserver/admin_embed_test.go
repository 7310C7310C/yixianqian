package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// 顶层 document 与 iframe 一样渲染 SSR：不再跳 /?admin=1。
// 未登录仍跳登录页，next 保留原始 /admin 深链。
func TestAdminHTMLDocumentRendersSSR(t *testing.T) {
	app := &App{}
	handler := app.adminHTML("", func(w http.ResponseWriter, _ *http.Request, _ adminAccess) {
		w.WriteHeader(http.StatusOK)
	})

	documentCases := []string{
		"/admin",
		"/admin/users?status=pending",
		"/admin?embed=1",
	}
	for _, target := range documentCases {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("GET %s: status = %d, want login redirect", target, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if loc == "/?admin=1" {
			t.Fatalf("GET %s: 仍跳 SPA（%q）", target, loc)
		}
		if want := "/login?next=" + url.QueryEscape(target); loc != want {
			t.Fatalf("GET %s: location = %q, want %q", target, loc, want)
		}
	}

	// POST 顶层 document 与 iframe/无 header 一致：未登录回 /login，不带 next。
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/approve", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("POST document: status = %d location = %q, want 303 /login",
			rec.Code, rec.Header().Get("Location"))
	}
}

// iframe、?embed=1 与无 header（测试/API/CLI）行为不变：直接走 SSR 鉴权，
// 未登录应跳登录页且 next 保留原路径。
func TestAdminHTMLIframeAndHeaderlessRenderSSR(t *testing.T) {
	app := &App{}
	handler := app.adminHTML("", func(w http.ResponseWriter, _ *http.Request, _ adminAccess) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name   string
		target string
		dest   string
	}{
		{"iframe", "/admin/users", "iframe"},
		{"iframe embed", "/admin?embed=1", "iframe"},
		{"embed without header", "/admin?embed=1", ""},
		{"no header (tests/api)", "/admin", ""},
	}
	for _, tc := range cases {
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
		if want := "/login?next=" + url.QueryEscape(tc.target); loc != want {
			t.Fatalf("%s: location = %q, want %q", tc.name, loc, want)
		}
	}
}
