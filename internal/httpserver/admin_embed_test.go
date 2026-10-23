package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("GET %s: content type = %q, want text/plain", target, ct)
		}
		if strings.Contains(rec.Body.String(), "admin-nav") {
			t.Fatalf("GET %s: 404 body leaked admin markup", target)
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

// 管理台受权媒体串流（照片原图）允许顶层 document 打开：管理台用
// target="_blank" 在新窗口看原图时，浏览器发的是 Sec-Fetch-Dest: document，
// 媒体路由不能落进页面路由的 404 分支；鉴权仍先于一切。
func TestAdminHTMLMediaAllowsDocumentNavigation(t *testing.T) {
	app := &App{}
	handler := app.adminHTMLMedia("", func(http.ResponseWriter, *http.Request, adminAccess) {
		t.Fatal("文档请求匿名访问时不应进入媒体 handler")
	})

	req := httptest.NewRequest(http.MethodGet,
		"/admin/users/1/photos/11111111-1111-4111-8111-111111111111", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("document media: status = %d, want login redirect (auth first, never 404)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("document media: location = %q, want /login?next=...", loc)
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
