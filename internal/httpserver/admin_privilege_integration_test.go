package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationAdminPrivilegeBoundaries covers the vertical RBAC limits of
// the admin console: temporary-password resets, the review state machine and
// audit metadata. It complements TestIntegrationAdminHTTP, which covers the
// happy paths.
func TestIntegrationAdminPrivilegeBoundaries(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)

	super := createMessageUser(t, ctx, database, pool, "最高管理员权限")
	admin := createMessageUser(t, ctx, database, pool, "管理员权限")
	admin2 := createMessageUser(t, ctx, database, pool, "管理员权限二")
	reviewer := createMessageUser(t, ctx, database, pool, "审核员权限")
	activePlain := createMessageUser(t, ctx, database, pool, "普通已通过")
	pendingPlain, err := database.UpsertLegacyUser(ctx,
		"U-ADMPRIV-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10), "普通待审核")
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}

	ids := []int64{super.ID, admin.ID, admin2.ID, reviewer.ID, activePlain.ID, pendingPlain.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	assign := func(user db.User, role string) {
		t.Helper()
		if changed, err := database.AssignAdminRole(ctx, user.ID, role, true); err != nil || !changed {
			t.Fatalf("assign %s to %d: changed=%v err=%v", role, user.ID, changed, err)
		}
	}
	assign(super, "super_admin")
	assign(admin, "admin")
	assign(admin2, "admin")
	assign(reviewer, "reviewer")

	superClient := loginMessageClient(t, database, server, super)
	adminClient := loginMessageClient(t, database, server, admin)
	reviewerClient := loginMessageClient(t, database, server, reviewer)

	tempPath := func(id int64) string {
		return "/api/admin/users/" + strconv.FormatInt(id, 10) + "/temp-password"
	}
	decodeAPIError := func(resp *http.Response) apiError {
		t.Helper()
		var payload struct {
			Error apiError `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode api error: %v", err)
		}
		return payload.Error
	}

	// Anonymous requests never reach the handler.
	anon := newWSClient(t, server.URL)
	resp := anon.postJSON(tempPath(admin2.ID), "", map[string]any{})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous temp password status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing CSRF header is rejected before any password change.
	resp = adminClient.postJSON(tempPath(admin2.ID), "", map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("temp password without csrf status = %d", resp.StatusCode)
	}
	if code := decodeAPIError(resp); code.Code != "csrf_invalid" {
		t.Fatalf("temp password without csrf code = %q", code.Code)
	}
	if _, _, err := database.GetCredentialState(ctx, admin2.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("credential created without csrf: %v", err)
	}

	// admin cannot reset a super admin, over JSON or the HTML form.
	resp = adminClient.postJSON(tempPath(super.ID), adminClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin reset super status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if code := decodeAPIError(resp); code.Code != "target_admin_forbidden" {
		t.Fatalf("admin reset super code = %q", code.Code)
	}
	adminCSRF := adminClient.pageCSRF("/admin/users")
	resp = adminClient.postForm("/admin/users/"+strconv.FormatInt(super.ID, 10)+"/temp-password",
		url.Values{"csrf_token": {adminCSRF}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin reset super html status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = superClient.get("/api/admin/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super session after blocked reset = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// admin cannot reset another admin either.
	resp = adminClient.postJSON(tempPath(admin2.ID), adminClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin reset admin status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if code := decodeAPIError(resp); code.Code != "target_admin_forbidden" {
		t.Fatalf("admin reset admin code = %q", code.Code)
	}
	if _, _, err := database.GetCredentialState(ctx, admin2.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("blocked reset still wrote a credential: %v", err)
	}

	// admin may reset an ordinary user; the audit row names roles but never
	// the temporary password.
	resp = adminClient.postJSON(tempPath(activePlain.ID), adminClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin reset plain status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var tempResp struct {
		TempPassword string `json:"temp_password"`
		MustChange   bool   `json:"must_change"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tempResp); err != nil {
		t.Fatal(err)
	}
	if len(tempResp.TempPassword) < auth.MinPasswordLength || !tempResp.MustChange {
		t.Fatalf("temp password response = %+v", tempResp)
	}
	hash, err := database.GetPasswordHash(ctx, activePlain.ID)
	if err != nil {
		t.Fatalf("load plain hash: %v", err)
	}
	if ok, err := auth.VerifyPassword(tempResp.TempPassword, hash); err != nil || !ok {
		t.Fatalf("verify temp password: ok=%v err=%v", ok, err)
	}
	var (
		auditActor  int64
		auditMeta   string
		targetRoles string
		actorRoles  string
		actorSuper  string
	)
	if err := pool.QueryRow(ctx,
		`SELECT actor_user_id, metadata::text,
		        COALESCE(metadata->>'target_roles', ''),
		        COALESCE(metadata->>'actor_roles', ''),
		        COALESCE(metadata->>'actor_super', '')
		 FROM audit_log
		 WHERE action = 'admin.user.temp_password' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`,
		strconv.FormatInt(activePlain.ID, 10)).
		Scan(&auditActor, &auditMeta, &targetRoles, &actorRoles, &actorSuper); err != nil {
		t.Fatalf("query temp password audit: %v", err)
	}
	if auditActor != admin.ID {
		t.Fatalf("temp password audit actor = %d, want %d", auditActor, admin.ID)
	}
	if targetRoles != "[]" || !strings.Contains(actorRoles, "admin") || actorSuper != "false" {
		t.Fatalf("temp password audit roles: target=%q actor=%q super=%q", targetRoles, actorRoles, actorSuper)
	}
	if strings.Contains(auditMeta, tempResp.TempPassword) || strings.Contains(auditMeta, "temp_password") {
		t.Fatalf("audit metadata leaked the temporary password: %s", auditMeta)
	}

	// Reviewer approval is limited to the review workflow: an active account
	// is not reviewable, and reviewer has no user.manage.
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(activePlain.ID, 10)+"/approve",
		reviewerClient.csrfCookie(), map[string]any{"note": "越权通过"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("reviewer approve active status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if code := decodeAPIError(resp); code.Code != "user_not_reviewable" {
		t.Fatalf("reviewer approve active code = %q", code.Code)
	}
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(activePlain.ID, 10)+"/reject",
		reviewerClient.csrfCookie(), map[string]any{"note": "越权拒绝"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("reviewer reject active status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if code := decodeAPIError(resp); code.Code != "user_not_reviewable" {
		t.Fatalf("reviewer reject active code = %q", code.Code)
	}
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(activePlain.ID, 10)+"/disable",
		reviewerClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer disable status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Normal review still works, but an admin cannot approve an active user.
	resp = reviewerClient.postJSON("/api/admin/users/"+strconv.FormatInt(pendingPlain.ID, 10)+"/approve",
		reviewerClient.csrfCookie(), map[string]any{"note": "资料完整"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviewer approve pending status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	resp = adminClient.postJSON("/api/admin/users/"+strconv.FormatInt(activePlain.ID, 10)+"/approve",
		adminClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("admin approve active status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if code := decodeAPIError(resp); code.Code != "user_not_reviewable" {
		t.Fatalf("admin approve active code = %q", code.Code)
	}

	// Super admin may reset an admin; audit records the protected role.
	resp = superClient.postJSON(tempPath(admin2.ID), superClient.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super reset admin status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&tempResp); err != nil {
		t.Fatal(err)
	}
	admin2Hash, err := database.GetPasswordHash(ctx, admin2.ID)
	if err != nil {
		t.Fatalf("load admin2 hash: %v", err)
	}
	if ok, err := auth.VerifyPassword(tempResp.TempPassword, admin2Hash); err != nil || !ok {
		t.Fatalf("verify admin2 temp password: ok=%v err=%v", ok, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(metadata->>'target_roles', '')
		 FROM audit_log
		 WHERE action = 'admin.user.temp_password' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`,
		strconv.FormatInt(admin2.ID, 10)).Scan(&targetRoles); err != nil {
		t.Fatalf("query admin reset audit: %v", err)
	}
	if !strings.Contains(targetRoles, "admin") {
		t.Fatalf("admin reset audit target_roles = %q", targetRoles)
	}
}
