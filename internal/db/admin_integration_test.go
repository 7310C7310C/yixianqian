package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
)

// TestIntegrationAdminRBAC covers the RBAC queries, the last super_admin
// guard, account review transitions, temporary passwords and report handling.
func TestIntegrationAdminRBAC(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Remove stale rows from an interrupted previous run; the prefix is
	// unique to this test and keeps reruns deterministic.
	if _, err := database.pool.Exec(ctx,
		`DELETE FROM users WHERE legacy_user_id LIKE 'U-ADMIN-%'`); err != nil {
		t.Fatalf("clean stale admin users: %v", err)
	}

	newUser := func(name string) User {
		u, err := database.UpsertLegacyUser(ctx,
			"U-ADMIN-"+randomDigits(t, 10), "1"+randomDigits(t, 10), name)
		if err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		return u
	}

	super1 := newUser("最高管理员一")
	super2 := newUser("最高管理员二")
	reviewer := newUser("审核员")
	plain := newUser("普通用户")
	t.Cleanup(func() {
		_, _ = database.pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = ANY($1)`,
			[]int64{super1.ID, super2.ID, reviewer.ID, plain.ID})
	})

	// super_admin is unconditional.
	if ok, err := database.UserHasAdminPermission(ctx, super1.ID, adminPermCodeForTest); err != nil || ok {
		t.Fatalf("plain permission before role = %v err=%v", ok, err)
	}
	if changed, err := database.AssignAdminRole(ctx, super1.ID, "super_admin", true); err != nil || !changed {
		t.Fatalf("assign super_admin: changed=%v err=%v", changed, err)
	}
	if ok, err := database.IsSuperAdmin(ctx, super1.ID); err != nil || !ok {
		t.Fatalf("is super admin = %v err=%v", ok, err)
	}
	if ok, err := database.UserHasAdminPermission(ctx, super1.ID, "not.a.real.permission"); err != nil || !ok {
		t.Fatalf("super_admin should own every permission: ok=%v err=%v", ok, err)
	}
	perms, err := database.UserPermissions(ctx, super1.ID)
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	if !slices.Contains(perms, adminPermCodeForTest) {
		t.Fatalf("super_admin permissions missing %q: %v", adminPermCodeForTest, perms)
	}

	// reviewer only sees the seeded reviewer permissions.
	if changed, err := database.AssignAdminRole(ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}
	if ok, err := database.UserHasAdminPermission(ctx, reviewer.ID, "user.review"); err != nil || !ok {
		t.Fatalf("reviewer user.review = %v err=%v", ok, err)
	}
	if ok, err := database.UserHasAdminPermission(ctx, reviewer.ID, "admin.manage"); err != nil || ok {
		t.Fatalf("reviewer admin.manage = %v err=%v", ok, err)
	}

	// The only super_admin cannot lose the role.
	if _, err := database.AssignAdminRole(ctx, super1.ID, "super_admin", false); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("remove last super_admin err = %v", err)
	}
	if _, err := database.SetAdminUserEnabled(ctx, super1.ID, reviewer.ID, false); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("disable last super_admin err = %v", err)
	}
	if _, err := database.RejectAdminUser(ctx, super1.ID, reviewer.ID, "不应成功"); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("reject last super_admin err = %v", err)
	}
	if changed, err := database.AssignAdminRole(ctx, super2.ID, "super_admin", true); err != nil || !changed {
		t.Fatalf("assign second super_admin: changed=%v err=%v", changed, err)
	}
	if changed, err := database.AssignAdminRole(ctx, super1.ID, "super_admin", false); err != nil || !changed {
		t.Fatalf("remove first super_admin: changed=%v err=%v", changed, err)
	}
	if _, err := database.AssignAdminRole(ctx, super2.ID, "super_admin", false); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("remove remaining super_admin err = %v", err)
	}

	// Review transitions.
	previous, err := database.ApproveAdminUser(ctx, plain.ID, super2.ID, "资料完整")
	if err != nil || previous != "pending" {
		t.Fatalf("approve: previous=%q err=%v", previous, err)
	}
	approved, err := database.GetAdminUser(ctx, plain.ID)
	if err != nil {
		t.Fatalf("load approved user: %v", err)
	}
	if approved.AccountStatus != "active" || approved.LoginStatus != "active" || approved.ReviewedAt == nil {
		t.Fatalf("approved user = %+v", approved)
	}

	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	_ = raw
	if _, err := database.CreateSession(ctx, plain.ID, hash, time.Hour, "", "admin-db-test"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	previous, err = database.RejectAdminUser(ctx, plain.ID, super2.ID, "资料不实")
	if err != nil || previous != "active" {
		t.Fatalf("reject: previous=%q err=%v", previous, err)
	}
	if _, _, err := database.GetSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected user session should be revoked: %v", err)
	}
	rejected, err := database.GetAdminUser(ctx, plain.ID)
	if err != nil || rejected.AccountStatus != "rejected" || rejected.LoginStatus != "disabled" {
		t.Fatalf("rejected user = %+v err=%v", rejected, err)
	}
	if _, err := database.SetAdminUserEnabled(ctx, plain.ID, super2.ID, true); !errors.Is(err, ErrAdminUserRejected) {
		t.Fatalf("enable rejected user err = %v", err)
	}
	if _, err := database.ApproveAdminUser(ctx, plain.ID, super2.ID, "覆核通过"); err != nil {
		t.Fatalf("re-approve rejected user: %v", err)
	}

	// Disable/enable only toggles the login flag and revokes sessions.
	raw, hash, err = auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	_ = raw
	if _, err := database.CreateSession(ctx, plain.ID, hash, time.Hour, "", "admin-db-test"); err != nil {
		t.Fatalf("create session before disable: %v", err)
	}
	if _, err := database.SetAdminUserEnabled(ctx, plain.ID, super2.ID, false); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, _, err := database.GetSession(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user session should be revoked: %v", err)
	}
	disabled, err := database.GetAdminUser(ctx, plain.ID)
	if err != nil || disabled.LoginStatus != "disabled" || disabled.AccountStatus != "active" {
		t.Fatalf("disabled user = %+v err=%v", disabled, err)
	}
	if _, err := database.SetAdminUserEnabled(ctx, plain.ID, super2.ID, true); err != nil {
		t.Fatalf("enable user: %v", err)
	}
	enabled, err := database.GetAdminUser(ctx, plain.ID)
	if err != nil || enabled.LoginStatus != "active" {
		t.Fatalf("enabled user = %+v err=%v", enabled, err)
	}

	// Temporary password: hashed, forced change and session revocation.
	tempRaw, err := auth.GenerateTempPassword()
	if err != nil {
		t.Fatal(err)
	}
	tempHash, err := auth.HashPassword(tempRaw)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	if err := database.AdminSetTempPassword(ctx, plain.ID, tempHash, expiresAt, super2.ID); err != nil {
		t.Fatalf("set temp password: %v", err)
	}
	storedHash, err := database.GetPasswordHash(ctx, plain.ID)
	if err != nil {
		t.Fatalf("load temp hash: %v", err)
	}
	if storedHash == tempRaw {
		t.Fatal("temporary password stored in clear text")
	}
	if ok, err := auth.VerifyPassword(tempRaw, storedHash); err != nil || !ok {
		t.Fatalf("verify temp password: ok=%v err=%v", ok, err)
	}
	mustChange, gotExpiry, err := database.GetCredentialState(ctx, plain.ID)
	if err != nil || !mustChange || gotExpiry == nil {
		t.Fatalf("credential state = %v %v err=%v", mustChange, gotExpiry, err)
	}

	newRaw := "new-password-for-admin-test"
	newHash, err := auth.HashPassword(newRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ChangePassword(ctx, plain.ID, newHash, 0); err != nil {
		t.Fatalf("change password: %v", err)
	}
	mustChange, gotExpiry, err = database.GetCredentialState(ctx, plain.ID)
	if err != nil || mustChange || gotExpiry != nil {
		t.Fatalf("credential state after change = %v %v err=%v", mustChange, gotExpiry, err)
	}
	if ok, err := auth.VerifyPassword(newRaw, storedHash); err != nil || ok {
		t.Fatalf("old hash should not verify new password: ok=%v err=%v", ok, err)
	}

	// Reports: user report resolve and message report delete.
	var userReportID int64
	if err := database.pool.QueryRow(ctx,
		`INSERT INTO reports (reporter_name, target_name, reason)
		 VALUES ('举报人', '被举报人', '不当行为') RETURNING id`).Scan(&userReportID); err != nil {
		t.Fatalf("insert user report: %v", err)
	}
	status, err := database.HandleAdminReport(ctx, userReportID, super2.ID, AdminReportResolve)
	if err != nil || status != ReportStatusApproved {
		t.Fatalf("resolve report: status=%q err=%v", status, err)
	}

	author := newUser("留言作者")
	target := newUser("留言对象")
	reporter := newUser("留言举报人")
	t.Cleanup(func() {
		_, _ = database.pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = ANY($1)`,
			[]int64{author.ID, target.ID, reporter.ID})
	})
	var messageID int64
	if err := database.pool.QueryRow(ctx,
		`INSERT INTO messages (target_user_id, author_id, content)
		 VALUES ($1, $2, '违规留言') RETURNING id`, target.ID, author.ID).Scan(&messageID); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	messageReportID, already, err := database.CreateMessageReport(ctx, ReportMessageParams{
		MessageID: messageID, ReporterID: reporter.ID, Reason: "不当内容",
	})
	if err != nil || already {
		t.Fatalf("create message report: id=%d already=%v err=%v", messageReportID, already, err)
	}
	status, err = database.HandleAdminReport(ctx, messageReportID, super2.ID, AdminReportDelete)
	if err != nil || status != ReportStatusApproved {
		t.Fatalf("delete message report: status=%q err=%v", status, err)
	}
	handled, err := database.GetMessageByID(ctx, messageID)
	if err != nil || handled.Status != MessageStatusDeleted {
		t.Fatalf("handled message = %+v err=%v", handled, err)
	}

	// Suggestions can be marked handled.
	var suggestionID int64
	if err := database.pool.QueryRow(ctx,
		`INSERT INTO suggestions (author_name, type, content)
		 VALUES ('反馈人', '功能建议', '希望有夜间模式') RETURNING id`).Scan(&suggestionID); err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}
	if err := database.HandleAdminSuggestion(ctx, suggestionID, AdminSuggestionHandled); err != nil {
		t.Fatalf("handle suggestion: %v", err)
	}
	handledSuggestions, err := database.ListAdminSuggestions(ctx, AdminSuggestionHandled, 50)
	if err != nil {
		t.Fatalf("list handled suggestions: %v", err)
	}
	found := false
	for _, s := range handledSuggestions {
		if s.ID == suggestionID {
			found = true
		}
	}
	if !found {
		t.Fatal("handled suggestion not listed")
	}
}

const adminPermCodeForTest = "user.review"
