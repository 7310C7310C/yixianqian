package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
)

// TestIntegrationTouchSessionInterval verifies the SQL-side throttle: the
// middleware interval is 15 minutes, and TouchSession only writes when the
// stored last_seen_at is older than that.
func TestIntegrationTouchSessionInterval(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user, err := database.UpsertLegacyUser(ctx, "U-TOUCH-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "touch")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateSession(ctx, user.ID, hash, time.Hour, "", "touch-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	readLastSeen := func() time.Time {
		var at time.Time
		if err := database.pool.QueryRow(ctx, `SELECT last_seen_at FROM sessions WHERE id = $1`, session.ID).Scan(&at); err != nil {
			t.Fatalf("read last_seen_at: %v", err)
		}
		return at
	}

	// Fresh session: a 15-minute throttle must not write.
	first := readLastSeen()
	if err := database.TouchSession(ctx, session.ID, 15*time.Minute); err != nil {
		t.Fatalf("touch fresh: %v", err)
	}
	if got := readLastSeen(); !got.Equal(first) {
		t.Fatalf("fresh session last_seen_at moved: %v -> %v", first, got)
	}

	// 20 minutes old: 15-minute throttle writes exactly once; the immediate
	// second call must not write again.
	old := time.Now().Add(-20 * time.Minute)
	if err := database.SetSessionLastSeen(ctx, session.ID, old); err != nil {
		t.Fatalf("backdate session: %v", err)
	}
	if err := database.TouchSession(ctx, session.ID, 15*time.Minute); err != nil {
		t.Fatalf("touch old: %v", err)
	}
	touched := readLastSeen()
	if !touched.After(old.Add(19 * time.Minute)) {
		t.Fatalf("last_seen_at not updated: old=%v got=%v", old, touched)
	}
	if err := database.TouchSession(ctx, session.ID, 15*time.Minute); err != nil {
		t.Fatalf("touch again: %v", err)
	}
	if got := readLastSeen(); !got.Equal(touched) {
		t.Fatalf("second touch rewrote last_seen_at: %v -> %v", touched, got)
	}

	// Session validity/expiry are untouched by a touch.
	loaded, loadedUser, err := database.GetSession(ctx, hash)
	if err != nil {
		t.Fatalf("get session after touch: %v", err)
	}
	if loaded.ExpiresAt.Sub(session.ExpiresAt).Abs() > time.Second {
		t.Fatalf("expiry changed: %v -> %v", session.ExpiresAt, loaded.ExpiresAt)
	}
	if loadedUser.ID != user.ID {
		t.Fatalf("session user = %d, want %d", loadedUser.ID, user.ID)
	}
}

func TestIntegrationExtendSession(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user, err := database.UpsertLegacyUser(ctx, "U-EXTEND-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "extend")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateSession(ctx, user.ID, hash, time.Hour, "", "extend-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// A session near expiry is slid forward to now()+ttl.
	if err := database.SetSessionExpiresAt(ctx, session.ID, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("backdate expiry: %v", err)
	}
	got, err := database.ExtendSession(ctx, session.ID, 2*time.Hour)
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if delta := time.Until(got); delta < 110*time.Minute || delta > 130*time.Minute {
		t.Fatalf("extended expiry = %v (delta %v), want ~2h", got, delta)
	}

	// A session that is already past its expiry is not resurrected.
	if err := database.SetSessionExpiresAt(ctx, session.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := database.ExtendSession(ctx, session.ID, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend expired session err = %v, want ErrNotFound", err)
	}

	// A revoked session cannot be extended.
	if _, err := database.pool.Exec(ctx, `UPDATE sessions SET expires_at = now() + interval '10 minutes', revoked_at = now() WHERE id = $1`, session.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, err := database.ExtendSession(ctx, session.ID, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend revoked session err = %v, want ErrNotFound", err)
	}

	// A disabled user's session is not extended.
	if _, err := database.pool.Exec(ctx, `UPDATE sessions SET revoked_at = NULL, expires_at = now() + interval '10 minutes' WHERE id = $1`, session.ID); err != nil {
		t.Fatalf("reset session: %v", err)
	}
	if _, err := database.pool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := database.ExtendSession(ctx, session.ID, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend disabled-user session err = %v, want ErrNotFound", err)
	}
}
