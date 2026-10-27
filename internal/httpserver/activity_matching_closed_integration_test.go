package httpserver

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// TestIntegrationActivityMatchingClosedAfterEnd covers the PM feedback:
// user-side grouping / one-to-one entries and result buttons disappear for an
// ended activity, and direct API calls return 409 instead of working or 500.
func TestIntegrationActivityMatchingClosedAfterEnd(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	viewer, client := env.createUser(t, "U-ENDGRP", "结束活动用户", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID)

	now := time.Now()
	endedID, endedNo := createHTTPActivity(t, env, 10, false, "")
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities
		    SET group_enabled = true, group_status = '已完成',
		        onetoone_enabled = true, onetoone_status = '已完成',
		        start_at = $2, end_at = $3
		  WHERE id = $1`, endedID, now.Add(-3*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("set ended activity: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status)
		 VALUES ($1, $2, '结束活动用户', '已报名')`, endedID, viewer.ID); err != nil {
		t.Fatalf("seed ended signup: %v", err)
	}

	futureID, futureNo := createHTTPActivity(t, env, 10, false, "")
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities
		    SET group_enabled = true, onetoone_enabled = true,
		        start_at = $2, end_at = $3
		  WHERE id = $1`, futureID, now.Add(time.Hour), now.Add(3*time.Hour)); err != nil {
		t.Fatalf("set future activity: %v", err)
	}

	// The card flags drive every user-side matching entry button.
	resp := client.get("/legacy/api/activities")
	var list struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &list)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	ended := findActivityPayload(t, list.Activities, endedNo)
	if ended["status"] != "已结束" || ended["group_flag"] != "否" ||
		ended["onetoone_flag"] != "否" || ended["can_signup"] != false {
		t.Fatalf("ended list item = %+v", ended)
	}
	future := findActivityPayload(t, list.Activities, futureNo)
	if future["status"] != "报名中" || future["group_flag"] != "是" ||
		future["onetoone_flag"] != "是" {
		t.Fatalf("future list item = %+v", future)
	}
	// User-side derivation must not touch the stored flags: admins still see
	// the ended activity's grouping / one-to-one panels.
	var storedGroup, storedOnetoone bool
	if err := env.pool.QueryRow(env.ctx,
		`SELECT group_enabled, onetoone_enabled FROM activities WHERE id = $1`,
		endedID).Scan(&storedGroup, &storedOnetoone); err != nil {
		t.Fatalf("reload ended activity flags: %v", err)
	}
	if !storedGroup || !storedOnetoone {
		t.Fatalf("stored flags changed: group=%v onetoone=%v", storedGroup, storedOnetoone)
	}

	resp = client.get("/legacy/api/activities/mine")
	var mine struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &mine)
	mineEnded := findActivityPayload(t, mine.Activities, endedNo)
	if mineEnded["group_flag"] != "否" || mineEnded["onetoone_flag"] != "否" {
		t.Fatalf("ended mine item = %+v", mineEnded)
	}
	resp = client.get("/legacy/api/activities/mine/groups")
	var groups struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &groups)
	if len(groups.Activities) != 0 {
		t.Fatalf("ended activity still listed in my groups: %+v", groups.Activities)
	}
	resp = client.get("/legacy/api/activities/mine/groups/flag")
	var flag struct {
		Has bool `json:"has_group_activity"`
	}
	profileDecode(t, resp, &flag)
	if flag.Has {
		t.Fatal("ended activity still reports has_group_activity")
	}

	base := "/legacy/api/activities/" + url.PathEscape(endedNo)
	for _, path := range []string{"/group/status", "/group/candidates", "/group/result", "/onetoone/result"} {
		resp = client.get(base + path)
		msg, code := legacyJSONError(t, resp)
		if resp.StatusCode != http.StatusConflict || code != "activity_ended" {
			t.Fatalf("%s status=%d code=%q msg=%q", path, resp.StatusCode, code, msg)
		}
	}

	// POST paths are gated before any write: seven resolvable refs still end in
	// the ended check, not in choice validation.
	refs := make([]string, 7)
	for i := range refs {
		refs[i] = strconv.FormatInt(viewer.ID, 10)
	}
	resp = profileJSONRequest(t, client, http.MethodPost, base+"/group/select",
		client.csrfCookie(), map[string]any{"choices": refs})
	if msg, code := legacyJSONError(t, resp); resp.StatusCode != http.StatusConflict || code != "activity_ended" {
		t.Fatalf("ended group select status=%d code=%q msg=%q", resp.StatusCode, code, msg)
	}
	resp = profileJSONRequest(t, client, http.MethodPost, base+"/onetoone/chat",
		client.csrfCookie(), map[string]any{
			"target_openid": strconv.FormatInt(viewer.ID, 10),
			"content":       "hello",
		})
	if msg, code := legacyJSONError(t, resp); resp.StatusCode != http.StatusConflict || code != "activity_ended" {
		t.Fatalf("ended onetoone chat status=%d code=%q msg=%q", resp.StatusCode, code, msg)
	}

	// A live activity is unaffected: grouping status is readable and one-to-one
	// remains enabled.
	resp = client.get("/legacy/api/activities/" + url.PathEscape(futureNo) + "/group/status")
	var status struct {
		GroupStatus string `json:"group_status"`
	}
	profileDecode(t, resp, &status)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("future group status = %d", resp.StatusCode)
	}
}
