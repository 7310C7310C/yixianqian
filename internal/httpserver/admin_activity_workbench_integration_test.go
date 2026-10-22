package httpserver

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegrationAdminActivityWorkbenchAccessAndRoster(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "活动管理员")
	reviewer := createMessageUser(t, ctx, database, pool, "活动审核员")
	plain := createMessageUser(t, ctx, database, pool, "活动普通用户")
	signupUser := createMessageUser(t, ctx, database, pool, "报名用户")
	ids := []int64{manager.ID, reviewer.ID, plain.ID, signupUser.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	if changed, err := database.AssignAdminRole(ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}

	activity := createWorkbenchActivity(t, ctx, database, pool, "工作台访问测试")
	if _, err := database.SignupActivity(ctx, signupUser.ID, activity.ActivityNo); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET gender = '女' WHERE id = $1`, signupUser.ID); err != nil {
		t.Fatal(err)
	}
	wantMask := signupUser.Phone[:3] + "****" + signupUser.Phone[len(signupUser.Phone)-4:]

	managerClient := loginMessageClient(t, database, server, manager)
	reviewerClient := loginMessageClient(t, database, server, reviewer)
	plainClient := loginMessageClient(t, database, server, plain)

	resp := managerClient.get("/admin/activities/" + strconv.FormatInt(activity.ID, 10))
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workbench status = %d body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{"工作台", "报名/签到名单", signupUser.Name, wantMask, "站内广播", "快速设置", "匹配控制台尚未在本期交付"} {
		if !strings.Contains(body, want) {
			t.Fatalf("workbench missing %q", want)
		}
	}
	if strings.Contains(body, signupUser.Phone) {
		t.Fatal("workbench leaked the raw phone number")
	}

	resp = reviewerClient.get("/admin/activities/" + strconv.FormatInt(activity.ID, 10))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer workbench status = %d", resp.StatusCode)
	}
	resp = plainClient.get("/admin/activities/" + strconv.FormatInt(activity.ID, 10))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain workbench status = %d", resp.StatusCode)
	}
	resp = reviewerClient.get("/admin/activities/" + strconv.FormatInt(activity.ID, 10) + "/poster")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer poster status = %d", resp.StatusCode)
	}
	resp = managerClient.get("/admin/activities/" + strconv.FormatInt(activity.ID, 10) + "/poster")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing poster status = %d", resp.StatusCode)
	}
	resp = managerClient.get("/admin/activities/999999999")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing activity status = %d", resp.StatusCode)
	}
}

func TestIntegrationAdminActivityAttendancePosterAndBroadcast(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "活动运营")
	signupUser := createMessageUser(t, ctx, database, pool, "签到用户")
	observer := createMessageUser(t, ctx, database, pool, "广播观察员")
	nonRecipient := createMessageUser(t, ctx, database, pool, "非收件人")
	ids := []int64{manager.ID, signupUser.ID, observer.ID, nonRecipient.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}

	activity := createWorkbenchActivity(t, ctx, database, pool, "签到海报广播测试")
	if _, err := database.SignupActivity(ctx, signupUser.ID, activity.ActivityNo); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET account_kind = 'observer', account_status = 'observer', observer_enabled = true
		  WHERE id = $1`, observer.ID); err != nil {
		t.Fatalf("set observer: %v", err)
	}

	client := loginMessageClient(t, database, server, manager)
	workbenchPath := "/admin/activities/" + strconv.FormatInt(activity.ID, 10)
	csrf := client.pageCSRF(workbenchPath)

	// Attendance requires CSRF and writes an audit row.
	resp := client.postForm(workbenchPath+"/attendance", url.Values{
		"user_id": {strconv.FormatInt(signupUser.ID, 10)}, "attendance": {string(db.AttendanceOnTime)},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("attendance without csrf status = %d", resp.StatusCode)
	}
	resp = client.postForm(workbenchPath+"/attendance", url.Values{
		"csrf_token": {csrf},
		"user_id":    {strconv.FormatInt(signupUser.ID, 10)},
		"attendance": {string(db.AttendanceOnTime)},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("attendance status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if state, err := database.GetAttendance(ctx, activity.ID, signupUser.ID); err != nil || state != db.AttendanceOnTime {
		t.Fatalf("attendance = %q err=%v", state, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.activity.attendance' AND actor_user_id = $1 AND target_id = $2`,
		manager.ID, activity.ActivityNo).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("attendance was not audited")
	}
	var attendanceMeta string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.activity.attendance' AND actor_user_id = $1 AND target_id = $2
		 ORDER BY id DESC LIMIT 1`, manager.ID, activity.ActivityNo).Scan(&attendanceMeta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(attendanceMeta, signupUser.Name) || strings.Contains(attendanceMeta, signupUser.Phone) {
		t.Fatalf("attendance audit leaked identity: %s", attendanceMeta)
	}
	resp = client.postForm(workbenchPath+"/attendance", url.Values{
		"csrf_token": {csrf}, "user_id": {strconv.FormatInt(signupUser.ID, 10)}, "attendance": {""},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear attendance status = %d", resp.StatusCode)
	}
	if state, err := database.GetAttendance(ctx, activity.ID, signupUser.ID); err != nil || state != "" {
		t.Fatalf("cleared attendance = %q err=%v", state, err)
	}

	// Poster upload: CSRF is checked after multipart parsing.
	posterPath := workbenchPath + "/poster"
	resp = client.postMultipart(posterPath, "", "poster.png", "image/png", testPNG(t))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("poster upload without csrf status = %d", resp.StatusCode)
	}
	resp = client.postMultipart(posterPath, csrf, "poster.png", "image/png", testPNG(t))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("poster upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	reloaded, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10))
	if err != nil || reloaded.PosterMediaID == nil {
		t.Fatalf("poster not linked: %+v err=%v", reloaded, err)
	}
	resp = client.get(posterPath)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("poster stream status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	_ = readBody(t, resp)
	resp = client.get(posterPath + "?size=thumb")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("poster thumb status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	_ = readBody(t, resp)
	var objectKey string
	if err := pool.QueryRow(ctx,
		`SELECT m.object_key FROM activities a JOIN media m ON m.id = a.poster_media_id
		  WHERE a.id = $1`, activity.ID).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	resp = client.get(workbenchPath)
	workbenchHTML := readBody(t, resp)
	if objectKey == "" || strings.Contains(workbenchHTML, objectKey) {
		t.Fatalf("workbench leaked object key %q", objectKey)
	}
	var posterMeta string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.activity.poster' AND actor_user_id = $1 AND target_id = $2
		 ORDER BY id DESC LIMIT 1`, manager.ID, activity.ActivityNo).Scan(&posterMeta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(posterMeta, objectKey) {
		t.Fatalf("poster audit leaked object key: %s", posterMeta)
	}
	if !strings.Contains(workbenchHTML, posterPath+"?size=thumb") {
		t.Fatalf("workbench missing poster thumbnail URL")
	}

	// Broadcast preview and send. Observer lookup is global, so derive the
	// expected count from the database instead of assuming a clean observer set.
	var observerCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users
		  WHERE observer_enabled = true AND status = 'active'
		    AND account_status IN ('active', 'observer')`).Scan(&observerCount); err != nil {
		t.Fatal(err)
	}
	wantPreview := "本次预计发送 " + strconv.Itoa(1+observerCount) + " 人"
	resp = client.get(workbenchPath + "?preview=1&include_signups=1&include_observers=1")
	preview := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(preview, wantPreview) {
		t.Fatalf("broadcast preview status=%d want=%q body=%s", resp.StatusCode, wantPreview, preview)
	}
	key := broadcastKeyFromHTML(t, preview)
	// The in-page preview button posts the same form without sending.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":       {csrf},
		"broadcast_key":    {key},
		"include_signups":  {"1"},
		"broadcast_action": {"preview"},
	})
	previewBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(previewBody, "本次预计发送 1 人") {
		t.Fatalf("post preview status=%d body=%s", resp.StatusCode, previewBody)
	}
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("preview sent notifications: %d", got)
	}
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"broadcast_key": {key}, "include_signups": {"1"}, "broadcast_title": {"无 CSRF"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("broadcast without csrf status = %d", resp.StatusCode)
	}
	broadcastValues := url.Values{
		"csrf_token":        {csrf},
		"broadcast_key":     {key},
		"include_signups":   {"1"},
		"include_observers": {"1"},
		"broadcast_title":   {"活动广播测试"},
		"broadcast_body":    {"请准时参加。"},
	}
	resp = client.postForm(workbenchPath+"/broadcast", broadcastValues)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("broadcast status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 1 {
		t.Fatalf("signup broadcast count = %d, want 1", got)
	}
	if got := notificationCount(t, ctx, pool, observer.ID, "activity_broadcast"); got != 1 {
		t.Fatalf("observer broadcast count = %d, want 1", got)
	}
	if got := notificationCount(t, ctx, pool, nonRecipient.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("non-recipient broadcast count = %d, want 0", got)
	}
	if got := notificationCount(t, ctx, pool, manager.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("manager broadcast count = %d, want 0", got)
	}
	// Repeating the same key is idempotent.
	resp = client.postForm(workbenchPath+"/broadcast", broadcastValues)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("duplicate broadcast status = %d", resp.StatusCode)
	}
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 1 {
		t.Fatalf("duplicate signup broadcast count = %d, want 1", got)
	}
	if got := notificationCount(t, ctx, pool, observer.ID, "activity_broadcast"); got != 1 {
		t.Fatalf("duplicate observer broadcast count = %d, want 1", got)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.activity.broadcast' AND actor_user_id = $1 AND target_id = $2`,
		manager.ID, activity.ActivityNo).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("broadcast was not audited")
	}
	var broadcastMeta string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.activity.broadcast' AND actor_user_id = $1 AND target_id = $2
		 ORDER BY id DESC LIMIT 1`, manager.ID, activity.ActivityNo).Scan(&broadcastMeta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(broadcastMeta, "请准时参加") || strings.Contains(broadcastMeta, signupUser.Phone) {
		t.Fatalf("broadcast audit leaked body or phone: %s", broadcastMeta)
	}
	var payload string
	if err := pool.QueryRow(ctx,
		`SELECT payload::text FROM notifications
		  WHERE user_id = $1 AND kind = 'activity_broadcast'
		  ORDER BY id DESC LIMIT 1`, signupUser.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, activity.ActivityNo) {
		t.Fatalf("broadcast payload = %s", payload)
	}
}

func TestIntegrationAdminActivityListQuickSettingsAndRedirects(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "活动列表管理员")
	signupUser := createMessageUser(t, ctx, database, pool, "列表报名用户")
	ids := []int64{manager.ID, signupUser.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	activity := createWorkbenchActivity(t, ctx, database, pool, "列表快速设置测试")
	if _, err := database.SignupActivity(ctx, signupUser.ID, activity.ActivityNo); err != nil {
		t.Fatalf("signup: %v", err)
	}
	mediaID, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMedia(ctx, db.Media{
		ID: mediaID, UserID: manager.ID, OriginalName: "list-poster.png",
		ObjectKey: "media/" + mediaID + ".png", ThumbKey: "media/" + mediaID + "_thumb.jpg",
		ContentType: "image/png", ByteSize: 10, ThumbSize: 5, Width: 40, Height: 30,
	}); err != nil {
		t.Fatalf("create poster media: %v", err)
	}
	if err := database.SetActivityPoster(ctx, activity.ID, mediaID); err != nil {
		t.Fatalf("set poster: %v", err)
	}

	client := loginMessageClient(t, database, server, manager)
	resp := client.get("/admin/activities")
	listHTML := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d body=%s", resp.StatusCode, listHTML)
	}
	for _, want := range []string{
		"工作台",
		`href="/admin/activities/` + strconv.FormatInt(activity.ID, 10) + `"`,
		`/admin/activities/` + strconv.FormatInt(activity.ID, 10) + `/poster?size=thumb`,
		"已签到",
		"报名 1",
	} {
		if !strings.Contains(listHTML, want) {
			t.Fatalf("list missing %q", want)
		}
	}

	// The quick-settings form reuses the normal update endpoint.
	startAt := ""
	if activity.StartAt != nil {
		startAt = activity.StartAt.Format("2006-01-02T15:04")
	}
	csrf := client.pageCSRF("/admin/activities")
	resp = client.postForm("/admin/activities/"+strconv.FormatInt(activity.ID, 10),
		url.Values{"name": {"缺少 CSRF"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("quick update without csrf status = %d", resp.StatusCode)
	}
	resp = client.postForm("/admin/activities", url.Values{"name": {"缺少 CSRF"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create without csrf status = %d", resp.StatusCode)
	}
	resp = client.postForm("/admin/activities/"+strconv.FormatInt(activity.ID, 10), url.Values{
		"csrf_token":       {csrf},
		"name":             {activity.Name},
		"description":      {"更新后的描述"},
		"location":         {activity.Location},
		"condition":        {activity.Condition},
		"max_signup":       {strconv.Itoa(activity.MaxSignup)},
		"fee":              {activity.Fee},
		"food":             {activity.Food},
		"publish_at":       {""},
		"start_at":         {startAt},
		"end_at":           {""},
		"status":           {"已结束"},
		"signin_enabled":   {"1"},
		"signin_code":      {"4321"},
		"group_enabled":    {"1"},
		"male_per_group":   {"3"},
		"female_per_group": {"2"},
		"onetoone_enabled": {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("quick update status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc,
		"/admin/activities/"+strconv.FormatInt(activity.ID, 10)+"?notice=") {
		t.Fatalf("quick update location = %q", loc)
	}
	updated, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "已结束" || !updated.SigninEnabled || updated.SigninCode != "4321" ||
		!updated.GroupEnabled || updated.MalePerGroup != 3 || updated.FemalePerGroup != 2 ||
		!updated.OnetooneEnabled {
		t.Fatalf("quick settings not applied: %+v", updated)
	}

	// Creating an activity redirects to its new workbench.
	createdName := "新建工作台活动 " + randomHTTPDigits(t, 8)
	resp = client.postForm("/admin/activities", url.Values{
		"csrf_token":       {csrf},
		"name":             {createdName},
		"description":      {"新建描述"},
		"location":         {"新建地点"},
		"condition":        {""},
		"max_signup":       {"10"},
		"fee":              {""},
		"food":             {""},
		"publish_at":       {""},
		"start_at":         {""},
		"end_at":           {""},
		"status":           {"报名中"},
		"male_per_group":   {"0"},
		"female_per_group": {"0"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location := resp.Header.Get("Location")
	createdID := adminActivityIDFromLocation(t, location)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activities WHERE id = $1`, createdID)
	})
	created, err := database.GetActivity(ctx, strconv.FormatInt(createdID, 10))
	if err != nil || created.Name != createdName {
		t.Fatalf("created activity = %+v err=%v", created, err)
	}
}

func createWorkbenchActivity(t *testing.T, ctx context.Context, database *db.DB, pool *pgxpool.Pool, name string) db.Activity {
	t.Helper()
	start := time.Now().Add(-time.Hour)
	activity, err := database.CreateActivity(ctx, db.AdminActivityInput{
		Name:          name + " " + randomHTTPDigits(t, 8),
		Description:   "测试描述",
		Location:      "测试地点",
		Condition:     "测试条件",
		MaxSignup:     20,
		Fee:           "100",
		Food:          "自理",
		Status:        db.ActivityStatusOpen,
		SigninEnabled: true,
		SigninCode:    "0123",
		StartAt:       &start,
	})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activities WHERE id = $1`, activity.ID)
	})
	return activity
}

func notificationCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int64, kind string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = $2`,
		userID, kind).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return count
}

var broadcastKeyRE = regexp.MustCompile(`name="broadcast_key" value="([^"]+)"`)

func broadcastKeyFromHTML(t *testing.T, body string) string {
	t.Helper()
	m := broadcastKeyRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("broadcast key not found in workbench HTML")
	}
	return m[1]
}

func adminActivityIDFromLocation(t *testing.T, location string) int64 {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location %q: %v", location, err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "admin" || parts[1] != "activities" {
		t.Fatalf("unexpected redirect location %q", location)
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("invalid activity id in %q", location)
	}
	return id
}
