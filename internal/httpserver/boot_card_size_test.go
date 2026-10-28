package httpserver

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// TestBootCardGzipBudget keeps the server-rendered first card within the shell
// allowance documented in tools/perf/budgets.json. The full first card + two
// compact next cards are the worst case.
func TestBootCardGzipBudget(t *testing.T) {
	payload := bootCardFixture()
	card := payload["cards"].([]map[string]any)[0]
	card["display_fields"] = CardDisplayFields{
		Simple: "30岁 · 上海 · 本科 · 165cm · 基督徒",
		Sections: []CardFieldSection{
			{Title: "基本信息", Icon: "👤", Fields: []CardFieldItem{
				{Label: "现居/工作城市", Value: "上海"},
				{Label: "家乡", Value: "浙江杭州"},
				{Label: "经常去的教堂", Value: "上海某天主教堂"},
				{Label: "参加的团体", Value: "青年团契"},
				{Label: "教堂所在市-区", Value: "上海市-徐汇区"},
			}},
			{Title: "工作与经济", Icon: "💼", Fields: []CardFieldItem{
				{Label: "从事行业", Value: "教育"},
				{Label: "职位", Value: "教师"},
				{Label: "年收入", Value: "20-30万"},
				{Label: "房产状况", Value: "有房无贷"},
				{Label: "汽车", Value: "有车"},
			}},
			{Title: "关于我", Icon: "✨", Fields: []CardFieldItem{
				{Label: "我是怎样的人", Value: "温和、喜欢阅读、愿意倾听，也喜欢和朋友一起做饭。"},
				{Label: "性格", Value: "内向、细心、有耐心"},
				{Label: "爱好", Value: "阅读、旅行、烘焙、摄影"},
				{Label: "运动", Value: "游泳、羽毛球"},
				{Label: "MBTI", Value: "INFJ"},
				{Label: "婚后与父母同住", Value: "否"},
				{Label: "家庭情况", Value: "父母健康，有一个弟弟"},
			}},
			{Title: "理想中的TA", Icon: "💖", Fields: []CardFieldItem{
				{Label: "理想中的TA", Value: "希望对方真诚、善良、愿意沟通，一起成长。"},
				{Label: "性格", Value: "开朗、包容"},
				{Label: "爱好", Value: "运动、音乐"},
				{Label: "运动", Value: "跑步"},
			}},
		},
	}
	cards := payload["cards"].([]map[string]any)
	for i := 1; i < 3; i++ {
		cards = append(cards, map[string]any{
			"openid":   "openid-compact-" + string(rune('0'+i)),
			"nickname": "候选" + string(rune('0'+i)),
			"gender":   "女性",
			"photo":    "/cards/photo/compact" + string(rune('0'+i)) + "?size=thumb",
			"photos":   []string{"/cards/photo/compact" + string(rune('0'+i))},
			"display_fields": CardDisplayFields{
				Simple: "31岁 · 杭州 · 硕士",
			},
		})
	}
	payload["cards"] = cards
	out := renderBootCardForTest(t, payload)
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte(out)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("boot card raw=%d gzip=%d", len(out), buf.Len())
	if buf.Len() > 2600 {
		t.Fatalf("boot card gzip=%d exceeds 2600 bytes", buf.Len())
	}
}
