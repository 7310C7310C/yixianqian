package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeBootstrapStripsSensitiveKeys(t *testing.T) {
	payload := map[string]any{
		"user": map[string]any{
			"nickname": "小明",
			"phone":    "13800001111",
			"wechat":   "wx-secret",
			"id_valid": true,
		},
		"cards": []map[string]any{
			{"nickname": "甲", "phone": "13900002222", "wechat": "wx-a"},
			{"nickname": "乙", "nested": map[string]any{"id_card_no": "110101199001010011"}},
		},
		"likes": map[string]any{
			"mutual": []any{
				map[string]any{"nickname": "丙", "wechat_id": "wx-c"},
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
		"13800001111", "13900002222", "wx-secret", "wx-a", "wx-c", "110101199001010011"} {
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
