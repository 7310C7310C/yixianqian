package httpserver

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

// TestFaviconSVGsUseBrandMark guards against regressing to the old placeholder
// glyph (three grey bars) — the v1 brand mark is the pink double heart.
func TestFaviconSVGsUseBrandMark(t *testing.T) {
	for _, name := range []string{"static/favicon.svg", "static/legacy/favicon.svg"} {
		data, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("%s missing from embed.FS: %v", name, err)
		}
		if !bytes.Contains(data, []byte("#ff6b81")) {
			t.Errorf("%s does not contain the brand pink #ff6b81", name)
		}
		if bytes.Contains(data, []byte("M18 22h28")) {
			t.Errorf("%s still uses the placeholder bar glyph", name)
		}
	}
}

func TestFaviconRootServesICO(t *testing.T) {
	want, err := fs.ReadFile(webassets.FS, "static/favicon.ico")
	if err != nil {
		t.Fatalf("static/favicon.ico missing from embed.FS: %v", err)
	}
	h := faviconHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/vnd.microsoft.icon" {
		t.Fatalf("content-type = %q, want image/vnd.microsoft.icon", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatal("served body differs from embedded static/favicon.ico")
	}

	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/favicon.ico", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body = %d bytes, want 0", head.Body.Len())
	}
}
