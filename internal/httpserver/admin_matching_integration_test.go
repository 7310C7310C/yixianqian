package httpserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func matchingSignup(t *testing.T, env *profileHTTPEnv, activityID, userID int64, attendance string) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, status, attendance)
		 VALUES ($1, $2, '已报名', $3)
		 ON CONFLICT (activity_id, user_id) DO UPDATE
		   SET status = '已报名', attendance = EXCLUDED.attendance`,
		activityID, userID, attendance); err != nil {
		t.Fatalf("insert matching signup: %v", err)
	}
}

func matchingSetModes(t *testing.T, env *profileHTTPEnv, activityID int64, group, onetoone bool) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET group_enabled = $2, onetoone_enabled = $3 WHERE id = $1`,
		activityID, group, onetoone); err != nil {
		t.Fatalf("set matching modes: %v", err)
	}
}

func matchingManager(t *testing.T, env *profileHTTPEnv, name string) (db.User, *wsClient) {
	t.Helper()
	manager, client := env.createUser(t, "U-MATCHMGR", name, "active", "user")
	cleanupHTTPUsers(t, env, manager.ID)
	assignGroupingAdmin(t, env, manager.ID)
	return manager, client
}

func matchingAuditCount(t *testing.T, env *profileHTTPEnv, actorID int64, action, targetID string) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log
		  WHERE actor_user_id = $1 AND action = $2 AND target_id = $3`,
		actorID, action, targetID).Scan(&count); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return count
}

func matchingPair(t *testing.T, env *profileHTTPEnv, prefix string) []db.User {
	t.Helper()
	users := make([]db.User, 0, 4)
	for i := 0; i < 2; i++ {
		male, _ := env.createUser(t, fmt.Sprintf("U-%s-M%d", prefix, i), fmt.Sprintf("男%d", i), "active", "user")
		female, _ := env.createUser(t, fmt.Sprintf("U-%s-F%d", prefix, i), fmt.Sprintf("女%d", i), "active", "user")
		setHTTPGender(t, env, male.ID, "男性")
		setHTTPGender(t, env, female.ID, "女性")
		cleanupHTTPUsers(t, env, male.ID, female.ID)
		users = append(users, male, female)
	}
	return users
}

func TestIntegrationAdminMatchingPagesAccessAndEntryLinks(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	_, managerClient := matchingManager(t, env, "匹配管理员")
	reviewer, reviewerClient := env.createUser(t, "U-MATCHREV", "匹配审核员", "active", "user")
	plain, plainClient := env.createUser(t, "U-MATCHPLAIN", "匹配普通用户", "active", "user")
	cleanupHTTPUsers(t, env, reviewer.ID, plain.ID)
	if changed, err := env.database.AssignAdminRole(env.ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}

	groupActivityID, groupActivityNo := createHTTPActivity(t, env, 0, false, "")
	matchingSetModes(t, env, groupActivityID, true, false)
	onetooneActivityID, onetooneActivityNo := createHTTPActivity(t, env, 0, false, "")
	matchingSetModes(t, env, onetooneActivityID, false, true)

	anon := newWSClient(t, env.server.URL)
	groupPath := "/admin/matching/group/" + strconv.FormatInt(groupActivityID, 10)
	onetoonePath := "/admin/matching/onetoone/" + strconv.FormatInt(onetooneActivityID, 10)
	for _, path := range []string{"/admin/matching", groupPath, onetoonePath} {
		resp := anon.get(path)
		if resp.StatusCode != http.StatusSeeOther ||
			!strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
			t.Fatalf("anonymous %s status=%d location=%q", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	for _, client := range []*wsClient{reviewerClient, plainClient} {
		for _, path := range []string{"/admin/matching", groupPath, onetoonePath} {
			resp := client.get(path)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("unauthorised %s status = %d", path, resp.StatusCode)
			}
		}
	}

	resp := managerClient.get("/admin")
	adminHome := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(adminHome, `href="/admin/matching"`) {
		t.Fatalf("admin dashboard should not expose matching entry: status=%d", resp.StatusCode)
	}
	resp = managerClient.get("/admin/matching")
	list := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("matching list status = %d body=%s", resp.StatusCode, list)
	}
	for _, want := range []string{
		"匹配控制台", "活动匹配清单", "匹配",
		`href="/admin/matching/group/` + strconv.FormatInt(groupActivityID, 10) + `"`,
		`href="/admin/matching/onetoone/` + strconv.FormatInt(onetooneActivityID, 10) + `"`,
		"活动工作台", "报名", "签到",
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("matching list missing %q", want)
		}
	}

	resp = managerClient.get(groupPath + "?round=1&male_per_group=1&female_per_group=1")
	groupPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("group console status = %d body=%s", resp.StatusCode, groupPage)
	}
	for _, want := range []string{
		`data-matching-console="group"`,
		`data-activity-id="` + strconv.FormatInt(groupActivityID, 10) + `"`,
		`data-activity-no="` + groupActivityNo + `"`,
		`data-base-url="` + groupPath + `"`,
		`data-csrf="`,
		`/static/admin_matching.js`,
		"执行预览", "报名与志愿", "聊天概览", "未提交志愿", "已提交志愿",
		`href="/admin/activities/` + strconv.FormatInt(groupActivityID, 10) + `#roster"`,
	} {
		if !strings.Contains(groupPage, want) {
			t.Fatalf("group console missing %q", want)
		}
	}
	if strings.Contains(groupPage, "匹配控制台尚未在本期交付") {
		t.Fatal("group console still contains the retired placeholder")
	}

	resp = managerClient.get(onetoonePath + "?round=1&female_chats=1")
	onetoonePage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("onetoone console status = %d body=%s", resp.StatusCode, onetoonePage)
	}
	for _, want := range []string{
		`data-matching-console="onetoone"`,
		`data-activity-no="` + onetooneActivityNo + `"`,
		"执行预览", "一对一结果", "聊天概览", "女生场次 K",
		`name="female_chats" min="1" value="1"`,
	} {
		if !strings.Contains(onetoonePage, want) {
			t.Fatalf("onetoone console missing %q", want)
		}
	}

	// The activity list and workbench expose real console entries, and the
	// workbench roster links back to the matching overview.
	resp = managerClient.get("/admin/activities")
	activityList := readBody(t, resp)
	for _, want := range []string{
		`href="/admin/matching/group/` + strconv.FormatInt(groupActivityID, 10) + `"`,
		`href="/admin/matching/onetoone/` + strconv.FormatInt(onetooneActivityID, 10) + `"`,
	} {
		if !strings.Contains(activityList, want) {
			t.Fatalf("activity list missing %q", want)
		}
	}
	resp = managerClient.get("/admin/activities/" + strconv.FormatInt(groupActivityID, 10))
	workbench := readBody(t, resp)
	for _, want := range []string{
		`href="/admin/matching/group/` + strconv.FormatInt(groupActivityID, 10) + `"`,
		`href="/admin/matching"`,
		"分组 / 一对一",
	} {
		if !strings.Contains(workbench, want) {
			t.Fatalf("workbench missing %q", want)
		}
	}
	if strings.Contains(workbench, "匹配控制台尚未在本期交付") {
		t.Fatal("workbench still contains the retired placeholder")
	}
}

func TestIntegrationAdminMatchingGroupConsoleFlow(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	manager, managerClient := matchingManager(t, env, "分组控制台管理员")
	users := matchingPair(t, env, "GRPCONS")
	unsubmitted, _ := env.createUser(t, "U-GRPCONS-NOSEL", "未填志愿女", "active", "user")
	setHTTPGender(t, env, unsubmitted.ID, "女性")
	cleanupHTTPUsers(t, env, unsubmitted.ID)
	activityID, activityNo := createHTTPActivity(t, env, 0, false, "")
	matchingSetModes(t, env, activityID, true, false)
	for _, user := range users {
		matchingSignup(t, env, activityID, user.ID, "按时")
	}
	matchingSignup(t, env, activityID, unsubmitted.ID, "按时")
	maleIDs := []int64{users[0].ID, users[2].ID}
	femaleIDs := []int64{users[1].ID, users[3].ID}
	for _, id := range maleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "男性", femaleIDs)
	}
	for _, id := range femaleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "女性", maleIDs)
	}

	pagePath := "/admin/matching/group/" + strconv.FormatInt(activityID, 10)
	resp := managerClient.get(pagePath + "?round=1&male_per_group=1&female_per_group=1")
	page := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("group page status = %d body=%s", resp.StatusCode, page)
	}
	for _, want := range []string{
		"预计组数", "已填志愿", "未提交志愿（1）", "已提交志愿（4）",
		users[0].Name, unsubmitted.Name,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("group page missing %q", want)
		}
	}

	var roster matchingRosterView
	resp = managerClient.get("/api/admin/group/roster?activity_id=" + url.QueryEscape(activityNo))
	profileDecode(t, resp, &roster)
	if resp.StatusCode != http.StatusOK || roster.Total != 5 || roster.Submitted != 4 ||
		roster.Unsubmitted != 1 || roster.CheckedIn != 5 || roster.UnsubmittedCheckedIn != 1 ||
		roster.SigninEnabled || len(roster.SubmittedList) != 4 ||
		len(roster.UnsubmittedList) != 1 ||
		roster.UnsubmittedList[0].ID != unsubmitted.ID {
		t.Fatalf("roster = %+v status=%d", roster, resp.StatusCode)
	}
	runPayload := map[string]any{
		"activity_id": activityNo, "round": 1, "male_per_group": 1, "female_per_group": 1,
	}
	resp = managerClient.postJSON("/api/admin/group/run", "", runPayload)
	if resp.StatusCode != http.StatusForbidden || profileAPIErrorCode(t, resp) != "csrf_failed" {
		t.Fatalf("run without csrf status = %d", resp.StatusCode)
	}
	csrf := managerClient.csrfCookie()
	if csrf == "" {
		t.Fatal("manager csrf cookie missing")
	}
	resp = managerClient.postJSON("/api/admin/group/run", csrf, runPayload)
	var run struct {
		OK     bool         `json:"ok"`
		Result GroupRunView `json:"result"`
	}
	profileDecode(t, resp, &run)
	if resp.StatusCode != http.StatusOK || !run.OK || run.Result.Round != 1 || run.Result.GroupCount != 2 {
		t.Fatalf("group run = %+v status=%d", run, resp.StatusCode)
	}
	if matchingAuditCount(t, env, manager.ID, "group.run", strconv.FormatInt(activityID, 10)) == 0 {
		t.Fatal("group.run was not audited")
	}
	resp = managerClient.postJSON("/api/admin/group/run", csrf, runPayload)
	if resp.StatusCode != http.StatusConflict || profileAPIErrorCode(t, resp) != "group_already_done" {
		t.Fatalf("duplicate group run status = %d", resp.StatusCode)
	}

	var result AdminGroupResultView
	resp = managerClient.get("/api/admin/group/result?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	profileDecode(t, resp, &result)
	if resp.StatusCode != http.StatusOK || result.Round != 1 || len(result.Groups) != 2 {
		t.Fatalf("group result = %+v status=%d", result, resp.StatusCode)
	}

	// One message between two members of the same group is enough to mark one
	// pair chatted and to show up in both the API and the server-rendered card.
	if len(result.Groups) == 0 || len(result.Groups[0].Members) < 2 {
		t.Fatalf("unexpected group shape: %+v", result.Groups)
	}
	from := result.Groups[0].Members[0].ID
	to := result.Groups[0].Members[1].ID
	if _, err := env.database.CreateMessage(env.ctx, db.CreateMessageParams{
		TargetUserID: to, AuthorID: from, Content: "分组聊天测试",
	}); err != nil {
		t.Fatalf("create matching message: %v", err)
	}
	var chat groupChatOverviewView
	resp = managerClient.get("/api/admin/group/chat-overview?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	profileDecode(t, resp, &chat)
	if resp.StatusCode != http.StatusOK || chat.Round != 1 ||
		chat.Summary.Groups != 2 || chat.Summary.Pairs != 2 ||
		chat.Summary.ChattedPairs != 1 || chat.Summary.Messages != 1 {
		t.Fatalf("group chat overview = %+v status=%d", chat, resp.StatusCode)
	}
	resp = managerClient.get(pagePath + "?round=1&chat_round=1")
	chattedPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(chattedPage, "已聊比例") ||
		!strings.Contains(chattedPage, "<strong>50%</strong>") ||
		!strings.Contains(chattedPage, "1 条") {
		t.Fatalf("group chat page missing summary: status=%d", resp.StatusCode)
	}
	resp = managerClient.get(pagePath + "?round=1&notice=run#result")
	if noticePage := readBody(t, resp); resp.StatusCode != http.StatusOK ||
		!strings.Contains(noticePage, "分组已完成") {
		t.Fatalf("group run notice missing: status=%d", resp.StatusCode)
	}

	resp = managerClient.postJSON("/api/admin/group/reset", "", map[string]any{"activity_id": activityNo})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reset without csrf status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/group/reset", csrf, map[string]any{"activity_id": activityNo})
	var reset struct {
		OK      bool  `json:"ok"`
		Cleared int64 `json:"cleared_results"`
	}
	profileDecode(t, resp, &reset)
	if resp.StatusCode != http.StatusOK || !reset.OK || reset.Cleared != 4 {
		t.Fatalf("group reset = %+v status=%d", reset, resp.StatusCode)
	}
	if matchingAuditCount(t, env, manager.ID, "group.reset", activityNo) == 0 {
		t.Fatal("group.reset was not audited")
	}
	var cleared AdminGroupResultView
	resp = managerClient.get("/api/admin/group/result?activity_id=" + url.QueryEscape(activityNo))
	profileDecode(t, resp, &cleared)
	if cleared.Round != 0 || len(cleared.Groups) != 0 {
		t.Fatalf("group result after reset = %+v", cleared)
	}

	// Mutual exclusion is visible in both previews and refused at run time.
	matchingSetModes(t, env, activityID, true, true)
	resp = managerClient.get("/api/admin/group/preview?activity_id=" + url.QueryEscape(activityNo) +
		"&round=2&male_per_group=1&female_per_group=1")
	var preview GroupPreviewView
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || preview.CanExecute ||
		!strings.Contains(strings.Join(preview.Blockers, "；"), "一对一功能已开启") {
		t.Fatalf("group preview conflict = %+v status=%d", preview, resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/group/run", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "male_per_group": 1, "female_per_group": 1,
	})
	if resp.StatusCode != http.StatusConflict || profileAPIErrorCode(t, resp) != "group_mode_conflict" {
		t.Fatalf("group mode conflict status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "female_chats": 1,
	})
	var otoPreview onetoonePreviewView
	profileDecode(t, resp, &otoPreview)
	if resp.StatusCode != http.StatusOK || otoPreview.CanExecute ||
		!strings.Contains(strings.Join(otoPreview.Blockers, "；"), "分组模式") {
		t.Fatalf("onetoone preview conflict = %+v status=%d", otoPreview, resp.StatusCode)
	}
}

func TestIntegrationAdminMatchingSigninGate(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	_, managerClient := matchingManager(t, env, "签到门禁管理员")
	users := matchingPair(t, env, "SIGNGATE")
	activityID, activityNo := createHTTPActivity(t, env, 0, true, "0123")
	for _, user := range users {
		matchingSignup(t, env, activityID, user.ID, "")
	}
	maleIDs := []int64{users[0].ID, users[2].ID}
	femaleIDs := []int64{users[1].ID, users[3].ID}
	for _, id := range maleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "男性", femaleIDs)
	}
	for _, id := range femaleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "女性", maleIDs)
	}

	resp := managerClient.get("/api/admin/group/preview?activity_id=" + url.QueryEscape(activityNo) +
		"&round=1&male_per_group=1&female_per_group=1")
	var preview GroupPreviewView
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || preview.CanExecute ||
		preview.CheckedInMaleCount != 0 || preview.CheckedInFemaleCount != 0 ||
		!strings.Contains(strings.Join(preview.Blockers, "；"), "人数不足") {
		t.Fatalf("group signin preview = %+v status=%d", preview, resp.StatusCode)
	}
	csrf := managerClient.csrfCookie()
	resp = managerClient.postJSON("/api/admin/group/run", csrf, map[string]any{
		"activity_id": activityNo, "round": 1, "male_per_group": 1, "female_per_group": 1,
	})
	if resp.StatusCode != http.StatusConflict || profileAPIErrorCode(t, resp) != "not_enough_people" {
		t.Fatalf("group signin run status = %d", resp.StatusCode)
	}

	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 1, "female_chats": 1,
	})
	var otoPreview onetoonePreviewView
	profileDecode(t, resp, &otoPreview)
	if resp.StatusCode != http.StatusOK || otoPreview.CanExecute ||
		!otoPreview.SigninEnabled || otoPreview.RegisteredMaleCount != 2 ||
		otoPreview.RegisteredFemaleCount != 2 || otoPreview.MaleCount != 0 ||
		otoPreview.FemaleCount != 0 ||
		!strings.Contains(strings.Join(otoPreview.Blockers, "；"),
			"开启签到后只有已签到者参与匹配，目前没有已签到的有效参与者") {
		t.Fatalf("onetoone signin preview = %+v status=%d", otoPreview, resp.StatusCode)
	}

	if _, err := env.pool.Exec(env.ctx,
		`UPDATE signups SET attendance = '按时' WHERE activity_id = $1`, activityID); err != nil {
		t.Fatalf("check in matching users: %v", err)
	}
	resp = managerClient.get("/api/admin/group/preview?activity_id=" + url.QueryEscape(activityNo) +
		"&round=1&male_per_group=1&female_per_group=1")
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || !preview.CanExecute ||
		preview.CheckedInMaleCount != 2 || preview.CheckedInFemaleCount != 2 {
		t.Fatalf("group preview after signin = %+v status=%d", preview, resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 1, "female_chats": 1,
	})
	profileDecode(t, resp, &otoPreview)
	if resp.StatusCode != http.StatusOK || !otoPreview.CanExecute ||
		otoPreview.MaleCount != 2 || otoPreview.FemaleCount != 2 {
		t.Fatalf("onetoone preview after signin = %+v status=%d", otoPreview, resp.StatusCode)
	}
}

func TestIntegrationAdminMatchingOnetooneConsoleFlow(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	manager, managerClient := matchingManager(t, env, "一对一控制台管理员")
	users := matchingPair(t, env, "OTOCONS")
	activityID, activityNo := createHTTPActivity(t, env, 0, false, "")
	matchingSetModes(t, env, activityID, false, true)
	for _, user := range users {
		matchingSignup(t, env, activityID, user.ID, "按时")
	}
	maleIDs := []int64{users[0].ID, users[2].ID}
	femaleIDs := []int64{users[1].ID, users[3].ID}
	for _, id := range maleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "男性", femaleIDs)
	}
	for _, id := range femaleIDs {
		createHTTPGroupSelection(t, env, activityID, id, "女性", maleIDs)
	}
	csrf := managerClient.csrfCookie()
	pagePath := "/admin/matching/onetoone/" + strconv.FormatInt(activityID, 10)

	// No history yet: the page falls back to K=3 and explains the blocker.
	resp := managerClient.get(pagePath + "?round=1")
	defaultPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(defaultPage, `name="female_chats" min="1" value="3"`) ||
		!strings.Contains(defaultPage, "超过男生人数 2") {
		t.Fatalf("onetoone default K page status=%d", resp.StatusCode)
	}

	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 1,
	})
	var missingK onetoonePreviewView
	profileDecode(t, resp, &missingK)
	if resp.StatusCode != http.StatusOK || missingK.CanExecute || missingK.FemaleChats != nil ||
		!strings.Contains(strings.Join(missingK.Blockers, "；"), "请指定女生场次") {
		t.Fatalf("preview without K = %+v status=%d", missingK, resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 0, "female_chats": 1,
	})
	if resp.StatusCode != http.StatusBadRequest || profileAPIErrorCode(t, resp) != "invalid_round" {
		t.Fatalf("preview invalid round status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/preview", "", map[string]any{
		"activity_id": activityNo, "round": 1, "female_chats": 1,
	})
	if resp.StatusCode != http.StatusForbidden || profileAPIErrorCode(t, resp) != "csrf_invalid" {
		t.Fatalf("onetoone preview without csrf status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 1, "female_chats": 1,
	})
	var preview onetoonePreviewView
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || !preview.CanExecute ||
		preview.MaleCount != 2 || preview.FemaleCount != 2 ||
		preview.TotalPairsExpected != 4 {
		t.Fatalf("onetoone preview = %+v status=%d", preview, resp.StatusCode)
	}

	resp = managerClient.get(pagePath + "?round=1&female_chats=1")
	page := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("onetoone page status = %d body=%s", resp.StatusCode, page)
	}
	for _, want := range []string{
		`data-matching-console="onetoone"`, "预计场次", "女生场次 K",
		`name="female_chats" min="1" value="1"`, "聊天概览",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("onetoone page missing %q", want)
		}
	}

	runPayload := map[string]any{
		"activity_id": activityNo, "round": 1, "female_chats": 1,
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", "", runPayload)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("onetoone run without csrf status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", csrf, runPayload)
	var run struct {
		OK    bool `json:"ok"`
		Round int  `json:"round"`
		Pairs int  `json:"total_pairs"`
	}
	profileDecode(t, resp, &run)
	if resp.StatusCode != http.StatusOK || !run.OK || run.Round != 1 || run.Pairs != 4 {
		t.Fatalf("onetoone run = %+v status=%d", run, resp.StatusCode)
	}
	if matchingAuditCount(t, env, manager.ID, "onetoone.run", strconv.FormatInt(activityID, 10)) == 0 {
		t.Fatal("onetoone.run was not audited")
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", csrf, runPayload)
	if resp.StatusCode != http.StatusConflict || profileAPIErrorCode(t, resp) != "already_done" {
		t.Fatalf("duplicate onetoone run status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "female_chats": 0,
	})
	if resp.StatusCode != http.StatusBadRequest ||
		profileAPIErrorCode(t, resp) != "invalid_female_chats" {
		t.Fatalf("invalid K status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "female_chats": 3,
	})
	if resp.StatusCode != http.StatusBadRequest ||
		profileAPIErrorCode(t, resp) != "invalid_female_chats" {
		t.Fatalf("K over male count status = %d", resp.StatusCode)
	}
	resp = managerClient.get("/api/admin/onetoone/result?activity_id=" +
		url.QueryEscape(activityNo) + "&round=abc")
	if resp.StatusCode != http.StatusBadRequest || profileAPIErrorCode(t, resp) != "invalid_round" {
		t.Fatalf("invalid result round status = %d", resp.StatusCode)
	}
	var result onetooneResultView
	resp = managerClient.get("/api/admin/onetoone/result?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	profileDecode(t, resp, &result)
	if resp.StatusCode != http.StatusOK || result.Round != 1 || !result.RoundHasResults ||
		result.ResultCount != 4 || result.MatchedCount != 4 {
		t.Fatalf("onetoone result = %+v status=%d", result, resp.StatusCode)
	}
	resp = managerClient.get(pagePath + "?round=1&chat_round=1")
	resultPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resultPage, "<details") ||
		!strings.Contains(resultPage, "必聊") || !strings.Contains(resultPage, users[0].Name) {
		t.Fatalf("onetoone result page status=%d", resp.StatusCode)
	}

	// The page default now follows the latest round's female rank (K=1).
	resp = managerClient.get(pagePath)
	historyPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(historyPage, `name="female_chats" min="1" value="1"`) {
		t.Fatalf("onetoone history default K status=%d", resp.StatusCode)
	}

	if _, err := env.database.CreateMessage(env.ctx, db.CreateMessageParams{
		TargetUserID: femaleIDs[0], AuthorID: maleIDs[0], Content: "一对一聊天测试",
	}); err != nil {
		t.Fatalf("create onetoone message: %v", err)
	}
	var chat onetooneChatOverviewView
	resp = managerClient.get("/api/admin/onetoone/chat-overview?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	profileDecode(t, resp, &chat)
	if resp.StatusCode != http.StatusOK || chat.Round != 1 ||
		chat.Summary.People != 4 || chat.Summary.Pairs != 4 ||
		chat.Summary.ChattedPairs != 1 || chat.Summary.Messages != 1 {
		t.Fatalf("onetoone chat overview = %+v status=%d", chat, resp.StatusCode)
	}
	resp = managerClient.get(pagePath + "?chat_round=1")
	chatPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(chatPage, "已聊比例") ||
		!strings.Contains(chatPage, "1 条") {
		t.Fatalf("onetoone chat page status=%d", resp.StatusCode)
	}

	resp = managerClient.postJSON("/api/admin/onetoone/reset", "", map[string]any{"activity_id": activityNo})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("onetoone reset without csrf status = %d", resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/reset", csrf, map[string]any{"activity_id": activityNo})
	var reset struct {
		OK      bool  `json:"ok"`
		Cleared int64 `json:"cleared_results"`
	}
	profileDecode(t, resp, &reset)
	if resp.StatusCode != http.StatusOK || !reset.OK || reset.Cleared != 4 {
		t.Fatalf("onetoone reset = %+v status=%d", reset, resp.StatusCode)
	}
	if matchingAuditCount(t, env, manager.ID, "onetoone.reset", strconv.FormatInt(activityID, 10)) == 0 {
		t.Fatal("onetoone.reset was not audited")
	}
	resp = managerClient.get("/api/admin/onetoone/result?activity_id=" + url.QueryEscape(activityNo))
	var cleared onetooneResultView
	profileDecode(t, resp, &cleared)
	if cleared.Round != 0 || len(cleared.People) != 0 {
		t.Fatalf("onetoone result after reset = %+v", cleared)
	}

	matchingSetModes(t, env, activityID, true, true)
	resp = managerClient.postJSON("/api/admin/onetoone/preview", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "female_chats": 1,
	})
	var conflict onetoonePreviewView
	profileDecode(t, resp, &conflict)
	if resp.StatusCode != http.StatusOK || conflict.CanExecute ||
		!strings.Contains(strings.Join(conflict.Blockers, "；"), "分组模式") {
		t.Fatalf("onetoone mode conflict preview = %+v status=%d", conflict, resp.StatusCode)
	}
	resp = managerClient.postJSON("/api/admin/onetoone/run", csrf, map[string]any{
		"activity_id": activityNo, "round": 2, "female_chats": 1,
	})
	if resp.StatusCode != http.StatusConflict ||
		profileAPIErrorCode(t, resp) != "exclusive_mode" {
		t.Fatalf("onetoone mode conflict run status = %d", resp.StatusCode)
	}
}
