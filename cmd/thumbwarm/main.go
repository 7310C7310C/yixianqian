// Command thumbwarm 预热缩略图磁盘缓存：把 media 表里所有 thumb 从 R2 拉到
// MEDIA_CACHE_DIR。可一次性跑（默认并发 8），也可放 cron/部署后后台执行。
//
// 只读 R2 + 写本地缓存目录；不改数据库。缓存 key 与线上读取路径一致
// （media.ThumbCacheKey），因此预热过的条目会被直接命中。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "thumbwarm:", err)
		os.Exit(1)
	}
}

func run() error {
	workers := flag.Int("workers", 8, "并发拉取数（1-8）")
	limit := flag.Int("limit", 0, "最多处理多少张；0=全部")
	dryRun := flag.Bool("dry-run", false, "只统计待处理数量")
	variants := flag.Bool("variants", false, "生成 768/1024/1280 卡面变体（默认只预热 512 缩略图）")
	flag.Parse()
	if *workers < 1 {
		*workers = 1
	}
	if *workers > 8 {
		*workers = 8
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.MediaCacheDir == "" {
		return fmt.Errorf("MEDIA_CACHE_DIR 未配置，无法预热")
	}
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)
	if err := cfg.ValidateServer(); err != nil {
		return err
	}
	cache, err := media.NewThumbCache(cfg.MediaCacheDir, cfg.MediaCacheBytes)
	if err != nil {
		return err
	}
	if cache == nil {
		return fmt.Errorf("缩略图缓存未启用")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := db.OpenMaintenance(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		return err
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
	if *variants {
		return backfillVariants(ctx, database, store, logger, *workers, *limit, *dryRun)
	}

	thumbs, err := database.ListThumbs(ctx, *limit)
	if err != nil {
		return err
	}
	entries, bytes := cache.Stats()
	logger.Info("thumb warm start", "candidates", len(thumbs), "workers", *workers,
		"cache_entries", entries, "cache_bytes", bytes, "limit_bytes", cfg.MediaCacheBytes)
	if *dryRun || len(thumbs) == 0 {
		return nil
	}

	var (
		warmed  atomic.Int64
		hit     atomic.Int64
		failed  atomic.Int64
		skipped atomic.Int64
	)
	jobs := make(chan db.ThumbRef)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				key := media.ThumbCacheKey(ref.MediaID, ref.ThumbKey, ref.ThumbSize)
				if _, ok := cache.Get(key); ok {
					hit.Add(1)
					continue
				}
				data, err := fetch(ctx, store, ref.ThumbKey, ref.ThumbSize)
				if err != nil {
					failed.Add(1)
					logger.Warn("thumb warm fetch failed", "media_id", ref.MediaID, "error", err)
					continue
				}
				if err := cache.Put(key, data); err != nil {
					failed.Add(1)
					logger.Warn("thumb warm cache write failed", "media_id", ref.MediaID, "error", err)
					continue
				}
				if _, ok := cache.Get(key); !ok {
					// 超过缓存上限被淘汰（单条过大或容量不足），如实记录。
					skipped.Add(1)
					continue
				}
				warmed.Add(1)
			}
		}()
	}
send:
	for _, ref := range thumbs {
		select {
		case <-ctx.Done():
			logger.Info("thumb warm interrupted", "remaining", len(thumbs))
			break send
		case jobs <- ref:
		}
	}
	close(jobs)
	wg.Wait()

	entries, bytes = cache.Stats()
	logger.Info("thumb warm done", "candidates", len(thumbs), "warmed", warmed.Load(),
		"already_cached", hit.Load(), "evicted", skipped.Load(), "failed", failed.Load(),
		"cache_entries", entries, "cache_bytes", bytes)
	return nil
}

func fetch(ctx context.Context, store media.Store, key string, size int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	obj, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(io.LimitReader(obj, size+1))
}

// backfillVariants generates the 768/1024/1280 card variants for existing
// media. It decodes each original once and writes only widths within the
// source's 15% upscale allowance. Safe to re-run; existing keys are overwritten.
func backfillVariants(ctx context.Context, database *db.DB, store media.Store, logger *slog.Logger, workers, limit int, dryRun bool) error {
	sources, err := database.ListVariantSources(ctx, limit)
	if err != nil {
		return err
	}
	logger.Info("variant backfill start", "candidates", len(sources), "workers", workers)
	if dryRun || len(sources) == 0 {
		return nil
	}
	var done, failed, skipped atomic.Int64
	jobs := make(chan db.VariantSource)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				widths := make([]int, 0, 3)
				for _, width := range []int{768, 1024} {
					if src.Width > 0 && width*100 > src.Width*115 {
						continue
					}
					widths = append(widths, width)
				}
				if len(widths) == 0 {
					skipped.Add(1)
					continue
				}
				original, err := fetch(ctx, store, src.ObjectKey, 32<<20)
				if err != nil {
					failed.Add(1)
					logger.Warn("variant backfill fetch failed", "media_id", src.MediaID, "error", err)
					continue
				}
				variants, err := media.ResizeWidthsJPEG(original, widths, 82)
				if errors.Is(err, media.ErrVariantWidth) {
					skipped.Add(1)
					continue
				}
				if err != nil {
					failed.Add(1)
					logger.Warn("variant backfill encode failed", "media_id", src.MediaID, "error", err)
					continue
				}
				for width, data := range variants {
					if err := store.Put(ctx, media.ThumbVariantKey(src.ThumbKey, width), data, "image/jpeg"); err != nil {
						failed.Add(1)
						logger.Warn("variant backfill upload failed", "media_id", src.MediaID, "width", width, "error", err)
						continue
					}
					done.Add(1)
				}
			}
		}()
	}
send:
	for _, src := range sources {
		select {
		case <-ctx.Done():
			break send
		case jobs <- src:
		}
	}
	close(jobs)
	wg.Wait()
	logger.Info("variant backfill done", "candidates", len(sources), "variants", done.Load(),
		"skipped_sources", skipped.Load(), "failed", failed.Load())
	return nil
}
