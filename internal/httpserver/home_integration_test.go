package httpserver

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationPublicHomeAndLoginEntry(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	anonymous := newWSClient(t, env.server.URL)

	resp := anonymous.get("/")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous home status = %d body=%s", resp.StatusCode, body)
	}
	assertPublicLandingContract(t, body)
	if strings.Contains(body, `id="app"`) {
		t.Fatal("anonymous home should not serve the SPA")
	}

	resp = anonymous.get("/login")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(body, `data-copy-text="login.altCta"`) ||
		!strings.Contains(body, publicFeishuAppURL) {
		t.Fatalf("login page status=%d missing register/feishu entry: %s", resp.StatusCode, body)
	}
	assertHanFreePublicBody(t, body)

	resp = anonymous.get("/app")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next=/" {
		t.Fatalf("anonymous /app status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp = anonymous.getWithHeaders("/admin", map[string]string{"Sec-Fetch-Dest": "document"})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/?admin=1" {
		t.Fatalf("anonymous /admin status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, client := env.createUser(t, "U-HOME", "主页测试用户", "active", "user")
	resp = client.get("/")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `id="app"`) ||
		!strings.Contains(body, `window.__YXQ_AUTHENTICATED__=true;`) {
		t.Fatalf("signed-in home status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `class="welcome-hero home-hero"`) {
		t.Fatal("signed-in home rendered the public landing page")
	}
}

func TestIntegrationPublicHomeQRAssetRetired(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	anonymous := newWSClient(t, env.server.URL)

	resp := anonymous.get("/")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous home status = %d body=%s", resp.StatusCode, body)
	}
	assertPublicLandingContract(t, body)

	resp = anonymous.get("/static/feishu-qr.png")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("retired feishu-qr.png status = %d, want 404", resp.StatusCode)
	}
}
