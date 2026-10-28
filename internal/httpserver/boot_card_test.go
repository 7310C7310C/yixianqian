package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func bootCardFixture() map[string]any {
	return map[string]any{
		"cards_total": 7,
		"user": map[string]any{
			"anon_left":   3,
			"real_left":   2,
			"is_observer": false,
		},
		"cards": []map[string]any{
			{
				"nickname":     "小美",
				"gender":       "女性",
				"photo":        "/cards/photo/a?size=thumb",
				"photos":       []string{"/cards/photo/a", "/cards/photo/b"},
				"photos_thumb": []string{"/cards/photo/a?size=thumb", "/cards/photo/b?size=thumb"},
				"recommended":  true,
				"msg_count":    2,
				"faces": []any{
					map[string]any{"c": []any{0.5, 0.4}, "box": []any{0.2, 0.3}},
				},
				"display_fields": CardDisplayFields{
					Simple: "30岁 · 上海 · 本科",
					Sections: []CardFieldSection{
						{Title: "基本信息", Icon: "👤", Fields: []CardFieldItem{
							{Label: "现居/工作城市", Value: "上海"},
							{Label: "家乡", Value: "杭州"},
						}},
						{Title: "关于我", Icon: "✨", Fields: []CardFieldItem{
							{Label: "我是怎样的人", Value: "温和、喜欢阅读"},
						}},
					},
				},
			},
		},
	}
}

func renderBootCardForTest(t *testing.T, payload map[string]any) string {
	t.Helper()
	data, ok := bootCardFromPayload(payload)
	if !ok {
		t.Fatal("bootCardFromPayload returned false")
	}
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	var buf bytes.Buffer
	if err := tpl.RenderBootCard(&buf, data); err != nil {
		t.Fatalf("RenderBootCard: %v", err)
	}
	return buf.String()
}

func TestBootCardRendersCompleteFirstCard(t *testing.T) {
	out := renderBootCardForTest(t, bootCardFixture())
	for _, want := range []string{
		`id="boot-card"`,
		`小美`,
		`30岁 · 上海 · 本科`,
		`1 / 7`,
		`💕 匿名 3 · 实名 2`,
		`推荐`,
		`/cards/photo/a?size=thumb`,
		`data-faces=`,
		`现居/工作城市`,
		`我是怎样的人`,
		`info-grid-item full`,
		`反馈`,
		`♥`,
		`>2</span>`,
		`牵线`,
		`我的`,
		`boot-tabbar`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("boot card missing %q", want)
		}
	}
}

func TestBootCardMiniDeck(t *testing.T) {
	payload := bootCardFixture()
	first := payload["cards"].([]map[string]any)[0]
	payload["cards"] = []map[string]any{
		first,
		{
			"openid":    "openid-second",
			"nickname":  "小芳",
			"gender":    "女性",
			"photo":     "/cards/photo/b?size=thumb",
			"photos":    []string{"/cards/photo/b"},
			"msg_count": 4,
			"display_fields": CardDisplayFields{
				Simple: "31岁 · 杭州 · 硕士",
				Sections: []CardFieldSection{
					{Title: "基本信息", Icon: "👤", Fields: []CardFieldItem{{Label: "现居/工作城市", Value: "杭州"}}},
				},
			},
		},
		{
			"openid":   "openid-third",
			"nickname": "小云",
			"gender":   "女性",
			"photo":    "/cards/photo/c?size=thumb",
			"photos":   []string{"/cards/photo/c"},
		},
	}
	data, ok := bootCardFromPayload(payload)
	if !ok {
		t.Fatal("bootCardFromPayload returned false")
	}
	if len(data.Cards) != 3 {
		t.Fatalf("mini deck cards=%d, want 3", len(data.Cards))
	}
	if data.Cards[0].Compact || !data.Cards[1].Compact || !data.Cards[2].Compact {
		t.Fatalf("compact flags = %v/%v/%v", data.Cards[0].Compact, data.Cards[1].Compact, data.Cards[2].Compact)
	}
	if data.Cards[1].OpenID != "openid-second" || data.Cards[2].OpenID != "openid-third" {
		t.Fatalf("mini deck openids = %q/%q", data.Cards[1].OpenID, data.Cards[2].OpenID)
	}
	if len(data.Cards[1].Sections) != 0 {
		t.Fatalf("compact card should not render profile sections")
	}
	out := renderBootCardForTest(t, payload)
	for _, want := range []string{
		`data-openid="openid-second"`, `data-openid="openid-third"`,
		`data-total="7"`, `--boot-cards:3`, `小芳`, `31岁 · 杭州 · 硕士`,
		`boot-deck-slide is-active`, `boot-line`, `data-src="/cards/photo/c?size=thumb"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mini deck missing %q", want)
		}
	}
	if strings.Count(out, "card-section") != 2 {
		t.Fatalf("profile sections rendered %d times, want 2 (first card only)", strings.Count(out, "card-section"))
	}
}

func TestBootCardEscapesUserContent(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	card["nickname"] = `<script>alert(1)</script>`
	card["display_fields"] = CardDisplayFields{
		Simple:   `<img src=x onerror=alert(1)>`,
		Sections: []CardFieldSection{{Title: `<b>标题</b>`, Icon: "👤", Fields: []CardFieldItem{{Label: "简介", Value: `<svg onload=alert(1)>`}}}},
	}
	out := renderBootCardForTest(t, payload)
	for _, banned := range []string{"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "<svg onload=alert(1)>", "<b>标题</b>"} {
		if strings.Contains(out, banned) {
			t.Fatalf("boot card did not escape %q", banned)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img", "&lt;svg", "&lt;b&gt;"} {
		if !strings.Contains(out, want) {
			t.Fatalf("boot card missing escaped marker %q", want)
		}
	}
}

func TestBootCardNoCards(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"empty": {"cards": []map[string]any{}, "cards_total": 0},
		"nil":   {},
		"wrong": {"cards": []any{map[string]any{"nickname": "x"}}},
		"gate":  {"cards": []any{}, "cards_total": 0, "user": map[string]any{"id_valid": false}},
	} {
		if _, ok := bootCardFromPayload(payload); ok {
			t.Fatalf("%s: bootCardFromPayload unexpectedly succeeded", name)
		}
	}
}

func TestBootCardObserverHidesHearts(t *testing.T) {
	payload := bootCardFixture()
	payload["user"] = map[string]any{"is_observer": true, "anon_left": 3, "real_left": 2}
	data, ok := bootCardFromPayload(payload)
	if !ok {
		t.Fatal("bootCardFromPayload returned false")
	}
	if data.ShowHearts {
		t.Fatal("observer boot card should hide hearts badge")
	}
	out := renderBootCardForTest(t, payload)
	if strings.Contains(out, "hearts-badge") {
		t.Fatal("observer boot card rendered hearts badge")
	}
}

func TestBootCardPhotoFallback(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	delete(card, "photo")
	data, ok := bootCardFromPayload(payload)
	if !ok {
		t.Fatal("bootCardFromPayload returned false")
	}
	if data.Cards[0].Photo != "/cards/photo/a?size=thumb" {
		t.Fatalf("photo fallback = %q", data.Cards[0].Photo)
	}
}

func TestBootCardSelfBadge(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	card["is_self"] = true
	card["recommended"] = false
	out := renderBootCardForTest(t, payload)
	if !strings.Contains(out, "这是你 · 他人视角") {
		t.Fatal("self boot card missing self badge")
	}
	if strings.Contains(out, ">推荐<") {
		t.Fatal("self boot card should prefer self badge over recommended")
	}
}

func TestBootCardNoPhotoPlaceholder(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	delete(card, "photo")
	delete(card, "photos")
	delete(card, "photos_thumb")
	payload["cards"] = []map[string]any{card}
	out := renderBootCardForTest(t, payload)
	if !strings.Contains(out, `class="card-photo-placeholder"`) || !strings.Contains(out, "👩") {
		t.Fatal("no-photo boot card missing gender placeholder")
	}
	if strings.Contains(out, "boot-photo") {
		t.Fatal("no-photo boot card should not render a boot-photo img")
	}
}

func TestBootCardSelfHasNoActionButtons(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	card["is_self"] = true
	card["recommended"] = false
	out := renderBootCardForTest(t, payload)
	for _, banned := range []string{"反馈", "♥"} {
		if strings.Contains(out, banned) {
			t.Fatalf("self boot card should not render action %q", banned)
		}
	}
}
