package httpserver

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	webassets "github.com/7310C7310C/yixianqian/web"
)

func makeTestIDCard(t *testing.T, birth, seq string) string {
	t.Helper()
	prefix := "110105" + birth + seq
	if len(prefix) != 17 {
		t.Fatalf("bad id prefix %q", prefix)
	}
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := "10X98765432"
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(prefix[i]-'0') * weights[i]
	}
	return prefix + string(codes[sum%11])
}

func validRegisterValues(t *testing.T, phone, idCard, birthday string) url.Values {
	t.Helper()
	return url.Values{
		"name":                     {"测试用户"},
		"nickname":                 {"小测"},
		"gender":                   {"女性"},
		"birthday":                 {birthday},
		"phone":                    {phone},
		"id_card":                  {idCard},
		"wechat":                   {"wx-test"},
		"baptismal_name":           {"玛利亚"},
		"church":                   {"南头堂"},
		"church_group":             {"圣咏团"},
		"church_location_province": {"44"},
		"church_location_city":     {"4403"},
		"church_location_area":     {"440305"},
		"native_place_province":    {"44"},
		"native_place_city":        {"4401"},
		"city_province":            {"44"},
		"city_city":                {"4403"},
		"family":                   {"两个兄弟姐妹"},
		"height_cm":                {"165"},
		"education":                {"本科"},
		"industry":                 {"资讯科技"},
		"position":                 {"工程师"},
		"income":                   {"20W - 30W"},
		"house":                    {"有"},
		"house_note_have":          {"深圳南山 89 平米"},
		"house_note_none":          {"无房"},
		"driving":                  {"无"},
		"driving_note_have":        {"粤 B"},
		"driving_note_none":        {"无车"},
		"marriage":                 {"没结过婚"},
		"live_with_parents":        {"独立生活"},
		"personality":              {"性格开朗，喜欢服务。"},
		"self_traits":              {"积极乐观", "容易相处"},
		"self_hobbies":             {"听音乐", "看书"},
		"self_sports":              {"羽毛球", "跑步"},
		"mbti":                     {"E", "N", "F", "J"},
		"partner_criteria":         {"希望对方善良、愿意一起服务。"},
		"partner_traits":           {"正直", "积极乐观"},
		"partner_hobbies":          {"看书"},
		"partner_sports":           {"爬山"},
		"is_for_child":             {"不是，我为自己报名"},
		"referral_source":          {"朋友介绍"},
		"angel_ack":                {registerAngelAck},
		"password":                 {"register-pass-123"},
		"password_confirm":         {"register-pass-123"},
	}
}

func TestChineseIDCardValidation(t *testing.T) {
	birth := "19900101"
	id := makeTestIDCard(t, birth, "002")
	normalized, gotBirth, err := validateChineseIDCard(strings.ToLower(id))
	if err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
	if normalized != id {
		t.Fatalf("normalized = %q want %q", normalized, id)
	}
	if gotBirth.Format("2006-01-02") != "1990-01-01" {
		t.Fatalf("birth = %s", gotBirth)
	}

	bad := id[:17] + "0"
	if bad == id {
		bad = id[:17] + "1"
	}
	if _, _, err := validateChineseIDCard(bad); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if _, _, err := validateChineseIDCard("110105491332002"); err == nil {
		t.Fatal("invalid date accepted")
	}
	legacy15 := "110105491231002"
	if normalized, birth, err := validateChineseIDCard(legacy15); err != nil ||
		normalized != "11010519491231002X" || birth.Year() != 1949 {
		t.Fatalf("15-digit id: %q %v %v", normalized, birth, err)
	}
	// The legacy 15-digit form must hash like its 18-digit equivalent, or one
	// card could register two accounts by switching spellings.
	if idCardHash("test-secret", legacy15) != idCardHash("test-secret", "11010519491231002X") {
		t.Fatal("15-digit and equivalent 18-digit ids hash differently")
	}
	for _, invalid15 := range []string{"110105491331002", "11010549123100", "1101054912310021"} {
		if _, _, err := validateChineseIDCard(invalid15); err == nil {
			t.Fatalf("invalid 15-digit id %q accepted", invalid15)
		}
	}
}

func TestParseRegisterInputNormalizesLegacy15DigitID(t *testing.T) {
	values := validRegisterValues(t, "13800138000", "110105491231002", "1949-12-31")
	in, ferr := parseRegisterInput(values)
	if ferr != nil {
		t.Fatalf("15-digit registration rejected: %+v", ferr)
	}
	if in.IDCard != "11010519491231002X" {
		t.Fatalf("stored id = %q, want canonical 18-digit form", in.IDCard)
	}
}

func TestWechatIDValidation(t *testing.T) {
	for _, valid := range []string{"wx-test", "wxid_abc123", "13800138000", "a.b@c"} {
		if got, err := validateWechatID("  " + valid + " "); err != nil || got != valid {
			t.Fatalf("valid wechat %q: got %q err=%v", valid, got, err)
		}
	}
	for _, invalid := range []string{"", "abcd", "wx test", "微信abc", "<script>", strings.Repeat("a", 65)} {
		if _, err := validateWechatID(invalid); err == nil {
			t.Fatalf("invalid wechat %q accepted", invalid)
		}
	}
}

func TestBackfillIDCardRequires18Digits(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	if got, birth, err := validateBackfillIDCard(id); err != nil || got != id ||
		birth.Format("2006-01-02") != "1990-01-01" {
		t.Fatalf("valid backfill id: %q %v %v", got, birth, err)
	}
	if _, _, err := validateBackfillIDCard("110105491231002"); err == nil {
		t.Fatal("15-digit id accepted for backfill")
	}
}

func TestIDCardCipherRoundTrip(t *testing.T) {
	const secret = "test-session-secret-0123456789abcdef"
	const id = "11010519900101002X"
	sealed, err := sealIDCard(secret, id)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(id)) {
		t.Fatal("ciphertext contains plaintext id")
	}
	opened, err := openIDCard(secret, sealed)
	if err != nil || opened != id {
		t.Fatalf("open = %q err=%v", opened, err)
	}
	if _, err := openIDCard("another-secret-0123456789abcdef", sealed); err == nil {
		t.Fatal("ciphertext opened with wrong key")
	}
}

func TestParseRegisterInputRequiresValidWechat(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	for _, tt := range []struct {
		name   string
		wechat string
		field  string
	}{
		{"missing", "", "wechat"},
		{"too short", "abcd", "wechat"},
		{"whitespace", "wx test", "wechat"},
		{"markup", "<b>wx</b>", "wechat"},
	} {
		values := validRegisterValues(t, "13800138000", id, "1990-01-01")
		values.Set("wechat", tt.wechat)
		if _, ferr := parseRegisterInput(values); ferr == nil || ferr.Field != tt.field {
			t.Fatalf("%s: err = %+v", tt.name, ferr)
		}
	}
	values := validRegisterValues(t, "13800138000", id, "1990-01-01")
	values.Set("wechat", "wx-valid_1")
	in, ferr := parseRegisterInput(values)
	if ferr != nil || in.Wechat != "wx-valid_1" {
		t.Fatalf("valid wechat: %q %+v", in.Wechat, ferr)
	}
}

func TestParseRegisterInputValid(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	values := validRegisterValues(t, "+86 138-0013-8000", id, "1990-01-01")
	values.Set("inviter", "u-0001")
	in, ferr := parseRegisterInput(values)
	if ferr != nil {
		t.Fatalf("parse: %v", ferr)
	}
	if in.Phone != "13800138000" {
		t.Fatalf("phone = %q", in.Phone)
	}
	if in.Inviter != "U-0001" {
		t.Fatalf("inviter = %q", in.Inviter)
	}
	if in.HeightCM != 165 || len(in.SelfTraits) != 2 || in.Birthday.Format("2006-01-02") != "1990-01-01" {
		t.Fatalf("unexpected input: %+v", in)
	}
	if in.HouseNoteHave != "深圳南山 89 平米" || in.HouseNoteNone != "" ||
		in.DrivingNoteNone != "无车" || in.DrivingNoteHave != "" {
		t.Fatalf("house/car notes = %q/%q/%q/%q", in.HouseNoteHave, in.HouseNoteNone,
			in.DrivingNoteHave, in.DrivingNoteNone)
	}
}

func TestParseRegisterInputRejectsInvalidFields(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	badID := id[:17] + "0"
	if badID == id {
		badID = id[:17] + "1"
	}
	cases := []struct {
		name   string
		mutate func(url.Values)
		want   string
	}{
		{"missing name", func(v url.Values) { v.Del("name") }, "姓名"},
		{"bad phone", func(v url.Values) { v.Set("phone", "123") }, "手机号码"},
		{"bad id checksum", func(v url.Values) { v.Set("id_card", badID) }, "身份证号码"},
		{"bad option", func(v url.Values) { v.Set("gender", "其他") }, "选项不正确"},
		{"bad multi", func(v url.Values) { v["self_traits"] = []string{"不存在"} }, "选项不正确"},
		{"missing ack", func(v url.Values) { v.Del("angel_ack") }, "小天使"},
		{"mismatched password", func(v url.Values) { v.Set("password_confirm", "different-123") }, "不一致"},
		{"short password", func(v url.Values) { v.Set("password", "short"); v.Set("password_confirm", "short") }, "至少需要 8"},
		{"bad height", func(v url.Values) { v.Set("height_cm", "99") }, "身高"},
		{"bad birthday", func(v url.Values) { v.Set("birthday", "1990-13-01") }, "生日格式"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := validRegisterValues(t, "13800138000", id, "1990-01-01")
			tc.mutate(values)
			if _, ferr := parseRegisterInput(values); ferr == nil {
				t.Fatalf("expected error containing %q", tc.want)
			} else if !strings.Contains(ferr.Message, tc.want) {
				t.Fatalf("message = %q, want %q", ferr.Message, tc.want)
			}
		})
	}
}

func TestRegisterTemplatesRender(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"register", "register_done"} {
		if !tpl.HasPage(page) {
			t.Fatalf("missing page %q", page)
		}
	}
	if tpl.HasPage("welcome") {
		t.Fatal("welcome page template should be deleted (merged into the landing page)")
	}

	id := makeTestIDCard(t, "19900101", "002")
	data := buildRegisterPageData(validRegisterValues(t, "13800138000", id, "1990-01-01"))
	var register bytes.Buffer
	if err := tpl.Render(&register, "register", PageData{
		Title: "注册", CSRFToken: "csrf-value", PageCSS: "register.css",
		ScanSafe: true, CopyJSON: registerCopyJSON(data, ""),
		Data: data,
	}); err != nil {
		t.Fatal(err)
	}
	body := register.String()
	for _, want := range []string{`action="/register"`, `enctype="multipart/form-data"`,
		`name="csrf_token" value="csrf-value"`, `name="photos"`, `id="register-form"`,
		`data-copy-json=`, `data-register-text="field.id_card.label"`,
		`data-register-option="gender.0"`, `data-register-value="name"`,
		`data-region-field="city"`, `name="city_province"`, `name="city_city"`,
		`name="church_location_area"`,
		`id="field_house_note_have"`, `data-show-when-field="house"`, `name="house_note_have"`,
		`id="field_driving_note_none"`, `data-show-when-field="driving"`,
		`/static/register.js`} {
		if !strings.Contains(body, want) {
			t.Fatalf("register missing %q", want)
		}
	}
	assertHanFreePublicBody(t, body)
	catalog := registerCatalogFromBody(t, body)
	for key, want := range map[string]string{
		"field.id_card.label": "身份证号码",
		"field.name.hint":     "不公开，请填写真实姓名。",
		"select.required":     "请选择",
		"region.province":     "省 / 直辖市",
		"page.error":          "",
	} {
		if got := catalog.Text[key]; got != want {
			t.Fatalf("catalog text[%q] = %q, want %q", key, got, want)
		}
	}
	if catalog.Values["name"] != "测试用户" {
		t.Fatalf("catalog echoed name = %q", catalog.Values["name"])
	}
	if catalog.Options["gender.1"] != "女性" {
		t.Fatalf("catalog gender option = %q", catalog.Options["gender.1"])
	}
	// The submitted gender (女性) must stay selected after the round trip.
	if !strings.Contains(body, `data-register-option="gender.1" selected`) {
		t.Fatal("register did not preserve the selected option")
	}

	evilValues := validRegisterValues(t, "13800138000", id, "1990-01-01")
	evilValues.Set("name", `<script>alert(1)</script>`)
	evilData := buildRegisterPageData(evilValues)
	var evil bytes.Buffer
	if err := tpl.Render(&evil, "register", PageData{
		Title: "注册", CSRFToken: "csrf-value", PageCSS: "register.css",
		ScanSafe: true, CopyJSON: registerCopyJSON(evilData, ""),
		Data: evilData,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evil.String(), "<script>alert(1)</script>") {
		t.Fatal("register form did not escape user data")
	}
	if strings.Contains(evil.String(), `alert(1)</script>`) {
		t.Fatal("register form re-emitted the raw payload")
	}
	evilCatalog := registerCatalogFromBody(t, evil.String())
	if evilCatalog.Values["name"] != `<script>alert(1)</script>` {
		t.Fatalf("hostile value not carried verbatim in the catalog: %q", evilCatalog.Values["name"])
	}

	var done bytes.Buffer
	if err := tpl.Render(&done, "register_done", PageData{
		Title: "待审核", CSRFToken: "csrf-value", PageCSS: "register.css",
		ScanSafe: true, CopyPage: "register_done",
		Data: registerDoneData{CopyKey: "register_done.pending", LoggedIn: true, PhoneMasked: "138****00"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-copy-text="register_done.pending.heading"`, "138****00", "/logout"} {
		if !strings.Contains(done.String(), want) {
			t.Fatalf("done missing %q", want)
		}
	}
}

var registerCopyAttrPattern = regexp.MustCompile(`data-copy-json="([^"]*)"`)
var registerHookPattern = regexp.MustCompile(`data-register-(text|placeholder|aria|option|option-text|value)="([^"]+)"`)
var registerStaticRefPattern = regexp.MustCompile(`(?:src|href)="(/static/[^"?#]+)`)

// registerCatalogFromBody decodes the ASCII-only catalog the register page
// carries in <body data-copy-json>. The attribute is HTML-escaped by
// html/template, so unescape before parsing the JSON.
func registerCatalogFromBody(t *testing.T, body string) registerCopyCatalog {
	t.Helper()
	match := registerCopyAttrPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("register page has no data-copy-json attribute")
	}
	var catalog registerCopyCatalog
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &catalog); err != nil {
		t.Fatalf("decode register catalog: %v", err)
	}
	return catalog
}

func renderRegisterForTest(t *testing.T, values url.Values, message string) string {
	t.Helper()
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	data := buildRegisterPageData(values)
	data.MaxMB = 20
	var buf bytes.Buffer
	if err := tpl.Render(&buf, "register", PageData{
		Title: "注册", CSRFToken: "csrf-value", PageCSS: "register.css",
		ScanSafe: true, CopyJSON: registerCopyJSON(data, message),
		Data: data, Error: message,
	}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestRegisterCopyCatalogIsASCII guards the scan contract for the generated
// catalog: no raw non-ASCII byte may reach the HTML, and the decoded copy must
// still match registerSections().
func TestRegisterCopyCatalogIsASCII(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	data := buildRegisterPageData(validRegisterValues(t, "13800138000", id, "1990-01-01"))
	data.MaxMB = 20
	raw := registerCopyJSON(data, "请上传 3-9 张个人照片。")
	if raw == "" {
		t.Fatal("empty catalog")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] >= utf8.RuneSelf {
			t.Fatalf("catalog has a non-ASCII byte at %d: %q", i, raw[i])
		}
	}
	if match := hanPattern.FindString(raw); match != "" {
		t.Fatalf("catalog contains Han (%q)", match)
	}
	var catalog registerCopyCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		t.Fatalf("catalog is not valid JSON: %v", err)
	}
	if len(catalog.Text) < 30 || len(catalog.Options) < 50 || len(catalog.Values) == 0 {
		t.Fatalf("suspiciously small catalog: text=%d options=%d values=%d",
			len(catalog.Text), len(catalog.Options), len(catalog.Values))
	}
	if got := catalog.Text["field.house_note_have.label"]; got != "房产补充（有房）" {
		t.Fatalf("house note label = %q", got)
	}
	if got := catalog.Text["field.id_card.label"]; got != "身份证号码" {
		t.Fatalf("id card label = %q", got)
	}
	if got := catalog.Text["page.error"]; got != "请上传 3-9 张个人照片。" {
		t.Fatalf("page.error = %q", got)
	}
	if got := catalog.Values["family"]; got != "两个兄弟姐妹" {
		t.Fatalf("echoed family = %q", got)
	}
	optionCount := 0
	for _, section := range data.Sections {
		for _, field := range section.Fields {
			if field.Type == string(registerKindCheckbox) {
				optionCount++
				continue
			}
			optionCount += len(field.Options)
		}
	}
	if len(catalog.Options) != optionCount {
		t.Fatalf("catalog options = %d, want %d", len(catalog.Options), optionCount)
	}
}

// TestRegisterCopyCoversTemplateKeys keeps the empty markup hooks and the
// server catalog in sync: a missing key would render a blank label.
func TestRegisterCopyCoversTemplateKeys(t *testing.T) {
	body := renderRegisterForTest(t, nil, "")
	assertHanFreePublicBody(t, body)
	catalog := registerCatalogFromBody(t, body)

	fieldNames := map[string]bool{}
	for _, spec := range registerFields() {
		fieldNames[spec.Name] = true
	}
	hooks := registerHookPattern.FindAllStringSubmatch(body, -1)
	if len(hooks) < 100 {
		t.Fatalf("expected many register hooks, got %d", len(hooks))
	}
	textHooks := map[string]bool{}
	optionHooks := map[string]bool{}
	valueHooks := map[string]bool{}
	for _, hook := range hooks {
		kind, key := hook[1], hook[2]
		switch kind {
		case "text", "placeholder", "aria":
			textHooks[key] = true
			if _, ok := catalog.Text[key]; !ok {
				t.Fatalf("catalog has no text for %s=%q", kind, key)
			}
		case "option", "option-text":
			optionHooks[key] = true
			if _, ok := catalog.Options[key]; !ok {
				t.Fatalf("catalog has no option for %s=%q", kind, key)
			}
		case "value":
			valueHooks[key] = true
			if !fieldNames[key] {
				t.Fatalf("data-register-value=%q is not a register field", key)
			}
		default:
			t.Fatalf("unexpected hook kind %q", kind)
		}
	}

	// 反向断言：catalog 里的每个 key 都必须有模板钩子。只查 hooks→catalog
	// 时，删掉一个钩子会让该文案/选项/回填值在页面上静默消失而 Go 测试仍绿
	// （此前只有 Playwright 能抓）。
	for key := range catalog.Text {
		if key == "page.error" { // 只在失败重渲染时注入，由错误用例覆盖。
			continue
		}
		if !textHooks[key] {
			t.Fatalf("catalog text %q has no template hook (data-register-text/-placeholder); the copy would render blank", key)
		}
	}
	for key := range catalog.Options {
		if !optionHooks[key] {
			t.Fatalf("catalog option %q has no template hook (data-register-option/-option-text); the option would render blank", key)
		}
	}
	for key := range catalog.Values {
		if !valueHooks[key] {
			t.Fatalf("catalog value %q has no data-register-value hook; the submitted value would not be restored", key)
		}
	}
}

// TestRegisterPageReferencesHanFreeAssets covers the second half of the scan
// contract: every same-origin CSS/JS the form loads is ASCII-only too.
func TestRegisterPageReferencesHanFreeAssets(t *testing.T) {
	body := renderRegisterForTest(t, nil, "")
	refs := registerStaticRefPattern.FindAllStringSubmatch(body, -1)
	if len(refs) == 0 {
		t.Fatal("register page references no static assets")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		name := strings.TrimPrefix(ref[1], "/")
		if seen[name] || !scanTextAsset(name) {
			continue
		}
		seen[name] = true
		data, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("register references missing asset %q: %v", ref[1], err)
		}
		if match := hanPattern.FindString(string(data)); match != "" {
			t.Fatalf("%s contains Han (%q); referenced assets must stay ASCII-only", name, match)
		}
	}
	for _, want := range []string{"static/home.css", "static/auth.css", "static/register.css", "static/register.js"} {
		if !seen[want] {
			t.Fatalf("register page should load %q", want)
		}
	}
	for _, unwanted := range []string{"static/app.css", "static/shell.css", "static/public-copy.js"} {
		if seen[unwanted] {
			t.Fatalf("register page must not load %q", unwanted)
		}
	}
}

// TestRegisterCopyCarriesPostError proves a rejected POST re-renders Han-free
// while the user still receives the unchanged Chinese error message.
func TestRegisterCopyCarriesPostError(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	values := validRegisterValues(t, "13800138000", id, "1990-01-01")
	values.Del("angel_ack")
	message := "请确认已加小天使微信号并备注真实姓名。"
	body := renderRegisterForTest(t, values, message)
	assertHanFreePublicBody(t, body)
	catalog := registerCatalogFromBody(t, body)
	if catalog.Text["page.error"] != message {
		t.Fatalf("page.error = %q, want %q", catalog.Text["page.error"], message)
	}
	if catalog.Values["name"] != "测试用户" || catalog.Values["church"] != "南头堂" {
		t.Fatalf("echoed values missing: %+v", catalog.Values)
	}
	if !strings.Contains(body, `data-register-error`) {
		t.Fatal("missing error hook")
	}
	if strings.Contains(body, message) {
		t.Fatal("server error message leaked into the raw HTML")
	}
}

func TestRegistrationGatePaths(t *testing.T) {
	for _, path := range []string{"/welcome", "/register", "/register/done", "/register/regions",
		"/login", "/logout", "/activate", "/password", "/angel-qr", "/static/app.css", "/media/abc"} {
		if registrationGateApplies(path) {
			t.Fatalf("%s should be exempt", path)
		}
	}
	for _, path := range []string{"/", "/cards", "/likes", "/api/cards", "/profile", "/admin/users"} {
		if !registrationGateApplies(path) {
			t.Fatalf("%s should be gated", path)
		}
	}
}

func TestRegisterFormValuesDropSecrets(t *testing.T) {
	values := url.Values{
		"name":     {"测试"},
		"id_card":  {"11010519900101002X"},
		"password": {"super-secret-123"},
	}
	clean := registerFormValues(values)
	if clean.Get("name") != "测试" {
		t.Fatalf("name = %q", clean.Get("name"))
	}
	if clean.Get("id_card") != "" || clean.Get("password") != "" {
		t.Fatalf("secrets echoed back: %v", clean)
	}
	data := buildRegisterPageData(values)
	for _, section := range data.Sections {
		for _, field := range section.Fields {
			if field.Name == "id_card" && field.Value != "" {
				t.Fatalf("id card echoed in form: %q", field.Value)
			}
		}
	}
}

func TestRegisterRegionsEndpoint(t *testing.T) {
	app := newHomeTestApp(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/register/regions", nil)
	app.handleRegisterRegions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if !strings.Contains(rec.Body.String(), `"provinces"`) ||
		!strings.Contains(rec.Body.String(), `广东`) {
		t.Fatalf("payload missing provinces: %s", rec.Body.String()[:120])
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/register/regions", nil)
	req.Header.Set("If-None-Match", etag)
	app.handleRegisterRegions(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d", rec.Code)
	}
}

func TestParseRegisterRegionFields(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	values := validRegisterValues(t, "13800138000", id, "1990-01-01")
	in, ferr := parseRegisterInput(values)
	if ferr != nil {
		t.Fatalf("structured parse: %v", ferr)
	}
	if in.City != "广东深圳" || in.NativePlace != "广东广州" || in.ChurchLocation != "深圳南山" {
		t.Fatalf("region values = %q / %q / %q", in.City, in.NativePlace, in.ChurchLocation)
	}

	// Free-text fallback is gone: a city must come from the dataset cascade.
	manual := validRegisterValues(t, "13800138001", id, "1990-01-01")
	for _, key := range []string{"native_place_province", "native_place_city"} {
		manual.Del(key)
	}
	manual.Set("native_place", "香港")
	if _, ferr := parseRegisterInput(manual); ferr == nil || ferr.Field != "native_place" {
		t.Fatalf("manual region should be rejected, got %+v", ferr)
	}

	// A mismatched province/city pair must be rejected server-side.
	bad := validRegisterValues(t, "13800138002", id, "1990-01-01")
	bad.Set("city_province", "44")
	bad.Set("city_city", "1101")
	if _, ferr := parseRegisterInput(bad); ferr == nil || !strings.Contains(ferr.Message, "地区不正确") {
		t.Fatalf("mismatched region error = %v", ferr)
	}
}

func TestAtLeast18OnCalendarDay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 29, 7, 0, 0, 0, loc)
	birth, err := time.Parse("20060102", "20081029") // UTC midnight
	if err != nil {
		t.Fatal(err)
	}
	if !atLeast18On(birth, now) {
		t.Fatal("18th birthday today must pass (ID date is UTC, server is CST)")
	}
	if atLeast18On(birth, now.AddDate(0, 0, -1)) {
		t.Fatal("one day before the 18th birthday must be rejected")
	}
	leapNow := time.Date(2028, 2, 29, 12, 0, 0, 0, loc)
	afterLeap, _ := time.Parse("20060102", "20100301")
	if atLeast18On(afterLeap, leapNow) {
		t.Fatal("Mar 1 birthday is 17 on Feb 29 (time.Date would normalize to Mar 1)")
	}
	beforeLeap, _ := time.Parse("20060102", "20100228")
	if !atLeast18On(beforeLeap, leapNow) {
		t.Fatal("Feb 28 birthday is 18 on Feb 29")
	}
}

func TestParseRegisterCorrectsBirthdayFromID(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	values := validRegisterValues(t, "13800138000", id, "1990-01-02")
	in, ferr := parseRegisterInput(values)
	if ferr != nil {
		t.Fatalf("mismatched birthday should be corrected, got %v", ferr)
	}
	if !sameCalendarDay(in.Birthday, time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("birthday = %v, want 1990-01-01 from the ID card", in.Birthday)
	}
	if in.IDCard != id {
		t.Fatalf("id card = %q, want %q", in.IDCard, id)
	}
}

func TestParseRegisterRejectsUnderageAndNonMobile(t *testing.T) {
	id := makeTestIDCard(t, "20150101", "002")
	values := validRegisterValues(t, "13800138000", id, "2015-01-01")
	if _, ferr := parseRegisterInput(values); ferr == nil || !strings.Contains(ferr.Message, "18") {
		t.Fatalf("underage error = %v", ferr)
	}
	// The typed birthday claims 18+, but the ID card is underage. The corrected
	// (ID-card) birthday must be checked and the error must stay on birthday.
	values = validRegisterValues(t, "13800138000", id, "1990-01-01")
	if _, ferr := parseRegisterInput(values); ferr == nil || ferr.Field != "birthday" || !strings.Contains(ferr.Message, "18") {
		t.Fatalf("underage ID with adult birthday error = %+v", ferr)
	}
	id = makeTestIDCard(t, "19900101", "002")
	values = validRegisterValues(t, "12345678", id, "1990-01-01")
	if _, ferr := parseRegisterInput(values); ferr == nil || !strings.Contains(ferr.Message, "手机号码") {
		t.Fatalf("non-mobile error = %v", ferr)
	}
}

func TestRegisterAngelQRMarkupWhenAssetExists(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	id := makeTestIDCard(t, "19900101", "002")
	data := buildRegisterPageData(validRegisterValues(t, "13800138000", id, "1990-01-01"))
	data.HasAngelQR = true
	for si := range data.Sections {
		for fi := range data.Sections[si].Fields {
			if data.Sections[si].Fields[fi].Name == "angel_ack" {
				data.Sections[si].Fields[fi].ShowQR = true
			}
		}
	}
	var buf bytes.Buffer
	if err := tpl.Render(&buf, "register", PageData{
		Title: "注册", CSRFToken: "csrf", PageCSS: "register.css",
		ScanSafe: true, CopyJSON: registerCopyJSON(data, ""), Data: data,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `src="/angel-qr?v=`) {
		t.Fatal("register page missing angel QR image")
	}
	if !strings.Contains(body, `class="register-angel-qr"`) ||
		!strings.Contains(body, `data-angel-qr-image`) ||
		!strings.Contains(body, `data-angel-fallback`) ||
		!strings.Contains(body, `data-register-text="field.angel_ack.qrHint"`) {
		t.Fatal("register page missing QR option markup")
	}
	assertHanFreePublicBody(t, body)
}

func TestParseRegisterClearsUnselectedNotes(t *testing.T) {
	id := makeTestIDCard(t, "19900101", "002")
	values := validRegisterValues(t, "13800138000", id, "1990-01-01")
	values.Set("house", "无")
	values.Set("house_note_have", "不应保留")
	values.Set("driving", "有")
	values.Set("driving_note_none", "不应保留")
	in, ferr := parseRegisterInput(values)
	if ferr != nil {
		t.Fatal(ferr)
	}
	if in.HouseNoteHave != "" || in.HouseNoteNone != "无房" ||
		in.DrivingNoteHave != "粤 B" || in.DrivingNoteNone != "" {
		t.Fatalf("notes after switch = %q/%q/%q/%q",
			in.HouseNoteHave, in.HouseNoteNone, in.DrivingNoteHave, in.DrivingNoteNone)
	}
}
