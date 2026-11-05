package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UserRegionRow is the minimal user row needed by the one-off region repair
// tool. Values are COALESCEd because the columns are NOT NULL but legacy rows
// may still be empty strings.
type UserRegionRow struct {
	ID             int64
	LegacyUserID   string
	City           string
	NativePlace    string
	ChurchLocation string
}

// ListUserRegionRows returns every user in id order. limit <= 0 means all.
func (d *DB) ListUserRegionRows(ctx context.Context, limit int) ([]UserRegionRow, error) {
	query := `SELECT id, COALESCE(legacy_user_id, ''), COALESCE(city, ''),
	                 COALESCE(native_place, ''), COALESCE(church_location, '')
	          FROM users ORDER BY id`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list user region rows: %w", err)
	}
	defer rows.Close()
	var out []UserRegionRow
	for rows.Next() {
		var row UserRegionRow
		if err := rows.Scan(&row.ID, &row.LegacyUserID, &row.City, &row.NativePlace, &row.ChurchLocation); err != nil {
			return nil, fmt.Errorf("db: scan user region row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate user region rows: %w", err)
	}
	return out, nil
}

// UserRegionUpdate is one row of the one-off region repair.
type UserRegionUpdate struct {
	ID             int64
	City           string
	NativePlace    string
	ChurchLocation string
}

// BackupUserRegions snapshots the three region columns into a new table. The
// caller must pass a validated SQL identifier; the table must not already exist.
func (d *DB) BackupUserRegions(ctx context.Context, table string) error {
	quoted := pgx.Identifier{table}.Sanitize()
	_, err := d.pool.Exec(ctx,
		`CREATE TABLE `+quoted+` AS
		 SELECT id, city, native_place, church_location FROM users`)
	if err != nil {
		return fmt.Errorf("db: create region backup %s: %w", table, err)
	}
	return nil
}

// UpdateUserRegions applies all repairs in one transaction.
func (d *DB) UpdateUserRegions(ctx context.Context, updates []UserRegionUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin user region repair: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	for _, u := range updates {
		if _, err := tx.Exec(ctx,
			`UPDATE users
			    SET city = $2, native_place = $3, church_location = $4, updated_at = now()
			  WHERE id = $1`, u.ID, u.City, u.NativePlace, u.ChurchLocation); err != nil {
			return fmt.Errorf("db: update user region row %d: %w", u.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit user region repair: %w", err)
	}
	return nil
}
