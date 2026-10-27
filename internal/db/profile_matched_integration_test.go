package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func seedProfileSignup(t *testing.T, d *DB, userID, activityID int64, status string) {
	t.Helper()
	if _, err := d.pool.Exec(context.Background(),
		`INSERT INTO signups (activity_id, user_id, status)
		 VALUES ($1, $2, $3)`, activityID, userID, status); err != nil {
		t.Fatalf("seed signup: %v", err)
	}
}

// TestIntegrationEndedSignupDoesNotBlockMatched is the U-0006 dead-lock
// regression: signups on activities that already ended must not block the
// matched switch, while live/future signups still do.
func TestIntegrationEndedSignupDoesNotBlockMatched(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	t.Run("ended activity does not block", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-ENDED")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusOpen, EndAt: &past,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusSigned)

		has, err := d.HasActiveSignups(ctx, user.ID)
		if err != nil || has {
			t.Fatalf("HasActiveSignups = %v err=%v, want false", has, err)
		}
		status, err := d.SetAccountStatus(ctx, user.ID, "matched")
		if err != nil || status != "matched" {
			t.Fatalf("switch to matched = %q err=%v", status, err)
		}
	})

	t.Run("future activity still blocks", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-FUTURE")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusOpen, StartAt: &future, EndAt: &future,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusSigned)

		has, err := d.HasActiveSignups(ctx, user.ID)
		if err != nil || !has {
			t.Fatalf("HasActiveSignups = %v err=%v, want true", has, err)
		}
		if _, err := d.SetAccountStatus(ctx, user.ID, "matched"); !errors.Is(err, ErrActiveSignupBlocked) {
			t.Fatalf("future signup switch err = %v, want ErrActiveSignupBlocked", err)
		}
	})

	t.Run("ongoing activity still blocks", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-ONGOING")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusOpen, StartAt: &past, EndAt: &future,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusSigned)

		if _, err := d.SetAccountStatus(ctx, user.ID, "matched"); !errors.Is(err, ErrActiveSignupBlocked) {
			t.Fatalf("ongoing signup switch err = %v, want ErrActiveSignupBlocked", err)
		}
	})

	t.Run("nil end_at still blocks", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-NOEND")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusOpen,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusSigned)

		if _, err := d.SetAccountStatus(ctx, user.ID, "matched"); !errors.Is(err, ErrActiveSignupBlocked) {
			t.Fatalf("nil end_at switch err = %v, want ErrActiveSignupBlocked", err)
		}
	})

	t.Run("cancelled signup does not block", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-CANCELSIGNUP")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusOpen, EndAt: &future,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusCancelled)

		has, err := d.HasActiveSignups(ctx, user.ID)
		if err != nil || has {
			t.Fatalf("HasActiveSignups = %v err=%v, want false", has, err)
		}
		if status, err := d.SetAccountStatus(ctx, user.ID, "matched"); err != nil || status != "matched" {
			t.Fatalf("switch after cancelled signup = %q err=%v", status, err)
		}
	})

	t.Run("cancelled activity does not block", func(t *testing.T) {
		user := profileTestUser(t, ctx, d, "U-MATCH-CANCELACT")
		activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
			Status: ActivityStatusCancelled, EndAt: &future,
		})
		seedProfileSignup(t, d, user.ID, activity.ID, SignupStatusSigned)

		has, err := d.HasActiveSignups(ctx, user.ID)
		if err != nil || has {
			t.Fatalf("HasActiveSignups = %v err=%v, want false", has, err)
		}
		if status, err := d.SetAccountStatus(ctx, user.ID, "matched"); err != nil || status != "matched" {
			t.Fatalf("switch after cancelled activity = %q err=%v", status, err)
		}
	})
}
