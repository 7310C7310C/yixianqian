package httpserver

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

type fakeMediaStore struct {
	data    []byte
	getErr  error
	readErr error
}

func (f fakeMediaStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return nil
}
func (f fakeMediaStore) Remove(ctx context.Context, key string) error { return nil }
func (f fakeMediaStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return io.NopCloser(&fakeReader{data: f.data, err: f.readErr}), nil
}

type fakeReader struct {
	data []byte
	err  error
	done bool
}

func (r *fakeReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.done {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.done = true
	return n, nil
}

func testMediaApp(store media.Store) *App {
	return &App{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func mediaObjectFixture(id string) mediaObject {
	return mediaObject{
		ID:         id,
		ObjectKey:  "media/" + id + ".jpg",
		ObjectSize: 4,
		ObjectType: "image/jpeg",
		ThumbKey:   "media/" + id + "_thumb.jpg",
		ThumbSize:  4,
	}
}

func TestMediaHeroVariant(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 82}); err != nil {
		t.Fatal(err)
	}
	app := testMediaApp(fakeMediaStore{data: buf.Bytes()})
	rec := httptest.NewRecorder()
	app.serveMediaHero(rec, httptest.NewRequest(http.MethodGet, "/cards/photo/m1?size=hero", nil), mediaObjectFixture("m1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); !strings.Contains(got, "-hero") {
		t.Fatalf("etag=%q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content-type=%q", got)
	}
}

func TestMediaErrorsAreNotCacheable(t *testing.T) {
	cases := []struct {
		name   string
		store  fakeMediaStore
		thumb  bool
		status int
	}{
		{name: "404 Get", store: fakeMediaStore{getErr: media.ErrObjectNotFound}, thumb: true, status: http.StatusNotFound},
		{name: "500 Get", store: fakeMediaStore{getErr: errors.New("r2 down")}, thumb: false, status: http.StatusInternalServerError},
		{name: "404 read", store: fakeMediaStore{data: []byte("jpeg"), readErr: media.ErrObjectNotFound}, thumb: true, status: http.StatusNotFound},
		{name: "500 read", store: fakeMediaStore{data: []byte("jpeg"), readErr: errors.New("read failed")}, thumb: false, status: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := testMediaApp(tc.store)
			rec := httptest.NewRecorder()
			app.serveMediaObject(rec, httptest.NewRequest(http.MethodGet, "/media/x", nil), mediaObjectFixture("m1"), tc.thumb)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "max-age") || !strings.Contains(got, "no-store") {
				t.Fatalf("error cache-control = %q, want no-store without max-age", got)
			}
			if got := rec.Header().Get("ETag"); got != "" {
				t.Fatalf("error ETag = %q, want empty", got)
			}
			if strings.Contains(rec.Body.String(), "r2 down") || strings.Contains(rec.Body.String(), "read failed") {
				t.Fatal("error body leaks the storage error")
			}
		})
	}
}

func TestMediaSuccessCacheHeaders(t *testing.T) {
	cases := []struct {
		thumb string
		want  string
	}{
		{thumb: "thumb", want: "private, max-age=86400, immutable"},
		{thumb: "orig", want: "private, max-age=3600, immutable"},
	}
	for _, tc := range cases {
		t.Run(tc.thumb, func(t *testing.T) {
			app := testMediaApp(fakeMediaStore{data: []byte("jpeg")})
			rec := httptest.NewRecorder()
			app.serveMediaObject(rec, httptest.NewRequest(http.MethodGet, "/media/x", nil), mediaObjectFixture("m1"), tc.thumb == "thumb")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Fatalf("cache-control = %q, want %q", got, tc.want)
			}
			wantETag := `"m1-thumb"`
			if tc.thumb == "orig" {
				wantETag = `"m1-orig"`
			}
			if got := rec.Header().Get("ETag"); got != wantETag {
				t.Fatalf("etag = %q, want %q", got, wantETag)
			}
			if strings.Contains(rec.Header().Get("Cache-Control"), "public") {
				t.Fatal("media must stay private")
			}
		})
	}
}

func TestMediaConditionalOnlyAfterExistence(t *testing.T) {
	app := testMediaApp(fakeMediaStore{data: []byte("jpeg")})
	req := httptest.NewRequest(http.MethodGet, "/media/x", nil)
	req.Header.Set("If-None-Match", `"m1-thumb"`)
	rec := httptest.NewRecorder()
	app.serveMediaObject(rec, req, mediaObjectFixture("m1"), true)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}

	// Missing object must not answer 304 even with a matching ETag.
	app = testMediaApp(fakeMediaStore{getErr: media.ErrObjectNotFound})
	req = httptest.NewRequest(http.MethodGet, "/media/x", nil)
	req.Header.Set("If-None-Match", `"m1-thumb"`)
	rec = httptest.NewRecorder()
	app.serveMediaObject(rec, req, mediaObjectFixture("m1"), true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing object status = %d, want 404", rec.Code)
	}
}

func TestActivityPosterETagTracksMediaID(t *testing.T) {
	first := activityPosterObject(db.ActivityPoster{MediaID: "media-v1"})
	second := activityPosterObject(db.ActivityPoster{MediaID: "media-v2"})
	if first.ID == second.ID {
		t.Fatalf("poster ETag identity did not change: %q", first.ID)
	}
	app := testMediaApp(fakeMediaStore{data: []byte("jpeg")})
	req := httptest.NewRequest(http.MethodGet, "/activities/A-001/poster", nil)
	req.Header.Set("If-None-Match", `"`+first.ID+"-orig\"")
	rec := httptest.NewRecorder()
	app.serveMediaObject(rec, req, second, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("new poster with old ETag status = %d, want 200", rec.Code)
	}
}

func TestThumbVariantKey(t *testing.T) {
	cases := []struct {
		base  string
		width int
		want  string
	}{
		{"media/1/a_thumb.jpg", 768, "media/1/a_thumb_768.jpg"},
		{"media/1/a_thumb", 1024, "media/1/a_thumb_1024"},
		{"", 768, ""},
	}
	for _, tc := range cases {
		if got := thumbVariantKey(tc.base, tc.width); got != tc.want {
			t.Fatalf("thumbVariantKey(%q,%d)=%q want %q", tc.base, tc.width, got, tc.want)
		}
	}
}

func TestEffectiveThumbWidth(t *testing.T) {
	cases := []struct{ original, requested, want int }{
		{480, 1280, 512},
		{480, 768, 512},
		{736, 1280, 768},
		{1080, 1280, 1024},
		{1080, 768, 768},
		{0, 1280, 1280},
	}
	for _, tc := range cases {
		if got := effectiveThumbWidth(tc.original, tc.requested); got != tc.want {
			t.Fatalf("effectiveThumbWidth(%d,%d)=%d want %d", tc.original, tc.requested, got, tc.want)
		}
	}
}

func TestRemoveMediaVariantsDeletesAllWidths(t *testing.T) {
	store := newFakeStore()
	app := &App{store: store}
	thumb := "media/1/m_thumb.jpg"
	for _, width := range thumbVariantDeleteWidths {
		store.objects[thumbVariantKey(thumb, width)] = []byte("variant")
	}
	app.removeMediaVariants(httptest.NewRequest(http.MethodDelete, "/media/m1", nil), "m1", thumb)
	for _, width := range thumbVariantDeleteWidths {
		if _, ok := store.objects[thumbVariantKey(thumb, width)]; ok {
			t.Fatalf("variant %d not deleted", width)
		}
	}
}

func TestThumbVariantCandidatesDedupe(t *testing.T) {
	if got := thumbVariantCandidates(768); len(got) != 1 || got[0] != 768 {
		// 1024 > 768 is skipped; the duplicate 768 probe is removed.
		t.Fatalf("candidates(768)=%v", got)
	}
	if got := thumbVariantCandidates(1024); len(got) != 2 || got[0] != 1024 || got[1] != 768 {
		t.Fatalf("candidates(1024)=%v", got)
	}
	if got := thumbVariantCandidates(1280); len(got) != 3 || got[0] != 1280 || got[1] != 1024 || got[2] != 768 {
		t.Fatalf("candidates(1280)=%v", got)
	}
}
