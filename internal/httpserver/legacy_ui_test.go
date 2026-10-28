package httpserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLegacyShellBootstrapCardLimits(t *testing.T) {
	src, err := os.ReadFile("legacy_ui.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bootstrapLimit := 20",
		`r.URL.Query().Get("native_admin") == "1"`,
		"bootstrapLimit = 3",
		"bootCardFromPayloadLimit(sanitized, 3)",
	} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("legacy shell bootstrap limit marker %q missing", want)
		}
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
