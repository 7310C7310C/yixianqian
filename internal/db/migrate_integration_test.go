package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// newMigrationTestDB creates a throwaway database on the same server as
// TEST_DATABASE_URL so migration upgrades can start from a known baseline
// without touching the shared integration database.
func newMigrationTestDB(t *testing.T) (context.Context, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" || parsed.Path == "" {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %q", dsn)
	}
	adminURL := *parsed
	adminURL.Path = "/postgres"
	adminDSN := adminURL.String()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	name := "yxq_mig_" + randomDigits(t, 10)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			t.Skip("test database user cannot create databases")
		}
		t.Fatalf("create migration test database: %v", err)
	}

	testURL := *parsed
	testURL.Path = "/" + name
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			t.Logf("drop migration test database %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx,
			"DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop migration test database %s: %v", name, err)
		}
	})
	return ctx, testURL.String()
}

// applyMigrationsThrough replays the embedded files up to last (inclusive) and
// records them exactly like the runner, producing a database that looks like
// it stopped at that version.
func applyMigrationsThrough(t *testing.T, ctx context.Context, database *DB, last int64) {
	t.Helper()
	list, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := database.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration baseline connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create schema_migrations baseline: %v", err)
	}
	for _, m := range list {
		if m.version > last {
			break
		}
		if _, err := conn.Conn().PgConn().Exec(ctx, m.body).ReadAll(); err != nil {
			t.Fatalf("apply baseline migration %04d_%s: %v", m.version, m.name, err)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
			t.Fatalf("record baseline migration %04d_%s: %v", m.version, m.name, err)
		}
	}
}

func migrationRecordName(t *testing.T, ctx context.Context, database *DB, version int64) string {
	t.Helper()
	var name string
	if err := database.pool.QueryRow(ctx,
		`SELECT name FROM schema_migrations WHERE version = $1`, version).Scan(&name); err != nil {
		t.Fatalf("read applied migration %d: %v", version, err)
	}
	return name
}

func migrationColumnNullable(t *testing.T, ctx context.Context, database *DB, table, column string) (exists, nullable bool) {
	t.Helper()
	var isNullable string
	err := database.pool.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		  WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&isNullable)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false
	}
	if err != nil {
		t.Fatalf("read column %s.%s: %v", table, column, err)
	}
	return true, isNullable == "YES"
}

// TestIntegrationMigrationUpgradeFromV5 covers the release path: a database at
// schema version 5 must apply 0006 and 0007 and end with the weekly list table
// and the ID-card hash column.
func TestIntegrationMigrationUpgradeFromV5(t *testing.T) {
	ctx, dsn := newMigrationTestDB(t)
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	t.Cleanup(database.Close)
	applyMigrationsThrough(t, ctx, database, 5)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate v5 database: %v", err)
	}
	if got := migrationRecordName(t, ctx, database, 6); got != "weekly_recommendations_list" {
		t.Fatalf("migration 6 name = %q", got)
	}
	if got := migrationRecordName(t, ctx, database, 7); got != "id_card_hash" {
		t.Fatalf("migration 7 name = %q", got)
	}

	exists, nullable := migrationColumnNullable(t, ctx, database,
		"weekly_recommendations", "target_user_id")
	if !exists || nullable {
		t.Fatalf("weekly_recommendations.target_user_id exists=%v nullable=%v", exists, nullable)
	}
	exists, _ = migrationColumnNullable(t, ctx, database, "users", "id_card_hash")
	if !exists {
		t.Fatal("users.id_card_hash missing after upgrade")
	}
	var uniqueIndexes int
	if err := database.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes
		  WHERE schemaname = 'public' AND tablename = 'weekly_recommendations'
		    AND indexname = 'weekly_recommendations_week_user_target_uidx'`).Scan(&uniqueIndexes); err != nil {
		t.Fatal(err)
	}
	if uniqueIndexes != 1 {
		t.Fatalf("weekly unique index count = %d", uniqueIndexes)
	}
	var primaryKey string
	if err := database.pool.QueryRow(ctx, `
		SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		  FROM pg_constraint c
		  JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
		  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
		 WHERE c.conrelid = 'weekly_recommendations'::regclass AND c.contype = 'p'`).Scan(&primaryKey); err != nil {
		t.Fatal(err)
	}
	if primaryKey != "week,user_id,position" {
		t.Fatalf("weekly_recommendations primary key = %q", primaryKey)
	}

	// A second run is a no-op and must not rewrite history.
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("rerun migrate: %v", err)
	}
}

// TestIntegrationMigrationDetectsRenamedVersion reproduces the historical
// numbering bug: an environment recorded version 6 as id_card_hash before
// 0006 was reused for the weekly list. The runner used to skip the new file
// silently and leave the schema without target_user_id; it must now fail loudly
// and point at the fix.
func TestIntegrationMigrationDetectsRenamedVersion(t *testing.T) {
	ctx, dsn := newMigrationTestDB(t)
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	t.Cleanup(database.Close)
	applyMigrationsThrough(t, ctx, database, 5)

	// The old numbering applied id_card_hash as both 0006 and 0007.
	if _, err := database.pool.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES (6, 'id_card_hash'), (7, 'id_card_hash')`); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = database.Migrate(ctx, logger)
	if err == nil {
		t.Fatal("migrate accepted version 6 recorded under a different name")
	}
	message := err.Error()
	for _, want := range []string{"id_card_hash", "weekly_recommendations_list", "dropdb"} {
		if !strings.Contains(message, want) {
			t.Fatalf("migration error %q does not mention %q", message, want)
		}
	}
	if exists, _ := migrationColumnNullable(t, ctx, database,
		"weekly_recommendations", "target_user_id"); exists {
		t.Fatal("mismatched migration still applied 0006")
	}
}
