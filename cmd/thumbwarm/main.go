// Command thumbwarm 预热缩略图磁盘缓存：把 media 表里所有 thumb 从 R2 拉到
// MEDIA_CACHE_DIR。可一次性跑（默认并发 8），也可放 cron/部署后后台执行。
//
// 只读 R2 + 写本地缓存目录；不改数据库。缓存 key 与线上读取路径一致
// （media.ThumbCacheKey），因此预热过的条目会被直接命中。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	limit := flag.Int("limit", 0, "最多预热多少张；0=全部")
	dryRun := flag.Bool("dry-run", false, "只统计待预热数量")
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
