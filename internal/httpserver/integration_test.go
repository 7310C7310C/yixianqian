package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
}

func (s *fakeStore) Put(_ context.Context, key string, data []byte, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
	s.types[key] = contentType
	return nil
}

func (s *fakeStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, media.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeStore) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

type wsClient struct {
	t    *testing.T
	base string
	http *http.Client
	jar  http.CookieJar
}

func newWSClient(t *testing.T, base string) *wsClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &wsClient{
		t:    t,
		base: base,
		jar:  jar,
		http: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 15 * time.Second,
		},
	}
}

func (c *wsClient) do(req *http.Request) *http.Response {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	c.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (c *wsClient) get(path string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	return c.do(req)
}

func (c *wsClient) getWithHeaders(path string, headers map[string]string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return c.do(req)
}

func (c *wsClient) postForm(path string, values url.Values) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(values.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

func (c *wsClient) postMultipart(path, csrf, filename, contentType string, data []byte) *http.Response {
	c.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if csrf != "" {
		if err := w.WriteField(auth.CSRFFormField, csrf); err != nil {
			c.t.Fatal(err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		c.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		c.t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, c.base+path, &buf)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if csrf != "" && path == "/api/media" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	return c.do(req)
}

func (c *wsClient) csrfCookie() string {
	c.t.Helper()
	u, err := url.Parse(c.base)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, cookie := range c.jar.Cookies(u) {
		if cookie.Name == auth.CSRFCookieName {
			return cookie.Value
		}
	}
	return ""
}

func (c *wsClient) sessionCookie() string {
	c.t.Helper()
	u, err := url.Parse(c.base)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, cookie := range c.jar.Cookies(u) {
		if cookie.Name == testConfig().SessionCookieName {
			return cookie.Value
		}
	}
	return ""
}

var csrfFieldRE = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (c *wsClient) pageCSRF(path string) string {
	c.t.Helper()
	resp := c.get(path)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s status = %d body=%s", path, resp.StatusCode, body)
	}
	m := csrfFieldRE.FindSubmatch(body)
	if len(m) != 2 {
		c.t.Fatalf("csrf field not found on %s", path)
	}
	return string(m[1])
}

func testConfig() config.Config {
	return config.Config{
		AppEnv:            "test",
		BaseURL:           "http://example.test",
		DatabaseURL:       "unused-in-handler-tests",
		SessionSecret:     strings.Repeat("s", 32),
		SessionCookieName: "yxq_test_session",
		SessionTTL:        time.Hour,
		MediaMaxBytes:     10 << 20,
		MediaThumbSize:    128,
		HEICEnabled:       true,
		HEIFConvertPath:   "heif-convert",
		HEICTimeout:       20 * time.Second,
		ActivationTTL:     time.Hour,
		LogLevel:          slog.LevelError,
		LogFormat:         "text",
	}
}

// TestIntegrationHTTPFlow exercises activation, session cookies, CSRF, upload,
// streaming, logout and login through the real router against PostgreSQL and
// an in-memory object store.
func TestIntegrationHTTPFlow(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	store := newFakeStore()
	app, err := New(cfg, database, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	phone := randomHTTPPhone(t)
	legacy := "U-HTTP-" + randomHTTPDigits(t, 10)
	user, err := database.UpsertLegacyUser(ctx, legacy, phone, "测试用户")
	if err != nil {
		t.Fatal(err)
	}
	rawToken, tokenHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateActivationToken(ctx, &user.ID, "", "", tokenHash, time.Hour); err != nil {
		t.Fatal(err)
	}

	client := newWSClient(t, server.URL)
	csrf := client.pageCSRF("/activate?token=" + url.QueryEscape(rawToken))
	password := "http-flow-password-1"

	resp := client.postForm("/activate", url.Values{
		"csrf_token":       {csrf},
		"token":            {rawToken},
		"password":         {password},
		"password_confirm": {password},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/?activated=1" {
		t.Fatalf("activation status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if client.sessionCookie() == "" {
		t.Fatal("activation did not set a session cookie")
	}
	sessionRaw := client.sessionCookie()

	resp = client.get("/")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `id="app"`) {
		t.Fatalf("legacy app status=%d body=%s", resp.StatusCode, body)
	}
	resp = client.get("/legacy/api/profile")
	body = readBody(t, resp)
	var profile struct {
		UserID string `json:"user_id"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &profile) != nil || profile.UserID != legacy {
		t.Fatalf("legacy profile status=%d missing identity: %s", resp.StatusCode, body)
	}

	resp = client.get("/activate?token=" + url.QueryEscape(rawToken))
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("reused activation status = %d", resp.StatusCode)
	}

	csrf = client.csrfCookie()
	if csrf == "" {
		t.Fatal("csrf cookie missing after activation")
	}

	resp = client.postMultipart("/api/media", "", "photo.jpg", "image/jpeg", testJPEG(t))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("upload without csrf status = %d", resp.StatusCode)
	}

	imageBytes := testJPEG(t)
	resp = client.postMultipart("/api/media", csrf, "photo.jpg", "image/jpeg", imageBytes)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("api upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var uploaded mediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if uploaded.ID == "" || uploaded.Width != 800 || uploaded.Height != 600 {
		t.Fatalf("unexpected upload response: %+v", uploaded)
	}

	resp = client.get("/media/" + uploaded.ID)
	served := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || len(served) == 0 {
		t.Fatalf("media status=%d bytes=%d", resp.StatusCode, len(served))
	}
	// The stored original is re-encoded (metadata stripped), so it must differ
	// from the upload bytes while keeping the decoded dimensions.
	if served == string(imageBytes) {
		t.Fatal("stored original must be re-encoded")
	}
	if cfg, format, err := image.DecodeConfig(strings.NewReader(served)); err != nil || format != "jpeg" || cfg.Width != 800 || cfg.Height != 600 {
		t.Fatalf("stored original not a valid 800x600 jpeg: format=%q cfg=%+v err=%v", format, cfg, err)
	}
	if resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("media content type = %q", resp.Header.Get("Content-Type"))
	}

	resp = client.get("/media/" + uploaded.ID + "?size=thumb")
	thumb := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(thumb) == 0 {
		t.Fatalf("thumb status=%d type=%q len=%d", resp.StatusCode, resp.Header.Get("Content-Type"), len(thumb))
	}
	if _, _, err := image.DecodeConfig(strings.NewReader(thumb)); err != nil {
		t.Fatalf("thumbnail is not a decodable image: %v", err)
	}

	heic := make([]byte, 32)
	heic[4], heic[5], heic[6], heic[7] = 'f', 't', 'y', 'p'
	heic[8], heic[9], heic[10], heic[11] = 'h', 'e', 'i', 'c'
	resp = client.postMultipart("/api/media", csrf, "photo.heic", "image/heic", heic)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("heic status = %d", resp.StatusCode)
	}
	var apiErr struct {
		Error apiError `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	// A malformed HEIC container is detected, then the converter fails; the
	// user still gets the actionable "convert to JPEG" message.
	if apiErr.Error.Code != "heic_conversion_failed" {
		t.Fatalf("heic code = %q", apiErr.Error.Code)
	}
	if !strings.Contains(apiErr.Error.Message, "HEIC") || !strings.Contains(apiErr.Error.Message, "JPEG") || !strings.Contains(apiErr.Error.Message, "高效") {
		t.Fatalf("heic message must be actionable, got %q", apiErr.Error.Message)
	}

	resp = client.postMultipart("/media/upload", csrf, "form.png", "image/png", testPNG(t))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/?uploaded=1" {
		t.Fatalf("form upload status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// The legacy media form endpoint is kept for the P0 upload pipeline; profile
	// photos are covered by the profile integration test.

	resp = client.postForm("/logout", url.Values{"csrf_token": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	if client.sessionCookie() != "" {
		t.Fatal("session cookie was not cleared")
	}

	// The old raw token must no longer authenticate after logout. Anonymous
	// home is now the public landing page, so assert the SPA flag is absent.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: cfg.SessionCookieName, Value: sessionRaw})
	resp = newWSClient(t, server.URL).do(req)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, `window.__YXQ_AUTHENTICATED__=true;`) {
		t.Fatalf("revoked session status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `class="welcome-hero home-hero"`) {
		t.Fatal("revoked session did not fall back to the public landing page")
	}

	// A session for another user must not read the media (404, not 403, to
	// avoid confirming existence).
	other, err := database.UpsertLegacyUser(ctx, "U-HTTP-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10), "其他用户")
	if err != nil {
		t.Fatal(err)
	}
	otherRaw, otherHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(ctx, other.ID, otherHash, time.Hour, "", "http-test"); err != nil {
		t.Fatal(err)
	}
	otherReq, _ := http.NewRequest(http.MethodGet, server.URL+"/media/"+uploaded.ID, nil)
	otherReq.AddCookie(&http.Cookie{Name: cfg.SessionCookieName, Value: otherRaw})
	otherResp := newWSClient(t, server.URL).do(otherReq)
	if otherResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user media status = %d", otherResp.StatusCode)
	}

	// Login: four 401s, then lockout, then reset and succeed.
	loginClient := newWSClient(t, server.URL)
	loginCSRF := loginClient.pageCSRF("/")
	for i := 1; i <= 4; i++ {
		resp = loginClient.postForm("/login", url.Values{
			"csrf_token": {loginCSRF}, "phone": {phone}, "password": {"wrong-password"},
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", i, resp.StatusCode)
		}
	}
	resp = loginClient.postForm("/login", url.Values{
		"csrf_token": {loginCSRF}, "phone": {phone}, "password": {"wrong-password"},
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("fifth attempt status = %d", resp.StatusCode)
	}
	resp = loginClient.postForm("/login", url.Values{
		"csrf_token": {loginCSRF}, "phone": {phone}, "password": {password},
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked login status = %d", resp.StatusCode)
	}
	if err := database.ResetLoginFailures(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	resp = loginClient.postForm("/login", url.Values{
		"csrf_token": {loginCSRF}, "phone": {phone}, "password": {password},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if loginClient.sessionCookie() == "" {
		t.Fatal("login did not set session cookie")
	}
	resp = loginClient.get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-login app status = %d", resp.StatusCode)
	}

	// Anonymous media access is rejected.
	anon := newWSClient(t, server.URL)
	resp = anon.get("/media/" + uploaded.ID)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous media status = %d", resp.StatusCode)
	}

	resp = anon.get("/healthz")
	health := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(health, `"status":"ok"`) {
		t.Fatalf("health status=%d body=%s", resp.StatusCode, health)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func randomHTTPPhone(t *testing.T) string {
	t.Helper()
	return "138" + randomHTTPDigits(t, 8)
}

func randomHTTPDigits(t *testing.T, n int) string {
	t.Helper()
	b, err := platform.RandomBytes(n)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = '0' + v%10
	}
	return string(out)
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
