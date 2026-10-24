package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/auth"
)

var adminTempValueRE = regexp.MustCompile(`<p class="admin-temp-value"><code>([^<]+)</code></p>`)

// TestIntegrationAdminUserPagination covers ADM-01: the list must page through
// every matching user (default 50, explicit per_page/page, search over the
// whole table) and clamp out-of-range pages.
func TestIntegrationAdminUserPagination(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	admin := createMessageUser(t, ctx, database, pool, "分页管理员")
	if _, err := database.AssignAdminRole(ctx, admin.ID, "super_admin", true); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	client := adminSessionClient(t, database, server, admin)

	token := "分页" + randomHTTPDigits(t, 8)
	ids := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		u, err := database.UpsertLegacyUser(ctx,
			"U-PAGE-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10),
			fmt.Sprintf("%s-%d", token, i))
		if err != nil {
			t.Fatalf("create user %d: %v", i, err)
		}
		ids = append(ids, u.ID)
	}
	ids = append(ids, admin.ID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	basePath := "/admin/users?status=all&per_page=2&q=" + url.QueryEscape(token)
	resp := client.get(basePath + "&page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if got := strings.Count(body, `<article class="admin-record"`); got != 2 {
		t.Fatalf("page 2 record count = %d, want 2", got)
	}
	for _, want := range []string{"共 5 位", "第 2 / 3 页", `rel="prev"`, `rel="next"`, "page=3"} {
		if !strings.Contains(body, want) {
			t.Fatalf("page 2 missing %q", want)
		}
	}

	// Out-of-range pages clamp to the last page instead of showing an empty
	// list with no way back.
	resp = client.get(basePath + "&page=99")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "第 3 / 3 页") {
		t.Fatalf("page 99 status=%d missing clamp: %s", resp.StatusCode, body)
	}
	if got := strings.Count(body, `<article class="admin-record"`); got != 1 {
		t.Fatalf("clamped last page record count = %d, want 1", got)
	}

	// per_page is capped so a hand-edited URL cannot request arbitrary sizes.
	resp = client.get("/admin/users?status=all&q=" + url.QueryEscape(token) + "&per_page=9999")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "共 5 位") {
		t.Fatalf("capped per_page status=%d body=%s", resp.StatusCode, body)
	}
}

// TestIntegrationAdminTempPasswordPRG covers ADM-06: the HTML flow must redirect
// after POST, show the password exactly once, and never reset it again on
// refresh.
func TestIntegrationAdminTempPasswordPRG(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	admin := createMessageUser(t, ctx, database, pool, "临时密码管理员")
	if _, err := database.AssignAdminRole(ctx, admin.ID, "super_admin", true); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	client := adminSessionClient(t, database, server, admin)
	target := createMessageUser(t, ctx, database, pool, "临时密码目标")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{admin.ID, target.ID})
	})

	csrf := client.pageCSRF("/admin/users?status=all")
	resp := client.postForm("/admin/users/"+strconv.FormatInt(target.ID, 10)+"/temp-password",
		url.Values{
			"csrf_token":    {csrf},
			"status_filter": {"all"},
			"per_page":      {"50"},
			"page":          {"1"},
		})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("temp password status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "temp=") {
		t.Fatalf("redirect location = %q, want one-time temp token", location)
	}
	hashAfterPost, err := database.GetPasswordHash(ctx, target.ID)
	if err != nil {
		t.Fatalf("load hash: %v", err)
	}

	resp = client.get(location)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reveal GET status = %d", resp.StatusCode)
	}
	m := adminTempValueRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("temp password not rendered: %s", body)
	}
	raw := m[1]
	if ok, err := auth.VerifyPassword(raw, hashAfterPost); err != nil || !ok {
		t.Fatalf("served password does not match stored hash: ok=%v err=%v", ok, err)
	}

	// Refresh: the token is single-use, and the stored hash must not change.
	resp = client.get(location)
	body = readBody(t, resp)
	if strings.Contains(body, `class="admin-temp-value"`) {
		t.Fatal("refreshing the reveal URL showed the password twice")
	}
	hashAfterRefresh, err := database.GetPasswordHash(ctx, target.ID)
	if err != nil {
		t.Fatalf("reload hash: %v", err)
	}
	if hashAfterRefresh != hashAfterPost {
		t.Fatal("refreshing the result page reset the password again")
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'admin.user.temp_password' AND target_id = $1`,
		strconv.FormatInt(target.ID, 10)).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("temp password audit rows = %d, want 1", auditCount)
	}
}

// TestIntegrationAdminActivityFormValidation covers ADM-02 and ADM-08.
func TestIntegrationAdminActivityFormValidation(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	admin := createMessageUser(t, ctx, database, pool, "活动校验管理员")
	if changed, err := database.AssignAdminRole(ctx, admin.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := adminSessionClient(t, database, server, admin)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, admin.ID) })
	csrf := client.pageCSRF("/admin/activities/new")

	name := "双开校验" + randomHTTPDigits(t, 8)
	values := adminActivityFormValues(csrf, name)
	values.Set("group_enabled", "1")
	values.Set("onetoone_enabled", "1")
	resp := client.postForm("/admin/activities", values)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "不能同时开启") {
		t.Fatalf("dual mode status=%d body=%s", resp.StatusCode, body)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE name = $1`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("dual-mode activity was persisted (%d rows)", count)
	}

	for _, code := range []string{"", "12"} {
		values = adminActivityFormValues(csrf, name)
		values.Set("signin_enabled", "1")
		values.Set("signin_code", code)
		resp = client.postForm("/admin/activities", values)
		body = readBody(t, resp)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "4 位数字") {
			t.Fatalf("signin code %q status=%d body=%s", code, resp.StatusCode, body)
		}
	}

	// A valid 4-digit code still creates the activity.
	values = adminActivityFormValues(csrf, name)
	values.Set("signin_enabled", "1")
	values.Set("signin_code", "0123")
	resp = client.postForm("/admin/activities", values)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid activity status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var activityID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM activities WHERE name = $1 ORDER BY id DESC LIMIT 1`, name).Scan(&activityID); err != nil {
		t.Fatalf("load created activity: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM activities WHERE id = $1`, activityID) })

	created, err := database.GetActivity(ctx, strconv.FormatInt(activityID, 10))
	if err != nil {
		t.Fatalf("reload activity: %v", err)
	}
	if !created.SigninEnabled || created.SigninCode != "0123" {
		t.Fatalf("created activity signin = %+v", created)
	}
	if created.GroupEnabled && created.OnetooneEnabled {
		t.Fatal("created activity opened both matching modes")
	}

	// The workbench exposes a broadcast entry that deep-links to its tab.
	resp = client.get("/admin/activities/" + strconv.FormatInt(activityID, 10))
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `href="#broadcast"`) {
		t.Fatalf("workbench broadcast entry missing: status=%d", resp.StatusCode)
	}
	resp = client.get("/admin/activities")
	if body = readBody(t, resp); !strings.Contains(body, fmt.Sprintf("/admin/activities/%d#broadcast", activityID)) {
		t.Fatalf("activity list broadcast entry missing: status=%d", resp.StatusCode)
	}
}

// TestIntegrationAdminErrorPages covers ADM-04/05: admin paths must render the
// admin shell (with 返回管理台 / 重新登录) instead of the user-facing error page
// whose only link is blocked inside the iframe.
func TestIntegrationAdminErrorPages(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	admin := createMessageUser(t, ctx, database, pool, "错误页管理员")
	if _, err := database.AssignAdminRole(ctx, admin.ID, "super_admin", true); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	reviewer := createMessageUser(t, ctx, database, pool, "错误页审核员")
	if _, err := database.AssignAdminRole(ctx, reviewer.ID, "reviewer", true); err != nil {
		t.Fatalf("assign reviewer: %v", err)
	}
	client := adminSessionClient(t, database, server, admin)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{admin.ID, reviewer.ID})
	})
	iframe := map[string]string{"Sec-Fetch-Dest": "iframe"}

	resp := client.getWithHeaders("/admin/users/999999999", iframe)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing user status = %d", resp.StatusCode)
	}
	for _, want := range []string{`class="admin-topbar"`, "返回管理台", "重新登录", "找不到用户"} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin 404 missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "回到首页") {
		t.Fatal("admin 404 still renders the user-facing error page")
	}

	resp = client.getWithHeaders("/admin/definitely-not-a-page", iframe)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `class="admin-topbar"`) {
		t.Fatalf("unmatched admin path status=%d body=%s", resp.StatusCode, body)
	}

	// Session expiry inside the iframe: an invalidated session still carries a
	// cookie, so the admin page must render 401 instead of a 303 to /login.
	anon := newWSClient(t, server.URL)
	staleCookie := testConfig().SessionCookieName + "=expired-or-revoked-session"
	resp = anon.getWithHeaders("/admin/users", map[string]string{
		"Sec-Fetch-Dest": "iframe",
		"Cookie":         staleCookie,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d, want 401", resp.StatusCode)
	}
	for _, want := range []string{"登录已过期", "重新登录", `class="admin-topbar"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expired session page missing %q", want)
		}
	}
	// A visitor with no session at all keeps the login redirect (CLI/tests).
	resp = anon.getWithHeaders("/admin/users", iframe)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous iframe status = %d, want 303 to login", resp.StatusCode)
	}

	// Permission failures and CSRF failures also stay inside the admin shell.
	reviewerClient := adminSessionClient(t, database, server, reviewer)
	resp = reviewerClient.getWithHeaders("/admin/roles", iframe)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, `class="admin-topbar"`) {
		t.Fatalf("permission error status=%d body=%s", resp.StatusCode, body)
	}
	resp = client.postForm("/admin/users/"+strconv.FormatInt(reviewer.ID, 10)+"/approve", url.Values{})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, `class="admin-topbar"`) {
		t.Fatalf("csrf error status=%d body=%s", resp.StatusCode, body)
	}
}

// TestIntegrationPhoneChangeRequiresPassword covers UD-05: changing the login
// phone needs the current password, stays unique, and is audited.
func TestIntegrationPhoneChangeRequiresPassword(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-PHONECHANGE", "手机号用户", "active", "user")
	other, _ := env.createUser(t, "U-PHONEOTHER", "另一用户", "active", "user")
	password := "phone-change-pass-1"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO auth_credentials (user_id, password_hash) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		owner.ID, hash); err != nil {
		t.Fatalf("set password: %v", err)
	}
	csrf := ownerClient.csrfCookie()
	newPhone := "1" + randomHTTPDigits(t, 10)

	// No password: rejected before anything is written.
	resp := profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"手机号": newPhone})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "password_required") {
		t.Fatalf("missing password status=%d body=%s", resp.StatusCode, body)
	}
	reloaded, err := env.database.GetProfile(env.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Phone == newPhone {
		t.Fatal("phone changed without the current password")
	}

	// Wrong password: rejected.
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"手机号": newPhone, "current_password": "wrong-password"})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "password_invalid") {
		t.Fatalf("wrong password status=%d body=%s", resp.StatusCode, body)
	}

	// Correct password: applied and audited with masked values only.
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"手机号": newPhone, "current_password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("phone change status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	reloaded, err = env.database.GetProfile(env.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Phone != newPhone {
		t.Fatalf("phone = %q, want %q", reloaded.Phone, newPhone)
	}
	var meta string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'profile.phone.changed' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`,
		strconv.FormatInt(owner.ID, 10)).Scan(&meta); err != nil {
		t.Fatalf("query phone audit: %v", err)
	}
	if strings.Contains(meta, newPhone) {
		t.Fatal("audit metadata leaked the full phone number")
	}
	var metaMap map[string]any
	if err := json.Unmarshal([]byte(meta), &metaMap); err != nil {
		t.Fatalf("decode audit meta: %v", err)
	}
	if metaMap["new_phone"] == nil || metaMap["old_phone"] == nil {
		t.Fatalf("audit meta = %v", metaMap)
	}

	// Updating other fields without touching the phone needs no password.
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"昵称": "手机号用户改", "手机号": newPhone})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-phone update status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	// Another account's phone: 409 with a stable code.
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"手机号": other.Phone, "current_password": password})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "phone_taken") {
		t.Fatalf("duplicate phone status=%d body=%s", resp.StatusCode, body)
	}
}
