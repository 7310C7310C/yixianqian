package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ObserverEnabled reports whether a user also has the v1 observer identity.
// A user may have both the normal member and observer records under one open_id;
// the import keeps one users row and marks the observer capability here.
func (d *DB) ObserverEnabled(ctx context.Context, userID int64) (bool, error) {
	var enabled bool
	err := d.pool.QueryRow(ctx, `SELECT observer_enabled FROM users WHERE id = $1`, userID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("db: get observer_enabled: %w", err)
	}
	return enabled, nil
}
