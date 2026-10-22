package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminPointsStats is the 麦穗 overview shown on /admin/points.
type AdminPointsStats struct {
	Earned                 int64
	Spent                  int64
	Balance                int64
	PendingInvites         int64
	PendingRedemptions     int64
	ActiveRedemptions      int64
	PendingWishes          int64
	WishTotal              int64
	PendingRecommendations int64
	MatchmakerTotal        int64
}

// AdminPointUser is one row of the admin points user picker. Phone is kept
// masked so the safe projection can be handed to templates unchanged.
type AdminPointUser struct {
	ID            int64
	LegacyUserID  string
	Name          string
	Nickname      string
	PhoneMasked   string
	AccountStatus string
	LoginStatus   string
	Balance       int
	LastEntryAt   *time.Time
}

// AdminLedgerRow is one ledger row across all users with the owning user and operator names.
type AdminLedgerRow struct {
	ID           int64
	UserID       int64
	UserName     string
	LegacyUserID string
	Delta        int
	BalanceAfter int
	Kind         string
	KindLabel    string
	Reason       string
	OperatorName string
	CreatedAt    time.Time
}

// AdminLedgerFilter narrows the ledger listing across all users.
type AdminLedgerFilter struct {
	UserID int64
	Query  string
	Kind   string
	Limit  int
	Offset int
}

// AdminInviteRow is the masked admin projection of an invite. The full phone
// and the raw note are deliberately not part of this struct.
type AdminInviteRow struct {
	ID              int64
	InviterID       int64
	InviterName     string
	InviterLegacyID string
	PhoneMasked     string
	Status          string
	StatusLabel     string
	Gender          string
	Reward          int
	CreatedAt       time.Time
	ConfirmDueAt    *time.Time
	ConfirmedAt     *time.Time
}

// AdminInviteCounts groups invites by lifecycle status.
type AdminInviteCounts struct {
	Total      int64
	Pending    int64
	Confirmed  int64
	Awaiting   int64
	Finalized  int64
	ClawedBack int64
	Rejected   int64
}

// AdminRedemptionRow is the safe admin projection of a redemption. Params are
// joined into display fields; the raw jsonb is never exposed.
type AdminRedemptionRow struct {
	ID            int64
	UserID        int64
	UserName      string
	UserLegacyID  string
	Item          string
	ItemLabel     string
	Cost          int
	Status        string
	StatusLabel   string
	ActivityID    int64
	ActivityTitle string
	TargetUserID  int64
	TargetName    string
	TargetLegacy  string
	Condition     string
	Note          string
	RefID         string
	CreatedAt     time.Time
	ResolvedAt    *time.Time
}

// AdminRedemptionFilter narrows the redemption listing across all users.
type AdminRedemptionFilter struct {
	Item   string
	Status string
	Limit  int
	Offset int
}

// AdminPointsStats aggregates发放/消耗/余额 and the pending work counters.
func (d *DB) AdminPointsStats(ctx context.Context) (AdminPointsStats, error) {
	var s AdminPointsStats
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta) FILTER (
		            WHERE delta > 0 AND kind = ANY($1)), 0),
		        COALESCE(-SUM(delta) FILTER (
		            WHERE delta < 0 AND kind = $2), 0),
		        COALESCE(SUM(delta), 0)
		   FROM points_ledger`,
		[]string{PointsKindInvite, PointsKindAssist, PointsKindAdmin},
		PointsKindRedeem).
		Scan(&s.Earned, &s.Spent, &s.Balance)
	if err != nil {
		return AdminPointsStats{}, fmt.Errorf("db: admin points ledger stats: %w", err)
	}
	err = d.pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM invites WHERE status = ANY($1)),
		   (SELECT count(*) FROM points_redemptions WHERE status = $2),
		   (SELECT count(*) FROM points_redemptions WHERE status = $3),
		   (SELECT count(*) FROM points_redemptions WHERE item = $4 AND status = $5),
		   (SELECT count(*) FROM points_redemptions WHERE item = $4),
		   (SELECT count(*) FROM points_redemptions WHERE item = $6 AND status = ANY($7)),
		   (SELECT count(*) FROM points_redemptions WHERE item = $6)`,
		[]string{InviteStatusPending, InviteStatusConfirmed},
		RedeemStatusPending,
		RedeemStatusActive,
		RedeemItemWish,
		RedeemStatusActive,
		RedeemItemMatchmaker,
		[]string{RedeemStatusPending, RedeemStatusRecommended}).
		Scan(&s.PendingInvites, &s.PendingRedemptions, &s.ActiveRedemptions,
			&s.PendingWishes, &s.WishTotal,
			&s.PendingRecommendations, &s.MatchmakerTotal)
	if err != nil {
		return AdminPointsStats{}, fmt.Errorf("db: admin points work stats: %w", err)
	}
	return s, nil
}

const adminPointUserSelect = `SELECT u.id, COALESCE(u.legacy_user_id, ''), u.name,
		COALESCE(NULLIF(u.nickname, ''), ''), u.phone, u.account_status, u.status,
		COALESCE((SELECT SUM(pl.delta) FROM points_ledger pl WHERE pl.user_id = u.id), 0),
		(SELECT max(pl.created_at) FROM points_ledger pl WHERE pl.user_id = u.id)
	FROM users u`

func scanAdminPointUser(row rowScanner) (AdminPointUser, error) {
	var (
		u     AdminPointUser
		phone string
	)
	err := row.Scan(&u.ID, &u.LegacyUserID, &u.Name, &u.Nickname, &phone,
		&u.AccountStatus, &u.LoginStatus, &u.Balance, &u.LastEntryAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminPointUser{}, ErrNotFound
	}
	if err != nil {
		return AdminPointUser{}, err
	}
	u.PhoneMasked = maskPointsPhone(phone)
	return u, nil
}

// SearchPointUsers finds users by name, nickname, phone or member number. With
// an empty query it lists users that have ledger history, newest first.
func (d *DB) SearchPointUsers(ctx context.Context, query string, limit int) ([]AdminPointUser, error) {
	query = strings.TrimSpace(query)
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := d.pool.Query(ctx, adminPointUserSelect+`
		WHERE ($1 = ''
		       AND EXISTS (SELECT 1 FROM points_ledger pl WHERE pl.user_id = u.id))
		   OR ($1 <> ''
		       AND (position(lower($1) in lower(u.name)) > 0
		            OR position(lower($1) in lower(COALESCE(u.nickname, ''))) > 0
		            OR position(lower($1) in lower(u.phone)) > 0
		            OR position(lower($1) in lower(COALESCE(u.legacy_user_id, ''))) > 0))
		ORDER BY COALESCE((SELECT max(pl.id) FROM points_ledger pl WHERE pl.user_id = u.id), 0) DESC,
		         u.id DESC
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("db: search points users: %w", err)
	}
	defer rows.Close()
	out := make([]AdminPointUser, 0)
	for rows.Next() {
		u, err := scanAdminPointUser(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan points user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate points users: %w", err)
	}
	return out, nil
}

// GetPointUser loads one user with the current ledger balance.
func (d *DB) GetPointUser(ctx context.Context, userID int64) (AdminPointUser, error) {
	if userID <= 0 {
		return AdminPointUser{}, ErrNotFound
	}
	u, err := scanAdminPointUser(d.pool.QueryRow(ctx,
		adminPointUserSelect+` WHERE u.id = $1`, userID))
	if errors.Is(err, ErrNotFound) {
		return AdminPointUser{}, ErrNotFound
	}
	if err != nil {
		return AdminPointUser{}, fmt.Errorf("db: get points user: %w", err)
	}
	return u, nil
}

// ListAdminLedger returns the newest ledger rows across all users. Limit is clamped to
// 1..200 and callers may fetch limit+1 to detect another page.
func (d *DB) ListAdminLedger(ctx context.Context, f AdminLedgerFilter) ([]AdminLedgerRow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	query := strings.TrimSpace(f.Query)
	rows, err := d.pool.Query(ctx,
		`SELECT l.id, l.user_id, u.name, COALESCE(u.legacy_user_id, ''),
		        l.delta, l.balance_after, l.kind, l.reason,
		        COALESCE(op.name, ''), l.created_at
		   FROM points_ledger l
		   JOIN users u ON u.id = l.user_id
		   LEFT JOIN users op ON op.id = l.operator_id
		  WHERE ($1 = 0 OR l.user_id = $1)
		    AND ($2 = ''
		         OR position(lower($2) in lower(u.name)) > 0
		         OR position(lower($2) in lower(COALESCE(u.nickname, ''))) > 0
		         OR position(lower($2) in lower(u.phone)) > 0
		         OR position(lower($2) in lower(COALESCE(u.legacy_user_id, ''))) > 0)
		    AND ($3 = '' OR l.kind = $3)
		  ORDER BY l.id DESC
		  LIMIT $4 OFFSET $5`,
		f.UserID, query, strings.TrimSpace(f.Kind), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("db: list admin ledger: %w", err)
	}
	defer rows.Close()
	out := make([]AdminLedgerRow, 0)
	for rows.Next() {
		var row AdminLedgerRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.UserName, &row.LegacyUserID,
			&row.Delta, &row.BalanceAfter, &row.Kind, &row.Reason,
			&row.OperatorName, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan admin ledger: %w", err)
		}
		row.KindLabel = pointsKindLabels[row.Kind]
		if row.KindLabel == "" {
			row.KindLabel = row.Kind
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin ledger: %w", err)
	}
	return out, nil
}

// ListAdminInvites returns masked invite rows, newest first.
func (d *DB) ListAdminInvites(ctx context.Context, status string, limit int) ([]AdminInviteRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.pool.Query(ctx,
		`SELECT i.id, i.inviter_id, u.name, COALESCE(u.legacy_user_id, ''),
		        i.invitee_phone, i.status, i.gender, i.created_at,
		        i.confirm_due_at, i.confirmed_at
		   FROM invites i
		   JOIN users u ON u.id = i.inviter_id
		  WHERE ($1 = '' OR i.status = $1)
		  ORDER BY i.id DESC
		  LIMIT $2`,
		strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("db: list admin invites: %w", err)
	}
	defer rows.Close()
	out := make([]AdminInviteRow, 0)
	for rows.Next() {
		var (
			row   AdminInviteRow
			phone string
		)
		if err := rows.Scan(&row.ID, &row.InviterID, &row.InviterName,
			&row.InviterLegacyID, &phone, &row.Status, &row.Gender,
			&row.CreatedAt, &row.ConfirmDueAt, &row.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("db: scan admin invite: %w", err)
		}
		row.PhoneMasked = maskPointsPhone(phone)
		row.StatusLabel = inviteStatusLabels[row.Status]
		if row.StatusLabel == "" {
			row.StatusLabel = row.Status
		}
		if row.Status == InviteStatusConfirmed || row.Status == InviteStatusFinalized {
			row.Reward = InviteRewardAmount(cfg, row.Gender)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin invites: %w", err)
	}
	return out, nil
}

// AdminInviteCounts counts invites per status for the overview and filters.
func (d *DB) AdminInviteCounts(ctx context.Context) (AdminInviteCounts, error) {
	var c AdminInviteCounts
	err := d.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE status = $1),
		        count(*) FILTER (WHERE status = $2),
		        count(*) FILTER (WHERE status = ANY($3)),
		        count(*) FILTER (WHERE status = $4),
		        count(*) FILTER (WHERE status = $5),
		        count(*) FILTER (WHERE status = $6)
		   FROM invites`,
		InviteStatusPending, InviteStatusConfirmed,
		[]string{InviteStatusPending, InviteStatusConfirmed},
		InviteStatusFinalized, InviteStatusClawedBack, InviteStatusRejected).
		Scan(&c.Total, &c.Pending, &c.Confirmed, &c.Awaiting,
			&c.Finalized, &c.ClawedBack, &c.Rejected)
	if err != nil {
		return AdminInviteCounts{}, fmt.Errorf("db: admin invite counts: %w", err)
	}
	return c, nil
}

// The CASE guards keep legacy/corrupt params from failing the bigint cast.
const adminRedemptionSelect = `SELECT r.id, r.user_id, u.name,
		COALESCE(u.legacy_user_id, ''), r.item, r.cost, r.status,
		COALESCE(a.id, 0),
		COALESCE(NULLIF(a.name, ''), NULLIF(r.params->>'title', ''), ''),
		COALESCE(t.id, 0), COALESCE(t.name, ''),
		COALESCE(t.legacy_user_id, ''),
		COALESCE(r.params->>'condition', ''), r.note, r.ref_id,
		r.created_at, r.resolved_at
	FROM points_redemptions r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN activities a ON a.id =
		CASE WHEN r.params->>'activity_id' ~ '^[0-9]+$'
		     THEN (r.params->>'activity_id')::bigint END
	LEFT JOIN users t ON t.id =
		CASE WHEN r.params->>'target_user_id' ~ '^[0-9]+$'
		     THEN (r.params->>'target_user_id')::bigint END`

func scanAdminRedemption(row rowScanner) (AdminRedemptionRow, error) {
	var r AdminRedemptionRow
	err := row.Scan(&r.ID, &r.UserID, &r.UserName, &r.UserLegacyID,
		&r.Item, &r.Cost, &r.Status, &r.ActivityID, &r.ActivityTitle,
		&r.TargetUserID, &r.TargetName, &r.TargetLegacy, &r.Condition,
		&r.Note, &r.RefID, &r.CreatedAt, &r.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminRedemptionRow{}, ErrNotFound
	}
	if err != nil {
		return AdminRedemptionRow{}, err
	}
	r.ItemLabel = RedeemItemLabels[r.Item]
	if r.ItemLabel == "" {
		r.ItemLabel = r.Item
	}
	r.StatusLabel = RedeemStatusLabels[r.Status]
	if r.StatusLabel == "" {
		r.StatusLabel = r.Status
	}
	return r, nil
}

// ListAdminRedemptions returns safe redemption rows across all users, newest first.
func (d *DB) ListAdminRedemptions(ctx context.Context, f AdminRedemptionFilter) ([]AdminRedemptionRow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := d.pool.Query(ctx, adminRedemptionSelect+`
		WHERE ($1 = '' OR r.item = $1)
		  AND ($2 = '' OR r.status = $2)
		ORDER BY r.id DESC
		LIMIT $3 OFFSET $4`,
		strings.TrimSpace(f.Item), strings.TrimSpace(f.Status), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("db: list admin redemptions: %w", err)
	}
	defer rows.Close()
	out := make([]AdminRedemptionRow, 0)
	for rows.Next() {
		row, err := scanAdminRedemption(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan admin redemption: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin redemptions: %w", err)
	}
	return out, nil
}

// GetAdminRedemption loads one safe redemption projection.
func (d *DB) GetAdminRedemption(ctx context.Context, id int64) (AdminRedemptionRow, error) {
	if id <= 0 {
		return AdminRedemptionRow{}, ErrNotFound
	}
	row, err := scanAdminRedemption(d.pool.QueryRow(ctx,
		adminRedemptionSelect+` WHERE r.id = $1`, id))
	if errors.Is(err, ErrNotFound) {
		return AdminRedemptionRow{}, ErrNotFound
	}
	if err != nil {
		return AdminRedemptionRow{}, fmt.Errorf("db: get admin redemption: %w", err)
	}
	return row, nil
}
