package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func createOnetooneFixtureUser(t *testing.T, d *DB, ctx context.Context, name, gender, openID string) int64 {
	t.Helper()
	user, err := d.UpsertLegacyUser(ctx, "U-OTO-"+randomDigits(t, 10),
		"1"+randomDigits(t, 10), name)
	if err != nil {
		t.Fatalf("create onetoone user: %v", err)
	}
	if _, err := d.pool.Exec(ctx, `
		UPDATE users
		   SET account_status = 'active', account_kind = 'user', nickname = $2,
		       gender = $3, legacy_open_id = $4, education = '本科',
		       self_hobbies = '["看书"]'::jsonb
		 WHERE id = $1`, user.ID, name, gender, openID); err != nil {
		t.Fatalf("configure onetoone user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = d.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user.ID
}

func signupOnetooneUser(t *testing.T, d *DB, ctx context.Context, activityNo string, userID int64) {
	t.Helper()
	if _, err := d.SignupActivity(ctx, userID, activityNo); err != nil {
		t.Fatalf("signup onetoone user: %v", err)
	}
}

func saveOnetooneSelection(t *testing.T, d *DB, ctx context.Context, activityID, selectorID int64,
	gender string, choices []int64) {
	t.Helper()
	raw, err := json.Marshal(choices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx, `
		INSERT INTO group_selections (activity_id, selector_id, gender, choices, round)
		VALUES ($1, $2, $3, $4::jsonb, 1)`, activityID, selectorID, gender, raw); err != nil {
		t.Fatalf("save onetoone selection: %v", err)
	}
}

func onetooneResultCount(t *testing.T, d *DB, ctx context.Context, activityID int64, round int) int {
	t.Helper()
	var count int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM onetoone_results WHERE activity_id = $1 AND round = $2`,
		activityID, round).Scan(&count); err != nil {
		t.Fatalf("count onetoone results: %v", err)
	}
	return count
}

func TestIntegrationOnetooneExecuteIdempotentAndOwnerScoped(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})
	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET onetoone_enabled = true WHERE id = $1`, activity.ID); err != nil {
		t.Fatal(err)
	}
	male1 := createOnetooneFixtureUser(t, d, ctx, "男一", "男性", "ou_oto_m1_"+randomDigits(t, 6))
	male2 := createOnetooneFixtureUser(t, d, ctx, "男二", "男性", "ou_oto_m2_"+randomDigits(t, 6))
	female1 := createOnetooneFixtureUser(t, d, ctx, "女一", "女性", "ou_oto_f1_"+randomDigits(t, 6))
	female2 := createOnetooneFixtureUser(t, d, ctx, "女二", "女性", "ou_oto_f2_"+randomDigits(t, 6))
	for _, id := range []int64{male1, male2, female1, female2} {
		signupOnetooneUser(t, d, ctx, activity.ActivityNo, id)
	}
	saveOnetooneSelection(t, d, ctx, activity.ID, male1, "男性", []int64{female1, female2})
	saveOnetooneSelection(t, d, ctx, activity.ID, male2, "男性", []int64{female2, female1})
	saveOnetooneSelection(t, d, ctx, activity.ID, female1, "女性", []int64{male1, male2})
	saveOnetooneSelection(t, d, ctx, activity.ID, female2, "女性", []int64{male2, male1})

	run, err := d.ExecuteOnetoone(ctx, activity.ID, 1, 1)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if run.Participants != 4 || run.MaleCount != 2 || run.FemaleCount != 2 || run.Pairs != 4 {
		t.Fatalf("run = %+v", run)
	}
	if got := onetooneResultCount(t, d, ctx, activity.ID, 1); got != 4 {
		t.Fatalf("round 1 rows = %d", got)
	}
	reloaded := reloadActivity(t, d, ctx, activity.ID)
	if reloaded.OnetooneStatus != OnetooneStatusDone {
		t.Fatalf("status = %q", reloaded.OnetooneStatus)
	}

	// Same round is an idempotent no-op and must not add rows.
	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 1, 1); !errors.Is(err, ErrOnetooneRoundExists) {
		t.Fatalf("rerun err = %v, want ErrOnetooneRoundExists", err)
	}
	if got := onetooneResultCount(t, d, ctx, activity.ID, 1); got != 4 {
		t.Fatalf("rows after rerun = %d", got)
	}

	rounds, err := d.OnetooneRounds(ctx, activity.ID)
	if err != nil || len(rounds) != 1 || rounds[0] != 1 {
		t.Fatalf("rounds = %v err=%v", rounds, err)
	}
	if latest, err := d.LatestOnetooneRound(ctx, activity.ID); err != nil || latest != 1 {
		t.Fatalf("latest = %d err=%v", latest, err)
	}

	// Owner scoping: each person only sees their own partners.
	mine, err := d.ListOnetooneResultsForUser(ctx, activity.ID, male1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].TargetID != female1 {
		t.Fatalf("male1 partners = %+v", mine)
	}
	other, err := d.ListOnetooneResultsForUser(ctx, activity.ID, male2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].TargetID != female2 {
		t.Fatalf("male2 partners = %+v", other)
	}
	outsider, err := d.ListOnetooneResultsForUser(ctx, activity.ID, male1+1000000, 1)
	if err != nil || len(outsider) != 0 {
		t.Fatalf("outsider partners = %+v err=%v", outsider, err)
	}

	// "Already chatted" only accepts the viewer's own non-deleted messages.
	if _, err := d.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: female1, AuthorID: male1, Content: "你好",
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := d.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: female2, AuthorID: male1, Content: "再见",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeleteMessage(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	// Another user's message must not mark female2 as chatted for male1.
	if _, err := d.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: female2, AuthorID: male2, Content: "别人的留言",
	}); err != nil {
		t.Fatal(err)
	}
	chatted, err := d.ChattedTargetIDs(ctx, male1, []int64{female1, female2})
	if err != nil {
		t.Fatal(err)
	}
	if !chatted[female1] || chatted[female2] {
		t.Fatalf("chatted = %v", chatted)
	}

	cleared, err := d.ResetOnetooneResults(ctx, activity.ID)
	if err != nil || cleared != 4 {
		t.Fatalf("reset cleared=%d err=%v", cleared, err)
	}
	if got := onetooneResultCount(t, d, ctx, activity.ID, 1); got != 0 {
		t.Fatalf("rows after reset = %d", got)
	}
	if reloadActivity(t, d, ctx, activity.ID).OnetooneStatus != "" {
		t.Fatal("status was not cleared")
	}
}

func TestIntegrationOnetooneRoundsAndValidation(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})
	male1 := createOnetooneFixtureUser(t, d, ctx, "男甲", "男性", "ou_oto_m1_"+randomDigits(t, 6))
	male2 := createOnetooneFixtureUser(t, d, ctx, "男乙", "男性", "ou_oto_m2_"+randomDigits(t, 6))
	female1 := createOnetooneFixtureUser(t, d, ctx, "女甲", "女性", "ou_oto_f1_"+randomDigits(t, 6))
	female2 := createOnetooneFixtureUser(t, d, ctx, "女乙", "女性", "ou_oto_f2_"+randomDigits(t, 6))
	for _, id := range []int64{male1, male2, female1, female2} {
		signupOnetooneUser(t, d, ctx, activity.ActivityNo, id)
	}

	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 2, 1); err != nil {
		t.Fatal(err)
	}
	rounds, err := d.OnetooneRounds(ctx, activity.ID)
	if err != nil || len(rounds) != 2 || rounds[0] != 1 || rounds[1] != 2 {
		t.Fatalf("rounds = %v err=%v", rounds, err)
	}
	if onetooneResultCount(t, d, ctx, activity.ID, 1) != 4 ||
		onetooneResultCount(t, d, ctx, activity.ID, 2) != 4 {
		t.Fatal("round history was not preserved")
	}
	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 2, 1); !errors.Is(err, ErrOnetooneRoundExists) {
		t.Fatalf("round 2 rerun err = %v", err)
	}

	// K > male count is rejected without wiping the completed status.
	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 3, 3); !errors.Is(err, ErrOnetooneFemaleChats) {
		t.Fatalf("K=3 err = %v", err)
	}
	if onetooneResultCount(t, d, ctx, activity.ID, 3) != 0 {
		t.Fatal("invalid K wrote rows")
	}
	if reloadActivity(t, d, ctx, activity.ID).OnetooneStatus != OnetooneStatusDone {
		t.Fatal("invalid K changed status")
	}

	// Group mode is mutually exclusive.
	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET group_enabled = true WHERE id = $1`, activity.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecuteOnetoone(ctx, activity.ID, 3, 1); !errors.Is(err, ErrOnetooneGroupMode) {
		t.Fatalf("group mode err = %v", err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET group_enabled = false WHERE id = $1`, activity.ID); err != nil {
		t.Fatal(err)
	}

	// Sign-in gate: with signin enabled, only checked-in signups participate.
	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET signin_enabled = true WHERE id = $1`, activity.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE signups SET attendance = '按时'
		  WHERE activity_id = $1 AND user_id IN ($2, $3)`,
		activity.ID, male1, female1); err != nil {
		t.Fatal(err)
	}
	run, err := d.ExecuteOnetoone(ctx, activity.ID, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if run.Participants != 2 || run.MaleCount != 1 || run.FemaleCount != 1 || run.Pairs != 2 {
		t.Fatalf("signin-filtered run = %+v", run)
	}
}

func TestIntegrationOnetooneA0011RealData(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	var total int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM onetoone_results`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	activity, err := d.GetActivity(ctx, "A-0011")
	if errors.Is(err, ErrActivityNotFound) {
		t.Skip("A-0011 migrated data not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	if total != 2972 {
		t.Fatalf("migrated onetoone_results total = %d, want 2972", total)
	}
	counts, err := d.OnetooneRoundCounts(ctx, activity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 9 || counts[9] != 90 {
		t.Fatalf("A-0011 round counts = %v", counts)
	}
	latest, err := d.LatestOnetooneRound(ctx, activity.ID)
	if err != nil || latest != 9 {
		t.Fatalf("latest = %d err=%v", latest, err)
	}
	user, err := d.FindUserByRef(ctx, "U-0006")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListOnetooneResultsForUser(ctx, activity.ID, user.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets := []int64{21, 9, 19, 1110, 15}
	if len(rows) != len(wantTargets) {
		t.Fatalf("U-0006 round 9 partners = %d, want %d (%+v)", len(rows), len(wantTargets), rows)
	}
	for i, id := range wantTargets {
		if rows[i].TargetID != id || rows[i].Rank != i+1 {
			t.Fatalf("U-0006 partner[%d] = %+v, want target %d", i, rows[i], id)
		}
		if rows[i].TargetLegacyID == "" || rows[i].TargetDisplayName() == "" {
			t.Fatalf("U-0006 partner[%d] missing display fields: %+v", i, rows[i])
		}
	}
	chatted, err := d.ChattedTargetIDs(ctx, user.ID, wantTargets)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range wantTargets {
		if !chatted[id] {
			t.Fatalf("U-0006 should have chatted with %d; chatted=%v", id, chatted)
		}
	}
	// Round 1 is an older, smaller fixture and must remain readable.
	first, err := d.ListOnetooneResultsForUser(ctx, activity.ID, user.ID, 1)
	if err != nil || len(first) == 0 {
		t.Fatalf("U-0006 round 1 rows = %d err=%v", len(first), err)
	}

	// Every migrated result row must be reachable through the admin query, not
	// just A-0011: the acceptance base is the full 2972-row table.
	loaded := 0
	for _, ref := range []string{"A-0011", "A-0012", "A-0033"} {
		act, err := d.GetActivity(ctx, ref)
		if err != nil {
			t.Fatalf("load %s: %v", ref, err)
		}
		refRounds, err := d.OnetooneRounds(ctx, act.ID)
		if err != nil {
			t.Fatalf("rounds %s: %v", ref, err)
		}
		for _, round := range refRounds {
			rows, err := d.ListOnetooneResults(ctx, act.ID, round)
			if err != nil {
				t.Fatalf("list %s round %d: %v", ref, round, err)
			}
			for _, row := range rows {
				if row.UserID == 0 || row.TargetID == 0 || row.Rank < 1 ||
					row.TargetDisplayName() == "" || row.Gender == "" {
					t.Fatalf("unusable migrated row in %s round %d: %+v", ref, round, row)
				}
			}
			loaded += len(rows)
		}
	}
	if loaded != total {
		t.Fatalf("admin query loaded %d rows, want %d", loaded, total)
	}
}
