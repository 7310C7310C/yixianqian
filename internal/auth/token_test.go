package auth

import (
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if len(raw) < 40 {
		t.Fatalf("raw token too short: %d", len(raw))
	}
	if len(hash) != 64 {
		t.Fatalf("hash should be 64 hex chars, got %d", len(hash))
	}
	if strings.Contains(raw, "=") || strings.ContainsAny(raw, "+/") {
		t.Fatalf("token is not URL-safe: %q", raw)
	}
	if HashToken(raw) != hash {
		t.Fatal("HashToken must be deterministic")
	}
	raw2, hash2, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == raw2 || hash == hash2 {
		t.Fatal("tokens must be unique")
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"13800000006":        "13800000006",
		" 138-0000-0006 ":    "13800000006",
		"+8613800000006":     "13800000006",
		"008613800000006":    "13800000006",
		"(02) 1234 5678":     "0212345678",
		"123456":             "123456",
		"abc":                "",
		"123":                "",
		"":                   "",
		"13800000006 ext 12": "",
	}
	for input, want := range cases {
		if got := NormalizePhone(input); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("13800000006"); got != "138****06" {
		t.Fatalf("got %q", got)
	}
	if got := MaskPhone("12345"); got != "1***" {
		t.Fatalf("got %q", got)
	}
	if got := MaskPhone(""); got != "" {
		t.Fatalf("got %q", got)
	}
}
