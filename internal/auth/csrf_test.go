package auth

import (
	"strings"
	"testing"
	"time"
)

func TestCSRFIssueAndVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	csrf := newCSRFWithClock([]byte("0123456789abcdef0123456789abcdef"), time.Hour,
		func() time.Time { return now })

	token, err := csrf.Issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !csrf.Verify(token) {
		t.Fatal("fresh token should verify")
	}
	if csrf.Verify(token + "x") {
		t.Fatal("tampered token should fail")
	}
	if csrf.Verify("") || csrf.Verify("a.b.c") || csrf.Verify("not a token") {
		t.Fatal("malformed tokens should fail")
	}

	now = now.Add(time.Hour + time.Second)
	if csrf.Verify(token) {
		t.Fatal("expired token should fail")
	}
}

func TestCSRFKeyIsolation(t *testing.T) {
	now := time.Now
	a := newCSRFWithClock([]byte("key-a"), time.Hour, now)
	b := newCSRFWithClock([]byte("key-b"), time.Hour, now)
	token, err := a.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if b.Verify(token) {
		t.Fatal("a token from another key must not verify")
	}
}

func TestCSRFEmptyKeyDisabled(t *testing.T) {
	csrf := NewCSRF(nil, time.Hour)
	if _, err := csrf.Issue(); err == nil {
		t.Fatal("expected error for empty key")
	}
	if csrf.Verify("anything") {
		t.Fatal("empty key must never verify")
	}
}

func TestCSRFTokenShape(t *testing.T) {
	csrf := NewCSRF([]byte(strings.Repeat("k", 32)), time.Hour)
	token, err := csrf.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Split(token, "."); len(parts) != 2 {
		t.Fatalf("unexpected token shape: %q", token)
	}
}
