package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func pointsGrant(t *testing.T, d *DB, ctx context.Context, userID int64, amount int, key string) {
	t.Helper()
	if _, _, err := d.AddPointsEntry(ctx, AddPointsParams{
		UserID:         userID,
		Delta:          amount,
		Kind:           PointsKindAdmin,
		Reason:         "integration test grant",
		RefType:        "manual",
		IdempotencyKey: key,
	}); err != nil {
		t.Fatalf("grant points: %v", err)
	}
}

func pointsDirectSum(t *testing.T, d *DB, ctx context.Context, userID int64) int {
	t.Helper()
	var sum int
	if err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM points_ledger WHERE user_id = $1`,
		userID).Scan(&sum); err != nil {
		t.Fatalf("direct sum: %v", err)
	}
	return sum
}

func pointsCreateActivity(t *testing.T, d *DB, ctx context.Context, quota int, status string) int64 {
	t.Helper()
	no := "A-PTS-" + randomDigits(t, 10)
	var id int64
	if err := d.pool.QueryRow(ctx,
		`INSERT INTO activities (activity_no, name, status, max_signup, start_at)
		 VALUES ($1, $2, $3, $4, now() + interval '7 days') RETURNING id`,
		no, "测试活动 "+no, status, quota).Scan(&id); err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = d.pool.Exec(cleanupCtx, `DELETE FROM activities WHERE id = $1`, id)
	})
	return id
}

func TestIntegrationPointsLedgerBalanceIdempotency(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	user := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{})
	other := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{})

	pointsGrant(t, d, ctx, user, 60, "")
	if _, _, err := d.AddPointsEntry(ctx, AddPointsParams{
		UserID: user, Delta: -20, Kind: PointsKindRedeem,
		Reason: "test spend", RefType: RedeemItemRealLike,
	}); err != nil {
		t.Fatalf("spend: %v", err)
	}
	balance, err := d.PointsBalance(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 40 {
		t.Fatalf("balance = %d, want 40", balance)
	}
	if direct := pointsDirectSum(t, d, ctx, user); direct != balance {
		t.Fatalf("balance %d != SUM(delta) %d", balance, direct)
	}
	summary, err := d.PointsSummary(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Balance != 40 || summary.Earned != 60 || summary.Spent != 20 {
		t.Fatalf("summary = %+v", summary)
	}

	// Idempotent grant: second call is a normal "already done" result.
	key := "it-grant-" + randomDigits(t, 10)
	entry, _, created, err := d.GrantPoints(ctx, AddPointsParams{
		UserID: user, Delta: 5, Kind: PointsKindAdmin,
		Reason: "idempotent", IdempotencyKey: key,
	})
	if err != nil || !created {
		t.Fatalf("first grant created=%v err=%v", created, err)
	}
	if entry.BalanceAfter != 45 {
		t.Fatalf("balance_after = %d, want 45", entry.BalanceAfter)
	}
	if _, _, created, err := d.GrantPoints(ctx, AddPointsParams{
		UserID: user, Delta: 5, Kind: PointsKindAdmin,
		Reason: "idempotent", IdempotencyKey: key,
	}); err != nil || created {
		t.Fatalf("second grant created=%v err=%v", created, err)
	}
	if got := pointsDirectSum(t, d, ctx, user); got != 45 {
		t.Fatalf("balance after retry = %d, want 45", got)
	}
	if _, _, err := d.AddPointsEntry(ctx, AddPointsParams{
		UserID: user, Delta: 5, Kind: PointsKindAdmin, IdempotencyKey: key,
	}); !errors.Is(err, ErrPointsDuplicate) {
		t.Fatalf("duplicate err = %v, want ErrPointsDuplicate", err)
	}

	// Insufficient balance must not write a negative row.
	if _, _, err := d.AddPointsEntry(ctx, AddPointsParams{
		UserID: user, Delta: -100, Kind: PointsKindRedeem,
	}); !errors.Is(err, ErrPointsInsufficient) {
		t.Fatalf("overdraw err = %v, want ErrPointsInsufficient", err)
	}
	if got := pointsDirectSum(t, d, ctx, user); got != 45 {
		t.Fatalf("balance after overdraw = %d, want 45", got)
	}

	// A user can only see their own ledger.
	if otherBalance, err := d.PointsBalance(ctx, other); err != nil || otherBalance != 0 {
		t.Fatalf("other balance = %d err=%v", otherBalance, err)
	}
	entries, err := d.ListPointsEntries(ctx, user, 50, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for _, e := range entries {
		if e.UserID != user {
			t.Fatalf("entry belongs to %d, want %d", e.UserID, user)
		}
	}
	grants, err := d.ListPointsEntries(ctx, user, 50, 0, []string{PointsKindAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("admin entries = %d, want 2", len(grants))
	}
}

func TestIntegrationPointsInviteLifecycle(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	inviter := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	invitee := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	phone := "138" + randomDigits(t, 8)
	rawPhone := phone[:3] + " " + phone[3:7] + " " + phone[7:]

	inviteID, status, err := d.RecordInviteFromForm(ctx, invitee, rawPhone, GenderFemale,
		inviter, "form")
	if err != nil {
		t.Fatalf("record invite: %v", err)
	}
	if inviteID == 0 || status != InviteStatusPending {
		t.Fatalf("invite = %d status=%q", inviteID, status)
	}
	progress, err := d.InviteProgress(ctx, inviter)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pending != 1 || progress.Total != 1 || progress.PendingPoints != 0 {
		t.Fatalf("pending progress = %+v", progress)
	}
	list, err := d.ListInvitesFor(ctx, inviter, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].PhoneMasked != phone[:3]+"****"+phone[7:] {
		t.Fatalf("masked list = %+v", list)
	}
	if list[0].Reward != 0 {
		t.Fatalf("pending reward = %d, want 0", list[0].Reward)
	}

	startID, status, err := d.StartInviteConfirmWindow(ctx, invitee, rawPhone, GenderFemale)
	if err != nil || startID != inviteID || status != InviteStatusConfirmed {
		t.Fatalf("start confirm id=%d status=%q err=%v", startID, status, err)
	}
	progress, _ = d.InviteProgress(ctx, inviter)
	if progress.Confirmed != 1 || progress.PendingPoints != 20 {
		t.Fatalf("confirmed progress = %+v", progress)
	}

	if _, _, err := d.SettleInvite(ctx, inviteID); !errors.Is(err, ErrPointsInviteDue) {
		t.Fatalf("early settle err = %v, want ErrPointsInviteDue", err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE invites SET confirm_due_at = now() - interval '1 minute' WHERE id = $1`,
		inviteID); err != nil {
		t.Fatal(err)
	}
	settledStatus, entryID, err := d.SettleInvite(ctx, inviteID)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settledStatus != InviteStatusFinalized || entryID == nil {
		t.Fatalf("settled = %q entry=%v", settledStatus, entryID)
	}
	balance, _ := d.PointsBalance(ctx, inviter)
	if balance != 20 {
		t.Fatalf("inviter balance = %d, want 20", balance)
	}
	// Retrying settlement must not double-pay.
	if status, _, err := d.SettleInvite(ctx, inviteID); err != nil || status != InviteStatusFinalized {
		t.Fatalf("second settle status=%q err=%v", status, err)
	}
	if balance, _ := d.PointsBalance(ctx, inviter); balance != 20 {
		t.Fatalf("balance after retry = %d", balance)
	}
	progress, _ = d.InviteProgress(ctx, inviter)
	if progress.Finalized != 1 || progress.FinalizedPoints != 20 {
		t.Fatalf("final progress = %+v", progress)
	}

	// Banning the invitee inside the window claws the reward back without pay.
	banned := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	bannedPhone := "139" + randomDigits(t, 8)
	bannedInvite, _, err := d.RecordInviteFromForm(ctx, banned, bannedPhone, GenderMale, inviter, "form")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.StartInviteConfirmWindow(ctx, banned, bannedPhone, GenderMale); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'disabled' WHERE id = $1`, banned); err != nil {
		t.Fatal(err)
	}
	status, _, err = d.SettleInvite(ctx, bannedInvite)
	if err != nil || status != InviteStatusClawedBack {
		t.Fatalf("banned settle status=%q err=%v", status, err)
	}
	if balance, _ := d.PointsBalance(ctx, inviter); balance != 20 {
		t.Fatalf("balance after clawback = %d, want 20", balance)
	}
	progress, _ = d.InviteProgress(ctx, inviter)
	if progress.ClawedBack != 1 {
		t.Fatalf("clawed progress = %+v", progress)
	}

	// Immediate clawback for a confirmed invite.
	clawed := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	clawedPhone := "137" + randomDigits(t, 8)
	clawedInvite, _, err := d.RecordInviteFromForm(ctx, clawed, clawedPhone, GenderFemale, inviter, "form")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.StartInviteConfirmWindow(ctx, clawed, clawedPhone, GenderFemale); err != nil {
		t.Fatal(err)
	}
	n, err := d.ClawBackInvitesForInvitee(ctx, clawed, "确认期内被封禁")
	if err != nil || n != 1 {
		t.Fatalf("claw back n=%d err=%v", n, err)
	}
	if status, _, err := d.SettleInvite(ctx, clawedInvite); err != nil || status != InviteStatusClawedBack {
		t.Fatalf("clawed invite settle status=%q err=%v", status, err)
	}
	if balance, _ := d.PointsBalance(ctx, inviter); balance != 20 {
		t.Fatalf("balance after immediate clawback = %d", balance)
	}

	// Self-invite is rejected at confirmation time.
	selfPhone := "136" + randomDigits(t, 8)
	selfInvite, _, err := d.RecordInviteFromForm(ctx, inviter, selfPhone, GenderMale, inviter, "form")
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := d.StartInviteConfirmWindow(ctx, inviter, selfPhone, GenderMale); err != nil ||
		status != InviteStatusRejected {
		t.Fatalf("self invite status=%q err=%v", status, err)
	}
	if status, _, err := d.SettleInvite(ctx, selfInvite); err != nil || status != InviteStatusRejected {
		t.Fatalf("self settle status=%q err=%v", status, err)
	}

	// Historical participants: normalization + import idempotency.
	histPhone := "135" + randomDigits(t, 8)
	added, skipped, err := d.AddHistoricalParticipants(ctx, []HistoricalParticipantInput{
		{Phone: histPhone, Name: "老王"},
		{Phone: histPhone[:3] + " " + histPhone[3:], Name: "老王"},
		{Phone: "not-a-phone", Name: "坏号码"},
	}, "2023")
	if err != nil || added != 1 || skipped != 2 {
		t.Fatalf("historical import added=%d skipped=%d err=%v", added, skipped, err)
	}
	if ok, err := d.IsHistoricalParticipant(ctx, histPhone); err != nil || !ok {
		t.Fatalf("historical check ok=%v err=%v", ok, err)
	}
	if count, err := d.HistoricalParticipantCount(ctx); err != nil || count < 1 {
		t.Fatalf("historical count=%d err=%v", count, err)
	}
}

func TestIntegrationPointsRedemption(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	viewer := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	female := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})
	male := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	pointsGrant(t, d, ctx, viewer, 600, "")

	// real_like: deduct + order; duplicate key is rejected without a second charge.
	reqKey := "it-real-" + randomDigits(t, 8)
	order, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemRealLike, RequestKey: reqKey,
	})
	if err != nil {
		t.Fatalf("redeem real_like: %v", err)
	}
	if order.Cost != 20 || order.Status != RedeemStatusActive || order.LedgerID == nil {
		t.Fatalf("order = %+v", order)
	}
	if balance, _ := d.PointsBalance(ctx, viewer); balance != 580 {
		t.Fatalf("balance after real_like = %d", balance)
	}
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemRealLike, RequestKey: reqKey,
	}); !errors.Is(err, ErrPointsAlreadyRedeemed) {
		t.Fatalf("duplicate redemption err = %v", err)
	}
	if balance, _ := d.PointsBalance(ctx, viewer); balance != 580 {
		t.Fatalf("balance after duplicate = %d", balance)
	}
	extra, err := d.ExtraRealLikeQuota(ctx, viewer)
	if err != nil || extra != 1 {
		t.Fatalf("extra real like = %d err=%v", extra, err)
	}
	quota, err := d.PointsLikeQuota(ctx, viewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if quota.RealPermanent != 1 || quota.RealTotal != 2 {
		t.Fatalf("like quota = %+v", quota)
	}
	refund, err := d.RefundRedemption(ctx, order.ID, "测试退还", PointsKindRefund, &viewer, RedeemStatusRefunded)
	if err != nil || refund == nil {
		t.Fatalf("refund: %+v err=%v", refund, err)
	}
	if refund.Balance != 600 || refund.RefundEntryID == 0 {
		t.Fatalf("refund result = %+v", refund)
	}
	if extra, _ := d.ExtraRealLikeQuota(ctx, viewer); extra != 0 {
		t.Fatalf("extra after refund = %d", extra)
	}
	if again, err := d.RefundRedemption(ctx, order.ID, "", PointsKindRefund, nil, ""); err != nil || again != nil {
		t.Fatalf("second refund = %+v err=%v", again, err)
	}

	// Insufficient balance is reported before any row is written.
	poor := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{})
	if _, err := d.RedeemPoints(ctx, poor, RedeemRequest{
		Item: RedeemItemRealLike, RequestKey: "poor",
	}); !errors.Is(err, ErrPointsInsufficient) {
		t.Fatalf("poor redeem err = %v", err)
	}
	if balance, _ := d.PointsBalance(ctx, poor); balance != 0 {
		t.Fatalf("poor balance = %d", balance)
	}

	// priority: per-user once, activity cap = floor(quota * ratio).
	activityID := pointsCreateActivity(t, d, ctx, 10, "报名中")
	actKey := fmt.Sprintf("%d", activityID)
	priority, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemPriority, RequestKey: "priority-1", ActivityID: actKey,
	})
	if err != nil {
		t.Fatalf("priority redeem: %v", err)
	}
	if priority.Cost != 30 || priority.RefID != actKey || priority.Status != RedeemStatusActive {
		t.Fatalf("priority order = %+v", priority)
	}
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemPriority, RequestKey: "priority-2", ActivityID: actKey,
	}); !errors.Is(err, ErrPointsAlreadyRedeemed) {
		t.Fatalf("second priority err = %v", err)
	}
	avail, err := d.PriorityAvailability(ctx, actKey)
	if err != nil {
		t.Fatal(err)
	}
	if avail != 2 {
		t.Fatalf("priority availability = %d, want 2", avail)
	}
	// Fill the cap with two more users; a fourth is refused.
	for i := 0; i < 2; i++ {
		u := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{})
		pointsGrant(t, d, ctx, u, 100, "")
		if _, err := d.RedeemPoints(ctx, u, RedeemRequest{
			Item: RedeemItemPriority, RequestKey: fmt.Sprintf("priority-extra-%d", i), ActivityID: actKey,
		}); err != nil {
			t.Fatalf("fill priority %d: %v", i, err)
		}
	}
	fourth := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{})
	pointsGrant(t, d, ctx, fourth, 100, "")
	if _, err := d.RedeemPoints(ctx, fourth, RedeemRequest{
		Item: RedeemItemPriority, RequestKey: "priority-fourth", ActivityID: actKey,
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("over-cap priority err = %v", err)
	}

	// priority cancellation: >=48h refunds, <48h only marks cancelled.
	hours := 72.0
	cancel, err := d.CancelPriorityOnSignupCancel(ctx, viewer, actKey, &hours, "")
	if err != nil || cancel == nil || !cancel.Refunded || cancel.Points != 30 {
		t.Fatalf("cancel refund = %+v err=%v", cancel, err)
	}
	// Re-redeem after refund uses a suffixed idempotency key.
	priority2, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemPriority, RequestKey: "priority-again", ActivityID: actKey,
	})
	if err != nil {
		t.Fatalf("re-redeem priority: %v", err)
	}
	short := 1.0
	cancel, err = d.CancelPriorityOnSignupCancel(ctx, viewer, actKey, &short, "")
	if err != nil || cancel == nil || cancel.Refunded || cancel.Order.Status != RedeemStatusCancelled {
		t.Fatalf("short cancel = %+v err=%v", cancel, err)
	}
	if _, err := d.RefundRedemption(ctx, priority2.ID, "", PointsKindRefund, nil, ""); err != nil {
		t.Fatalf("cancelled order refund: %v", err)
	}

	// wish: requires a signup, opposite-gender active target, and cannot target self.
	wishActivity := pointsCreateActivity(t, d, ctx, 10, "报名中")
	wishKey := fmt.Sprintf("%d", wishActivity)
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemWish, RequestKey: "wish-nosignup", ActivityID: wishKey, TargetUserID: female,
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("wish without signup err = %v", err)
	}
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, status) VALUES ($1, $2, '已报名')`,
		wishActivity, viewer); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemWish, RequestKey: "wish-self", ActivityID: wishKey, TargetUserID: viewer,
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("wish self err = %v", err)
	}
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemWish, RequestKey: "wish-same", ActivityID: wishKey, TargetUserID: male,
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("wish same gender err = %v", err)
	}
	wishOrder, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemWish, RequestKey: "wish-ok", ActivityID: wishKey, TargetUserID: female,
	})
	if err != nil {
		t.Fatalf("wish redeem: %v", err)
	}
	if wishOrder.Cost != 30 || wishOrder.Params["target_user_id"] == nil {
		t.Fatalf("wish order = %+v", wishOrder)
	}

	// matchmaker: condition limit, one open order, refund, then done.
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemMatchmaker, RequestKey: "mm-long",
		Condition: strings.Repeat("字", 201),
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("long condition err = %v", err)
	}
	mm, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemMatchmaker, RequestKey: "mm-1", Condition: "希望对方也在教会服事",
	})
	if err != nil {
		t.Fatalf("matchmaker redeem: %v", err)
	}
	if mm.Status != RedeemStatusPending || mm.Cost != 50 {
		t.Fatalf("matchmaker order = %+v", mm)
	}
	if _, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemMatchmaker, RequestKey: "mm-2", Condition: "再一张",
	}); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("second matchmaker err = %v", err)
	}
	marked, err := d.MarkRedemption(ctx, mm.ID, RedeemStatusRecommended, "已推荐")
	if err != nil || marked.Status != RedeemStatusRecommended {
		t.Fatalf("mark recommended = %+v err=%v", marked, err)
	}
	if _, err := d.MarkRedemption(ctx, mm.ID, "not-a-status", ""); !errors.Is(err, ErrPointsUnavailable) {
		t.Fatalf("invalid status err = %v", err)
	}
	if res, err := d.RefundRedemption(ctx, mm.ID, "", PointsKindRefund, nil, ""); err != nil || res == nil {
		t.Fatalf("matchmaker refund = %+v err=%v", res, err)
	}

	// Overdue only covers pending orders, not recommended ones.
	overdueActivity := pointsCreateActivity(t, d, ctx, 10, "报名中")
	_ = overdueActivity
	mmOverdue, err := d.RedeemPoints(ctx, viewer, RedeemRequest{
		Item: RedeemItemMatchmaker, RequestKey: "mm-overdue", Condition: "测试逾期",
	})
	if err != nil {
		t.Fatalf("overdue order: %v", err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE points_redemptions SET created_at = now() - interval '30 days' WHERE id = $1`,
		mmOverdue.ID); err != nil {
		t.Fatal(err)
	}
	overdue, err := d.MatchmakerOverdue(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range overdue {
		if row.ID == mmOverdue.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("overdue order %d not returned (%d rows)", mmOverdue.ID, len(overdue))
	}

	// Lists are owner-scoped.
	list, err := d.ListRedemptionsFor(ctx, viewer, 100, "")
	if err != nil || len(list) == 0 {
		t.Fatalf("list redemptions len=%d err=%v", len(list), err)
	}
	otherList, err := d.ListRedemptionsFor(ctx, female, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList) != 0 {
		t.Fatalf("other user redemptions = %d, want 0", len(otherList))
	}
}

// TestIntegrationPointsMigratedLedger runs against the migrated ledger copy
// when MIGRATED_LEDGER_DATABASE_URL is provided. It never writes.
func TestIntegrationPointsMigratedLedger(t *testing.T) {
	dsn := os.Getenv("MIGRATED_LEDGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATED_LEDGER_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer d.Close()
	if err := d.Ping(ctx); err != nil {
		t.Fatalf("ping migrated db: %v", err)
	}
	var total int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM points_ledger`).Scan(&total); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if total == 0 {
		t.Fatal("migrated ledger is empty")
	}
	if want := os.Getenv("MIGRATED_LEDGER_EXPECT_COUNT"); want != "" {
		var expected int
		if _, err := fmt.Sscanf(want, "%d", &expected); err != nil {
			t.Fatalf("bad MIGRATED_LEDGER_EXPECT_COUNT: %v", err)
		}
		if total != expected {
			t.Fatalf("migrated ledger count = %d, want %d", total, expected)
		}
	}
	var userID int64
	err = d.pool.QueryRow(ctx,
		`SELECT user_id FROM points_ledger WHERE legacy_id IS NOT NULL
		 GROUP BY user_id ORDER BY count(*) DESC LIMIT 1`).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("no migrated (legacy_id) ledger rows")
	}
	if err != nil {
		t.Fatalf("find migrated user: %v", err)
	}
	summary, err := d.PointsSummary(ctx, userID)
	if err != nil {
		t.Fatalf("migrated summary: %v", err)
	}
	if summary.Balance != pointsDirectSum(t, d, ctx, userID) {
		t.Fatalf("migrated balance %d != SUM(delta)", summary.Balance)
	}
	entries, err := d.ListPointsEntries(ctx, userID, 500, 0, nil)
	if err != nil {
		t.Fatalf("migrated entries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("migrated entries empty")
	}
	if entries[0].KindLabel == "" {
		t.Fatal("migrated entry missing kind label")
	}
}
