package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func legacyTestIntPtr(v int) *int { return &v }

func decodeLegacyMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestLegacyProfileEditableMirrorsV1Schema(t *testing.T) {
	profile := db.Profile{
		Nickname:        "小美",
		HeightCM:        legacyTestIntPtr(165),
		Education:       "本科",
		City:            "杭州",
		Church:          "南头堂",
		ChurchGroup:     "青年组",
		ChurchLocation:  "上城区",
		Industry:        "互联网",
		Position:        "产品",
		Income:          "20W - 30W",
		House:           "无",
		Driving:         "有",
		Personality:     "安静",
		SelfTraits:      []string{"喜欢安静"},
		SelfHobbies:     []string{"看书"},
		SelfSports:      []string{"跑步"},
		MBTI:            []string{"I", "N"},
		LiveWithParents: "独立生活",
		Family:          "一家四口",
		PartnerCriteria: "温柔",
		PartnerTraits:   []string{"正直"},
		PartnerHobbies:  []string{"旅游爱好者"},
		PartnerSports:   []string{"游泳"},
		Wechat:          "wx-mimi",
		Phone:           "13800000000",
	}
	fields := legacyProfileEditable(profile)
	if len(fields) != 30 {
		t.Fatalf("editable fields = %d", len(fields))
	}
	byName := map[string]legacyEditableField{}
	for _, field := range fields {
		byName[field.Field] = field
	}
	for _, want := range []string{
		legacyFieldIDCard, legacyFieldNickname, legacyFieldHeight, legacyFieldEducation, legacyFieldCity,
		legacyFieldChurch, legacyFieldChurchGroup, legacyFieldChurchLocation,
		legacyFieldIndustry, legacyFieldPosition, legacyFieldIncome, legacyFieldHouse,
		legacyFieldHouseNoteHave, legacyFieldHouseNoteNone,
		legacyFieldDriving, legacyFieldDrivingNoteHave, legacyFieldDrivingNoteNone,
		legacyFieldPersonality, legacyFieldSelfTraits,
		legacyFieldSelfHobbies, legacyFieldSelfSports, legacyFieldMBTI,
		legacyFieldLiveWithParents, legacyFieldFamily, legacyFieldPartnerCriteria,
		legacyFieldPartnerTraits, legacyFieldPartnerHobbies, legacyFieldPartnerSports,
		legacyFieldWechat, legacyFieldPhone,
	} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("editable field missing %q", want)
		}
	}
	if byName[legacyFieldIDCard].Value != "" || byName[legacyFieldIDCard].Hint == "" {
		t.Fatalf("id card backfill field leaked or missing hint: %+v", byName[legacyFieldIDCard])
	}
	if got := byName[legacyFieldHeight].Value; got != 165 {
		t.Fatalf("height value = %#v", got)
	}
	if !byName[legacyFieldPersonality].Long {
		t.Fatal("personality must be long text")
	}
	if byName[legacyFieldSelfTraits].Type != "multi" {
		t.Fatalf("traits type = %q", byName[legacyFieldSelfTraits].Type)
	}
	city := byName[legacyFieldCity]
	if city.Type != "region" || city.RegionLevel != "pc" || city.Value != "浙江杭州" ||
		city.ProvinceCode != "33" || city.CityCode != "3301" || !city.Required {
		t.Fatalf("city region field = %+v", city)
	}
	churchLocation := byName[legacyFieldChurchLocation]
	if churchLocation.Type != "region" || churchLocation.RegionLevel != "cd" || churchLocation.Value != "杭州上城" ||
		churchLocation.ProvinceCode != "33" || churchLocation.CityCode != "3301" || churchLocation.AreaCode == "" {
		t.Fatalf("church region field = %+v", churchLocation)
	}
	if got := byName[legacyFieldMBTI].Value.([]string); len(got) != 2 || got[0] != "I" {
		t.Fatalf("mbti = %#v", got)
	}
}

func TestLegacyProfileEditableObserverOnlyIDCard(t *testing.T) {
	observer := db.Profile{AccountKind: "observer", AccountStatus: "observer"}
	fields := legacyProfileEditable(observer)
	if len(fields) != 1 || fields[0].Field != legacyFieldIDCard || fields[0].Value != "" {
		t.Fatalf("observer editable = %+v", fields)
	}
	observer.IDCardValid = true
	if got := legacyProfileEditable(observer); len(got) != 0 {
		t.Fatalf("verified observer editable = %+v", got)
	}
	if !legacyIDCardOnlyBody(map[string]any{"身份证号": "x"}) ||
		!legacyIDCardOnlyBody(map[string]any{"id_card": "x"}) {
		t.Fatal("ID-card-only payload rejected")
	}
	if legacyIDCardOnlyBody(map[string]any{"身份证号": "x", "昵称": "y"}) ||
		legacyIDCardOnlyBody(map[string]any{"昵称": "y"}) {
		t.Fatal("mixed payload accepted for observer")
	}
}

func TestIDCardGateAllowlistCannotBeBypassedByDotSegments(t *testing.T) {
	cases := []string{
		"/legacy/api/notifications/../cards",
		"/legacy/api/home/../like",
		"/legacy/api/profile/../activities",
	}
	for _, raw := range cases {
		p := path.Clean(raw)
		if !idCardGateApplies(p) || idCardGateAllowed(p) {
			t.Fatalf("%s cleaned to %s: applies=%v allowed=%v", raw, p, idCardGateApplies(p), idCardGateAllowed(p))
		}
	}
	for _, p := range []string{"/legacy/api/notifications", "/legacy/api/notifications/stream", "/legacy/api/profile"} {
		if !idCardGateAllowed(p) {
			t.Fatalf("%s should be allowed", p)
		}
	}
}

func TestLegacyProfileEditableNoAreaCity(t *testing.T) {
	fields := legacyProfileEditable(db.Profile{City: "台湾台北", ChurchLocation: "台湾台北"})
	byName := map[string]legacyEditableField{}
	for _, field := range fields {
		byName[field.Field] = field
	}
	city := byName[legacyFieldCity]
	if city.Type != "region" || city.Value != "台湾台北" || city.ProvinceCode == "" || city.CityCode == "" {
		t.Fatalf("no-area city field = %+v", city)
	}
	church := byName[legacyFieldChurchLocation]
	if church.Type != "region" || church.Value != "台湾台北" || church.AreaCode != "" || church.CityCode == "" {
		t.Fatalf("no-area church field = %+v", church)
	}
}

func TestLegacyProfileUpdateMapsChineseFields(t *testing.T) {
	body := decodeLegacyMap(t, `{
		"昵称": " 新昵称 ",
		"身高（cm）": "170",
		"学历": "硕士",
		"现居/工作城市": "上海",
		"经常去的教堂": "南头堂",
		"参加的团体": "青年组",
		"教堂所在市-区": "上城区",
		"从事行业": "教育",
		"职位": "老师",
		"年收入": "10W - 20W",
		"房产状况": "有",
		"是否有车": "无",
		"我是一个怎样的人": "慢热",
		"我是一个怎样的人-性格": ["正直", " 理性待事 "],
		"我是一个怎样的人-爱好": ["看书"],
		"我是一个怎样的人-运动": [],
		"我是个怎样的人-MBTI人格": ["I", "N"],
		"婚后是否与父母同住": "独立生活",
		"家庭成员情况": "三口之家",
		"理想中的TA": "温柔",
		"理想中的TA-性格": ["温柔"],
		"理想中的TA-爱好": ["做饭"],
		"理想中的TA-运动": ["慢走"],
		"微信号": "wx-new",
		"手机号": "13800000000",
		"unknown": "ignored"
	}`)
	upd, err := legacyProfileUpdateFromJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Empty() {
		t.Fatal("update is empty")
	}
	if upd.Nickname == nil || *upd.Nickname != "新昵称" {
		t.Fatalf("nickname = %#v", upd.Nickname)
	}
	if upd.HeightCM == nil || *upd.HeightCM != 170 {
		t.Fatalf("height = %#v", upd.HeightCM)
	}
	if upd.Education == nil || *upd.Education != "硕士" {
		t.Fatalf("education = %#v", upd.Education)
	}
	if upd.Driving == nil || *upd.Driving != "无" {
		t.Fatalf("driving = %#v", upd.Driving)
	}
	if upd.SelfTraits == nil || len(*upd.SelfTraits) != 2 || (*upd.SelfTraits)[1] != "理性待事" {
		t.Fatalf("traits = %#v", upd.SelfTraits)
	}
	if upd.SelfSports == nil || len(*upd.SelfSports) != 0 {
		t.Fatalf("sports = %#v", upd.SelfSports)
	}
	if upd.Phone == nil || *upd.Phone != "13800000000" {
		t.Fatalf("phone = %#v", upd.Phone)
	}
	if upd.PartnerCriteria == nil || *upd.PartnerCriteria != "温柔" {
		t.Fatalf("partner criteria = %#v", upd.PartnerCriteria)
	}
}

func TestLegacyProfileUpdateRejectsMalformedValues(t *testing.T) {
	cases := []string{
		`{"身高（cm）": "一米七"}`,
		`{"昵称": 123}`,
		`{"我是一个怎样的人-性格": "正直"}`,
		`{"我是一个怎样的人-爱好": [1]}`,
		`{"现居/工作城市": "不存在市"}`,
		`{"教堂所在市-区": "广东"}`,
	}
	for _, raw := range cases {
		if _, err := legacyProfileUpdateFromJSON(decodeLegacyMap(t, raw)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestLegacyProfileUpdateIgnoresUnknownOnly(t *testing.T) {
	upd, err := legacyProfileUpdateFromJSON(decodeLegacyMap(t, `{"foo":"bar"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !upd.Empty() {
		t.Fatalf("expected empty update: %+v", upd)
	}
}

// samePhone 必须与登录/注册用同一套归一化：带 +86、空格或分隔符的同一个
// 号码不能被当成换号而要求输入当前密码。
func TestSamePhoneNormalizesLikeLogin(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"国家码与空格", "+86 180 5856 7994", "18058567994", true},
		{"连字符分隔", "180-5856-7994", "18058567994", true},
		{"0086 前缀", "0086 180 5856 7994", "18058567994", true},
		{"两侧空格", " 18058567994 ", "18058567994", true},
		{"不同号码", "18058567994", "18058567995", false},
		{"非法值只匹配自身", "not-a-phone", "not-a-phone", true},
		{"非法值不等于空", "not-a-phone", "", false},
		{"合法值不等于非法值", "18058567994", "not-a-phone", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := samePhone(tc.a, tc.b); got != tc.want {
				t.Fatalf("samePhone(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := samePhone(tc.b, tc.a); got != tc.want {
				t.Fatalf("samePhone(%q, %q) = %v, want %v (not symmetric)", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestLegacyPhotoWantsCover(t *testing.T) {
	parse := func(body string) bool {
		req := httptest.NewRequest("POST", "/legacy/api/profile/photo",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return legacyPhotoWantsCover(req)
	}
	if !parse(url.Values{"cover": {"1"}}.Encode()) {
		t.Fatal("cover=1 should request cover")
	}
	if !parse(url.Values{"index": {"0"}}.Encode()) {
		t.Fatal("index=0 should request cover")
	}
	if parse(url.Values{"index": {"1"}}.Encode()) {
		t.Fatal("index=1 must not move the cover")
	}
	if parse("") {
		t.Fatal("empty form must not move the cover")
	}
}
