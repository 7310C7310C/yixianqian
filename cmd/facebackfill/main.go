// Command facebackfill 批量补齐迁移照片的本地人脸检测结果。
//
// 只处理 face_checked=false 的照片；-all 可强制重扫。检测失败的照片保持
// checked=false，方便下次重试。日志只输出 media_id 与计数，不输出对象键、
// 临时文件路径或凭证。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
	"github.com/7310C7310C/yixianqian/internal/tencentface"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "facebackfill:", err)
		os.Exit(1)
	}
}

func run() error {
	limit := flag.Int("limit", 0, "最多处理多少张照片（按 media_id 去重）；0=全部")
	workers := flag.Int("workers", 2, "并发检测数（1-8）")
	legacyUser := flag.String("user", "", "只处理指定 legacy user id，例如 U-0006")
	includeChecked := flag.Bool("all", false, "连已检测过的照片一起重扫")
	dryRun := flag.Bool("dry-run", false, "只列出待处理数量，不执行检测")
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
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)
	if err := cfg.ValidateServer(); err != nil {
		return err
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
	if err := database.Migrate(ctx, logger); err != nil {
		return err
	}

	// 单实例保护：两批 backfill 同时跑会重复下载 R2、重复占 CPU，并与线上
	// probe 抢资源。拿不到 advisory lock 直接退出，已预约的业务不受影响。
	lock, acquired, err := database.TryFaceBackfillLock(ctx)
	if err != nil {
		return err
	}
	if !acquired {
		return errors.New("另一个 facebackfill 实例正在运行（advisory lock 被占用）；请等待其结束")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lock.Release(releaseCtx)
	}()

	candidates, err := database.ListFaceBackfillCandidates(ctx, *limit, *includeChecked, strings.TrimSpace(*legacyUser))
	if err != nil {
		return err
	}
	logger.Info("face backfill candidates", "count", len(candidates),
		"include_checked", *includeChecked, "user", strings.TrimSpace(*legacyUser))
	if *dryRun || len(candidates) == 0 {
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
	probe, err := face.NewPythonProbe(cfg.FaceProbeCmd, cfg.FaceProbeTimeout)
	if err != nil {
		return err
	}
	// 腾讯云照片合规判定在回填里默认关闭：1000+ 张历史照片会按次计费，
	// 只有显式 TENCENT_FACE_BACKFILL=true 才启用（TENCENT_FACE_ENABLED 只
	// 控制上传路径）。凭证缺失时 fail-open，不阻断回填。
	var compliance face.Compliance
	if !cfg.TencentFaceBackfill {
		logger.Info("tencent face backfill disabled", "hint", "set TENCENT_FACE_BACKFILL=true to enable")
	}
	if cfg.TencentFaceBackfill {
		checker, err := tencentface.NewChecker(tencentface.Options{
			SecretID:  cfg.TencentSecretID,
			SecretKey: cfg.TencentSecretKey,
			Region:    cfg.TencentRegion,
			Timeout:   cfg.TencentFaceTimeout,
			Logger:    logger,
		})
		if err != nil {
			return err
		}
		if !checker.Configured() {
			logger.Warn("tencent face backfill requested without credentials; compliance will be skipped")
		}
		compliance = checker
		logger.Info("tencent face backfill enabled", "timeout", cfg.TencentFaceTimeout)
	}

	tmpDir, err := os.MkdirTemp("", "yxq-facebackfill-")
	if err != nil {
		return errors.New("create temp dir failed")
	}
	defer os.RemoveAll(tmpDir)

	var (
		done     int64
		withFace int64
		failed   int64
	)
	jobs := make(chan db.FaceBackfillCandidate)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				hasFace, err := processPhoto(ctx, store, probe, compliance, database, tmpDir, item, cfg.MediaMaxBytes)
				if err != nil {
					atomic.AddInt64(&failed, 1)
					logger.Warn("face backfill photo failed", "media_id", item.MediaID, "error", err)
				} else if hasFace {
					atomic.AddInt64(&withFace, 1)
				}
				current := atomic.AddInt64(&done, 1)
				if current%25 == 0 || current == int64(len(candidates)) {
					logger.Info("face backfill progress", "done", current,
						"total", len(candidates),
						"has_face", atomic.LoadInt64(&withFace),
						"failed", atomic.LoadInt64(&failed))
				}
			}
		}()
	}
	for _, item := range candidates {
		select {
		case jobs <- item:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()

	logger.Info("face backfill finished", "done", done, "has_face", withFace, "failed", failed)
	if failed > 0 {
		return fmt.Errorf("%d 张照片检测失败；未写入 checked，可重跑补齐", failed)
	}
	return nil
}

func processPhoto(
	ctx context.Context,
	store media.Store,
	probe face.Probe,
	compliance face.Compliance,
	database *db.DB,
	tmpDir string,
	item db.FaceBackfillCandidate,
	maxBytes int64,
) (bool, error) {
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	photoCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	obj, err := store.Get(photoCtx, item.ObjectKey)
	if err != nil {
		// 不包装原始错误，避免对象键进入日志。
		return false, errors.New("object unavailable")
	}
	defer obj.Close()

	tmp, err := os.CreateTemp(tmpDir, "photo-*"+objectExt(item.ContentType))
	if err != nil {
		return false, errors.New("create temp file failed")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, copyErr := io.Copy(tmp, io.LimitReader(obj, maxBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > maxBytes {
		return false, errors.New("read object failed")
	}

	result, err := probe.Probe(photoCtx, tmpName)
	if err != nil {
		return false, err
	}
	if compliance != nil {
		result = face.ApplyCompliance(result, compliance.CheckPhoto(photoCtx, item.MediaID, tmpName))
	}
	row := db.ProfilePhotoFace{MediaID: item.MediaID, Checked: true, HasFace: result.HasFace}
	if len(result.Center) == 2 && len(result.Box) == 2 {
		centerX, centerY := result.Center[0], result.Center[1]
		boxW, boxH := result.Box[0], result.Box[1]
		row.CenterX, row.CenterY = &centerX, &centerY
		row.BoxW, row.BoxH = &boxW, &boxH
	}
	if err := database.SetProfilePhotoFace(ctx, item.MediaID, row); err != nil {
		return false, err
	}
	return result.HasFace, nil
}

func objectExt(contentType string) string {
	if strings.Contains(strings.ToLower(contentType), "png") {
		return ".png"
	}
	return ".jpg"
}
