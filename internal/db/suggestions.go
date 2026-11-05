package db

import (
	"context"
	"fmt"
)

// CreateSuggestion stores one member-submitted feedback row and returns its id.
// The admin suggestion queue already reads this table; the member side had no
// writer before this.
func (d *DB) CreateSuggestion(ctx context.Context, authorID int64, authorName, kind, content string) (int64, error) {
	var id int64
	err := d.pool.QueryRow(ctx,
		`INSERT INTO suggestions (author_id, author_name, type, content)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		authorID, authorName, kind, content).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("db: create suggestion: %w", err)
	}
	return id, nil
}
