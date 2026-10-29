package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRegionInvalidValues(t *testing.T) {
	cases := []struct {
		city, church string
		want         bool
	}{
		{"", "", false}, // legacy rows may omit both
		{"深圳", "", false},
		{"广东深圳", "深圳南山区", false},
		{"北京", "北京东城", false},
		{"当前记录", "", true},
		{"1", "", true},
		{"", "广东", true},
		{"深圳", "潮州", true},
	}
	for _, tc := range cases {
		if got := regionInvalid(tc.city, tc.church); got != tc.want {
			t.Errorf("regionInvalid(%q,%q) = %v, want %v", tc.city, tc.church, got, tc.want)
		}
	}
}

func TestIntegrationRegionGateAndProfileFix(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	user := createMessageUser(t, ctx, database, pool, "城市门禁用户")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE users SET city = '当前记录', church_location = '广东', gender = '男性' WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	client := loginMessageClient(t, database, server, user)

	resp := client.get("/legacy/api/cards")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cards status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var gateErr struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gateErr); err != nil {
		t.Fatalf("decode gate error: %v", err)
	}
	if gateErr.Code != "region_required" || gateErr.Error == "" {
		t.Fatalf("cards gate = %+v, want region_required", gateErr)
	}
	// Profile (and the other ID-gate allowlist surfaces) must stay reachable.
	if resp := client.get("/legacy/api/profile"); resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	csrf := client.pageCSRF("/register/done")
	// Invalid values are rejected instead of being silently stored.
	resp = profileJSONRequest(t, client, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"现居/工作城市": "不存在市"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid city status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = profileJSONRequest(t, client, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"教堂所在市-区": "广东"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid church status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = profileJSONRequest(t, client, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"现居/工作城市": "深圳", "教堂所在市-区": "深圳南山区"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile fix status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var city, church string
	if err := pool.QueryRow(ctx,
		`SELECT city, church_location FROM users WHERE id = $1`, user.ID).Scan(&city, &church); err != nil {
		t.Fatal(err)
	}
	if city != "广东深圳" || church != "深圳南山" {
		t.Fatalf("normalized city=%q church=%q", city, church)
	}
	resp = client.get("/legacy/api/cards")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cards after fix status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	// Observers only have the ID-card field in their editable schema; they must
	// not be trapped by a legacy city value they cannot change.
	observer := createMessageUser(t, ctx, database, pool, "城市门禁观察员")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, observer.ID)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE users SET account_kind = 'observer', account_status = 'observer', city = '当前记录' WHERE id = $1`,
		observer.ID); err != nil {
		t.Fatal(err)
	}
	observerClient := loginMessageClient(t, database, server, observer)
	resp = observerClient.get("/legacy/api/home")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("observer home status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home struct {
		User struct {
			RegionInvalid bool `json:"region_invalid"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&home); err != nil {
		t.Fatal(err)
	}
	if home.User.RegionInvalid {
		t.Fatal("observer home must not advertise region_invalid")
	}
	resp = observerClient.get("/legacy/api/cards")
	if resp.StatusCode == http.StatusForbidden && strings.Contains(readBody(t, resp), "region_required") {
		t.Fatal("observer must be exempt from the region gate")
	}
}
