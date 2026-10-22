package auth

import (
	"testing"
	"time"
)

func TestLimiterFixedWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(3, time.Minute, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		ok, _ := l.Allow("k")
		if !ok {
			t.Fatalf("event %d should be allowed", i+1)
		}
	}
	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("fourth event should be denied")
	}
	if retry != time.Minute {
		t.Fatalf("retry = %v, want 1m", retry)
	}

	ok, _ = l.Allow("other")
	if !ok {
		t.Fatal("different keys are independent")
	}

	now = now.Add(time.Minute)
	ok, _ = l.Allow("k")
	if !ok {
		t.Fatal("window should reset")
	}
}

func TestLimiterDisabled(t *testing.T) {
	var l *Limiter
	if ok, _ := l.Allow("x"); !ok {
		t.Fatal("nil limiter allows")
	}
	empty := NewLimiter(0, time.Minute)
	if ok, _ := empty.Allow("x"); !ok {
		t.Fatal("zero max allows")
	}
}
