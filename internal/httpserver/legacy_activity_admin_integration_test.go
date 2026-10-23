package httpserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The legacy activity console contract is exercised end-to-end: the SPA sends
// /legacy/api/admin/* with the v1 error envelope, "是/否" flags and open_id
// keys. These tests pin that contract, the RBAC split and the CSRF rule.

func legacyITAdmin(t *testing.T, env *profileHTTPEnv) (db.User, *wsClient) {
	t.Helper()
	admin, client := env.createUser(t, "U-LEGACYADM", "活动管理员", "active", "user")
	cleanupHTTPUsers(t, env, admin.ID)
	assignGroupingAdmin(t, env, admin.ID)
	return admin, client
}

func legacyITRole(t *testing.T, env *profileHTTPEnv, code string, perms ...string) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO admin_roles (code, name) VALUES ($1, $1)
		 ON CONFLICT (code) DO NOTHING`, code); err != nil {
		t.Fatalf("create role %s: %v", code, err)
	}
	for _, perm := range perms {
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO admin_role_permissions (role_id, permission)
			 SELECT id, $2 FROM admin_roles WHERE code = $1
			 ON CONFLICT DO NOTHING`, code, perm); err != nil {
			t.Fatalf("grant %s to %s: %v", perm, code, err)
		}
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(env.ctx, `DELETE FROM admin_roles WHERE code = $1`, code)
	})
}

func legacyITTempAdmin(t *testing.T, env *profileHTTPEnv, role string,
	perms ...string) (*wsClient, db.User) {
	t.Helper()
	user, client := env.createUser(t, "U-LEGACYROLE", "受限管理员", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)
	legacyITRole(t, env, role, perms...)
	changed, err := env.database.AssignAdminRole(env.ctx, user.ID, role, true)
	if err != nil || !changed {
		t.Fatalf("assign %s: changed=%v err=%v", role, changed, err)
	}
	return client, user
}

func legacyITActivity(t *testing.T, env *profileHTTPEnv) (int64, string) {
	t.Helper()
	return createHTTPActivity(t, env, 0, true, "0123")
}

// legacyITMatchingFixture creates 2 male + 2 female signups and volunteer rows.
// The last male stays unsubmitted and is checked in on site.
func legacyITMatchingFixture(t *testing.T, env *profileHTTPEnv) (int64, string, []db.User, []db.User) {
	t.Helper()
	activityID, activityNo := legacyITActivity(t, env)
	matchingSetModes(t, env, activityID, true, false)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET male_per_group = 1, female_per_group = 1 WHERE id = $1`,
		activityID); err != nil {
		t.Fatal(err)
	}

	males := make([]db.User, 0, 2)
	females := make([]db.User, 0, 2)
	for i := 0; i < 2; i++ {
		male, _ := env.createUser(t, fmt.Sprintf("U-LEGACY-M%d", i), fmt.Sprintf("男%d", i), "active", "user")
		female, _ := env.createUser(t, fmt.Sprintf("U-LEGACY-F%d", i), fmt.Sprintf("女%d", i), "active", "user")
		cleanupHTTPUsers(t, env, male.ID, female.ID)
		setHTTPGender(t, env, male.ID, "男性")
		setHTTPGender(t, env, female.ID, "女性")
		males = append(males, male)
		females = append(females, female)
	}
	matchingSignup(t, env, activityID, males[0].ID, "按时")
	matchingSignup(t, env, activityID, males[1].ID, "按时")
	matchingSignup(t, env, activityID, females[0].ID, "按时")
	matchingSignup(t, env, activityID, females[1].ID, "按时")
	createHTTPGroupSelection(t, env, activityID, males[0].ID, "男性", []int64{females[0].ID, females[1].ID})
	createHTTPGroupSelection(t, env, activityID, males[1].ID, "男性", []int64{females[0].ID, females[1].ID})
	createHTTPGroupSelection(t, env, activityID, females[0].ID, "女性", []int64{males[0].ID, males[1].ID})
	createHTTPGroupSelection(t, env, activityID, females[1].ID, "女性", []int64{males[0].ID, males[1].ID})
	return activityID, activityNo, males, females
}

func legacyITError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var payload map[string]string
	profileDecode(t, resp, &payload)
	return payload["error"]
}

func legacyITAuditContains(t *testing.T, env *profileHTTPEnv, actorID int64,
	action, targetID, needle string) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log
		  WHERE actor_user_id = $1 AND action = $2 AND target_id = $3
		    AND metadata::text LIKE $4`,
		actorID, action, targetID, "%"+needle+"%").Scan(&count); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return count
}

type legacyITActivityRow struct {
	ID                int64  `json:"id"`
	ActivityID        string `json:"activity_id"`
	Name              string `json:"name"`
	ActivityStatus    string `json:"activity_status"`
	GroupStatus       string `json:"group_status"`
	GroupFlag         string `json:"group_flag"`
	OnetooneStatus    string `json:"onetoone_status"`
	OnetooneFlag      string `json:"onetoone_flag"`
	OnetooneSubmitted int    `json:"onetoone_submitted_count"`
	Mode              string `json:"mode"`
	Conflict          bool   `json:"conflict"`
	MalesPerGroup     int    `json:"males_per_group"`
	FemalesPerGroup   int    `json:"females_per_group"`
	SignupCount       int    `json:"signup_count"`
	SubmittedCount    int    `json:"submitted_count"`
	HasResults        bool   `json:"has_results"`
	LatestRound       *int   `json:"latest_round"`
	AvailableRounds   []int  `json:"available_rounds"`
}

type legacyITStatus struct {
	ID                        int64  `json:"id"`
	ActivityID                string `json:"activity_id"`
	Name                      string `json:"name"`
	GroupStatus               string `json:"group_status"`
	GroupFlag                 string `json:"group_flag"`
	OnetooneStatus            string `json:"onetoone_status"`
	OnetooneFlag              string `json:"onetoone_flag"`
	SigninFlag                string `json:"signin_flag"`
	SigninCode                string `json:"signin_code"`
	ActivityStatus            string `json:"activity_status"`
	SignupCount               int    `json:"signup_count"`
	SubmittedCount            int    `json:"submitted_count"`
	UnsubmittedCount          int    `json:"unsubmitted_count"`
	UnsubmittedOnSite         int    `json:"unsubmitted_on_site_count"`
	MaleCount                 int    `json:"male_count"`
	FemaleCount               int    `json:"female_count"`
	MalesPerGroup             int    `json:"males_per_group"`
	FemalesPerGroup           int    `json:"females_per_group"`
	Rounds                    []int  `json:"rounds"`
	LatestRound               *int   `json:"latest_round"`
	OnetooneSubmittedCount    int    `json:"onetoone_submitted_count"`
	OnetooneUnsubmittedCount  int    `json:"onetoone_unsubmitted_count"`
	OnetooneUnsubmittedOnSite int    `json:"onetoone_unsubmitted_on_site_count"`
	OnetooneResultCount       int    `json:"onetoone_result_count"`
	OnetooneMatchedCount      int    `json:"onetoone_matched_count"`
	OnetooneRounds            []int  `json:"onetoone_rounds"`
	OnetooneLatestRound       *int   `json:"onetoone_latest_round"`
	Mode                      string `json:"mode"`
	Conflict                  bool   `json:"conflict"`
}

type legacyITRosterItem struct {
	OpenID     string `json:"open_id"`
	UserID     string `json:"user_id"`
	Gender     string `json:"gender"`
	Nickname   string `json:"nickname"`
	RealName   string `json:"real_name"`
	Attendance string `json:"attendance"`
	CheckedIn  bool   `json:"checked_in"`
	State      string `json:"state"`
}

type legacyITUnsubmitted struct {
	ActivityID         string               `json:"activity_id"`
	Total              int                  `json:"total"`
	Unsubmitted        []legacyITRosterItem `json:"unsubmitted"`
	Submitted          []legacyITRosterItem `json:"submitted"`
	AllSubmitted       bool                 `json:"all_submitted"`
	SigninEnabled      bool                 `json:"signin_enabled"`
	UnsubmittedChecked int                  `json:"unsubmitted_checked_in"`
}

type legacyITGroupMember struct {
	OpenID      string `json:"open_id"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
	UserID      string `json:"user_id"`
	Gender      string `json:"gender"`
}

type legacyITGroupResult struct {
	ActivityID string `json:"activity_id"`
	Name       string `json:"name"`
	Round      int    `json:"round"`
	Rounds     []int  `json:"rounds"`
	Groups     []struct {
		GroupID int                   `json:"group_id"`
		Members []legacyITGroupMember `json:"members"`
	} `json:"groups"`
}

type legacyITRunPayload struct {
	OK     bool `json:"ok"`
	Result struct {
		Code       string `json:"code"`
		Round      int    `json:"round"`
		GroupCount int    `json:"group_count"`
		Males      int    `json:"males"`
		Females    int    `json:"females"`
		TotalPairs int    `json:"total_pairs"`
		Groups     []struct {
			GroupID int                   `json:"group_id"`
			Members []legacyITGroupMember `json:"members"`
		} `json:"groups"`
	} `json:"result"`
}

type legacyITOnetoonePerson struct {
	OpenID   string `json:"open_id"`
	Nickname string `json:"nickname"`
	UserID   string `json:"user_id"`
	Gender   string `json:"gender"`
	Partners []struct {
		OpenID   string `json:"open_id"`
		Nickname string `json:"nickname"`
		UserID   string `json:"user_id"`
		Rank     int    `json:"rank"`
	} `json:"partners"`
}

type legacyITChatPerson struct {
	ID       int64  `json:"id"`
	OpenID   string `json:"open_id"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Assigned int    `json:"assigned"`
	Chatted  int    `json:"chatted"`
	Pairs    []struct {
		OpenID  string `json:"open_id"`
		Name    string `json:"name"`
		Chatted bool   `json:"chatted"`
	} `json:"pairs"`
	Unchatted []struct {
		OpenID string `json:"open_id"`
		Name   string `json:"name"`
	} `json:"unchatted"`
}

func TestIntegrationLegacyAdminActivityReadPaths(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	_, adminClient := legacyITAdmin(t, env)
	_, plainClient := env.createUser(t, "U-LEGACYPLAIN", "普通用户", "active", "user")
	reviewer, reviewerClient := env.createUser(t, "U-LEGACYREV", "审核员", "active", "user")
	cleanupHTTPUsers(t, env, reviewer.ID)
	if changed, err := env.database.AssignAdminRole(env.ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}
	matchingOnlyClient, _ := legacyITTempAdmin(t, env, "legacy_matching_only", adminPermMatchingRun)

	activityID, activityNo, _, _ := legacyITMatchingFixture(t, env)
	// One extra signed-up male stays unsubmitted and is checked in on site.
	unsubmitted, _ := env.createUser(t, "U-LEGACY-UNSUB", "未填男", "active", "user")
	cleanupHTTPUsers(t, env, unsubmitted.ID)
	setHTTPGender(t, env, unsubmitted.ID, "男性")
	matchingSignup(t, env, activityID, unsubmitted.ID, "按时")
	cancelledID, cancelledNo := createHTTPActivity(t, env, 0, false, "")
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET status = '已取消' WHERE id = $1`, cancelledID); err != nil {
		t.Fatal(err)
	}

	anon := newWSClient(t, env.server.URL)
	resp := anon.get("/legacy/api/admin/group/activities")
	if resp.StatusCode != http.StatusUnauthorized || legacyITError(t, resp) != "未登录" {
		t.Fatalf("anonymous activities status=%d", resp.StatusCode)
	}
	resp = plainClient.get("/legacy/api/admin/group/activities")
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "没有权限" {
		t.Fatalf("plain activities status=%d", resp.StatusCode)
	}
	resp = reviewerClient.get("/legacy/api/admin/group/activities")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer activities status=%d", resp.StatusCode)
	}

	resp = adminClient.get("/legacy/api/admin/group/activities")
	var list struct {
		Activities []legacyITActivityRow `json:"activities"`
	}
	profileDecode(t, resp, &list)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activities status=%d", resp.StatusCode)
	}
	row := findLegacyActivity(t, list.Activities, activityNo)
	if row.ID != activityID || row.SignupCount != 5 || row.SubmittedCount != 4 ||
		row.OnetooneSubmitted != 4 || row.Mode != "group" || row.Conflict ||
		row.GroupFlag != "是" || row.OnetooneFlag != "否" ||
		row.AvailableRounds == nil || len(row.AvailableRounds) != 0 || row.LatestRound != nil {
		t.Fatalf("activity row = %+v", row)
	}
	if row.GroupStatus != "" {
		t.Fatalf("group_status = %q", row.GroupStatus)
	}
	if findLegacyActivityIndex(list.Activities, cancelledNo) < 0 {
		t.Fatal("cancelled activity missing from the admin list")
	}

	// Reads accept either permission; the code is only visible to activity.manage.
	resp = matchingOnlyClient.get("/legacy/api/admin/group/status?activity_id=" + url.QueryEscape(activityNo))
	var limitedStatus legacyITStatus
	profileDecode(t, resp, &limitedStatus)
	if resp.StatusCode != http.StatusOK || limitedStatus.SigninCode != "" {
		t.Fatalf("matching-only status=%d code=%q", resp.StatusCode, limitedStatus.SigninCode)
	}

	resp = adminClient.get("/legacy/api/admin/group/status?activity_id=" + url.QueryEscape(activityNo))
	var status legacyITStatus
	profileDecode(t, resp, &status)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if status.ActivityID != activityNo || status.ID != activityID || status.Mode != "group" ||
		status.SigninFlag != "是" || status.SigninCode != "0123" ||
		status.SignupCount != 5 || status.SubmittedCount != 4 || status.UnsubmittedCount != 1 ||
		status.MaleCount != 2 || status.FemaleCount != 2 ||
		status.UnsubmittedOnSite != 1 || status.OnetooneSubmittedCount != 4 ||
		status.OnetooneUnsubmittedCount != 1 || status.OnetooneUnsubmittedOnSite != 1 ||
		status.Rounds == nil || len(status.Rounds) != 0 || status.LatestRound != nil ||
		status.OnetooneRounds == nil || len(status.OnetooneRounds) != 0 {
		t.Fatalf("status = %+v", status)
	}

	for _, path := range []string{
		"/legacy/api/admin/group/unsubmitted?activity_id=" + url.QueryEscape(activityNo),
		"/legacy/api/admin/onetoone/unsubmitted?activity_id=" + url.QueryEscape(activityNo),
	} {
		resp = adminClient.get(path)
		var roster legacyITUnsubmitted
		profileDecode(t, resp, &roster)
		if resp.StatusCode != http.StatusOK || roster.Total != 1 || len(roster.Unsubmitted) != 1 ||
			len(roster.Submitted) != 4 || roster.SigninEnabled != true || roster.UnsubmittedChecked != 1 {
			t.Fatalf("%s roster = %+v", path, roster)
		}
		item := roster.Unsubmitted[0]
		if item.OpenID == "" || item.UserID == "" || item.RealName == "" || !item.CheckedIn ||
			item.Gender != "男性" {
			t.Fatalf("%s unsubmitted item = %+v", path, item)
		}
		if roster.Unsubmitted == nil || roster.Submitted == nil {
			t.Fatalf("%s returned null arrays", path)
		}
	}

	emptyID, emptyNo := createHTTPActivity(t, env, 0, false, "")
	_ = emptyID
	resp = adminClient.get("/legacy/api/admin/group/unsubmitted?activity_id=" + url.QueryEscape(emptyNo))
	var emptyRoster legacyITUnsubmitted
	profileDecode(t, resp, &emptyRoster)
	if emptyRoster.Unsubmitted == nil || emptyRoster.Submitted == nil ||
		!emptyRoster.AllSubmitted || emptyRoster.Total != 0 {
		t.Fatalf("empty roster = %+v", emptyRoster)
	}

	resp = adminClient.get("/legacy/api/admin/group/status")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing activity_id status=%d", resp.StatusCode)
	}
	resp = adminClient.get("/legacy/api/admin/group/status?activity_id=A-NOT-THERE")
	if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "活动不存在" {
		t.Fatalf("unknown activity status=%d", resp.StatusCode)
	}
	// Unknown legacy paths must stay JSON so the SPA reports the real error.
	resp = adminClient.get("/legacy/api/admin/not-a-route")
	if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "找不到资源" {
		t.Fatalf("unknown legacy route status=%d", resp.StatusCode)
	}
}

func findLegacyActivityIndex(rows []legacyITActivityRow, activityNo string) int {
	for i, row := range rows {
		if row.ActivityID == activityNo {
			return i
		}
	}
	return -1
}

func findLegacyActivity(t *testing.T, rows []legacyITActivityRow, activityNo string) legacyITActivityRow {
	t.Helper()
	index := findLegacyActivityIndex(rows, activityNo)
	if index < 0 {
		t.Fatalf("activity %s not in list", activityNo)
	}
	return rows[index]
}

func TestIntegrationLegacyAdminActivitySettings(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	admin, adminClient := legacyITAdmin(t, env)
	_, plainClient := env.createUser(t, "U-LEGACYSETPLAIN", "普通用户", "active", "user")
	activityID, activityNo := legacyITActivity(t, env)
	matchingSetModes(t, env, activityID, false, false)
	csrf := adminClient.csrfCookie()

	post := func(client *wsClient, path string, token string, payload any) *http.Response {
		t.Helper()
		return profileJSONRequest(t, client, http.MethodPost, path, token, payload)
	}

	resp := post(plainClient, "/legacy/api/admin/activity/mode", plainClient.csrfCookie(),
		map[string]any{"activity_id": activityNo, "mode": "group"})
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "没有权限" {
		t.Fatalf("plain mode status=%d", resp.StatusCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", "",
		map[string]any{"activity_id": activityNo, "mode": "group"})
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "表单已过期，请重新整理后再试" {
		t.Fatalf("csrf mode status=%d", resp.StatusCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "bogus"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d", resp.StatusCode)
	}

	var before string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT updated_at::text FROM activities WHERE id = $1`, activityID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "group"})
	var modePayload struct {
		OK           bool   `json:"ok"`
		Mode         string `json:"mode"`
		GroupFlag    string `json:"group_flag"`
		OnetooneFlag string `json:"onetoone_flag"`
		Changed      bool   `json:"changed"`
	}
	profileDecode(t, resp, &modePayload)
	if resp.StatusCode != http.StatusOK || !modePayload.OK || modePayload.Mode != "group" ||
		modePayload.GroupFlag != "是" || modePayload.OnetooneFlag != "否" || !modePayload.Changed {
		t.Fatalf("mode group = %+v status=%d", modePayload, resp.StatusCode)
	}
	var after string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT updated_at::text FROM activities WHERE id = $1`, activityID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("mode switch did not touch updated_at")
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "group"})
	modePayload = struct {
		OK           bool   `json:"ok"`
		Mode         string `json:"mode"`
		GroupFlag    string `json:"group_flag"`
		OnetooneFlag string `json:"onetoone_flag"`
		Changed      bool   `json:"changed"`
	}{}
	profileDecode(t, resp, &modePayload)
	if resp.StatusCode != http.StatusOK || modePayload.Changed {
		t.Fatalf("idempotent mode = %+v status=%d", modePayload, resp.StatusCode)
	}
	var unchanged string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT updated_at::text FROM activities WHERE id = $1`, activityID).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != after {
		t.Fatal("same-value mode switch wrote the row")
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "onetoone"})
	profileDecode(t, resp, &modePayload)
	if resp.StatusCode != http.StatusOK || modePayload.Mode != "onetoone" ||
		modePayload.GroupFlag != "否" || modePayload.OnetooneFlag != "是" || !modePayload.Changed {
		t.Fatalf("mode onetoone = %+v", modePayload)
	}

	// The advisory lock is the same one grouping uses: a held lock must turn
	// the switch into a v1 409 instead of a half-written mode.
	lockTx, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var locked bool
	if err := lockTx.QueryRow(env.ctx, `SELECT pg_try_advisory_xact_lock($1)`,
		int64(0x47525000)+activityID).Scan(&locked); err != nil || !locked {
		t.Fatalf("acquire fake grouping lock: locked=%v err=%v", locked, err)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "none"})
	var busy struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	profileDecode(t, resp, &busy)
	if resp.StatusCode != http.StatusConflict || busy.Code != "MODE_SWITCH_BLOCKED" {
		t.Fatalf("busy mode status=%d payload=%+v", resp.StatusCode, busy)
	}
	if err := lockTx.Rollback(env.ctx); err != nil {
		t.Fatal(err)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/mode", csrf,
		map[string]any{"activity_id": activityNo, "mode": "none"})
	profileDecode(t, resp, &modePayload)
	if resp.StatusCode != http.StatusOK || modePayload.Mode != "none" {
		t.Fatalf("mode none after lock = %+v status=%d", modePayload, resp.StatusCode)
	}

	resp = post(adminClient, "/legacy/api/admin/activity/status", csrf,
		map[string]any{"activity_id": activityNo, "status": "不存在的状态"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status=%d", resp.StatusCode)
	}
	for _, target := range []string{"已结束", "已结束", "报名中"} {
		resp = post(adminClient, "/legacy/api/admin/activity/status", csrf,
			map[string]any{"activity_id": activityNo, "status": target})
		var payload struct {
			OK     bool   `json:"ok"`
			Status string `json:"status"`
		}
		profileDecode(t, resp, &payload)
		if resp.StatusCode != http.StatusOK || !payload.OK || payload.Status != target {
			t.Fatalf("status %s = %+v code=%d", target, payload, resp.StatusCode)
		}
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET status = '已取消' WHERE id = $1`, activityID); err != nil {
		t.Fatal(err)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/status", csrf,
		map[string]any{"activity_id": activityNo, "status": "已结束"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status from cancelled=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = post(adminClient, "/legacy/api/admin/activity/signin-code", csrf,
		map[string]any{"activity_id": activityNo, "code": "123"})
	if resp.StatusCode != http.StatusBadRequest || legacyITError(t, resp) != "签到码须为4位数字" {
		t.Fatalf("short code status=%d", resp.StatusCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/signin-code", csrf,
		map[string]any{"activity_id": activityNo, "code": "abcd"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-digit code status=%d", resp.StatusCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/signin-code", csrf,
		map[string]any{"activity_id": activityNo, "code": "0123"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save code status=%d", resp.StatusCode)
	}
	var storedCode string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT signin_code FROM activities WHERE id = $1`, activityID).Scan(&storedCode); err != nil {
		t.Fatal(err)
	}
	if storedCode != "0123" {
		t.Fatalf("stored code = %q", storedCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/signin-code", csrf,
		map[string]any{"activity_id": activityNo, "code": 4321})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("numeric code status=%d", resp.StatusCode)
	}
	if legacyITAuditContains(t, env, admin.ID, "admin.activity.signin_code", activityNo, "4321") != 0 ||
		legacyITAuditContains(t, env, admin.ID, "admin.activity.signin_code", activityNo, "0123") != 0 {
		t.Fatal("signin code leaked into audit metadata")
	}

	resp = post(adminClient, "/legacy/api/admin/activity/signin/flag", csrf,
		map[string]any{"activity_id": activityNo, "enabled": true})
	var flagPayload struct {
		OK         bool   `json:"ok"`
		SigninFlag string `json:"signin_flag"`
	}
	profileDecode(t, resp, &flagPayload)
	if resp.StatusCode != http.StatusOK || flagPayload.SigninFlag != "是" {
		t.Fatalf("flag on = %+v status=%d", flagPayload, resp.StatusCode)
	}
	resp = post(adminClient, "/legacy/api/admin/activity/signin/flag", csrf,
		map[string]any{"activity_id": activityNo, "enabled": false})
	profileDecode(t, resp, &flagPayload)
	if resp.StatusCode != http.StatusOK || flagPayload.SigninFlag != "否" {
		t.Fatalf("flag off = %+v status=%d", flagPayload, resp.StatusCode)
	}

	var audits int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log
		  WHERE actor_user_id = $1 AND action IN
		        ('admin.activity.mode', 'admin.activity.status', 'admin.activity.signin_flag')
		    AND target_id = $2`, admin.ID, activityNo).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits < 3 {
		t.Fatalf("settings audit rows = %d", audits)
	}
}

func TestIntegrationLegacyAdminSignin(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	admin, adminClient := legacyITAdmin(t, env)
	activityID, activityNo := legacyITActivity(t, env)

	people := make([]db.User, 0, 3)
	for i, gender := range []string{"男性", "男性", "女性"} {
		person, _ := env.createUser(t, fmt.Sprintf("U-LEGACYSIGN%d", i), fmt.Sprintf("签到%d", i), "active", "user")
		cleanupHTTPUsers(t, env, person.ID)
		setHTTPGender(t, env, person.ID, gender)
		matchingSignup(t, env, activityID, person.ID, "")
		people = append(people, person)
	}
	// A signed-up user outside the roster path.
	unsigned, _ := env.createUser(t, "U-LEGACYUNSIGN", "没报名", "active", "user")
	cleanupHTTPUsers(t, env, unsigned.ID)

	openIDs := make([]string, 0, len(people))
	for _, person := range people {
		openIDs = append(openIDs, *person.LegacyUserID)
	}
	csrf := adminClient.csrfCookie()

	resp := adminClient.get("/legacy/api/admin/activity/signin?activity_id=" + url.QueryEscape(activityNo))
	var roster struct {
		OK            bool                 `json:"ok"`
		ActivityID    string               `json:"activity_id"`
		Name          string               `json:"name"`
		SigninEnabled bool                 `json:"signin_enabled"`
		Stats         map[string]int       `json:"stats"`
		Roster        []legacyITRosterItem `json:"roster"`
	}
	profileDecode(t, resp, &roster)
	if resp.StatusCode != http.StatusOK || !roster.OK || roster.ActivityID != activityNo ||
		!roster.SigninEnabled || len(roster.Roster) != 3 ||
		roster.Stats["signed_up"] != 3 || roster.Stats["checked_in"] != 0 ||
		roster.Stats["not_checked_in"] != 3 {
		t.Fatalf("signin roster = %+v status=%d", roster, resp.StatusCode)
	}
	seen := map[string]bool{}
	for _, item := range roster.Roster {
		if item.OpenID == "" || item.UserID == "" || item.RealName == "" || item.State != "" || item.CheckedIn {
			t.Fatalf("signin item = %+v", item)
		}
		if seen[item.OpenID] {
			t.Fatalf("duplicate open_id %q", item.OpenID)
		}
		seen[item.OpenID] = true
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "open_id": openIDs[0], "state": "按时"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mark signin status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var attendance string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT COALESCE(attendance,'') FROM signups WHERE activity_id=$1 AND user_id=$2`,
		activityID, people[0].ID).Scan(&attendance); err != nil {
		t.Fatal(err)
	}
	if attendance != "按时" {
		t.Fatalf("attendance = %q", attendance)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "open_id": openIDs[0], "state": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear signin status=%d", resp.StatusCode)
	}
	if err := env.pool.QueryRow(env.ctx,
		`SELECT COALESCE(attendance,'') FROM signups WHERE activity_id=$1 AND user_id=$2`,
		activityID, people[0].ID).Scan(&attendance); err != nil {
		t.Fatal(err)
	}
	if attendance != "" {
		t.Fatalf("attendance after clear = %q", attendance)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "open_id": openIDs[0], "state": "迟到"})
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(legacyITError(t, resp), "state 只能为") {
		t.Fatalf("late state status=%d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "open_id": *unsigned.LegacyUserID, "state": "按时"})
	if resp.StatusCode != http.StatusNotFound ||
		legacyITError(t, resp) != "该用户没有报名此活动" {
		t.Fatalf("unsigned user status=%d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "open_id": "U-NOBODY", "state": "按时"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown user status=%d", resp.StatusCode)
	}

	for _, index := range []int{0, 1} {
		resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
			csrf, map[string]any{"activity_id": activityNo, "open_id": openIDs[index], "state": "按时"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("bulk mark %d status=%d", index, resp.StatusCode)
		}
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "clear_all": true})
	var cleared struct {
		OK      bool  `json:"ok"`
		Cleared int64 `json:"cleared"`
		Failed  int64 `json:"failed"`
		Total   int   `json:"total"`
	}
	profileDecode(t, resp, &cleared)
	if resp.StatusCode != http.StatusOK || cleared.Cleared != 2 || cleared.Failed != 0 || cleared.Total != 3 {
		t.Fatalf("clear all = %+v status=%d", cleared, resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/signin",
		csrf, map[string]any{"activity_id": activityNo, "clear_all": true})
	profileDecode(t, resp, &cleared)
	if cleared.Cleared != 0 {
		t.Fatalf("second clear all = %+v", cleared)
	}

	if legacyITAuditContains(t, env, admin.ID, "admin.activity.attendance", activityNo, "按时") < 1 ||
		legacyITAuditContains(t, env, admin.ID, "admin.activity.attendance_clear", activityNo, "cleared") < 1 {
		t.Fatal("signin audit rows missing")
	}
	resp = adminClient.get("/legacy/api/admin/activity/signin")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing activity_id status=%d", resp.StatusCode)
	}
}

func TestIntegrationLegacyAdminMatchingFlow(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	_, adminClient := legacyITAdmin(t, env)
	_, activityNo, males, females := legacyITMatchingFixture(t, env)
	csrf := adminClient.csrfCookie()

	// Preview: JSON POST with CSRF, v1 field names.
	resp := profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/preview",
		csrf, map[string]any{"activity_id": activityNo, "round": 1})
	var preview struct {
		ActivityID     string   `json:"activity_id"`
		MaleCount      int      `json:"male_count"`
		FemaleCount    int      `json:"female_count"`
		ExpectedGroups int      `json:"expected_groups"`
		Blockers       []string `json:"blockers"`
	}
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || preview.ActivityID != activityNo ||
		preview.MaleCount != 2 || preview.FemaleCount != 2 ||
		preview.ExpectedGroups != 2 || preview.Blockers == nil {
		t.Fatalf("group preview = %+v status=%d", preview, resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/preview",
		"", map[string]any{"activity_id": activityNo})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("preview without csrf status=%d", resp.StatusCode)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/run",
		csrf, map[string]any{
			"activity_id": activityNo, "round": 1,
			"males_per_group": 1, "females_per_group": 1,
		})
	var run legacyITRunPayload
	profileDecode(t, resp, &run)
	if resp.StatusCode != http.StatusOK || !run.OK || run.Result.Code != "OK" ||
		run.Result.Round != 1 || run.Result.GroupCount != 2 || len(run.Result.Groups) != 2 {
		t.Fatalf("group run = %+v status=%d", run, resp.StatusCode)
	}
	for _, group := range run.Result.Groups {
		for _, member := range group.Members {
			if member.OpenID == "" || member.DisplayName == "" || member.UserID == "" {
				t.Fatalf("run member = %+v", member)
			}
		}
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/run",
		csrf, map[string]any{
			"activity_id": activityNo, "round": 1,
			"males_per_group": 1, "females_per_group": 1,
		})
	var duplicate legacyITRunPayload
	profileDecode(t, resp, &duplicate)
	if resp.StatusCode != http.StatusOK || duplicate.Result.Code != "ALREADY_DONE" ||
		duplicate.Result.Round != 1 {
		t.Fatalf("duplicate run = %+v status=%d", duplicate, resp.StatusCode)
	}

	resp = adminClient.get("/legacy/api/admin/group/result?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	var result legacyITGroupResult
	profileDecode(t, resp, &result)
	if resp.StatusCode != http.StatusOK || result.ActivityID != activityNo ||
		result.Round != 1 || len(result.Groups) != 2 || len(result.Rounds) != 1 {
		t.Fatalf("group result = %+v status=%d", result, resp.StatusCode)
	}
	for _, group := range result.Groups {
		for _, member := range group.Members {
			if member.OpenID == "" || member.DisplayName == "" || member.UserID == "" {
				t.Fatalf("result member = %+v", member)
			}
		}
	}

	// The shared v2 endpoint gained the same fields (additive only).
	resp = adminClient.get("/api/admin/group/result?activity_id=" + url.QueryEscape(activityNo))
	var v2Result struct {
		Round  int `json:"round"`
		Groups []struct {
			GroupID int                   `json:"group_id"`
			Members []legacyITGroupMember `json:"members"`
		} `json:"groups"`
	}
	profileDecode(t, resp, &v2Result)
	if resp.StatusCode != http.StatusOK || len(v2Result.Groups) != 2 || v2Result.Round != 1 {
		t.Fatalf("v2 group result status=%d", resp.StatusCode)
	}
	for _, group := range v2Result.Groups {
		for _, member := range group.Members {
			if member.OpenID == "" || member.DisplayName == "" {
				t.Fatalf("v2 result member missing open_id/display_name: %+v", member)
			}
		}
	}

	// v2 run accepts the plural keys too (item 9 of the compatibility list).
	secondID, secondNo := legacyITActivity(t, env)
	matchingSetModes(t, env, secondID, true, false)
	for _, person := range males {
		matchingSignup(t, env, secondID, person.ID, "按时")
	}
	for _, person := range females {
		matchingSignup(t, env, secondID, person.ID, "按时")
	}
	createHTTPGroupSelection(t, env, secondID, males[0].ID, "男性",
		[]int64{females[0].ID, females[1].ID})
	createHTTPGroupSelection(t, env, secondID, males[1].ID, "男性",
		[]int64{females[0].ID, females[1].ID})
	createHTTPGroupSelection(t, env, secondID, females[0].ID, "女性",
		[]int64{males[0].ID, males[1].ID})
	createHTTPGroupSelection(t, env, secondID, females[1].ID, "女性",
		[]int64{males[0].ID, males[1].ID})
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/api/admin/group/run", csrf,
		map[string]any{
			"activity_id": secondNo, "round": 1,
			"males_per_group": 1, "females_per_group": 1,
		})
	var v2Run struct {
		OK     bool `json:"ok"`
		Result struct {
			Round      int `json:"round"`
			GroupCount int `json:"group_count"`
		} `json:"result"`
	}
	profileDecode(t, resp, &v2Run)
	if resp.StatusCode != http.StatusOK || !v2Run.OK || v2Run.Result.Round != 1 ||
		v2Run.Result.GroupCount != 2 {
		t.Fatalf("v2 plural run = %+v status=%d", v2Run, resp.StatusCode)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/reset",
		csrf, map[string]any{"activity_id": activityNo})
	var reset struct {
		OK             bool   `json:"ok"`
		ActivityID     string `json:"activity_id"`
		ClearedResults int64  `json:"cleared_results"`
	}
	profileDecode(t, resp, &reset)
	if resp.StatusCode != http.StatusOK || reset.ActivityID != activityNo || reset.ClearedResults != 4 {
		t.Fatalf("group reset = %+v status=%d", reset, resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/reset",
		csrf, map[string]any{"activity_id": activityNo})
	profileDecode(t, resp, &reset)
	if reset.ClearedResults != 0 {
		t.Fatalf("second group reset = %+v", reset)
	}

	// One-to-one: switch mode, preview, run synchronously, read results.
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/activity/mode",
		csrf, map[string]any{"activity_id": activityNo, "mode": "onetoone"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch onetoone status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/onetoone/preview",
		csrf, map[string]any{"activity_id": activityNo, "round": 1, "female_chats": 2})
	var otoPreview struct {
		MaleCount     int      `json:"male_count"`
		FemaleCount   int      `json:"female_count"`
		TotalExpected int      `json:"total_pairs_expected"`
		Blockers      []string `json:"blockers"`
	}
	profileDecode(t, resp, &otoPreview)
	if resp.StatusCode != http.StatusOK || otoPreview.MaleCount != 2 ||
		otoPreview.FemaleCount != 2 || otoPreview.Blockers == nil {
		t.Fatalf("onetoone preview = %+v status=%d", otoPreview, resp.StatusCode)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/onetoone/run",
		csrf, map[string]any{"activity_id": activityNo, "round": 1, "female_chats": 2})
	var otoRun legacyITRunPayload
	profileDecode(t, resp, &otoRun)
	if resp.StatusCode != http.StatusOK || !otoRun.OK || otoRun.Result.Code != "OK" ||
		otoRun.Result.Round != 1 || otoRun.Result.TotalPairs != 8 {
		t.Fatalf("onetoone run = %+v status=%d", otoRun, resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/onetoone/run",
		csrf, map[string]any{"activity_id": activityNo, "round": 1, "female_chats": 2})
	profileDecode(t, resp, &otoRun)
	if resp.StatusCode != http.StatusOK || otoRun.Result.Code != "ALREADY_DONE" {
		t.Fatalf("duplicate onetoone run = %+v status=%d", otoRun, resp.StatusCode)
	}

	resp = adminClient.get("/legacy/api/admin/onetoone/result?activity_id=" +
		url.QueryEscape(activityNo) + "&round=1")
	var otoResult struct {
		ActivityID  string                   `json:"activity_id"`
		Round       int                      `json:"round"`
		Rounds      []int                    `json:"rounds"`
		ResultCount int                      `json:"result_count"`
		Matched     int                      `json:"matched_count"`
		People      []legacyITOnetoonePerson `json:"people"`
	}
	profileDecode(t, resp, &otoResult)
	if resp.StatusCode != http.StatusOK || otoResult.ActivityID != activityNo ||
		otoResult.ResultCount != 8 || otoResult.Matched != 4 || len(otoResult.People) != 4 {
		t.Fatalf("onetoone result = %+v status=%d", otoResult, resp.StatusCode)
	}
	for _, person := range otoResult.People {
		if person.OpenID == "" || person.Nickname == "" || person.UserID == "" {
			t.Fatalf("onetoone person = %+v", person)
		}
		for _, partner := range person.Partners {
			if partner.OpenID == "" || partner.Nickname == "" || partner.UserID == "" {
				t.Fatalf("onetoone partner = %+v", partner)
			}
		}
	}

	resp = adminClient.get("/legacy/api/admin/onetoone/chat-overview?activity_id=" +
		url.QueryEscape(activityNo))
	var chat struct {
		ActivityID string               `json:"activity_id"`
		Round      int                  `json:"round"`
		Rounds     []int                `json:"rounds"`
		People     []legacyITChatPerson `json:"people"`
	}
	profileDecode(t, resp, &chat)
	if resp.StatusCode != http.StatusOK || chat.ActivityID != activityNo ||
		chat.Round != 1 || len(chat.People) != 4 {
		t.Fatalf("chat overview = %+v status=%d", chat, resp.StatusCode)
	}
	for _, person := range chat.People {
		if person.OpenID == "" || person.ID == 0 {
			t.Fatalf("chat person = %+v", person)
		}
		for _, pair := range person.Pairs {
			if pair.OpenID == "" {
				t.Fatalf("chat pair = %+v", pair)
			}
		}
		for _, unchatted := range person.Unchatted {
			if unchatted.OpenID == "" {
				t.Fatalf("chat unchatted = %+v", unchatted)
			}
		}
	}

	// The v2 chat endpoint keeps id and gains open_id.
	resp = adminClient.get("/api/admin/onetoone/chat-overview?activity_id=" + url.QueryEscape(activityNo))
	var v2Chat struct {
		People []legacyITChatPerson `json:"people"`
	}
	profileDecode(t, resp, &v2Chat)
	if resp.StatusCode != http.StatusOK || len(v2Chat.People) != 4 || v2Chat.People[0].OpenID == "" {
		t.Fatalf("v2 chat overview status=%d people=%+v", resp.StatusCode, v2Chat.People)
	}

	resp = adminClient.get("/api/admin/onetoone/result?activity_id=" + url.QueryEscape(activityNo))
	var v2Oto struct {
		People []legacyITOnetoonePerson `json:"people"`
	}
	profileDecode(t, resp, &v2Oto)
	if resp.StatusCode != http.StatusOK || len(v2Oto.People) != 4 || v2Oto.People[0].OpenID == "" {
		t.Fatalf("v2 onetoone result status=%d", resp.StatusCode)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/onetoone/reset",
		csrf, map[string]any{"activity_id": activityNo})
	var otoReset struct {
		OK             bool  `json:"ok"`
		ClearedResults int64 `json:"cleared_results"`
	}
	profileDecode(t, resp, &otoReset)
	if resp.StatusCode != http.StatusOK || otoReset.ClearedResults != 8 {
		t.Fatalf("onetoone reset = %+v status=%d", otoReset, resp.StatusCode)
	}

	// Group run in one-to-one mode reports the v1 job result code.
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/legacy/api/admin/group/run",
		csrf, map[string]any{
			"activity_id": activityNo, "males_per_group": 1, "females_per_group": 1,
		})
	profileDecode(t, resp, &run)
	if resp.StatusCode != http.StatusOK || run.Result.Code != "EXCLUSIVE_MODE" {
		t.Fatalf("group run in onetoone mode = %+v status=%d", run, resp.StatusCode)
	}

	for _, path := range []string{
		"/legacy/api/admin/group/job",
		"/legacy/api/admin/onetoone/job",
	} {
		resp = adminClient.get(path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s without job_id status=%d", path, resp.StatusCode)
		}
		resp = adminClient.get(path + "?job_id=deadbeef")
		if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "任务不存在" {
			t.Fatalf("%s unknown job status=%d", path, resp.StatusCode)
		}
	}
}
