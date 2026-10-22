package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetUserByPhone loads an account by normalized phone number.
func (d *DB) GetUserByPhone(ctx context.Context, phone string) (User, error) {
	return scanUser(d.pool.QueryRow(ctx,
		"SELECT "+userColumns+" FROM users WHERE phone = $1", phone))
}

// GetUserByLegacyID loads an account by its legacy system identifier.
func (d *DB) GetUserByLegacyID(ctx context.Context, legacyID string) (User, error) {
	return scanUser(d.pool.QueryRow(ctx,
		"SELECT "+userColumns+" FROM users WHERE legacy_user_id = $1", legacyID))
}

// GetUserByID loads an account by surrogate id.
func (d *DB) GetUserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(d.pool.QueryRow(ctx,
		"SELECT "+userColumns+" FROM users WHERE id = $1", id))
}

// UpsertLegacyUser creates or refreshes the seed account. Conflicting phone
// numbers owned by a different account are reported as ErrPhoneConflict.
func (d *DB) UpsertLegacyUser(ctx context.Context, legacyID, phone, name string) (User, error) {
	u, err := scanUser(d.pool.QueryRow(ctx,
		`INSERT INTO users (legacy_user_id, phone, name)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (legacy_user_id) DO UPDATE
		   SET phone = EXCLUDED.phone, name = EXCLUDED.name, updated_at = now()
		 RETURNING `+userColumns,
		legacyID, phone, name))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return User{}, err
		}
		return User{}, mapWriteError(err)
	}
	return u, nil
}

// HasCredential reports whether the account has a password set.
func (d *DB) HasCredential(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM auth_credentials WHERE user_id = $1)", userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: has credential: %w", err)
	}
	return exists, nil
}

// GetPasswordHash returns the Argon2id PHC string for an account.
func (d *DB) GetPasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	err := d.pool.QueryRow(ctx,
		"SELECT password_hash FROM auth_credentials WHERE user_id = $1", userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: get password hash: %w", err)
	}
	return hash, nil
}

// RecordFailedLogin increments the failure counter and applies a lockout when
// the threshold is reached. Failures older than windowSeconds are discarded.
func (d *DB) RecordFailedLogin(ctx context.Context, userID int64, threshold int, lockFor, window time.Duration) (int, *time.Time, error) {
	var count int
	var lockedUntil *time.Time
	err := d.pool.QueryRow(ctx,
		`WITH cur AS (
		   SELECT id,
		          CASE WHEN last_failed_at IS NULL
		                    OR last_failed_at < now() - make_interval(secs => $4)
		               THEN 0 ELSE failed_login_count END AS base
		   FROM users WHERE id = $1
		 )
		 UPDATE users u
		 SET failed_login_count = cur.base + 1,
		     last_failed_at = now(),
		     locked_until = CASE WHEN cur.base + 1 >= $2
		                         THEN now() + make_interval(secs => $3)
		                         ELSE u.locked_until END,
		     updated_at = now()
		 FROM cur
		 WHERE u.id = cur.id
		 RETURNING u.failed_login_count, u.locked_until`,
		userID, threshold, lockFor.Seconds(), window.Seconds()).Scan(&count, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	if err != nil {
		return 0, nil, fmt.Errorf("db: record failed login: %w", err)
	}
	return count, lockedUntil, nil
}

// ResetLoginFailures clears the lockout state after a successful login.
func (d *DB) ResetLoginFailures(ctx context.Context, userID int64) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE users
		 SET failed_login_count = 0, last_failed_at = NULL, locked_until = NULL, updated_at = now()
		 WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("db: reset login failures: %w", err)
	}
	return nil
}
