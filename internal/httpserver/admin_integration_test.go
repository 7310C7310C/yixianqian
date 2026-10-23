package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationAdminHTTP covers server-side RBAC, CSRF, review actions,
// temporary passwords, forced password changes, reports, suggestions, role
// assignment, the last super_admin guard and audit logging.
func TestIntegrationAdminHTTP(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE legacy_user_id LIKE 'U-ADMHTTP-%'`); err != nil {
		t.Fatalf("clean stale users: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM reports WHERE reason LIKE 'ADMHTTP-%'`); err != nil {
		t.Fatalf("clean stale reports: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM suggestions WHERE content LIKE 'ADMHTTP-%'`); err != nil {
		t.Fatalf("clean stale suggestions: %v", err)
	}

	super := createMessageUser(t, ctx, database, pool, "最高管理员")
	reviewer := createMessageUser(t, ctx, database, pool, "审核员")
	moderator := createMessageUser(t, ctx, database, pool, "内容管理员")
	plain := createMessageUser(t, ctx, database, pool, "普通用户")
	target := createMessageUser(t, ctx, database, pool, "临时密码用户")
	pending, err := database.UpsertLegacyUser(ctx,
		"U-ADMHTTP-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10), "待审核用户")
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	pending2, err := database.UpsertLegacyUser(ctx,
		"U-ADMHTTP-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10), "待审核用户二")
	if err != nil {
		t.Fatalf("create second pending user: %v", err)
	}
	loginTarget := createMessageUser(t, ctx, database, pool, "临时登录用户")

	ids := []int64{super.ID, reviewer.ID, moderator.ID, plain.ID,
		target.ID, pending.ID, pending2.ID, loginTarget.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM reports WHERE reason LIKE 'ADMHTTP-%'`)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM suggestions WHERE content LIKE 'ADMHTTP-%'`)
	})

	must := func(user db.User, role string) {
		t.Helper()
		if changed, err := database.AssignAdminRole(ctx, user.ID, role, true); err != nil || !changed {
			t.Fatalf("assign %s to %d: changed=%v err=%v", role, user.ID, changed, err)
		}
	}
	must(super, "super_admin")
	must(reviewer, "reviewer")
	must(moderator, "content_moderator")

	plainClient := loginMessageClient(t, database, server, plain)
	reviewerClient := loginMessageClient(t, database, server, reviewer)
	moderatorClient := loginMessageClient(t, database, server, moderator)
	superClient := loginMessageClient(t, database, server, super)

	// Non-admin accounts cannot even enter the console.
	resp := plainClient.get("/admin")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain /admin status = %d", resp.StatusCode)
	}
	resp = plainClient.get("/api/admin/me")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain /api/admin/me status = %d", resp.StatusCode)
	}

	// Reviewer gets the review permission but not admin.manage.
	resp = reviewerClient.get("/api/admin/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviewer /api/admin/me status = %d", resp.StatusCode)
	}
	var me adminMeJSON
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.SuperAdmin {
		t.Fatalf("reviewer should not be super admin: %+v", me)
	}
	if !containsString(me.Permissions, "user.review") {
		t.Fatalf("reviewer permissions = %v", me.Permissions)
	}
	if containsString(me.Permissions, "admin.manage") {
		t.Fatalf("reviewer unexpectedly has admin.manage: %v", me.Permissions)
	}

	resp = reviewerClient.get("/admin")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "总览") {
		t.Fatalf("reviewer /admin status = %d", resp.StatusCode)
	}

	// 管理台只有 SPA 一个入口：顶层 document 一律 303 回 /?admin=1，
	// 不渲染顶层页面。iframe 内部导航（无 embed=1）仍渲染 SSR 管理台。
	for _, tc := range []struct {
		target string
	}{
		{"/admin"},
		{"/admin/users?status=pending"},
		{"/admin?embed=1"},
	} {
		req, err := http.NewRequest(http.MethodGet, reviewerClient.base+tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Dest", "document")
		resp = reviewerClient.do(req)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("top-level %s status = %d location = %q, want 303",
				tc.target, resp.StatusCode, resp.Header.Get("Location"))
		}
		if loc := resp.Header.Get("Location"); loc != "/?admin=1" {
			t.Fatalf("top-level %s location = %q, want /?admin=1", tc.target, loc)
		}
		if body := readBody(t, resp); strings.Contains(body, "admin-nav") {
			t.Fatalf("top-level %s 不应渲染管理台（body 长度 %d）", tc.target, len(body))
		}
		resp.Body.Close()
	}
	req, err := http.NewRequest(http.MethodGet, reviewerClient.base+"/admin/users?status=pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	resp = reviewerClient.do(req)
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), pending.Name) {
		t.Fatalf("iframe /admin/users status = %d", resp.StatusCode)
	}
	resp = reviewerClient.get("/admin/users?status=pending&q=" + url.QueryEscape("待审核用户"))
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), pending.Name) {
		t.Fatalf("reviewer user list status = %d", resp.StatusCode)
	}

	// CSRF is mandatory for HTML writes.
	csrf := reviewerClient.pageCSRF("/admin/users?status=pending")
	resp = reviewerClient.postForm("/admin/users/"+strconv.FormatInt(pending.ID, 10)+"/approve", url.Values{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("approve without csrf status = %d", resp.StatusCode)
	}
	resp = reviewerClient.postForm("/admin/users/"+strconv.FormatInt(pending.ID, 10)+"/approve",
		url.Values{"csrf_token": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reviewer approve status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	approved, err := database.GetAdminUser(ctx, pending.ID)
	if err != nil || approved.AccountStatus != "active" || approved.LoginStatus != "active" {
		t.Fatalf("approved user = %+v err=%v", approved, err)
	}

	// Reviewer cannot use user.manage or admin.manage.
	resp = reviewerClient.postForm("/admin/users/"+strconv.FormatInt(target.ID, 10)+"/disable",
		url.Values{"csrf_token": {csrf}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer disable status = %d", resp.StatusCode)
	}
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(target.ID, 10)+"/disable",
		reviewerClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer api disable status = %d", resp.StatusCode)
	}
	resp = reviewerClient.get("/admin/roles")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer roles page status = %d", resp.StatusCode)
	}
	resp = reviewerClient.postForm("/admin/roles/assign", url.Values{
		"csrf_token": {csrf}, "user_ref": {adminUserRef(plain)}, "role": {"super_admin"}, "action": {"assign"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer role assign status = %d", resp.StatusCode)
	}

	// Reviewer can approve through JSON too.
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(pending2.ID, 10)+"/approve",
		reviewerClient.csrfCookie(), map[string]any{"note": "API 审核"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviewer api approve status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	// Audit row exists for the HTML approval.
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.user.approve' AND actor_user_id = $1 AND target_id = $2`,
		reviewer.ID, strconv.FormatInt(pending.ID, 10)).Scan(&auditCount); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("approve action was not audited")
	}

	// Super admin sees roles and the assignment form.
	resp = superClient.get("/admin/roles")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super /admin/roles status = %d", resp.StatusCode)
	}
	rolesBody := readBody(t, resp)
	if !strings.Contains(rolesBody, "super_admin") || !strings.Contains(rolesBody, "指派角色") {
		t.Fatalf("roles page missing content: %s", rolesBody)
	}

	// The last super_admin cannot remove its own role.
	resp = superClient.postJSON("/api/admin/roles/assign", superClient.csrfCookie(), map[string]any{
		"user_ref": adminUserRef(super), "role": "super_admin", "action": "remove",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("last super_admin remove status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var apiErr struct {
		Error apiError `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr.Error.Code != "last_super_admin" {
		t.Fatalf("last super_admin code = %q", apiErr.Error.Code)
	}

	// Temporary password: returned once over JSON, stored as a hash and
	// flagged for forced change.
	resp = superClient.postJSON("/api/admin/users/"+strconv.FormatInt(target.ID, 10)+"/temp-password",
		superClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("temp password status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var tempResp struct {
		OK           bool   `json:"ok"`
		TempPassword string `json:"temp_password"`
		MustChange   bool   `json:"must_change"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tempResp); err != nil {
		t.Fatal(err)
	}
	if !tempResp.OK || !tempResp.MustChange || len(tempResp.TempPassword) < auth.MinPasswordLength || tempResp.ExpiresAt == "" {
		t.Fatalf("temp password response = %+v", tempResp)
	}
	storedHash, err := database.GetPasswordHash(ctx, target.ID)
	if err != nil {
		t.Fatalf("load temp hash: %v", err)
	}
	if storedHash == tempResp.TempPassword {
		t.Fatal("temp password stored in clear text")
	}
	if ok, err := auth.VerifyPassword(tempResp.TempPassword, storedHash); err != nil || !ok {
		t.Fatalf("verify temp password: ok=%v err=%v", ok, err)
	}
	mustChange, _, err := database.GetCredentialState(ctx, target.ID)
	if err != nil || !mustChange {
		t.Fatalf("must_change flag = %v err=%v", mustChange, err)
	}
	var auditMeta string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.user.temp_password' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`, strconv.FormatInt(target.ID, 10)).Scan(&auditMeta); err != nil {
		t.Fatalf("query temp audit: %v", err)
	}
	if strings.Contains(auditMeta, tempResp.TempPassword) {
		t.Fatal("audit metadata leaked the temporary password")
	}

	// A session holding a temporary password is restricted to /password.
	mustChangeClient := adminSessionClient(t, database, server, target)
	resp = mustChangeClient.get("/")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/password?required=1" {
		t.Fatalf("forced change status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp = mustChangeClient.get("/api/admin/me")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forced change api status = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr.Error.Code != "password_change_required" {
		t.Fatalf("forced change api code = %q", apiErr.Error.Code)
	}
	passwordCSRF := mustChangeClient.pageCSRF("/password")
	newPassword := "admin-http-new-password-1"
	resp = mustChangeClient.postForm("/password", url.Values{
		"csrf_token":       {passwordCSRF},
		"current_password": {tempResp.TempPassword},
		"password":         {newPassword},
		"password_confirm": {newPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustChangeClient.get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("home after password change status = %d", resp.StatusCode)
	}
	mustChange, _, err = database.GetCredentialState(ctx, target.ID)
	if err != nil || mustChange {
		t.Fatalf("must_change after change = %v err=%v", mustChange, err)
	}

	// Login with a temporary password redirects to the forced-change page.
	resp = superClient.postJSON("/api/admin/users/"+strconv.FormatInt(loginTarget.ID, 10)+"/temp-password",
		superClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login target temp password status = %d", resp.StatusCode)
	}
	var loginTemp struct {
		TempPassword string `json:"temp_password"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginTemp); err != nil {
		t.Fatal(err)
	}
	loginClient := newWSClient(t, server.URL)
	loginCSRF := loginClient.pageCSRF("/login")
	resp = loginClient.postForm("/login", url.Values{
		"csrf_token": {loginCSRF},
		"phone":      {loginTarget.Phone},
		"password":   {loginTemp.TempPassword},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/password?required=1" {
		t.Fatalf("temp login status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Reports: list and resolve one pending report.
	var reportID int64
	reason := "ADMHTTP-" + randomHTTPDigits(t, 8) + " 不当行为"
	if err := pool.QueryRow(ctx,
		`INSERT INTO reports (reporter_name, target_name, reason)
		 VALUES ('ADMHTTP 举报人', 'ADMHTTP 对象', $1) RETURNING id`, reason).Scan(&reportID); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	resp = moderatorClient.get("/admin/reports?status=pending")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), reason) {
		t.Fatalf("reports page status = %d", resp.StatusCode)
	}
	reportCSRF := moderatorClient.pageCSRF("/admin/reports?status=pending")
	resp = moderatorClient.postForm("/admin/reports/"+strconv.FormatInt(reportID, 10)+"/handle",
		url.Values{"csrf_token": {reportCSRF}, "action": {"resolve"}, "note": {"已联系"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("handle report status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var reportStatus string
	var handledBy *int64
	if err := pool.QueryRow(ctx,
		`SELECT status, handled_by FROM reports WHERE id = $1`, reportID).
		Scan(&reportStatus, &handledBy); err != nil {
		t.Fatalf("load handled report: %v", err)
	}
	if reportStatus != db.ReportStatusApproved || handledBy == nil || *handledBy != moderator.ID {
		t.Fatalf("handled report status=%q handled_by=%v", reportStatus, handledBy)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.report.handle' AND actor_user_id = $1 AND target_id = $2`,
		moderator.ID, strconv.FormatInt(reportID, 10)).Scan(&auditCount); err != nil {
		t.Fatalf("query report audit: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("report handling was not audited")
	}

	// Suggestions: list and mark handled.
	content := "ADMHTTP-" + randomHTTPDigits(t, 8) + " 希望增加夜间模式"
	var suggestionID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO suggestions (author_name, type, content)
		 VALUES ('ADMHTTP 反馈人', '功能建议', $1) RETURNING id`, content).Scan(&suggestionID); err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}
	resp = moderatorClient.get("/admin/suggestions?status=pending")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), content) {
		t.Fatalf("suggestions page status = %d", resp.StatusCode)
	}
	suggestionCSRF := moderatorClient.pageCSRF("/admin/suggestions?status=pending")
	resp = moderatorClient.postForm("/admin/suggestions/"+strconv.FormatInt(suggestionID, 10)+"/handle",
		url.Values{"csrf_token": {suggestionCSRF}, "action": {"handle"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("handle suggestion status = %d", resp.StatusCode)
	}
	var suggestionStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM suggestions WHERE id = $1`, suggestionID).Scan(&suggestionStatus); err != nil {
		t.Fatalf("load suggestion: %v", err)
	}
	if suggestionStatus != db.AdminSuggestionHandled {
		t.Fatalf("suggestion status = %q", suggestionStatus)
	}
}

// adminSessionClient creates a session without touching the home page, which
// may be blocked by the forced password-change middleware.
func adminSessionClient(t *testing.T, database *db.DB, server *httptest.Server, user db.User) *wsClient {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(context.Background(), user.ID, hash, time.Hour, "", "admin-http-test"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	client := newWSClient(t, server.URL)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.jar.SetCookies(u, []*http.Cookie{{
		Name:  testConfig().SessionCookieName,
		Value: raw,
		Path:  "/",
	}})
	return client
}

func adminUserRef(u db.User) string {
	if u.LegacyUserID != nil && *u.LegacyUserID != "" {
		return *u.LegacyUserID
	}
	return strconv.FormatInt(u.ID, 10)
}
