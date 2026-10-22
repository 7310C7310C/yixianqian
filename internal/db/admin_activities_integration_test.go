package db

import (
	"errors"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

func TestMaskPhone(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "mobile", in: "13812341234", want: "138****1234"},
		{name: "trimmed", in: " 13800000006 ", want: "138****0006"},
		{name: "short", in: "12345", want: "1***5"},
		{name: "empty", in: "", want: ""},
		{name: "junk", in: "abc", want: "***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskPhone(tc.in); got != tc.want {
				t.Fatalf("maskPhone(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIntegrationAdminActivityRosterAndAttendance(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})

	user1 := createActivityFixtureUser(t, d, ctx, "甲")
	user2 := createActivityFixtureUser(t, d, ctx, "乙")
	user3 := createActivityFixtureUser(t, d, ctx, "丙")
	outsider := createActivityFixtureUser(t, d, ctx, "丁")
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET gender = '女', nickname = '用户昵称甲' WHERE id = $1`, user1); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{user1, user2, user3} {
		if _, err := d.SignupActivity(ctx, userID, activity.ActivityNo); err != nil {
			t.Fatalf("signup %d: %v", userID, err)
		}
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE signups SET nickname = '报名昵称甲' WHERE activity_id = $1 AND user_id = $2`,
		activity.ID, user1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE signups SET nickname = '' WHERE activity_id = $1 AND user_id = $2`,
		activity.ID, user3); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET nickname = '用户昵称丙' WHERE id = $1`, user3); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CancelSignup(ctx, user2, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}

	roster, err := d.AdminActivityRoster(ctx, activity.ID)
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	if len(roster) != 2 {
		t.Fatalf("roster len = %d, want 2: %+v", len(roster), roster)
	}
	byUser := make(map[int64]AdminActivityRosterEntry, len(roster))
	for _, entry := range roster {
		byUser[entry.UserID] = entry
	}
	first, ok := byUser[user1]
	if !ok {
		t.Fatalf("roster missing user1: %+v", roster)
	}
	if first.Name != "甲" || first.Nickname != "报名昵称甲" || first.Gender != "女" {
		t.Fatalf("roster user1 = %+v", first)
	}
	if first.LegacyUserID == "" || first.SignupAt.IsZero() {
		t.Fatalf("roster user1 missing legacy id or signup time: %+v", first)
	}
	third, ok := byUser[user3]
	if !ok || third.Nickname != "用户昵称丙" {
		t.Fatalf("roster nickname fallback = %+v", third)
	}
	var rawPhone string
	if err := d.pool.QueryRow(ctx, `SELECT phone FROM users WHERE id = $1`, user1).Scan(&rawPhone); err != nil {
		t.Fatal(err)
	}
	if first.PhoneMasked == rawPhone || first.PhoneMasked != maskPhone(rawPhone) {
		t.Fatalf("phone mask = %q raw = %q", first.PhoneMasked, rawPhone)
	}

	if state, err := d.AdminSetActivityAttendance(ctx, activity.ID, user1, AttendanceOnTime); err != nil || state != AttendanceOnTime {
		t.Fatalf("set attendance = %q err = %v", state, err)
	}
	stats, err := d.AdminActivitySigninStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats[activity.ID]; got.SignupCount != 2 || got.CheckedInCount != 1 {
		t.Fatalf("stats after on-time = %+v", got)
	}
	if state, err := d.AdminSetActivityAttendance(ctx, activity.ID, user1, AttendanceLate); err != nil || state != AttendanceLate {
		t.Fatalf("set late = %q err = %v", state, err)
	}
	if state, err := d.AdminSetActivityAttendance(ctx, activity.ID, user1, ""); err != nil || state != "" {
		t.Fatalf("clear attendance = %q err = %v", state, err)
	}
	stats, err = d.AdminActivitySigninStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats[activity.ID]; got.CheckedInCount != 0 {
		t.Fatalf("stats after clear = %+v", got)
	}
	if _, err := d.AdminSetActivityAttendance(ctx, activity.ID, user1, Attendance("早退")); !errors.Is(err, ErrInvalidAttendance) {
		t.Fatalf("invalid attendance err = %v, want ErrInvalidAttendance", err)
	}
	if _, err := d.AdminSetActivityAttendance(ctx, activity.ID, outsider, AttendanceOnTime); !errors.Is(err, ErrNotSignedUp) {
		t.Fatalf("outsider attendance err = %v, want ErrNotSignedUp", err)
	}

	// A cancelled signup is history: the update must not rewrite it.
	if _, err := d.AdminSetActivityAttendance(ctx, activity.ID, user3, AttendanceOnTime); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CancelSignup(ctx, user3, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AdminSetActivityAttendance(ctx, activity.ID, user3, AttendanceLate); !errors.Is(err, ErrNotSignedUp) {
		t.Fatalf("cancelled attendance err = %v, want ErrNotSignedUp", err)
	}
	var stored string
	if err := d.pool.QueryRow(ctx,
		`SELECT attendance FROM signups WHERE activity_id = $1 AND user_id = $2`,
		activity.ID, user3).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(AttendanceOnTime) {
		t.Fatalf("cancelled signup attendance = %q, want %q", stored, AttendanceOnTime)
	}
}

func TestIntegrationAdminActivityPoster(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})
	user := createActivityFixtureUser(t, d, ctx, "海报上传者")

	mediaID, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	objectKey := "media/" + mediaID + ".png"
	thumbKey := "media/" + mediaID + "_thumb.jpg"
	if _, err := d.CreateMedia(ctx, Media{
		ID:           mediaID,
		UserID:       user,
		OriginalName: "poster.png",
		ObjectKey:    objectKey,
		ThumbKey:     thumbKey,
		ContentType:  "image/png",
		ByteSize:     10,
		ThumbSize:    5,
		Width:        40,
		Height:       30,
	}); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if err := d.SetActivityPoster(ctx, activity.ID, mediaID); err != nil {
		t.Fatalf("set poster: %v", err)
	}
	reloaded := reloadActivity(t, d, ctx, activity.ID)
	if reloaded.PosterMediaID == nil || *reloaded.PosterMediaID != mediaID {
		t.Fatalf("poster id = %v, want %q", reloaded.PosterMediaID, mediaID)
	}
	poster, err := d.GetActivityPoster(ctx, activity.ActivityNo)
	if err != nil {
		t.Fatalf("get poster: %v", err)
	}
	if poster.ObjectKey != objectKey || poster.ThumbKey != thumbKey {
		t.Fatalf("poster object = %+v", poster)
	}
	if err := d.SetActivityPoster(ctx, activity.ID, ""); err != nil {
		t.Fatalf("clear poster: %v", err)
	}
	reloaded = reloadActivity(t, d, ctx, activity.ID)
	if reloaded.PosterMediaID != nil {
		t.Fatalf("poster not cleared: %v", reloaded.PosterMediaID)
	}
	if err := d.SetActivityPoster(ctx, 999999999, mediaID); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("missing activity err = %v, want ErrActivityNotFound", err)
	}
}

func TestIntegrationAdminActivityBroadcastAudience(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})

	activeUser := createActivityFixtureUser(t, d, ctx, "报名有效")
	matchedUser := createActivityFixtureUser(t, d, ctx, "报名已配对")
	disabledUser := createActivityFixtureUser(t, d, ctx, "报名已停用")
	cancelledUser := createActivityFixtureUser(t, d, ctx, "报名已取消")
	pendingUser := createActivityFixtureUser(t, d, ctx, "报名待审核")
	observer := createActivityFixtureUser(t, d, ctx, "观察员")
	observerDisabled := createActivityFixtureUser(t, d, ctx, "观察员停用")
	observerPending := createActivityFixtureUser(t, d, ctx, "观察员待审核")
	createActivityFixtureUser(t, d, ctx, "普通用户")

	for _, userID := range []int64{activeUser, matchedUser, disabledUser, cancelledUser} {
		if _, err := d.SignupActivity(ctx, userID, activity.ActivityNo); err != nil {
			t.Fatalf("signup %d: %v", userID, err)
		}
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'matched' WHERE id = $1`, matchedUser); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET status = 'disabled' WHERE id = $1`, disabledUser); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CancelSignup(ctx, cancelledUser, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}
	// Insert directly: SignupActivity rejects pending accounts, but imported
	// data can still carry an active signup row for a pending user.
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status)
		 VALUES ($1, $2, '待审核', $3)`, activity.ID, pendingUser, SignupStatusSigned); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'pending' WHERE id = $1`, pendingUser); err != nil {
		t.Fatal(err)
	}

	for _, userID := range []int64{observer, observerDisabled, observerPending} {
		if _, err := d.pool.Exec(ctx,
			`UPDATE users SET observer_enabled = true, account_kind = 'observer', account_status = 'observer'
			  WHERE id = $1`, userID); err != nil {
			t.Fatalf("set observer %d: %v", userID, err)
		}
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET status = 'disabled' WHERE id = $1`, observerDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'pending' WHERE id = $1`, observerPending); err != nil {
		t.Fatal(err)
	}

	audience, err := d.AdminActivityBroadcastAudience(ctx, activity.ID, true, true)
	if err != nil {
		t.Fatalf("audience: %v", err)
	}
	assertIDSet(t, "signups", audience.SignupUserIDs, []int64{activeUser, matchedUser})
	// Observer lookup is global by design; only assert membership of the
	// fixtures so a shared test database with other observers stays valid.
	observerSet := make(map[int64]bool, len(audience.ObserverUserIDs))
	for _, id := range audience.ObserverUserIDs {
		observerSet[id] = true
	}
	if !observerSet[observer] {
		t.Fatalf("observers missing fixture %d: %+v", observer, audience.ObserverUserIDs)
	}
	for _, excluded := range []int64{observerDisabled, observerPending} {
		if observerSet[excluded] {
			t.Fatalf("observer %d should be excluded: %+v", excluded, audience.ObserverUserIDs)
		}
	}

	onlyObservers, err := d.AdminActivityBroadcastAudience(ctx, activity.ID, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyObservers.SignupUserIDs) != 0 {
		t.Fatalf("signups should be empty: %+v", onlyObservers.SignupUserIDs)
	}
	if !containsID(onlyObservers.ObserverUserIDs, observer) {
		t.Fatalf("observers only missing fixture %d: %+v", observer, onlyObservers.ObserverUserIDs)
	}

	none, err := d.AdminActivityBroadcastAudience(ctx, activity.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if none.SignupUserIDs == nil || none.ObserverUserIDs == nil ||
		len(none.SignupUserIDs) != 0 || len(none.ObserverUserIDs) != 0 {
		t.Fatalf("empty audience = %+v", none)
	}

}

func assertIDSet(t *testing.T, label string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	seen := make(map[int64]int, len(got))
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		if seen[id] != 1 {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
