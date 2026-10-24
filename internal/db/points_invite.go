package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Invite lifecycle. These strings are persisted; only add, never rename.
const (
	InviteStatusPending    = "pending"     // phone registered, invitee not active yet
	InviteStatusConfirmed  = "confirmed"   // active account, 7-day confirmation running
	InviteStatusFinalized  = "finalized"   // due date passed, reward credited
	InviteStatusClawedBack = "clawed_back" // banned during confirmation window
	InviteStatusRejected   = "rejected"    // self-invite / invalid at registration
)

var inviteStatusLabels = map[string]string{
	InviteStatusPending:    "待确认",
	InviteStatusConfirmed:  "待确认",
	InviteStatusFinalized:  "已到账",
	InviteStatusClawedBack: "已收回",
	InviteStatusRejected:   "无效邀请",
}

// Invite is one row of the invites table.
type Invite struct {
	ID             int64      `json:"id"`
	InviterID      int64      `json:"inviter_id"`
	InviteePhone   string     `json:"-"`
	InviteeID      *int64     `json:"invitee_id,omitempty"`
	Status         string     `json:"status"`
	StatusLabel    string     `json:"status_label"`
	Gender         string     `json:"gender"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	BoundAt        *time.Time `json:"bound_at,omitempty"`
	ConfirmDueAt   *time.Time `json:"confirm_due_at,omitempty"`
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	RewardLedgerID *int64     `json:"reward_ledger_id,omitempty"`
	Note           string     `json:"-"`
}

// InviteView is the safe user-facing projection: phone is masked and the note
// is omitted because it can mention another account's identifier.
type InviteView struct {
	ID           int64      `json:"id"`
	PhoneMasked  string     `json:"phone_masked"`
	Status       string     `json:"status"`
	StatusLabel  string     `json:"status_label"`
	Gender       string     `json:"gender"`
	Reward       int        `json:"reward"`
	CreatedAt    time.Time  `json:"created_at"`
	ConfirmDueAt *time.Time `json:"confirm_due_at,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
}

// InviteProgress mirrors lib/points_invite.progress.
type InviteProgress struct {
	Total           int `json:"total"`
	Pending         int `json:"pending"`
	Confirmed       int `json:"confirmed"`
	Finalized       int `json:"finalized"`
	ClawedBack      int `json:"clawed_back"`
	Rejected        int `json:"rejected"`
	PendingPoints   int `json:"pending_points"`
	FinalizedPoints int `json:"finalized_points"`
}

// HistoricalParticipantInput is one imported old-activity participant.
type HistoricalParticipantInput struct {
	Phone string
	Name  string
}

var pointsPhoneNonDigit = regexp.MustCompile(`\D`)

// NormalizePointsPhone ports points_invite.normalize_phone exactly: strip
// non-digits, strip +86/0086, require an 11-digit mainland mobile number.
func NormalizePointsPhone(raw string) string {
	s := pointsPhoneNonDigit.ReplaceAllString(raw, "")
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "0086") {
		s = s[4:]
	} else if strings.HasPrefix(s, "86") && len(s) > 11 {
		s = s[2:]
	}
	if len(s) != 11 || !strings.HasPrefix(s, "1") {
		return ""
	}
	return s
}

func pointsPhoneLast4(phone string) string {
	if len(phone) < 4 {
		return "****"
	}
	return phone[len(phone)-4:]
}

func maskPointsPhone(phone string) string {
	if len(phone) != 11 {
		return ""
	}
	return phone[:3] + "****" + phone[7:]
}

func pointsInviteFromRow(row rowScanner) (Invite, error) {
	var inv Invite
	err := row.Scan(&inv.ID, &inv.InviterID, &inv.InviteePhone, &inv.InviteeID,
		&inv.Status, &inv.Gender, &inv.Source, &inv.CreatedAt, &inv.UpdatedAt,
		&inv.BoundAt, &inv.ConfirmDueAt, &inv.ConfirmedAt, &inv.RewardLedgerID, &inv.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrNotFound
	}
	if err != nil {
		return Invite{}, err
	}
	inv.StatusLabel = inviteStatusLabels[inv.Status]
	if inv.StatusLabel == "" {
		inv.StatusLabel = inv.Status
	}
	return inv, nil
}

const pointsInviteColumns = `id, inviter_id, invitee_phone, invitee_id, status, gender,
	source, created_at, updated_at, bound_at, confirm_due_at, confirmed_at,
	reward_ledger_id, note`

// InviteRewardAmount returns 20 for female invitees and 15 for everyone else.
// Missing/unrecognised gender deliberately pays the lower amount: overpaying
// is much harder to reverse after the points have been spent.
func InviteRewardAmount(cfg PointsConfig, gender string) int {
	if strings.TrimSpace(gender) == "女性" {
		return cfg.InviteRewardFemale
	}
	return cfg.InviteRewardMale
}

// IsHistoricalParticipant reports whether a phone attended events before v2.
func (d *DB) IsHistoricalParticipant(ctx context.Context, phone string) (bool, error) {
	phone = NormalizePointsPhone(phone)
	if phone == "" {
		return false, nil
	}
	var exists bool
	if err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM historical_participants WHERE phone = $1)`,
		phone).Scan(&exists); err != nil {
		return false, fmt.Errorf("db: check historical participant: %w", err)
	}
	return exists, nil
}

// AddHistoricalParticipants imports old participants idempotently. Invalid
// phone numbers count as skipped instead of aborting a large import.
func (d *DB) AddHistoricalParticipants(ctx context.Context, rows []HistoricalParticipantInput, source string) (added, skipped int, err error) {
	if len(rows) == 0 {
		return 0, 0, nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("db: begin historical participants: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, row := range rows {
		phone := NormalizePointsPhone(row.Phone)
		if phone == "" {
			skipped++
			continue
		}
		tag, err := tx.Exec(ctx,
			`INSERT INTO historical_participants (phone, name, source, added_at)
			 VALUES ($1, $2, $3, now()) ON CONFLICT (phone) DO NOTHING`,
			phone, strings.TrimSpace(row.Name), strings.TrimSpace(source))
		if err != nil {
			return added, skipped, fmt.Errorf("db: insert historical participant: %w", err)
		}
		if tag.RowsAffected() == 0 {
			skipped++
		} else {
			added++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return added, skipped, fmt.Errorf("db: commit historical participants: %w", err)
	}
	return added, skipped, nil
}

// HistoricalParticipantCount returns the imported list size.
func (d *DB) HistoricalParticipantCount(ctx context.Context) (int, error) {
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM historical_participants`).Scan(&n); err != nil {
		return 0, fmt.Errorf("db: count historical participants: %w", err)
	}
	return n, nil
}

// RecordInviteFromForm registers the invitee's phone against the inviter. It
// is called from the registration flow (not exposed as a user API): the person
// is already filling in the form, so only the "one invitee, one inviter" rule
// needs enforcing here.
func (d *DB) RecordInviteFromForm(ctx context.Context, inviteeID int64, phone, gender string, inviterID int64, source string) (int64, string, error) {
	phone = NormalizePointsPhone(phone)
	if phone == "" || inviterID <= 0 || inviteeID <= 0 {
		return 0, "", nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("db: begin invite registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, lookupErr := findInviteForInvitee(ctx, tx, phone, inviteeID)
	switch {
	case lookupErr != nil:
		return 0, "", lookupErr
	case existing.ID != 0:
		if existing.InviterID != inviterID && existing.Status == InviteStatusPending {
			_, err = tx.Exec(ctx,
				`UPDATE invites SET note = $2, updated_at = now() WHERE id = $1`,
				existing.ID, "注册时填的邀请人与已绑定的不一致，以先绑定的为准")
			if err != nil {
				return 0, "", fmt.Errorf("db: note invite conflict: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, "", fmt.Errorf("db: commit invite registration: %w", err)
		}
		return existing.ID, existing.Status, nil
	}

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, invitee_id, status, gender,
		                      source, created_at, updated_at, bound_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now(), now(), now())
		 RETURNING id`,
		inviterID, phone, inviteeID, InviteStatusPending, strings.TrimSpace(gender),
		strings.TrimSpace(source)).Scan(&id)
	if err != nil {
		// Concurrent registration for the same phone/account: return the row
		// that won instead of surfacing a constraint error to the caller.
		if isPointsUniqueViolation(err, "") {
			_ = tx.Rollback(ctx)
			existing, lookupErr := d.findInviteByPhoneOrInvitee(ctx, phone, inviteeID)
			if lookupErr == nil && existing.ID != 0 {
				return existing.ID, existing.Status, nil
			}
		}
		return 0, "", fmt.Errorf("db: insert invite: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", fmt.Errorf("db: commit invite registration: %w", err)
	}
	return id, InviteStatusPending, nil
}

func (d *DB) findInviteByPhoneOrInvitee(ctx context.Context, phone string, inviteeID int64) (Invite, error) {
	return findInviteForInvitee(ctx, d.pool, phone, inviteeID)
}

type pointsInviteQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func findInviteForInvitee(ctx context.Context, q pointsInviteQuerier, phone string, inviteeID int64) (Invite, error) {
	if phone != "" {
		inv, err := pointsInviteFromRow(q.QueryRow(ctx,
			`SELECT `+pointsInviteColumns+` FROM invites WHERE invitee_phone = $1`, phone))
		if err == nil {
			return inv, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Invite{}, fmt.Errorf("db: find invite by phone: %w", err)
		}
	}
	if inviteeID > 0 {
		inv, err := pointsInviteFromRow(q.QueryRow(ctx,
			`SELECT `+pointsInviteColumns+` FROM invites WHERE invitee_id = $1`, inviteeID))
		if err == nil {
			return inv, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Invite{}, fmt.Errorf("db: find invite by invitee: %w", err)
		}
	}
	return Invite{}, nil
}

// StartInviteConfirmWindow starts the 7-day clock when the invitee becomes an
// active single user. The due date is fixed at this moment so a later config
// change cannot move an already-running window.
func (d *DB) StartInviteConfirmWindow(ctx context.Context, inviteeID int64, phone, gender string) (int64, string, error) {
	if inviteeID <= 0 {
		return 0, "", nil
	}
	phone = NormalizePointsPhone(phone)
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("db: begin invite confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := findInviteForInvitee(ctx, tx, phone, inviteeID)
	if err != nil {
		return 0, "", err
	}
	if inv.ID == 0 {
		return 0, "", nil
	}
	if inv.InviterID == inviteeID {
		_, err = tx.Exec(ctx,
			`UPDATE invites SET status = $2, invitee_id = $3, note = $4, updated_at = now()
			 WHERE id = $1`, inv.ID, InviteStatusRejected, inviteeID, "自己邀请自己")
		if err != nil {
			return 0, "", fmt.Errorf("db: reject self invite: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, "", fmt.Errorf("db: commit self invite rejection: %w", err)
		}
		return inv.ID, InviteStatusRejected, nil
	}
	if inv.Status != InviteStatusPending {
		if err := tx.Commit(ctx); err != nil {
			return 0, "", fmt.Errorf("db: commit invite confirmation: %w", err)
		}
		return inv.ID, inv.Status, nil
	}
	cfg, _, err := loadPointsConfig(ctx, tx)
	if err != nil {
		return 0, "", err
	}
	due := PointsNow().Add(time.Duration(cfg.InviteConfirmDays) * 24 * time.Hour)
	if gender == "" {
		gender = inv.Gender
	}
	_, err = tx.Exec(ctx,
		`UPDATE invites
		 SET status = $2, invitee_id = $3, gender = $4, confirmed_at = now(),
		     confirm_due_at = $5, updated_at = now(), bound_at = COALESCE(bound_at, now())
		 WHERE id = $1`,
		inv.ID, InviteStatusConfirmed, inviteeID, strings.TrimSpace(gender), due)
	if err != nil {
		if isPointsUniqueViolation(err, "invites_invitee_uidx") {
			return 0, "", pointsError(ErrPointsUnavailable, "这个账号已绑定其他邀请记录。")
		}
		return 0, "", fmt.Errorf("db: start invite confirmation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", fmt.Errorf("db: commit invite confirmation: %w", err)
	}
	return inv.ID, InviteStatusConfirmed, nil
}

// StartInviteConfirmWindowForUser starts the window for an invitee identified
// by user id, loading the phone and gender from the account. Approval handlers
// use it; the call is idempotent so retries and backfills are safe.
func (d *DB) StartInviteConfirmWindowForUser(ctx context.Context, inviteeID int64) (int64, string, error) {
	if inviteeID <= 0 {
		return 0, "", nil
	}
	var phone, gender string
	err := d.pool.QueryRow(ctx,
		`SELECT phone, gender FROM users WHERE id = $1`, inviteeID).Scan(&phone, &gender)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("db: load invitee for confirmation: %w", err)
	}
	return d.StartInviteConfirmWindow(ctx, inviteeID, phone, gender)
}

// StartPendingInviteWindows backfills invites recorded before the approval
// hook existed (or whose window start failed). Only approved, active singles
// are eligible; everything else waits for the approval path.
func (d *DB) StartPendingInviteWindows(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx,
		`SELECT i.invitee_id, u.phone, u.gender
		   FROM invites i
		   JOIN users u ON (i.invitee_id = u.id OR (i.invitee_id IS NULL AND u.phone = i.invitee_phone))
		  WHERE i.status = $1 AND u.status = 'active' AND u.account_status = 'active'
		  ORDER BY i.id LIMIT $2`,
		InviteStatusPending, limit)
	if err != nil {
		return 0, fmt.Errorf("db: list pending invite windows: %w", err)
	}
	type pendingInvite struct {
		inviteeID int64
		phone     string
		gender    string
	}
	items := make([]pendingInvite, 0, limit)
	for rows.Next() {
		var item pendingInvite
		if err := rows.Scan(&item.inviteeID, &item.phone, &item.gender); err != nil {
			rows.Close()
			return 0, fmt.Errorf("db: scan pending invite window: %w", err)
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("db: iterate pending invite windows: %w", err)
	}
	started := 0
	var firstErr error
	for _, item := range items {
		_, status, err := d.StartInviteConfirmWindow(ctx, item.inviteeID, item.phone, item.gender)
		if err != nil {
			// One malformed invite must not block the rest of the batch.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if status == InviteStatusConfirmed {
			started++
		}
	}
	return started, firstErr
}

// pointsInviteBanned treats an explicit administrator disable as the v2
// equivalent of the old "封禁" status. "left" is deliberately not included:
// it is a voluntary pause and withdrawing an invite reward for it would be
// surprising and hard to explain.
func pointsInviteBanned(loginStatus, accountStatus string) bool {
	return loginStatus == "disabled" || accountStatus == "disabled"
}

// SettleInvite finalizes one due invite. Reward insertion and the status change
// happen in the same transaction; the reward key makes retries harmless.
func (d *DB) SettleInvite(ctx context.Context, inviteID int64) (string, *int64, error) {
	status, entryID, err := d.settleInvite(ctx, inviteID, PointsNow())
	return status, entryID, err
}

func (d *DB) settleInvite(ctx context.Context, inviteID int64, now time.Time) (string, *int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("db: begin invite settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := pointsInviteFromRow(tx.QueryRow(ctx,
		`SELECT `+pointsInviteColumns+` FROM invites WHERE id = $1 FOR UPDATE`, inviteID))
	if errors.Is(err, ErrNotFound) {
		return "", nil, pointsError(ErrPointsNotFound, "邀请记录不存在。")
	}
	if err != nil {
		return "", nil, fmt.Errorf("db: load invite for settlement: %w", err)
	}
	if inv.Status != InviteStatusConfirmed {
		if err := tx.Commit(ctx); err != nil {
			return "", nil, fmt.Errorf("db: commit invite settlement: %w", err)
		}
		return inv.Status, nil, nil
	}

	banned := false
	if inv.InviteeID != nil {
		var loginStatus, accountStatus string
		err = tx.QueryRow(ctx,
			`SELECT status, account_status FROM users WHERE id = $1`, *inv.InviteeID).
			Scan(&loginStatus, &accountStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", nil, fmt.Errorf("db: load invitee status: %w", err)
		}
		banned = pointsInviteBanned(loginStatus, accountStatus)
	}
	if banned {
		_, err = tx.Exec(ctx,
			`UPDATE invites SET status = $2, note = $3, updated_at = now() WHERE id = $1`,
			inviteID, InviteStatusClawedBack, "确认期内被封禁")
		if err != nil {
			return "", nil, fmt.Errorf("db: claw back invite: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", nil, fmt.Errorf("db: commit invite clawback: %w", err)
		}
		return InviteStatusClawedBack, nil, nil
	}
	// Never award early: the reverse direction (clawing back spent points) is
	// not reliably possible once the invitee has redeemed them.
	if inv.ConfirmDueAt != nil && inv.ConfirmDueAt.After(now) {
		return "", nil, pointsError(ErrPointsInviteDue,
			"确认期还没期满，现在不能发放邀请奖励。")
	}
	cfg, _, err := loadPointsConfig(ctx, tx)
	if err != nil {
		return "", nil, err
	}
	amount := InviteRewardAmount(cfg, inv.Gender)
	key := fmt.Sprintf("invite_reward:%d", inviteID)
	var entryID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM points_ledger WHERE idempotency_key = $1`, key).Scan(&entryID)
	switch {
	case err == nil:
		// Already granted by an earlier run; just finish the status update.
	case errors.Is(err, pgx.ErrNoRows):
		entry, _, addErr := addPointsEntryTx(ctx, tx, AddPointsParams{
			UserID:         inv.InviterID,
			Delta:          amount,
			Kind:           PointsKindInvite,
			Reason:         fmt.Sprintf("邀请好友注册（%s）", pointsPhoneLast4(inv.InviteePhone)),
			RefType:        "invite",
			RefID:          fmt.Sprintf("%d", inviteID),
			IdempotencyKey: key,
		})
		if addErr != nil {
			if errors.Is(addErr, ErrPointsDuplicate) {
				if qerr := tx.QueryRow(ctx,
					`SELECT id FROM points_ledger WHERE idempotency_key = $1`, key).
					Scan(&entryID); qerr != nil {
					return "", nil, fmt.Errorf("db: reload invite reward: %w", qerr)
				}
			} else {
				return "", nil, addErr
			}
		} else {
			entryID = entry.ID
		}
	default:
		return "", nil, fmt.Errorf("db: check invite reward: %w", err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE invites SET status = $2, reward_ledger_id = $3, updated_at = now()
		 WHERE id = $1`, inviteID, InviteStatusFinalized, entryID)
	if err != nil {
		return "", nil, fmt.Errorf("db: finalize invite: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("db: commit invite settlement: %w", err)
	}
	return InviteStatusFinalized, &entryID, nil
}

// SettleDueInvites scans finalizable invites. It is safe to call on request
// paths and from a future job: each invite is locked and idempotent.
func (d *DB) SettleDueInvites(ctx context.Context, limit int) (finalized, clawedBack int, err error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	now := PointsNow()
	rows, err := d.pool.Query(ctx,
		`SELECT id FROM invites
		 WHERE status = $1 AND confirm_due_at IS NOT NULL AND confirm_due_at <= $2
		 ORDER BY id LIMIT $3`,
		InviteStatusConfirmed, now, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("db: list due invites: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("db: scan due invite: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("db: iterate due invites: %w", err)
	}
	for _, id := range ids {
		status, _, settleErr := d.settleInvite(ctx, id, now)
		if settleErr != nil {
			if errors.Is(settleErr, ErrPointsInviteDue) {
				continue
			}
			return finalized, clawedBack, settleErr
		}
		switch status {
		case InviteStatusFinalized:
			finalized++
		case InviteStatusClawedBack:
			clawedBack++
		}
	}
	return finalized, clawedBack, nil
}

// SettleDueInvitesForInviter is the user-scoped variant used by points pages:
// opening one's own points settles that user's due invites without touching
// other users' ledgers.
func (d *DB) SettleDueInvitesForInviter(ctx context.Context, inviterID int64, limit int) (finalized, clawedBack int, err error) {
	if inviterID <= 0 {
		return 0, 0, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	now := PointsNow()
	rows, err := d.pool.Query(ctx,
		`SELECT id FROM invites
		 WHERE inviter_id = $1 AND status = $2 AND confirm_due_at IS NOT NULL
		   AND confirm_due_at <= $3
		 ORDER BY id LIMIT $4`,
		inviterID, InviteStatusConfirmed, now, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("db: list due invites for inviter: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("db: scan due invite for inviter: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("db: iterate due invites for inviter: %w", err)
	}
	for _, id := range ids {
		status, _, settleErr := d.settleInvite(ctx, id, now)
		if settleErr != nil {
			if errors.Is(settleErr, ErrPointsInviteDue) {
				continue
			}
			return finalized, clawedBack, settleErr
		}
		switch status {
		case InviteStatusFinalized:
			finalized++
		case InviteStatusClawedBack:
			clawedBack++
		}
	}
	return finalized, clawedBack, nil
}

// ClawBackInvitesForInvitee cancels invites still inside the confirmation
// window when the invitee is banned. Already-finalized rewards are untouched:
// the clawback window is exactly those 7 days.
func (d *DB) ClawBackInvitesForInvitee(ctx context.Context, inviteeID int64, reason string) (int, error) {
	if inviteeID <= 0 {
		return 0, nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "确认期内被封禁"
	}
	tag, err := d.pool.Exec(ctx,
		`UPDATE invites SET status = $2, note = $3, updated_at = now()
		 WHERE invitee_id = $1 AND status = $4`,
		inviteeID, InviteStatusClawedBack, reason, InviteStatusConfirmed)
	if err != nil {
		return 0, fmt.Errorf("db: claw back invitee invites: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// OpenInvitesForInvitee returns invites still in the confirmation window. Used
// to notify the inviter if the invitee is banned.
func (d *DB) OpenInvitesForInvitee(ctx context.Context, inviteeID int64) ([]Invite, error) {
	if inviteeID <= 0 {
		return nil, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT `+pointsInviteColumns+` FROM invites
		 WHERE invitee_id = $1 AND status = $2 ORDER BY id`,
		inviteeID, InviteStatusConfirmed)
	if err != nil {
		return nil, fmt.Errorf("db: list open invites for invitee: %w", err)
	}
	defer rows.Close()
	out := make([]Invite, 0)
	for rows.Next() {
		inv, err := pointsInviteFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan open invite: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate open invites: %w", err)
	}
	return out, nil
}

// InviteProgress aggregates statuses and pending/finalized point totals.
func (d *DB) InviteProgress(ctx context.Context, inviterID int64) (InviteProgress, error) {
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return InviteProgress{}, err
	}
	rows, err := d.pool.Query(ctx,
		`SELECT status, gender, count(*) FROM invites
		 WHERE inviter_id = $1 GROUP BY status, gender`, inviterID)
	if err != nil {
		return InviteProgress{}, fmt.Errorf("db: invite progress: %w", err)
	}
	defer rows.Close()
	var out InviteProgress
	for rows.Next() {
		var status, gender string
		var n int
		if err := rows.Scan(&status, &gender, &n); err != nil {
			return InviteProgress{}, fmt.Errorf("db: scan invite progress: %w", err)
		}
		out.Total += n
		switch status {
		case InviteStatusPending:
			out.Pending += n
		case InviteStatusConfirmed:
			out.Confirmed += n
			out.PendingPoints += InviteRewardAmount(cfg, gender) * n
		case InviteStatusFinalized:
			out.Finalized += n
			out.FinalizedPoints += InviteRewardAmount(cfg, gender) * n
		case InviteStatusClawedBack:
			out.ClawedBack += n
		case InviteStatusRejected:
			out.Rejected += n
		}
	}
	if err := rows.Err(); err != nil {
		return InviteProgress{}, fmt.Errorf("db: iterate invite progress: %w", err)
	}
	return out, nil
}

// ListInvitesFor returns safe masked rows for "my invites".
func (d *DB) ListInvitesFor(ctx context.Context, inviterID int64, limit int) ([]InviteView, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	cfg, err := d.GetPointsConfig(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.pool.Query(ctx,
		`SELECT `+pointsInviteColumns+` FROM invites
		 WHERE inviter_id = $1 ORDER BY id DESC LIMIT $2`, inviterID, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list invites: %w", err)
	}
	defer rows.Close()
	out := make([]InviteView, 0)
	for rows.Next() {
		inv, err := pointsInviteFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan invite: %w", err)
		}
		reward := 0
		if inv.Status == InviteStatusConfirmed || inv.Status == InviteStatusFinalized {
			reward = InviteRewardAmount(cfg, inv.Gender)
		}
		statusLabel := inviteStatusLabels[inv.Status]
		if statusLabel == "" {
			statusLabel = inv.Status
		}
		out = append(out, InviteView{
			ID:           inv.ID,
			PhoneMasked:  maskPointsPhone(inv.InviteePhone),
			Status:       inv.Status,
			StatusLabel:  statusLabel,
			Gender:       inv.Gender,
			Reward:       reward,
			CreatedAt:    inv.CreatedAt,
			ConfirmDueAt: inv.ConfirmDueAt,
			ConfirmedAt:  inv.ConfirmedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate invites: %w", err)
	}
	return out, nil
}
