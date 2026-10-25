package httpserver

import (
	"context"
	"errors"
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
			value, err := group.Do(context.Background(), "user:1|role:user|q=", fn)
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
	if got, err := group.Do(context.Background(), "user:2|role:user|q=", func() (any, error) { return "other", nil }); err != nil || got != "other" {
		t.Fatalf("different key result = %v err=%v", got, err)
	}
}

// TestInflightWaiterHonoursOwnContext covers reviewer N3: the leader being
// cancelled (user switches page) must not fail other requests sharing the key.
func TestInflightWaiterHonoursOwnContext(t *testing.T) {
	var group inflightGroup
	release := make(chan struct{})
	leaderStarted := make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())

	leaderDone := make(chan error, 1)
	go func() {
		_, err := group.Do(leaderCtx, "k", func() (any, error) {
			close(leaderStarted)
			<-release
			return "value", nil // even a successful build is irrelevant if leader ctx dies
		})
		leaderDone <- err
	}()
	<-leaderStarted

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := group.Do(waiterCtx, "k", func() (any, error) { return "unused", nil })
		waiterDone <- err
	}()

	// The waiter leaving on its own ctx must not block the leader (or vice versa).
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter blocked on leader even after its own context was cancelled")
	}

	cancelLeader()
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader err = %v, want nil", err)
	}

	// The group must be reusable afterwards (no stuck in-flight entry).
	if got, err := group.Do(context.Background(), "k", func() (any, error) { return "again", nil }); err != nil || got != "again" {
		t.Fatalf("post-cancel result = %v err=%v", got, err)
	}
}
