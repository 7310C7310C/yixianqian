// Package httpserver wires routes, middleware and handlers.
package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

// App holds the process-wide dependencies shared by all handlers.
type App struct {
	cfg     config.Config
	db      *db.DB
	store   media.Store
	tpl     *Templates
	log     *slog.Logger
	csrf    *auth.CSRF
	trusted []netip.Prefix
	clock   platform.Clock

	// faceProbe 在照片上传后同步运行；失败只记日志，不阻断上传。
	faceProbe face.Probe

	loginIPLimiter    *auth.Limiter
	loginPhoneLimiter *auth.Limiter
	activationLimiter *auth.Limiter
	uploadLimiter     *auth.Limiter
	// profilePasswordLimiter 限制「修改手机号需当前密码」的验证尝试，避免
	// 已登录会话被用来爆破账号密码。
	profilePasswordLimiter *auth.Limiter

	hashSem  chan struct{}
	mediaSem chan struct{}
	// faceSem 限制本地人脸检测并发；它独立于 mediaSem，避免慢 probe
	// 让普通上传排队超时（429）。
	faceSem chan struct{}

	// registrationFaceProbeBudget 是注册响应前同步检测的总预算；默认
	// maxRegistrationFaceProbeBudget，测试可缩短。
	registrationFaceProbeBudget time.Duration
}

// New loads templates and constructs the application.
func New(cfg config.Config, database *db.DB, store media.Store, logger *slog.Logger) (*App, error) {
	// Directly-constructed Config values in tests may omit optional limits.
	if cfg.MaxHashes <= 0 {
		cfg.MaxHashes = 2
	}
	if cfg.MaxMedia <= 0 {
		cfg.MaxMedia = 1
	}
	if cfg.MediaMaxPixels <= 0 {
		cfg.MediaMaxPixels = media.DefaultMaxPixels
	}
	if cfg.MediaMaxDim <= 0 {
		cfg.MediaMaxDim = media.DefaultMaxDimension
	}
	tpl, err := LoadTemplates()
	if err != nil {
		return nil, err
	}
	trusted, err := platform.ParseTrustedProxies(strings.Join(cfg.TrustedProxies, ","))
	if err != nil {
		return nil, fmt.Errorf("httpserver: trusted proxies: %w", err)
	}
	probe, err := face.NewPythonProbe(cfg.FaceProbeCmd, cfg.FaceProbeTimeout)
	if err != nil {
		return nil, fmt.Errorf("httpserver: face probe: %w", err)
	}
	return &App{
		cfg:                         cfg,
		db:                          database,
		store:                       store,
		tpl:                         tpl,
		log:                         logger,
		csrf:                        auth.NewCSRF(cfg.CSRFKey(), 2*time.Hour),
		trusted:                     trusted,
		clock:                       platform.SystemClock{},
		faceProbe:                   probe,
		loginIPLimiter:              auth.NewLimiter(20, 15*time.Minute),
		loginPhoneLimiter:           auth.NewLimiter(10, 15*time.Minute),
		activationLimiter:           auth.NewLimiter(10, time.Hour),
		uploadLimiter:               auth.NewLimiter(60, time.Hour),
		profilePasswordLimiter:      auth.NewLimiter(10, 15*time.Minute),
		hashSem:                     make(chan struct{}, cfg.MaxHashes),
		mediaSem:                    make(chan struct{}, cfg.MaxMedia),
		faceSem:                     make(chan struct{}, maxConcurrentFaceProbes),
		registrationFaceProbeBudget: maxRegistrationFaceProbeBudget,
	}, nil
}

// acquireGate bounds expensive in-process work (Argon2, image decode). The
// caller must release exactly once when it returns true.
func (a *App) acquireGate(w http.ResponseWriter, r *http.Request, sem chan struct{}, timeout time.Duration) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-time.After(timeout):
		a.renderError(w, r, http.StatusTooManyRequests, "系统繁忙", "目前请求较多，请稍后再试。")
		return false
	case <-r.Context().Done():
		return false
	}
}

func (a *App) acquireGateAPI(w http.ResponseWriter, r *http.Request, sem chan struct{}, timeout time.Duration) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-time.After(timeout):
		writeAPIError(w, http.StatusTooManyRequests, "busy", "目前请求较多，请稍后再试。")
		return false
	case <-r.Context().Done():
		return false
	}
}

func releaseGate(sem chan struct{}) { <-sem }

func (a *App) acquireHash(w http.ResponseWriter, r *http.Request) bool {
	return a.acquireGate(w, r, a.hashSem, 2*time.Second)
}

// acquireHashQuiet 只做 Argon2 并发限流，不直接写响应，方便共用调用方
// 按 HTML 或 legacy JSON 自己渲染失败结果。
func (a *App) acquireHashQuiet(r *http.Request) (ok, busy bool) {
	select {
	case a.hashSem <- struct{}{}:
		return true, false
	case <-time.After(2 * time.Second):
		return false, true
	case <-r.Context().Done():
		return false, false
	}
}

func (a *App) releaseHash() { releaseGate(a.hashSem) }

func (a *App) acquireMedia(w http.ResponseWriter, r *http.Request, api bool) bool {
	if api {
		return a.acquireGateAPI(w, r, a.mediaSem, 2*time.Second)
	}
	return a.acquireGate(w, r, a.mediaSem, 2*time.Second)
}

func (a *App) releaseMedia() { releaseGate(a.mediaSem) }
