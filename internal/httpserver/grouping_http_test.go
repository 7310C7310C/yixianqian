package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func createHTTPGroupSelection(t *testing.T, env *profileHTTPEnv, activityID, userID int64,
	gender string, choices []int64) {
	t.Helper()
	encoded, err := json.Marshal(choices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO group_selections (activity_id, selector_id, gender, choices, round)
		 VALUES ($1, $2, $3, $4, 1)
		 ON CONFLICT (activity_id, selector_id) DO UPDATE
		   SET gender = EXCLUDED.gender, choices = EXCLUDED.choices, updated_at = now()`,
		activityID, userID, gender, encoded); err != nil {
		t.Fatalf("insert group selection: %v", err)
	}
}

func setHTTPGender(t *testing.T, env *profileHTTPEnv, userID int64, gender string) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, nickname = COALESCE(NULLIF(nickname, ''), $3) WHERE id = $1`,
		userID, gender, gender); err != nil {
		t.Fatalf("set gender: %v", err)
	}
}

func assignGroupingAdmin(t *testing.T, env *profileHTTPEnv, userID int64) {
	t.Helper()
	changed, err := env.database.AssignAdminRole(env.ctx, userID, "activity_manager", true)
	if err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
}

func TestIntegrationGroupingAdminHTTPFlow(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	admin, adminClient := env.createUser(t, "U-GRPADM", "活动管理员", "active", "user")
	plain, plainClient := env.createUser(t, "U-GRPADM-PLAIN", "普通用户", "active", "user")
	cleanupHTTPUsers(t, env, admin.ID, plain.ID)
	assignGroupingAdmin(t, env, admin.ID)
	activityID, activityNo := createHTTPActivity(t, env, 0, false, "")

	var males, females []db.User
	for i := 0; i < 2; i++ {
		male, _ := env.createUser(t, fmt.Sprintf("U-GRPADM-M%d", i), fmt.Sprintf("男%d", i), "active", "user")
		female, _ := env.createUser(t, fmt.Sprintf("U-GRPADM-F%d", i), fmt.Sprintf("女%d", i), "active", "user")
		males = append(males, male)
		females = append(females, female)
		cleanupHTTPUsers(t, env, male.ID, female.ID)
		setHTTPGender(t, env, male.ID, "男性")
		setHTTPGender(t, env, female.ID, "女性")
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO signups (activity_id, user_id, status, attendance)
			 VALUES ($1, $2, '已报名', '按时')`, activityID, male.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO signups (activity_id, user_id, status, attendance)
			 VALUES ($1, $2, '已报名', '按时')`, activityID, female.ID); err != nil {
			t.Fatal(err)
		}
	}
	maleIDs := []int64{males[0].ID, males[1].ID}
	femaleIDs := []int64{females[0].ID, females[1].ID}
	for _, male := range males {
		createHTTPGroupSelection(t, env, activityID, male.ID, "男性", femaleIDs)
	}
	for _, female := range females {
		createHTTPGroupSelection(t, env, activityID, female.ID, "女性", maleIDs)
	}
	// Participants need sessions for the user result endpoint.
	_, maleClient := loginGroupingUser(t, env, males[0])
	_, femaleClient := loginGroupingUser(t, env, females[0])

	resp := plainClient.get("/api/admin/group/preview?activity_id=" + url.QueryEscape(activityNo) +
		"&male_per_group=1&female_per_group=1")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain preview status = %d", resp.StatusCode)
	}
	resp = adminClient.get("/api/admin/group/preview?activity_id=" + url.QueryEscape(activityNo) +
		"&male_per_group=1&female_per_group=1")
	var preview GroupPreviewView
	profileDecode(t, resp, &preview)
	if resp.StatusCode != http.StatusOK || !preview.CanExecute || preview.ExpectedGroups != 2 {
		t.Fatalf("preview = %+v status=%d", preview, resp.StatusCode)
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/api/admin/group/run", "",
		map[string]any{"activity_id": activityNo, "round": 1, "male_per_group": 1, "female_per_group": 1})
	if resp.StatusCode != http.StatusForbidden || profileAPIErrorCode(t, resp) != "csrf_failed" {
		t.Fatalf("run without csrf status = %d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/api/admin/group/run",
		adminClient.csrfCookie(), map[string]any{
			"activity_id": activityNo, "round": 1, "male_per_group": 1, "female_per_group": 1,
		})
	var runPayload struct {
		OK     bool         `json:"ok"`
		Result GroupRunView `json:"result"`
	}
	profileDecode(t, resp, &runPayload)
	if resp.StatusCode != http.StatusOK || !runPayload.OK ||
		runPayload.Result.Round != 1 || runPayload.Result.GroupCount != 2 {
		t.Fatalf("run = %+v status=%d", runPayload, resp.StatusCode)
	}
	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/api/admin/group/run",
		adminClient.csrfCookie(), map[string]any{
			"activity_id": activityNo, "round": 1, "male_per_group": 1, "female_per_group": 1,
		})
	if resp.StatusCode != http.StatusConflict || profileAPIErrorCode(t, resp) != "group_already_done" {
		t.Fatalf("duplicate run status = %d", resp.StatusCode)
	}

	resp = adminClient.get("/api/admin/group/latest_round?activity_id=" + url.QueryEscape(activityNo))
	var latest struct {
		Round int `json:"round"`
	}
	profileDecode(t, resp, &latest)
	if resp.StatusCode != http.StatusOK || latest.Round != 1 {
		t.Fatalf("latest round = %+v status=%d", latest, resp.StatusCode)
	}
	resp = adminClient.get("/api/admin/group/result?activity_id=" + url.QueryEscape(activityNo))
	var adminResult AdminGroupResultView
	profileDecode(t, resp, &adminResult)
	if resp.StatusCode != http.StatusOK || len(adminResult.Groups) != 2 || adminResult.Round != 1 {
		t.Fatalf("admin result = %+v status=%d", adminResult, resp.StatusCode)
	}

	// Each participant sees only their own group.
	groupByUser := map[int64]int{}
	for _, group := range adminResult.Groups {
		for _, member := range group.Members {
			groupByUser[member.ID] = group.GroupID
		}
	}
	for _, client := range []*wsClient{maleClient, femaleClient} {
		resp = client.get("/api/activities/" + activityNo + "/group/result")
		var result GroupResultView
		profileDecode(t, resp, &result)
		if resp.StatusCode != http.StatusOK || result.MyGroup == 0 || result.TotalGroups != 2 {
			t.Fatalf("user result = %+v status=%d", result, resp.StatusCode)
		}
		if len(result.Members) != 2 {
			t.Fatalf("user result members = %+v", result.Members)
		}
		if groupByUser[result.Members[0].ID] != result.MyGroup {
			t.Fatalf("user result leaked another group: %+v", result)
		}
	}

	resp = profileJSONRequest(t, adminClient, http.MethodPost, "/api/admin/group/reset",
		adminClient.csrfCookie(), map[string]any{"activity_id": activityNo})
	var reset struct {
		OK      bool  `json:"ok"`
		Cleared int64 `json:"cleared_results"`
	}
	profileDecode(t, resp, &reset)
	if resp.StatusCode != http.StatusOK || !reset.OK || reset.Cleared != 4 {
		t.Fatalf("reset = %+v status=%d", reset, resp.StatusCode)
	}
	resp = adminClient.get("/api/admin/group/result?activity_id=" + url.QueryEscape(activityNo))
	var clearedResult AdminGroupResultView
	profileDecode(t, resp, &clearedResult)
	if clearedResult.Round != 0 || len(clearedResult.Groups) != 0 {
		t.Fatalf("result after reset = %+v", clearedResult)
	}
	resp = femaleClient.get("/api/activities/" + activityNo + "/group/result")
	if resp.StatusCode != http.StatusConflict || activityAPIErrorCode(t, resp) != "group_not_complete" {
		t.Fatalf("result after reset status = %d", resp.StatusCode)
	}

	// Sign-in gate is enforced at the API boundary too.
	signinID, signinNo := createHTTPActivity(t, env, 0, true, "4321")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, status, attendance)
		 VALUES ($1, $2, '已报名', '')`, signinID, females[0].ID); err != nil {
		t.Fatal(err)
	}
	resp = femaleClient.get("/api/activities/" + signinNo + "/group/candidates")
	if resp.StatusCode != http.StatusForbidden || activityAPIErrorCode(t, resp) != "signin_required" {
		t.Fatalf("signin gate candidates status = %d", resp.StatusCode)
	}
}

// loginGroupingUser attaches a fresh session for a user created through the
// database directly (participants in the admin flow).
func loginGroupingUser(t *testing.T, env *profileHTTPEnv, user db.User) (db.User, *wsClient) {
	t.Helper()
	client := newWSClient(t, env.server.URL)
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateSession(env.ctx, user.ID, hash, time.Hour, "", "grouping-test"); err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.jar.SetCookies(base, []*http.Cookie{{
		Name:  env.cfg.SessionCookieName,
		Value: raw,
		Path:  "/",
	}})
	resp := client.get("/welcome")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prime grouping client: status = %d", resp.StatusCode)
	}
	return user, client
}
