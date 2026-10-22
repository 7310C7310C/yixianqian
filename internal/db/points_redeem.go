package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Redemption items. Persisted values from v1; only add, never rename.
const (
	RedeemItemRealLike   = "real_like"
	RedeemItemPriority   = "priority_signup"
	RedeemItemWish       = "wish"
	RedeemItemMatchmaker = "matchmaker"
)

var RedeemItemLabels = map[string]string{
	RedeemItemRealLike:   "额外实名喜欢",
	RedeemItemPriority:   "活动优先报名",
	RedeemItemWish:       "心愿名额",
	RedeemItemMatchmaker: "红娘人工推荐",
}

// Redemption statuses. Persisted values from v1; only add, never rename.
const (
	RedeemStatusActive      = "active"
	RedeemStatusRefunded    = "refunded"
	RedeemStatusArranged    = "arranged"
	RedeemStatusPending     = "pending"
	RedeemStatusRecommended = "recommended"
	RedeemStatusDone        = "done"
	RedeemStatusCancelled   = "cancelled"
)

var RedeemStatusLabels = map[string]string{
	RedeemStatusActive:      "生效中",
	RedeemStatusRefunded:    "已退穗",
	RedeemStatusArranged:    "已安排",
	RedeemStatusPending:     "待处理",
	RedeemStatusRecommended: "已推荐",
	RedeemStatusDone:        "已完成",
	RedeemStatusCancelled:   "已取消",
}

var pointsOpenActivityStatuses = map[string]bool{
	"报名中":   true,
	"未开始报名": true,
}

// PointsActivity is the activity fact set needed by redemption. Activity
// module ownership stays with activities; this is a read-only projection.
type PointsActivity struct {
	ID         int64      `json:"id"`
	ActivityNo string     `json:"activity_no"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	MaxSignup  int        `json:"max_signup"`
	StartAt    *time.Time `json:"start_at,omitempty"`
	Open       bool       `json:"open"`
}

// WishTarget is the safe public projection returned by /api/points/wish-targets.
type WishTarget struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
	UserID   string `json:"user_id"`
	Gender   string `json:"gender"`
}

// Redemption is one points_redemptions row.
type Redemption struct {
	ID             int64          `json:"id"`
	UserID         int64          `json:"-"`
	Item           string         `json:"item"`
	ItemLabel      string         `json:"item_label"`
	Cost           int            `json:"cost"`
	Status         string         `json:"status"`
	StatusLabel    string         `json:"status_label"`
	Params         map[string]any `json:"params"`
	IdempotencyKey string         `json:"-"`
	LedgerID       *int64         `json:"ledger_id,omitempty"`
	RefundLedgerID *int64         `json:"refund_ledger_id,omitempty"`
	RefundKey      *string        `json:"-"`
	RefID          string         `json:"ref_id"`
	CreatedAt      time.Time      `json:"created_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	Note           string         `json:"note"`
}

// RedeemRequest is the raw user request after JSON/form decoding.
type RedeemRequest struct {
	Item         string
	RequestKey   string
	ActivityID   string
	TargetUserID int64
	Condition    string
}

type pointsActivityQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetPointsActivity resolves a numeric id or an activity_no.
func (d *DB) GetPointsActivity(ctx context.Context, key string) (PointsActivity, error) {
	return getPointsActivity(ctx, d.pool, key)
}

func getPointsActivity(ctx context.Context, q pointsActivityQuerier, key string) (PointsActivity, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return PointsActivity{}, pointsError(ErrPointsNotFound, "活动不存在。")
	}
	var (
		act PointsActivity
		err error
	)
	if id, parseErr := strconv.ParseInt(key, 10, 64); parseErr == nil && id > 0 {
		err = q.QueryRow(ctx,
			`SELECT id, activity_no, name, status, max_signup, start_at
			 FROM activities WHERE id = $1`, id).
			Scan(&act.ID, &act.ActivityNo, &act.Name, &act.Status, &act.MaxSignup, &act.StartAt)
	} else {
		err = q.QueryRow(ctx,
			`SELECT id, activity_no, name, status, max_signup, start_at
			 FROM activities WHERE activity_no = $1`, key).
			Scan(&act.ID, &act.ActivityNo, &act.Name, &act.Status, &act.MaxSignup, &act.StartAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return PointsActivity{}, pointsError(ErrPointsNotFound, "活动不存在。")
	}
	if err != nil {
		return PointsActivity{}, fmt.Errorf("db: load points activity: %w", err)
	}
	act.Open = pointsOpenActivityStatuses[act.Status]
	return act, nil
}

// ListOpenPointsActivities feeds the priority/wish pickers.
func (d *DB) ListOpenPointsActivities(ctx context.Context, limit int) ([]PointsActivity, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := d.pool.Query(ctx,
		`SELECT id, activity_no, name, status, max_signup, start_at
		 FROM activities
		 WHERE status = ANY($1)
		 ORDER BY start_at NULLS LAST, id DESC
		 LIMIT $2`,
		[]string{"报名中", "未开始报名"}, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list open activities: %w", err)
	}
	defer rows.Close()
	out := make([]PointsActivity, 0)
	for rows.Next() {
		var act PointsActivity
		if err := rows.Scan(&act.ID, &act.ActivityNo, &act.Name, &act.Status,
			&act.MaxSignup, &act.StartAt); err != nil {
			return nil, fmt.Errorf("db: scan open activity: %w", err)
		}
		act.Open = true
		out = append(out, act)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate open activities: %w", err)
	}
	return out, nil
}

// IsSignedUp matches the old get_user_signup rule: any non-cancelled signup.
func (d *DB) IsSignedUp(ctx context.Context, userID, activityID int64) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM signups
		   WHERE user_id = $1 AND activity_id = $2 AND status <> '已取消'
		 )`, userID, activityID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: check signup: %w", err)
	}
	return exists, nil
}

func pointsTargetGender(viewerGender string) string {
	if strings.TrimSpace(viewerGender) == "男性" {
		return "女性"
	}
	return "男性"
}

// ListWishTargets returns active, single, opposite-gender users. Only public
// card fields are exposed; phone/wechat are never selected.
func (d *DB) ListWishTargets(ctx context.Context, viewerID int64, query string, limit int) ([]WishTarget, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	var viewerGender string
	err := d.pool.QueryRow(ctx, `SELECT gender FROM users WHERE id = $1`, viewerID).Scan(&viewerGender)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pointsError(ErrPointsNotFound, "用户不存在。")
	}
	if err != nil {
		return nil, fmt.Errorf("db: load wish viewer: %w", err)
	}
	targetGender := pointsTargetGender(viewerGender)
	query = strings.ToLower(strings.TrimSpace(query))
	rows, err := d.pool.Query(ctx,
		`SELECT id, COALESCE(NULLIF(nickname, ''), ''), COALESCE(legacy_user_id, ''), gender
		 FROM users
		 WHERE id <> $1
		   AND status = 'active'
		   AND account_kind = 'user'
		   AND account_status = 'active'
		   AND gender = $2
		   AND ($3 = '' OR lower(COALESCE(nickname, '')) LIKE '%' || $3 || '%'
		                OR lower(COALESCE(legacy_user_id, '')) LIKE '%' || $3 || '%')
		 ORDER BY COALESCE(legacy_user_id, ''), id
		 LIMIT $4`,
		viewerID, targetGender, query, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list wish targets: %w", err)
	}
	defer rows.Close()
	out := make([]WishTarget, 0)
	for rows.Next() {
		var t WishTarget
		if err := rows.Scan(&t.ID, &t.Nickname, &t.UserID, &t.Gender); err != nil {
			return nil, fmt.Errorf("db: scan wish target: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate wish targets: %w", err)
	}
	return out, nil
}

func getWishTarget(ctx context.Context, q pointsActivityQuerier, viewerID, targetID int64) (WishTarget, error) {
	if targetID <= 0 {
		return WishTarget{}, pointsError(ErrPointsUnavailable, "请指定一位想认识的人。")
	}
	if targetID == viewerID {
		return WishTarget{}, pointsError(ErrPointsUnavailable, "不能把心愿指定给自己。")
	}
	var viewerGender string
	err := q.QueryRow(ctx, `SELECT gender FROM users WHERE id = $1`, viewerID).Scan(&viewerGender)
	if err != nil {
		return WishTarget{}, fmt.Errorf("db: load wish viewer: %w", err)
	}
	targetGender := pointsTargetGender(viewerGender)
	var t WishTarget
	err = q.QueryRow(ctx,
		`SELECT id, COALESCE(NULLIF(nickname, ''), ''), COALESCE(legacy_user_id, ''), gender
		 FROM users
		 WHERE id = $1 AND status = 'active' AND account_kind = 'user'
		   AND account_status = 'active' AND gender = $2`,
		targetID, targetGender).
		Scan(&t.ID, &t.Nickname, &t.UserID, &t.Gender)
	if errors.Is(err, pgx.ErrNoRows) {
		return WishTarget{}, pointsError(ErrPointsUnavailable, "对方目前不在可指定名单中。")
	}
	if err != nil {
		return WishTarget{}, fmt.Errorf("db: load wish target: %w", err)
	}
	return t, nil
}

func pointsItemCost(cfg PointsConfig, item string) (int, error) {
	switch item {
	case RedeemItemRealLike:
		return cfg.RedeemRealLike, nil
	case RedeemItemPriority:
		return cfg.RedeemPrioritySignup, nil
	case RedeemItemWish:
		return cfg.RedeemWish, nil
	case RedeemItemMatchmaker:
		return cfg.RedeemMatchmaker, nil
	default:
		return 0, pointsError(ErrPointsUnavailable, "没有这个兑换项。")
	}
}

func sanitizePointsRequestKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 128 {
		raw = raw[:128]
	}
	return raw
}

// newRedemptionKey mirrors lib/points_redeem._new_key. Activity-scoped items
// are one-per-user-per-activity; refunded orders advance the suffix so a user
// can redeem again after a refund.
func newRedemptionKey(ctx context.Context, tx pgx.Tx, item string, userID int64, ref, requestKey string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		base := fmt.Sprintf("redeem:%s:%d:%s", item, userID, ref)
		var refunded int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM points_redemptions
			 WHERE user_id = $1 AND item = $2 AND ref_id = $3 AND refund_key IS NOT NULL`,
			userID, item, ref).Scan(&refunded); err != nil {
			return "", fmt.Errorf("db: count refunded redemptions: %w", err)
		}
		if refunded > 0 {
			return fmt.Sprintf("%s#%d", base, refunded+1), nil
		}
		return base, nil
	}
	token := sanitizePointsRequestKey(requestKey)
	if token == "" {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM points_redemptions WHERE user_id = $1 AND item = $2`,
			userID, item).Scan(&n); err != nil {
			return "", fmt.Errorf("db: count redemptions for key: %w", err)
		}
		token = fmt.Sprintf("seq%d", n+1)
	}
	return fmt.Sprintf("redeem:%s:%d:%s", item, userID, token), nil
}

func precheckRedemptionKey(ctx context.Context, tx pgx.Tx, key, item string) error {
	var status string
	err := tx.QueryRow(ctx,
		`SELECT status FROM points_redemptions WHERE idempotency_key = $1`, key).Scan(&status)
	if err == nil {
		return pointsError(ErrPointsAlreadyRedeemed,
			fmt.Sprintf("这笔兑换已经开过单了（%s，%s）。",
				RedeemItemLabels[item], RedeemStatusLabels[status]))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("db: precheck redemption key: %w", err)
	}
	return nil
}

func lockPointsActivity(ctx context.Context, tx pgx.Tx, activityID int64) error {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM activities WHERE id = $1 FOR UPDATE`, activityID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return pointsError(ErrPointsNotFound, "活动不存在。")
	}
	if err != nil {
		return fmt.Errorf("db: lock points activity: %w", err)
	}
	return nil
}

func requirePointsBalance(ctx context.Context, tx pgx.Tx, userID int64, item string, cost int) error {
	balance, err := pointsBalanceTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if balance < cost {
		return pointsError(ErrPointsInsufficient,
			fmt.Sprintf("麦穗不足：%s需要 %d 穗，目前只有 %d 穗。",
				RedeemItemLabels[item], cost, balance))
	}
	return nil
}

const pointsRedemptionColumns = `id, user_id, item, cost, status, params,
	idempotency_key, ledger_id, refund_ledger_id, refund_key, ref_id,
	created_at, resolved_at, note`

func pointsRedemptionFromRow(row rowScanner) (Redemption, error) {
	var (
		r      Redemption
		raw    []byte
		status string
	)
	err := row.Scan(&r.ID, &r.UserID, &r.Item, &r.Cost, &status, &raw,
		&r.IdempotencyKey, &r.LedgerID, &r.RefundLedgerID, &r.RefundKey, &r.RefID,
		&r.CreatedAt, &r.ResolvedAt, &r.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return Redemption{}, ErrNotFound
	}
	if err != nil {
		return Redemption{}, err
	}
	r.Status = status
	r.ItemLabel = RedeemItemLabels[r.Item]
	if r.ItemLabel == "" {
		r.ItemLabel = r.Item
	}
	r.StatusLabel = RedeemStatusLabels[r.Status]
	if r.StatusLabel == "" {
		r.StatusLabel = r.Status
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &r.Params); err != nil {
			r.Params = map[string]any{}
		}
	}
	if r.Params == nil {
		r.Params = map[string]any{}
	}
	return r, nil
}

func redemptionByIDTx(ctx context.Context, tx pgx.Tx, id int64) (Redemption, error) {
	return pointsRedemptionFromRow(tx.QueryRow(ctx,
		`SELECT `+pointsRedemptionColumns+` FROM points_redemptions WHERE id = $1`, id))
}

// RedeemPoints debits the ledger and creates the order in one transaction.
func (d *DB) RedeemPoints(ctx context.Context, userID int64, req RedeemRequest) (Redemption, error) {
	if _, ok := RedeemItemLabels[req.Item]; !ok {
		return Redemption{}, pointsError(ErrPointsUnavailable, "没有这个兑换项。")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Redemption{}, fmt.Errorf("db: begin redemption: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockPointsUser(ctx, tx, userID); err != nil {
		return Redemption{}, err
	}
	cfg, _, err := loadPointsConfig(ctx, tx)
	if err != nil {
		return Redemption{}, err
	}
	cost, err := pointsItemCost(cfg, req.Item)
	if err != nil {
		return Redemption{}, err
	}

	var (
		activity   *PointsActivity
		refID      string
		params     = map[string]any{}
		status     = RedeemStatusActive
		reason     string
		requestKey = req.RequestKey
		condition  = strings.TrimSpace(req.Condition)
	)
	switch req.Item {
	case RedeemItemPriority, RedeemItemWish:
		act, err := getPointsActivity(ctx, tx, req.ActivityID)
		if err != nil {
			return Redemption{}, err
		}
		activity = &act
		refID = strconv.FormatInt(act.ID, 10)
		params["activity_id"] = act.ID
		params["title"] = act.Name
	case RedeemItemMatchmaker:
		if condition == "" {
			return Redemption{}, pointsError(ErrPointsUnavailable, "请先写下你的条件和期望。")
		}
		if len([]rune(condition)) > cfg.MatchmakerConditionMax {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("条件最多 %d 个字，现在 %d 个。",
					cfg.MatchmakerConditionMax, len([]rune(condition))))
		}
	}
	if req.Item == RedeemItemWish {
		// Input errors come before balance errors: retrying after filling the
		// target should not suddenly reveal an unrelated insufficient balance.
		if req.TargetUserID <= 0 {
			return Redemption{}, pointsError(ErrPointsUnavailable, "请指定一位想认识的人。")
		}
		if req.TargetUserID == userID {
			return Redemption{}, pointsError(ErrPointsUnavailable, "不能把心愿指定给自己。")
		}
	}

	key, err := newRedemptionKey(ctx, tx, req.Item, userID, refID, requestKey)
	if err != nil {
		return Redemption{}, err
	}
	if err := precheckRedemptionKey(ctx, tx, key, req.Item); err != nil {
		return Redemption{}, err
	}
	if err := requirePointsBalance(ctx, tx, userID, req.Item, cost); err != nil {
		return Redemption{}, err
	}

	switch req.Item {
	case RedeemItemRealLike:
		reason = "兑换额外实名喜欢"

	case RedeemItemPriority:
		// Serialize the cap check across users. A per-user lock is not enough:
		// two different users could each read 2/3 and both take the last slot.
		if err := lockPointsActivity(ctx, tx, activity.ID); err != nil {
			return Redemption{}, err
		}
		if !activity.Open {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("「%s」现在不能使用优先名额。", activity.Name))
		}
		if activity.MaxSignup <= 0 {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("「%s」没有名额，优先名额用不了。", activity.Name))
		}
		cap := int(float64(activity.MaxSignup) * cfg.PriorityRatio)
		if cap < 1 {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("「%s」共 %d 个名额，优先名额上限不足 1 个。",
					activity.Name, activity.MaxSignup))
		}
		var taken int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM points_redemptions
			 WHERE item = $1 AND ref_id = $2 AND status = ANY($3)`,
			RedeemItemPriority, refID,
			[]string{RedeemStatusActive, RedeemStatusDone}).Scan(&taken); err != nil {
			return Redemption{}, fmt.Errorf("db: count priority redemptions: %w", err)
		}
		if taken >= cap {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("「%s」的优先名额已经满了（%d/%d）。", activity.Name, taken, cap))
		}
		reason = "兑换优先报名名额：" + activity.Name

	case RedeemItemWish:
		signed, err := isSignedUpTx(ctx, tx, userID, activity.ID)
		if err != nil {
			return Redemption{}, err
		}
		if !signed {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				fmt.Sprintf("先报名「%s」，才能在这场活动上许愿。", activity.Name))
		}
		target, err := getWishTarget(ctx, tx, userID, req.TargetUserID)
		if err != nil {
			return Redemption{}, err
		}
		params["target_user_id"] = target.ID
		params["target_user_id_text"] = target.UserID
		reason = "兑换心愿名额：" + activity.Name

	case RedeemItemMatchmaker:
		var open int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM points_redemptions
			 WHERE user_id = $1 AND item = $2 AND status = ANY($3)`,
			userID, RedeemItemMatchmaker,
			[]string{RedeemStatusPending, RedeemStatusRecommended}).Scan(&open); err != nil {
			return Redemption{}, fmt.Errorf("db: count open matchmaker orders: %w", err)
		}
		if open > 0 {
			return Redemption{}, pointsError(ErrPointsUnavailable,
				"你还有一张推荐单没完成，等处理完再兑。")
		}
		params["condition"] = condition
		status = RedeemStatusPending
		reason = "兑换红娘人工推荐"
	}

	entry, _, err := addPointsEntryTx(ctx, tx, AddPointsParams{
		UserID:         userID,
		Delta:          -cost,
		Kind:           PointsKindRedeem,
		Reason:         reason,
		RefType:        req.Item,
		RefID:          refID,
		OperatorID:     &userID,
		IdempotencyKey: key,
	})
	if err != nil {
		return Redemption{}, err
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return Redemption{}, fmt.Errorf("db: marshal redemption params: %w", err)
	}
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO points_redemptions
		   (user_id, item, cost, status, params, idempotency_key, ledger_id,
		    ref_id, created_at)
		 VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, now())
		 RETURNING id`,
		userID, req.Item, cost, status, string(rawParams), key, entry.ID, refID).Scan(&id)
	if err != nil {
		if isPointsUniqueViolation(err, "points_redemptions_idem_uidx") {
			return Redemption{}, pointsError(ErrPointsAlreadyRedeemed, "这笔兑换已经开过单了。")
		}
		return Redemption{}, fmt.Errorf("db: insert redemption: %w", err)
	}
	order, err := redemptionByIDTx(ctx, tx, id)
	if err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Redemption{}, fmt.Errorf("db: commit redemption: %w", err)
	}
	return order, nil
}

func isSignedUpTx(ctx context.Context, tx pgx.Tx, userID, activityID int64) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM signups
		   WHERE user_id = $1 AND activity_id = $2 AND status <> '已取消'
		 )`, userID, activityID).Scan(&exists); err != nil {
		return false, fmt.Errorf("db: check signup in tx: %w", err)
	}
	return exists, nil
}

// PointsRefundResult describes a successful (or skipped) refund.
type PointsRefundResult struct {
	Redemption    Redemption `json:"redemption"`
	RefundEntryID int64      `json:"refund_entry_id"`
	Balance       int        `json:"balance"`
}

// RefundRedemption returns the exact amount originally charged and updates the
// order. A terminal order is left untouched (nil result). Idempotency uses the
// order's refund_key unique index + ledger key, not a read-then-write check.
func (d *DB) RefundRedemption(ctx context.Context, redemptionID int64, reason, kind string, operatorID *int64, status string) (*PointsRefundResult, error) {
	if kind == "" {
		kind = PointsKindRefund
	}
	if status == "" {
		status = RedeemStatusRefunded
	}
	if _, ok := RedeemStatusLabels[status]; !ok {
		return nil, pointsError(ErrPointsUnavailable, "未知的兑换单状态。")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: begin redemption refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	order, err := pointsRedemptionFromRow(tx.QueryRow(ctx,
		`SELECT `+pointsRedemptionColumns+` FROM points_redemptions WHERE id = $1 FOR UPDATE`,
		redemptionID))
	if errors.Is(err, ErrNotFound) {
		return nil, pointsError(ErrPointsNotFound, "兑换单不存在。")
	}
	if err != nil {
		return nil, fmt.Errorf("db: load redemption for refund: %w", err)
	}
	if order.Status != RedeemStatusActive && order.Status != RedeemStatusPending &&
		order.Status != RedeemStatusRecommended {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("db: commit redemption refund: %w", err)
		}
		return nil, nil
	}
	if order.RefundKey != nil && *order.RefundKey != "" {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("db: commit redemption refund: %w", err)
		}
		return nil, nil
	}
	if order.LedgerID == nil {
		return nil, pointsError(ErrPointsUnavailable,
			"兑换单没有对应的扣穗流水，不能自动退还。")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "退还" + RedeemItemLabels[order.Item]
	}
	refundKey := fmt.Sprintf("refund:%d", order.ID)
	entry, balance, err := reversePointsEntryTx(ctx, tx, *order.LedgerID, kind, reason,
		operatorID, refundKey)
	if err != nil {
		if errors.Is(err, ErrPointsDuplicate) {
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("db: commit redemption refund: %w", err)
			}
			return nil, nil
		}
		return nil, err
	}
	_, err = tx.Exec(ctx,
		`UPDATE points_redemptions
		 SET status = $2, refund_key = $3, refund_ledger_id = $4,
		     resolved_at = now(), note = $5
		 WHERE id = $1`,
		order.ID, status, refundKey, entry.ID, reason)
	if err != nil {
		return nil, fmt.Errorf("db: update redemption refund: %w", err)
	}
	updated, err := redemptionByIDTx(ctx, tx, order.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("db: commit redemption refund: %w", err)
	}
	return &PointsRefundResult{Redemption: updated, RefundEntryID: entry.ID, Balance: balance}, nil
}

// MarkRedemption changes status without touching points. Refunds must go
// through RefundRedemption so there is only one path that credits points.
func (d *DB) MarkRedemption(ctx context.Context, redemptionID int64, status, note string) (Redemption, error) {
	if _, ok := RedeemStatusLabels[status]; !ok {
		return Redemption{}, pointsError(ErrPointsUnavailable, "未知的兑换单状态。")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Redemption{}, fmt.Errorf("db: begin redemption update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	order, err := pointsRedemptionFromRow(tx.QueryRow(ctx,
		`SELECT `+pointsRedemptionColumns+` FROM points_redemptions WHERE id = $1 FOR UPDATE`,
		redemptionID))
	if errors.Is(err, ErrNotFound) {
		return Redemption{}, pointsError(ErrPointsNotFound, "兑换单不存在。")
	}
	if err != nil {
		return Redemption{}, fmt.Errorf("db: load redemption for update: %w", err)
	}
	if strings.TrimSpace(note) == "" {
		note = order.Note
	}
	if _, err := tx.Exec(ctx,
		`UPDATE points_redemptions SET status = $2, note = $3, resolved_at = now()
		 WHERE id = $1`, order.ID, status, note); err != nil {
		return Redemption{}, fmt.Errorf("db: update redemption status: %w", err)
	}
	updated, err := redemptionByIDTx(ctx, tx, order.ID)
	if err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Redemption{}, fmt.Errorf("db: commit redemption update: %w", err)
	}
	return updated, nil
}

// GetRedemption loads one order.
func (d *DB) GetRedemption(ctx context.Context, id int64) (Redemption, error) {
	order, err := pointsRedemptionFromRow(d.pool.QueryRow(ctx,
		`SELECT `+pointsRedemptionColumns+` FROM points_redemptions WHERE id = $1`, id))
	if errors.Is(err, ErrNotFound) {
		return Redemption{}, pointsError(ErrPointsNotFound, "兑换单不存在。")
	}
	if err != nil {
		return Redemption{}, fmt.Errorf("db: get redemption: %w", err)
	}
	return order, nil
}

// ListRedemptionsFor returns only the caller's orders, newest first.
func (d *DB) ListRedemptionsFor(ctx context.Context, userID int64, limit int, item string) ([]Redemption, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sql := `SELECT ` + pointsRedemptionColumns + ` FROM points_redemptions WHERE user_id = $1`
	args := []any{userID}
	if strings.TrimSpace(item) != "" {
		sql += ` AND item = $2`
		args = append(args, item)
	}
	sql += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list redemptions: %w", err)
	}
	defer rows.Close()
	out := make([]Redemption, 0)
	for rows.Next() {
		order, err := pointsRedemptionFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan redemption: %w", err)
		}
		out = append(out, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate redemptions: %w", err)
	}
	return out, nil
}

// ExtraRealLikeQuota is the number of active real_like orders. It plugs
// directly into ComputeLikeQuota's permanent argument.
func (d *DB) ExtraRealLikeQuota(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM points_redemptions
		 WHERE user_id = $1 AND item = $2 AND status = $3`,
		userID, RedeemItemRealLike, RedeemStatusActive).Scan(&n); err != nil {
		return 0, fmt.Errorf("db: extra real like quota: %w", err)
	}
	return n, nil
}

// PointsLikeQuota computes the social like allowance with redeemed permanent
// slots included. It is the recommended integration point for the social
// module; social files are intentionally not modified here.
func (d *DB) PointsLikeQuota(ctx context.Context, userID int64, now time.Time) (LikeQuota, error) {
	extra, err := d.ExtraRealLikeQuota(ctx, userID)
	if err != nil {
		return LikeQuota{}, err
	}
	records, err := d.listLikeQuotaRecords(ctx, userID)
	if err != nil {
		return LikeQuota{}, err
	}
	return ComputeLikeQuota(records, extra, MonthKey(now)), nil
}

// PriorityAvailability returns remaining priority slots for an activity.
func (d *DB) PriorityAvailability(ctx context.Context, activityKey string) (int, error) {
	act, err := d.GetPointsActivity(ctx, activityKey)
	if err != nil {
		return 0, err
	}
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return 0, err
	}
	cap := int(float64(act.MaxSignup) * cfg.PriorityRatio)
	if cap < 0 {
		cap = 0
	}
	var taken int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM points_redemptions
		 WHERE item = $1 AND ref_id = $2 AND status = ANY($3)`,
		RedeemItemPriority, fmt.Sprintf("%d", act.ID),
		[]string{RedeemStatusActive, RedeemStatusDone}).Scan(&taken); err != nil {
		return 0, fmt.Errorf("db: priority availability: %w", err)
	}
	if taken >= cap {
		return 0, nil
	}
	return cap - taken, nil
}

// CanCancelPriority ports points_redeem.can_cancel_priority: a user cancelling
// at least priority_cancel_hours before the activity start gets a refund.
func CanCancelPriority(cfg PointsConfig, hoursToStart *float64) bool {
	if hoursToStart == nil {
		return false
	}
	return *hoursToStart >= float64(cfg.PriorityCancelHours)
}

// PriorityCancelResult reports what happened when a signup was cancelled.
type PriorityCancelResult struct {
	Order    Redemption `json:"order"`
	Refunded bool       `json:"refunded"`
	Points   int        `json:"points"`
	Balance  int        `json:"balance"`
}

// CancelPriorityOnSignupCancel ports _refund_priority_on_cancel: refund only
// when the 48-hour rule passes; otherwise mark the order cancelled without a
// refund. Returns nil when there is no active priority order.
func (d *DB) CancelPriorityOnSignupCancel(ctx context.Context, userID int64, activityKey string, hoursToStart *float64, note string) (*PriorityCancelResult, error) {
	act, err := d.GetPointsActivity(ctx, activityKey)
	if err != nil {
		return nil, err
	}
	ref := fmt.Sprintf("%d", act.ID)
	var id int64
	err = d.pool.QueryRow(ctx,
		`SELECT id FROM points_redemptions
		 WHERE user_id = $1 AND item = $2 AND ref_id = $3
		 ORDER BY id DESC LIMIT 1`,
		userID, RedeemItemPriority, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: find priority order: %w", err)
	}
	order, err := d.GetRedemption(ctx, id)
	if err != nil {
		return nil, err
	}
	if order.Status != RedeemStatusActive {
		return nil, nil
	}
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return nil, err
	}
	if CanCancelPriority(cfg, hoursToStart) {
		reason := strings.TrimSpace(note)
		if reason == "" {
			reason = "活动开始 48 小时前主动取消报名"
		}
		res, err := d.RefundRedemption(ctx, order.ID, reason, PointsKindRefund, &userID, RedeemStatusRefunded)
		if err != nil || res == nil {
			return nil, err
		}
		return &PriorityCancelResult{
			Order:    res.Redemption,
			Refunded: true,
			Points:   res.Redemption.Cost,
			Balance:  res.Balance,
		}, nil
	}
	updated, err := d.MarkRedemption(ctx, order.ID, RedeemStatusCancelled, "活动开始前 48 小时内取消，不退穗")
	if err != nil {
		return nil, err
	}
	return &PriorityCancelResult{Order: updated}, nil
}

// MatchmakerOverdue returns pending matchmaker orders past the configured
// handling deadline. Recommended orders are excluded: the deadline is about
// the matchmaker doing work, not the user getting married.
func (d *DB) MatchmakerOverdue(ctx context.Context, now time.Time) ([]Redemption, error) {
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-time.Duration(cfg.MatchmakerDeadline) * 24 * time.Hour)
	rows, err := d.pool.Query(ctx,
		`SELECT `+pointsRedemptionColumns+` FROM points_redemptions
		 WHERE item = $1 AND status = $2 AND created_at <= $3 ORDER BY id`,
		RedeemItemMatchmaker, RedeemStatusPending, cutoff)
	if err != nil {
		return nil, fmt.Errorf("db: list overdue matchmaker orders: %w", err)
	}
	defer rows.Close()
	out := make([]Redemption, 0)
	for rows.Next() {
		order, err := pointsRedemptionFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan overdue matchmaker order: %w", err)
		}
		out = append(out, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate overdue matchmaker orders: %w", err)
	}
	return out, nil
}
