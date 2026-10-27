package db

import (
	"errors"
	"testing"
	"time"
)

// TestIntegrationGroupingEndedActivityGate covers the user-side close of
// grouping entries: every read/write rejects an ended activity, and a
// group-disabled activity returns a typed error instead of a generic failure.
func TestIntegrationGroupingEndedActivityGate(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	user := createActivityFixtureUser(t, d, ctx, "结束分组用户")
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	ended := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, EndAt: &past,
	})
	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET group_enabled = true, group_status = '已完成' WHERE id = $1`,
		ended.ID); err != nil {
		t.Fatalf("enable grouping on ended activity: %v", err)
	}
	if _, _, err := d.GroupCandidates(ctx, user, ended.ActivityNo); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("ended candidates err = %v, want ErrActivityEnded", err)
	}
	if _, err := d.GroupStatusForUser(ctx, user, ended.ActivityNo); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("ended status err = %v, want ErrActivityEnded", err)
	}
	if _, err := d.SelectGroupChoices(ctx, user, ended.ActivityNo, nil); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("ended select err = %v, want ErrActivityEnded", err)
	}
	if _, err := d.GroupResultForUser(ctx, user, ended.ActivityNo, 0); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("ended result err = %v, want ErrActivityEnded", err)
	}

	disabled := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, EndAt: &future,
	})
	if _, _, err := d.GroupCandidates(ctx, user, disabled.ActivityNo); !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("disabled candidates err = %v, want ErrActivityNotOpen", err)
	}
	if _, err := d.GroupStatusForUser(ctx, user, disabled.ActivityNo); !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("disabled status err = %v, want ErrActivityNotOpen", err)
	}
	if _, err := d.SelectGroupChoices(ctx, user, disabled.ActivityNo, nil); !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("disabled select err = %v, want ErrActivityNotOpen", err)
	}
	if _, err := d.GroupResultForUser(ctx, user, disabled.ActivityNo, 0); !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("disabled result err = %v, want ErrActivityNotOpen", err)
	}
}
