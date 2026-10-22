package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

// TestIntegrationActivationAndSessions exercises every SQL path against a real
// PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL=postgres://... go test ./internal/db -run Integration -v
func TestIntegrationActivationAndSessions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}

	legacyID := "U-TEST-" + randomDigits(t, 10)
	phone := "1" + randomDigits(t, 10)
	user, err := database.UpsertLegacyUser(ctx, legacyID, phone, "测试用户")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if user.LegacyUserID == nil || *user.LegacyUserID != legacyID {
		t.Fatalf("legacy id = %v", user.LegacyUserID)
	}

	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty activation token")
	}
	if _, err := database.CreateActivationToken(ctx, &user.ID, "", "", tokenHash, time.Hour); err != nil {
		t.Fatalf("create activation token: %v", err)
	}
	loaded, err := database.GetActivationToken(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get activation token: %v", err)
	}
	if !loaded.Valid || loaded.ConsumedAt != nil {
		t.Fatalf("token should be valid: %+v", loaded)
	}
	if loaded.Phone != phone || loaded.Name != "测试用户" {
		t.Fatalf("token display fields = %q %q", loaded.Phone, loaded.Name)
	}
	if loaded.UserID == nil || *loaded.UserID != user.ID {
		t.Fatalf("token user id = %v", loaded.UserID)
	}

	password := "p0-test-password-123"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	activated, err := database.ActivateUser(ctx, ActivateParams{
		TokenHash:    tokenHash,
		PasswordHash: passwordHash,
		IP:           "203.0.113.10",
		UserAgent:    "integration-test",
	})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if activated.ID != user.ID {
		t.Fatalf("activated id = %d, want %d", activated.ID, user.ID)
	}

	has, err := database.HasCredential(ctx, user.ID)
	if err != nil || !has {
		t.Fatalf("credential missing: has=%v err=%v", has, err)
	}
	storedHash, err := database.GetPasswordHash(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.VerifyPassword(password, storedHash)
	if err != nil || !ok {
		t.Fatalf("verify stored hash: ok=%v err=%v", ok, err)
	}

	consumed, err := database.GetActivationToken(ctx, tokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if consumed.Valid || consumed.ConsumedAt == nil {
		t.Fatalf("token should be consumed: %+v", consumed)
	}
	if _, err := database.ActivateUser(ctx, ActivateParams{
		TokenHash:    tokenHash,
		PasswordHash: passwordHash,
	}); !errors.Is(err, ErrInvalidActivation) {
		t.Fatalf("second activation err = %v, want ErrInvalidActivation", err)
	}

	rawSession, sessionHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if rawSession == "" {
		t.Fatal("empty session token")
	}
	session, err := database.CreateSession(ctx, user.ID, sessionHash, time.Hour, "203.0.113.10", "integration-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	loadedSession, loadedUser, err := database.GetSession(ctx, sessionHash)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loadedSession.ID != session.ID || loadedUser.ID != user.ID {
		t.Fatalf("session mismatch: %+v %+v", loadedSession, loadedUser)
	}
	if err := database.TouchSession(ctx, session.ID, 0); err != nil {
		t.Fatalf("touch session: %v", err)
	}
	if err := database.RevokeSessionByID(ctx, session.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, _, err := database.GetSession(ctx, sessionHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked session err = %v", err)
	}

	var lockedUntil *time.Time
	for i := 1; i <= 5; i++ {
		count, locked, err := database.RecordFailedLogin(ctx, user.ID, 5, 15*time.Minute, 15*time.Minute)
		if err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
		if count != i {
			t.Fatalf("failure count = %d, want %d", count, i)
		}
		if i == 5 {
			lockedUntil = locked
		}
	}
	if lockedUntil == nil || !lockedUntil.After(time.Now()) {
		t.Fatalf("expected lockout, got %v", lockedUntil)
	}
	lockedUser, err := database.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !lockedUser.Locked(time.Now()) {
		t.Fatal("user should be locked")
	}
	if err := database.ResetLoginFailures(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	unlocked, err := database.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unlocked.Locked(time.Now()) || unlocked.FailedLoginCount != 0 {
		t.Fatalf("user not reset: %+v", unlocked)
	}

	mediaID, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := database.CreateMedia(ctx, Media{
		ID:           mediaID,
		UserID:       user.ID,
		OriginalName: "photo.jpg",
		ObjectKey:    fmt.Sprintf("media/%d/%s.jpg", user.ID, mediaID),
		ThumbKey:     fmt.Sprintf("media/%d/%s_thumb.jpg", user.ID, mediaID),
		ContentType:  "image/jpeg",
		ByteSize:     1000,
		ThumbSize:    100,
		Width:        800,
		Height:       600,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("media created_at missing")
	}
	list, err := database.ListMediaByUser(ctx, user.ID, 10)
	if err != nil || len(list) == 0 {
		t.Fatalf("list media: len=%d err=%v", len(list), err)
	}
	got, err := database.GetMediaForUser(ctx, mediaID, user.ID)
	if err != nil || got.ID != mediaID {
		t.Fatalf("get media: %+v err=%v", got, err)
	}
	other, err := database.UpsertLegacyUser(ctx, "U-TEST-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "其他用户")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetMediaForUser(ctx, mediaID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user media err = %v, want ErrNotFound", err)
	}

	newPhone := "1" + randomDigits(t, 10)
	newRaw, newHash, err := auth.NewToken()
	if err != nil || newRaw == "" {
		t.Fatalf("new token: %v", err)
	}
	if _, err := database.CreateActivationToken(ctx, nil, newPhone, "新用户", newHash, time.Hour); err != nil {
		t.Fatalf("create unattached token: %v", err)
	}
	newPasswordHash, err := auth.HashPassword("another-password-456")
	if err != nil {
		t.Fatal(err)
	}
	newUser, err := database.ActivateUser(ctx, ActivateParams{
		TokenHash:    newHash,
		PasswordHash: newPasswordHash,
		IP:           "203.0.113.11",
		UserAgent:    "integration-test",
	})
	if err != nil {
		t.Fatalf("activate new user: %v", err)
	}
	if newUser.LegacyUserID != nil {
		t.Fatalf("new user should not have legacy id: %v", *newUser.LegacyUserID)
	}
	if newUser.Phone != newPhone || newUser.Name != "新用户" {
		t.Fatalf("new user = %+v", newUser)
	}
	if _, err := database.GetUserByPhone(ctx, newPhone); err != nil {
		t.Fatalf("load new user: %v", err)
	}

	if err := database.InsertAudit(ctx, AuditEntry{
		ActorUserID: &user.ID,
		Action:      "test.integration",
		TargetType:  "user",
		TargetID:    fmt.Sprintf("%d", user.ID),
		IP:          "203.0.113.10",
		UserAgent:   "integration-test",
		Metadata:    map[string]any{"legacy_user_id": legacyID},
	}); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	if err := database.CreateNotification(ctx, user.ID, "test", "测试", "内容"); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	if _, _, err := database.CleanupExpired(ctx); err != nil {
		t.Fatalf("cleanup expired: %v", err)
	}
}

func randomDigits(t *testing.T, n int) string {
	t.Helper()
	b, err := platform.RandomBytes(n)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = '0' + v%10
	}
	return string(out)
}
