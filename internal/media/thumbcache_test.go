package media

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestCache(t *testing.T, limit int64) *ThumbCache {
	t.Helper()
	dir := t.TempDir()
	c, err := NewThumbCache(dir, limit)
	if err != nil {
		t.Fatalf("NewThumbCache: %v", err)
	}
	if c == nil {
		t.Fatal("cache nil")
	}
	return c
}

func TestThumbCachePutGet(t *testing.T) {
	c := newTestCache(t, 1<<20)
	key := ThumbCacheKey("media-1", "media/1_thumb.jpg", 1024)
	if _, ok := c.Get(key); ok {
		t.Fatal("unexpected hit before put")
	}
	if err := c.Put(key, []byte("thumb-bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get(key)
	if !ok || string(got) != "thumb-bytes" {
		t.Fatalf("Get = %q ok=%v", got, ok)
	}
	if entries, bytes := c.Stats(); entries != 1 || bytes != int64(len("thumb-bytes")) {
		t.Fatalf("stats entries=%d bytes=%d", entries, bytes)
	}
}

func TestThumbCacheKeyChangesWithObjectOrSize(t *testing.T) {
	base := ThumbCacheKey("m1", "media/1_thumb.jpg", 100)
	if base == ThumbCacheKey("m1", "media/1_thumb.jpg", 101) {
		t.Fatal("size change must change key")
	}
	if base == ThumbCacheKey("m1", "media/9_thumb.jpg", 100) {
		t.Fatal("replaced object key must change key (invalidation on replace)")
	}
	if base == ThumbCacheKey("m2", "media/1_thumb.jpg", 100) {
		t.Fatal("media id must be part of key")
	}
}

func TestThumbCacheEvictsLeastRecentlyUsed(t *testing.T) {
	const chunk = 100
	c := newTestCache(t, 3*chunk)
	keys := []string{"m__a", "m__b", "m__c"}
	for i, key := range keys {
		if err := c.Put(key, make([]byte, chunk)); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	// 触碰 a，让 b 成为最久未用。
	if _, ok := c.Get("m__a"); !ok {
		t.Fatal("a missing")
	}
	if err := c.Put("m__d", make([]byte, chunk)); err != nil {
		t.Fatalf("put d: %v", err)
	}
	if _, ok := c.Get("m__b"); ok {
		t.Fatal("b should have been evicted as LRU")
	}
	for _, key := range []string{"m__a", "m__c", "m__d"} {
		if _, ok := c.Get(key); !ok {
			t.Fatalf("%s should still be cached", key)
		}
	}
	if _, bytes := c.Stats(); bytes > 3*chunk {
		t.Fatalf("cache over limit: %d", bytes)
	}
}

func TestThumbCacheSkipsEntryLargerThanLimit(t *testing.T) {
	c := newTestCache(t, 50)
	if err := c.Put("m__big", make([]byte, 51)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := c.Get("m__big"); ok {
		t.Fatal("oversized entry must not be cached")
	}
}

func TestThumbCacheRemoveMediaPurgesAllVariants(t *testing.T) {
	c := newTestCache(t, 1<<20)
	keep := ThumbCacheKey("other", "other_thumb.jpg", 10)
	doomed := []string{
		ThumbCacheKey("photo-1", "p1_thumb.jpg", 10),
		ThumbCacheKey("photo-1", "p1_thumb_v2.jpg", 10),
	}
	for _, key := range doomed {
		if err := c.Put(key, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Put(keep, []byte("y")); err != nil {
		t.Fatal(err)
	}
	c.RemoveMedia("photo-1")
	for _, key := range doomed {
		if _, ok := c.Get(key); ok {
			t.Fatalf("%s should be purged", key)
		}
		if _, err := os.Stat(filepath.Join(c.Dir(), key+".bin")); !os.IsNotExist(err) {
			t.Fatalf("%s file still on disk", key)
		}
	}
	if _, ok := c.Get(keep); !ok {
		t.Fatal("unrelated entry must survive")
	}
}

func TestThumbCacheReloadsIndexFromDisk(t *testing.T) {
	dir := t.TempDir()
	c, err := NewThumbCache(dir, 1<<20)
	if err != nil || c == nil {
		t.Fatalf("cache: %v", err)
	}
	key := ThumbCacheKey("m1", "m1_thumb.jpg", 4)
	if err := c.Put(key, []byte("data")); err != nil {
		t.Fatal(err)
	}
	again, err := NewThumbCache(dir, 1<<20)
	if err != nil || again == nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := again.Get(key); !ok {
		t.Fatal("entry must survive process restart")
	}
}

func TestThumbCacheMissWhenFileDeleted(t *testing.T) {
	c := newTestCache(t, 1<<20)
	key := ThumbCacheKey("m1", "m1_thumb.jpg", 4)
	if err := c.Put(key, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c.Dir(), key+".bin")); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key); ok {
		t.Fatal("missing file must be a miss")
	}
	if entries, _ := c.Stats(); entries != 0 {
		t.Fatalf("index not cleaned: %d", entries)
	}
}

func TestThumbCacheConcurrentAccess(t *testing.T) {
	c := newTestCache(t, 1<<20)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := ThumbCacheKey(fmt.Sprintf("m%d", n%3), "obj.jpg", 4)
			for j := 0; j < 200; j++ {
				_ = c.Put(key, []byte("data"))
				_, _ = c.Get(key)
				if j%50 == 0 {
					c.RemoveMedia("m1")
				}
			}
		}(i)
	}
	wg.Wait()
	if _, bytes := c.Stats(); bytes < 0 {
		t.Fatalf("negative bytes: %d", bytes)
	}
}

func TestDisabledThumbCache(t *testing.T) {
	c, err := NewThumbCache("", 0)
	if err != nil || c != nil {
		t.Fatalf("empty dir must disable cache: c=%v err=%v", c, err)
	}
	if _, ok := c.Get("x"); ok {
		t.Fatal("nil cache must miss")
	}
	if err := c.Put("x", []byte("y")); err != nil {
		t.Fatalf("nil cache Put: %v", err)
	}
	c.RemoveMedia("x")
	if entries, bytes := c.Stats(); entries != 0 || bytes != 0 {
		t.Fatalf("nil cache stats: %d %d", entries, bytes)
	}
}
