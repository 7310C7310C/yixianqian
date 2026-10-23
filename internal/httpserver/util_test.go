package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
)

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                            "/",
		"/":                           "/",
		"/media?size=thumb":           "/media?size=thumb",
		"//evil.example.com":          "/",
		"https://evil.example.com":    "/",
		"/\\evil":                     "/",
		"/login?next=/x":              "/",
		"/logout":                     "/",
		"/activate?token=x":           "/",
		"/api/media":                  "/",
		"/media/upload":               "/",
		"relative/path":               "/",
		"/path\r\nX-Test: 1":          "/",
		"/admin":                      "/?admin=1",
		"/admin/users":                "/?admin=1",
		"/admin/users?status=pending": "/?admin=1",
		"/admin?embed=1":              "/?admin=1",
		"/administrator":              "/administrator",
	}
	for input, want := range cases {
		if got := safeNext(input); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidMediaID(t *testing.T) {
	if !validMediaID("11111111-1111-4111-8111-111111111111") {
		t.Fatal("valid uuid rejected")
	}
	for _, id := range []string{"", "abc", "11111111-1111-4111-8111-11111111111z", "11111111111141118111111111111111"} {
		if validMediaID(id) {
			t.Fatalf("invalid id accepted: %q", id)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("../../etc/passwd"); got != "passwd" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeFilename(`C:\temp\photo.jpg`); got != "photo.jpg" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeFilename("a\x00b.jpg"); got != "ab.jpg" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeFilename(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeRequestID(t *testing.T) {
	if got := sanitizeRequestID("abc-123_XY"); got != "abc-123_XY" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeRequestID("bad id"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeRequestID(strings.Repeat("a", 65)); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCSRFDoubleSubmit(t *testing.T) {
	app := &App{
		cfg:  config.Config{SessionCookieSecure: false},
		csrf: auth.NewCSRF([]byte(strings.Repeat("k", 32)), 0),
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	token := app.ensureCSRF(rec, req)
	if token == "" {
		t.Fatal("empty csrf token")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != auth.CSRFCookieName || c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected cookie: %+v", c)
	}

	withCookie := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.AddCookie(c)
		return r
	}

	r := withCookie()
	r.Header.Set(auth.CSRFHeader, token)
	if !app.headerCSRFValid(r) {
		t.Fatal("valid header token rejected")
	}
	r.Header.Set(auth.CSRFHeader, token+"x")
	if app.headerCSRFValid(r) {
		t.Fatal("tampered header token accepted")
	}

	form := url.Values{auth.CSRFFormField: {token}}
	r = httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if !app.formCSRFValid(r) {
		t.Fatal("valid form token rejected")
	}

	r = httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if app.formCSRFValid(r) {
		t.Fatal("missing cookie accepted")
	}
}

func TestRequireUserRedirects(t *testing.T) {
	app := &App{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media?size=thumb", nil)
	_, ok := app.requireUserHTML(rec, req)
	if ok {
		t.Fatal("anonymous request should not be authenticated")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login?next=%2Fmedia%3Fsize%3Dthumb" {
		t.Fatalf("location = %q", loc)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/media/upload", nil)
	if _, ok := app.requireUserHTML(rec, req); ok {
		t.Fatal("anonymous POST should not be authenticated")
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("POST redirect = %q, want /login without next", loc)
	}
}

func TestHomeFlash(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?activated=1&uploaded=1", nil)
	notice, errMsg := homeFlash(r)
	if notice != "照片已上传。" || errMsg != "" {
		t.Fatalf("notice=%q err=%q", notice, errMsg)
	}

	r = httptest.NewRequest(http.MethodGet, "/?upload_error=heic", nil)
	notice, errMsg = homeFlash(r)
	if notice != "" || !strings.Contains(errMsg, "HEIC") {
		t.Fatalf("notice=%q err=%q", notice, errMsg)
	}

	r = httptest.NewRequest(http.MethodGet, "/?upload_error=<script>", nil)
	_, errMsg = homeFlash(r)
	if strings.Contains(errMsg, "<script>") {
		t.Fatal("query input reflected into flash")
	}
}
