package db

import (
	"context"
	"fmt"
)

// CleanupExpired removes old session and activation-token rows. Rows are kept
// for a grace period so incident investigation remains possible.
func (d *DB) CleanupExpired(ctx context.Context) (sessions, tokens int64, err error) {
	tag, err := d.pool.Exec(ctx,
		`DELETE FROM sessions
		 WHERE expires_at < now() - interval '7 days'
		    OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '7 days')`)
	if err != nil {
		return 0, 0, fmt.Errorf("db: cleanup sessions: %w", err)
	}
	sessions = tag.RowsAffected()

	tag, err = d.pool.Exec(ctx,
		`DELETE FROM activation_tokens
		 WHERE expires_at < now() - interval '7 days'
		    OR (consumed_at IS NOT NULL AND consumed_at < now() - interval '7 days')`)
	if err != nil {
		return sessions, 0, fmt.Errorf("db: cleanup activation tokens: %w", err)
	}
	tokens = tag.RowsAffected()
	return sessions, tokens, nil
}
