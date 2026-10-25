package httpserver

import (
	"testing"
	"time"
)

func TestReadCacheUserIsolation(t *testing.T) {
	c := newReadCache(time.Minute, 8)
	body := []byte(`{"cards":[]}`)
	c.Put(readCacheKey(1, "home", "user", ""), body)
	if _, ok := c.Get(readCacheKey(2, "home", "user", "")); ok {
		t.Fatal("other user must not hit")
	}
	if _, ok := c.Get(readCacheKey(1, "cards", "user", "")); ok {
		t.Fatal("other kind must not hit")
	}
	if _, ok := c.Get(readCacheKey(1, "home", "observer", "")); ok {
		t.Fatal("other viewRole must not hit")
	}
	if _, ok := c.Get(readCacheKey(1, "home", "user", "?x=1")); ok {
		t.Fatal("other query must not hit")
	}
	got, ok := c.Get(readCacheKey(1, "home", "user", ""))
	if !ok || string(got) != string(body) {
		t.Fatalf("same key hit=%v body=%s", ok, got)
	}
}

func TestReadCacheTTLExpiry(t *testing.T) {
	c := newReadCache(20*time.Millisecond, 8)
	key := readCacheKey(1, "home", "user", "")
	c.Put(key, []byte("x"))
	if _, ok := c.Get(key); !ok {
		t.Fatal("expected fresh hit")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get(key); ok {
		t.Fatal("expired entry must miss")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry should be dropped, len=%d", c.Len())
	}
}

func TestReadCacheLRUBound(t *testing.T) {
	c := newReadCache(time.Minute, 2)
	k1 := readCacheKey(1, "home", "user", "")
	k2 := readCacheKey(2, "home", "user", "")
	k3 := readCacheKey(3, "home", "user", "")
	c.Put(k1, []byte("1"))
	c.Put(k2, []byte("2"))
	if _, ok := c.Get(k1); !ok { // 触碰 k1 → k2 变最旧
		t.Fatal("k1 missing")
	}
	c.Put(k3, []byte("3"))
	if _, ok := c.Get(k2); ok {
		t.Fatal("k2 should be evicted (LRU)")
	}
	if _, ok := c.Get(k1); !ok {
		t.Fatal("k1 should survive")
	}
	if c.Len() != 2 {
		t.Fatalf("len=%d, want 2", c.Len())
	}
}

func TestReadCacheInvalidateUser(t *testing.T) {
	c := newReadCache(time.Minute, 8)
	c.Put(readCacheKey(1, "home", "user", ""), []byte("a"))
	c.Put(readCacheKey(1, "cards", "user", "?p=1"), []byte("b"))
	c.Put(readCacheKey(2, "home", "user", ""), []byte("c"))
	c.InvalidateUser(1)
	if _, ok := c.Get(readCacheKey(1, "home", "user", "")); ok {
		t.Fatal("user 1 home not invalidated")
	}
	if _, ok := c.Get(readCacheKey(1, "cards", "user", "?p=1")); ok {
		t.Fatal("user 1 cards not invalidated")
	}
	if _, ok := c.Get(readCacheKey(2, "home", "user", "")); !ok {
		t.Fatal("user 2 must survive invalidation")
	}
}

func TestReadCacheDisabledAndEmptyBody(t *testing.T) {
	var nilCache *readCache
	nilCache.Put("k", []byte("x"))
	if _, ok := nilCache.Get("k"); ok {
		t.Fatal("nil cache must miss")
	}
	nilCache.InvalidateUser(1)
	if nilCache.Len() != 0 {
		t.Fatal("nil cache len")
	}
	c := newReadCache(time.Minute, 4)
	c.Put("k", nil) // 空 body 不入缓存
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty body must not be cached")
	}
}

func TestReadCacheConcurrentAccess(t *testing.T) {
	c := newReadCache(time.Minute, 64)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 500; j++ {
				key := readCacheKey(int64(n%4), "home", "user", "")
				c.Put(key, []byte("x"))
				_, _ = c.Get(key)
				if j%100 == 0 {
					c.InvalidateUser(int64(n % 4))
				}
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
