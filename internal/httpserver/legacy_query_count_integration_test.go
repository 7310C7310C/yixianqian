package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationLegacyQueryBudget pins the batch-7 SQL budgets on the two hot
// aggregates. The count includes session lookup/touch and the ID gate.
func TestIntegrationLegacyQueryBudget(t *testing.T) {
	tracer := &countingTracer{}
	env := setupActivitiesHTTPEnvWithOptions(t, nil, db.WithQueryTracer(tracer))

	viewer, client := env.createUser(t, "U-QBUDGET", "预算视角", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, nickname = '预算本人', city = '上海', native_place = '江苏南京', church_location = '上海浦东' WHERE id = $1`,
		viewer.ID, db.GenderMale); err != nil {
		t.Fatalf("update viewer: %v", err)
	}
	ids := []int64{viewer.ID}
	for i := 0; i < 3; i++ {
		candidate, _ := env.createUser(t, "U-QCAND", "候选预算", "active", "user")
		ids = append(ids, candidate.ID)
		if _, err := env.pool.Exec(env.ctx,
			`UPDATE users SET gender = $2, nickname = $3 WHERE id = $1`,
			candidate.ID, db.GenderFemale, "预算候选"+randomHTTPDigits(t, 4)); err != nil {
			t.Fatalf("update candidate: %v", err)
		}
		attachTestPhoto(t, env, candidate.ID)
	}
	cleanupHTTPUsers(t, env, ids...)

	// Populate social overlays so the merged state query has rows to decode.
	candidateID := ids[1]
	now := time.Now()
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, $4, $5)`,
		viewer.ID, candidateID, db.LikeStatusSingle, db.LikeTypeAnon, db.MonthKey(now)); err != nil {
		t.Fatalf("insert like: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO praises (initiator_id, target_id, day, week, status)
		 VALUES ($1, $2, $3, $4, $5)`,
		viewer.ID, candidateID, db.DayKey(now), db.WeekKey(now), db.PraiseStatusActive); err != nil {
		t.Fatalf("insert praise: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO messages (target_user_id, author_id, content) VALUES ($1, $2, '预算测试留言')`,
		candidateID, viewer.ID); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	tracer.queries.Store(0)
	resp := client.get("/legacy/api/home")
	homeQueries := tracer.queries.Load()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("home status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home legacyHomePayload
	legacyJSONDecode(t, resp, &home)
	if len(home.Cards) < 2 || !home.Cards[0].IsSelf {
		t.Fatalf("home cards=%+v, want self first plus candidates", home.Cards)
	}

	tracer.queries.Store(0)
	resp = client.get("/legacy/api/cards?limit=20")
	cardsQueries := tracer.queries.Load()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cards status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var cards struct {
		Cards []legacyHomeCard `json:"cards"`
		Total int              `json:"total"`
	}
	legacyJSONDecode(t, resp, &cards)
	if len(cards.Cards) < 3 {
		t.Fatalf("cards len=%d, want >=3", len(cards.Cards))
	}

	if homeQueries > 16 {
		t.Fatalf("home SQL = %d, want <=16", homeQueries)
	}
	if cardsQueries > 8 {
		t.Fatalf("cards SQL = %d, want <=8", cardsQueries)
	}
	t.Logf("query budget: home=%d cards=%d", homeQueries, cardsQueries)
}
