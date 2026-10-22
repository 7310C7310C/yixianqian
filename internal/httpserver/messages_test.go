package httpserver

import (
	"encoding/json"
	"testing"
)

func TestMessageGateMatrix(t *testing.T) {
	t.Parallel()
	postCases := []struct {
		status  string
		blocked bool
	}{
		{"active", false},
		{"observer", false},
		{"matched", true},
		{"pending", true},
		{"rejected", true},
		{"left", true},
		{"disabled", true},
		{"", false},
	}
	for _, tc := range postCases {
		if _, blocked := messagePostBlock(tc.status); blocked != tc.blocked {
			t.Errorf("messagePostBlock(%q) blocked = %v, want %v", tc.status, blocked, tc.blocked)
		}
	}
	viewCases := []struct {
		status  string
		blocked bool
	}{
		{"active", false},
		{"observer", false},
		{"matched", false},
		{"pending", true},
		{"rejected", true},
		{"left", true},
		{"disabled", true},
	}
	for _, tc := range viewCases {
		if _, blocked := messageViewBlock(tc.status); blocked != tc.blocked {
			t.Errorf("messageViewBlock(%q) blocked = %v, want %v", tc.status, blocked, tc.blocked)
		}
	}
}

func TestParseOptionalIDJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want *int64
		bad  bool
	}{
		{`null`, nil, false},
		{`""`, nil, false},
		{`42`, ptrInt64(42), false},
		{`"42"`, ptrInt64(42), false},
		{`0`, nil, true},
		{`"x"`, nil, true},
		{`{}`, nil, true},
	}
	for _, tc := range cases {
		got, err := parseOptionalIDJSON(json.RawMessage(tc.raw))
		if tc.bad {
			if err == nil {
				t.Errorf("parseOptionalIDJSON(%s) expected error, got %v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseOptionalIDJSON(%s) error: %v", tc.raw, err)
			continue
		}
		if tc.want == nil {
			if got != nil {
				t.Errorf("parseOptionalIDJSON(%s) = %d, want nil", tc.raw, *got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			t.Errorf("parseOptionalIDJSON(%s) = %v, want %d", tc.raw, got, *tc.want)
		}
	}
}

func ptrInt64(v int64) *int64 { return &v }
