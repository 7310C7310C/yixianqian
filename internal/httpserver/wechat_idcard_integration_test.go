package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func setUserIDCardInvalid(t *testing.T, e *profileHTTPEnv, userID int64, birthday string) {
	t.Helper()
	if _, err := e.pool.Exec(e.ctx,
		`UPDATE users SET birthday = $2::date, id_card_valid = false,
		        id_card_ciphertext = NULL, id_card_last4 = '', id_card_hash = NULL,
		        gender = CASE WHEN gender = '' THEN '男性' ELSE gender END
		  WHERE id = $1`, userID, birthday); err != nil {
		t.Fatalf("set invalid id card: %v", err)
	}
}

func putJSON(t *testing.T, client *wsClient, path, csrf string, payload any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, client.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return client.do(req)
}

func TestIntegrationIDCardGateAndBackfill(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	user, client := env.createUser(t, "U-IDGATE", "门禁用户", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)
	setUserIDCardInvalid(t, env, user.ID, "1990-01-01")
	csrf := client.csrfCookie()

	assertGate := func(path, method string) {
		t.Helper()
		var resp *http.Response
		if method == http.MethodPost {
			resp = client.postJSON(path, csrf, map[string]any{"target_id": 1})
		} else {
			resp = client.get(path)
		}
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "id_card_required") {
			t.Fatalf("%s status=%d body=%s", path, resp.StatusCode, body)
		}
	}
	assertGate("/legacy/api/cards", http.MethodGet)
	assertGate("/legacy/api/like", http.MethodPost)
	assertGate("/legacy/api/activities/1/signup", http.MethodPost)

	for _, path := range []string{"/legacy/api/home", "/legacy/api/profile", "/legacy/api/notifications"} {
		resp := client.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, resp.StatusCode, readBody(t, resp))
		}
	}
	// Home stays reachable for the SPA shell, but an unverified account must
	// not receive cards/likes/activities through it.
	resp := client.get("/legacy/api/home")
	var home struct {
		User struct {
			IDValid bool `json:"id_valid"`
		} `json:"user"`
		Cards      []any `json:"cards"`
		Activities []any `json:"activities"`
	}
	legacyJSONDecode(t, resp, &home)
	if home.User.IDValid || len(home.Cards) != 0 || len(home.Activities) != 0 {
		t.Fatalf("invalid home leaked data: %+v", home)
	}
	if resp := client.get("/app"); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(readBody(t, resp), "需要补填身份证") {
		t.Fatalf("/app status=%d", resp.StatusCode)
	}

	// Wrong checksum, birthday mismatch and a valid backfill.
	valid := makeTestIDCard(t, "19900101", "002")
	badChecksum := valid[:17] + "0"
	if badChecksum == valid {
		badChecksum = valid[:17] + "1"
	}
	mismatch := makeTestIDCard(t, "19920202", "003")

	for _, tt := range []struct {
		name string
		id   string
		want string
	}{
		{"checksum", badChecksum, "invalid_id_card"},
		{"birthday", mismatch, "id_card_birthday_mismatch"},
	} {
		resp := putJSON(t, client, "/legacy/api/profile", csrf, map[string]any{"身份证号": tt.id})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, tt.want) {
			t.Fatalf("%s status=%d body=%s", tt.name, resp.StatusCode, body)
		}
	}

	resp = putJSON(t, client, "/legacy/api/profile", csrf, map[string]any{"身份证号": valid})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backfill status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, valid) {
		t.Fatal("backfill response leaked the full id card")
	}
	var sealed []byte
	var last4, hash string
	var idValid bool
	if err := env.pool.QueryRow(env.ctx,
		`SELECT id_card_ciphertext, id_card_last4, id_card_hash, id_card_valid
		   FROM users WHERE id = $1`, user.ID).Scan(&sealed, &last4, &hash, &idValid); err != nil {
		t.Fatal(err)
	}
	if !idValid || last4 != valid[len(valid)-4:] || hash == "" {
		t.Fatalf("backfill state valid=%v last4=%q hash=%q", idValid, last4, hash)
	}
	if bytes.Contains(sealed, []byte(valid)) {
		t.Fatal("full id card stored in plaintext")
	}
	if opened, err := openIDCard(env.cfg.SessionSecret, sealed); err != nil || opened != valid {
		t.Fatalf("open id = %q err=%v", opened, err)
	}
	var auditPayload string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT metadata::text FROM audit_log
		  WHERE action = 'profile.id_card.backfilled' AND target_id = $1
		  ORDER BY id DESC LIMIT 1`, fmt.Sprint(user.ID)).Scan(&auditPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditPayload, valid) || !strings.Contains(auditPayload, last4) {
		t.Fatalf("audit payload = %s", auditPayload)
	}
	if resp := client.get("/legacy/api/cards"); resp.StatusCode != http.StatusOK {
		t.Fatalf("cards after backfill status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestIntegrationIDCardBackfillUniqueness(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	first, firstClient := env.createUser(t, "U-IDUNIQ1", "唯一甲", "active", "user")
	second, secondClient := env.createUser(t, "U-IDUNIQ2", "唯一乙", "active", "user")
	cleanupHTTPUsers(t, env, first.ID, second.ID)
	setUserIDCardInvalid(t, env, first.ID, "1990-01-01")
	setUserIDCardInvalid(t, env, second.ID, "1990-01-01")
	id := makeTestIDCard(t, "19900101", "002")

	resp := putJSON(t, firstClient, "/legacy/api/profile", firstClient.csrfCookie(), map[string]any{"身份证号": id})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first backfill status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = putJSON(t, secondClient, "/legacy/api/profile", secondClient.csrfCookie(), map[string]any{"身份证号": id})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "id_card_taken") {
		t.Fatalf("duplicate status=%d body=%s", resp.StatusCode, body)
	}
}

func TestIntegrationObserverStillBlockedByIDCard(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	observer, client := env.createUser(t, "U-IDOBS", "观察员门禁", "observer", "observer")
	cleanupHTTPUsers(t, env, observer.ID)
	setUserIDCardInvalid(t, env, observer.ID, "1990-01-01")

	resp := client.get("/legacy/api/cards")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "id_card_required") {
		t.Fatalf("observer cards status=%d body=%s", resp.StatusCode, body)
	}
	resp = client.get("/legacy/api/profile")
	var profile struct {
		HasFace bool `json:"has_face"`
		IDValid bool `json:"id_valid"`
	}
	legacyJSONDecode(t, resp, &profile)
	if !profile.HasFace || profile.IDValid {
		t.Fatalf("observer face/id projection = %+v", profile)
	}
}

func TestIntegrationMutualWechatExchange(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	male, maleClient := env.createUser(t, "U-WX-M", "微信男", "active", "user")
	female, femaleClient := env.createUser(t, "U-WX-F", "微信女", "active", "user")
	cleanupHTTPUsers(t, env, male.ID, female.ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = $3, contact_consent = $4 WHERE id = $1`,
		male.ID, db.GenderMale, "wx-male", true); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = $3, contact_consent = $4 WHERE id = $1`,
		female.ID, db.GenderFemale, "wx-female-old", false); err != nil {
		t.Fatal(err)
	}
	const message = "很高兴认识你，希望有机会聊聊。"
	if _, err := env.database.CreateLike(env.ctx, db.CreateLikeParams{
		InitiatorID: male.ID, TargetID: female.ID, LikeType: db.LikeTypeReal,
		Message: message, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateLike(env.ctx, db.CreateLikeParams{
		InitiatorID: female.ID, TargetID: male.ID, LikeType: db.LikeTypeAnon,
		Message: message, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	type mutualPayload struct {
		Mutual []struct {
			Nickname       string `json:"nickname"`
			TheirWechat    string `json:"their_wechat"`
			MyWechat       string `json:"my_wechat"`
			WechatRequired bool   `json:"wechat_required"`
			WechatStatus   string `json:"wechat_status"`
			WaitingOther   bool   `json:"waiting_other"`
		} `json:"mutual"`
	}
	load := func(client *wsClient) mutualPayload {
		t.Helper()
		resp := client.get("/legacy/api/likes/me")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("likes/me status=%d body=%s", resp.StatusCode, readBody(t, resp))
		}
		var payload mutualPayload
		legacyJSONDecode(t, resp, &payload)
		if len(payload.Mutual) != 1 {
			t.Fatalf("mutual = %+v", payload.Mutual)
		}
		return payload
	}

	// Female has not consented: neither side may see the other's wechat.
	if got := load(maleClient).Mutual[0]; got.TheirWechat != "" || !got.WechatRequired ||
		got.WechatStatus != "waiting_other" || got.WaitingOther != true {
		t.Fatalf("male before consent = %+v", got)
	}
	if got := load(femaleClient).Mutual[0]; got.TheirWechat != "" || !got.WechatRequired || got.WechatStatus != "need_consent" {
		t.Fatalf("female before consent = %+v", got)
	}

	// Female fills a new wechat and agrees through the mutual-page endpoint.
	resp := femaleClient.postJSON("/legacy/api/mutual/wechat", femaleClient.csrfCookie(), map[string]any{
		"wechat": "wx-female-new", "agree": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("consent status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	maleView := load(maleClient).Mutual[0]
	femaleView := load(femaleClient).Mutual[0]
	if maleView.TheirWechat != "wx-female-new" || maleView.WechatStatus != "ready" {
		t.Fatalf("male after consent = %+v", maleView)
	}
	if femaleView.TheirWechat != "wx-male" || femaleView.WechatStatus != "ready" {
		t.Fatalf("female after consent = %+v", femaleView)
	}

	// Each side gets exactly one notification containing the other's wechat.
	checkNotification := func(userID int64, otherWechat string) {
		t.Helper()
		var count int
		var body string
		if err := env.pool.QueryRow(env.ctx,
			`SELECT count(*), COALESCE(max(body), '') FROM notifications
			  WHERE user_id = $1 AND kind = 'wechat_exchange'`, userID).Scan(&count, &body); err != nil {
			t.Fatal(err)
		}
		if count != 1 || !strings.Contains(body, otherWechat) {
			t.Fatalf("user %d notifications count=%d body=%q", userID, count, body)
		}
	}
	checkNotification(male.ID, "wx-female-new")
	checkNotification(female.ID, "wx-male")

	// Repeated reads must not duplicate the exchange notification.
	_ = load(maleClient)
	_ = load(femaleClient)
	var total int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM notifications WHERE kind = 'wechat_exchange' AND user_id = ANY($1)`,
		[]int64{male.ID, female.ID}).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("wechat notifications = %d, want 2", total)
	}
}

func TestIntegrationMutualWechatDoesNotLeakAnonymousLike(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	male, maleClient := env.createUser(t, "U-WX-ANON-M", "匿名女用户", "active", "user")
	female, _ := env.createUser(t, "U-WX-ANON-F", "匿名男用户", "active", "user")
	cleanupHTTPUsers(t, env, male.ID, female.ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = 'wx-hidden-anon', contact_consent = false WHERE id = $1`,
		female.ID, db.GenderFemale); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = 'wx-male-anon', contact_consent = true WHERE id = $1`,
		male.ID, db.GenderMale); err != nil {
		t.Fatal(err)
	}
	// Only an incoming anonymous like: not mutual, so no contact fields exist.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := env.database.CreateLike(ctx, db.CreateLikeParams{
		InitiatorID: female.ID, TargetID: male.ID, LikeType: db.LikeTypeAnon,
		Message: "匿名喜欢不应该出现在相互喜欢里。", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	resp := maleClient.get("/legacy/api/likes/me")
	body := readBody(t, resp)
	if strings.Contains(body, "wx-hidden-anon") || strings.Contains(body, "匿名女用户") {
		t.Fatalf("anonymous like leaked identity: %s", body)
	}
}
