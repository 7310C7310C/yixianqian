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
	for _, want := range []string{
		"一线牵",
		"立即注册",
		`href="/register?from=home"`,
		`href="/login"`,
		"已在飞书版使用过",
		publicFeishuAppURL,
		`/static/feishu-qr.png`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("anonymous home missing %q", want)
		}
	}
	if strings.Contains(body, `id="app"`) {
		t.Fatal("anonymous home should not serve the SPA")
	}

	resp = anonymous.get("/login")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "注册新账号") ||
		!strings.Contains(body, publicFeishuAppURL) {
		t.Fatalf("login page status=%d missing register/feishu entry: %s", resp.StatusCode, body)
	}

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
	if strings.Contains(body, "已在飞书版使用过") {
		t.Fatal("signed-in home rendered the public landing page")
	}
}

func TestIntegrationPublicHomeQRAsset(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	anonymous := newWSClient(t, env.server.URL)

	resp := anonymous.get("/")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous home status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `/static/feishu-qr.png`) || !strings.Contains(body, publicFeishuAppURL) {
		t.Fatalf("anonymous home missing fixed QR asset or feishu link: %s", body)
	}

	resp = anonymous.get("/static/feishu-qr.png")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feishu-qr.png status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("feishu-qr.png content-type = %q", ct)
	}
	if !strings.HasPrefix(body, "\x89PNG\r\n\x1a\n") {
		t.Fatal("feishu-qr.png is not a PNG")
	}
}
