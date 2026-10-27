package httpserver

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
)

func TestPhotoQuotaDayUsesConfiguredTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: config.Config{PhotoQuotaLocation: loc}}

	// 2026-10-28T16:30Z == 2026-10-29T00:30+08：日界必须落在 10-29，而不是 UTC 的 10-28。
	now := time.Date(2026, 10, 28, 16, 30, 0, 0, time.UTC)
	day := app.photoQuotaDay(now)
	want := time.Date(2026, 10, 29, 0, 0, 0, 0, loc)
	if !day.Equal(want) {
		t.Fatalf("quota day = %v, want %v", day, want)
	}
	// 同日 23:59:59+08 仍属当天。
	late := time.Date(2026, 10, 28, 23, 59, 59, 0, loc)
	if got := app.photoQuotaDay(late); !got.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, loc)) {
		t.Fatalf("late quota day = %v", got)
	}
}

func TestPhotoQuotaMessageMirrorsV1(t *testing.T) {
	app := &App{cfg: config.Config{PhotoDailyLimit: 7}}
	if got, want := app.photoQuotaMessage(),
		"今日上传照片次数已达上限（7 次），请明天再试"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestAppRejectsInvalidPhotoQuotaTimezone(t *testing.T) {
	cfg := testConfig()
	cfg.PhotoQuotaTZ = "Not/AZone"
	_, err := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "photo quota timezone") {
		t.Fatalf("New err = %v, want photo quota timezone error", err)
	}
}
