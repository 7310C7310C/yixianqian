package db

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/migrations"
)

var migrationNameRE = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// migrationLockKey is an arbitrary constant used with pg_advisory_lock so
// multiple processes can race-start safely.
const migrationLockKey int64 = 0x7958514D4947

type migration struct {
	version int64
	name    string
	body    string
}

// Migrate applies pending embedded migrations in version order. Each file runs
// in its own transaction under a session-level advisory lock.
func (d *DB) Migrate(ctx context.Context, log *slog.Logger) error {
	list, err := loadMigrations()
	if err != nil {
		return err
	}

	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("db: acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			log.Warn("migration unlock failed", "error", err)
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("db: read schema_migrations: %w", err)
	}
	applied := make(map[int64]bool)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: iterate schema_migrations: %w", err)
	}

	for _, m := range list {
		if applied[m.version] {
			continue
		}
		start := time.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("db: begin migration %d: %w", m.version, err)
		}
		// The simple query protocol allows a single file with many statements.
		if _, err := tx.Conn().PgConn().Exec(ctx, m.body).ReadAll(); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: apply migration %04d_%s: %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.version, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("db: commit migration %d: %w", m.version, err)
		}
		log.Info("migration applied", "version", m.version, "name", m.name, "duration_ms", time.Since(start).Milliseconds())
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read embedded migrations: %w", err)
	}
	var out []migration
	seen := make(map[int64]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := migrationNameRE.FindStringSubmatch(e.Name())
		if match == nil {
			return nil, fmt.Errorf("db: invalid migration filename %q", e.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("db: parse migration version %q: %w", e.Name(), err)
		}
		if prev, ok := seen[version]; ok {
			return nil, fmt.Errorf("db: duplicate migration version %d (%s and %s)", version, prev, e.Name())
		}
		seen[version] = e.Name()
		body, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("db: read migration %q: %w", e.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("db: migration %q is empty", e.Name())
		}
		out = append(out, migration{version: version, name: match[2], body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// VerifyMigrationFiles validates embedded migration filenames without a DB.
func VerifyMigrationFiles() error {
	_, err := loadMigrations()
	return err
}
