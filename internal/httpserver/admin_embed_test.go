package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// 顶层不提供管理台页面：document 导航（含 /admin?embed=1）一律 404，且在鉴权
// 之前返回——不跳登录、不跳 SPA、不带 Location。管理台只经站内 iframe 使用。
func TestAdminHTMLDocumentIsNotFound(t *testing.T) {
	app := &App{}
	handler := app.adminHTML("", func(w http.ResponseWriter, _ *http.Request, _ adminAccess) {
		t.Fatal("document 请求不应进入管理台 handler")
	})

	for _, target := range []string{
		"/admin",
		"/admin/users?status=pending",
		"/admin?embed=1",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d, want 404", target, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("GET %s: location = %q, want no redirect", target, loc)
		}
	}

	// POST document 同样在鉴权前 404，不暴露登录跳转。
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/approve", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
		t.Fatalf("POST document: status = %d location = %q, want 404 without redirect",
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
