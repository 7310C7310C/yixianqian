package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestLikedMeAPIHidesAnonymousIdentity(t *testing.T) {
	overview := db.SocialOverview{
		LikesMe: []db.LikePair{
			{
				Like:   db.Like{ID: 1, LikeType: db.LikeTypeAnon, Status: db.LikeStatusSingle, CreatedAt: time.Now()},
				Other:  db.SocialProfile{ID: 42, Nickname: "秘密", LegacyID: "U-0042"},
				Active: true,
			},
			{
				Like:   db.Like{ID: 2, LikeType: db.LikeTypeReal, Status: db.LikeStatusSingle, CreatedAt: time.Now()},
				Other:  db.SocialProfile{ID: 43, Nickname: "实名", LegacyID: "U-0043"},
				Active: true,
			},
			{
				Like:   db.Like{ID: 3, LikeType: db.LikeTypeReal, Status: db.LikeStatusMutual},
				Other:  db.SocialProfile{ID: 44, Nickname: "配对"},
				Mutual: true, Active: true,
			},
		},
	}
	items := likedMeAPI(overview)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	anonItem := items[0]
	if anonItem.Nickname != "匿名用户" || anonItem.InitiatorOpenID != "" || anonItem.UserID != "" || anonItem.TargetID != 0 {
		t.Fatalf("anonymous item leaked identity: %+v", anonItem)
	}
	if anonItem.Message != "" {
		t.Fatal("anonymous like message leaked")
	}
	if anonItem.Real {
		t.Fatal("anonymous item marked real")
	}
	if !items[1].Real || items[1].Nickname != "实名" || items[1].InitiatorOpenID != "U-0043" {
		t.Fatalf("real item mismatch: %+v", items[1])
	}
}

func TestMutualAPIContactConsent(t *testing.T) {
	overview := db.SocialOverview{
		Me: db.SocialProfile{ID: 1, ContactConsent: true, Wechat: "my-wx"},
		LikesMe: []db.LikePair{{
			Like: db.Like{ID: 1, LikeType: db.LikeTypeReal, Status: db.LikeStatusMutual},
			Other: db.SocialProfile{ID: 2, Nickname: "对方", LegacyID: "U-0002",
				ContactConsent: false, Wechat: "hidden-wx"},
			Mutual: true, Active: true,
		}},
	}
	items := mutualAPI(overview)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].MyWechat != "my-wx" {
		t.Fatalf("my wechat = %q", items[0].MyWechat)
	}
	if items[0].TheirWechat != "" {
		t.Fatalf("their wechat leaked without consent: %q", items[0].TheirWechat)
	}
	overview.LikesMe[0].Other.ContactConsent = true
	items = mutualAPI(overview)
	if items[0].TheirWechat != "hidden-wx" {
		t.Fatalf("their wechat = %q, want visible after consent", items[0].TheirWechat)
	}
}

func TestCardTargetGenderAndGates(t *testing.T) {
	male := db.SocialViewer{ID: 1, Gender: db.GenderMale, AccountKind: "user", AccountStatus: "active", Status: "active"}
	female := db.SocialViewer{ID: 2, Gender: db.GenderFemale, AccountKind: "user", AccountStatus: "active", Status: "active"}
	observer := db.SocialViewer{ID: 3, AccountKind: "observer", AccountStatus: "observer", Status: "active"}

	if got, err := socialTargetGender(male, "男性"); err != nil || got != db.GenderFemale {
		t.Fatalf("male target = %q,%v", got, err)
	}
	if got, err := socialTargetGender(female, "男性"); err != nil || got != db.GenderMale {
		t.Fatalf("female target = %q,%v", got, err)
	}
	if got, err := socialTargetGender(observer, ""); err != nil || got != "" {
		t.Fatalf("observer target = %q,%v", got, err)
	}
	if got, err := socialTargetGender(observer, db.GenderMale); err != nil || got != db.GenderMale {
		t.Fatalf("observer filtered target = %q,%v", got, err)
	}
	if _, err := socialTargetGender(db.SocialViewer{}, ""); err != errGenderMissing {
		t.Fatalf("missing gender err = %v", err)
	}

	if _, _, ok := socialAccountState(male, true); !ok {
		t.Fatal("active user should interact")
	}
	if _, _, ok := socialAccountState(db.SocialViewer{AccountKind: "user", AccountStatus: "matched", Status: "active"}, false); !ok {
		t.Fatal("matched user should browse")
	}
	if _, _, ok := socialAccountState(db.SocialViewer{AccountKind: "user", AccountStatus: "matched", Status: "active"}, true); ok {
		t.Fatal("matched user should not like")
	}
	if _, _, ok := socialAccountState(observer, true); ok {
		t.Fatal("observer should not like")
	}
	if _, _, ok := socialAccountState(db.SocialViewer{AccountKind: "user", AccountStatus: "pending", Status: "active"}, false); ok {
		t.Fatal("pending user should not browse")
	}
}

func TestCardFilterParsingAndSeed(t *testing.T) {
	q := url.Values{
		"nickname": {"  小美  "}, "age_min": {"30"}, "age_max": {"999"},
		"height_min": {"-1"}, "education": {"本科", "", "本科"},
	}
	f := parseCardFilter(q)
	if f.Nickname != "小美" || f.AgeMin != 30 || f.AgeMax != 0 || f.HeightMin != 0 {
		t.Fatalf("filter = %+v", f)
	}
	if len(f.Education) != 2 {
		t.Fatalf("education = %v", f.Education)
	}
	if !cardFilterActive(f) || cardFilterActive(db.CardFilter{}) {
		t.Fatal("filterActive mismatch")
	}

	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if got := socialSeed("abc-123", 7, now); got != "abc-123" {
		t.Fatalf("valid seed = %q", got)
	}
	if got := socialSeed("bad seed", 7, now); got != "7-2026-10-21" {
		t.Fatalf("fallback seed = %q", got)
	}
	v1 := cardVersion("abc", f, db.GenderFemale)
	v2 := cardVersion("abc", f, db.GenderFemale)
	if v1 != v2 {
		t.Fatal("card version is not deterministic")
	}
	if v1 == cardVersion("xyz", f, db.GenderFemale) {
		t.Fatal("card version should change with seed")
	}
}

func TestSecurityHeadersAllowSameOriginScripts(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodGet, "/cards", nil)
	rec := httptest.NewRecorder()
	app.withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP must allow same-origin scripts, got %q", csp)
	}
	if strings.Contains(csp, "script-src 'none'") {
		t.Fatalf("CSP still blocks scripts: %q", csp)
	}
}

func TestCardsReturnURLKeepsPositionAndDropsOldFlash(t *testing.T) {
	form := url.Values{
		"return_to": {"/cards?city=%E6%B7%B1%E5%9C%B3&offset=20&seed=abc&index=4&liked=1"},
		"offset":    {"20"},
		"seed":      {"abc"},
		"index":     {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/cards/praise", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got := cardsReturnURL(req, "praised", "1")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse return URL: %v", err)
	}
	if parsed.Path != "/cards" {
		t.Fatalf("path = %q", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("offset") != "20" || q.Get("seed") != "abc" || q.Get("index") != "1" {
		t.Fatalf("position lost: %q", got)
	}
	if q.Get("city") != "深圳" || q.Get("praised") != "1" {
		t.Fatalf("filter/flash lost: %q", got)
	}
	if q.Get("liked") != "" {
		t.Fatalf("old flash leaked: %q", got)
	}

	bad := httptest.NewRequest(http.MethodPost, "/cards/praise",
		strings.NewReader(url.Values{"return_to": {"https://evil.example/cards"}, "index": {"2"}}.Encode()))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := cardsReturnURL(bad, "praised", "1"); strings.Contains(got, "evil") || got != "/cards?index=2&praised=1" {
		t.Fatalf("unsafe return_to accepted: %q", got)
	}
}
