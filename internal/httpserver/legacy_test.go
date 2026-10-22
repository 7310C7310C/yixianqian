package httpserver

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
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
		`const BASE_PATH = '/legacy';`,
		`const APP_VERSION = 'legacy';`,
		`src="lib/vue.global.prod.js"`,
		`src="lib/vant.min.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("legacy index missing %q", want)
		}
	}
	if strings.Contains(html, "lf1-cdn-tos") || strings.Contains(html, "h5-js-sdk") {
		t.Fatal("legacy index still references the Feishu SDK")
	}
}

func TestLegacyAppRedirectsAnonymous(t *testing.T) {
	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
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
	user := &db.User{ID: 6, LegacyUserID: &legacy, Name: "测试用户"}
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
	if !strings.Contains(rec.Body.String(), `<base href="/static/legacy/">`) {
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
