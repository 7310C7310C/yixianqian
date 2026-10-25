package db

import (
	"context"
	"fmt"
	"time"
)

// SetSessionLastSeen is a test-only seam so the touch-interval test can move
// the timestamp backwards without sleeping for minutes.
func (d *DB) SetSessionLastSeen(ctx context.Context, sessionID int64, at time.Time) error {
	if _, err := d.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, sessionID, at); err != nil {
		return fmt.Errorf("db: set session last seen: %w", err)
	}
	return nil
}
