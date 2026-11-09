package httpserver

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
)

// TestIntegrationLegacyAdminStaleSessionClearsCookie is the regression test
// for the "登录已过期 → 重新登录 → 登录已过期" loop:
//
//   - the user session is still valid (30-day cookie), but the admin console's
//     optional absolute cap rejects it;
//   - the legacy admin API must revoke the stale session (not only clear the
//     cookie) so a restored/in-flight token cannot re-enter the loop, and the
//     follow-up GET /login sends the browser to the login dialog instead of
//     303-ing back to /.
func TestIntegrationLegacyAdminStaleSessionClearsCookie(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) {
		c.AdminSessionTTL = time.Hour
		c.SessionTTL = 24 * time.Hour
	})
	admin := createMessageUser(t, env.ctx, env.database, env.pool, "过期管理员")
	t.Cleanup(func() {
		_, _ = env.pool.Exec(env.ctx, `DELETE FROM users WHERE id = $1`, admin.ID)
	})
	if changed, err := env.database.AssignAdminRole(env.ctx, admin.ID, "super_admin", true); err != nil || !changed {
		t.Fatalf("assign super_admin: changed=%v err=%v", changed, err)
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	session, err := env.database.CreateSession(env.ctx, admin.ID, hash, 24*time.Hour, "", "stale-admin-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Keep the session unexpired but older than AdminSessionTTL.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE sessions SET created_at = now() - interval '2 hours' WHERE id = $1`, session.ID); err != nil {
		t.Fatalf("backdate session: %v", err)
	}

	serverURL, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookieName := testConfig().SessionCookieName
	client := newWSClient(t, env.server.URL)
	client.jar.SetCookies(serverURL, []*http.Cookie{{
		Name: cookieName, Value: raw, Path: "/",
	}})

	resp := client.get("/legacy/api/admin/group/activities")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "未登录") {
		t.Fatalf("stale admin session status=%d body=%s", resp.StatusCode, body)
	}
	cleared := false
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, cookieName+"=;") && strings.Contains(sc, "Max-Age=0") {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatalf("401 did not clear %s: %v", cookieName, resp.Header.Values("Set-Cookie"))
	}
	var revokedAt *time.Time
	if err := env.pool.QueryRow(env.ctx,
		`SELECT revoked_at FROM sessions WHERE id = $1`, session.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("stale admin 401 must revoke the session, not only clear the cookie")
	}

	// A restored/in-flight copy of the old cookie must not authenticate and
	// must not send /login back to / (the loop).
	restored := newWSClient(t, env.server.URL)
	restored.jar.SetCookies(serverURL, []*http.Cookie{{
		Name: cookieName, Value: raw, Path: "/",
	}})
	resp = restored.get("/legacy/api/profile")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still authenticated: %d", resp.StatusCode)
	}
	login := restored.get("/login")
	if login.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /login after revoked token = %d, want 303", login.StatusCode)
	}
	if loc := login.Header.Get("Location"); !strings.Contains(loc, "login=1") {
		t.Fatalf("GET /login Location = %q, want the login dialog", loc)
	}
}

// TestIntegrationSlidingSessionRefreshesCookie exercises the middleware, not
// just the DB helper: a near-expiry session is slid forward, a second request
// does not rewrite the expiry, and a revoked session is rejected without
// extension.
func TestIntegrationSlidingSessionRefreshesCookie(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) {
		c.SessionTTL = 2 * time.Hour
	})
	user := createMessageUser(t, env.ctx, env.database, env.pool, "滑动会话用户")
	t.Cleanup(func() {
		_, _ = env.pool.Exec(env.ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	session, err := env.database.CreateSession(env.ctx, user.ID, hash, 2*time.Hour, "", "slide-http-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE sessions SET expires_at = now() + interval '30 minutes' WHERE id = $1`, session.ID); err != nil {
		t.Fatalf("shorten expiry: %v", err)
	}

	client := newWSClient(t, env.server.URL)
	serverURL, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookieName := testConfig().SessionCookieName
	client.jar.SetCookies(serverURL, []*http.Cookie{{
		Name: cookieName, Value: raw, Path: "/",
	}})

	resp := client.get("/legacy/api/profile")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status = %d, want 200", resp.StatusCode)
	}
	refreshed := false
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, cookieName+"="+raw+";") && !strings.Contains(sc, "Max-Age=0") {
			refreshed = true
			break
		}
	}
	if !refreshed {
		t.Fatalf("sliding request did not refresh the session cookie: %v", resp.Header.Values("Set-Cookie"))
	}
	readExpiry := func() time.Time {
		var at time.Time
		if err := env.pool.QueryRow(env.ctx, `SELECT expires_at FROM sessions WHERE id = $1`, session.ID).Scan(&at); err != nil {
			t.Fatalf("read expiry: %v", err)
		}
		return at
	}
	first := readExpiry()
	if remaining := time.Until(first); remaining < 100*time.Minute {
		t.Fatalf("expiry not slid: remaining %v, want ~2h", remaining)
	}

	// A second request must not rewrite the already-extended expiry.
	resp = client.get("/legacy/api/profile")
	_ = readBody(t, resp)
	if second := readExpiry(); !second.Equal(first) {
		t.Fatalf("second request rewrote expiry: %v -> %v", first, second)
	}

	// A revoked session is rejected and cannot be extended.
	if _, err := env.pool.Exec(env.ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, session.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	resp = client.get("/legacy/api/profile")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want 401", resp.StatusCode)
	}
	if revokedExpiry := readExpiry(); !revokedExpiry.Equal(first) {
		t.Fatalf("revoked session expiry changed: %v -> %v", first, revokedExpiry)
	}
}

// TestIntegrationSessionCookieNotOnPublicCache guards the refresh path: an
// authenticated response must not carry a session token while remaining
// public-cacheable. The middleware marks such responses private/no-store and
// leaves anonymous public caching untouched.
func TestIntegrationSessionCookieNotOnPublicCache(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	user := createMessageUser(t, env.ctx, env.database, env.pool, "缓存会话用户")
	t.Cleanup(func() {
		_, _ = env.pool.Exec(env.ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateSession(env.ctx, user.ID, hash, time.Hour, "", "cache-test"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	serverURL, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookieName := testConfig().SessionCookieName
	client := newWSClient(t, env.server.URL)
	client.jar.SetCookies(serverURL, []*http.Cookie{{
		Name: cookieName, Value: raw, Path: "/",
	}})

	resp := client.get("/register/regions")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /register/regions status = %d", resp.StatusCode)
	}
	cc := strings.ToLower(resp.Header.Get("Cache-Control"))
	if strings.Contains(cc, "public") || (!strings.Contains(cc, "private") && !strings.Contains(cc, "no-store")) {
		t.Fatalf("authenticated cacheable response Cache-Control = %q, want private/no-store", cc)
	}
	if !hasSessionSetCookie(resp, cookieName) {
		t.Fatalf("authenticated response missing session refresh: %v", resp.Header.Values("Set-Cookie"))
	}

	anon := newWSClient(t, env.server.URL)
	resp = anon.get("/register/regions")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous /register/regions status = %d", resp.StatusCode)
	}
	anonCC := strings.ToLower(resp.Header.Get("Cache-Control"))
	if !strings.Contains(anonCC, "public") || !strings.Contains(anonCC, "max-age") {
		t.Fatalf("anonymous public cache weakened: Cache-Control = %q", anonCC)
	}
	if hasSessionSetCookie(resp, cookieName) {
		t.Fatalf("anonymous response carried a session cookie: %v", resp.Header.Values("Set-Cookie"))
	}
}

func hasSessionSetCookie(resp *http.Response, name string) bool {
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, name+"=") && !strings.Contains(sc, "Max-Age=0") {
			return true
		}
	}
	return false
}
