package httpserver

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

func readEmbedded(t *testing.T, name string) string {
	t.Helper()
	raw, err := fs.ReadFile(webassets.FS, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

// The shell must stay small: only the boot flags and the version constants may
// remain inline. CSS and the application bundle are separate, cacheable files.
func TestLegacyShellHasNoLargeInlineAssets(t *testing.T) {
	html := readEmbedded(t, "static/legacy/index.html")

	styleRe := regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	for _, m := range styleRe.FindAllStringSubmatch(html, -1) {
		if len(m[1]) > 2048 {
			t.Fatalf("inline <style> block is %d bytes; move it to app.css", len(m[1]))
		}
	}
	scriptRe := regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`)
	for _, m := range scriptRe.FindAllStringSubmatch(html, -1) {
		if len(m[1]) > 4096 {
			t.Fatalf("inline <script> block is %d bytes; move it to app.js", len(m[1]))
		}
	}

	refRe := regexp.MustCompile(`(?:src|href)="((?:lib/|app\.)[^"]+)"`)
	refs := refRe.FindAllStringSubmatch(html, -1)
	if len(refs) < 4 {
		t.Fatalf("expected at least 4 versioned lib/app references, got %d", len(refs))
	}
	for _, m := range refs {
		if !strings.Contains(m[1], "v=__ASSET_URL_VERSION__") {
			t.Fatalf("asset reference %q is missing v=__ASSET_URL_VERSION__", m[1])
		}
	}
	for _, banned := range []string{"photoswipe.umd.min.js", "photoswipe-lightbox.umd.min.js", "lib/photoswipe.css"} {
		if strings.Contains(html, banned) {
			t.Fatalf("shell still loads %s synchronously", banned)
		}
	}
	if !strings.Contains(html, "boot-skeleton") {
		t.Fatal("shell is missing the boot skeleton")
	}
}

func TestPhotoSwipeLoadsOnDemand(t *testing.T) {
	js := readEmbedded(t, "static/legacy/app.js")
	for _, want := range []string{"function loadPhotoSwipe", "photoswipe.umd.min.js?v=", "photoswipe-lightbox.umd.min.js?v="} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js missing %q", want)
		}
	}
	if strings.Contains(js, "<script src=\"lib/photoswipe") {
		t.Fatal("app.js still contains a synchronous PhotoSwipe tag")
	}
}

func TestStaticCacheHeadersAndETag(t *testing.T) {
	h := staticHandler()

	req := httptest.NewRequest(http.MethodGet, "/static/app.css?v=5", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("versioned cache-control = %q", got)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/static/app.css?v=5", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", rec2.Code)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if got := rec3.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=3600") || strings.Contains(got, "immutable") {
		t.Fatalf("bare cache-control = %q", got)
	}
}

func TestMediaETagMatches(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{`"m1-thumb"`, true},
		{`W/"m1-thumb"`, true},
		{`"other", "m1-thumb"`, true},
		{`*`, true},
		{``, false},
		{`"m1-orig"`, false},
	}
	for _, tc := range cases {
		if got := mediaETagMatches(tc.header, `"m1-thumb"`); got != tc.want {
			t.Fatalf("mediaETagMatches(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}
