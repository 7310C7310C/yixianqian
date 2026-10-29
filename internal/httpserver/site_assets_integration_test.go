package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestIntegrationAngelQRUploadAndPublicServe(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	if _, err := pool.Exec(ctx, `DELETE FROM site_assets WHERE key = 'angel_qr'`); err != nil {
		t.Fatalf("clean site asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log
		WHERE action = 'admin.site_asset.update' AND target_id = 'angel_qr'`); err != nil {
		t.Fatalf("clean site asset audit: %v", err)
	}
	moderator := createMessageUser(t, ctx, database, pool, "二维码管理员")
	plain := createMessageUser(t, ctx, database, pool, "二维码普通用户")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM site_assets WHERE key = 'angel_qr'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]int64{moderator.ID, plain.ID})
	})
	if changed, err := database.AssignAdminRole(ctx, moderator.ID, "content_moderator", true); err != nil || !changed {
		t.Fatalf("assign content_moderator: changed=%v err=%v", changed, err)
	}

	anonymous := newWSClient(t, server.URL)
	resp := anonymous.get("/angel-qr")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("angel-qr before upload = %d, want 404", resp.StatusCode)
	}

	modClient := loginMessageClient(t, database, server, moderator)
	plainClient := loginMessageClient(t, database, server, plain)
	modCSRF := modClient.pageCSRF("/admin")
	plainCSRF := plainClient.pageCSRF("/register")
	png := testAngelPNGSize(t, 1074, 1458)
	resp = plainClient.postMultipart("/admin/settings/angel-qr", plainCSRF, "angel.png", "image/png", png)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain upload status = %d, want 403", resp.StatusCode)
	}
	resp = modClient.postMultipart("/admin/settings/angel-qr", modCSRF, "angel.png", "image/png", png)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/settings/angel-qr?saved=1" {
		t.Fatalf("moderator upload status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp = anonymous.get("/angel-qr")
	body := readBodyBytes(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("angel-qr after upload status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") {
		t.Fatalf("angel-qr cache-control = %q", cc)
	}
	if resp.Header.Get("ETag") == "" || len(body) == 0 {
		t.Fatalf("angel-qr etag=%q bytes=%d", resp.Header.Get("ETag"), len(body))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || cfg.Width > 720 || cfg.Height > 720 {
		t.Fatalf("angel-qr format=%q size=%dx%d err=%v, want jpeg <=720", format, cfg.Width, cfg.Height, err)
	}
	etag := resp.Header.Get("ETag")
	resp = anonymous.getWithHeaders("/angel-qr", map[string]string{"If-None-Match": etag})
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("angel-qr conditional status = %d", resp.StatusCode)
	}

	// HEIC input goes through the subprocess converter and is stored as JPEG.
	heic, err := os.ReadFile("../media/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read HEIC fixture: %v", err)
	}
	resp = modClient.postMultipart("/admin/settings/angel-qr", modCSRF, "angel.heic", "image/heic", heic)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/settings/angel-qr?saved=1" {
		t.Fatalf("HEIC angel upload status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp = anonymous.get("/angel-qr")
	body = readBodyBytes(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("HEIC angel-qr status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	cfg, format, err = image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("HEIC angel-qr format=%q size=%dx%d err=%v", format, cfg.Width, cfg.Height, err)
	}
	etag = resp.Header.Get("ETag")

	resp = anonymous.get("/register")
	register := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(register, `src="/angel-qr?v=`+strings.Trim(etag, `"`)+`"`) {
		t.Fatalf("register page missing versioned angel QR: status=%d", resp.StatusCode)
	}
	assertHanFreePublicBody(t, register)

	resp = anonymous.get("/register/done")
	done := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(done, `src="/angel-qr?v=`) {
		t.Fatalf("register done missing angel QR: status=%d", resp.StatusCode)
	}

	var audits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'admin.site_asset.update' AND target_id = 'angel_qr'`).Scan(&audits); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 2 {
		t.Fatalf("site asset audit rows = %d, want 2", audits)
	}
}

func TestFlattenOnWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	src.SetNRGBA(2, 2, color.NRGBA{R: 200, A: 255})
	flat := flattenOnWhite(src)
	if r, g, b, a := flat.At(0, 0).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
		t.Fatalf("transparent corner = %04x %04x %04x %04x, want white", r, g, b, a)
	}
	if r, _, _, _ := flat.At(2, 2).RGBA(); r == 0 || r == 0xffff {
		t.Fatalf("opaque pixel lost: r=%04x", r)
	}
}

func testAngelPNGSize(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
