package platform

import (
	"regexp"
	"testing"
)

func TestNewUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id, err := NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Fatalf("invalid uuid: %s", id)
		}
		if seen[id] {
			t.Fatalf("duplicate uuid: %s", id)
		}
		seen[id] = true
	}
}

func TestRandomToken(t *testing.T) {
	tok, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 {
		t.Fatalf("len = %d", len(tok))
	}
	if _, err := RandomToken(0); err == nil {
		t.Fatal("expected error for zero length")
	}
}

func TestNewRequestID(t *testing.T) {
	id := NewRequestID()
	if len(id) != 16 {
		t.Fatalf("len = %d", len(id))
	}
	if id == NewRequestID() {
		t.Fatal("request ids should differ")
	}
}
