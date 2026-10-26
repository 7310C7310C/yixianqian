package httpserver

import (
	"testing"
	"time"
)

func TestPhotoGrantStoreViewerScopedAndExpires(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	store := newPhotoGrantStore(15*time.Minute, 10, 10, func() time.Time { return now })

	store.Grant(7, []int64{101, 102})
	if !store.AllowedOwner(7, 101) || !store.AllowedOwner(7, 102) {
		t.Fatal("granted viewer must be allowed")
	}
	if store.AllowedOwner(8, 101) {
		t.Fatal("grant must not leak across viewers")
	}
	if store.AllowedOwner(7, 103) {
		t.Fatal("ungranted owner must be denied")
	}

	now = now.Add(15*time.Minute + time.Second)
	if store.AllowedOwner(7, 101) {
		t.Fatal("expired grant must be denied")
	}
}

func TestPhotoGrantStoreZeroTTLDisables(t *testing.T) {
	store := newPhotoGrantStore(0, 10, 10, time.Now)
	store.Grant(1, []int64{2})
	if store.AllowedOwner(1, 2) {
		t.Fatal("ttl=0 must fail closed")
	}
}

func TestPhotoGrantStoreBoundsTargetsAndKeepsFresh(t *testing.T) {
	now := time.Now()
	store := newPhotoGrantStore(time.Hour, 10, 3, func() time.Time { return now })
	store.Grant(1, []int64{1})
	now = now.Add(time.Second)
	store.Grant(1, []int64{2})
	now = now.Add(time.Second)
	store.Grant(1, []int64{3})
	now = now.Add(time.Second)
	store.Grant(1, []int64{4})
	now = now.Add(time.Second)
	store.Grant(1, []int64{5})
	if got := store.targetCount(1); got > 3 {
		t.Fatalf("per-viewer targets = %d, want <= 3", got)
	}
	// Oldest-expiry targets are evicted first; the freshly granted owners remain.
	if !store.AllowedOwner(1, 4) || !store.AllowedOwner(1, 5) {
		t.Fatal("fresh targets must survive cap eviction")
	}
	if store.AllowedOwner(1, 1) {
		t.Fatal("oldest target should be evicted first")
	}
}

func TestPhotoGrantStoreBoundsViewersByLRU(t *testing.T) {
	now := time.Now()
	store := newPhotoGrantStore(time.Hour, 2, 10, func() time.Time { return now })
	store.Grant(1, []int64{1})
	now = now.Add(time.Minute)
	store.Grant(2, []int64{2})
	now = now.Add(time.Minute)
	// Touch viewer 1 so viewer 2 becomes the LRU victim.
	if !store.AllowedOwner(1, 1) {
		t.Fatal("viewer 1 should still be allowed")
	}
	store.Grant(3, []int64{3})
	if got := store.viewerCount(); got > 2 {
		t.Fatalf("viewer count = %d, want <= 2", got)
	}
	if store.AllowedOwner(2, 2) {
		t.Fatal("LRU viewer should be evicted first")
	}
	if !store.AllowedOwner(1, 1) || !store.AllowedOwner(3, 3) {
		t.Fatal("recent viewers should survive")
	}
}

func TestPhotoGrantStoreRestartDropsGrants(t *testing.T) {
	now := time.Now()
	first := newPhotoGrantStore(time.Hour, 10, 10, func() time.Time { return now })
	first.Grant(1, []int64{2})
	if !first.AllowedOwner(1, 2) {
		t.Fatal("grant missing before restart")
	}
	second := newPhotoGrantStore(time.Hour, 10, 10, func() time.Time { return now })
	if second.AllowedOwner(1, 2) {
		t.Fatal("new process (store) must not inherit grants")
	}
}

func TestPhotoGrantStoreMergesDedupesAndBoundsWorstCase(t *testing.T) {
	now := time.Now()
	store := newPhotoGrantStore(time.Hour, 2, 2000, func() time.Time { return now })
	first := make([]int64, 0, 1500)
	for i := int64(1); i <= 1500; i++ {
		first = append(first, i)
	}
	store.Grant(1, first)
	second := make([]int64, 0, 1501)
	for i := int64(1000); i <= 2500; i++ {
		second = append(second, i)
	}
	store.Grant(1, second)
	if got := store.targetCount(1); got != 2000 {
		t.Fatalf("merged target count = %d, want 2000", got)
	}
	if !store.AllowedOwner(1, 2500) || store.AllowedOwner(1, 1) {
		t.Fatal("cap must keep newest IDs and drop oldest; merge must be unique")
	}
	// Two viewers × 2000 targets is the documented worst case (~32MiB).
	all := append(append([]int64{}, first...), second...)
	store.Grant(2, all)
	if got := store.viewerCount(); got != 2 {
		t.Fatalf("viewer count = %d, want 2", got)
	}
	if store.targetCount(1)+store.targetCount(2) != 4000 {
		t.Fatal("worst-case target count mismatch")
	}
}
