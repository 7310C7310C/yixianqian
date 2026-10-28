package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRewriteBootstrapHeroPhoto(t *testing.T) {
	payload := map[string]any{"cards": []map[string]any{{
		"photo":        "/cards/photo/a?size=thumb",
		"photos_thumb": []string{"/cards/photo/a?size=thumb"},
	}}}
	rewriteBootstrapHeroPhoto(payload)
	card := payload["cards"].([]map[string]any)[0]
	if card["photo"] != "/cards/photo/a?size=hero" {
		t.Fatalf("photo=%v", card["photo"])
	}
	if got := card["photos_thumb"].([]string)[0]; got != "/cards/photo/a?size=hero" {
		t.Fatalf("photos_thumb[0]=%v", got)
	}
}

func TestSanitizeBootstrapStripsSensitiveKeys(t *testing.T) {
	payload := map[string]any{
		"user": map[string]any{
			"nickname":       "小明",
			"phone":          "13800001111",
			"wechat":         "wx-secret",
			"id_valid":       true,
			"wechat_payment": "wx-pay",
			"telephone":      "010-88886666",
			"id_card_last4":  "0011",
			"IdCardHash":     "hash-should-stay-hidden",
		},
		"cards": []map[string]any{
			{"nickname": "甲", "phone": "13900002222", "wechat": "wx-a"},
			{"nickname": "乙", "nested": map[string]any{"id_card_no": "110101199001010011"}},
		},
		"likes": map[string]any{
			"mutual": []any{
				map[string]any{
					"nickname":     "丙",
					"wechat_id":    "wx-c",
					"my_wechat":    "wx-mine",
					"their_wechat": "wx-theirs",
				},
			},
		},
		"cards_total": 2,
	}

	raw, err := json.Marshal(sanitizeBootstrap(payload))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, banned := range []string{`"phone"`, `"wechat"`, `"wechat_id"`, `"id_card_no"`,
		`"wechat_payment"`, `"telephone"`, `"id_card_last4"`, `"IdCardHash"`,
		`"my_wechat"`, `"their_wechat"`,
		"13800001111", "13900002222", "wx-secret", "wx-a", "wx-c", "wx-mine", "wx-theirs",
		"wx-pay", "010-88886666", "0011", "hash-should-stay-hidden", "110101199001010011"} {
		if strings.Contains(body, banned) {
			t.Fatalf("bootstrap JSON still contains %s: %s", banned, body)
		}
	}
	// 非敏感字段必须保留，避免误删把首页数据打空。
	for _, keep := range []string{`"nickname"`, `"id_valid"`, `"cards_total"`, "小明", "甲", "丙"} {
		if !strings.Contains(body, keep) {
			t.Fatalf("bootstrap JSON lost %s: %s", keep, body)
		}
	}
}
