package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 管理台只有 SPA 一个入口：浏览器顶层 document 导航（含 /admin?embed=1、POST）
// 一律 303 回 /?admin=1，且在鉴权之前返回——不渲染管理台、不跳登录页。
func TestAdminHTMLRedirectsTopLevelDocument(t *testing.T) {
	app := &App{}
	handler := app.adminHTML("", func(http.ResponseWriter, *http.Request, adminAccess) {
		t.Fatal("document 请求不应进入管理台 handler")
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
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s %s: cache-control = %q, want no-store", tc.method, tc.target, cc)
		}
		if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Sec-Fetch-Dest") {
			t.Fatalf("%s %s: vary = %q, want Sec-Fetch-Dest", tc.method, tc.target, vary)
		}
		if body := rec.Body.String(); strings.Contains(body, "admin-nav") {
			t.Fatalf("%s %s: 303 响应不应渲染管理台", tc.method, tc.target)
		}
	}
}

// 管理台 iframe 打开 /admin/activities 时不再渲染 SSR 列表，303 到 v1 原版活动管理；
// 顶层 document 仍由 adminHTML 包装层 303 到 /?admin=1（现有测试覆盖）。
func TestAdminActivitiesIframeRedirectsToNativeConsole(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodGet, "/admin/activities", nil)
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	rec := httptest.NewRecorder()
	app.handleAdminActivitiesPage(rec, req, adminAccess{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/app?native_admin=1&p=group_admin" {
		t.Fatalf("location = %q", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
}

// 管理台受权媒体串流（照片原图、活动海报）允许顶层 document 打开：管理台用
// target="_blank" 在新窗口看原图时，浏览器发的是 Sec-Fetch-Dest: document，
// 媒体路由不能落进页面路由的 303；鉴权仍先于一切。
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
		t.Fatalf("document media: status = %d, want login redirect (auth first, never 303 to SPA)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/?login=1&next=") || loc == "/?admin=1" {
		t.Fatalf("document media: location = %q, want /?login=1&next=...", loc)
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
		if loc == "/?admin=1" {
			t.Fatalf("%s: unexpectedly redirected to SPA (%q)", tc.name, loc)
		}
		if want := "/?login=1&next=" + url.QueryEscape(tc.target); loc != want {
			t.Fatalf("%s: location = %q, want %q", tc.name, loc, want)
		}
	}
}
