package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationRecommendationAdminPage 覆盖管理端只读名单、权限、CSRF 与手动重跑。
func TestIntegrationRecommendationAdminPage(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	admin, adminClient := env.createUser(t, "U-RECADM", "推荐管理员", "active", "user")
	plain, plainClient := env.createUser(t, "U-RECADM-PLAIN", "普通用户", "active", "user")
	cleanupHTTPUsers(t, env, admin.ID, plain.ID)
	assignGroupingAdmin(t, env, admin.ID)

	anon := newWSClient(t, env.server.URL)
	if resp := anon.get("/admin/matching"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous /admin/matching = %d, want 303", resp.StatusCode)
	}
	if resp := plainClient.get("/admin/matching"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain /admin/matching = %d, want 403", resp.StatusCode)
	}
	resp := adminClient.get("/admin/matching")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin /admin/matching = %d body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{"本周推荐位", "重新生成本周名单", "必显原因只存服务端"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Fatalf("admin page missing %q", want)
		}
	}

	if resp := adminClient.postForm("/admin/matching/recommend/run",
		url.Values{"csrf_token": {"wrong-csrf"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf status = %d, want 403", resp.StatusCode)
	}

	csrf := adminClient.pageCSRF("/admin/matching")
	resp = adminClient.postForm("/admin/matching/recommend/run", url.Values{"csrf_token": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("rerun status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/admin/matching?notice=recommend" {
		t.Fatalf("rerun location = %q", loc)
	}
	var audits int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log WHERE actor_user_id = $1 AND action = 'admin.recommend.run'`,
		admin.ID).Scan(&audits); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Fatalf("recommend run audits = %d, want 1", audits)
	}
	resp = adminClient.get("/admin/matching?notice=recommend")
	body = readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("本周推荐名单已重新生成")) {
		t.Fatalf("notice missing: %s", body)
	}
}

type recommendCard struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	Recommended bool   `json:"recommended"`
}

// TestIntegrationWeeklyRecommendationsOnCards 验证本周推荐对象被置顶并带
// recommended=true（必显在前），且旧版 cards API 已暴露该字段。
func TestIntegrationWeeklyRecommendationsOnCards(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	if _, err := env.pool.Exec(env.ctx, `DELETE FROM weekly_recommendations`); err != nil {
		t.Fatalf("clear weekly recommendations: %v", err)
	}

	viewer, viewerClient := env.createUser(t, "U-RECVIEW", "推荐浏览者", "active", "user")
	liker, _ := env.createUser(t, "U-RECLIKER", "暗恋者", "active", "user")
	other, _ := env.createUser(t, "U-RECOTHER", "陪跑者", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, liker.ID, other.ID)
	setHTTPGender(t, env, viewer.ID, db.GenderMale)
	setHTTPGender(t, env, liker.ID, db.GenderFemale)
	setHTTPGender(t, env, other.ID, db.GenderFemale)
	attachTestPhoto(t, env, liker.ID)
	attachTestPhoto(t, env, other.ID)

	if _, err := env.pool.Exec(env.ctx, `
		INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		VALUES ($1, $2, $3, $4, $5)`,
		liker.ID, viewer.ID, db.LikeStatusSingle, db.LikeTypeAnon, db.MonthKey(db.SocialNow())); err != nil {
		t.Fatalf("insert anonymous like: %v", err)
	}

	result, err := env.database.GenerateWeeklyRecommendations(env.ctx, db.SocialNow())
	if err != nil {
		t.Fatalf("generate recommendations: %v", err)
	}
	if result.Viewers == 0 || result.Entries == 0 {
		t.Fatalf("generation result = %+v", result)
	}

	resp := viewerClient.get("/legacy/api/cards")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cards status = %d body=%s", resp.StatusCode, raw)
	}
	var payload struct {
		Cards []recommendCard `json:"cards"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode cards: %v body=%s", err, raw)
	}
	if len(payload.Cards) < 2 {
		t.Fatalf("cards = %d, want >= 2 body=%s", len(payload.Cards), raw)
	}
	likerLegacy := ""
	if liker.LegacyUserID != nil {
		likerLegacy = *liker.LegacyUserID
	}
	if payload.Cards[0].UserID != likerLegacy || !payload.Cards[0].Recommended {
		t.Fatalf("first card = %+v, want pinned liker %s with recommended=true",
			payload.Cards[0], likerLegacy)
	}
	// 推荐位整体置顶：recommended=true 必须是开头连续的一段。
	seenPlain := false
	for _, card := range payload.Cards {
		if card.Recommended {
			if seenPlain {
				t.Fatalf("recommended card appears after plain cards: %+v", card)
			}
			continue
		}
		seenPlain = true
	}
	for i, card := range payload.Cards {
		if i > 0 && card.UserID == likerLegacy {
			t.Fatalf("pinned liker duplicated later in deck at %d", i)
		}
	}

	// 推荐位不能泄漏必显原因：接口只给布尔值，不给 pinned/暗恋字段。
	for _, key := range []string{"pinned_by", "pinned_by_user_id", "liked_me", "initiator_id"} {
		if bytes.Contains([]byte(raw), []byte(key)) {
			t.Fatalf("cards payload leaks %q: %s", key, raw)
		}
	}

	// SPA 角标已接线：字段存在时卡片渲染「推荐」。
	spa, err := os.ReadFile("../../web/static/legacy/index.html")
	if err != nil {
		t.Fatalf("read SPA: %v", err)
	}
	if !bytes.Contains(spa, []byte("currentCard.recommended")) {
		t.Fatal("SPA card does not render the recommended badge")
	}
}
