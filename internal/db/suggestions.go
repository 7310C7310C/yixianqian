package db

import (
	"context"
	"fmt"
)

// CreateSuggestion stores one member-submitted feedback row. The admin
// suggestion queue already reads this table; the member side had no writer.
func (d *DB) CreateSuggestion(ctx context.Context, authorID int64, authorName, kind, content string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO suggestions (author_id, author_name, type, content) VALUES ($1, $2, $3, $4)`,
		authorID, authorName, kind, content)
	if err != nil {
		return fmt.Errorf("db: create suggestion: %w", err)
	}
	return nil
}
