package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func minimalEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":   "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"SESSION_SECRET": strings.Repeat("s", 32),
	}
}

func envGetter(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(envGetter(minimalEnv()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AppEnv != "development" || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if !cfg.SessionCookieSecure {
		t.Fatal("session cookie should default to Secure")
	}
	if !cfg.AutoMigrate {
		t.Fatal("auto migrate should default on")
	}
	if cfg.SessionTTL != 720*time.Hour {
		t.Fatalf("session ttl = %v", cfg.SessionTTL)
	}
	if cfg.ActivationTTL != 24*time.Hour {
		t.Fatalf("activation ttl = %v", cfg.ActivationTTL)
	}
	if cfg.MediaThumbSize != 512 || cfg.MediaMaxBytes != 10<<20 {
		t.Fatalf("media defaults: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "45.125.16.205/32" {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxies)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != "json" {
		t.Fatalf("log defaults: %v %q", cfg.LogLevel, cfg.LogFormat)
	}
	if len(cfg.CSRFKey()) != 32 {
		t.Fatal("csrf key must be 32 bytes")
	}
	if cfg.FaceProbeCmd != "python3 tools/face_probe.py" || cfg.FaceProbeTimeout != 8*time.Second {
		t.Fatalf("face probe defaults: %q %v", cfg.FaceProbeCmd, cfg.FaceProbeTimeout)
	}
}

func TestLoadRequiresSecrets(t *testing.T) {
	_, err := LoadFrom(envGetter(map[string]string{}))
	if err == nil {
		t.Fatal("expected errors")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("missing required errors: %v", err)
	}
}

func TestBaseURLValidation(t *testing.T) {
	for _, raw := range []string{
		"localhost:8080",
		"ftp://example.com",
		"https://example.com/app",
		"https://example.com?x=1",
	} {
		env := minimalEnv()
		env["BASE_URL"] = raw
		if _, err := LoadFrom(envGetter(env)); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
	env := minimalEnv()
	env["BASE_URL"] = "https://example.com/"
	cfg, err := LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Fatalf("trailing slash not trimmed: %q", cfg.BaseURL)
	}
}

func TestEndpointNormalization(t *testing.T) {
	env := minimalEnv()
	env["R2_ENDPOINT"] = "https://abc.r2.cloudflarestorage.com"
	cfg, err := LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2Endpoint != "abc.r2.cloudflarestorage.com" || !cfg.R2Secure {
		t.Fatalf("endpoint = %q secure=%v", cfg.R2Endpoint, cfg.R2Secure)
	}

	env["R2_ENDPOINT"] = "http://localhost:9000"
	cfg, err = LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2Endpoint != "localhost:9000" || cfg.R2Secure {
		t.Fatalf("endpoint = %q secure=%v", cfg.R2Endpoint, cfg.R2Secure)
	}

	env["R2_ENDPOINT"] = "https://abc.example.com"
	env["R2_SECURE"] = "false"
	cfg, err = LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2Secure {
		t.Fatal("explicit R2_SECURE=false must win")
	}

	env["R2_ENDPOINT"] = "not a host/path"
	if _, err := LoadFrom(envGetter(env)); err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestInvalidSettings(t *testing.T) {
	cases := map[string]string{
		"SESSION_COOKIE_SECURE": "maybe",
		"SESSION_TTL":           "soon",
		"ACTIVATION_TTL":        "0s",
		"MEDIA_MAX_BYTES":       "1",
		"MEDIA_THUMB_SIZE":      "99999",
		"TRUSTED_PROXY":         "bogus",
		"LOG_FORMAT":            "xml",
		"LOG_LEVEL":             "chatty",
		"SESSION_COOKIE_NAME":   "bad name",
		"CSRF_SECRET":           "short",
	}
	for key, value := range cases {
		env := minimalEnv()
		env[key] = value
		if _, err := LoadFrom(envGetter(env)); err == nil {
			t.Fatalf("expected error for %s=%s", key, value)
		}
	}
}

func TestValidateServer(t *testing.T) {
	env := minimalEnv()
	cfg, err := LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateServer(); err == nil {
		t.Fatal("expected missing R2 settings to fail")
	}
	env["R2_ENDPOINT"] = "https://abc.example.com"
	env["R2_ACCESS_KEY_ID"] = "key"
	env["R2_SECRET_ACCESS_KEY"] = "secret"
	env["R2_BUCKET"] = "bucket"
	cfg, err = LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateServer(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestCSRFKeyDerivation(t *testing.T) {
	env := minimalEnv()
	cfg1, err := LoadFrom(envGetter(env))
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := cfg1
	cfg2.SessionSecret = strings.Repeat("t", 32)
	if string(cfg1.CSRFKey()) == string(cfg2.CSRFKey()) {
		t.Fatal("derived key should change with the session secret")
	}
	cfg3 := cfg1
	cfg3.CSRFSecret = strings.Repeat("c", 32)
	if string(cfg1.CSRFKey()) == string(cfg3.CSRFKey()) {
		t.Fatal("dedicated csrf secret should be used")
	}
}

func TestIsProduction(t *testing.T) {
	cfg := Config{AppEnv: "production"}
	if !cfg.IsProduction() {
		t.Fatal("production not detected")
	}
	cfg.AppEnv = "development"
	if cfg.IsProduction() {
		t.Fatal("development misdetected")
	}
}

func TestFaceProbeConfig(t *testing.T) {
	env := minimalEnv()
	env["FACE_PROBE_CMD"] = "/opt/venv/bin/python tools/face_probe.py"
	env["FACE_PROBE_TIMEOUT"] = "5s"
	cfg, err := LoadFrom(envGetter(env))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FaceProbeCmd != env["FACE_PROBE_CMD"] || cfg.FaceProbeTimeout != 5*time.Second {
		t.Fatalf("face probe config: %q %v", cfg.FaceProbeCmd, cfg.FaceProbeTimeout)
	}
	for _, raw := range []string{"500ms", "2m", "nope"} {
		env := minimalEnv()
		env["FACE_PROBE_TIMEOUT"] = raw
		if _, err := LoadFrom(envGetter(env)); err == nil {
			t.Fatalf("expected error for FACE_PROBE_TIMEOUT=%q", raw)
		}
	}
}
