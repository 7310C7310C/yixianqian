package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ActivateParams carries the data needed to atomically consume an activation
// token and install the first credential.
type ActivateParams struct {
	TokenHash    string
	PasswordHash string
	IP           string
	UserAgent    string
}

// CreateActivationToken invalidates previous unused tokens for the same target
// and stores only the hash of the new token.
func (d *DB) CreateActivationToken(ctx context.Context, userID *int64, phone, name, tokenHash string, ttl time.Duration) (ActivationToken, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ActivationToken{}, fmt.Errorf("db: begin activation token: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if userID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE activation_tokens SET consumed_at = now()
			 WHERE user_id = $1 AND consumed_at IS NULL`, *userID); err != nil {
			return ActivationToken{}, fmt.Errorf("db: invalidate activation tokens: %w", err)
		}
	} else if phone != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE activation_tokens SET consumed_at = now()
			 WHERE user_id IS NULL AND phone = $1 AND consumed_at IS NULL`, phone); err != nil {
			return ActivationToken{}, fmt.Errorf("db: invalidate phone activation tokens: %w", err)
		}
	}

	var uidArg any
	if userID != nil {
		uidArg = *userID
	}
	var phoneArg, nameArg any
	if phone != "" {
		phoneArg = phone
	}
	if name != "" {
		nameArg = name
	}

	var out ActivationToken
	err = tx.QueryRow(ctx,
		`INSERT INTO activation_tokens (user_id, phone, name, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))
		 RETURNING id, user_id, COALESCE(phone, ''), COALESCE(name, ''), expires_at, consumed_at`,
		uidArg, phoneArg, nameArg, tokenHash, ttl.Seconds()).
		Scan(&out.ID, &out.UserID, &out.Phone, &out.Name, &out.ExpiresAt, &out.ConsumedAt)
	if err != nil {
		return ActivationToken{}, mapWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ActivationToken{}, fmt.Errorf("db: commit activation token: %w", err)
	}
	out.Valid = true
	return out, nil
}

// GetActivationToken loads a token by hash for rendering the activation page.
func (d *DB) GetActivationToken(ctx context.Context, tokenHash string) (ActivationToken, error) {
	var t ActivationToken
	err := d.pool.QueryRow(ctx,
		`SELECT t.id, t.user_id,
		        COALESCE(u.phone, t.phone, ''),
		        COALESCE(u.name, t.name, ''),
		        t.expires_at, t.consumed_at,
		        (t.consumed_at IS NULL AND t.expires_at > now()) AS valid
		 FROM activation_tokens t
		 LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = $1`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.Phone, &t.Name, &t.ExpiresAt, &t.ConsumedAt, &t.Valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivationToken{}, ErrInvalidActivation
	}
	if err != nil {
		return ActivationToken{}, fmt.Errorf("db: get activation token: %w", err)
	}
	return t, nil
}

// ActivateUser consumes a token, creates the user when the token targets a new
// phone, stores the credential, revokes old sessions and records the audit
// entry — all in one transaction.
func (d *DB) ActivateUser(ctx context.Context, p ActivateParams) (User, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("db: begin activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		tokenID    int64
		userID     *int64
		phone      *string
		name       *string
		expiresAt  time.Time
		consumedAt *time.Time
		valid      bool
	)
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, phone, name, expires_at, consumed_at,
		        (consumed_at IS NULL AND expires_at > now())
		 FROM activation_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`, p.TokenHash).
		Scan(&tokenID, &userID, &phone, &name, &expiresAt, &consumedAt, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidActivation
	}
	if err != nil {
		return User{}, fmt.Errorf("db: lock activation token: %w", err)
	}
	if !valid {
		return User{}, ErrInvalidActivation
	}

	var user User
	if userID != nil {
		user, err = scanUser(tx.QueryRow(ctx,
			"SELECT "+userColumns+" FROM users WHERE id = $1 FOR UPDATE", *userID))
		if errors.Is(err, ErrNotFound) {
			return User{}, ErrInvalidActivation
		}
		if err != nil {
			return User{}, fmt.Errorf("db: lock activation user: %w", err)
		}
		if user.Status != "active" {
			return User{}, ErrAccountDisabled
		}
	} else {
		if phone == nil || name == nil || *phone == "" || *name == "" {
			return User{}, ErrInvalidActivation
		}
		user, err = scanUser(tx.QueryRow(ctx,
			`INSERT INTO users (phone, name) VALUES ($1, $2) RETURNING `+userColumns,
			*phone, *name))
		if err != nil {
			return User{}, mapWriteError(err)
		}
	}

	var hasCredential bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM auth_credentials WHERE user_id = $1)", user.ID).
		Scan(&hasCredential); err != nil {
		return User{}, fmt.Errorf("db: check existing credential: %w", err)
	}
	if hasCredential {
		return User{}, ErrAlreadyActivated
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO auth_credentials (user_id, password_hash) VALUES ($1, $2)`,
		user.ID, p.PasswordHash); err != nil {
		return User{}, mapWriteError(err)
	}

	var phoneArg any
	if phone != nil {
		phoneArg = *phone
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activation_tokens SET consumed_at = now()
		 WHERE id = $1
		    OR (user_id = $2 AND consumed_at IS NULL)
		    OR (user_id IS NULL AND phone = $3 AND consumed_at IS NULL)`,
		tokenID, user.ID, phoneArg); err != nil {
		return User{}, fmt.Errorf("db: consume activation token: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, user.ID); err != nil {
		return User{}, fmt.Errorf("db: revoke sessions after activation: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO notifications (user_id, kind, title, body)
		 VALUES ($1, 'account.activated', '账号已启用', '密码已设定完成，可以开始使用。')`,
		user.ID); err != nil {
		return User{}, fmt.Errorf("db: create welcome notification: %w", err)
	}

	meta := map[string]any{"user_id": user.ID}
	if user.LegacyUserID != nil {
		meta["legacy_user_id"] = *user.LegacyUserID
	}
	if err := insertAudit(ctx, tx, AuditEntry{
		ActorUserID: &user.ID,
		Action:      "user.activated",
		TargetType:  "user",
		TargetID:    fmt.Sprintf("%d", user.ID),
		IP:          p.IP,
		UserAgent:   p.UserAgent,
		Metadata:    meta,
	}); err != nil {
		return User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("db: commit activation: %w", err)
	}
	return user, nil
}

func marshalMetadata(meta map[string]any) string {
	if len(meta) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
