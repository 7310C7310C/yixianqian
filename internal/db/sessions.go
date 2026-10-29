package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreateSession persists the hash of a new session token.
func (d *DB) CreateSession(ctx context.Context, userID int64, tokenHash string, ttl time.Duration, ip, userAgent string) (Session, error) {
	var s Session
	err := d.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent)
		 SELECT $1, $2, now() + make_interval(secs => $3), NULLIF($4, '')::inet, NULLIF($5, '')
		 WHERE EXISTS (SELECT 1 FROM users WHERE id = $1 AND status = 'active')
		 RETURNING id, user_id, expires_at, created_at, last_seen_at`,
		userID, tokenHash, ttl.Seconds(), ip, sanitizeText(userAgent, 512)).
		Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt, &s.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrAccountDisabled
	}
	if err != nil {
		return Session{}, fmt.Errorf("db: create session: %w", err)
	}
	return s, nil
}

// GetSession validates a session hash and returns its user. Expired, revoked
// and disabled-user sessions are treated as not found.
func (d *DB) GetSession(ctx context.Context, tokenHash string) (Session, User, error) {
	var s Session
	var u User
	err := d.pool.QueryRow(ctx,
		`SELECT s.id, s.user_id, s.expires_at, s.created_at, s.last_seen_at,
		        COALESCE(c.must_change_password, false), c.temp_expires_at,
		        `+qualifiedUserColumns("u")+`,
		        u.city, u.native_place, u.church_location, u.account_kind, u.account_status
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 LEFT JOIN auth_credentials c ON c.user_id = u.id
		 WHERE s.token_hash = $1
		   AND s.revoked_at IS NULL
		   AND s.expires_at > now()
		   AND u.status = 'active'`,
		tokenHash).
		Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt, &s.LastSeenAt,
			&s.MustChangePassword, &s.TempExpiresAt,
			&u.ID, &u.LegacyUserID, &u.Phone, &u.Name, &u.Status,
			&u.FailedLoginCount, &u.LastFailedAt, &u.LockedUntil, &u.CreatedAt, &u.UpdatedAt,
			&u.IDCardValid, &u.City, &u.NativePlace, &u.ChurchLocation, &u.AccountKind, &u.AccountStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("db: get session: %w", err)
	}
	return s, u, nil
}

// TouchSession updates last_seen_at at most once per minInterval.
func (d *DB) TouchSession(ctx context.Context, sessionID int64, minInterval time.Duration) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now()
		 WHERE id = $1 AND last_seen_at < now() - make_interval(secs => $2)`,
		sessionID, minInterval.Seconds())
	if err != nil {
		return fmt.Errorf("db: touch session: %w", err)
	}
	return nil
}

// RevokeSessionByID revokes one session idempotently.
func (d *DB) RevokeSessionByID(ctx context.Context, sessionID int64) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, sessionID)
	if err != nil {
		return fmt.Errorf("db: revoke session: %w", err)
	}
	return nil
}
