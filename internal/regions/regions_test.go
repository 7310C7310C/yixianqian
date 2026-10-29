package regions

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLookupLegacyStyle(t *testing.T) {
	cases := []struct {
		province, city, area string
		want                 string
	}{
		{"44", "4403", "", "广东深圳"},
		{"44", "4403", "440305", "深圳南山"},
		{"11", "1101", "", "北京"},
		{"11", "1101", "110101", "北京东城"},
		{"43", "4331", "", "湖南湘西"},
		{"41", "419001", "", "河南济源"},
		{"81", "8101", "", "香港"},
		{"81", "8101", "810101", "香港中西"},
		{"71", "7101", "", "台湾台北"},
	}
	for _, tc := range cases {
		var got string
		var ok bool
		if tc.area == "" {
			got, ok = LookupProvinceCity(tc.province, tc.city)
		} else {
			got, ok = LookupCityArea(tc.province, tc.city, tc.area)
		}
		if !ok || got != tc.want {
			t.Errorf("lookup(%s,%s,%s) = %q,%v want %q", tc.province, tc.city, tc.area, got, ok, tc.want)
		}
	}
}

func TestLookupRejectsMismatch(t *testing.T) {
	if _, ok := LookupProvinceCity("44", "1101"); ok {
		t.Fatal("广东省 + 北京 city should not validate")
	}
	if _, ok := LookupCityArea("44", "4403", "110101"); ok {
		t.Fatal("深圳 + 东城 should not validate")
	}
}

func TestJSONShape(t *testing.T) {
	var payload struct {
		Provinces []Province `json:"provinces"`
	}
	if err := json.Unmarshal(JSON(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Provinces) < 34 {
		t.Fatalf("provinces = %d, want >=34", len(payload.Provinces))
	}
	if strings.Contains(string(JSON()), `"value":""`) {
		t.Fatal("empty region value in payload")
	}
}

func TestNormalizeLegacyValues(t *testing.T) {
	cityCases := map[string]string{
		"广东深圳":  "广东深圳",
		"深圳":    "广东深圳",
		"广东-汕头": "广东汕头",
		"陕西-宝鸡": "陕西宝鸡",
		"北京":    "北京",
	}
	for input, want := range cityCases {
		got, ok := NormalizeCityValue(input)
		if !ok || got != want {
			t.Errorf("NormalizeCityValue(%q) = %q,%v want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "当前记录", "1", "广东", "不存在市"} {
		if _, ok := NormalizeCityValue(input); ok {
			t.Errorf("NormalizeCityValue(%q) should fail", input)
		}
	}

	areaCases := map[string]string{
		"深圳南山":  "深圳南山",
		"深圳南山区": "深圳南山",
		"北京东城区": "北京东城",
	}
	for input, want := range areaCases {
		got, ok := NormalizeAreaValue(input)
		if !ok || got != want {
			t.Errorf("NormalizeAreaValue(%q) = %q,%v want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "广东", "潮州", "南山区"} {
		if _, ok := NormalizeAreaValue(input); ok {
			t.Errorf("NormalizeAreaValue(%q) should fail", input)
		}
	}
	// Cities without county-level children are valid church locations; short
	// and full legacy names normalize to the canonical value.
	noArea := map[string]string{
		"河南济源": "河南济源", "济源": "河南济源", "济源市": "河南济源",
		"台湾台北": "台湾台北", "台北": "台湾台北", "台北市": "台湾台北",
	}
	for input, want := range noArea {
		if got, ok := NormalizeAreaValue(input); !ok || got != want {
			t.Errorf("NormalizeAreaValue(%q) = %q,%v want %q", input, got, ok, want)
		}
		if _, _, area, ok := LookupAreaValue(input); !ok || area != "" {
			t.Errorf("LookupAreaValue(%q) area=%q ok=%v, want empty area", input, area, ok)
		}
	}
	// Short names that collide across a district and a no-area city are never
	// guessed (新北 = 常州新北 or 台湾新北; 昌江 = 景德镇昌江 or 海南昌江).
	for _, input := range []string{"新北", "昌江", "新竹", "嘉义"} {
		if _, ok := NormalizeAreaValue(input); ok {
			t.Errorf("NormalizeAreaValue(%q) must be ambiguous", input)
		}
	}
	// Canonical duplicates are real dataset values but cannot preselect one code.
	if got, ok := NormalizeAreaValue("临夏临夏"); !ok || got != "临夏临夏" {
		t.Errorf("NormalizeAreaValue(临夏临夏) = %q,%v", got, ok)
	}
	if _, _, _, ok := LookupAreaValue("临夏临夏"); ok {
		t.Error("LookupAreaValue(临夏临夏) should not guess the duplicate code")
	}
	if province, city, ok := LookupCityValue("深圳"); !ok || province != "44" || city != "4403" {
		t.Errorf("LookupCityValue(深圳) = %q,%q,%v", province, city, ok)
	}
	if province, city, area, ok := LookupAreaValue("深圳南山区"); !ok || province != "44" || city != "4403" || area != "440305" {
		t.Errorf("LookupAreaValue = %q,%q,%q,%v", province, city, area, ok)
	}
}
