package httpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func legacyJSONError(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode legacy error: %v", err)
	}
	return payload.Error, payload.Code
}

func legacySignupRow(t *testing.T, env *profileHTTPEnv, activityID, userID int64) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, status, attendance)
		 VALUES ($1, $2, '已报名', '按时')
		 ON CONFLICT (activity_id, user_id) DO UPDATE
		   SET status = '已报名', attendance = '按时'`, activityID, userID); err != nil {
		t.Fatalf("insert signup: %v", err)
	}
}

func TestIntegrationLegacyActivitiesFlow(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	viewer, viewerClient := env.createUser(t, "U-LGACT-VIEW", "志愿女", "active", "user")
	var males []db.User
	for i := 0; i < 7; i++ {
		male, _ := env.createUser(t, "U-LGACT-M"+strconv.Itoa(i),
			"候选男"+strconv.Itoa(i), "active", "user")
		males = append(males, male)
	}
	cleanupHTTPUsers(t, env, viewer.ID,
		males[0].ID, males[1].ID, males[2].ID, males[3].ID,
		males[4].ID, males[5].ID, males[6].ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '女性', legacy_open_id = 'ou_legacy_viewer' WHERE id = $1`,
		viewer.ID); err != nil {
		t.Fatal(err)
	}
	for i, male := range males {
		if _, err := env.pool.Exec(env.ctx,
			`UPDATE users SET gender = '男性', legacy_open_id = $2 WHERE id = $1`,
			male.ID, "ou_legacy_male_"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	activityID, activityNo := createHTTPActivity(t, env, 0, true, "0123")
	base := "/legacy/api/activities/" + url.PathEscape(activityNo)
	csrf := viewerClient.csrfCookie()
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}

	// The detail route keeps the legacy fields the SPA reads.
	resp := viewerClient.get(base)
	var detail map[string]any
	profileDecode(t, resp, &detail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", resp.StatusCode)
	}
	for _, key := range []string{"poster", "signin_enabled", "my_attendance",
		"signup_users", "group_flag", "onetoone_flag", "start_time", "end_time",
		"fee", "food", "record_id"} {
		if _, ok := detail[key]; !ok {
			t.Fatalf("detail missing %q: %+v", key, detail)
		}
	}
	if detail["signin_enabled"] != true || detail["record_id"] != activityNo {
		t.Fatalf("detail signin/record mismatch: %+v", detail)
	}

	// Sign-up requires CSRF, reports duplicates in the legacy envelope and
	// keeps the count consistent.
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/signup", "", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("signup without csrf status = %d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/signup", csrf, nil)
	var signupResp struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	profileDecode(t, resp, &signupResp)
	if resp.StatusCode != http.StatusOK || !signupResp.OK || signupResp.Message != "报名成功" {
		t.Fatalf("signup = %+v status=%d", signupResp, resp.StatusCode)
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/signup", csrf, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate signup status = %d", resp.StatusCode)
	}
	if msg, _ := legacyJSONError(t, resp); !strings.Contains(msg, "已经报名") {
		t.Fatalf("duplicate message = %q", msg)
	}

	// Mine lists the activity with my_signup and the stored attendance.
	resp = viewerClient.get("/legacy/api/activities/mine")
	var mine struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &mine)
	if resp.StatusCode != http.StatusOK || len(mine.Activities) != 1 ||
		mine.Activities[0]["my_signup"] != true {
		t.Fatalf("mine = %+v status=%d", mine, resp.StatusCode)
	}

	// Wrong and correct self sign-in codes; the second submission is idempotent.
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/self-signin",
		csrf, map[string]any{"code": "9999"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong signin code status = %d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/self-signin",
		csrf, map[string]any{"code": "0123"})
	var signin struct {
		OK         bool   `json:"ok"`
		Attendance string `json:"attendance"`
		Already    bool   `json:"already"`
	}
	profileDecode(t, resp, &signin)
	if resp.StatusCode != http.StatusOK || !signin.OK || signin.Attendance != "按时" {
		t.Fatalf("signin = %+v status=%d", signin, resp.StatusCode)
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/self-signin",
		csrf, map[string]any{"code": "0123"})
	profileDecode(t, resp, &signin)
	if resp.StatusCode != http.StatusOK || !signin.Already {
		t.Fatalf("idempotent signin = %+v status=%d", signin, resp.StatusCode)
	}
	resp = viewerClient.get(base)
	var signedDetail struct {
		MyAttendance string   `json:"my_attendance"`
		SignupUsers  []string `json:"signup_users"`
	}
	profileDecode(t, resp, &signedDetail)
	if signedDetail.MyAttendance != "按时" ||
		len(signedDetail.SignupUsers) != 1 || signedDetail.SignupUsers[0] != "志愿女" {
		t.Fatalf("detail after signin = %+v", signedDetail)
	}

	// Roster is privacy-safe but keeps the openid the SPA needs for profiles.
	resp = viewerClient.get(base + "/signups")
	rosterBody := string(readBody(t, resp))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("roster status = %d body=%s", resp.StatusCode, rosterBody)
	}
	var roster struct {
		Visible bool `json:"visible"`
		Count   int  `json:"count"`
		Signups []struct {
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
			RealName string `json:"real_name"`
			UserID   string `json:"user_id"`
			Phone    string `json:"phone"`
		} `json:"signups"`
	}
	if err := json.Unmarshal([]byte(rosterBody), &roster); err != nil {
		t.Fatal(err)
	}
	if !roster.Visible || roster.Count != 1 || len(roster.Signups) != 1 {
		t.Fatalf("roster = %+v", roster)
	}
	if roster.Signups[0].OpenID != "ou_legacy_viewer" ||
		roster.Signups[0].Nickname != "志愿女" || roster.Signups[0].RealName != "" ||
		roster.Signups[0].Phone != "" {
		t.Fatalf("roster privacy fields = %+v", roster.Signups[0])
	}
	if viewer.Phone != "" && strings.Contains(rosterBody, viewer.Phone) {
		t.Fatalf("roster leaked the phone number: %s", rosterBody)
	}

	// Grouping starts: flag, my groups, candidates, status.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET group_enabled = true, group_status = '收集中' WHERE id = $1`,
		activityID); err != nil {
		t.Fatal(err)
	}
	for _, male := range males {
		legacySignupRow(t, env, activityID, male.ID)
	}
	// The user SPA reads the matching entry flags from /mine; they must stay in
	// sync with the activity mode.
	resp = viewerClient.get("/legacy/api/activities/mine")
	var mineGrouped struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &mineGrouped)
	if resp.StatusCode != http.StatusOK || len(mineGrouped.Activities) != 1 ||
		mineGrouped.Activities[0]["group_flag"] != "是" ||
		mineGrouped.Activities[0]["group_status"] != "收集中" ||
		mineGrouped.Activities[0]["onetoone_flag"] != "否" ||
		mineGrouped.Activities[0]["onetoone_status"] != "" {
		t.Fatalf("mine matching flags = %+v status=%d", mineGrouped.Activities, resp.StatusCode)
	}
	resp = viewerClient.get("/legacy/api/activities/mine/groups/flag")
	var flag struct {
		Has bool `json:"has_group_activity"`
	}
	profileDecode(t, resp, &flag)
	if !flag.Has {
		t.Fatalf("group flag = %+v", flag)
	}
	resp = viewerClient.get("/legacy/api/activities/mine/groups")
	var groups struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &groups)
	if len(groups.Activities) != 1 || groups.Activities[0]["my_choice_count"] != float64(0) {
		t.Fatalf("my groups = %+v", groups.Activities)
	}

	resp = viewerClient.get(base + "/group/candidates")
	var candidates struct {
		Candidates []struct {
			OpenID        string `json:"openid"`
			Nickname      string `json:"nickname"`
			RealName      string `json:"real_name"`
			UserID        string `json:"user_id"`
			BaptismalName string `json:"baptismal_name"`
			Photo         string `json:"photo"`
		} `json:"candidates"`
	}
	profileDecode(t, resp, &candidates)
	if resp.StatusCode != http.StatusOK || len(candidates.Candidates) != 7 {
		t.Fatalf("candidates status=%d payload=%+v", resp.StatusCode, candidates)
	}
	openIDs := make([]string, 0, 7)
	for i, candidate := range candidates.Candidates {
		openIDs = append(openIDs, candidate.OpenID)
		if candidate.OpenID != "ou_legacy_male_"+strconv.Itoa(i) {
			t.Fatalf("candidate openid %d = %q", i, candidate.OpenID)
		}
		if candidate.RealName != "候选男"+strconv.Itoa(i) {
			t.Fatalf("candidate real name %d = %q", i, candidate.RealName)
		}
	}
	resp = viewerClient.get(base + "/group/status")
	var status struct {
		GroupStatus string   `json:"group_status"`
		MySelected  bool     `json:"my_selected"`
		MyChoices   []string `json:"my_choices"`
	}
	profileDecode(t, resp, &status)
	if status.GroupStatus != "收集中" || status.MySelected || len(status.MyChoices) != 0 {
		t.Fatalf("initial group status = %+v", status)
	}

	// Seven choices are required; a valid submission round-trips the openids.
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/group/select",
		csrf, map[string]any{"choices": openIDs[:6]})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short choices status = %d", resp.StatusCode)
	}
	if msg, code := legacyJSONError(t, resp); code != "SELECT_NOT_FULL" || !strings.Contains(msg, "7") {
		t.Fatalf("short choices error = %q code=%q", msg, code)
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/group/select",
		csrf, map[string]any{"choices": openIDs})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("group select status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = viewerClient.get(base + "/group/status")
	profileDecode(t, resp, &status)
	if !status.MySelected || len(status.MyChoices) != 7 || status.MyChoices[0] != openIDs[0] {
		t.Fatalf("group status after select = %+v", status)
	}
	resp = viewerClient.get("/legacy/api/activities/mine/groups")
	groups = struct {
		Activities []map[string]any `json:"activities"`
	}{}
	profileDecode(t, resp, &groups)
	if len(groups.Activities) != 1 || groups.Activities[0]["my_choice_count"] != float64(7) {
		t.Fatalf("my groups after select = %+v", groups.Activities)
	}

	// Results are hidden until the activity is complete.
	resp = viewerClient.get(base + "/group/result")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("incomplete group result status = %d", resp.StatusCode)
	}
	if msg, _ := legacyJSONError(t, resp); msg != "分组尚未完成" {
		t.Fatalf("incomplete group message = %q", msg)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET group_status = '已完成' WHERE id = $1`, activityID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		userID int64
		name   string
		gender string
	}{
		{viewer.ID, "志愿女", "女性"},
		{males[0].ID, "候选男0", "男性"},
	} {
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO group_results (activity_id, group_no, user_id, user_name, gender, round)
			 VALUES ($1, 2, $2, $3, $4, 1)`,
			activityID, row.userID, row.name, row.gender); err != nil {
			t.Fatal(err)
		}
	}
	resp = viewerClient.get(base + "/group/result")
	var result struct {
		MyGroup     int `json:"my_group"`
		TotalGroups int `json:"total_groups"`
		Round       int `json:"round"`
		Rounds      []int
		Members     []struct {
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
			UserID   string `json:"user_id"`
			Gender   string `json:"gender"`
		} `json:"members"`
	}
	profileDecode(t, resp, &result)
	if resp.StatusCode != http.StatusOK || result.MyGroup != 2 || result.TotalGroups != 1 ||
		result.Round != 1 || len(result.Rounds) != 1 || len(result.Members) != 2 {
		t.Fatalf("group result = %+v status=%d", result, resp.StatusCode)
	}
	if result.Members[0].OpenID != "ou_legacy_male_0" ||
		result.Members[0].Nickname != "候选男0" || result.Members[0].Gender != "男性" {
		t.Fatalf("group member = %+v", result.Members[0])
	}

	// One-to-one result and chat marker.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET onetoone_enabled = true, onetoone_status = '已完成' WHERE id = $1`,
		activityID); err != nil {
		t.Fatal(err)
	}
	for i, male := range males[:2] {
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO onetoone_results
			   (activity_id, rank, user_id, user_name, gender, target_id, target_name, round)
			 VALUES ($1, $2, $3, '志愿女', '女性', $4, $5, 1)`,
			activityID, i+1, viewer.ID, male.ID, male.Name); err != nil {
			t.Fatal(err)
		}
	}
	resp = viewerClient.get(base + "/onetoone/result")
	var onetoone struct {
		Count    int `json:"count"`
		Round    int `json:"round"`
		Rounds   []int
		Partners []struct {
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
			Rank     int    `json:"rank"`
			Chatted  bool   `json:"chatted"`
		} `json:"partners"`
	}
	profileDecode(t, resp, &onetoone)
	if resp.StatusCode != http.StatusOK || onetoone.Count != 2 || onetoone.Round != 1 ||
		len(onetoone.Partners) != 2 || onetoone.Partners[0].Chatted {
		t.Fatalf("onetoone result = %+v status=%d", onetoone, resp.StatusCode)
	}
	if onetoone.Partners[0].OpenID != "ou_legacy_male_0" ||
		onetoone.Partners[0].Nickname != "候选男0" || onetoone.Partners[0].Rank != 1 {
		t.Fatalf("onetoone partner = %+v", onetoone.Partners[0])
	}
	resp = profileJSONRequest(t, viewerClient, http.MethodPost, base+"/onetoone/chat", csrf,
		map[string]any{"target_openid": "ou_legacy_male_0", "content": "你好，很高兴认识你"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("onetoone chat status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = viewerClient.get(base + "/onetoone/result")
	onetoone = struct {
		Count    int `json:"count"`
		Round    int `json:"round"`
		Rounds   []int
		Partners []struct {
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
			Rank     int    `json:"rank"`
			Chatted  bool   `json:"chatted"`
		} `json:"partners"`
	}{}
	profileDecode(t, resp, &onetoone)
	if !onetoone.Partners[0].Chatted || onetoone.Partners[1].Chatted {
		t.Fatalf("onetoone chat marker = %+v", onetoone.Partners)
	}

	// Cancel removes the activity from mine but keeps the grouping history.
	resp = profileJSONRequest(t, viewerClient, http.MethodDelete, base+"/signup", csrf, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = viewerClient.get("/legacy/api/activities/mine")
	mine = struct {
		Activities []map[string]any `json:"activities"`
	}{}
	profileDecode(t, resp, &mine)
	if len(mine.Activities) != 0 {
		t.Fatalf("mine after cancel = %+v", mine.Activities)
	}
}

func TestIntegrationLegacyRosterFeatureFlag(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(cfg *config.Config) {
		cfg.ActivityRosterDisabled = true
	})
	viewer, client := env.createUser(t, "U-LGROSTER", "名单甲", "active", "user")
	other, _ := env.createUser(t, "U-LGROSTER2", "名单乙", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, other.ID)
	activityID, activityNo := createHTTPActivity(t, env, 0, false, "")
	legacySignupRow(t, env, activityID, viewer.ID)
	legacySignupRow(t, env, activityID, other.ID)

	resp := client.get("/legacy/api/activities/" + activityNo + "/signups")
	var payload struct {
		Visible bool `json:"visible"`
		Count   int  `json:"count"`
		Signups []struct {
			Nickname string `json:"nickname"`
		} `json:"signups"`
	}
	profileDecode(t, resp, &payload)
	if resp.StatusCode != http.StatusOK || payload.Visible || payload.Count != 2 ||
		len(payload.Signups) != 0 {
		t.Fatalf("count-only legacy roster = %+v status=%d", payload, resp.StatusCode)
	}
}
