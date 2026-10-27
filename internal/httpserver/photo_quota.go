package httpserver

import (
	"errors"
	"fmt"
	"time"
)

// errPhotoDailyLimit marks a rejected upload because the per-account daily
// photo quota (v1 PHOTO_DAILY_LIMIT semantics, decision 44) is exhausted.
var errPhotoDailyLimit = errors.New("httpserver: photo daily limit reached")

// photoQuotaDay returns the start of the quota "natural day" containing now in
// the configured timezone. The database stores date only, so the wall-clock
// date in loc is what matters.
func (a *App) photoQuotaDay(now time.Time) time.Time {
	loc := a.cfg.PhotoQuotaLocation
	if loc == nil {
		loc = time.UTC
	}
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// photoQuotaMessage mirrors the v1 wording, with the configured limit.
func (a *App) photoQuotaMessage() string {
	return fmt.Sprintf("今日上传照片次数已达上限（%d 次），请明天再试", a.cfg.PhotoDailyLimit)
}
