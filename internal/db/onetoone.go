package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/matching"
	"github.com/jackc/pgx/v5"
)

// One-to-one status values mirrored from the legacy select options.
const (
	OnetooneStatusDone = "已完成"
)

var (
	// ErrOnetooneRoundExists is the idempotency guard: rerunning a round would
	// overwrite a completed result set, so it is refused instead.
	ErrOnetooneRoundExists = errors.New("db: onetoone round already exists")
	// ErrOnetooneNoParticipants means the signup pool (after the optional
	// on-site check-in filter) contains no valid male/female participants.
	ErrOnetooneNoParticipants = errors.New("db: no onetoone participants")
	// ErrOnetooneGroupMode blocks one-to-one while the activity is in grouping
	// mode; the two modes are mutually exclusive.
	ErrOnetooneGroupMode = errors.New("db: group mode is active")
	// ErrOnetooneFemaleChats wraps invalid K values (including K > male count).
	ErrOnetooneFemaleChats = errors.New("db: invalid female chats")
)

// OnetooneParticipant is one signed-up participant with the profile fields the
// matching run needs. The sign-in state comes from the signup row.
type OnetooneParticipant struct {
	UserID         int64
	LegacyUserID   string
	LegacyOpenID   string
	Name           string
	Nickname       string
	Gender         string
	SignupNickname string
	Attendance     string
}

// DisplayName follows the legacy on-site rule: real name first, then nickname,
// then the signup nickname.
func (p OnetooneParticipant) DisplayName() string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(p.Nickname); name != "" {
		return name
	}
	return strings.TrimSpace(p.SignupNickname)
}

// OnetooneSelection is one submitted volunteer list. Choices preserve the
// volunteer order; the matching package derives priority 1..N.
type OnetooneSelection struct {
	SelectorID int64
	Gender     string
	Round      int
	Choices    []int64
}

// OnetooneResult is one stored result row enriched with current display data.
type OnetooneResult struct {
	ID             int64
	ActivityID     int64
	Round          int
	Rank           int
	UserID         int64
	UserName       string
	Gender         string
	TargetID       int64
	TargetName     string
	UserRealName   string
	UserNickname   string
	UserLegacyID   string
	UserOpenID     string
	TargetRealName string
	TargetNickname string
	TargetLegacyID string
	TargetOpenID   string
	TargetPhotoID  string
	CreatedAt      time.Time
}

// TargetDisplayName prefers the user's current nickname, then real name, then
// the name snapshotted into the result row.
func (r OnetooneResult) TargetDisplayName() string {
	if name := strings.TrimSpace(r.TargetNickname); name != "" {
		return name
	}
	if name := strings.TrimSpace(r.TargetRealName); name != "" {
		return name
	}
	if name := strings.TrimSpace(r.TargetName); name != "" {
		return name
	}
	return strconv.FormatInt(r.TargetID, 10)
}

// UserDisplayName is the admin-facing real-name-first display.
func (r OnetooneResult) UserDisplayName() string {
	if name := strings.TrimSpace(r.UserRealName); name != "" {
		return name
	}
	if name := strings.TrimSpace(r.UserNickname); name != "" {
		return name
	}
	return strings.TrimSpace(r.UserName)
}

// TargetAdminDisplayName is the admin-facing real-name-first target display.
func (r OnetooneResult) TargetAdminDisplayName() string {
	if name := strings.TrimSpace(r.TargetRealName); name != "" {
		return name
	}
	if name := strings.TrimSpace(r.TargetNickname); name != "" {
		return name
	}
	return strings.TrimSpace(r.TargetName)
}

// OnetooneResultInput is one row passed to the save helper.
type OnetooneResultInput struct {
	Rank       int
	UserID     int64
	UserName   string
	Gender     string
	TargetID   int64
	TargetName string
}

// OnetooneRunResult reports what one execution wrote.
type OnetooneRunResult struct {
	ActivityID   int64
	Round        int
	Participants int
	MaleCount    int
	FemaleCount  int
	Pairs        int
}

// OnetooneMessageStat aggregates the viewer's non-deleted messages to a
// required chat partner.
type OnetooneMessageStat struct {
	Count  int
	LastAt time.Time
}

// OnetoonePairKey identifies one directed message pair.
type OnetoonePairKey struct {
	AuthorID int64
	TargetID int64
}

type onetooneQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const onetooneParticipantsSQL = `
SELECT s.user_id, COALESCE(u.legacy_user_id, ''), COALESCE(u.legacy_open_id, ''),
       COALESCE(u.name, ''), COALESCE(u.nickname, ''), COALESCE(u.gender, ''),
       COALESCE(s.nickname, ''), COALESCE(s.attendance, '')
FROM signups s
JOIN users u ON u.id = s.user_id
WHERE s.activity_id = $1 AND s.status = $2
ORDER BY s.created_at, s.id`

// ListOnetooneParticipants returns all active signups (including those who did
// not submit volunteers) in signup order.
func (d *DB) ListOnetooneParticipants(ctx context.Context, activityID int64) ([]OnetooneParticipant, error) {
	return loadOnetooneParticipants(ctx, d.pool, activityID)
}

func loadOnetooneParticipants(ctx context.Context, q onetooneQueryer, activityID int64) ([]OnetooneParticipant, error) {
	rows, err := q.Query(ctx, onetooneParticipantsSQL, activityID, SignupStatusSigned)
	if err != nil {
		return nil, fmt.Errorf("db: list onetoone participants: %w", err)
	}
	defer rows.Close()
	var out []OnetooneParticipant
	for rows.Next() {
		var p OnetooneParticipant
		if err := rows.Scan(&p.UserID, &p.LegacyUserID, &p.LegacyOpenID, &p.Name,
			&p.Nickname, &p.Gender, &p.SignupNickname, &p.Attendance); err != nil {
			return nil, fmt.Errorf("db: scan onetoone participant: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone participants: %w", err)
	}
	return out, nil
}

// ListOnetooneSelections returns the unified volunteer rows. The new product
// stores one-to-one choices in group_selections, exactly like grouping.
func (d *DB) ListOnetooneSelections(ctx context.Context, activityID int64) ([]OnetooneSelection, error) {
	return loadOnetooneSelections(ctx, d.pool, activityID)
}

func loadOnetooneSelections(ctx context.Context, q onetooneQueryer, activityID int64) ([]OnetooneSelection, error) {
	rows, err := q.Query(ctx,
		`SELECT selector_id, COALESCE(gender, ''), round, choices
		   FROM group_selections
		  WHERE activity_id = $1
		  ORDER BY id`, activityID)
	if err != nil {
		return nil, fmt.Errorf("db: list onetoone selections: %w", err)
	}
	defer rows.Close()
	var out []OnetooneSelection
	for rows.Next() {
		var s OnetooneSelection
		var raw []byte
		if err := rows.Scan(&s.SelectorID, &s.Gender, &s.Round, &raw); err != nil {
			return nil, fmt.Errorf("db: scan onetoone selection: %w", err)
		}
		choices, err := decodeChoiceIDs(raw)
		if err != nil {
			return nil, err
		}
		s.Choices = choices
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone selections: %w", err)
	}
	return out, nil
}

func loadOnetooneProfiles(ctx context.Context, q onetooneQueryer, userIDs []int64) (map[int64]matching.Profile, error) {
	out := make(map[int64]matching.Profile, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx,
		`SELECT id, COALESCE(education, ''), COALESCE(city, ''), COALESCE(church, ''),
		        self_hobbies, self_sports, self_traits, mbti
		   FROM users WHERE id = ANY($1)`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("db: load onetoone profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p matching.Profile
		var hobbies, sports, traits, mbti []byte
		if err := rows.Scan(&id, &p.Education, &p.City, &p.Church,
			&hobbies, &sports, &traits, &mbti); err != nil {
			return nil, fmt.Errorf("db: scan onetoone profile: %w", err)
		}
		p.Hobbies = decodeOnetooneStringList(hobbies)
		p.Sports = decodeOnetooneStringList(sports)
		p.Traits = decodeOnetooneStringList(traits)
		p.MBTI = decodeOnetooneStringList(mbti)
		out[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone profiles: %w", err)
	}
	return out, nil
}

func decodeOnetooneStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return values
}

// ExecuteOnetoone runs the balanced allocation for (activity, round) inside a
// transaction guarded by an advisory lock. The same round is never written
// twice; a failed run leaves no result rows and no status change.
func (d *DB) ExecuteOnetoone(ctx context.Context, activityID int64, round, femaleChats int) (OnetooneRunResult, error) {
	if round < 1 {
		return OnetooneRunResult{}, fmt.Errorf("db: round must be >= 1")
	}
	if femaleChats < 1 {
		return OnetooneRunResult{}, fmt.Errorf("%w: 女生场次 female_chats 必须 ≥ 1，当前 %d",
			ErrOnetooneFemaleChats, femaleChats)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return OnetooneRunResult{}, fmt.Errorf("db: begin onetoone: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOnetooneActivity(ctx, tx, activityID); err != nil {
		return OnetooneRunResult{}, err
	}
	var groupEnabled, signinEnabled bool
	if err := tx.QueryRow(ctx,
		`SELECT group_enabled, signin_enabled FROM activities WHERE id = $1 FOR UPDATE`,
		activityID).Scan(&groupEnabled, &signinEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OnetooneRunResult{}, ErrActivityNotFound
		}
		return OnetooneRunResult{}, fmt.Errorf("db: lock activity for onetoone: %w", err)
	}
	if groupEnabled {
		return OnetooneRunResult{}, ErrOnetooneGroupMode
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM onetoone_results WHERE activity_id = $1 AND round = $2)`,
		activityID, round).Scan(&exists); err != nil {
		return OnetooneRunResult{}, fmt.Errorf("db: check onetoone round: %w", err)
	}
	if exists {
		return OnetooneRunResult{}, ErrOnetooneRoundExists
	}

	participants, err := loadOnetooneParticipants(ctx, tx, activityID)
	if err != nil {
		return OnetooneRunResult{}, err
	}
	participants = eligibleOnetooneParticipants(participants, signinEnabled)
	if len(participants) == 0 {
		return OnetooneRunResult{}, ErrOnetooneNoParticipants
	}
	selections, err := loadOnetooneSelections(ctx, tx, activityID)
	if err != nil {
		return OnetooneRunResult{}, err
	}
	userIDs := make([]int64, 0, len(participants))
	for _, p := range participants {
		userIDs = append(userIDs, p.UserID)
	}
	profiles, err := loadOnetooneProfiles(ctx, tx, userIDs)
	if err != nil {
		return OnetooneRunResult{}, err
	}

	keys, keyToUser := onetooneKeys(participants)
	matchParticipants := make([]matching.OtoParticipant, 0, len(participants))
	for i, p := range participants {
		matchParticipants = append(matchParticipants, matching.OtoParticipant{
			Key: keys[i], Gender: onetooneGender(p.Gender),
		})
	}
	matchSelections := make(map[string][]matching.Choice, len(selections))
	for _, s := range selections {
		selectorKey, ok := keyToUser[s.SelectorID]
		if !ok {
			continue
		}
		choices := make([]matching.Choice, 0, len(s.Choices))
		for i, target := range s.Choices {
			targetKey, ok := keyToUser[target]
			if !ok {
				continue
			}
			choices = append(choices, matching.Choice{Target: targetKey, Priority: i + 1})
		}
		matchSelections[selectorKey] = choices
	}
	matchProfiles := make(map[string]matching.Profile, len(participants))
	for i, p := range participants {
		matchProfiles[keys[i]] = profiles[p.UserID]
	}

	allocated, err := matching.RunOffline(matchParticipants, matchSelections, matchProfiles, femaleChats)
	if err != nil {
		return OnetooneRunResult{}, fmt.Errorf("%w: %v", ErrOnetooneFemaleChats, err)
	}

	displayNames := make(map[string]string, len(participants))
	for i, p := range participants {
		displayNames[keys[i]] = p.DisplayName()
	}
	rows := rebuildOnetooneRanks(participants, keys, keyToUser, displayNames, allocated)

	if _, err := insertOnetooneResultsTx(ctx, tx, activityID, round, rows); err != nil {
		return OnetooneRunResult{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities SET onetoone_status = $2, updated_at = now() WHERE id = $1`,
		activityID, OnetooneStatusDone); err != nil {
		return OnetooneRunResult{}, fmt.Errorf("db: mark onetoone done: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OnetooneRunResult{}, fmt.Errorf("db: commit onetoone: %w", err)
	}
	stats := OnetooneRunResult{
		ActivityID: activityID, Round: round, Participants: len(participants),
		Pairs: len(rows),
	}
	for _, p := range participants {
		if onetooneGender(p.Gender) == "male" {
			stats.MaleCount++
		} else {
			stats.FemaleCount++
		}
	}
	return stats, nil
}

// rebuildOnetooneRanks emits rows in participant order with per-person ranks.
func rebuildOnetooneRanks(participants []OnetooneParticipant, keys []string,
	keyToUser map[int64]string, displayNames map[string]string,
	allocated matching.OfflineResult) []OnetooneResultInput {
	userByKey := make(map[string]int64, len(keyToUser))
	for userID, key := range keyToUser {
		userByKey[key] = userID
	}
	rows := make([]OnetooneResultInput, 0, len(participants))
	for i, p := range participants {
		key := keys[i]
		if list := allocated.MaleLists[key]; len(list) > 0 {
			for _, item := range list {
				rows = append(rows, OnetooneResultInput{
					Rank: item.Rank, UserID: p.UserID, UserName: p.DisplayName(),
					Gender: p.Gender, TargetID: userByKey[item.Target],
					TargetName: displayNames[item.Target],
				})
			}
			continue
		}
		for rank, pair := range allocated.FemaleLists[key] {
			rows = append(rows, OnetooneResultInput{
				Rank: rank + 1, UserID: p.UserID, UserName: p.DisplayName(),
				Gender: p.Gender, TargetID: userByKey[pair.Target],
				TargetName: displayNames[pair.Target],
			})
		}
	}
	return rows
}

func eligibleOnetooneParticipants(participants []OnetooneParticipant, signinEnabled bool) []OnetooneParticipant {
	out := make([]OnetooneParticipant, 0, len(participants))
	for _, p := range participants {
		if onetooneGender(p.Gender) == "" {
			continue
		}
		if signinEnabled && !IsCheckedIn(p.Attendance) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func onetooneGender(raw string) string {
	switch strings.TrimSpace(raw) {
	case "男性":
		return "male"
	case "女性":
		return "female"
	default:
		return ""
	}
}

// onetooneKeys assigns a stable key per participant. Legacy open ids keep the
// v1 tie-break; numeric ids are the fallback. Duplicate keys get a suffix so
// the allocation cannot merge two different people.
func onetooneKeys(participants []OnetooneParticipant) ([]string, map[int64]string) {
	keys := make([]string, len(participants))
	keyToUser := make(map[int64]string, len(participants))
	seen := map[string]int{}
	for i, p := range participants {
		key := strings.TrimSpace(p.LegacyOpenID)
		if key == "" {
			key = "u-" + strconv.FormatInt(p.UserID, 10)
		}
		seen[key]++
		if seen[key] > 1 {
			key = key + "#" + strconv.FormatInt(p.UserID, 10)
		}
		keys[i] = key
		keyToUser[p.UserID] = key
	}
	return keys, keyToUser
}

func lockOnetooneActivity(ctx context.Context, tx pgx.Tx, activityID int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7411, $1)`, activityID); err != nil {
		return fmt.Errorf("db: lock onetoone activity: %w", err)
	}
	return nil
}

func insertOnetooneResultsTx(ctx context.Context, tx pgx.Tx, activityID int64, round int,
	rows []OnetooneResultInput) (int, error) {
	for _, row := range rows {
		if row.Rank < 1 || row.UserID == 0 || row.TargetID == 0 {
			return 0, fmt.Errorf("db: invalid onetoone result row: %+v", row)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO onetoone_results
			   (activity_id, rank, user_id, user_name, gender, target_id, target_name, round)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (activity_id, round, user_id, target_id) DO NOTHING`,
			activityID, row.Rank, row.UserID, row.UserName, row.Gender,
			row.TargetID, row.TargetName, round); err != nil {
			return 0, fmt.Errorf("db: insert onetoone result: %w", err)
		}
	}
	return len(rows), nil
}

// SaveOnetooneResults stores a precomputed round for admin/manual use. It keeps
// the same idempotency contract as ExecuteOnetoone: an existing round is never
// overwritten.
func (d *DB) SaveOnetooneResults(ctx context.Context, activityID int64, round int,
	rows []OnetooneResultInput) (int, error) {
	if round < 1 {
		return 0, fmt.Errorf("db: round must be >= 1")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin save onetoone: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockOnetooneActivity(ctx, tx, activityID); err != nil {
		return 0, err
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM onetoone_results WHERE activity_id = $1 AND round = $2)`,
		activityID, round).Scan(&exists); err != nil {
		return 0, fmt.Errorf("db: check onetoone round: %w", err)
	}
	if exists {
		return 0, ErrOnetooneRoundExists
	}
	if _, err := insertOnetooneResultsTx(ctx, tx, activityID, round, rows); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities SET onetoone_status = $2, updated_at = now() WHERE id = $1`,
		activityID, OnetooneStatusDone); err != nil {
		return 0, fmt.Errorf("db: mark onetoone saved: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit save onetoone: %w", err)
	}
	return len(rows), nil
}

// ResetOnetooneResults deletes every round for the activity and clears the
// status field. It returns the number of removed result rows.
func (d *DB) ResetOnetooneResults(ctx context.Context, activityID int64) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin reset onetoone: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockOnetooneActivity(ctx, tx, activityID); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM onetoone_results WHERE activity_id = $1`, activityID)
	if err != nil {
		return 0, fmt.Errorf("db: clear onetoone results: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities SET onetoone_status = '', updated_at = now() WHERE id = $1`,
		activityID); err != nil {
		return 0, fmt.Errorf("db: clear onetoone status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit reset onetoone: %w", err)
	}
	return tag.RowsAffected(), nil
}

// OnetooneRounds lists the generated rounds in ascending order.
func (d *DB) OnetooneRounds(ctx context.Context, activityID int64) ([]int, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT DISTINCT round FROM onetoone_results WHERE activity_id = $1 ORDER BY round`,
		activityID)
	if err != nil {
		return nil, fmt.Errorf("db: list onetoone rounds: %w", err)
	}
	defer rows.Close()
	out := make([]int, 0)
	for rows.Next() {
		var round int
		if err := rows.Scan(&round); err != nil {
			return nil, fmt.Errorf("db: scan onetoone round: %w", err)
		}
		out = append(out, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone rounds: %w", err)
	}
	return out, nil
}

// OnetooneRoundCounts returns the result-row count per generated round.
func (d *DB) OnetooneRoundCounts(ctx context.Context, activityID int64) (map[int]int, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT round, count(*) FROM onetoone_results
		  WHERE activity_id = $1 GROUP BY round ORDER BY round`, activityID)
	if err != nil {
		return nil, fmt.Errorf("db: count onetoone rounds: %w", err)
	}
	defer rows.Close()
	out := make(map[int]int)
	for rows.Next() {
		var round, count int
		if err := rows.Scan(&round, &count); err != nil {
			return nil, fmt.Errorf("db: scan onetoone round count: %w", err)
		}
		out[round] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone round counts: %w", err)
	}
	return out, nil
}

// LatestOnetooneRound returns the highest generated round, or 0 when none.
func (d *DB) LatestOnetooneRound(ctx context.Context, activityID int64) (int, error) {
	var latest int
	if err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(round), 0) FROM onetoone_results WHERE activity_id = $1`,
		activityID).Scan(&latest); err != nil {
		return 0, fmt.Errorf("db: latest onetoone round: %w", err)
	}
	return latest, nil
}

const onetooneResultSelect = `
SELECT r.id, r.activity_id, r.round, r.rank, r.user_id, r.user_name, r.gender,
       r.target_id, r.target_name,
       COALESCE(u.name, ''), COALESCE(u.nickname, ''),
       COALESCE(u.legacy_user_id, ''), COALESCE(u.legacy_open_id, ''),
       COALESCE(t.name, ''), COALESCE(t.nickname, ''),
       COALESCE(t.legacy_user_id, ''), COALESCE(t.legacy_open_id, ''),
       COALESCE(cover.media_id, ''), r.created_at
FROM onetoone_results r
JOIN users u ON u.id = r.user_id
JOIN users t ON t.id = r.target_id
LEFT JOIN LATERAL (
    SELECT pp.media_id
      FROM profile_photos pp
     WHERE pp.user_id = t.id AND pp.is_cover
     ORDER BY pp.sort_order, pp.id
     LIMIT 1
) cover ON true`

func scanOnetooneResult(row pgx.Row) (OnetooneResult, error) {
	var r OnetooneResult
	err := row.Scan(&r.ID, &r.ActivityID, &r.Round, &r.Rank, &r.UserID, &r.UserName,
		&r.Gender, &r.TargetID, &r.TargetName, &r.UserRealName, &r.UserNickname,
		&r.UserLegacyID, &r.UserOpenID, &r.TargetRealName, &r.TargetNickname,
		&r.TargetLegacyID, &r.TargetOpenID, &r.TargetPhotoID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OnetooneResult{}, ErrNotFound
	}
	if err != nil {
		return OnetooneResult{}, fmt.Errorf("db: scan onetoone result: %w", err)
	}
	return r, nil
}

// ListOnetooneResultsForUser returns only the caller's partners in one round.
func (d *DB) ListOnetooneResultsForUser(ctx context.Context, activityID, userID int64, round int) ([]OnetooneResult, error) {
	rows, err := d.pool.Query(ctx,
		onetooneResultSelect+`
		WHERE r.activity_id = $1 AND r.round = $2 AND r.user_id = $3
		ORDER BY r.rank, r.target_id`, activityID, round, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list my onetoone results: %w", err)
	}
	defer rows.Close()
	return collectOnetooneResults(rows)
}

// ListOnetooneResults returns every row in one round for the admin console.
func (d *DB) ListOnetooneResults(ctx context.Context, activityID int64, round int) ([]OnetooneResult, error) {
	rows, err := d.pool.Query(ctx,
		onetooneResultSelect+`
		WHERE r.activity_id = $1 AND r.round = $2
		ORDER BY r.user_id, r.rank, r.target_id`, activityID, round)
	if err != nil {
		return nil, fmt.Errorf("db: list onetoone results: %w", err)
	}
	defer rows.Close()
	return collectOnetooneResults(rows)
}

func collectOnetooneResults(rows pgx.Rows) ([]OnetooneResult, error) {
	out := make([]OnetooneResult, 0)
	for rows.Next() {
		r, err := scanOnetooneResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone results: %w", err)
	}
	return out, nil
}

// ChattedTargetIDs reports which targets the author has left a non-deleted
// message on. This is the only "already chatted" definition.
func (d *DB) ChattedTargetIDs(ctx context.Context, authorID int64, targetIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool)
	if len(targetIDs) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT DISTINCT target_user_id
		   FROM messages
		  WHERE author_id = $1 AND status <> $2 AND target_user_id = ANY($3)`,
		authorID, MessageStatusDeleted, targetIDs)
	if err != nil {
		return nil, fmt.Errorf("db: chatted targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: scan chatted target: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate chatted targets: %w", err)
	}
	return out, nil
}

// OnetooneMessageStats returns per-pair message counts and latest timestamps
// for the given required pairs. Only non-deleted messages count.
func (d *DB) OnetooneMessageStats(ctx context.Context, authors, targets []int64) (map[OnetoonePairKey]OnetooneMessageStat, error) {
	out := make(map[OnetoonePairKey]OnetooneMessageStat)
	if len(authors) == 0 || len(targets) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT author_id, target_user_id, count(*), max(created_at)
		   FROM messages
		  WHERE status <> $1 AND author_id = ANY($2) AND target_user_id = ANY($3)
		  GROUP BY author_id, target_user_id`,
		MessageStatusDeleted, authors, targets)
	if err != nil {
		return nil, fmt.Errorf("db: onetoone message stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key OnetoonePairKey
		var stat OnetooneMessageStat
		if err := rows.Scan(&key.AuthorID, &key.TargetID, &stat.Count, &stat.LastAt); err != nil {
			return nil, fmt.Errorf("db: scan onetoone message stat: %w", err)
		}
		out[key] = stat
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate onetoone message stats: %w", err)
	}
	return out, nil
}

// CountOnetooneSelections returns the number of distinct volunteers.
func (d *DB) CountOnetooneSelections(ctx context.Context, activityID int64) (int, error) {
	var count int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(DISTINCT selector_id) FROM group_selections WHERE activity_id = $1`,
		activityID).Scan(&count); err != nil {
		return 0, fmt.Errorf("db: count onetoone selections: %w", err)
	}
	return count, nil
}

// FindOnetooneActivityForUser resolves the most recent activity with a result
// for the caller. It powers the legacy /onetoone/result entry point.
func (d *DB) FindOnetooneActivityForUser(ctx context.Context, userID int64) (Activity, error) {
	act, err := scanActivity(d.pool.QueryRow(ctx,
		`SELECT `+qualifiedActivityColumns("a")+`
		   FROM onetoone_results r
		   JOIN activities a ON a.id = r.activity_id
		  WHERE r.user_id = $1
		  ORDER BY r.created_at DESC, r.id DESC
		  LIMIT 1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, err
	}
	return act, nil
}

// OnetooneRoundSummary groups admin result rows by person.
type OnetooneRoundSummary struct {
	People       []OnetoonePerson
	ResultCount  int
	MatchedCount int
}

// OnetoonePerson is one person's partners in a round.
type OnetoonePerson struct {
	UserID      int64
	UserIDLabel string
	Name        string
	Gender      string
	Partners    []OnetooneResult
}

// SummarizeOnetooneResults groups flat result rows into people, preserving
// partner rank order.
func SummarizeOnetooneResults(rows []OnetooneResult) OnetooneRoundSummary {
	byUser := map[int64]*OnetoonePerson{}
	order := make([]int64, 0)
	for _, row := range rows {
		person, ok := byUser[row.UserID]
		if !ok {
			person = &OnetoonePerson{
				UserID: row.UserID, UserIDLabel: row.UserLegacyID,
				Name: row.UserDisplayName(), Gender: row.Gender,
			}
			byUser[row.UserID] = person
			order = append(order, row.UserID)
		}
		person.Partners = append(person.Partners, row)
	}
	people := make([]OnetoonePerson, 0, len(order))
	for _, id := range order {
		person := byUser[id]
		sort.SliceStable(person.Partners, func(i, j int) bool {
			if person.Partners[i].Rank != person.Partners[j].Rank {
				return person.Partners[i].Rank < person.Partners[j].Rank
			}
			return person.Partners[i].TargetID < person.Partners[j].TargetID
		})
		people = append(people, *person)
	}
	return OnetooneRoundSummary{
		People: people, ResultCount: len(rows), MatchedCount: len(people),
	}
}
