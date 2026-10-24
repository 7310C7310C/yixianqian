package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/httpserver"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(cfg.LogLevel, cfg.LogFormat, os.Stdout)
	slog.SetDefault(logger)

	if err := cfg.ValidateServer(); err != nil {
		return err
	}
	if cfg.IsProduction() && !cfg.SessionCookieSecure {
		logger.Warn("session cookie is not marked Secure; production traffic must be HTTPS")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	database, err := db.Open(openCtx, cfg.DatabaseURL)
	cancelOpen()
	if err != nil {
		return err
	}
	defer database.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	err = database.Ping(pingCtx)
	cancelPing()
	if err != nil {
		return err
	}

	if cfg.AutoMigrate {
		migrateCtx, cancelMigrate := context.WithTimeout(ctx, 60*time.Second)
		err = database.Migrate(migrateCtx, logger)
		cancelMigrate()
		if err != nil {
			return err
		}
	}

	store, err := media.NewR2(media.R2Config{
		Endpoint:        cfg.R2Endpoint,
		Region:          cfg.R2Region,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		Secure:          cfg.R2Secure,
	})
	if err != nil {
		return err
	}
	bucketCtx, cancelBucket := context.WithTimeout(ctx, 10*time.Second)
	err = store.EnsureBucket(bucketCtx)
	cancelBucket()
	if err != nil {
		return err
	}

	app, err := httpserver.New(cfg, database, store, logger)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	go cleanupLoop(ctx, database, logger)
	go inviteLoop(ctx, database, logger)
	go recommendLoop(ctx, database, logger)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv,
			"base_url", cfg.BaseURL, "bucket", cfg.R2Bucket)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

// inviteLoop settles invite confirmation windows. It runs once at startup (so
// already-due invites do not wait an hour) and then hourly. Settlement is
// idempotent: every invite is locked and its reward ledger key is unique.
func inviteLoop(ctx context.Context, database *db.DB, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		started, err := database.StartPendingInviteWindows(runCtx, 500)
		if err != nil {
			logger.Warn("pending invite window backfill failed", "error", err)
		} else if started > 0 {
			logger.Info("pending invite windows started", "count", started)
		}
		finalized, clawedBack, err := database.SettleDueInvites(runCtx, 500)
		if err != nil {
			logger.Warn("invite settlement failed", "error", err)
			return
		}
		if finalized > 0 || clawedBack > 0 {
			logger.Info("invites settled", "finalized", finalized, "clawed_back", clawedBack)
		}
	}

	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// recommendLoop 刷新牵线页「每周推荐位」。生成器本身幂等：ISO 周切换时换批，
// 同周重跑只剔除失效并补空位。启动时先跑一次，补上「本周缺失」的名单；
// 之后整点检查一次，不新增常驻进程。
func recommendLoop(ctx context.Context, database *db.DB, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		result, err := database.GenerateWeeklyRecommendations(runCtx, db.SocialNow())
		if err != nil {
			logger.Warn("weekly recommendations failed", "error", err)
			return
		}
		if result.Changed {
			logger.Info("weekly recommendations generated", "week", result.Week,
				"viewers", result.Viewers, "entries", result.Entries, "rotated", result.Rotated)
		}
	}

	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func cleanupLoop(ctx context.Context, database *db.DB, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	run := func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		sessions, tokens, err := database.CleanupExpired(cleanupCtx)
		if err != nil {
			logger.Warn("expired row cleanup failed", "error", err)
			return
		}
		if sessions > 0 || tokens > 0 {
			logger.Info("expired rows cleaned", "sessions", sessions, "activation_tokens", tokens)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
