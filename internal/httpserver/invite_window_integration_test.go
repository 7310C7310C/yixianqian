package httpserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationApprovalStartsInviteWindow covers the full HTTP chain:
// reviewer approves a pending account, the invite window starts with a fixed
// due date, and the settlement job pays the inviter once.
func TestIntegrationApprovalStartsInviteWindow(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	reviewer := createMessageUser(t, ctx, database, pool, "邀请审核员")
	inviter := createMessageUser(t, ctx, database, pool, "邀请人")
	invitee := createMessageUser(t, ctx, database, pool, "被邀请人")
	ids := []int64{reviewer.ID, inviter.ID, invitee.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if changed, err := database.AssignAdminRole(ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET account_status = 'pending', gender = '女性' WHERE id = $1`, invitee.ID); err != nil {
		t.Fatalf("make invitee pending: %v", err)
	}
	inviteID, status, err := database.RecordInviteFromForm(ctx,
		invitee.ID, invitee.Phone, "女性", inviter.ID, "form")
	if err != nil || inviteID == 0 || status != db.InviteStatusPending {
		t.Fatalf("record invite: id=%d status=%q err=%v", inviteID, status, err)
	}

	client := loginMessageClient(t, database, server, reviewer)
	userPath := "/admin/users/" + strconv.FormatInt(invitee.ID, 10)
	csrf := client.pageCSRF(userPath)
	resp := client.postForm(userPath+"/approve", url.Values{
		"csrf_token": {csrf},
		"note":       {"资料齐全"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	var inviteStatus string
	var dueAt, confirmedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, confirm_due_at, confirmed_at FROM invites WHERE id = $1`, inviteID).
		Scan(&inviteStatus, &dueAt, &confirmedAt); err != nil {
		t.Fatal(err)
	}
	if inviteStatus != db.InviteStatusConfirmed || dueAt == nil || confirmedAt == nil {
		t.Fatalf("invite after approval = %q due=%v confirmed=%v", inviteStatus, dueAt, confirmedAt)
	}
	firstDue := *dueAt
	// Approving again (or a retry of the window start) must not move the due
	// date: the 7-day clock is fixed when the invitee becomes active.
	if _, status, err := database.StartInviteConfirmWindowForUser(ctx, invitee.ID); err != nil ||
		status != db.InviteStatusConfirmed {
		t.Fatalf("idempotent window start: status=%q err=%v", status, err)
	}
	var againDue time.Time
	if err := pool.QueryRow(ctx,
		`SELECT confirm_due_at FROM invites WHERE id = $1`, inviteID).Scan(&againDue); err != nil {
		t.Fatal(err)
	}
	if !againDue.Equal(firstDue) {
		t.Fatalf("due date moved: %v -> %v", firstDue, againDue)
	}

	// Force the window closed and run the job once.
	if _, err := pool.Exec(ctx,
		`UPDATE invites SET confirm_due_at = now() - interval '1 minute' WHERE id = $1`,
		inviteID); err != nil {
		t.Fatal(err)
	}
	if finalized, _, err := database.SettleDueInvites(ctx, 100); err != nil || finalized == 0 {
		t.Fatalf("settle due invites: finalized=%d err=%v", finalized, err)
	}
	var finalStatus string
	var rewardID *int64
	if err := pool.QueryRow(ctx,
		`SELECT status, reward_ledger_id FROM invites WHERE id = $1`, inviteID).
		Scan(&finalStatus, &rewardID); err != nil {
		t.Fatal(err)
	}
	if finalStatus != db.InviteStatusFinalized || rewardID == nil {
		t.Fatalf("settled invite = %q reward=%v", finalStatus, rewardID)
	}
	cfg, err := database.GetPointsConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantReward := db.InviteRewardAmount(cfg, "女性")
	var balance int
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM points_ledger WHERE user_id = $1`, inviter.ID).
		Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != wantReward {
		t.Fatalf("inviter balance = %d, want %d", balance, wantReward)
	}
	// A second run is a no-op; no duplicate reward row.
	if _, _, err := database.SettleDueInvites(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var inviteRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM points_ledger WHERE user_id = $1 AND kind = 'invite'`,
		inviter.ID).Scan(&inviteRows); err != nil {
		t.Fatal(err)
	}
	if inviteRows != 1 {
		t.Fatalf("invite reward rows = %d, want 1", inviteRows)
	}
}

// TestIntegrationStartPendingInviteWindows covers the staging backfill: a
// pending invite whose invitee was approved before this feature shipped must
// still get a confirmation window.
func TestIntegrationStartPendingInviteWindows(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	inviter := createMessageUser(t, ctx, database, pool, "补录邀请人")
	invitee := createMessageUser(t, ctx, database, pool, "补录被邀请人")
	ids := []int64{inviter.ID, invitee.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if _, err := pool.Exec(ctx, `UPDATE users SET gender = '男性' WHERE id = $1`, invitee.ID); err != nil {
		t.Fatal(err)
	}
	inviteID, status, err := database.RecordInviteFromForm(ctx,
		invitee.ID, invitee.Phone, "男性", inviter.ID, "form")
	if err != nil || status != db.InviteStatusPending {
		t.Fatalf("record invite: id=%d status=%q err=%v", inviteID, status, err)
	}
	started, err := database.StartPendingInviteWindows(ctx, 100)
	if err != nil || started == 0 {
		t.Fatalf("backfill windows: started=%d err=%v", started, err)
	}
	var backfilled string
	var dueAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, confirm_due_at FROM invites WHERE id = $1`, inviteID).
		Scan(&backfilled, &dueAt); err != nil {
		t.Fatal(err)
	}
	if backfilled != db.InviteStatusConfirmed || dueAt == nil {
		t.Fatalf("backfilled invite = %q due=%v", backfilled, dueAt)
	}
	_ = server
}
