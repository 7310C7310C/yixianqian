package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func newHomeTestApp(t *testing.T, mutate func(*config.Config)) *App {
	t.Helper()
	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	app, err := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestHomeRendersPublicLandingForAnonymous(t *testing.T) {
	app := newHomeTestApp(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	app.handleHome(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"一线牵",
		"为教会内未婚青年牵线搭桥",
		"愿主内的弟兄姐妹，早日找到另一半",
		"人工审核",
		"匿名喜欢",
		"动态通知",
		`href="/register?from=home"`,
		"立即注册",
		`href="/login"`,
		"已在飞书版使用过",
		"请先在飞书版设置好密码",
		publicFeishuAppURL,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in landing page", want)
		}
	}
	if strings.Contains(body, `id="app"`) {
		t.Fatal("anonymous landing page should not serve the SPA")
	}
	for _, unwanted := range []string{`feishu-qr`, `二维码`, `扫一扫`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("landing page still contains %q", unwanted)
		}
	}
}

func TestHomeAdminQueryRedirectsAnonymousToLogin(t *testing.T) {
	app := newHomeTestApp(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?admin=1", nil)
	app.handleHome(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login?next=%2F%3Fadmin%3D1" {
		t.Fatalf("location = %q", got)
	}
}

func TestHomeServesSPAForSignedInUser(t *testing.T) {
	app := newHomeTestApp(t, nil)
	user := &db.User{ID: 6, Name: "测试用户", Phone: "13800000006"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withUser(req.Context(), user))
	rec := httptest.NewRecorder()
	app.handleHome(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Fatal("signed-in home should serve the SPA")
	}
	if !strings.Contains(body, `window.__YXQ_AUTHENTICATED__=true;`) {
		t.Fatal("signed-in SPA is missing the authenticated flag")
	}
	if strings.Contains(body, "已在飞书版使用过") {
		t.Fatal("signed-in home should not render the public landing page")
	}
}

func TestLoginPageShowsRegisterCTA(t *testing.T) {
	app := newHomeTestApp(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	app.handleLoginPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="csrf_token"`,
		`href="/register"`,
		"注册新账号",
		`class="button primary block"`,
		"已在飞书版使用过",
		"请先在飞书版设置好密码",
		publicFeishuAppURL,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in login page", want)
		}
	}
}
