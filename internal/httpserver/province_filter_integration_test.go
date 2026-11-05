package httpserver

import (
	"net/http"
	"net/url"
	"testing"
)

// TestIntegrationCardsProvinceFilter covers the full HTTP path: repeated
// city_province/native_province params are expanded to canonical and legacy
// city spellings, combined with AND, and never match another province.
func TestIntegrationCardsProvinceFilter(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	viewer, viewerClient := env.createUser(t, "U-PROVF", "省份筛选本人", "active", "user")
	canonical, _ := env.createUser(t, "U-PROVA", "省份筛选A", "active", "user")
	legacy, _ := env.createUser(t, "U-PROVB", "省份筛选B", "active", "user")
	other, _ := env.createUser(t, "U-PROVC", "省份筛选C", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, canonical.ID, legacy.ID, other.ID)

	prefix := "省份筛选" + randomHTTPDigits(t, 8)
	update := func(id int64, gender, nickname, city, native, church string) {
		t.Helper()
		if _, err := env.pool.Exec(env.ctx,
			`UPDATE users SET gender = $2, nickname = $3, city = $4, native_place = $5,
			        church_location = $6 WHERE id = $1`,
			id, gender, nickname, city, native, church); err != nil {
			t.Fatalf("update user %d: %v", id, err)
		}
	}
	update(viewer.ID, "男性", prefix+"-本人", "上海", "江苏南京", "上海浦东")
	update(canonical.ID, "女性", prefix+"-A", "广东深圳", "广东汕头", "深圳南山")
	update(legacy.ID, "女性", prefix+"-B", "深圳", "汕头", "深圳福田")
	update(other.ID, "女性", prefix+"-C", "浙江杭州", "浙江宁波", "杭州上城")
	attachTestPhoto(t, env, canonical.ID)
	attachTestPhoto(t, env, legacy.ID)
	attachTestPhoto(t, env, other.ID)

	cardsQuery := func(extra string) []string {
		t.Helper()
		path := "/legacy/api/cards?limit=50&nickname=" + url.QueryEscape(prefix) + extra
		resp := viewerClient.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cards status = %d body=%s", resp.StatusCode, readBody(t, resp))
		}
		var payload legacyHomePayload
		legacyJSONDecode(t, resp, &payload)
		names := make([]string, 0, len(payload.Cards))
		for _, card := range payload.Cards {
			names = append(names, card.Nickname)
		}
		return names
	}

	names := cardsQuery("&city_province=44")
	if len(names) != 2 {
		t.Fatalf("city province 44 = %v, want A+B", names)
	}
	for _, name := range names {
		if name != prefix+"-A" && name != prefix+"-B" {
			t.Fatalf("city province 44 leaked %q", name)
		}
	}

	names = cardsQuery("&native_province=44")
	if len(names) != 2 {
		t.Fatalf("native province 44 = %v, want A+B", names)
	}
	for _, name := range names {
		if name != prefix+"-A" && name != prefix+"-B" {
			t.Fatalf("native province 44 leaked %q", name)
		}
	}

	names = cardsQuery("&city_province=44&native_province=33")
	if len(names) != 0 {
		t.Fatalf("AND across provinces = %v, want empty", names)
	}

	names = cardsQuery("&city_province=44&native_province=44")
	if len(names) != 2 {
		t.Fatalf("AND same province = %v, want A+B", names)
	}
}
