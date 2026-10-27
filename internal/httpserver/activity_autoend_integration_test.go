package httpserver

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func findActivityPayload(t *testing.T, items []map[string]any, activityNo string) map[string]any {
	t.Helper()
	for _, item := range items {
		if item["activity_id"] == activityNo {
			return item
		}
	}
	t.Fatalf("activity %s missing from payload: %+v", activityNo, items)
	return nil
}

func setActivityWindow(t *testing.T, env *profileHTTPEnv, activityID int64, startAt, endAt time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE activities SET start_at = $2, end_at = $3 WHERE id = $1`,
		activityID, startAt, endAt); err != nil {
		t.Fatalf("set activity window: %v", err)
	}
}

// TestIntegrationActivityAutoEndUserViews covers the user-facing contract:
// expired open/full activities read back as 已结束 with no signup/self-signin,
// future activities stay unchanged, and can_self_signin follows the real
// self sign-in window.
func TestIntegrationActivityAutoEndUserViews(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	viewer, client := env.createUser(t, "U-AUTOEND", "活动用户", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID)

	endedID, endedNo := createHTTPActivity(t, env, 10, true, "0123")
	now := time.Now()
	setActivityWindow(t, env, endedID, now.Add(-3*time.Hour), now.Add(-time.Hour))

	futureID, futureNo := createHTTPActivity(t, env, 10, true, "0123")
	setActivityWindow(t, env, futureID, now.Add(time.Hour), now.Add(3*time.Hour))

	liveID, liveNo := createHTTPActivity(t, env, 10, true, "0123")
	setActivityWindow(t, env, liveID, now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status)
		 VALUES ($1, $2, '活动用户', '已报名')`, liveID, viewer.ID); err != nil {
		t.Fatalf("seed live signup: %v", err)
	}

	// List: expired activity is rendered as ended in the user payload.
	resp := client.get("/legacy/api/activities")
	var list struct {
		Activities []map[string]any `json:"activities"`
	}
	profileDecode(t, resp, &list)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	ended := findActivityPayload(t, list.Activities, endedNo)
	if ended["status"] != "已结束" || ended["can_signup"] != false || ended["can_self_signin"] != false {
		t.Fatalf("expired list item = %+v", ended)
	}
	future := findActivityPayload(t, list.Activities, futureNo)
	if future["status"] != "报名中" || future["can_signup"] != true || future["can_self_signin"] != false {
		t.Fatalf("future list item = %+v", future)
	}

	// Detail: same derivation, plus the self-signin capability for a live signup.
	resp = client.get("/legacy/api/activities/" + url.PathEscape(endedNo))
	var endedDetail map[string]any
	profileDecode(t, resp, &endedDetail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ended detail status = %d", resp.StatusCode)
	}
	if endedDetail["status"] != "已结束" || endedDetail["can_signup"] != false ||
		endedDetail["can_cancel"] != false || endedDetail["can_self_signin"] != false {
		t.Fatalf("expired detail = %+v", endedDetail)
	}

	resp = client.get("/legacy/api/activities/" + url.PathEscape(liveNo))
	var liveDetail map[string]any
	profileDecode(t, resp, &liveDetail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live detail status = %d", resp.StatusCode)
	}
	if liveDetail["status"] != "报名中" || liveDetail["my_signup"] != true ||
		liveDetail["can_self_signin"] != true {
		t.Fatalf("live detail = %+v", liveDetail)
	}

	// Once attendance is stored, the button capability turns off immediately.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE signups SET attendance = '按时' WHERE activity_id = $1 AND user_id = $2`,
		liveID, viewer.ID); err != nil {
		t.Fatalf("mark checked in: %v", err)
	}
	resp = client.get("/legacy/api/activities/" + url.PathEscape(liveNo))
	profileDecode(t, resp, &liveDetail)
	if liveDetail["can_self_signin"] != false {
		t.Fatalf("checked-in detail still allows self sign-in: %+v", liveDetail)
	}

}
