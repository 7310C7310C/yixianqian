package db

import (
	"testing"
	"time"
)

// TestIntegrationCloseEndedActivities covers the persisted half of the
// activity auto-end rule: only 报名中/已满员 with a past end_at are closed,
// the update is idempotent and the returned count matches the changed rows.
func TestIntegrationCloseEndedActivities(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	openEnded := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, EndAt: &past,
	})
	fullEnded := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusFull, MaxSignup: 1, EndAt: &past,
	})
	openFuture := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, EndAt: &future,
	})
	openNoEnd := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen,
	})
	cancelled := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusCancelled, EndAt: &past,
	})
	finished := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusFinished, EndAt: &past,
	})
	notStarted := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusNotStarted, EndAt: &past,
	})

	var expected int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM activities
		  WHERE status IN ($1, $2) AND end_at IS NOT NULL AND end_at < now()`,
		ActivityStatusOpen, ActivityStatusFull).Scan(&expected); err != nil {
		t.Fatalf("count candidates: %v", err)
	}
	if expected < 2 {
		t.Fatalf("expected at least the two fixtures to be candidates, got %d", expected)
	}

	closed, err := d.CloseEndedActivities(ctx)
	if err != nil {
		t.Fatalf("close ended activities: %v", err)
	}
	if closed != int64(expected) {
		t.Fatalf("closed = %d, want %d", closed, expected)
	}

	for _, tc := range []struct {
		name string
		id   int64
		want string
	}{
		{"open ended", openEnded.ID, ActivityStatusFinished},
		{"full ended", fullEnded.ID, ActivityStatusFinished},
		{"open future", openFuture.ID, ActivityStatusOpen},
		{"open without end", openNoEnd.ID, ActivityStatusOpen},
		{"cancelled", cancelled.ID, ActivityStatusCancelled},
		{"already finished", finished.ID, ActivityStatusFinished},
		{"not started", notStarted.ID, ActivityStatusNotStarted},
	} {
		if got := reloadActivity(t, d, ctx, tc.id); got.Status != tc.want {
			t.Fatalf("%s status = %q, want %q", tc.name, got.Status, tc.want)
		}
	}

	closed, err = d.CloseEndedActivities(ctx)
	if err != nil {
		t.Fatalf("second close: %v", err)
	}
	if closed != 0 {
		t.Fatalf("second close = %d, want 0 (idempotent)", closed)
	}
}
