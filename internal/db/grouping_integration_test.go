package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupGroupingDB(t *testing.T) (*DB, *pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return database, pool, ctx
}

func createGroupUser(t *testing.T, ctx context.Context, database *DB, pool *pgxpool.Pool,
	prefix, gender string) User {
	t.Helper()
	legacy := prefix + "-" + randomDigits(t, 10)
	user, err := database.UpsertLegacyUser(ctx, legacy, "1"+randomDigits(t, 10), prefix+"名字")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET gender = $2, nickname = $3, account_status = 'active'
		  WHERE id = $1`, user.ID, gender, prefix+"暱称"); err != nil {
		t.Fatalf("update user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user
}

func createGroupActivity(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	signin bool, male, female int) Activity {
	t.Helper()
	activityNo := "A-GRP-" + randomDigits(t, 8)
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO activities (activity_no, name, status, group_status, group_enabled,
		                         signin_enabled, male_per_group, female_per_group)
		 VALUES ($1, $2, '报名中', '', true, $3, $4, $5)
		 RETURNING id`,
		activityNo, "分组测试活动 "+activityNo, signin, male, female).Scan(&id)
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activities WHERE id = $1`, id)
	})
	activity, err := getActivityByID(ctx, pool, id)
	if err != nil {
		t.Fatalf("load activity: %v", err)
	}
	return activity
}

func getActivityByID(ctx context.Context, pool *pgxpool.Pool, id int64) (Activity, error) {
	return scanActivity(pool.QueryRow(ctx,
		`SELECT `+activityColumns+` FROM activities WHERE id = $1`, id))
}

func addGroupSignup(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	activityID, userID int64, status string, attendance Attendance) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, status, attendance)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (activity_id, user_id) DO UPDATE
		   SET status = EXCLUDED.status, attendance = EXCLUDED.attendance`,
		activityID, userID, status, string(attendance)); err != nil {
		t.Fatalf("add signup: %v", err)
	}
}

func addGroupSelection(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	activityID, userID int64, gender string, choices []int64) {
	t.Helper()
	encoded, err := json.Marshal(choices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_selections (activity_id, selector_id, gender, choices, round)
		 VALUES ($1, $2, $3, $4, 1)
		 ON CONFLICT (activity_id, selector_id) DO UPDATE
		   SET gender = EXCLUDED.gender, choices = EXCLUDED.choices,
		       updated_at = now()`,
		activityID, userID, gender, encoded); err != nil {
		t.Fatalf("add selection: %v", err)
	}
}

func TestIntegrationGroupCandidatesAndSelect(t *testing.T) {
	database, pool, ctx := setupGroupingDB(t)
	activity := createGroupActivity(t, ctx, pool, false, 2, 2)
	viewer := createGroupUser(t, ctx, database, pool, "GRP-VIEW", "女性")
	otherFemale := createGroupUser(t, ctx, database, pool, "GRP-OTHER", "女性")
	notSignedUp := createGroupUser(t, ctx, database, pool, "GRP-NOSIGN", "男性")

	var males []User
	for i := 0; i < 8; i++ {
		male := createGroupUser(t, ctx, database, pool, fmt.Sprintf("GRP-M%d", i), "男性")
		males = append(males, male)
	}
	addGroupSignup(t, ctx, pool, activity.ID, viewer.ID, SignupStatusSigned, AttendanceOnTime)
	addGroupSignup(t, ctx, pool, activity.ID, otherFemale.ID, SignupStatusSigned, "")
	for i, male := range males {
		status := SignupStatusSigned
		if i == 7 {
			status = SignupStatusCancelled
		}
		addGroupSignup(t, ctx, pool, activity.ID, male.ID, status, AttendanceOnTime)
	}

	_, candidates, err := database.GroupCandidates(ctx, viewer.ID, activity.ActivityNo)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(candidates) != 7 {
		t.Fatalf("candidates = %d, want 7", len(candidates))
	}
	for _, candidate := range candidates {
		if candidate.UserID == otherFemale.ID || candidate.UserID == viewer.ID {
			t.Fatalf("candidate pool leaked same gender/self: %+v", candidate)
		}
		if candidate.UserID == males[7].ID {
			t.Fatal("cancelled signup appeared in candidates")
		}
	}

	choices := make([]int64, 0, 7)
	for _, male := range males[:7] {
		choices = append(choices, male.ID)
	}
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		choices[:6]); !errors.Is(err, ErrGroupChoiceCount) {
		t.Fatalf("short selection error = %v", err)
	}
	dup := append([]int64{}, choices...)
	dup[6] = dup[0]
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		dup); !errors.Is(err, ErrGroupChoiceDuplicate) {
		t.Fatalf("duplicate selection error = %v", err)
	}
	self := append([]int64{}, choices...)
	self[0] = viewer.ID
	var choiceErr *GroupChoiceError
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		self); !errors.As(err, &choiceErr) || !errors.Is(err, ErrGroupChoiceInvalid) {
		t.Fatalf("self selection error = %v", err)
	}
	sameGender := append([]int64{}, choices...)
	sameGender[0] = otherFemale.ID
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		sameGender); !errors.Is(err, ErrGroupChoiceInvalid) {
		t.Fatalf("same gender selection error = %v", err)
	}
	unsigned := append([]int64{}, choices...)
	unsigned[0] = notSignedUp.ID
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		unsigned); !errors.Is(err, ErrGroupChoiceInvalid) {
		t.Fatalf("unsigned selection error = %v", err)
	}

	selection, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo, choices)
	if err != nil {
		t.Fatalf("select choices: %v", err)
	}
	if len(selection.Choices) != 7 || selection.Choices[0] != choices[0] {
		t.Fatalf("stored choices = %v", selection.Choices)
	}
	// Updating replaces the row instead of appending a second one.
	reversed := make([]int64, len(choices))
	for i := range choices {
		reversed[i] = choices[len(choices)-1-i]
	}
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		reversed); err != nil {
		t.Fatalf("update choices: %v", err)
	}
	var selectionRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM group_selections WHERE activity_id = $1 AND selector_id = $2`,
		activity.ID, viewer.ID).Scan(&selectionRows); err != nil {
		t.Fatal(err)
	}
	if selectionRows != 1 {
		t.Fatalf("selection rows = %d, want 1", selectionRows)
	}
	status, err := database.GroupStatusForUser(ctx, viewer.ID, activity.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Selected || len(status.Choices) != 7 || status.Choices[0] != reversed[0] {
		t.Fatalf("status = %+v", status)
	}
	otherStatus, err := database.GroupStatusForUser(ctx, otherFemale.ID, activity.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if otherStatus.Selected || len(otherStatus.Choices) != 0 {
		t.Fatalf("another user saw leaked choices: %+v", otherStatus)
	}

	// Concurrent double submits must leave exactly one row and one of the
	// complete choice sets.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		permutation := make([]int64, len(choices))
		for j := range choices {
			permutation[j] = choices[(j+i)%len(choices)]
		}
		wg.Add(1)
		go func(p []int64) {
			defer wg.Done()
			_, err := database.SelectGroupChoices(context.Background(), viewer.ID,
				activity.ActivityNo, p)
			errs <- err
		}(permutation)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent select: %v", err)
		}
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM group_selections WHERE activity_id = $1 AND selector_id = $2`,
		activity.ID, viewer.ID).Scan(&selectionRows); err != nil {
		t.Fatal(err)
	}
	if selectionRows != 1 {
		t.Fatalf("selection rows after concurrency = %d, want 1", selectionRows)
	}
}

func TestIntegrationGroupSigninGate(t *testing.T) {
	database, pool, ctx := setupGroupingDB(t)
	activity := createGroupActivity(t, ctx, pool, true, 1, 1)
	viewer := createGroupUser(t, ctx, database, pool, "GRP-SIGN-VIEW", "女性")
	onTime := createGroupUser(t, ctx, database, pool, "GRP-SIGN-OK", "男性")
	late := createGroupUser(t, ctx, database, pool, "GRP-SIGN-LATE", "男性")
	absent := createGroupUser(t, ctx, database, pool, "GRP-SIGN-ABSENT", "男性")
	empty := createGroupUser(t, ctx, database, pool, "GRP-SIGN-EMPTY", "男性")
	addGroupSignup(t, ctx, pool, activity.ID, viewer.ID, SignupStatusSigned, "")
	addGroupSignup(t, ctx, pool, activity.ID, onTime.ID, SignupStatusSigned, AttendanceOnTime)
	addGroupSignup(t, ctx, pool, activity.ID, late.ID, SignupStatusSigned, AttendanceLate)
	addGroupSignup(t, ctx, pool, activity.ID, absent.ID, SignupStatusSigned, AttendanceAbsent)
	addGroupSignup(t, ctx, pool, activity.ID, empty.ID, SignupStatusSigned, "")

	if _, _, err := database.GroupCandidates(ctx, viewer.ID, activity.ActivityNo); !errors.Is(err, ErrGroupSigninRequired) {
		t.Fatalf("unchecked viewer candidates error = %v", err)
	}
	if _, err := database.SelectGroupChoices(ctx, viewer.ID, activity.ActivityNo,
		[]int64{onTime.ID, late.ID, absent.ID, empty.ID, onTime.ID, late.ID, absent.ID}); !errors.Is(err, ErrGroupSigninRequired) {
		t.Fatalf("unchecked viewer select error = %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE signups SET attendance = $3 WHERE activity_id = $1 AND user_id = $2`,
		activity.ID, viewer.ID, string(AttendanceOnTime)); err != nil {
		t.Fatal(err)
	}
	_, candidates, err := database.GroupCandidates(ctx, viewer.ID, activity.ActivityNo)
	if err != nil {
		t.Fatalf("candidates after signin: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("checked-in candidates = %d, want 2: %+v", len(candidates), candidates)
	}
	for _, candidate := range candidates {
		if candidate.UserID != onTime.ID && candidate.UserID != late.ID {
			t.Fatalf("unchecked candidate leaked: %+v", candidate)
		}
	}
}

func TestIntegrationGroupingRunAndResult(t *testing.T) {
	database, pool, ctx := setupGroupingDB(t)
	activity := createGroupActivity(t, ctx, pool, false, 2, 2)
	var males, females []User
	for i := 0; i < 5; i++ {
		male := createGroupUser(t, ctx, database, pool, fmt.Sprintf("GRP-RUN-M%d", i), "男性")
		female := createGroupUser(t, ctx, database, pool, fmt.Sprintf("GRP-RUN-F%d", i), "女性")
		males = append(males, male)
		females = append(females, female)
		addGroupSignup(t, ctx, pool, activity.ID, male.ID, SignupStatusSigned, AttendanceOnTime)
		addGroupSignup(t, ctx, pool, activity.ID, female.ID, SignupStatusSigned, AttendanceOnTime)
	}
	femaleIDs := make([]int64, 0, len(females))
	for _, female := range females {
		femaleIDs = append(femaleIDs, female.ID)
	}
	maleIDs := make([]int64, 0, len(males))
	for _, male := range males {
		maleIDs = append(maleIDs, male.ID)
	}
	for _, male := range males {
		addGroupSelection(t, ctx, pool, activity.ID, male.ID, "男性", femaleIDs)
	}
	for _, female := range females {
		addGroupSelection(t, ctx, pool, activity.ID, female.ID, "女性", maleIDs)
	}

	preview, err := database.PreviewGrouping(ctx, activity.ActivityNo, 0, 0, 0)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.CanExecute || preview.ExpectedGroups != 2 || preview.ParticipantCount != 10 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Round != 1 {
		t.Fatalf("default preview round = %d, want 1", preview.Round)
	}

	run, err := database.RunGrouping(ctx, activity.ActivityNo, 0, 0, 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.Round != 1 || run.GroupCount != 2 || run.ParticipantCount != 10 {
		t.Fatalf("run = %+v", run)
	}
	if _, err := database.RunGrouping(ctx, activity.ActivityNo, 1, 0, 0); !errors.Is(err, ErrGroupAlreadyDone) {
		t.Fatalf("repeat round error = %v", err)
	}
	latest, err := database.LatestGroupRound(ctx, activity.ActivityNo)
	if err != nil || latest != 1 {
		t.Fatalf("latest round = %d err=%v", latest, err)
	}
	loaded, err := database.GetActivity(ctx, activity.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GroupStatus != "已完成" || loaded.MalePerGroup != 2 || loaded.FemalePerGroup != 2 {
		t.Fatalf("activity after run = %+v", loaded)
	}

	second, err := database.RunGrouping(ctx, activity.ActivityNo, 0, 0, 0)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Round != 2 {
		t.Fatalf("second round = %d", second.Round)
	}
	admin, err := database.AdminGroupResult(ctx, activity.ActivityNo, 0)
	if err != nil {
		t.Fatalf("admin result: %v", err)
	}
	if admin.Round != 2 || len(admin.Groups) != 2 {
		t.Fatalf("admin result = %+v", admin)
	}
	adminGroups := map[int64]int{}
	for _, group := range admin.Groups {
		for _, member := range group.Members {
			adminGroups[member.UserID] = group.GroupID
		}
	}

	// A user only ever sees their own group, even though the admin result has two.
	target := females[0]
	userResult, err := database.GroupResultForUser(ctx, target.ID, activity.ActivityNo, 0)
	if err != nil {
		t.Fatalf("user result: %v", err)
	}
	if userResult.Round != 2 || userResult.MyGroup == 0 || userResult.TotalGroups != 2 {
		t.Fatalf("user result = %+v", userResult)
	}
	if len(userResult.Members) == 0 {
		t.Fatal("user result had no members")
	}
	for _, member := range userResult.Members {
		if adminGroups[member.UserID] != userResult.MyGroup {
			t.Fatalf("result leaked another group member %d", member.UserID)
		}
	}
	// A user outside the grouping pool still gets no other group.
	outsider := createGroupUser(t, ctx, database, pool, "GRP-RUN-OUT", "女性")
	outsideResult, err := database.GroupResultForUser(ctx, outsider.ID, activity.ActivityNo, 0)
	if err != nil {
		t.Fatalf("outsider result: %v", err)
	}
	if outsideResult.MyGroup != 0 || len(outsideResult.Members) != 0 {
		t.Fatalf("outsider saw a group: %+v", outsideResult)
	}

	cleared, err := database.ResetGrouping(ctx, activity.ActivityNo)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if cleared != 20 {
		t.Fatalf("cleared = %d, want 20", cleared)
	}
	cleared, err = database.ResetGrouping(ctx, activity.ActivityNo)
	if err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if cleared != 0 {
		t.Fatalf("second reset cleared = %d", cleared)
	}
	loaded, err = database.GetActivity(ctx, activity.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GroupStatus != "" {
		t.Fatalf("group status after reset = %q", loaded.GroupStatus)
	}
	if _, err := database.GroupResultForUser(ctx, target.ID, activity.ActivityNo, 0); !errors.Is(err, ErrGroupNotComplete) {
		t.Fatalf("result after reset error = %v", err)
	}
}

func TestIntegrationGroupingConcurrentRun(t *testing.T) {
	database, pool, ctx := setupGroupingDB(t)
	activity := createGroupActivity(t, ctx, pool, false, 1, 1)
	var participants []User
	for i := 0; i < 4; i++ {
		male := createGroupUser(t, ctx, database, pool, fmt.Sprintf("GRP-CONC-M%d", i), "男性")
		female := createGroupUser(t, ctx, database, pool, fmt.Sprintf("GRP-CONC-F%d", i), "女性")
		participants = append(participants, male, female)
		addGroupSignup(t, ctx, pool, activity.ID, male.ID, SignupStatusSigned, AttendanceOnTime)
		addGroupSignup(t, ctx, pool, activity.ID, female.ID, SignupStatusSigned, AttendanceOnTime)
	}
	maleIDs := []int64{participants[0].ID, participants[2].ID, participants[4].ID, participants[6].ID}
	femaleIDs := []int64{participants[1].ID, participants[3].ID, participants[5].ID, participants[7].ID}
	addGroupSelection(t, ctx, pool, activity.ID, maleIDs[0], "男性", femaleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, maleIDs[1], "男性", femaleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, maleIDs[2], "男性", femaleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, maleIDs[3], "男性", femaleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, femaleIDs[0], "女性", maleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, femaleIDs[1], "女性", maleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, femaleIDs[2], "女性", maleIDs)
	addGroupSelection(t, ctx, pool, activity.ID, femaleIDs[3], "女性", maleIDs)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := database.RunGrouping(context.Background(), activity.ActivityNo, 1, 0, 0)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, already := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrGroupAlreadyDone):
			already++
		default:
			t.Fatalf("concurrent run error = %v", err)
		}
	}
	if success != 1 || already != 1 {
		t.Fatalf("concurrent run success=%d already=%d", success, already)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM group_results WHERE activity_id = $1 AND round = 1`,
		activity.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 8 {
		t.Fatalf("result rows = %d, want 8", rows)
	}
}

// TestStagingA0011Smoke is the real migrated-data smoke test. It is opt-in
// because it reads the staging database instead of the disposable test DB.
func TestStagingA0011Smoke(t *testing.T) {
	if os.Getenv("GROUPING_A0011_SMOKE") != "1" {
		t.Skip("set GROUPING_A0011_SMOKE=1 with TEST_DATABASE_URL=staging to run")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open staging: %v", err)
	}
	defer database.Close()

	activity, err := database.GetActivity(ctx, "A-0011")
	if err != nil {
		t.Fatalf("A-0011: %v", err)
	}
	user, err := database.GetUserByLegacyID(ctx, "U-0006")
	if err != nil {
		t.Fatalf("U-0006: %v", err)
	}
	_, candidates, err := database.GroupCandidates(ctx, user.ID, activity.ActivityNo)
	if err != nil {
		t.Fatalf("A-0011 candidates: %v", err)
	}
	status, err := database.GroupStatusForUser(ctx, user.ID, activity.ActivityNo)
	if err != nil {
		t.Fatalf("A-0011 status: %v", err)
	}
	result, err := database.GroupResultForUser(ctx, user.ID, activity.ActivityNo, 0)
	if err != nil {
		t.Fatalf("A-0011 result: %v", err)
	}
	admin, err := database.AdminGroupResult(ctx, activity.ActivityNo, 0)
	if err != nil {
		t.Fatalf("A-0011 admin result: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("A-0011 returned no candidates for U-0006")
	}
	if !status.Selected || len(status.Choices) != 7 {
		t.Fatalf("A-0011 U-0006 selection = %+v", status)
	}
	if result.MyGroup == 0 || len(result.Members) == 0 {
		t.Fatalf("A-0011 U-0006 result = %+v", result)
	}
	if admin.Round == 0 || len(admin.Groups) == 0 {
		t.Fatalf("A-0011 admin result = %+v", admin)
	}
	t.Logf("A-0011 status=%q candidates=%d choices=%v my_group=%d members=%d round=%d groups=%d",
		result.GroupStatus, len(candidates), status.Choices, result.MyGroup,
		len(result.Members), result.Round, len(admin.Groups))
}
