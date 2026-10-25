package httpserver

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testParts(user int64, kind, query string) string {
	return readCacheParts(user, kind, "user", query)
}

func TestReadCacheUserIsolation(t *testing.T) {
	c := newReadCache(true, time.Minute, 8, 1<<20, 0)
	c.Put(testParts(1, "home", ""), []byte(`{"ok":1}`))
	if _, ok := c.Get(testParts(2, "home", "")); ok {
		t.Fatal("other user must not hit")
	}
	if _, ok := c.Get(testParts(1, "cards", "")); ok {
		t.Fatal("other kind must not hit")
	}
	if _, ok := c.Get(readCacheParts(1, "home", "observer", "")); ok {
		t.Fatal("other viewRole must not hit")
	}
	if _, ok := c.Get(testParts(1, "home", "?x=1")); ok {
		t.Fatal("other query must not hit")
	}
	if _, ok := c.Get(testParts(1, "home", "")); !ok {
		t.Fatal("same key must hit")
	}
}

func TestReadCacheTTLExpiry(t *testing.T) {
	c := newReadCache(true, 20*time.Millisecond, 8, 1<<20, 0)
	key := testParts(1, "home", "")
	c.Put(key, []byte("x"))
	if _, ok := c.Get(key); !ok {
		t.Fatal("expected fresh hit")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get(key); ok {
		t.Fatal("expired entry must miss")
	}
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Fatalf("expired entry not dropped: len=%d bytes=%d", c.Len(), c.Bytes())
	}
}

func TestReadCacheByteLimitEvictsLRU(t *testing.T) {
	// 总字节 200（含键长）、单条上限 60：3 条 40B（cost≈key+40）只能留 2 条，按字节 LRU。
	c := newReadCache(true, time.Minute, 100, 140, 60)
	k1, k2, k3 := testParts(1, "home", ""), testParts(2, "home", ""), testParts(3, "home", "")
	c.Put(k1, make([]byte, 40))
	c.Put(k2, make([]byte, 40))
	if _, ok := c.Get(k1); !ok { // 触碰 k1 → k2 最旧
		t.Fatal("k1 missing")
	}
	c.Put(k3, make([]byte, 40))
	if c.Bytes() > 140 {
		t.Fatalf("bytes=%d exceeds limit 140", c.Bytes())
	}
	if _, ok := c.Get(k2); ok {
		t.Fatal("k2 should be evicted (byte LRU)")
	}
	if _, ok := c.Get(k1); !ok {
		t.Fatal("k1 should survive")
	}
}

func TestReadCacheEntryLargerThanMaxEntryIsSkipped(t *testing.T) {
	c := newReadCache(true, time.Minute, 8, 1<<20, 10)
	c.Put(testParts(1, "home", ""), make([]byte, 11))
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Fatalf("oversized entry cached: len=%d bytes=%d", c.Len(), c.Bytes())
	}
	c.Put(testParts(1, "home", ""), make([]byte, 10))
	if c.Len() != 1 {
		t.Fatal("entry at limit should be cached")
	}
}

func TestReadCacheItemLimit(t *testing.T) {
	c := newReadCache(true, time.Minute, 2, 1<<20, 0)
	c.Put(testParts(1, "home", ""), []byte("1"))
	c.Put(testParts(2, "home", ""), []byte("2"))
	c.Put(testParts(3, "home", ""), []byte("3"))
	if c.Len() != 2 {
		t.Fatalf("len=%d, want 2", c.Len())
	}
}

func TestReadCacheEpochBumpInvalidatesAllUsers(t *testing.T) {
	c := newReadCache(true, time.Minute, 8, 1<<20, 0)
	c.Put(testParts(1, "home", ""), []byte("a"))
	c.Put(testParts(2, "cards", "?p=1"), []byte("b"))
	c.BumpEpoch()
	if _, ok := c.Get(testParts(1, "home", "")); ok {
		t.Fatal("user 1 entry must be invalid after epoch bump")
	}
	if _, ok := c.Get(testParts(2, "cards", "?p=1")); ok {
		t.Fatal("user 2 entry must be invalid after epoch bump")
	}
	c.Put(testParts(1, "home", ""), []byte("c"))
	if _, ok := c.Get(testParts(1, "home", "")); !ok {
		t.Fatal("post-bump entry should hit")
	}
}

func TestReadCacheKillSwitch(t *testing.T) {
	// 任一项为 0、或 ENABLED=false、或零值 Config 都必须彻底关闭（不得回落默认值）。
	for name, c := range map[string]*readCache{
		"ttl0":   newReadCache(true, 0, 10, 1<<20, 0),
		"items0": newReadCache(true, time.Minute, 0, 1<<20, 0),
		"bytes0": newReadCache(true, time.Minute, 10, 0, 0),
	} {
		if c != nil {
			t.Fatalf("%s: cache must be disabled (nil), got %#v", name, c)
		}
		c.Put("x", []byte("y"))
		if _, ok := c.Get("x"); ok {
			t.Fatalf("%s: disabled cache must miss", name)
		}
		c.BumpEpoch()
		if c.Len() != 0 || c.Bytes() != 0 {
			t.Fatalf("%s: disabled cache stats non-zero", name)
		}
	}
}

func TestReadCacheNilAndEmptyBody(t *testing.T) {
	var nilCache *readCache
	nilCache.Put("k", []byte("x"))
	if _, ok := nilCache.Get("k"); ok {
		t.Fatal("nil cache must miss")
	}
	nilCache.BumpEpoch()
	if nilCache.Len() != 0 {
		t.Fatal("nil cache len")
	}
	c := newReadCache(true, time.Minute, 4, 1<<20, 0)
	c.Put("k", nil)
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty body must not be cached")
	}
}

func TestReadCacheWorstCaseMemoryBounded(t *testing.T) {
	// 最坏情况：单条都取到 maxEntry，总字节仍是硬上限（不随条数线性膨胀）。
	const maxBytes = 64 << 10
	c := newReadCache(true, time.Minute, 4096, maxBytes, 0)
	for i := 0; i < 200; i++ {
		c.Put(testParts(int64(i%8), "cards", "?p="+string(rune('a'+i%26))), make([]byte, 32<<10))
	}
	if c.Bytes() > maxBytes {
		t.Fatalf("bytes=%d exceeds cap %d", c.Bytes(), maxBytes)
	}
	if c.Len() > 4096 {
		t.Fatalf("len=%d exceeds item cap", c.Len())
	}
}

func TestReadCacheConcurrentAccess(t *testing.T) {
	c := newReadCache(true, time.Minute, 64, 1<<20, 0)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 500; j++ {
				key := testParts(int64(n%4), "home", "")
				c.Put(key, []byte("x"))
				_, _ = c.Get(key)
				if j%100 == 0 {
					c.BumpEpoch()
				}
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if c.Bytes() > 1<<20 {
		t.Fatalf("bytes=%d", c.Bytes())
	}
}

func TestReadCacheKeyBytesCountAgainstBudget(t *testing.T) {
	// 120 条超长 query：键长也占预算，总量不得超过上限。
	const maxBytes = 32 << 10
	c := newReadCache(true, time.Minute, 4096, maxBytes, 0)
	long := strings.Repeat("q", 300)
	for i := 0; i < 120; i++ {
		c.Put(testParts(int64(i%4), "cards", "?"+long+strconv.Itoa(i)), []byte("x"))
	}
	if c.Bytes() > maxBytes {
		t.Fatalf("bytes=%d exceeds cap %d (key length must count)", c.Bytes(), maxBytes)
	}
}

func TestReadCacheStalePutAfterBump(t *testing.T) {
	c := newReadCache(true, time.Minute, 8, 1<<20, 0)
	key := testParts(1, "home", "")
	epoch := c.Epoch()
	c.BumpEpoch() // 构建期间发生了写
	if ok := c.PutIfEpoch(key, []byte("stale"), epoch); ok {
		t.Fatal("stale PutIfEpoch must be rejected after epoch bump")
	}
	if _, hit := c.Get(key); hit {
		t.Fatal("stale value must not be readable")
	}
	fresh := c.Epoch()
	if ok := c.PutIfEpoch(key, []byte("fresh"), fresh); !ok {
		t.Fatal("fresh PutIfEpoch must succeed")
	}
	if got, hit := c.Get(key); !hit || string(got) != "fresh" {
		t.Fatalf("fresh value missing: %q hit=%v", got, hit)
	}
}

func TestReadCacheEpochWritePredicate(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/legacy/api/home", false},
		{"HEAD", "/api/profile", false},
		{"OPTIONS", "/legacy/api/home", false},
		{"POST", "/legacy/api/like", true},
		{"POST", "/legacy/api/track", false},
		{"POST", "/legacy/api/track/batch", false},
		{"POST", "/legacy/api/client-events", false},
		{"POST", "/static/legacy/app.min.js", false},
		{"POST", "/healthz", false},
		{"POST", "/admin/points/adjust", true},
		{"DELETE", "/api/profile/photo", true},
		{"PUT", "/legacy/api/profile", true},
		{"POST", "/api/admin/matching/run", true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := readCacheEpochWrite(r); got != tc.want {
			t.Fatalf("%s %s: got %v want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
