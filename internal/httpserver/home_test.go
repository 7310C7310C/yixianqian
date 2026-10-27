package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
)

const publicLandingTitle = "一线牵 · 祝你早日找到另一半"

var hanPattern = regexp.MustCompile(`\p{Han}`)

// assertEnglishPublicLanding enforces the PM scan-resistance contract: the
// landing page may contain Chinese only inside <title>, never in the header,
// body copy, aria labels or links.
func assertEnglishPublicLanding(t *testing.T, body string) {
	t.Helper()
	titleTag := "<title>" + publicLandingTitle + "</title>"
	if strings.Count(body, titleTag) != 1 {
		t.Fatalf("public landing title must be exactly %q\nbody=%s", publicLandingTitle, body)
	}
	rest := strings.Replace(body, titleTag, "", 1)
	if match := hanPattern.FindString(rest); match != "" {
		start := strings.Index(rest, match)
		from := start - 40
		if from < 0 {
			from = 0
		}
		to := start + 40
		if to > len(rest) {
			to = len(rest)
		}
		t.Fatalf("public landing contains Han characters outside <title>: %q near %q",
			match, rest[from:to])
	}
	for _, unwanted := range []string{
		"跳至主要内容", "shell-skip", "shell-header", "shell-account",
		"一线牵", "立即注册", "人工审核", "匿名喜欢", "动态通知",
		"已在飞书版使用过", "feishu-qr", "二维码", "扫一扫",
	} {
		if strings.Contains(rest, unwanted) {
			t.Fatalf("public landing still contains %q", unwanted)
		}
	}
	for _, want := range []string{
		`lang="en"`,
		`class="shell-body public-landing"`,
		"YIXIANQIAN",
		"A trusted place for single Christians to meet.",
		"Create account",
		"Log in",
		"Free to join · Human review · Phone number login",
		"Human review",
		"Anonymous likes",
		"Activity updates",
		"Used the Feishu version before?",
		"Open the Feishu version",
		`href="/register?from=home"`,
		`href="/login"`,
		publicFeishuAppURL,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in landing page", want)
		}
	}
}

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
	assertEnglishPublicLanding(t, body)
	if strings.Contains(body, `id="app"`) {
		t.Fatal("anonymous landing page should not serve the SPA")
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
	if strings.Contains(body, "public-landing") {
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
		"<title>登录 · 一线牵</title>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in login page", want)
		}
	}
	// The login page stays Chinese: only the public landing page is English.
	if !hanPattern.MatchString(body) {
		t.Fatal("login page should remain Chinese")
	}
}
