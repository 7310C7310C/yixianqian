package httpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/media"
)

// countingStore 记录 Get 次数，用来断言缓存命中后不再访问 R2。
type countingStore struct {
	data  []byte
	gets  atomic.Int64
	thumb string
}

func (s *countingStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return nil
}
func (s *countingStore) Remove(ctx context.Context, key string) error { return nil }
func (s *countingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.gets.Add(1)
	s.thumb = key
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func TestServeThumbUsesDiskCache(t *testing.T) {
	cache, err := media.NewThumbCache(t.TempDir(), 1<<20)
	if err != nil || cache == nil {
		t.Fatalf("cache: %v", err)
	}
	store := &countingStore{data: []byte("thumb-bytes")}
	app := testMediaApp(store)
	app.thumbCache = cache
	m := mediaObjectFixture("m1")
	m.ThumbSize = int64(len(store.data))

	// 第一次：miss → 读 R2 → 落盘。
	rec := httptest.NewRecorder()
	app.serveMediaObject(rec, httptest.NewRequest(http.MethodGet, "/media/m1?size=thumb", nil), m, true)
	if rec.Code != http.StatusOK || rec.Body.String() != "thumb-bytes" {
		t.Fatalf("first status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("R2 gets after miss = %d, want 1", got)
	}
	if app.thumbCacheMisses.Load() != 1 || app.thumbCacheHits.Load() != 0 {
		t.Fatalf("counters hits=%d misses=%d", app.thumbCacheHits.Load(), app.thumbCacheMisses.Load())
	}

	// 第二次：命中缓存，不再访问 R2。
	rec = httptest.NewRecorder()
	app.serveMediaObject(rec, httptest.NewRequest(http.MethodGet, "/media/m1?size=thumb", nil), m, true)
	if rec.Code != http.StatusOK || rec.Body.String() != "thumb-bytes" {
		t.Fatalf("second status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("R2 gets after hit = %d, want 1", got)
	}
	if app.thumbCacheHits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", app.thumbCacheHits.Load())
	}

	// 条件请求：304 也不该回源。
	req := httptest.NewRequest(http.MethodGet, "/media/m1?size=thumb", nil)
	req.Header.Set("If-None-Match", `"m1-thumb"`)
	rec = httptest.NewRecorder()
	app.serveMediaObject(rec, req, m, true)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional status=%d", rec.Code)
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("R2 gets after 304 = %d, want 1", got)
	}
}

func TestServeThumbCacheInvalidatesOnReplacedObject(t *testing.T) {
	cache, err := media.NewThumbCache(t.TempDir(), 1<<20)
	if err != nil || cache == nil {
		t.Fatalf("cache: %v", err)
	}
	store := &countingStore{data: []byte("v1")}
	app := testMediaApp(store)
	app.thumbCache = cache
	m := mediaObjectFixture("m1")
	m.ThumbSize = 2

	app.serveMediaObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/m1", nil), m, true)

	// 替换缩略图：object key 变了 → 新 key，必须重新回源。
	store.data = []byte("v2")
	m.ThumbKey = "media/m1_thumb_v2.jpg"
	rec := httptest.NewRecorder()
	app.serveMediaObject(rec, httptest.NewRequest(http.MethodGet, "/media/m1", nil), m, true)
	if rec.Body.String() != "v2" {
		t.Fatalf("replaced thumb body = %q, want v2", rec.Body.String())
	}
	if got := store.gets.Load(); got != 2 {
		t.Fatalf("R2 gets = %d, want 2 (replace must not hit stale cache)", got)
	}
}

func TestServeOriginalNeverUsesThumbCache(t *testing.T) {
	cache, err := media.NewThumbCache(t.TempDir(), 1<<20)
	if err != nil || cache == nil {
		t.Fatalf("cache: %v", err)
	}
	store := &countingStore{data: []byte("orig")}
	app := testMediaApp(store)
	app.thumbCache = cache
	m := mediaObjectFixture("m1")
	m.ObjectSize = 4

	app.serveMediaObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/m1", nil), m, false)
	app.serveMediaObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/m1", nil), m, false)
	if got := store.gets.Load(); got != 2 {
		t.Fatalf("original R2 gets = %d, want 2 (originals are not cached)", got)
	}
	if entries, _ := cache.Stats(); entries != 0 {
		t.Fatalf("originals leaked into thumb cache: %d", entries)
	}
}

// TestRemoveObjectPurgesThumbCache 覆盖 reviewer M1：删除路径必须真的清缓存
// （去掉 removeObject 里的 purgeThumbs 调用后本用例会红）。
func TestRemoveObjectPurgesThumbCache(t *testing.T) {
	cache, err := media.NewThumbCache(t.TempDir(), 1<<20)
	if err != nil || cache == nil {
		t.Fatalf("cache: %v", err)
	}
	store := &countingStore{data: []byte("thumb")}
	app := testMediaApp(store)
	app.thumbCache = cache
	m := mediaObjectFixture("m1")
	m.ThumbSize = 5
	app.serveMediaObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/m1", nil), m, true)
	if entries, _ := cache.Stats(); entries != 1 {
		t.Fatalf("expected cached thumb, entries=%d", entries)
	}

	app.removeObject(httptest.NewRequest(http.MethodGet, "/media/m1", nil), "m1", m.ObjectKey)
	if entries, _ := cache.Stats(); entries != 0 {
		t.Fatalf("removeObject must purge thumb cache, entries=%d", entries)
	}
}

// TestRemoveObjectPurgesPosterThumb 覆盖 reviewer M11：活动海报缩略图缓存 key 为
// poster-<mediaID>__…，删除海报时必须一并清理（purgeThumbs 同时清两个前缀）。
func TestRemoveObjectPurgesPosterThumb(t *testing.T) {
	cache, err := media.NewThumbCache(t.TempDir(), 1<<20)
	if err != nil || cache == nil {
		t.Fatalf("cache: %v", err)
	}
	store := &countingStore{data: []byte("poster")}
	app := testMediaApp(store)
	app.thumbCache = cache
	m := mediaObjectFixture("m1")
	m.ID = "poster-m1"
	m.ThumbSize = 6

	app.serveMediaObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/activities/1/poster", nil), m, true)
	if entries, _ := cache.Stats(); entries != 1 {
		t.Fatalf("expected poster thumb cached, entries=%d", entries)
	}
	app.removeObject(httptest.NewRequest(http.MethodGet, "/admin/activities/1/poster", nil), "m1", m.ObjectKey)
	if entries, _ := cache.Stats(); entries != 0 {
		t.Fatalf("poster thumb must be purged on delete, entries=%d", entries)
	}
}
