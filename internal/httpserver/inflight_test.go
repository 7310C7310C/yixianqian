package httpserver

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestInflightGroupCoalescesSameKeyOnly(t *testing.T) {
	var group inflightGroup
	release := make(chan struct{})
	var calls atomic.Int64
	fn := func() (any, error) {
		calls.Add(1)
		<-release
		return "value", nil
	}

	const waiters = 8
	results := make(chan any, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			value, err := group.Do("user:1|role:user|q=", fn)
			if err != nil {
				results <- err
				return
			}
			results <- value
		}()
	}
	// Give every goroutine a chance to join the in-flight call before releasing
	// the leader; the assertion below is about coalescing, not scheduling.
	time.Sleep(50 * time.Millisecond)
	close(release)
	for i := 0; i < waiters; i++ {
		if got := <-results; got != "value" {
			t.Fatalf("result = %v, want value", got)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("same-key calls = %d, want 1", got)
	}

	// A different key must execute independently.
	if got, err := group.Do("user:2|role:user|q=", func() (any, error) { return "other", nil }); err != nil || got != "other" {
		t.Fatalf("different key result = %v err=%v", got, err)
	}
}
