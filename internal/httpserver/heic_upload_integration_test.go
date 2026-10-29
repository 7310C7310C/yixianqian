package httpserver

import (
	"bytes"
	"encoding/json"
	"image"
	"net/http"
	"os"
	"testing"
)

func TestIntegrationHEICUploadNormalizesToJPEG(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	_, client := env.createUser(t, "U-HEIC", "HEIC 上传用户", "active", "user")
	csrf := client.pageCSRF("/register")
	data, err := os.ReadFile("../media/testdata/sample.heic")
	if err != nil {
		t.Skipf("HEIC fixture unavailable: %v", err)
	}
	resp := client.postMultipart("/api/media", csrf, "sample.heic", "image/heic", data)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("HEIC upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var uploaded mediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if uploaded.ID == "" || uploaded.Width != 64 || uploaded.Height != 64 {
		t.Fatalf("unexpected HEIC upload: %+v", uploaded)
	}
	resp = client.get("/media/" + uploaded.ID)
	body := readBodyBytes(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("HEIC media status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("HEIC media format=%q size=%dx%d err=%v", format, cfg.Width, cfg.Height, err)
	}
}
