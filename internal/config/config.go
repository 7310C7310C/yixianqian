// Package config is the single entry point for environment configuration.
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

const (
	defaultTrustedProxy  = "45.125.16.205"
	defaultSessionTTL    = 720 * time.Hour
	defaultActivationTTL = 24 * time.Hour
	defaultMediaMaxBytes = 10 << 20
	defaultThumbSize     = 512
	defaultMaxHashes     = 2
	defaultMaxMedia      = 1
	defaultMediaPixels   = 12_000_000
	defaultMediaDim      = 8000
	defaultFaceProbeCmd  = "python3 tools/face_probe.py"
	defaultFaceProbeTime = 8 * time.Second
)

// Config holds every runtime setting. Secrets are never logged.
type Config struct {
	AppEnv              string
	HTTPAddr            string
	BaseURL             string
	DatabaseURL         string
	SessionSecret       string
	CSRFSecret          string
	SessionCookieName   string
	SessionCookieSecure bool
	SessionTTL          time.Duration
	TrustedProxies      []string
	AutoMigrate         bool
	// ActivityRosterDisabled hides the signup roster behind a count-only view.
	// Zero value keeps the v1 behaviour (roster visible to logged-in users).
	ActivityRosterDisabled bool

	R2Endpoint        string
	R2Region          string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Bucket          string
	R2Secure          bool

	MediaMaxBytes  int64
	MediaThumbSize int
	MediaMaxPixels int64
	MediaMaxDim    int
	MaxHashes      int
	MaxMedia       int
	ActivationTTL  time.Duration

	// FaceProbeCmd 是本地人脸检测命令（空格分隔，图片路径追加在末尾）。
	// FaceProbeTimeout 是单张照片检测超时；失败只记录并放行上传。
	FaceProbeCmd     string
	FaceProbeTimeout time.Duration

	LogLevel  slog.Level
	LogFormat string
}

// Load reads configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom is Load with an injectable getter, used by tests.
func LoadFrom(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	var errs []error
	cfg := Config{
		AppEnv:            get("APP_ENV", "development"),
		HTTPAddr:          get("HTTP_ADDR", ":8080"),
		BaseURL:           get("BASE_URL", "http://localhost:8080"),
		DatabaseURL:       get("DATABASE_URL", ""),
		SessionSecret:     get("SESSION_SECRET", ""),
		CSRFSecret:        get("CSRF_SECRET", ""),
		SessionCookieName: get("SESSION_COOKIE_NAME", "yxq_session"),
		R2Endpoint:        get("R2_ENDPOINT", ""),
		R2Region:          get("R2_REGION", "auto"),
		R2AccessKeyID:     get("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey: get("R2_SECRET_ACCESS_KEY", ""),
		R2Bucket:          get("R2_BUCKET", ""),
		LogFormat:         strings.ToLower(get("LOG_FORMAT", "json")),
		FaceProbeCmd:      get("FACE_PROBE_CMD", defaultFaceProbeCmd),
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("config: DATABASE_URL is required"))
	}
	if len(cfg.SessionSecret) < 32 {
		errs = append(errs, errors.New("config: SESSION_SECRET must be at least 32 characters"))
	}
	if cfg.CSRFSecret != "" && len(cfg.CSRFSecret) < 32 {
		errs = append(errs, errors.New("config: CSRF_SECRET must be at least 32 characters when set"))
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		errs = append(errs, fmt.Errorf("config: BASE_URL must be an absolute http(s) URL, got %q", cfg.BaseURL))
	} else if base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		errs = append(errs, errors.New("config: BASE_URL must be an origin without path, query or fragment"))
	} else {
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	}

	if err := validateCookieName(cfg.SessionCookieName); err != nil {
		errs = append(errs, err)
	}

	if v, err := parseBool(getenv("SESSION_COOKIE_SECURE"), true); err != nil {
		errs = append(errs, fmt.Errorf("config: SESSION_COOKIE_SECURE: %w", err))
	} else {
		cfg.SessionCookieSecure = v
	}
	if v, err := parseBool(getenv("AUTO_MIGRATE"), true); err != nil {
		errs = append(errs, fmt.Errorf("config: AUTO_MIGRATE: %w", err))
	} else {
		cfg.AutoMigrate = v
	}
	if v, err := parseBool(getenv("ACTIVITY_ROSTER_ENABLED"), true); err != nil {
		errs = append(errs, fmt.Errorf("config: ACTIVITY_ROSTER_ENABLED: %w", err))
	} else {
		cfg.ActivityRosterDisabled = !v
	}

	if v, err := parseDuration(getenv("SESSION_TTL"), defaultSessionTTL); err != nil {
		errs = append(errs, fmt.Errorf("config: SESSION_TTL: %w", err))
	} else {
		cfg.SessionTTL = v
	}
	if v, err := parseDuration(getenv("ACTIVATION_TTL"), defaultActivationTTL); err != nil {
		errs = append(errs, fmt.Errorf("config: ACTIVATION_TTL: %w", err))
	} else {
		cfg.ActivationTTL = v
	}
	if cfg.SessionTTL <= 0 || cfg.SessionTTL > 365*24*time.Hour {
		errs = append(errs, errors.New("config: SESSION_TTL must be between 1s and 8760h"))
	}
	if cfg.ActivationTTL <= 0 || cfg.ActivationTTL > 30*24*time.Hour {
		errs = append(errs, errors.New("config: ACTIVATION_TTL must be between 1s and 720h"))
	}

	if v, err := parseInt64(getenv("MEDIA_MAX_BYTES"), defaultMediaMaxBytes); err != nil {
		errs = append(errs, fmt.Errorf("config: MEDIA_MAX_BYTES: %w", err))
	} else {
		cfg.MediaMaxBytes = v
	}
	if cfg.MediaMaxBytes < 1<<20 || cfg.MediaMaxBytes > 64<<20 {
		errs = append(errs, errors.New("config: MEDIA_MAX_BYTES must be between 1 MiB and 64 MiB"))
	}
	if v, err := parseInt64(getenv("MEDIA_THUMB_SIZE"), defaultThumbSize); err != nil {
		errs = append(errs, fmt.Errorf("config: MEDIA_THUMB_SIZE: %w", err))
	} else {
		cfg.MediaThumbSize = int(v)
	}
	if cfg.MediaThumbSize < 32 || cfg.MediaThumbSize > 2048 {
		errs = append(errs, errors.New("config: MEDIA_THUMB_SIZE must be between 32 and 2048"))
	}
	if v, err := parseInt64(getenv("MEDIA_MAX_PIXELS"), defaultMediaPixels); err != nil {
		errs = append(errs, fmt.Errorf("config: MEDIA_MAX_PIXELS: %w", err))
	} else {
		cfg.MediaMaxPixels = v
	}
	if cfg.MediaMaxPixels < 1_000_000 || cfg.MediaMaxPixels > 24_000_000 {
		errs = append(errs, errors.New("config: MEDIA_MAX_PIXELS must be between 1000000 and 24000000"))
	}
	if v, err := parseInt64(getenv("MEDIA_MAX_DIMENSION"), defaultMediaDim); err != nil {
		errs = append(errs, fmt.Errorf("config: MEDIA_MAX_DIMENSION: %w", err))
	} else {
		cfg.MediaMaxDim = int(v)
	}
	if cfg.MediaMaxDim < 1000 || cfg.MediaMaxDim > 12000 {
		errs = append(errs, errors.New("config: MEDIA_MAX_DIMENSION must be between 1000 and 12000"))
	}
	if v, err := parseInt64(getenv("MAX_CONCURRENT_HASHES"), defaultMaxHashes); err != nil {
		errs = append(errs, fmt.Errorf("config: MAX_CONCURRENT_HASHES: %w", err))
	} else {
		cfg.MaxHashes = int(v)
	}
	if cfg.MaxHashes < 1 || cfg.MaxHashes > 16 {
		errs = append(errs, errors.New("config: MAX_CONCURRENT_HASHES must be between 1 and 16"))
	}
	if v, err := parseInt64(getenv("MAX_CONCURRENT_MEDIA"), defaultMaxMedia); err != nil {
		errs = append(errs, fmt.Errorf("config: MAX_CONCURRENT_MEDIA: %w", err))
	} else {
		cfg.MaxMedia = int(v)
	}
	if cfg.MaxMedia < 1 || cfg.MaxMedia > 4 {
		errs = append(errs, errors.New("config: MAX_CONCURRENT_MEDIA must be between 1 and 4"))
	}
	if v, err := parseDuration(getenv("FACE_PROBE_TIMEOUT"), defaultFaceProbeTime); err != nil {
		errs = append(errs, fmt.Errorf("config: FACE_PROBE_TIMEOUT: %w", err))
	} else {
		cfg.FaceProbeTimeout = v
	}
	if cfg.FaceProbeTimeout < time.Second || cfg.FaceProbeTimeout > time.Minute {
		errs = append(errs, errors.New("config: FACE_PROBE_TIMEOUT must be between 1s and 60s"))
	}

	trusted, err := platform.ParseTrustedProxies(get("TRUSTED_PROXY", defaultTrustedProxy))
	if err != nil {
		errs = append(errs, fmt.Errorf("config: TRUSTED_PROXY: %w", err))
	} else {
		for _, p := range trusted {
			cfg.TrustedProxies = append(cfg.TrustedProxies, p.String())
		}
	}

	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("config: LOG_FORMAT must be json or text, got %q", cfg.LogFormat))
	}
	level, err := platform.ParseLogLevel(get("LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, fmt.Errorf("config: LOG_LEVEL: %w", err))
	}
	cfg.LogLevel = level

	// The endpoint may be given as https://host or bare host. R2_SECURE wins
	// when explicitly set; otherwise the scheme decides, defaulting to true.
	secureExplicit := strings.TrimSpace(getenv("R2_SECURE")) != ""
	secure, err := parseBool(getenv("R2_SECURE"), true)
	if err != nil {
		errs = append(errs, fmt.Errorf("config: R2_SECURE: %w", err))
	}
	if cfg.R2Endpoint != "" {
		host, fromScheme, schemeSecure, err := normalizeEndpoint(cfg.R2Endpoint)
		if err != nil {
			errs = append(errs, fmt.Errorf("config: R2_ENDPOINT: %w", err))
		} else {
			cfg.R2Endpoint = host
			if secureExplicit {
				cfg.R2Secure = secure
			} else if fromScheme {
				cfg.R2Secure = schemeSecure
			} else {
				cfg.R2Secure = secure
			}
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

// ValidateServer checks the additional settings required by cmd/server.
func (c Config) ValidateServer() error {
	var errs []error
	if c.R2Endpoint == "" {
		errs = append(errs, errors.New("config: R2_ENDPOINT is required for the server"))
	}
	if c.R2AccessKeyID == "" {
		errs = append(errs, errors.New("config: R2_ACCESS_KEY_ID is required for the server"))
	}
	if c.R2SecretAccessKey == "" {
		errs = append(errs, errors.New("config: R2_SECRET_ACCESS_KEY is required for the server"))
	}
	if c.R2Bucket == "" {
		errs = append(errs, errors.New("config: R2_BUCKET is required for the server"))
	}
	return errors.Join(errs...)
}

// CSRFKey derives the fixed-size HMAC key. A dedicated secret is preferred;
// otherwise the session secret is domain-separated with SHA-256.
func (c Config) CSRFKey() []byte {
	raw := c.CSRFSecret
	if raw == "" {
		raw = "yixianqian/csrf/v1:" + c.SessionSecret
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// IsProduction reports whether production-only defaults should apply.
func (c Config) IsProduction() bool {
	return c.AppEnv == "production" || c.AppEnv == "prod"
}

func normalizeEndpoint(raw string) (host string, fromScheme bool, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, true, errors.New("empty endpoint")
	}
	candidate := raw
	if !strings.Contains(raw, "://") {
		candidate = "https://" + raw
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return "", false, true, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false, true, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, true, fmt.Errorf("endpoint must be scheme://host[:port], got %q", raw)
	}
	return u.Host, strings.Contains(raw, "://"), u.Scheme == "https", nil
}

func validateCookieName(name string) error {
	if name == "" {
		return errors.New("config: SESSION_COOKIE_NAME is required")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("config: SESSION_COOKIE_NAME contains invalid character %q", r)
	}
	return nil
}

func parseBool(raw string, def bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return strconv.ParseBool(raw)
}

func parseDuration(raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return time.ParseDuration(raw)
}

func parseInt64(raw string, def int64) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
