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
