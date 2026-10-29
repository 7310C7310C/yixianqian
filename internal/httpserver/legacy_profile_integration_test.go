package httpserver

import (
	"bytes"
	"encoding/json"
	"image"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func legacyPhotoUpload(t *testing.T, client *wsClient, path, csrf, cover string, data []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if cover != "" {
		if err := w.WriteField("cover", cover); err != nil {
			t.Fatal(err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="legacy.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return profileRequest(t, client, http.MethodPost, path, csrf, &buf, w.FormDataContentType())
}

func legacyJSONDecode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

type legacyPhotoPayload struct {
	OK     bool     `json:"ok"`
	Photo  string   `json:"photo"`
	Photos []string `json:"photos"`
}

func TestIntegrationLegacyProfileFlow(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-LEGACYPROFILE", "真实姓名", "active", "user")
	viewer, viewerClient := env.createUser(t, "U-LEGACYVIEW", "浏览者", "active", "user")
	// 修改手机号需要当前密码（审计 UD-05）；为 owner 设置一个已知密码。
	ownerPassword := "legacy-profile-pass-1"
	ownerHash, err := auth.HashPassword(ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO auth_credentials (user_id, password_hash) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		owner.ID, ownerHash); err != nil {
		t.Fatalf("set owner password: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '男性', nickname = '' WHERE id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	viewerName := "浏览" + randomHTTPDigits(t, 8)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '女性', nickname = $2 WHERE id = $1`, viewer.ID, viewerName); err != nil {
		t.Fatal(err)
	}
	csrf := ownerClient.csrfCookie()

	resp := ownerClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var self struct {
		Nickname string                `json:"nickname"`
		Editable []legacyEditableField `json:"editable"`
		Photos   []string              `json:"photos"`
	}
	legacyJSONDecode(t, resp, &self)
	if len(self.Editable) != 29 {
		t.Fatalf("editable schema length = %d", len(self.Editable))
	}
	for _, field := range self.Editable {
		if field.Field == legacyFieldNickname && field.Type != "text" {
			t.Fatalf("nickname field = %+v", field)
		}
	}

	newPhone := randomHTTPPhone(t)
	update := map[string]any{
		"昵称":                 "小美",
		"身高（cm）":             168,
		"学历":                 "本科",
		"现居/工作城市":            "上海",
		"经常去的教堂":             "某教堂",
		"参加的团体":              "青年团",
		"教堂所在市-区":            "上海-浦东",
		"从事行业":               "教育",
		"职位":                 "老师",
		"年收入":                "20W - 30W",
		"房产状况":               "有",
		"房产状况-有-补充内容":        "上海浦东 89 平米",
		"是否有车":               "无",
		"是否有车-无-补充内容":        "暂无",
		"我是一个怎样的人":           "喜欢安静",
		"我是一个怎样的人-性格":        []string{"喜欢安静", "正直"},
		"我是一个怎样的人-爱好":        []string{"看书"},
		"我是一个怎样的人-运动":        []string{"跑步"},
		"我是个怎样的人-MBTI人格":     []string{"I", "N"},
		"婚后是否与父母同住":          "独立生活",
		"家庭成员情况":             "三口之家",
		"理想中的TA":             "善良",
		"理想中的TA-性格":          []string{"正直"},
		"理想中的TA-爱好":          []string{"看电影"},
		"理想中的TA-运动":          []string{"游泳"},
		"微信号":                "wx-legacy-owner",
		"手机号":                newPhone,
		"current_password":   ownerPassword,
		"name":               "被篡改",
		"id_card_ciphertext": "leak",
	}
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf, update)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy profile update status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	reloaded, err := env.database.GetProfile(env.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Nickname != "小美" || reloaded.HeightCM == nil || *reloaded.HeightCM != 168 ||
		reloaded.Phone != newPhone || reloaded.Name != "真实姓名" {
		t.Fatalf("profile after update = %+v", reloaded)
	}
	if len(reloaded.SelfTraits) != 2 || len(reloaded.MBTI) != 2 {
		t.Fatalf("multi values = %v / %v", reloaded.SelfTraits, reloaded.MBTI)
	}
	if reloaded.HouseNoteHave != "上海浦东 89 平米" || reloaded.DrivingNoteNone != "暂无" {
		t.Fatalf("notes = %q / %q", reloaded.HouseNoteHave, reloaded.DrivingNoteNone)
	}

	// Switching 有/无 must clear the other option's note even if the client
	// sends the stale value back.
	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"房产状况": "无", "房产状况-有-补充内容": "旧值"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("note switch status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	reloaded, err = env.database.GetProfile(env.ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.HouseNoteHave != "" {
		t.Fatalf("house note after switch = %q, want empty", reloaded.HouseNoteHave)
	}

	resp = profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", csrf,
		map[string]any{"身高（cm）": "一米七"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid height status = %d", resp.StatusCode)
	}
	var errPayload struct {
		Error string `json:"error"`
	}
	legacyJSONDecode(t, resp, &errPayload)
	if errPayload.Error == "" {
		t.Fatal("legacy error envelope missing message")
	}
	if resp := profileJSONRequest(t, ownerClient, http.MethodPut, "/legacy/api/profile", "", map[string]any{"昵称": "x"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("profile update without csrf status = %d", resp.StatusCode)
	}

	// Photos: upload, cover, delete. URLs must go through /profile/photo/{id}.
	resp = legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy photo upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var first legacyPhotoPayload
	legacyJSONDecode(t, resp, &first)
	if !first.OK || len(first.Photos) != 1 || !strings.HasPrefix(first.Photos[0], "/profile/photo/") {
		t.Fatalf("first photo = %+v", first)
	}
	if strings.Contains(strings.Join(first.Photos, " "), "media/") {
		t.Fatal("photo URL leaked an object key")
	}
	firstID := strings.TrimPrefix(first.Photos[0], "/profile/photo/")

	// The bundled SPA posts uploads to the absolute /api/profile/photo alias
	// (uploadWithProgress does not prepend BASE_PATH); keep both paths working.
	heicPhoto, err := os.ReadFile("../media/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read HEIC fixture: %v", err)
	}
	resp = legacyPhotoUpload(t, ownerClient, "/api/profile/photo", csrf, "1", heicPhoto)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy cover upload status = %d", resp.StatusCode)
	}
	var second legacyPhotoPayload
	legacyJSONDecode(t, resp, &second)
	if len(second.Photos) != 2 || second.Photo != second.Photos[0] {
		t.Fatalf("cover upload = %+v", second)
	}
	if second.Photos[0] == "/profile/photo/"+firstID {
		t.Fatal("cover=1 upload did not move the new photo to the front")
	}
	heicID := strings.TrimPrefix(second.Photos[0], "/profile/photo/")
	resp = ownerClient.get("/profile/photo/" + heicID)
	heicBody := readBodyBytes(t, resp)
	heicCfg, heicFormat, err := image.DecodeConfig(bytes.NewReader(heicBody))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" ||
		err != nil || heicFormat != "jpeg" || heicCfg.Width != 64 || heicCfg.Height != 64 {
		t.Fatalf("HEIC profile photo status=%d type=%q format=%q size=%dx%d err=%v",
			resp.StatusCode, resp.Header.Get("Content-Type"), heicFormat, heicCfg.Width, heicCfg.Height, err)
	}

	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/profile/photo/cover", csrf,
		map[string]any{"index": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy set cover status = %d", resp.StatusCode)
	}
	var cover legacyPhotoPayload
	legacyJSONDecode(t, resp, &cover)
	if len(cover.Photos) != 2 || cover.Photos[0] != "/profile/photo/"+firstID {
		t.Fatalf("cover result = %+v", cover)
	}

	// 批次10：/profile/photo 与 /cards/photo 共用授权，陌生人 404；
	// 建立喜欢关系后才允许跨用户读取。
	if resp := viewerClient.get("/profile/photo/" + firstID); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("stranger photo status = %d, want 404", resp.StatusCode)
	}
	// 候选池：viewer 拉一次牌堆后，本次下发的照片获得短时 grant。
	if resp := viewerClient.get("/legacy/api/cards?limit=50&nickname=" + url.QueryEscape("小美")); resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer cards status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if resp := viewerClient.get("/profile/photo/" + firstID); resp.StatusCode != http.StatusOK {
		t.Fatalf("candidate viewer photo status = %d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, ownerClient, http.MethodDelete, "/legacy/api/profile/photo", csrf,
		map[string]any{"index": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy delete photo status = %d", resp.StatusCode)
	}
	var afterDelete legacyPhotoPayload
	legacyJSONDecode(t, resp, &afterDelete)
	if len(afterDelete.Photos) != 1 {
		t.Fatalf("photos after delete = %+v", afterDelete)
	}

	// Public data must not leak contact fields.
	resp = viewerClient.get("/legacy/api/users/" + *owner.LegacyUserID + "/public")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy public status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	publicBody := readBody(t, resp)
	for _, secret := range []string{newPhone, "wx-legacy-owner", "真实姓名", `"phone"`, `"wechat"`, `"id_card"`} {
		if strings.Contains(publicBody, secret) {
			t.Fatalf("legacy public leaked %q: %s", secret, publicBody)
		}
	}
	if !strings.Contains(publicBody, "小美") || !strings.Contains(publicBody, "/profile/photo/") {
		t.Fatalf("legacy public missing allowed data: %s", publicBody)
	}
	var public struct {
		OpenID   string   `json:"openid"`
		UserID   string   `json:"user_id"`
		Nickname string   `json:"nickname"`
		Photos   []string `json:"photos"`
		CanLike  bool     `json:"can_like"`
		IsSelf   bool     `json:"is_self"`
	}
	if err := json.Unmarshal([]byte(publicBody), &public); err != nil {
		t.Fatal(err)
	}
	if public.IsSelf || !public.CanLike || len(public.Photos) != 1 {
		t.Fatalf("legacy public payload = %+v", public)
	}

	// Account status toggles and restores.
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/account/status", csrf, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("account status status = %d", resp.StatusCode)
	}
	var statusPayload struct {
		OK            bool   `json:"ok"`
		AccountStatus string `json:"account_status"`
	}
	legacyJSONDecode(t, resp, &statusPayload)
	if !statusPayload.OK || statusPayload.AccountStatus != "已脱单" {
		t.Fatalf("account status payload = %+v", statusPayload)
	}
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/account/status", csrf, nil)
	legacyJSONDecode(t, resp, &statusPayload)
	if statusPayload.AccountStatus != "单身" {
		t.Fatalf("account status restore = %+v", statusPayload)
	}

	// Suggestions and feedback share the admin suggestions table.
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/suggestions", csrf,
		map[string]any{"type": "功能建议", "content": "希望增加夜间模式"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("suggestion status = %d", resp.StatusCode)
	}
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/feedback", csrf,
		map[string]any{"target_openid": *viewer.LegacyUserID, "reason": "资料有误"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var suggestionCount int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM suggestions WHERE author_id = $1`, owner.ID).Scan(&suggestionCount); err != nil {
		t.Fatal(err)
	}
	if suggestionCount != 2 {
		t.Fatalf("suggestion count = %d", suggestionCount)
	}

	// Points endpoints keep the v1 shapes.
	if _, _, err := env.database.AddPointsEntry(env.ctx, db.AddPointsParams{
		UserID: owner.ID, Delta: 50, Kind: db.PointsKindAdmin,
		Reason: "legacy http test", RefType: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	resp = ownerClient.get("/legacy/api/points/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy points me status = %d", resp.StatusCode)
	}
	var pointsMe struct {
		Balance       int              `json:"balance"`
		Earned        int              `json:"earned"`
		Spent         int              `json:"spent"`
		PendingPoints int              `json:"pending_points"`
		PendingCount  int              `json:"pending_count"`
		Entries       []map[string]any `json:"entries"`
	}
	legacyJSONDecode(t, resp, &pointsMe)
	if pointsMe.Balance != 50 || pointsMe.Earned != 50 || len(pointsMe.Entries) != 1 {
		t.Fatalf("legacy points me = %+v", pointsMe)
	}

	resp = ownerClient.get("/legacy/api/points/rules")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy points rules status = %d", resp.StatusCode)
	}
	var rules struct {
		Items  []map[string]any `json:"items"`
		Config map[string]any   `json:"config"`
		Notes  []string         `json:"notes"`
	}
	legacyJSONDecode(t, resp, &rules)
	if len(rules.Items) != 4 || len(rules.Notes) == 0 ||
		rules.Config["invite_confirm_days"] == nil {
		t.Fatalf("legacy rules = %+v", rules)
	}

	resp = ownerClient.get("/legacy/api/points/invite")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy points invite status = %d", resp.StatusCode)
	}
	var invite struct {
		InviterID string `json:"inviter_id"`
		Progress  struct {
			Total int `json:"total"`
		} `json:"progress"`
		List []any `json:"list"`
	}
	legacyJSONDecode(t, resp, &invite)
	if invite.InviterID != *owner.LegacyUserID || invite.Progress.Total != 0 || len(invite.List) != 0 {
		t.Fatalf("legacy invite = %+v", invite)
	}

	resp = ownerClient.get("/legacy/api/points/wish-targets?q=" + viewerName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy wish targets status = %d", resp.StatusCode)
	}
	var wish struct {
		List []struct {
			OpenID   string `json:"openid"`
			Nickname string `json:"nickname"`
			UserID   string `json:"user_id"`
		} `json:"list"`
	}
	legacyJSONDecode(t, resp, &wish)
	if len(wish.List) != 1 || wish.List[0].UserID != *viewer.LegacyUserID || wish.List[0].OpenID == "" {
		t.Fatalf("legacy wish targets = %+v", wish)
	}

	requestKey := "legacy-http-" + randomHTTPDigits(t, 12)
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/points/redeem", csrf,
		map[string]any{"item": "real_like", "request_key": requestKey})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy redeem status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var redeem struct {
		OK      bool `json:"ok"`
		Balance int  `json:"balance"`
	}
	legacyJSONDecode(t, resp, &redeem)
	if !redeem.OK || redeem.Balance != 30 {
		t.Fatalf("legacy redeem = %+v", redeem)
	}
	// Retrying the same request key must not double-charge. The v1 API
	// reported the duplicate as a business error rather than a second order.
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/points/redeem", csrf,
		map[string]any{"item": "real_like", "request_key": requestKey})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("legacy idempotent redeem status = %d", resp.StatusCode)
	}
	resp = ownerClient.get("/legacy/api/points/me")
	legacyJSONDecode(t, resp, &pointsMe)
	if pointsMe.Balance != 30 {
		t.Fatalf("idempotent redeem balance = %+v", pointsMe)
	}

	resp = ownerClient.get("/legacy/api/points/redemptions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy redemptions status = %d", resp.StatusCode)
	}
	var redemptions struct {
		List []map[string]any `json:"list"`
	}
	legacyJSONDecode(t, resp, &redemptions)
	if len(redemptions.List) != 1 || redemptions.List[0]["item"] != "real_like" {
		t.Fatalf("legacy redemptions = %+v", redemptions)
	}

	// The Feishu claim entry is gone, but the endpoint must answer explicitly.
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/auth/claim", csrf,
		map[string]any{"phone": "13800000000", "name": "某人"})
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("claim status = %d", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "停用") {
		t.Fatalf("claim body = %s", body)
	}

	// Observer cannot toggle status.
	observer, observerClient := env.createUser(t, "U-LEGACYOBS", "观察员", "observer", "observer")
	_ = observer
	observerCSRF := observerClient.csrfCookie()
	resp = profileJSONRequest(t, observerClient, http.MethodPost, "/legacy/api/account/status", observerCSRF, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("observer status switch = %d", resp.StatusCode)
	}

	// Account role switch that is not registered returns the old 403, not 501.
	resp = profileJSONRequest(t, ownerClient, http.MethodPost, "/legacy/api/account/switch", csrf,
		map[string]any{"role": "observer"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("account switch status = %d", resp.StatusCode)
	}
	var switchBody struct {
		Error string `json:"error"`
	}
	legacyJSONDecode(t, resp, &switchBody)
	if !strings.Contains(switchBody.Error, "尚未注册") {
		t.Fatalf("account switch error = %q", switchBody.Error)
	}
}

// TestIntegrationSharedMediaDeleteKeepsObjects proves the HTTP layer only
// removes R2 objects when no other profile references the media and the caller
// owns it. Migrated data can share one media row between many users.
func TestIntegrationSharedMediaDeleteKeepsObjects(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-SHAREOBJ-A", "属主", "active", "user")
	other, otherClient := env.createUser(t, "U-SHAREOBJ-B", "引用者", "active", "user")
	cleanupHTTPUsers(t, env, owner.ID, other.ID)

	resp := legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", ownerClient.csrfCookie(), "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var payload legacyPhotoPayload
	legacyJSONDecode(t, resp, &payload)
	if len(payload.Photos) != 1 {
		t.Fatalf("photos = %v", payload.Photos)
	}
	mediaID := strings.TrimPrefix(payload.Photos[0], "/profile/photo/")
	if mediaID == payload.Photos[0] {
		t.Fatalf("unexpected photo url %q", payload.Photos[0])
	}

	seedShared := func() {
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
			 VALUES ($1, $2, 0, true)`, other.ID, mediaID); err != nil {
			t.Fatalf("seed shared attachment: %v", err)
		}
	}
	seedShared()

	var key, thumbKey string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT object_key, thumb_key FROM media WHERE id = $1`, mediaID).Scan(&key, &thumbKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.store.objects[key]; !ok {
		t.Fatal("uploaded original missing from store")
	}

	resp = profileJSONRequest(t, otherClient, http.MethodDelete, "/legacy/api/profile/photo",
		otherClient.csrfCookie(), map[string]any{"index": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("non-owner delete status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if _, ok := env.store.objects[key]; !ok {
		t.Fatal("non-owner delete removed the shared original")
	}
	if _, ok := env.store.objects[thumbKey]; !ok {
		t.Fatal("non-owner delete removed the shared thumbnail")
	}

	seedShared()
	resp = profileJSONRequest(t, ownerClient, http.MethodDelete, "/legacy/api/profile/photo",
		ownerClient.csrfCookie(), map[string]any{"index": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner delete status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if _, ok := env.store.objects[key]; !ok {
		t.Fatal("owner delete with a live reference removed the shared original")
	}
	var mediaRows int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM media WHERE id = $1`, mediaID).Scan(&mediaRows); err != nil {
		t.Fatal(err)
	}
	if mediaRows != 1 {
		t.Fatalf("media rows = %d, want 1", mediaRows)
	}
}
