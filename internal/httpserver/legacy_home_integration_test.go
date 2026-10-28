package httpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func attachTestPhoto(t *testing.T, env *profileHTTPEnv, userID int64) string {
	t.Helper()
	suffix := randomHTTPDigits(t, 12)
	mediaID := "m-home-" + suffix
	if _, err := env.database.CreateMedia(env.ctx, db.Media{
		ID:           mediaID,
		UserID:       userID,
		OriginalName: "photo.jpg",
		ObjectKey:    fmt.Sprintf("media/%d/%s.jpg", userID, mediaID),
		ThumbKey:     fmt.Sprintf("media/%d/%s_thumb.jpg", userID, mediaID),
		ContentType:  "image/jpeg",
		ByteSize:     1000,
		ThumbSize:    100,
		Width:        600,
		Height:       800,
	}); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, 0, true)`, userID, mediaID); err != nil {
		t.Fatalf("create profile photo: %v", err)
	}
	return mediaID
}

type legacyPhotoMeta struct {
	W int `json:"w"`
	H int `json:"h"`
}

type legacyHomeCard struct {
	OpenID     string            `json:"openid"`
	Nickname   string            `json:"nickname"`
	IsSelf     bool              `json:"is_self"`
	Liked      bool              `json:"liked"`
	Praised    bool              `json:"praised"`
	MsgCount   int               `json:"msg_count"`
	Faces      []*legacyFaceView `json:"faces"`
	Photos     []string          `json:"photos"`
	PhotosMeta []legacyPhotoMeta `json:"photos_meta"`
}

type legacyHomePayload struct {
	Cards      []legacyHomeCard `json:"cards"`
	CardsTotal int              `json:"cards_total"`
}

// TestIntegrationLegacyHomeSelfCardGenderMissing covers reviewer N2: a viewer
// without gender still gets the legacy home shape (self card + cards_total=1),
// while the candidate deck is empty. Batch7's merged query dropped the self
// card here, which was an unintended behaviour change.
func TestIntegrationLegacyHomeSelfCardGenderMissing(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	viewer, viewerClient := env.createUser(t, "U-HOMENG", "无性别", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID)

	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '', nickname = $2 WHERE id = $1`,
		viewer.ID, "无性别自卡"); err != nil {
		t.Fatalf("clear gender: %v", err)
	}

	resp := viewerClient.get("/legacy/api/home")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy home status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home legacyHomePayload
	legacyJSONDecode(t, resp, &home)
	if home.CardsTotal != 1 || len(home.Cards) != 1 {
		t.Fatalf("gender-missing home cards_total=%d cards=%d, want 1/1 (self only)",
			home.CardsTotal, len(home.Cards))
	}
	if !home.Cards[0].IsSelf {
		t.Fatalf("gender-missing home first card = %+v, want self", home.Cards[0])
	}
}

// TestIntegrationLegacyHomeSelfCard pins the v1 /api/home behaviour: the
// viewer's own card is the first card, cards_total counts it, while
// /legacy/api/cards keeps exposing only the candidate pool.
func TestIntegrationLegacyHomeSelfCard(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	viewer, viewerClient := env.createUser(t, "U-HOMESELF", "本人视角", "active", "user")
	candidate, _ := env.createUser(t, "U-HOMECAND", "候选用户", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, candidate.ID)

	prefix := "首页自卡" + randomHTTPDigits(t, 8)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '男性', nickname = $2, city = '上海' WHERE id = $1`,
		viewer.ID, prefix+"-本人"); err != nil {
		t.Fatalf("update viewer: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '女性', nickname = $2, city = '杭州' WHERE id = $1`,
		candidate.ID, prefix+"-候选"); err != nil {
		t.Fatalf("update candidate: %v", err)
	}
	mediaID := attachTestPhoto(t, env, candidate.ID)
	centerX, centerY := 0.45, 0.38
	boxW, boxH := 0.2, 0.24
	if err := env.database.SetProfilePhotoFace(env.ctx, mediaID, db.ProfilePhotoFace{
		Checked: true, HasFace: true,
		CenterX: &centerX, CenterY: &centerY, BoxW: &boxW, BoxH: &boxH,
	}); err != nil {
		t.Fatalf("set face: %v", err)
	}

	query := "?nickname=" + url.QueryEscape(prefix)

	resp := viewerClient.get("/legacy/api/home" + query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy home status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home legacyHomePayload
	legacyJSONDecode(t, resp, &home)
	if home.CardsTotal != 2 {
		t.Fatalf("home cards_total = %d, want 2", home.CardsTotal)
	}
	if len(home.Cards) != 2 {
		t.Fatalf("home cards = %d, want 2", len(home.Cards))
	}
	self := home.Cards[0]
	if !self.IsSelf || self.Nickname != prefix+"-本人" {
		t.Fatalf("first home card = %+v, want self", self)
	}
	if self.Liked || self.Praised {
		t.Fatalf("self card interactions leaked: liked=%v praised=%v", self.Liked, self.Praised)
	}
	if self.MsgCount != 0 {
		t.Fatalf("self card msg_count = %d, want 0", self.MsgCount)
	}
	if home.Cards[1].IsSelf || home.Cards[1].Nickname != prefix+"-候选" {
		t.Fatalf("second home card = %+v, want candidate", home.Cards[1])
	}
	if len(self.Faces) != 0 {
		t.Fatalf("self card faces = %+v, want empty", self.Faces)
	}
	if len(home.Cards[1].Faces) != 1 || home.Cards[1].Faces[0] == nil {
		t.Fatalf("candidate card faces = %+v", home.Cards[1].Faces)
	}
	// 大图查看器需要的原图尺寸：与 photos 索引对齐，且只含宽高。
	if len(home.Cards[1].PhotosMeta) != len(home.Cards[1].Photos) || len(home.Cards[1].PhotosMeta) == 0 {
		t.Fatalf("photos_meta not aligned: photos=%d meta=%d",
			len(home.Cards[1].Photos), len(home.Cards[1].PhotosMeta))
	}
	if meta := home.Cards[1].PhotosMeta[0]; meta.W != 600 || meta.H != 800 {
		t.Fatalf("photos_meta[0] = %+v, want 600x800", meta)
	}
	if math.Abs(home.Cards[1].Faces[0].C[0]-0.45) > 0.001 ||
		math.Abs(home.Cards[1].Faces[0].Box[1]-0.24) > 0.001 {
		t.Fatalf("candidate face = %+v", home.Cards[1].Faces[0])
	}

	resp = viewerClient.get("/legacy/api/cards" + query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy cards status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var pool struct {
		Cards      []legacyHomeCard `json:"cards"`
		CardsTotal int              `json:"cards_total"`
		Total      int              `json:"total"`
	}
	legacyJSONDecode(t, resp, &pool)
	if pool.CardsTotal != 1 || pool.Total != 1 || len(pool.Cards) != 1 {
		t.Fatalf("cards endpoint total=%d cards_total=%d len=%d, want 1", pool.Total, pool.CardsTotal, len(pool.Cards))
	}
	if pool.Cards[0].IsSelf {
		t.Fatal("/legacy/api/cards must not include the viewer's own card")
	}
}

func TestIntegrationShellBootstrapInjection(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	me, client := env.createUser(t, "U-BOOT", "引导用户", "active", "user")
	attachTestPhoto(t, env, me.ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2 WHERE id = $1`, me.ID, db.GenderFemale); err != nil {
		t.Fatal(err)
	}
	resp := client.get("/app")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "window.__YXQ_BOOTSTRAP__={") {
		t.Fatal("shell did not inject the home bootstrap")
	}
	if strings.Contains(body, "window.__YXQ_BOOTSTRAP__=null") {
		t.Fatal("shell kept the empty bootstrap placeholder")
	}
	if !strings.Contains(body, `<link rel="preload" as="image"`) {
		t.Fatal("shell did not preload the first card thumbnail")
	}
	for _, want := range []string{
		`id="boot-card"`,
		`class="card-photo-item boot-photo"`,
		`class="card-photo-overlay"`,
		`class="card-info"`,
		`class="boot-tabbar"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell did not render the complete boot card (%q missing)", want)
		}
	}
	if strings.Contains(body, `id="boot-card"`) && !strings.Contains(body, "正在加载") {
		t.Fatal("boot card slow hint missing")
	}
	// native_admin 是管理台原生 SPA，不是牌堆：不渲染首卡静态岛，也不 preload 首卡图。
	native := readBody(t, client.get("/app?native_admin=1"))
	if strings.Contains(native, `id="boot-card"`) {
		t.Fatal("native_admin shell must not render the boot card")
	}
	if strings.Contains(native, `rel="preload" as="image"`) {
		t.Fatal("native_admin shell must not preload the deck thumbnail")
	}

	etag := resp.Header.Get("ETag")
	if !strings.Contains(etag, fmt.Sprintf("-u%d-", me.ID)) {
		t.Fatalf("shell etag %q is not bound to the user", etag)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("cache-control=%q", got)
	}
	if vary := strings.Join(resp.Header.Values("Vary"), ","); !strings.Contains(vary, "Cookie") {
		t.Fatalf("vary=%q must cover Cookie (personal data)", vary)
	}

	// A nickname containing a script terminator must never break out of the
	// injected JSON script block.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET nickname = $2 WHERE id = $1`,
		me.ID, `x</script><script>alert(1)</script>`); err != nil {
		t.Fatal(err)
	}
	resp = client.get("/app")
	body = readBody(t, resp)
	if strings.Contains(body, "</script><script>alert(1)") {
		t.Fatal("bootstrap JSON was not escaped against </script> breakout")
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func TestIntegrationShellBootstrapETagAndAnon(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	me, client := env.createUser(t, "U-BOOT2", "引导用户2", "active", "user")
	attachTestPhoto(t, env, me.ID)
	env.app.clock = fixedClock{at: time.Unix(1790000000, 0).UTC()}

	first := client.get("/app")
	etag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("status=%d etag=%q", first.StatusCode, etag)
	}
	req, err := http.NewRequest(http.MethodGet, client.base+"/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	second := client.do(req)
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation status=%d want 304 (etag covers bootstrap bytes)", second.StatusCode)
	}
	if got := readBody(t, second); got != "" {
		t.Fatalf("304 leaked a body of %d bytes", len(got))
	}

	// 未登录不注入任何个人数据，直接跳登录页。
	anon := newWSClient(t, env.server.URL)
	resp := anon.get("/app")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon status=%d want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?next=/" {
		t.Fatalf("anon location=%q", loc)
	}
}

func TestIntegrationShellBootstrapOmitsSensitiveFields(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	me, client := env.createUser(t, "U-BOOT3", "隐私用户", "active", "user")
	attachTestPhoto(t, env, me.ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET wechat = $2 WHERE id = $1`, me.ID, "wx-secret-8888"); err != nil {
		t.Fatal(err)
	}

	shell := readBody(t, client.get("/app"))
	for _, banned := range []string{`"phone"`, `"wechat"`, "wx-secret-8888"} {
		if strings.Contains(shell, banned) {
			t.Fatalf("shell bootstrap leaked %s（壳是 private/no-cache 可复用，联系方式只能走 no-store API）", banned)
		}
	}
	// 首页数据本身仍要完整：昵称、额度、门禁字段都在。
	if !strings.Contains(shell, `"nickname"`) || !strings.Contains(shell, `"id_valid"`) {
		t.Fatal("shell bootstrap lost non-sensitive home data")
	}

	// /legacy/api/home 仍是 no-store 的完整载荷（含联系方式），语义未变。
	apiResp := client.get("/legacy/api/home")
	if got := apiResp.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("api home cache-control=%q", got)
	}
	api := readBody(t, apiResp)
	if !strings.Contains(api, `"wechat":"wx-secret-8888"`) || !strings.Contains(api, `"phone"`) {
		t.Fatalf("api home payload lost contact fields: %s", api[:min(len(api), 200)])
	}
}

// bootstrapJSON decodes the inlined home payload from the shell body.
func bootstrapJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	const marker = "window.__YXQ_BOOTSTRAP__="
	index := strings.Index(body, marker)
	if index < 0 {
		t.Fatal("shell has no bootstrap marker")
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(body[index+len(marker):]))
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	return payload
}

// assertNoSensitiveBootstrap walks the payload and fails on any contact/ID key
// or value. Key matching must stay pattern-based: the exact-list version missed
// likes.mutual[].their_wechat/my_wechat (Reviewer A8).
func assertNoSensitiveBootstrap(t *testing.T, value any, path string, bannedValues []string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveBootstrapKey(key) {
				t.Fatalf("bootstrap leaks sensitive key %s%s", path, key)
			}
			assertNoSensitiveBootstrap(t, child, path+key+".", bannedValues)
		}
	case []any:
		for i, child := range typed {
			assertNoSensitiveBootstrap(t, child, fmt.Sprintf("%s[%d].", path, i), bannedValues)
		}
	case string:
		for _, banned := range bannedValues {
			if banned != "" && strings.Contains(typed, banned) {
				t.Fatalf("bootstrap leaks value %q at %s", banned, path)
			}
		}
	}
}

func TestIntegrationMutualReadyShellOmitsWechat(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	male, maleClient := env.createUser(t, "U-MUT-SH-M", "壳微信男", "active", "user")
	female, _ := env.createUser(t, "U-MUT-SH-F", "壳微信女", "active", "user")
	cleanupHTTPUsers(t, env, male.ID, female.ID)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = 'wx-ready-male', contact_consent = true WHERE id = $1`,
		male.ID, db.GenderMale); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, wechat = 'wx-ready-female', contact_consent = true WHERE id = $1`,
		female.ID, db.GenderFemale); err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateLike(env.ctx, db.CreateLikeParams{
		InitiatorID: male.ID, TargetID: female.ID, LikeType: db.LikeTypeReal,
		Message: "壳内联回归：这条附言只用于集成测试。", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateLike(env.ctx, db.CreateLikeParams{
		InitiatorID: female.ID, TargetID: male.ID, LikeType: db.LikeTypeAnon,
		Message: "壳内联回归：这条附言只用于集成测试。", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// 双方已就绪：likes API 必须照旧返回两个微信号（my_wechat/their_wechat）。
	likesResp := maleClient.get("/legacy/api/likes/me")
	if got := likesResp.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("likes cache-control=%q", got)
	}
	likes := readBody(t, likesResp)
	for _, want := range []string{`"my_wechat":"wx-ready-male"`, `"their_wechat":"wx-ready-female"`} {
		if !strings.Contains(likes, want) {
			t.Fatalf("likes/me lost %s: %s", want, likes[:min(len(likes), 400)])
		}
	}

	// 壳（private, no-cache、可 304 复用）里不得出现任何联系方式。
	shell := readBody(t, maleClient.get("/app"))
	assertNoSensitiveBootstrap(t, bootstrapJSON(t, shell), "", []string{
		"wx-ready-male", "wx-ready-female",
	})
	if index := strings.Index(strings.ToLower(shell), "wx-ready-"); index >= 0 {
		start := index - 160
		if start < 0 {
			start = 0
		}
		end := index + 160
		if end > len(shell) {
			end = len(shell)
		}
		t.Fatalf("shell body leaked a wechat value: %q", shell[start:end])
	}
}
