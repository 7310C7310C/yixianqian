package httpserver

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

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
	if _, err := pool.Exec(ctx, `UPDATE activities SET group_enabled = true WHERE id = $1`,
		activity.ID); err != nil {
		t.Fatalf("enable grouping: %v", err)
	}
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
	for _, want := range []string{"工作台", "报名/签到名单", signupUser.Name, wantMask,
		"站内广播", "快速设置", "分组 / 一对一", "匹配总览",
		`href="/admin/matching/group/` + strconv.FormatInt(activity.ID, 10) + `"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("workbench missing %q", want)
		}
	}
	if strings.Contains(body, "匹配控制台尚未在本期交付") {
		t.Fatal("workbench still contains the retired matching placeholder")
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
	// Simulate a full activity: roster/quick-setting writes must never reset it.
	if _, err := pool.Exec(ctx, `UPDATE activities SET status = $2 WHERE id = $1`,
		activity.ID, db.ActivityStatusFull); err != nil {
		t.Fatalf("set full status: %v", err)
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
	attendanceLocation := resp.Header.Get("Location")
	if !strings.HasSuffix(attendanceLocation, "#member-"+strconv.FormatInt(signupUser.ID, 10)) {
		t.Fatalf("attendance redirect = %q, want member anchor", attendanceLocation)
	}
	resp = client.get(attendanceLocation)
	attendanceBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(attendanceBody, "签到状态已更新") ||
		!strings.Contains(attendanceBody, `id="member-`+strconv.FormatInt(signupUser.ID, 10)+`"`) {
		t.Fatalf("attendance result missing near roster: status=%d", resp.StatusCode)
	}
	if state, err := database.GetAttendance(ctx, activity.ID, signupUser.ID); err != nil || state != db.AttendanceOnTime {
		t.Fatalf("attendance = %q err=%v", state, err)
	}
	if reloaded, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10)); err != nil || reloaded.Status != db.ActivityStatusFull {
		t.Fatalf("attendance changed status: %+v err=%v", reloaded, err)
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

	// Roster search and attendance filtering are server-side and keep the row
	// anchor available for the post-check-in redirect flow.
	filterQuery := url.Values{}
	filterQuery.Set("q", signupUser.Name)
	resp = client.get(workbenchPath + "?" + filterQuery.Encode())
	filtered := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(filtered, signupUser.Name) ||
		!strings.Contains(filtered, `id="member-`+strconv.FormatInt(signupUser.ID, 10)+`"`) {
		t.Fatalf("roster search failed: status=%d", resp.StatusCode)
	}
	filterQuery.Set("q", "不存在的会员")
	resp = client.get(workbenchPath + "?" + filterQuery.Encode())
	missing := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(missing, signupUser.Name) ||
		!strings.Contains(missing, "没有符合条件的报名者") {
		t.Fatalf("roster search did not filter: status=%d body=%s", resp.StatusCode, missing)
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
	wantPreview := "去重后 <strong>" + strconv.Itoa(1+observerCount) + "</strong> 人"
	// Preview state is never put in the URL: a GET with preview parameters only
	// renders the normal form.
	resp = client.get(workbenchPath + "?preview=1&include_signups=1&include_observers=1")
	fresh := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(fresh, "broadcast_preview_token") ||
		strings.Contains(fresh, "确认发送？") {
		t.Fatalf("GET must not expose a preview: status=%d", resp.StatusCode)
	}
	key := broadcastKeyFromHTML(t, fresh)
	// The preview button posts the form and receives a signed audience token.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":       {csrf},
		"broadcast_key":    {key},
		"include_signups":  {"1"},
		"broadcast_action": {"preview"},
	})
	previewBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(previewBody, "去重后 <strong>1</strong> 人") ||
		!strings.Contains(previewBody, "确认发送给 1 人") {
		t.Fatalf("post preview status=%d body=%s", resp.StatusCode, previewBody)
	}
	// The preview response is rendered on POST, so the form action must carry
	// the #broadcast fragment or the browser lands at the top of the page with
	// the confirmation card far below the viewport.
	broadcastFormAction := `action="` + workbenchPath + `/broadcast#broadcast"`
	if !strings.Contains(previewBody, broadcastFormAction) {
		t.Fatalf("preview form action lost the #broadcast anchor: %s", previewBody)
	}
	signupPreviewToken := broadcastPreviewTokenFromHTML(t, previewBody)
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("preview sent notifications: %d", got)
	}

	// Observer-only preview counts only the global observer audience.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":        {csrf},
		"broadcast_key":     {key},
		"include_observers": {"1"},
		"broadcast_action":  {"preview"},
	})
	observerPreview := readBody(t, resp)
	wantObserver := "去重后 <strong>" + strconv.Itoa(observerCount) + "</strong> 人"
	if resp.StatusCode != http.StatusOK || !strings.Contains(observerPreview, wantObserver) ||
		strings.Contains(observerPreview, "去重后 <strong>"+strconv.Itoa(1+observerCount)+"</strong>") {
		t.Fatalf("observer preview status=%d want=%q body=%s", resp.StatusCode, wantObserver, observerPreview)
	}

	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"broadcast_key": {key}, "include_signups": {"1"}, "broadcast_title": {"无 CSRF"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("broadcast without csrf status = %d", resp.StatusCode)
	}
	// Sending without a preview token is rejected before any write.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":        {csrf},
		"broadcast_key":     {key},
		"include_signups":   {"1"},
		"include_observers": {"1"},
		"broadcast_title":   {"未预览"},
		"broadcast_body":    {"不应发送。"},
		"broadcast_action":  {"send"},
	})
	noPreview := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(noPreview, "必须先预览收件人") {
		t.Fatalf("send without preview status=%d body=%s", resp.StatusCode, noPreview)
	}
	// Error re-renders must keep the anchor too, otherwise the alert is hidden
	// offscreen just like the old preview bug.
	if !strings.Contains(noPreview, broadcastFormAction) || !strings.Contains(noPreview, `role="alert"`) {
		t.Fatalf("error render lost the #broadcast anchor or alert: %s", noPreview)
	}
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("send without preview wrote notifications: %d", got)
	}
	// Changing the audience after preview must be rejected, not silently sent
	// to the new set.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":              {csrf},
		"broadcast_key":           {key},
		"broadcast_preview_token": {signupPreviewToken},
		"include_signups":         {"1"},
		"include_observers":       {"1"},
		"broadcast_title":         {"改受众"},
		"broadcast_body":          {"不应发送。"},
		"broadcast_action":        {"send"},
	})
	changedAudience := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(changedAudience, "收件人群组在预览后已变更") {
		t.Fatalf("changed audience status=%d body=%s", resp.StatusCode, changedAudience)
	}
	if got := notificationCount(t, ctx, pool, signupUser.ID, "activity_broadcast"); got != 0 {
		t.Fatalf("changed audience wrote notifications: %d", got)
	}

	// Re-preview the combined audience, then send with that token.
	resp = client.postForm(workbenchPath+"/broadcast", url.Values{
		"csrf_token":        {csrf},
		"broadcast_key":     {key},
		"include_signups":   {"1"},
		"include_observers": {"1"},
		"broadcast_action":  {"preview"},
	})
	combinedPreview := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(combinedPreview, wantPreview) {
		t.Fatalf("combined preview status=%d want=%q", resp.StatusCode, wantPreview)
	}
	previewToken := broadcastPreviewTokenFromHTML(t, combinedPreview)
	broadcastValues := url.Values{
		"csrf_token":              {csrf},
		"broadcast_key":           {key},
		"broadcast_preview_token": {previewToken},
		"include_signups":         {"1"},
		"include_observers":       {"1"},
		"broadcast_title":         {"活动广播测试"},
		"broadcast_body":          {"请准时参加。"},
		"broadcast_action":        {"send"},
	}
	resp = client.postForm(workbenchPath+"/broadcast", broadcastValues)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("broadcast status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if loc := resp.Header.Get("Location"); !strings.HasSuffix(loc, "#broadcast") || !strings.Contains(loc, "broadcast=sent") {
		t.Fatalf("broadcast redirect = %q", loc)
	}
	resp = client.get(resp.Header.Get("Location"))
	sentBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(sentBody, "广播已发送") {
		t.Fatalf("broadcast result not shown near card: status=%d", resp.StatusCode)
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
	// 分组与一对一互斥：快速设置只开启分组，一对一必须保持关闭。
	if updated.Status != "已结束" || !updated.SigninEnabled || updated.SigninCode != "4321" ||
		!updated.GroupEnabled || updated.MalePerGroup != 3 || updated.FemalePerGroup != 2 ||
		updated.OnetooneEnabled {
		t.Fatalf("quick settings not applied: %+v", updated)
	}

	// Full activities keep their status when the quick form omits it: a
	// grouping/check-in change must never silently reopen registration.
	if _, err := pool.Exec(ctx, `UPDATE activities SET status = $2 WHERE id = $1`,
		activity.ID, db.ActivityStatusFull); err != nil {
		t.Fatal(err)
	}
	resp = client.postForm("/admin/activities/"+strconv.FormatInt(activity.ID, 10), url.Values{
		"csrf_token":       {csrf},
		"name":             {activity.Name},
		"max_signup":       {strconv.Itoa(activity.MaxSignup)},
		"male_per_group":   {"0"},
		"female_per_group": {"0"},
		"group_enabled":    {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("quick update without status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	preserved, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Status != db.ActivityStatusFull || !preserved.GroupEnabled {
		t.Fatalf("status was not preserved: %+v", preserved)
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

func TestIntegrationAdminActivityAttendanceFilterContext(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "筛选管理员")
	users := make([]db.User, 0, 8)
	ids := []int64{manager.ID}
	for i := 0; i < 8; i++ {
		user := createMessageUser(t, ctx, database, pool, "筛选用户"+strconv.Itoa(i+1))
		users = append(users, user)
		ids = append(ids, user.ID)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	activity := createWorkbenchActivity(t, ctx, database, pool, "签到筛选上下文测试")
	for _, user := range users {
		if _, err := database.SignupActivity(ctx, user.ID, activity.ActivityNo); err != nil {
			t.Fatalf("signup %d: %v", user.ID, err)
		}
	}
	if _, err := database.AdminSetActivityAttendance(ctx, activity.ID, users[0].ID, db.AttendanceOnTime); err != nil {
		t.Fatalf("seed checked attendance: %v", err)
	}

	client := loginMessageClient(t, database, server, manager)
	workbenchPath := "/admin/activities/" + strconv.FormatInt(activity.ID, 10)
	csrf := client.pageCSRF(workbenchPath)
	q := "筛选用户"
	filtered := workbenchPath + "?q=" + url.QueryEscape(q) + "&attendance=unchecked"
	resp := client.get(filtered)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Count(body, `id="member-`) != 7 {
		t.Fatalf("unchecked roster rows = %d, status=%d", strings.Count(body, `id="member-`), resp.StatusCode)
	}

	// Signing in while filtering to 未签到 removes the row from the filter. The
	// redirect keeps q, drops attendance, returns to page 1 and does not jump
	// to an anchor that is no longer rendered.
	resp = client.postForm(workbenchPath+"/attendance", url.Values{
		"csrf_token":       {csrf},
		"user_id":          {strconv.FormatInt(users[1].ID, 10)},
		"attendance_state": {string(db.AttendanceOnTime)},
		"q":                {q},
		"attendance":       {"unchecked"},
		"page":             {"1"},
		"return_to":        {filtered},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("attendance status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location := resp.Header.Get("Location")
	if strings.Contains(location, "attendance=unchecked") || strings.Contains(location, "member-") ||
		!strings.Contains(location, "q="+url.QueryEscape(q)) || !strings.Contains(location, "#roster") {
		t.Fatalf("filter-clearing redirect = %q", location)
	}
	resp = client.get(location)
	cleared := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(cleared, "已清除筛选并回到第一页") ||
		!strings.Contains(cleared, `value="`+q+`"`) {
		t.Fatalf("cleared-filter page missing notice or q: status=%d", resp.StatusCode)
	}
	if state, err := database.GetAttendance(ctx, activity.ID, users[1].ID); err != nil || state != db.AttendanceOnTime {
		t.Fatalf("attendance = %q err=%v", state, err)
	}

	// Updating a member that still matches the filter preserves q, attendance
	// and the row anchor, and shows the notice inside the target row.
	checkedFilter := workbenchPath + "?q=" + url.QueryEscape(q) + "&attendance=checked"
	resp = client.postForm(workbenchPath+"/attendance", url.Values{
		"csrf_token":       {csrf},
		"user_id":          {strconv.FormatInt(users[1].ID, 10)},
		"attendance_state": {string(db.AttendanceLate)},
		"q":                {q},
		"attendance":       {"checked"},
		"page":             {"1"},
		"return_to":        {checkedFilter},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("checked attendance status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location = resp.Header.Get("Location")
	for _, want := range []string{
		"attendance=checked", "q=" + url.QueryEscape(q), "updated=" + strconv.FormatInt(users[1].ID, 10),
		"#member-" + strconv.FormatInt(users[1].ID, 10),
	} {
		if !strings.Contains(location, want) {
			t.Fatalf("preserved redirect = %q, missing %q", location, want)
		}
	}
	resp = client.get(location)
	rowPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(rowPage, "签到状态已更新。") ||
		!strings.Contains(rowPage, `class="alert alert-ok admin-row-notice"`) ||
		!strings.Contains(rowPage, `id="member-`+strconv.FormatInt(users[1].ID, 10)+`"`) {
		t.Fatalf("row notice/link missing: status=%d", resp.StatusCode)
	}
}

func createWorkbenchActivity(t *testing.T, ctx context.Context, database *db.DB, pool *pgxpool.Pool, name string) db.Activity {
	t.Helper()
	// 不排期：报名/签到时间窗规则由 activities_window_integration_test 覆盖，
	// 工作台用例只验证管理动作。
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

var (
	broadcastKeyRE          = regexp.MustCompile(`name="broadcast_key" value="([^"]+)"`)
	broadcastPreviewTokenRE = regexp.MustCompile(`name="broadcast_preview_token" value="([^"]+)"`)
)

func broadcastKeyFromHTML(t *testing.T, body string) string {
	t.Helper()
	m := broadcastKeyRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("broadcast key not found in workbench HTML")
	}
	return m[1]
}

func broadcastPreviewTokenFromHTML(t *testing.T, body string) string {
	t.Helper()
	m := broadcastPreviewTokenRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("broadcast preview token not found in workbench HTML")
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
