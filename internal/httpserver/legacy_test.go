package httpserver

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
	webassets "github.com/7310C7310C/yixianqian/web"
)

func TestLegacyIndexIsEmbedded(t *testing.T) {
	if _, err := fs.Stat(webassets.FS, legacyIndexPath); err != nil {
		t.Fatalf("legacy index missing from embed.FS: %v", err)
	}
	body, err := loadLegacyIndex()
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`<base href="/static/legacy/">`,
		`window.__YXQ_AUTHENTICATED__=false;`,
		`const BASE_PATH = '/legacy';`,
		`const APP_VERSION = 'legacy';`,
		`src="lib/vue.global.prod.js"`,
		`src="lib/vant.min.js"`,
		`history.pushState(state, '', navURL());`,
		`nativeAdmin`,
		`function gaBack()`,
		`native_admin`,
		`viewport-fit=cover`,
		`href="favicon.ico"`,
		`<div class="section-title">管理</div>`,
		`fixed safe-area-inset-bottom`,
		`TAB_ORDER.includes(nv) && TAB_ORDER.includes(ov)`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("legacy index missing %q", want)
		}
	}
	if strings.Contains(html, "lf1-cdn-tos") || strings.Contains(html, "h5-js-sdk") {
		t.Fatal("legacy index still references the Feishu SDK")
	}
	if strings.Contains(html, `location.pathname + '#' + (state.page`) {
		t.Fatal("legacy pushHistory still appends a hash")
	}

	// 管理后台是独立 section（账号之后、帮助之前），不再放在账号 cell group 内。
	accountIdx := strings.Index(html, "<!-- 账号 -->")
	adminIdx := strings.Index(html, `<div class="section-title">管理</div>`)
	adminCellIdx := strings.Index(html, `title="🛠️ 管理后台"`)
	helpIdx := strings.Index(html, "<!-- 帮助 -->")
	if accountIdx < 0 || adminIdx < 0 || adminCellIdx < 0 || helpIdx < 0 {
		t.Fatalf("admin section markers missing: account=%d admin=%d cell=%d help=%d",
			accountIdx, adminIdx, adminCellIdx, helpIdx)
	}
	if !(accountIdx < adminIdx && adminIdx < adminCellIdx && adminCellIdx < helpIdx) {
		t.Fatalf("admin section order wrong: account=%d admin=%d cell=%d help=%d",
			accountIdx, adminIdx, adminCellIdx, helpIdx)
	}
}

func TestLegacyFaviconsAreEmbedded(t *testing.T) {
	for _, name := range []string{"static/legacy/favicon.ico", "static/legacy/favicon.png", "static/legacy/favicon.svg"} {
		info, err := fs.Stat(webassets.FS, name)
		if err != nil {
			t.Fatalf("%s missing from embed.FS: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestLegacyFaviconsAreServed(t *testing.T) {
	h := staticHandler()
	for _, path := range []string{"/static/legacy/favicon.ico", "/static/legacy/favicon.png", "/static/legacy/favicon.svg"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s served empty body", path)
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" {
			t.Fatalf("%s missing content type", path)
		}
	}
}

func TestLegacyAppRedirectsAnonymous(t *testing.T) {
	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	app.serveLegacyApp(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login?next=/" {
		t.Fatalf("location = %q", got)
	}
}

func TestLegacyAppServesHTMLForSignedInUser(t *testing.T) {
	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	legacy := "U-0006"
	phone := "13800000006"
	user := &db.User{ID: 6, LegacyUserID: &legacy, Name: "测试用户", Phone: phone}
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req = req.WithContext(withUser(req.Context(), user))
	rec := httptest.NewRecorder()
	app.serveLegacyApp(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `window.__YXQ_AUTHENTICATED__=true;`) {
		t.Fatal("served body is missing the authenticated flag")
	}
	if strings.Contains(body, `window.__YXQ_AUTHENTICATED__=false;`) {
		t.Fatal("served body still contains the anonymous flag")
	}
	for _, sensitive := range []string{user.Name, user.Phone, legacy} {
		if strings.Contains(body, sensitive) {
			t.Fatalf("served body leaked account data %q", sensitive)
		}
	}
	if !strings.Contains(body, `<base href="/static/legacy/">`) {
		t.Fatal("served body is not the legacy SPA")
	}
	if rec.Header().Values("Set-Cookie") == nil {
		t.Fatal("legacy page did not issue a CSRF cookie")
	}
}

func TestSecurityPolicyRelaxesLegacySPAOnly(t *testing.T) {
	for _, path := range []string{"/", "/app"} {
		policy := securityPolicy(path)
		for _, want := range []string{"script-src 'self' 'unsafe-inline'", "style-src 'self' 'unsafe-inline'", "img-src 'self' data: blob:", "base-uri 'self'"} {
			if !strings.Contains(policy, want) {
				t.Fatalf("%s policy missing %q: %s", path, want, policy)
			}
		}
	}
	strict := securityPolicy("/cards")
	if strings.Contains(strict, "unsafe-inline") {
		t.Fatalf("strict policy relaxed: %s", strict)
	}
	if !strings.Contains(strict, "base-uri 'none'") {
		t.Fatalf("strict policy lost base-uri: %s", strict)
	}
}

// 活动管理 native SPA 只在 native_admin=1 的同源 iframe 场景放宽 framing；
// 普通 / 与 /app 仍是 frame-ancestors 'none' / X-Frame-Options: DENY。
func TestNativeAdminAppAllowsSameOriginFraming(t *testing.T) {
	native := url.Values{"native_admin": {"1"}, "p": {"group_admin"}}
	if got := frameOptionsWithQuery("/app", native); got != "SAMEORIGIN" {
		t.Fatalf("/app?native_admin=1 X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if csp := securityPolicyWithQuery("/app", native); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("/app?native_admin=1 CSP = %q, want frame-ancestors 'self'", csp)
	}
	if got := frameOptions("/app"); got != "DENY" {
		t.Fatalf("/app X-Frame-Options = %q, want DENY", got)
	}
	if csp := securityPolicy("/app"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("/app CSP = %q, want frame-ancestors 'none'", csp)
	}
	if got := frameOptions("/"); got != "DENY" {
		t.Fatalf("/ X-Frame-Options = %q, want DENY", got)
	}
}

// 服务端渲染的壳要注入本次构建版本：前端比对 /legacy/api/version 后
// 只在 App 内提示「有新版本」，刷新由用户点击触发。
func TestLegacyAppInjectsBuildVersion(t *testing.T) {
	old := platform.Version
	platform.Version = "staging-test123"
	defer func() { platform.Version = old }()

	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	legacy := "U-0006"
	user := &db.User{ID: 6, LegacyUserID: &legacy, Name: "测试用户", Phone: "13800000006"}
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req = req.WithContext(withUser(req.Context(), user))
	rec := httptest.NewRecorder()
	app.serveLegacyApp(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `const APP_VERSION = "staging-test123";`) {
		t.Fatal("served shell is missing the injected build version")
	}
	if strings.Contains(body, `const APP_VERSION = 'legacy';`) {
		t.Fatal("served shell still carries the version placeholder")
	}
}

func TestLegacyVersionReportsBuildVersion(t *testing.T) {
	old := platform.Version
	platform.Version = "staging-test123"
	defer func() { platform.Version = old }()

	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	app.handleLegacyVersion(rec, httptest.NewRequest(http.MethodGet, "/legacy/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"version":"staging-test123"`) {
		t.Fatalf("version body = %s", got)
	}
}
