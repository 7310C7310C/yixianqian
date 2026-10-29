package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func drainResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func photoQuotaUsed(t *testing.T, env *profileHTTPEnv, userID int64) (used int, day time.Time) {
	t.Helper()
	if err := env.pool.QueryRow(env.ctx,
		`SELECT used, day FROM photo_upload_daily WHERE user_id = $1`, userID).
		Scan(&used, &day); err != nil {
		t.Fatalf("load quota row: %v", err)
	}
	return used, day
}

func photoQuotaMediaCount(t *testing.T, env *profileHTTPEnv, userID int64) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM media WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count media: %v", err)
	}
	return count
}

func photoQuotaTotalMedia(t *testing.T, env *profileHTTPEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx, `SELECT count(*) FROM media`).Scan(&count); err != nil {
		t.Fatalf("count media: %v", err)
	}
	return count
}

// 单张 legacy 上传：limit=2 时第 3 次 429 photo_daily_limit，已写入的 media
// 必须回滚，used 不增量。
func TestIntegrationLegacyPhotoDailyQuota(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) { c.PhotoDailyLimit = 2 })
	user, client := env.createUser(t, "U-QUOTA-LEGACY", "配额用户", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)
	csrf := client.csrfCookie()

	for i := 1; i <= 2; i++ {
		resp := legacyPhotoUpload(t, client, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload %d status = %d body=%s", i, resp.StatusCode, readBody(t, resp))
		}
		drainResponse(t, resp)
	}
	mediaBefore := photoQuotaMediaCount(t, env, user.ID)

	resp := legacyPhotoUpload(t, client, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	legacyJSONDecode(t, resp, &payload)
	if payload.Code != "photo_daily_limit" {
		t.Fatalf("code = %q, want photo_daily_limit", payload.Code)
	}
	if !strings.Contains(payload.Error, "今日上传照片次数已达上限（2 次）") {
		t.Fatalf("message = %q", payload.Error)
	}
	if got := photoQuotaMediaCount(t, env, user.ID); got != mediaBefore {
		t.Fatalf("media rows = %d, want %d (rejected upload must be rolled back)", got, mediaBefore)
	}
	used, _ := photoQuotaUsed(t, env, user.ID)
	if used != 2 {
		t.Fatalf("used = %d, want 2", used)
	}
}

// 注册 9 张（limit=10）后：删 1 张腾出资料位（删除不返还配额）→ 补 1 张成功
// （used=10）→ 再删 1 张 → 再传 1 张 429。
func TestIntegrationRegisterPhotoDailyQuota(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) { c.PhotoDailyLimit = 10 })
	anon := newWSClient(t, env.server.URL)
	csrf := anon.pageCSRF("/register")
	phone := randomHTTPPhone(t)
	idCard := makeTestIDCard(t, "19900101", "002")
	fields := validRegisterValues(t, phone, idCard, "1990-01-01")
	fields.Set("csrf_token", csrf)

	photos := make([][]byte, registerMaxPhotos)
	for i := range photos {
		photos[i] = testJPEG(t)
	}
	resp := postRegisterMultipart(t, anon, fields, photos)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = env.pool.Exec(cleanupCtx, `DELETE FROM users WHERE phone = $1`, phone)
	})
	user, err := env.database.GetUserByPhone(env.ctx, phone)
	if err != nil {
		t.Fatalf("load registered user: %v", err)
	}
	// 待审核账号被 registrationGate 挡在照片接口之外；本用例只验证配额，直接
	// 放行为 active（与 createUser 的测试口径一致）。
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET account_status = 'active' WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("activate registered user: %v", err)
	}
	used, _ := photoQuotaUsed(t, env, user.ID)
	if used != registerMaxPhotos {
		t.Fatalf("used after register = %d, want %d", used, registerMaxPhotos)
	}

	csrf = anon.csrfCookie()
	resp = profileJSONRequest(t, anon, http.MethodDelete, "/legacy/api/profile/photo", csrf,
		map[string]int{"index": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete photo status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)

	resp = legacyPhotoUpload(t, anon, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("10th photo status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)
	if used, _ = photoQuotaUsed(t, env, user.ID); used != 10 {
		t.Fatalf("used after 10th = %d, want 10", used)
	}

	resp = profileJSONRequest(t, anon, http.MethodDelete, "/legacy/api/profile/photo", csrf,
		map[string]int{"index": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second delete status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)
	mediaBefore := photoQuotaMediaCount(t, env, user.ID)

	resp = legacyPhotoUpload(t, anon, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("11th photo status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	legacyJSONDecode(t, resp, &payload)
	if payload.Code != "photo_daily_limit" || !strings.Contains(payload.Error, "（10 次）") {
		t.Fatalf("quota payload = %+v", payload)
	}
	if got := photoQuotaMediaCount(t, env, user.ID); got != mediaBefore {
		t.Fatalf("media rows = %d, want %d", got, mediaBefore)
	}
	if used, _ = photoQuotaUsed(t, env, user.ID); used != 10 {
		t.Fatalf("used after denial = %d, want 10", used)
	}
}

// 注册时额度不足（limit=5 < 9 张）：429 + 中文错误页 + 不建立 user/media，
// 已填表单值保留，raw HTML 仍 0 汉字。
func TestIntegrationRegisterQuotaRejectsWithoutUser(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) { c.PhotoDailyLimit = 5 })
	anon := newWSClient(t, env.server.URL)
	csrf := anon.pageCSRF("/register")
	phone := randomHTTPPhone(t)
	idCard := makeTestIDCard(t, "19900101", "002")
	fields := validRegisterValues(t, phone, idCard, "1990-01-01")
	fields.Set("csrf_token", csrf)

	photos := make([][]byte, registerMaxPhotos)
	for i := range photos {
		photos[i] = testJPEG(t)
	}
	mediaBefore := photoQuotaTotalMedia(t, env)
	resp := postRegisterMultipart(t, anon, fields, photos)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("register status = %d body=%s", resp.StatusCode, body)
	}
	assertHanFreePublicBody(t, body)
	catalog := registerCatalogFromBody(t, body)
	if !strings.Contains(catalog.Text["page.error"], "今日上传照片次数已达上限（5 次）") {
		t.Fatalf("error copy = %q", catalog.Text["page.error"])
	}
	if catalog.Values["phone"] != phone {
		t.Fatalf("phone value not preserved: %q", catalog.Values["phone"])
	}
	if _, err := env.database.GetUserByPhone(env.ctx, phone); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("user created despite quota denial: err=%v", err)
	}
	if got := photoQuotaTotalMedia(t, env); got != mediaBefore {
		t.Fatalf("media rows grew from %d to %d", mediaBefore, got)
	}
	var rows int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM photo_upload_daily q
		  JOIN users u ON u.id = q.user_id WHERE u.phone = $1`, phone).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("quota rows for denied registration = %d", rows)
	}
}

// 日界用 PHOTO_QUOTA_TZ（Asia/Shanghai），不用服务器本地时区：23:59:59 拒绝，
// 次日 00:00:05 放行。
func TestIntegrationPhotoQuotaCrossesShanghaiMidnight(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, func(c *config.Config) { c.PhotoDailyLimit = 2 })
	user, client := env.createUser(t, "U-QUOTA-TZ", "跨日用户", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)
	loc := env.app.cfg.PhotoQuotaLocation
	if loc == nil {
		t.Fatal("test env has no photo quota location")
	}
	csrf := client.csrfCookie()
	env.app.clock = fixedClock{at: time.Date(2026, 10, 28, 23, 59, 59, 0, loc)}
	for i := 1; i <= 2; i++ {
		resp := legacyPhotoUpload(t, client, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("day-1 upload %d status = %d body=%s", i, resp.StatusCode, readBody(t, resp))
		}
		drainResponse(t, resp)
	}
	resp := legacyPhotoUpload(t, client, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("same-day third upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)

	env.app.clock = fixedClock{at: time.Date(2026, 10, 29, 0, 0, 5, 0, loc)}
	resp = legacyPhotoUpload(t, client, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("next-day upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	drainResponse(t, resp)

	var rows int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM photo_upload_daily WHERE user_id = $1`, user.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("quota rows = %d, want 2 (one per natural day)", rows)
	}
}
