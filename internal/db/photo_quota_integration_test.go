package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openPhotoQuotaDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database, ctx
}

func photoQuotaUser(t *testing.T, ctx context.Context, database *DB) User {
	t.Helper()
	legacy := "U-QUOTA-" + randomDigits(t, 10)
	phone := "1" + randomDigits(t, 10)
	user, err := database.UpsertLegacyUser(ctx, legacy, phone, "配额测试")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user
}

func photoQuotaDay(base time.Time, offsetDays int) time.Time {
	return time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
		AddDate(0, 0, offsetDays)
}

func TestIntegrationPhotoQuotaDailyLimit(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	user := photoQuotaUser(t, ctx, database)
	day := photoQuotaDay(time.Now().UTC(), 0)

	for i := 1; i <= 10; i++ {
		allowed, used, err := database.TakePhotoQuota(ctx, user.ID, day, 1, 10)
		if err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
		if !allowed || used != i {
			t.Fatalf("take %d: allowed=%v used=%d, want true/%d", i, allowed, used, i)
		}
	}
	allowed, used, err := database.TakePhotoQuota(ctx, user.ID, day, 1, 10)
	if err != nil {
		t.Fatalf("take 11: %v", err)
	}
	if allowed || used != 10 {
		t.Fatalf("take 11: allowed=%v used=%d, want false/10", allowed, used)
	}
	var stored int
	if err := database.pool.QueryRow(ctx,
		`SELECT used FROM photo_upload_daily WHERE user_id = $1 AND day = $2`,
		user.ID, day).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 10 {
		t.Fatalf("stored used = %d, want 10 (denied take must not increment)", stored)
	}

	// 跨自然日重置。
	next := photoQuotaDay(day, 1)
	allowed, used, err = database.TakePhotoQuota(ctx, user.ID, next, 1, 10)
	if err != nil {
		t.Fatalf("take next day: %v", err)
	}
	if !allowed || used != 1 {
		t.Fatalf("next day: allowed=%v used=%d, want true/1", allowed, used)
	}
}

func TestIntegrationPhotoQuotaConcurrent(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	user := photoQuotaUser(t, ctx, database)
	day := photoQuotaDay(time.Now().UTC(), 0)

	var allowedCount atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := database.TakePhotoQuota(ctx, user.ID, day, 1, 10)
			if err != nil {
				failures.Add(1)
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent quota errors = %d", failures.Load())
	}
	if got := allowedCount.Load(); got != 10 {
		t.Fatalf("allowed = %d, want exactly 10", got)
	}
	var stored int
	if err := database.pool.QueryRow(ctx,
		`SELECT used FROM photo_upload_daily WHERE user_id = $1 AND day = $2`,
		user.ID, day).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 10 {
		t.Fatalf("stored used = %d, want 10", stored)
	}
}

func TestIntegrationPhotoQuotaDisabledAndCascade(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	user := photoQuotaUser(t, ctx, database)
	day := photoQuotaDay(time.Now().UTC(), 0)

	// limit=0 关闭限制，且不写行。
	for i := 0; i < 20; i++ {
		allowed, used, err := database.TakePhotoQuota(ctx, user.ID, day, 1, 0)
		if err != nil || !allowed || used != 0 {
			t.Fatalf("disabled take %d: allowed=%v used=%d err=%v", i, allowed, used, err)
		}
	}
	var rows int
	if err := database.pool.QueryRow(ctx,
		`SELECT count(*) FROM photo_upload_daily WHERE user_id = $1`, user.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("disabled quota wrote %d rows", rows)
	}

	if allowed, used, err := database.TakePhotoQuota(ctx, user.ID, day, 10, 10); err != nil || !allowed || used != 10 {
		t.Fatalf("limit disabled take: allowed=%v used=%d err=%v", allowed, used, err)
	}

	// ON DELETE CASCADE：删除账号后配额行清零。
	if _, err := database.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.pool.QueryRow(ctx,
		`SELECT count(*) FROM photo_upload_daily WHERE user_id = $1`, user.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("cascade left %d quota rows", rows)
	}
}

func photoQuotaRegisterParams(t *testing.T, phone string) RegisterParams {
	t.Helper()
	return RegisterParams{
		Phone:          phone,
		Name:           "配额注册",
		Nickname:       "配额注册",
		Gender:         "女性",
		Birthday:       time.Date(1992, 3, 4, 0, 0, 0, 0, time.UTC),
		IDCardCipher:   []byte("cipher"),
		IDCardLast4:    "1234",
		IDCardHash:     "quota-hash-" + randomDigits(t, 12),
		ContactConsent: true,
		PasswordHash:   "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	}
}

func TestIntegrationCreateRegisteredUserWithQuota(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	day := photoQuotaDay(time.Now().UTC(), 0)

	// 额度不足（limit=5 < 9 张）时整笔回滚：不产生 user，也不占配额。
	deniedPhone := "1" + randomDigits(t, 10)
	_, err := database.CreateRegisteredUserWithQuota(ctx, photoQuotaRegisterParams(t, deniedPhone), day, 9, 5)
	if !errors.Is(err, ErrPhotoDailyLimit) {
		t.Fatalf("denied err = %v, want ErrPhotoDailyLimit", err)
	}
	if _, err := database.GetUserByPhone(ctx, deniedPhone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied registration created a user: err=%v", err)
	}

	// 额度足够：user + credential + 配额一次写入。
	okPhone := "1" + randomDigits(t, 10)
	user, err := database.CreateRegisteredUserWithQuota(ctx, photoQuotaRegisterParams(t, okPhone), day, 3, 10)
	if err != nil {
		t.Fatalf("register with quota: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	if user.ID == 0 {
		t.Fatal("registered user id is zero")
	}
	has, err := database.HasCredential(ctx, user.ID)
	if err != nil || !has {
		t.Fatalf("credential missing: has=%v err=%v", has, err)
	}
	var used int
	if err := database.pool.QueryRow(ctx,
		`SELECT used FROM photo_upload_daily WHERE user_id = $1 AND day = $2`,
		user.ID, day).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 3 {
		t.Fatalf("used = %d, want 3", used)
	}

	// 新账号再取 8 张会超过 10，必须拒绝且不增量。
	allowed, remaining, err := database.TakePhotoQuota(ctx, user.ID, day, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || remaining != 3 {
		t.Fatalf("over-limit take: allowed=%v used=%d", allowed, remaining)
	}
}

func TestIntegrationPrunePhotoQuota(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	user := photoQuotaUser(t, ctx, database)
	base := photoQuotaDay(time.Now().UTC(), -2)

	for offset := 0; offset < 3; offset++ {
		day := photoQuotaDay(base, offset)
		if _, _, err := database.TakePhotoQuota(ctx, user.ID, day, 1, 10); err != nil {
			t.Fatalf("seed day %d: %v", offset, err)
		}
	}
	rows, err := database.PrunePhotoQuota(ctx, photoQuotaDay(base, 1))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if rows != 1 {
		t.Fatalf("pruned rows = %d, want 1 (only day-2)", rows)
	}
	var remaining int
	if err := database.pool.QueryRow(ctx,
		`SELECT count(*) FROM photo_upload_daily WHERE user_id = $1`, user.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("remaining rows = %d, want 2 (yesterday+today)", remaining)
	}
}
