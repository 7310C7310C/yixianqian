// Package db owns the PostgreSQL schema and all SQL. Handlers never build SQL.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool.
type DB struct {
	pool *pgxpool.Pool
	dsn  string
}

// OpenOption customises pool construction without adding production knobs to
// every caller. Tests use it to attach a query tracer.
type OpenOption func(*pgxpool.Config)

// WithQueryTracer records pool queries through tracer. It is intended for
// integration tests that assert query counts; production callers pass none.
func WithQueryTracer(tracer pgx.QueryTracer) OpenOption {
	return func(cfg *pgxpool.Config) {
		cfg.ConnConfig.Tracer = tracer
	}
}

// Open parses the DSN and creates a pool. It does not connect eagerly.
func Open(ctx context.Context, dsn string, opts ...OpenOption) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 0
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "yixianqian"
	for _, opt := range opts {
		opt(cfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	return &DB{pool: pool, dsn: dsn}, nil
}

// DSN returns the connection string this database was opened with. Components
// that need a dedicated connection (LISTEN/NOTIFY, long maintenance work) use
// it instead of holding a pooled connection forever.
func (d *DB) DSN() string { return d.dsn }

// Ping verifies the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Close releases pooled connections.
func (d *DB) Close() { d.pool.Close() }
