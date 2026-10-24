package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpdateWechatConsent stores the mutual-contact wechat and the explicit
// exchange consent. setWechat=false keeps the current value (used by the
// consent-only toggle on the mutual page).
func (d *DB) UpdateWechatConsent(ctx context.Context, userID int64, wechat string, setWechat, consent bool) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE users
		    SET wechat = CASE WHEN $2 THEN $3 ELSE wechat END,
		        contact_consent = $4,
		        updated_at = now()
		  WHERE id = $1`, userID, setWechat, wechat, consent)
	if err != nil {
		return fmt.Errorf("db: update wechat consent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// WechatConsentState is the minimal contact projection for the mutual page.
type WechatConsentState struct {
	Wechat         string
	ContactConsent bool
}

// GetWechatConsentState loads the viewer's current contact exchange state.
func (d *DB) GetWechatConsentState(ctx context.Context, userID int64) (WechatConsentState, error) {
	var s WechatConsentState
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(wechat, ''), contact_consent FROM users WHERE id = $1`, userID).
		Scan(&s.Wechat, &s.ContactConsent)
	if errors.Is(err, pgx.ErrNoRows) {
		return WechatConsentState{}, ErrNotFound
	}
	if err != nil {
		return WechatConsentState{}, fmt.Errorf("db: get wechat consent: %w", err)
	}
	return s, nil
}
