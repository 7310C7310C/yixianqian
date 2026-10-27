// Package httpserver wires routes, middleware and handlers.
package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
	"github.com/7310C7310C/yixianqian/internal/tencentface"
)

// App holds the process-wide dependencies shared by all handlers.
type App struct {
	cfg   config.Config
	db    *db.DB
	store media.Store
	// thumbCache 只缓存 ?size=thumb 的字节；nil = 未配置缓存。
	thumbCache *media.ThumbCache
	// 命中/未命中计数（运维观察用，不参与逻辑）。
	thumbCacheHits   atomic.Int64
	thumbCacheMisses atomic.Int64
	tpl              *Templates
	log              *slog.Logger
	csrf             *auth.CSRF
	trusted          []netip.Prefix
	clock            platform.Clock

	// faceProbe 在照片上传后同步运行；失败只记日志，不阻断上传。
	faceProbe face.Probe
	// faceCompliance 是腾讯云照片合规判定（图像标签 + 静默活体）；nil =
	// 未启用。它只影响 has_face，不产生人脸几何。
	faceCompliance face.Compliance

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

	// notifier 为 SSE 通知流提供 LISTEN/NOTIFY 唤醒；database 为 nil 的
	// 纯 handler 单测里保持 nil，流退回 15 秒兜底轮询。
	notifier *db.NotificationHub

	// homeInflight/cardsInflight 合并同一用户同一查询的并发重复构建。
	homeInflight  inflightGroup
	cardsInflight inflightGroup
	// readCache 是批次9 的 10s 成功响应缓存（home+cards **共享**同一实例与同一份
	// 32MiB/1024 条总预算，避免两接口各自 32MiB）。
	readCache *readCache

	// photoGrants 记住「哪些 /cards/photo 媒体曾下发给哪个 viewer」，让牌堆里
	// 尚未建立点赞/活动关系的候选照片可被本人短时读取；默认拒绝，见 photo_grants.go。
	photoGrants *photoGrantStore
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
	// 照片配额日界时区：Load() 已校验；直接构造 Config 的测试/工具也走同一
	// 默认与校验，非法值直接拒绝启动（决策 44）。
	if cfg.PhotoQuotaLocation == nil {
		name := cfg.PhotoQuotaTZ
		if name == "" {
			name = "Asia/Shanghai"
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			return nil, fmt.Errorf("httpserver: photo quota timezone %q: %w", name, err)
		}
		cfg.PhotoQuotaLocation = loc
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
	// 腾讯云照片合规判定：默认关闭；开启但缺凭证时只警告并继续（运行期
	// fail-open），不阻断启动。凭证不写日志。
	var faceCompliance face.Compliance
	if cfg.TencentFaceEnabled {
		checker, err := tencentface.NewChecker(tencentface.Options{
			SecretID:  cfg.TencentSecretID,
			SecretKey: cfg.TencentSecretKey,
			Region:    cfg.TencentRegion,
			Timeout:   cfg.TencentFaceTimeout,
			Logger:    logger,
		})
		if err != nil {
			return nil, fmt.Errorf("httpserver: tencent face: %w", err)
		}
		if !checker.Configured() {
			logger.Warn("tencent face enabled without credentials; photo compliance will fail open")
		}
		faceCompliance = checker
	}
	var notifier *db.NotificationHub
	if database != nil {
		notifier = db.NewNotificationHub(context.Background(), database.DSN(), logger)
	}
	// 性能护栏（AGENTS.md「性能护栏」）：thumbCache 与 readCache 都是有界优化，
	// 初始化失败必须降级而不是拒绝启动；改动须保留预算/epoch 语义并跑 perf-bench。
	thumbCache, cacheErr := media.NewThumbCache(cfg.MediaCacheDir, cfg.MediaCacheBytes)
	if cacheErr != nil {
		// 批次9：缓存目录不可写/不存在时降级关闭，不得拒绝启动（批次8 曾因此
		// 部署失败）。缓存是纯优化，失败只影响命中率。
		logger.Warn("thumb cache disabled", "dir", cfg.MediaCacheDir, "error", cacheErr)
		thumbCache = nil
	}
	if thumbCache != nil {
		entries, bytes := thumbCache.Stats()
		logger.Info("thumb cache ready", "dir", cfg.MediaCacheDir, "limit_bytes", cfg.MediaCacheBytes,
			"entries", entries, "bytes", bytes)
	}

	app := &App{
		cfg:                         cfg,
		db:                          database,
		store:                       store,
		thumbCache:                  thumbCache,
		readCache:                   newReadCacheFromConfig(cfg),
		tpl:                         tpl,
		log:                         logger,
		csrf:                        auth.NewCSRF(cfg.CSRFKey(), 2*time.Hour),
		trusted:                     trusted,
		clock:                       platform.SystemClock{},
		faceProbe:                   probe,
		faceCompliance:              faceCompliance,
		loginIPLimiter:              auth.NewLimiter(20, 15*time.Minute),
		loginPhoneLimiter:           auth.NewLimiter(10, 15*time.Minute),
		activationLimiter:           auth.NewLimiter(10, time.Hour),
		uploadLimiter:               auth.NewLimiter(60, time.Hour),
		profilePasswordLimiter:      auth.NewLimiter(10, 15*time.Minute),
		hashSem:                     make(chan struct{}, cfg.MaxHashes),
		mediaSem:                    make(chan struct{}, cfg.MaxMedia),
		faceSem:                     make(chan struct{}, maxConcurrentFaceProbes),
		registrationFaceProbeBudget: maxRegistrationFaceProbeBudget,
		notifier:                    notifier,
	}
	app.photoGrants = newPhotoGrantStore(defaultPhotoGrantTTL, defaultPhotoGrantMaxViewers,
		defaultPhotoGrantMaxTargets, app.clock.Now)
	return app, nil
}

// Close releases background resources owned by the app. Callers that build an
// App in tests must call it so the LISTEN connection does not outlive the DB.
func (a *App) Close() {
	if a.notifier != nil {
		a.notifier.Close()
	}
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
