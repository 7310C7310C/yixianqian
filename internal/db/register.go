package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RegistrationState is the small projection used by the registration gate and
// the "waiting for review" page. RegisteredAt is NULL for legacy/seed accounts
// that were not created through the public registration form.
type RegistrationState struct {
	AccountStatus string
	RegisteredAt  *time.Time
}

// GetRegistrationState loads the review state for one account.
func (d *DB) GetRegistrationState(ctx context.Context, userID int64) (RegistrationState, error) {
	var s RegistrationState
	err := d.pool.QueryRow(ctx,
		`SELECT account_status, registered_at FROM users WHERE id = $1`, userID).
		Scan(&s.AccountStatus, &s.RegisteredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RegistrationState{}, ErrNotFound
	}
	if err != nil {
		return RegistrationState{}, fmt.Errorf("db: get registration state: %w", err)
	}
	return s, nil
}

// RegisterParams carries the validated public-registration fields. PII is
// limited to the encrypted ID card payload and its last four characters; the
// full number must never be stored in clear text.
type RegisterParams struct {
	Phone           string
	Name            string
	Nickname        string
	Gender          string
	Birthday        time.Time
	HeightCM        int
	Education       string
	BaptismalName   string
	Church          string
	ChurchLocation  string
	ChurchGroup     string
	NativePlace     string
	City            string
	Industry        string
	Position        string
	Income          string
	House           string
	Driving         string
	Marriage        string
	Family          string
	LiveWithParents string
	Personality     string
	SelfTraits      []string
	SelfHobbies     []string
	SelfSports      []string
	MBTI            []string
	PartnerCriteria string
	PartnerTraits   []string
	PartnerHobbies  []string
	PartnerSports   []string
	Wechat          string
	IDCardCipher    []byte
	IDCardLast4     string
	IDCardHash      string
	ContactConsent  bool
	ReferralSource  string
	IsForChild      string
	PasswordHash    string
}

func marshalStringList(values []string) string {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// CreateRegisteredUser creates the account and its first credential in one
// transaction. New web registrations start as login-enabled (status=active)
// but business-pending (account_status=pending), so they can sign in to see
// their review status without entering the app.
func (d *DB) CreateRegisteredUser(ctx context.Context, p RegisterParams) (User, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("db: begin registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := insertRegisteredUserTx(ctx, tx, p)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("db: commit registration: %w", err)
	}
	return u, nil
}

// CreateRegisteredUserWithQuota creates the account, its first credential and
// the photo-quota reservation in one transaction. When the daily quota cannot
// cover photos, the whole transaction rolls back and no user row is committed;
// callers get ErrPhotoDailyLimit. limit <= 0 disables the quota.
func (d *DB) CreateRegisteredUserWithQuota(ctx context.Context, p RegisterParams,
	day time.Time, photos, limit int) (User, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("db: begin registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := insertRegisteredUserTx(ctx, tx, p)
	if err != nil {
		return User{}, err
	}
	allowed, _, err := takePhotoQuota(ctx, tx, u.ID, day, photos, limit)
	if err != nil {
		return User{}, err
	}
	if !allowed {
		return User{}, ErrPhotoDailyLimit
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("db: commit registration: %w", err)
	}
	return u, nil
}

// insertRegisteredUserTx writes the account row and its credential; the caller
// owns the transaction and must commit it.
func insertRegisteredUserTx(ctx context.Context, tx pgx.Tx, p RegisterParams) (User, error) {
	u, err := scanUser(tx.QueryRow(ctx,
		`INSERT INTO users (
		   legacy_user_id, phone, name, nickname, gender, birthday, height_cm, education,
		   baptismal_name, church, church_location, church_group, native_place,
		   city, industry, position, income, house, driving, marriage, family,
		   live_with_parents, personality, self_traits, self_hobbies, self_sports,
		   mbti, partner_criteria, partner_traits, partner_hobbies, partner_sports,
		   wechat, id_card_ciphertext, id_card_last4, id_card_hash, contact_consent,
		   referral_source, is_for_child, status, account_status, registered_at
		 ) VALUES (
		   'U-' || nextval('user_no_seq'), $1, $2, $3, $4, $5, $6, $7,
		   $8, $9, $10, $11, $12,
		   $13, $14, $15, $16, $17, $18, $19, $20,
		   $21, $22, $23::jsonb, $24::jsonb, $25::jsonb,
		   $26::jsonb, $27, $28::jsonb, $29::jsonb, $30::jsonb,
		   $31, $32, $33, $34, $35,
		   $36, $37, 'active', 'pending', now()
		 ) RETURNING `+userColumns,
		p.Phone, p.Name, p.Nickname, p.Gender, p.Birthday, p.HeightCM, p.Education,
		p.BaptismalName, p.Church, p.ChurchLocation, p.ChurchGroup, p.NativePlace,
		p.City, p.Industry, p.Position, p.Income, p.House, p.Driving, p.Marriage, p.Family,
		p.LiveWithParents, p.Personality, marshalStringList(p.SelfTraits),
		marshalStringList(p.SelfHobbies), marshalStringList(p.SelfSports),
		marshalStringList(p.MBTI), p.PartnerCriteria,
		marshalStringList(p.PartnerTraits), marshalStringList(p.PartnerHobbies),
		marshalStringList(p.PartnerSports),
		p.Wechat, p.IDCardCipher, p.IDCardLast4, p.IDCardHash, p.ContactConsent,
		p.ReferralSource, p.IsForChild))
	if err != nil {
		return User{}, mapWriteError(err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO auth_credentials (user_id, password_hash, algorithm)
		 VALUES ($1, $2, 'argon2id')`, u.ID, p.PasswordHash); err != nil {
		return User{}, mapWriteError(err)
	}
	return u, nil
}

// DeleteRegisteredUser removes a registration that could not be completed.
// Only pending rows created by the public form are eligible, so a race with an
// administrator's approval can never delete an approved account.
func (d *DB) DeleteRegisteredUser(ctx context.Context, userID int64) error {
	_, err := d.pool.Exec(ctx,
		`DELETE FROM users
		 WHERE id = $1 AND registered_at IS NOT NULL AND account_status = 'pending'`, userID)
	if err != nil {
		return fmt.Errorf("db: delete pending registration: %w", err)
	}
	return nil
}

// RecordTrackEvent appends one first-party funnel event (page_view/join_click).
func (d *DB) RecordTrackEvent(ctx context.Context, event, source, ip string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO track_events (event, source, ip)
		 VALUES ($1, $2, NULLIF($3, '')::inet)`, event, source, ip)
	if err != nil {
		return fmt.Errorf("db: record track event: %w", err)
	}
	return nil
}
