package httpserver

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
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
		"name":              {"测试用户"},
		"nickname":          {"小测"},
		"gender":            {"女性"},
		"birthday":          {birthday},
		"phone":             {phone},
		"id_card":           {idCard},
		"wechat":            {"wx-test"},
		"baptismal_name":    {"玛利亚"},
		"church":            {"南头堂"},
		"church_group":      {"圣咏团"},
		"church_location":   {"深圳南山"},
		"native_place":      {"广东广州"},
		"city":              {"广东深圳"},
		"family":            {"两个兄弟姐妹"},
		"height_cm":         {"165"},
		"education":         {"本科"},
		"industry":          {"资讯科技"},
		"position":          {"工程师"},
		"income":            {"20W - 30W"},
		"house":             {"有"},
		"driving":           {"无"},
		"marriage":          {"没结过婚"},
		"live_with_parents": {"独立生活"},
		"personality":       {"性格开朗，喜欢服务。"},
		"self_traits":       {"积极乐观", "容易相处"},
		"self_hobbies":      {"听音乐", "看书"},
		"self_sports":       {"羽毛球", "跑步"},
		"mbti":              {"E", "N", "F", "J"},
		"partner_criteria":  {"希望对方善良、愿意一起服务。"},
		"partner_traits":    {"正直", "积极乐观"},
		"partner_hobbies":   {"看书"},
		"partner_sports":    {"爬山"},
		"is_for_child":      {"不是，我为自己报名"},
		"referral_source":   {"朋友介绍"},
		"angel_ack":         {registerAngelAck},
		"password":          {"register-pass-123"},
		"password_confirm":  {"register-pass-123"},
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
		normalized != legacy15 || birth.Year() != 1949 {
		t.Fatalf("15-digit id: %q %v %v", normalized, birth, err)
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
		{"bad phone", func(v url.Values) { v.Set("phone", "123") }, "手机号码格式"},
		{"bad id checksum", func(v url.Values) { v.Set("id_card", badID) }, "身份证号码格式"},
		{"bad option", func(v url.Values) { v.Set("gender", "其他") }, "选项不正确"},
		{"bad multi", func(v url.Values) { v["self_traits"] = []string{"不存在"} }, "选项不正确"},
		{"missing ack", func(v url.Values) { v.Del("angel_ack") }, "小天使"},
		{"mismatched password", func(v url.Values) { v.Set("password_confirm", "different-123") }, "不一致"},
		{"short password", func(v url.Values) { v.Set("password", "short"); v.Set("password_confirm", "short") }, "至少需要 8"},
		{"bad height", func(v url.Values) { v.Set("height_cm", "99") }, "100-250"},
		{"bad birthday", func(v url.Values) { v.Set("birthday", "1990-13-01") }, "生日格式"},
		{"id birthday mismatch", func(v url.Values) { v.Set("birthday", "1990-01-02") }, "出生日期"},
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
	for _, page := range []string{"welcome", "register", "register_done"} {
		if !tpl.HasPage(page) {
			t.Fatalf("missing page %q", page)
		}
	}

	var welcome bytes.Buffer
	if err := tpl.Render(&welcome, "welcome", PageData{Title: "一线牵", PageCSS: "register.css"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"一线牵", "/register?from=welcome", "/login", "人工审核"} {
		if !strings.Contains(welcome.String(), want) {
			t.Fatalf("welcome missing %q", want)
		}
	}
	if strings.Contains(welcome.String(), "飞书") || strings.Contains(welcome.String(), "飞书") {
		t.Fatal("welcome still mentions Feishu")
	}

	id := makeTestIDCard(t, "19900101", "002")
	var register bytes.Buffer
	if err := tpl.Render(&register, "register", PageData{
		Title: "注册", CSRFToken: "csrf-value", PageCSS: "register.css",
		Data: buildRegisterPageData(validRegisterValues(t, "13800138000", id, "1990-01-01")),
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`action="/register"`, `enctype="multipart/form-data"`,
		`name="csrf_token" value="csrf-value"`, `name="photos"`, "身份证号码", "登录密码",
		`name="phone"`, `placeholder="手机号"`} {
		if !strings.Contains(register.String(), want) {
			t.Fatalf("register missing %q", want)
		}
	}

	evilValues := validRegisterValues(t, "13800138000", id, "1990-01-01")
	evilValues.Set("name", `<script>alert(1)</script>`)
	var evil bytes.Buffer
	if err := tpl.Render(&evil, "register", PageData{
		Title: "注册", CSRFToken: "csrf-value", PageCSS: "register.css",
		Data: buildRegisterPageData(evilValues),
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evil.String(), "<script>alert(1)</script>") {
		t.Fatal("register form did not escape user data")
	}

	var done bytes.Buffer
	if err := tpl.Render(&done, "register_done", PageData{
		Title: "待审核", CSRFToken: "csrf-value", PageCSS: "register.css",
		Data: registerDoneData{Heading: "资料审核中", Message: "等待审核", LoggedIn: true, PhoneMasked: "138****00"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"资料审核中", "138****00", "/logout"} {
		if !strings.Contains(done.String(), want) {
			t.Fatalf("done missing %q", want)
		}
	}
}

func TestRegistrationGatePaths(t *testing.T) {
	for _, path := range []string{"/welcome", "/register", "/register/done", "/login", "/logout",
		"/activate", "/password", "/static/app.css", "/media/abc"} {
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
