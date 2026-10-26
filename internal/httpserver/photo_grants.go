package httpserver

import (
	"sort"
	"sync"
	"time"
)

// photoGrants is the short-lived authorization memory for deck candidates.
//
// A deck card corresponds to one *gallery owner* (profile_photos.user_id); all
// of that owner's profile photos are visible together. Grants therefore store
// gallery-owner user IDs, not media IDs, so a 60-card deck with 9 photos each
// adds 60 entries, never 540.
//
// Each viewer has a single sorted, de-duplicated []int64 of target IDs. The
// whole viewer grant shares one 15-minute TTL refreshed by each cards/home
// grant, and the per-viewer hard cap is 2,000 targets (binary search via
// sort.Search). Worst-case memory is 2,000 viewers × 2,000 targets × 8B ≈ 32MiB
// plus slice headers/map overhead (no per-target time.Time/map entry), well
// below the previous unbounded segment design. Viewer count is capped at 2,000
// with lastSeen LRU; over-cap eviction is fail-closed. A process restart drops
// all grants. Multi-instance deployments need a shared store or HMAC-signed
// URLs; the current deployment is single-process.
const (
	defaultPhotoGrantTTL        = 15 * time.Minute
	defaultPhotoGrantMaxViewers = 2000
	defaultPhotoGrantMaxTargets = 2000
)

type viewerPhotoGrants struct {
	targets  []int64 // sorted, unique gallery owner IDs
	expires  time.Time
	lastSeen time.Time
}

type photoGrantStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxViewers int
	maxTargets int
	now        func() time.Time
	viewers    map[int64]*viewerPhotoGrants
}

func newPhotoGrantStore(ttl time.Duration, maxViewers, maxTargets int, now func() time.Time) *photoGrantStore {
	if now == nil {
		now = time.Now
	}
	if maxViewers <= 0 {
		maxViewers = defaultPhotoGrantMaxViewers
	}
	if maxTargets <= 0 {
		maxTargets = defaultPhotoGrantMaxTargets
	}
	return &photoGrantStore{
		ttl:        ttl,
		maxViewers: maxViewers,
		maxTargets: maxTargets,
		now:        now,
		viewers:    make(map[int64]*viewerPhotoGrants),
	}
}

func addSortedUnique(targets []int64, ownerID int64) []int64 {
	pos := sort.Search(len(targets), func(i int) bool { return targets[i] >= ownerID })
	if pos < len(targets) && targets[pos] == ownerID {
		return targets
	}
	targets = append(targets, 0)
	copy(targets[pos+1:], targets[pos:])
	targets[pos] = ownerID
	return targets
}

// Grant records that viewerID may fetch all profile photos of the gallery
// owners in ownerIDs. A zero/negative TTL disables grants (fail closed).
func (s *photoGrantStore) Grant(viewerID int64, ownerIDs []int64) {
	if s == nil || viewerID <= 0 || len(ownerIDs) == 0 || s.ttl <= 0 {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	viewer := s.viewers[viewerID]
	if viewer == nil {
		viewer = &viewerPhotoGrants{}
		s.viewers[viewerID] = viewer
	}
	viewer.expires = now.Add(s.ttl)
	viewer.lastSeen = now
	for _, ownerID := range ownerIDs {
		if ownerID > 0 {
			viewer.targets = addSortedUnique(viewer.targets, ownerID)
		}
	}
	if len(viewer.targets) > s.maxTargets {
		viewer.targets = viewer.targets[len(viewer.targets)-s.maxTargets:]
	}
	// Global viewer LRU: drop expired viewers, then oldest lastSeen.
	if len(s.viewers) > s.maxViewers {
		for id, v := range s.viewers {
			if !v.expires.After(now) {
				delete(s.viewers, id)
			}
		}
		for len(s.viewers) > s.maxViewers {
			var oldestID int64
			var oldest time.Time
			first := true
			for id, v := range s.viewers {
				if first || v.lastSeen.Before(oldest) {
					oldestID, oldest, first = id, v.lastSeen, false
				}
			}
			delete(s.viewers, oldestID)
		}
	}
}

// AllowedOwner reports whether viewerID may fetch ownerID's gallery photos.
// Expired grants are denied lazily; lastSeen feeds the global viewer LRU.
func (s *photoGrantStore) AllowedOwner(viewerID, ownerID int64) bool {
	if s == nil || viewerID <= 0 || ownerID <= 0 {
		return false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	viewer := s.viewers[viewerID]
	if viewer == nil || !viewer.expires.After(now) {
		return false
	}
	pos := sort.Search(len(viewer.targets), func(i int) bool { return viewer.targets[i] >= ownerID })
	if pos >= len(viewer.targets) || viewer.targets[pos] != ownerID {
		return false
	}
	viewer.lastSeen = now
	return true
}

func (s *photoGrantStore) viewerCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.viewers)
}

func (s *photoGrantStore) targetCount(viewerID int64) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	viewer := s.viewers[viewerID]
	if viewer == nil {
		return 0
	}
	if !viewer.expires.After(s.now()) {
		return 0
	}
	return len(viewer.targets)
}

// grantCardOwners records exactly the gallery owners returned to viewerID by a
// cards or home payload. It performs no SQL: cards already loaded all photos in
// its batch query, so 15 cards become 15 owner entries, not 15 grant queries.
func (a *App) grantCardOwners(viewerID int64, cards []CardView) {
	if a.photoGrants == nil || viewerID <= 0 || len(cards) == 0 {
		return
	}
	owners := make([]int64, 0, len(cards))
	for _, card := range cards {
		if card.ID > 0 {
			owners = append(owners, card.ID)
		}
	}
	a.photoGrants.Grant(viewerID, owners)
}
