package db

import (
	"sync"
	"testing"
)

// newTestHub builds a hub without starting the LISTEN loop, so fan-out and
// subscription races can be exercised without a PostgreSQL connection.
func newTestHub() *NotificationHub {
	return &NotificationHub{subs: make(map[int64]map[*notificationSub]struct{})}
}

func drain(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestNotificationHubFanoutDeliversToUserOnly(t *testing.T) {
	h := newTestHub()
	aliceA, cancelA := h.Subscribe(7)
	defer cancelA()
	aliceB, cancelB := h.Subscribe(7)
	defer cancelB()
	bob, cancelBob := h.Subscribe(8)
	defer cancelBob()

	h.fanout(7)
	if !drain(aliceA) || !drain(aliceB) {
		t.Fatal("fanout(7) did not reach every subscriber of user 7")
	}
	if drain(bob) {
		t.Fatal("fanout(7) leaked to user 8")
	}
}

func TestNotificationHubFanoutCoalesces(t *testing.T) {
	h := newTestHub()
	ch, cancel := h.Subscribe(7)
	defer cancel()

	for i := 0; i < 5; i++ {
		h.fanout(7) // buffered size 1 + non-blocking send: must not block or pile up
	}
	if !drain(ch) {
		t.Fatal("expected at least one signal")
	}
	if drain(ch) {
		t.Fatal("buffer must coalesce repeated signals into one")
	}
}

func TestNotificationHubCancelIsIdempotent(t *testing.T) {
	h := newTestHub()
	_, cancel := h.Subscribe(7)
	cancel()
	cancel()
	h.fanout(7) // no subscribers left; must not panic
}

// TestNotificationHubFanoutConcurrentSubscribe is the regression test for the
// reviewer-reported fatal "concurrent map iteration and map write": fanout
// iterated the per-user map after RUnlock while cancel deleted from it.
//
// A few long-lived subscribers keep the per-user map non-empty and large enough
// that iteration overlaps the add/delete writers; run with -race (make
// test-race now covers internal/db).
func TestNotificationHubFanoutConcurrentSubscribe(t *testing.T) {
	h := newTestHub()
	// Stable subscribers: fanout must iterate a non-empty map on every call.
	for i := 0; i < 16; i++ {
		_, cancel := h.Subscribe(7)
		defer cancel()
	}
	for i := 0; i < 8; i++ {
		_, cancel := h.Subscribe(8)
		defer cancel()
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20000; j++ {
				h.fanout(7)
				h.broadcastAll()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20000; j++ {
				_, cancel := h.Subscribe(7)
				cancel()
				_, cancel2 := h.Subscribe(8)
				cancel2()
			}
		}()
	}
	wg.Wait()
}
