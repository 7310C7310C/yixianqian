package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Ledger kinds. These strings are persisted; only add, never rename.
const (
	PointsKindInvite   = "invite"
	PointsKindAssist   = "assist"
	PointsKindAdmin    = "admin"
	PointsKindRedeem   = "redeem"
	PointsKindRefund   = "refund"
	PointsKindClawback = "clawback"
)

var pointsKindLabels = map[string]string{
	PointsKindInvite:   "邀请好友",
	PointsKindAssist:   "协助组织",
	PointsKindAdmin:    "管理员调整",
	PointsKindRedeem:   "兑换",
	PointsKindRefund:   "退还",
	PointsKindClawback: "收回",
}

// PointsKindLabel returns the Chinese label for a ledger kind. Unknown values
// fall back to the raw kind so admin views never render an empty cell.
func PointsKindLabel(kind string) string {
	if label, ok := pointsKindLabels[kind]; ok {
		return label
	}
	return kind
}

var (
	ErrPointsInsufficient    = errors.New("db: insufficient points")
	ErrPointsDuplicate       = errors.New("db: duplicate points entry")
	ErrPointsInvalidKind     = errors.New("db: invalid points kind")
	ErrPointsInvalidDelta    = errors.New("db: points delta must not be zero")
	ErrPointsNotFound        = errors.New("db: points record not found")
	ErrPointsUnavailable     = errors.New("db: points action unavailable")
	ErrPointsAlreadyRedeemed = errors.New("db: redemption already exists")
	ErrPointsInviteDue       = errors.New("db: invite confirmation period has not finished")
	ErrPointsConfig          = errors.New("db: invalid points config")
)

// PointsError carries a user-facing message while remaining matchable with
// errors.Is against the sentinel kinds above.
type PointsError struct {
	Err error
	Msg string
}

func (e *PointsError) Error() string { return e.Msg }
func (e *PointsError) Unwrap() error { return e.Err }

func pointsError(err error, msg string) error {
	return &PointsError{Err: err, Msg: msg}
}

// PointsMessage returns the user-facing part of an error, if any.
func PointsMessage(err error) string {
	var pe *PointsError
	if errors.As(err, &pe) {
		return pe.Msg
	}
	return ""
}

// PointsNow is the ledger clock. It uses the same Asia/Shanghai offset as the
// rest of the product so displayed times do not drift with the host timezone.
func PointsNow() time.Time { return time.Now().In(shanghai) }

// PointsEntry is one immutable ledger row. BalanceAfter is an audit snapshot
// only; authoritative balance is always SUM(delta).
type PointsEntry struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"-"`
	Delta          int       `json:"delta"`
	BalanceAfter   int       `json:"balance_after"`
	Kind           string    `json:"kind"`
	KindLabel      string    `json:"kind_label"`
	Reason         string    `json:"reason"`
	RefType        string    `json:"ref_type"`
	RefID          string    `json:"ref_id"`
	CreatedAt      time.Time `json:"created_at"`
	IdempotencyKey string    `json:"-"`
}

// PointsSummary is the header shown on /points.
type PointsSummary struct {
	Balance int `json:"balance"`
	Earned  int `json:"earned"`
	Spent   int `json:"spent"`
}

// AddPointsParams describes one ledger insert.
type AddPointsParams struct {
	UserID         int64
	Delta          int
	Kind           string
	Reason         string
	RefType        string
	RefID          string
	OperatorID     *int64
	IdempotencyKey string
}

// AddPointsEntry appends one immutable entry and returns the new balance.
// The user row is locked first, so concurrent debits cannot both read the same
// balance and overdraw. Idempotency is enforced by the unique index, not by a
// read-then-write check outside the transaction.
func (d *DB) AddPointsEntry(ctx context.Context, p AddPointsParams) (PointsEntry, int, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: begin points entry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	entry, balance, err := addPointsEntryTx(ctx, tx, p)
	if err != nil {
		return PointsEntry{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: commit points entry: %w", err)
	}
	return entry, balance, nil
}

func addPointsEntryTx(ctx context.Context, tx pgx.Tx, p AddPointsParams) (PointsEntry, int, error) {
	if p.UserID <= 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsNotFound, "用户不存在。")
	}
	if p.Kind == "" {
		p.Kind = PointsKindAdmin
	}
	if _, ok := pointsKindLabels[p.Kind]; !ok {
		return PointsEntry{}, 0, pointsError(ErrPointsInvalidKind, "未知的麦穗流水类型。")
	}
	if p.Delta == 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsInvalidDelta, "麦穗数量不能为 0。")
	}
	if err := lockPointsUser(ctx, tx, p.UserID); err != nil {
		return PointsEntry{}, 0, err
	}
	key := strings.TrimSpace(p.IdempotencyKey)
	var keyArg any
	if key != "" {
		keyArg = key
		// Duplicate must be reported before insufficient balance: on a retry
		// the points may already be spent, and "not enough points" would send
		// the caller looking in the wrong place.
		var existing int64
		err := tx.QueryRow(ctx,
			`SELECT id FROM points_ledger WHERE idempotency_key = $1`, key).Scan(&existing)
		if err == nil {
			return PointsEntry{}, 0, pointsError(ErrPointsDuplicate, "这笔麦穗已经记过账了。")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return PointsEntry{}, 0, fmt.Errorf("db: check points idempotency: %w", err)
		}
	}
	balance, err := pointsBalanceTx(ctx, tx, p.UserID)
	if err != nil {
		return PointsEntry{}, 0, err
	}
	after := balance + p.Delta
	if after < 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsInsufficient,
			fmt.Sprintf("麦穗不足：目前 %d 穗，本次需要 %d 穗。", balance, -p.Delta))
	}
	var (
		id        int64
		createdAt time.Time
		refType   = strings.TrimSpace(p.RefType)
		refID     = strings.TrimSpace(p.RefID)
		reason    = strings.TrimSpace(p.Reason)
	)
	err = tx.QueryRow(ctx,
		`INSERT INTO points_ledger
		   (user_id, delta, balance_after, kind, reason, ref_type, ref_id,
		    operator_id, created_at, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), $9)
		 RETURNING id, created_at`,
		p.UserID, p.Delta, after, p.Kind, reason, refType, refID,
		p.OperatorID, keyArg).Scan(&id, &createdAt)
	if err != nil {
		if isPointsUniqueViolation(err, "points_ledger_idem_uidx") {
			return PointsEntry{}, 0, pointsError(ErrPointsDuplicate, "这笔麦穗已经记过账了。")
		}
		return PointsEntry{}, 0, fmt.Errorf("db: insert points entry: %w", err)
	}
	return PointsEntry{
		ID:             id,
		UserID:         p.UserID,
		Delta:          p.Delta,
		BalanceAfter:   after,
		Kind:           p.Kind,
		KindLabel:      pointsKindLabels[p.Kind],
		Reason:         reason,
		RefType:        refType,
		RefID:          refID,
		CreatedAt:      createdAt,
		IdempotencyKey: key,
	}, after, nil
}

func isPointsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

func lockPointsUser(ctx context.Context, tx pgx.Tx, userID int64) error {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pointsError(ErrPointsNotFound, "用户不存在。")
	}
	if err != nil {
		return fmt.Errorf("db: lock points user: %w", err)
	}
	return nil
}

// pointsBalanceTx is the single authoritative balance calculation.
func pointsBalanceTx(ctx context.Context, tx pgx.Tx, userID int64) (int, error) {
	var balance int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM points_ledger WHERE user_id = $1`,
		userID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("db: sum points balance: %w", err)
	}
	return balance, nil
}

// PointsBalance returns SUM(delta) for one user.
func (d *DB) PointsBalance(ctx context.Context, userID int64) (int, error) {
	if userID <= 0 {
		return 0, pointsError(ErrPointsNotFound, "用户不存在。")
	}
	var balance int
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM points_ledger WHERE user_id = $1`,
		userID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("db: sum points balance: %w", err)
	}
	return balance, nil
}

// PointsSummary returns balance plus lifetime earned/spent.
func (d *DB) PointsSummary(ctx context.Context, userID int64) (PointsSummary, error) {
	var out PointsSummary
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0),
		        COALESCE(SUM(CASE WHEN delta > 0 THEN delta ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN delta < 0 THEN -delta ELSE 0 END), 0)
		 FROM points_ledger WHERE user_id = $1`, userID).
		Scan(&out.Balance, &out.Earned, &out.Spent)
	if err != nil {
		return PointsSummary{}, fmt.Errorf("db: points summary: %w", err)
	}
	return out, nil
}

// ListPointsEntries returns newest-first entries. Optional kinds are matched
// with IN; limit is clamped to 1..500 and offset to >= 0.
func (d *DB) ListPointsEntries(ctx context.Context, userID int64, limit, offset int, kinds []string) ([]PointsEntry, error) {
	if userID <= 0 {
		return nil, pointsError(ErrPointsNotFound, "用户不存在。")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	sql := `SELECT id, user_id, delta, balance_after, kind, reason, ref_type, ref_id,
	               created_at, COALESCE(idempotency_key, '')
	        FROM points_ledger WHERE user_id = $1`
	args := []any{userID}
	if len(kinds) > 0 {
		sql += ` AND kind = ANY($2)`
		args = append(args, kinds)
	}
	sql += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list points entries: %w", err)
	}
	defer rows.Close()
	out := make([]PointsEntry, 0)
	for rows.Next() {
		var e PointsEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Delta, &e.BalanceAfter, &e.Kind,
			&e.Reason, &e.RefType, &e.RefID, &e.CreatedAt, &e.IdempotencyKey); err != nil {
			return nil, fmt.Errorf("db: scan points entry: %w", err)
		}
		e.KindLabel = pointsKindLabels[e.Kind]
		if e.KindLabel == "" {
			e.KindLabel = e.Kind
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate points entries: %w", err)
	}
	return out, nil
}

// PointsEntryIDForKey returns the ledger row for an idempotency key, or 0.
func (d *DB) PointsEntryIDForKey(ctx context.Context, key string) (int64, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, nil
	}
	var id int64
	err := d.pool.QueryRow(ctx,
		`SELECT id FROM points_ledger WHERE idempotency_key = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("db: find points entry key: %w", err)
	}
	return id, nil
}

// GrantPoints is the "already granted" friendly wrapper used by periodic
// settlement: a duplicate key returns ok=false instead of an error.
func (d *DB) GrantPoints(ctx context.Context, p AddPointsParams) (PointsEntry, int, bool, error) {
	entry, balance, err := d.AddPointsEntry(ctx, p)
	if errors.Is(err, ErrPointsDuplicate) {
		return PointsEntry{}, 0, false, nil
	}
	if err != nil {
		return PointsEntry{}, 0, false, err
	}
	return entry, balance, true, nil
}

// ReversePointsEntry appends the opposite of an existing entry. Original rows
// are never modified or deleted.
func (d *DB) ReversePointsEntry(ctx context.Context, entryID int64, kind, reason string, operatorID *int64, idempotencyKey string) (PointsEntry, int, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: begin points reversal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	entry, balance, err := reversePointsEntryTx(ctx, tx, entryID, kind, reason, operatorID, idempotencyKey)
	if err != nil {
		return PointsEntry{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: commit points reversal: %w", err)
	}
	return entry, balance, nil
}

func reversePointsEntryTx(ctx context.Context, tx pgx.Tx, entryID int64, kind, reason string, operatorID *int64, idempotencyKey string) (PointsEntry, int, error) {
	var userID int64
	var delta int
	err := tx.QueryRow(ctx,
		`SELECT user_id, delta FROM points_ledger WHERE id = $1`, entryID).Scan(&userID, &delta)
	if errors.Is(err, pgx.ErrNoRows) {
		return PointsEntry{}, 0, pointsError(ErrPointsNotFound, "麦穗流水不存在。")
	}
	if err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: load points entry for reversal: %w", err)
	}
	if delta == 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable, "这笔流水金额为 0，无需反向。")
	}
	if kind == "" {
		kind = PointsKindRefund
	}
	return addPointsEntryTx(ctx, tx, AddPointsParams{
		UserID:         userID,
		Delta:          -delta,
		Kind:           kind,
		Reason:         reason,
		RefType:        "ledger",
		RefID:          fmt.Sprintf("%d", entryID),
		OperatorID:     operatorID,
		IdempotencyKey: idempotencyKey,
	})
}
