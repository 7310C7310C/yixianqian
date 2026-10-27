package httpserver

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	webassets "github.com/7310C7310C/yixianqian/web"
)

// assertPublicErrorBody enforces the scan-safe public GET error contract:
// status/code preserved, zero Han outside <title>, ASCII-only assets, and the
// error copy resolved by data-error-code at runtime.
func assertPublicErrorBody(t *testing.T, resp *http.Response, body, code string, wantStatus int) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (body=%s)", resp.StatusCode, wantStatus, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if cache := resp.Header.Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cache)
	}
	assertHanFreePublicBody(t, body)
	for _, want := range []string{
		`data-copy-page="public_error"`,
		`data-error-code="` + code + `"`,
		`data-copy-error="heading"`,
		`data-copy-error="message"`,
		`data-copy-error="action"`,
		`/static/public-copy.js`,
		`/static/auth.css`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("public error page missing %q", want)
		}
	}
	for _, unwanted := range []string{`/static/app.css`, `/static/shell.css`, `class="shell-header"`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("public error page must not contain %q", unwanted)
		}
	}
}

func createActivationForTest(t *testing.T, env *profileHTTPEnv, userID *int64, ttl time.Duration) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := env.database.CreateActivationToken(env.ctx, userID, "", "", hash, ttl)
	if err != nil {
		t.Fatalf("create activation token: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(),
			`DELETE FROM activation_tokens WHERE id = $1`, token.ID)
	})
	return raw
}

// TestIntegrationPublicActivateErrorPages covers the public GET failure states
// that used to render the Chinese error.tmpl with app.css/shell.css.
func TestIntegrationPublicActivateErrorPages(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	anon := newWSClient(t, env.server.URL)

	t.Run("missing token", func(t *testing.T) {
		resp := anon.get("/activate")
		assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorActivateMissing, http.StatusBadRequest)
	})

	t.Run("invalid token", func(t *testing.T) {
		resp := anon.get("/activate?token=" + url.QueryEscape("not-a-real-token"))
		assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorActivateInvalid, http.StatusBadRequest)
	})

	t.Run("expired token", func(t *testing.T) {
		user, _ := env.createUser(t, "U-PUBERR-EXPIRED", "过期链接用户", "active", "user")
		cleanupHTTPUsers(t, env, user.ID)
		raw := createActivationForTest(t, env, &user.ID, -time.Hour)
		resp := anon.get("/activate?token=" + url.QueryEscape(raw))
		assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorActivateExpired, http.StatusGone)
	})

	t.Run("used token", func(t *testing.T) {
		user, _ := env.createUser(t, "U-PUBERR-USED", "已用链接用户", "active", "user")
		cleanupHTTPUsers(t, env, user.ID)
		raw := createActivationForTest(t, env, &user.ID, time.Hour)
		if _, err := env.pool.Exec(env.ctx,
			`UPDATE activation_tokens SET consumed_at = now() WHERE token_hash = $1`,
			auth.HashToken(raw)); err != nil {
			t.Fatal(err)
		}
		resp := anon.get("/activate?token=" + url.QueryEscape(raw))
		assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorActivateUsed, http.StatusGone)
	})

	t.Run("already enabled", func(t *testing.T) {
		user, _ := env.createUser(t, "U-PUBERR-ALREADY", "已启用用户", "active", "user")
		cleanupHTTPUsers(t, env, user.ID)
		hash, err := auth.HashPassword("public-error-pass-123")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO auth_credentials (user_id, password_hash) VALUES ($1,$2)
			 ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
			user.ID, hash); err != nil {
			t.Fatal(err)
		}
		raw := createActivationForTest(t, env, &user.ID, time.Hour)
		resp := anon.get("/activate?token=" + url.QueryEscape(raw))
		assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorActivateAlready, http.StatusGone)
	})
}

// TestIntegrationPublicPasswordErrorPages covers /password without a credential
// (409 scan-safe page) and an invalid session (redirect preserved, no body).
func TestIntegrationPublicPasswordErrorPages(t *testing.T) {
	env := setupProfileHTTPEnv(t)

	user, client := env.createUser(t, "U-PUBERR-NOCRED", "无密码用户", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)

	resp := client.get("/password")
	assertPublicErrorBody(t, resp, readBody(t, resp), publicErrorPasswordNoCred, http.StatusConflict)

	// Invalid/expired session cookie: still a 303 to /login (no HTML body).
	bad := newWSClient(t, env.server.URL)
	base, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	bad.jar.SetCookies(base, []*http.Cookie{{
		Name:  env.cfg.SessionCookieName,
		Value: "expired-session-token",
		Path:  "/",
	}})
	resp = bad.get("/password")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("invalid session status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("invalid session location = %q", loc)
	}
	if body := readBody(t, resp); strings.Contains(body, "data-copy-page") {
		t.Fatalf("invalid session must not render an HTML page: %q", body)
	}
}

// TestPublicErrorCopyCoversCodes keeps the Go error codes and public-copy.js
// table in sync (a missing key would render an empty error page).
func TestPublicErrorCopyCoversCodes(t *testing.T) {
	jsData, err := fs.ReadFile(webassets.FS, "static/public-copy.js")
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{
		publicErrorActivateMissing, publicErrorActivateInvalid, publicErrorActivateUsed,
		publicErrorActivateExpired, publicErrorActivateAlready, publicErrorPasswordNoCred, "default",
	}
	js := string(jsData)
	for _, code := range codes {
		if publicErrorTitles[code] == "" && code != "default" {
			t.Fatalf("missing title for error code %q", code)
		}
		for _, suffix := range []string{"heading", "message", "action"} {
			key := "'public_error." + code + "." + suffix + "'"
			if !strings.Contains(js, key) {
				t.Fatalf("public-copy.js missing %s", key)
			}
		}
	}
}
