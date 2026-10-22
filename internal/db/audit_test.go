package db

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeText(t *testing.T) {
	if got := sanitizeText("short", 512); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("测", 400)
	got := sanitizeText(long, 512)
	if len(got) > 512 || !utf8.ValidString(got) {
		t.Fatalf("invalid truncation: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	invalid := string([]byte{0xff, 0xfe, 'a'})
	got = sanitizeText(invalid, 512)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "a") {
		t.Fatalf("invalid utf8 not repaired: %q", got)
	}
}
