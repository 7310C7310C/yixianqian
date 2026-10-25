package db

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is an account row. Legacy users may come from the old system and have
// no credential until activation.
type User struct {
	ID               int64
	LegacyUserID     *string
	Phone            string
	Name             string
	Status           string
	FailedLoginCount int
	LastFailedAt     *time.Time
	LockedUntil      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// IDCardValid lets the request middleware enforce the P0 ID gate without a
	// second users lookup; GetSession is the only place that fills it.
	IDCardValid bool
}

// Locked reports whether the account is currently in a failed-login lockout.
func (u User) Locked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

// Session is an active server-side session (the raw token is never stored).
// MustChangePassword mirrors the credential flag so middleware can force a
// password change without an extra query on every request.
type Session struct {
	ID                 int64
	UserID             int64
	ExpiresAt          time.Time
	CreatedAt          time.Time
	LastSeenAt         time.Time
	MustChangePassword bool
	TempExpiresAt      *time.Time
}

// ActivationToken is a one-time password setup token.
type ActivationToken struct {
	ID         int64
	UserID     *int64
	Phone      string
	Name       string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	Valid      bool
}

// Media is one uploaded image and its derived thumbnail.
type Media struct {
	ID           string
	UserID       int64
	OriginalName string
	ObjectKey    string
	ThumbKey     string
	ContentType  string
	ByteSize     int64
	ThumbSize    int64
	Width        int
	Height       int
	CreatedAt    time.Time
}

type rowScanner interface {
	Scan(dest ...any) error
}

const userColumns = `id, legacy_user_id, phone, name, status, failed_login_count,
	last_failed_at, locked_until, created_at, updated_at`

func qualifiedUserColumns(alias string) string {
	return alias + `.id, ` + alias + `.legacy_user_id, ` + alias + `.phone, ` + alias + `.name, ` +
		alias + `.status, ` + alias + `.failed_login_count, ` + alias + `.last_failed_at, ` +
		alias + `.locked_until, ` + alias + `.created_at, ` + alias + `.updated_at, ` + alias + `.id_card_valid`
}

func scanUser(row rowScanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.LegacyUserID, &u.Phone, &u.Name, &u.Status,
		&u.FailedLoginCount, &u.LastFailedAt, &u.LockedUntil, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func anyOrNil[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
