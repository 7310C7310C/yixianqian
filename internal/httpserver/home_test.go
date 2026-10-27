package httpserver

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	webassets "github.com/7310C7310C/yixianqian/web"
)

const publicLandingTitle = "一线牵 · 祝你早日找到另一半"

var (
	hanPattern = regexp.MustCompile(`\p{Han}`)
	// Landing copy is injected by /static/home.js, so the rendered markup must
	// only carry empty data-* hooks: one per copy key, plus aria-label keys.
	homeTextAttr  = regexp.MustCompile(`data-home-text="([^"]+)"`)
	homeTextEmpty = regexp.MustCompile(`data-home-text="[^"]+"[^>]*>\s*<`)
	homeAriaAttr  = regexp.MustCompile(`data-home-aria="([^"]+)"`)
)

// assertPublicLandingContract enforces the PM scan-resistance contract: the
// anonymous landing page may contain Chinese only inside <title>. Every piece
// of copy is injected at runtime by /static/home.js, whose source — like the
// landing stylesheets — is ASCII-only, so a scanner reading the page or its
// assets has nothing to match.
func assertPublicLandingContract(t *testing.T, body string) {
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
		"立即注册", "人工审核", "匿名喜欢", "动态通知",
		"已在飞书版使用过", "feishu-qr", "二维码", "扫一扫",
		`id="app"`, `/static/app.css`, `/static/shell.css`, `lang="en"`,
		"A trusted place", "Create account", "Human review",
		"Used the Feishu version before?",
	} {
		if strings.Contains(rest, unwanted) {
			t.Fatalf("public landing still contains %q", unwanted)
		}
	}
	for _, want := range []string{
		`lang="zh-CN"`,
		`class="shell-body public-landing home-pending"`,
		`/static/home.css`,
		`/static/home.js`,
		`href="/register?from=home"`,
		`href="/login"`,
		`id="feishu"`,
		`data-home-text="hero.ctaRegister"`,
		`data-home-aria="features.label"`,
		publicFeishuAppURL,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in landing page", want)
		}
	}
	// The markup ships without copy: all data-home-text nodes stay empty until
	// home.js fills them.
	if got, empty := len(homeTextAttr.FindAllString(body, -1)), len(homeTextEmpty.FindAllString(body, -1)); got == 0 || got != empty {
		t.Fatalf("data-home-text nodes = %d, empty ones = %d; copy must be injected by home.js", got, empty)
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
	assertPublicLandingContract(t, body)
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
		`class="button primary block"`,
		`data-copy-page="login"`,
		`data-copy-text="login.altCta"`,
		publicFeishuAppURL,
		"<title>登录 · 一线牵</title>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in login page", want)
		}
	}
	// Scan contract: the login page is Han-free outside <title>; the visible
	// Chinese copy is injected by /static/public-copy.js.
	assertHanFreePublicBody(t, body)
	for _, want := range []string{`/static/home.css`, `/static/auth.css`, `/static/public-copy.js`} {
		if !strings.Contains(body, want) {
			t.Fatalf("login page should load %q", want)
		}
	}
	for _, unwanted := range []string{`/static/app.css`, `/static/shell.css`, `/static/home.js`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("login page must not load %q", unwanted)
		}
	}
}

// TestPublicLandingAssetsAreHanFree guards the second half of the scan
// contract: the CSS/JS the landing page requests must be ASCII-only too, so a
// scanner cannot match sensitive words in them.
func TestPublicLandingAssetsAreHanFree(t *testing.T) {
	for _, name := range []string{"static/home.js", "static/home.css", "static/register.css", "static/register.js"} {
		data, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
		if match := hanPattern.FindString(string(data)); match != "" {
			t.Fatalf("%s contains Han characters (%q); landing CSS/JS must stay ASCII-only", name, match)
		}
	}
}

// TestPublicLandingTextKeysCoveredByHomeJS keeps the empty markup hooks and the
// injected copy table in sync: a missing key would render a blank node.
func TestPublicLandingTextKeysCoveredByHomeJS(t *testing.T) {
	app := newHomeTestApp(t, nil)
	rec := httptest.NewRecorder()
	app.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	js, err := fs.ReadFile(webassets.FS, "static/home.js")
	if err != nil {
		t.Fatalf("read static/home.js: %v", err)
	}
	src := string(js)
	keys := append(homeTextAttr.FindAllStringSubmatch(body, -1), homeAriaAttr.FindAllStringSubmatch(body, -1)...)
	if len(keys) == 0 {
		t.Fatal("landing markup has no data-home-* copy keys")
	}
	for _, match := range keys {
		key := match[1]
		if !strings.Contains(src, "'"+key+"'") && !strings.Contains(src, `"`+key+`"`) {
			t.Fatalf("home.js has no copy for key %q", key)
		}
	}
}
