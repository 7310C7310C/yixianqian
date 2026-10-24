package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func pointsInvitePhone(t *testing.T, d *DB, ctx context.Context, userID int64) string {
	t.Helper()
	var phone string
	if err := d.pool.QueryRow(ctx, `SELECT phone FROM users WHERE id = $1`, userID).Scan(&phone); err != nil {
		t.Fatalf("load fixture phone: %v", err)
	}
	return phone
}

// TestIntegrationBackfillLegacyInvite is the B1 regression: an invite recorded
// before the approval hook has invitee_id NULL while its phone already belongs
// to an active user. The backfill must resolve the invitee from the phone,
// start the 7-day window and let the inviter settle the reward.
func TestIntegrationBackfillLegacyInvite(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	inviter := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	invitee := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	phone := pointsInvitePhone(t, d, ctx, invitee)

	var legacyID int64
	if err := d.pool.QueryRow(ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, invitee_id, status, gender, source)
		 VALUES ($1, $2, NULL, $3, '', 'legacy') RETURNING id`,
		inviter, phone, InviteStatusPending).Scan(&legacyID); err != nil {
		t.Fatalf("insert legacy invite: %v", err)
	}

	started, err := d.StartPendingInviteWindows(ctx, 1000)
	if err != nil {
		t.Fatalf("backfill legacy invite: %v", err)
	}
	if started == 0 {
		t.Fatal("no invite window started")
	}
	var status string
	var boundID *int64
	var confirmedAt, dueAt *time.Time
	if err := d.pool.QueryRow(ctx,
		`SELECT status, invitee_id, confirmed_at, confirm_due_at FROM invites WHERE id = $1`,
		legacyID).Scan(&status, &boundID, &confirmedAt, &dueAt); err != nil {
		t.Fatal(err)
	}
	if status != InviteStatusConfirmed || boundID == nil || *boundID != invitee {
		t.Fatalf("backfilled invite = %q invitee=%v, want confirmed/%d", status, boundID, invitee)
	}
	if confirmedAt == nil || dueAt == nil {
		t.Fatalf("backfilled invite window = confirmed %v due %v", confirmedAt, dueAt)
	}

	// The inviter must actually be able to settle the backfilled invite.
	if _, err := d.pool.Exec(ctx,
		`UPDATE invites SET confirm_due_at = now() - interval '1 minute' WHERE id = $1`,
		legacyID); err != nil {
		t.Fatal(err)
	}
	settled, entryID, err := d.SettleInvite(ctx, legacyID)
	if err != nil || settled != InviteStatusFinalized || entryID == nil {
		t.Fatalf("settle backfilled invite = %q entry=%v err=%v", settled, entryID, err)
	}
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := InviteRewardAmount(cfg, GenderMale)
	balance, err := d.PointsBalance(ctx, inviter)
	if err != nil {
		t.Fatal(err)
	}
	if balance != want {
		t.Fatalf("inviter balance = %d, want %d", balance, want)
	}
}

// TestIntegrationBackfillSkipsBrokenInvite proves a single broken invite does
// not abort the batch: the conflicting row stays pending and is reported, while
// the rows after it still start their windows.
func TestIntegrationBackfillSkipsBrokenInvite(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	inviter := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	target := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	goodInviter := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	goodTarget := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderFemale})

	// A legacy invite (invitee_id NULL) for the target's phone, followed by an
	// already-bound invite for the same account. Starting the legacy window
	// violates invites_invitee_uidx, so that row must be skipped with an error.
	var brokenID int64
	if err := d.pool.QueryRow(ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, invitee_id, status, gender, source)
		 VALUES ($1, $2, NULL, $3, '', 'legacy') RETURNING id`,
		inviter, pointsInvitePhone(t, d, ctx, target), InviteStatusPending).Scan(&brokenID); err != nil {
		t.Fatalf("insert legacy invite: %v", err)
	}
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, invitee_id, status, gender, source)
		 VALUES ($1, $2, $3, $4, '', 'legacy')`,
		inviter, "137"+randomDigits(t, 8), target, InviteStatusPending); err != nil {
		t.Fatalf("insert conflicting invite: %v", err)
	}
	goodID, _, err := d.RecordInviteFromForm(ctx, goodTarget,
		pointsInvitePhone(t, d, ctx, goodTarget), GenderFemale, goodInviter, "form")
	if err != nil || goodID == 0 {
		t.Fatalf("record good invite: id=%d err=%v", goodID, err)
	}

	started, err := d.StartPendingInviteWindows(ctx, 1000)
	if err == nil {
		t.Fatal("expected the conflicting invite to be reported")
	}
	if started < 2 {
		t.Fatalf("started = %d, want the two later invites to start (err=%v)", started, err)
	}

	var brokenStatus string
	var brokenDue *time.Time
	if err := d.pool.QueryRow(ctx,
		`SELECT status, confirm_due_at FROM invites WHERE id = $1`, brokenID).
		Scan(&brokenStatus, &brokenDue); err != nil {
		t.Fatal(err)
	}
	if brokenStatus != InviteStatusPending || brokenDue != nil {
		t.Fatalf("broken invite = %q due=%v, want untouched pending", brokenStatus, brokenDue)
	}
	var goodStatus string
	if err := d.pool.QueryRow(ctx,
		`SELECT status FROM invites WHERE id = $1`, goodID).Scan(&goodStatus); err != nil {
		t.Fatal(err)
	}
	if goodStatus != InviteStatusConfirmed {
		t.Fatalf("good invite = %q, want confirmed", goodStatus)
	}
}

// TestIntegrationInviteConfirmWindowBoundary pins the 7-day window: it starts
// exactly 7 days out, cannot be settled at day 0 or day 6, and settles at day 7
// and day 8. Times are moved through confirmed_at/confirm_due_at so the test
// does not depend on the wall clock.
func TestIntegrationInviteConfirmWindowBoundary(t *testing.T) {
	d, ctx := openSocialTestDB(t)
	inviter := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	invitee := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	inviteID, _, err := d.RecordInviteFromForm(ctx, invitee,
		pointsInvitePhone(t, d, ctx, invitee), GenderMale, inviter, "form")
	if err != nil || inviteID == 0 {
		t.Fatalf("record invite: id=%d err=%v", inviteID, err)
	}
	if _, status, err := d.StartInviteConfirmWindowForUser(ctx, invitee); err != nil ||
		status != InviteStatusConfirmed {
		t.Fatalf("start window: status=%q err=%v", status, err)
	}

	var confirmedAt, dueAt time.Time
	if err := d.pool.QueryRow(ctx,
		`SELECT confirmed_at, confirm_due_at FROM invites WHERE id = $1`, inviteID).
		Scan(&confirmedAt, &dueAt); err != nil {
		t.Fatal(err)
	}
	if window := dueAt.Sub(confirmedAt); window < 7*24*time.Hour-5*time.Second ||
		window > 7*24*time.Hour+5*time.Second {
		t.Fatalf("confirmation window = %v, want 7 days", window)
	}

	// Day 0: a freshly started window is never due.
	if _, _, err := d.SettleInvite(ctx, inviteID); !errors.Is(err, ErrPointsInviteDue) {
		t.Fatalf("day 0 settle err = %v, want ErrPointsInviteDue", err)
	}
	// Day 6: one day left, still not due.
	if _, err := d.pool.Exec(ctx,
		`UPDATE invites SET confirmed_at = now() - interval '6 days',
		                    confirm_due_at = now() + interval '1 day'
		  WHERE id = $1`, inviteID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.SettleInvite(ctx, inviteID); !errors.Is(err, ErrPointsInviteDue) {
		t.Fatalf("day 6 settle err = %v, want ErrPointsInviteDue", err)
	}
	// Day 7: the window is closed; settling is allowed.
	if _, err := d.pool.Exec(ctx,
		`UPDATE invites SET confirmed_at = now() - interval '7 days',
		                    confirm_due_at = now()
		  WHERE id = $1`, inviteID); err != nil {
		t.Fatal(err)
	}
	settled, entryID, err := d.SettleInvite(ctx, inviteID)
	if err != nil || settled != InviteStatusFinalized || entryID == nil {
		t.Fatalf("day 7 settle = %q entry=%v err=%v", settled, entryID, err)
	}

	// Day 8: a second invite is equally settleable.
	late := createSocialFixtureUser(t, d, ctx, socialFixtureOpts{Gender: GenderMale})
	lateID, _, err := d.RecordInviteFromForm(ctx, late,
		pointsInvitePhone(t, d, ctx, late), GenderMale, inviter, "form")
	if err != nil || lateID == 0 {
		t.Fatalf("record late invite: id=%d err=%v", lateID, err)
	}
	if _, status, err := d.StartInviteConfirmWindowForUser(ctx, late); err != nil ||
		status != InviteStatusConfirmed {
		t.Fatalf("start late window: status=%q err=%v", status, err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE invites SET confirmed_at = now() - interval '8 days',
		                    confirm_due_at = now() - interval '1 day'
		  WHERE id = $1`, lateID); err != nil {
		t.Fatal(err)
	}
	if settled, _, err := d.SettleInvite(ctx, lateID); err != nil ||
		settled != InviteStatusFinalized {
		t.Fatalf("day 8 settle = %q err=%v", settled, err)
	}

	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := 2 * InviteRewardAmount(cfg, GenderMale)
	balance, err := d.PointsBalance(ctx, inviter)
	if err != nil {
		t.Fatal(err)
	}
	if balance != want {
		t.Fatalf("inviter balance = %d, want %d", balance, want)
	}
}
