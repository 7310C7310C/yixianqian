package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIntegrationPoolStatementTimeout pins the batch-7 pool defaults: request
// queries are capped at 5s while maintenance connections stay uncapped.
func TestIntegrationPoolStatementTimeout(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	var timeout string
	if err := database.pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("show statement_timeout: %v", err)
	}
	if timeout != "5s" {
		t.Fatalf("pool statement_timeout = %q, want 5s", timeout)
	}

	maintenance, err := OpenMaintenance(ctx, dsn)
	if err != nil {
		t.Fatalf("open maintenance: %v", err)
	}
	defer maintenance.Close()
	if err := maintenance.pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("show maintenance statement_timeout: %v", err)
	}
	if timeout != "0" {
		t.Fatalf("maintenance statement_timeout = %q, want 0", timeout)
	}
}
