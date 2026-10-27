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

// Grouping-domain errors. Handlers translate these into stable API codes.
var (
	ErrGroupNotSignedUp     = errors.New("db: user is not signed up for the activity")
	ErrGroupSigninRequired  = errors.New("db: user must check in before grouping")
	ErrGroupChoiceCount     = errors.New("db: grouping requires exactly seven choices")
	ErrGroupChoiceDuplicate = errors.New("db: grouping choices must be unique")
	ErrGroupChoiceInvalid   = errors.New("db: grouping choice is not in the candidate pool")
	ErrGroupNotComplete     = errors.New("db: grouping is not complete")
	ErrGroupBusy            = errors.New("db: grouping lock is held by another operation")
	ErrGroupAlreadyDone     = errors.New("db: grouping round already has results")
	ErrGroupSizeInvalid     = errors.New("db: per-group sizes must be positive")
	ErrGroupRoundInvalid    = errors.New("db: grouping round must be a positive integer")
	ErrGroupModeConflict    = errors.New("db: one-to-one mode is active")
	ErrGroupEmpty           = errors.New("db: algorithm produced no groups")
	ErrGroupSelectionAbsent = errors.New("db: no grouping selection")
)

// GroupChoiceError carries the offending zero-based choice position.
type GroupChoiceError struct {
	Index int
	Err   error
}

func (e *GroupChoiceError) Error() string {
	return fmt.Sprintf("%s (choice %d)", e.Err, e.Index+1)
}

func (e *GroupChoiceError) Unwrap() error { return e.Err }

// GroupNotEnoughPeopleError reports the run-time pool after the sign-in gate.
type GroupNotEnoughPeopleError struct {
	Males   int
	Females int
	NeedM   int
	NeedF   int
}

func (e *GroupNotEnoughPeopleError) Error() string {
	return fmt.Sprintf("db: not enough participants: %d male %d female, need %d and %d",
		e.Males, e.Females, e.NeedM, e.NeedF)
}

// GroupCandidate is a privacy-safe candidate row. It intentionally excludes
// phone, ID card and credentials; it carries only what the selection page shows.
type GroupCandidate struct {
	UserID       int64      `json:"id"`
	LegacyUserID string     `json:"user_id"`
	LegacyOpenID string     `json:"openid"`
	Nickname     string     `json:"nickname"`
	Name         string     `json:"name"`
	Gender       string     `json:"gender"`
	Birthday     *time.Time `json:"-"`
	HeightCM     *int       `json:"height_cm,omitempty"`
	Education    string     `json:"education"`
	NativePlace  string     `json:"native_place"`
	City         string     `json:"city"`
	Hobbies      []string   `json:"hobbies"`
	PhotoID      string     `json:"photo_id"`
	Attendance   string     `json:"attendance"`
	CheckedIn    bool       `json:"checked_in"`
}

// DisplayName prefers the public nickname and never falls back to the phone.
func (c GroupCandidate) DisplayName() string {
	if strings.TrimSpace(c.Nickname) != "" {
		return c.Nickname
	}
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return c.LegacyUserID
}

// GroupSelection is one stored seven-choice submission.
type GroupSelection struct {
	ID         int64
	ActivityID int64
	SelectorID int64
	Gender     string
	Choices    []int64
	Round      int
	UpdatedAt  time.Time
}

// GroupStatus is the user-facing status payload.
type GroupStatus struct {
	ActivityID  int64
	GroupStatus string
	Selected    bool
	Choices     []int64
	Round       int
}

// GroupMember is one member of a generated group. DisplayName is resolved from
// the live profile first, then the stored result name.
type GroupMember struct {
	UserID       int64
	LegacyUserID string
	LegacyOpenID string
	Nickname     string
	Name         string
	UserName     string
	Gender       string
	PhotoID      string
}

// DisplayName mirrors the legacy result priority: real name, stored result
// name, nickname, then the legacy id.
func (m GroupMember) DisplayName() string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	if strings.TrimSpace(m.UserName) != "" {
		return m.UserName
	}
	if strings.TrimSpace(m.Nickname) != "" {
		return m.Nickname
	}
	return m.LegacyUserID
}

// UserGroupResult is the owner-only result projection.
type UserGroupResult struct {
	ActivityID   int64
	ActivityNo   string
	ActivityName string
	GroupStatus  string
	Round        int
	Rounds       []int
	MyGroup      int
	Members      []GroupMember
	TotalGroups  int
}

// AdminGroupResult is the full result projection for the management API.
type AdminGroupResult struct {
	ActivityID   int64
	ActivityNo   string
	ActivityName string
	GroupStatus  string
	Round        int
	Rounds       []int
	Groups       []AdminGroup
}

// AdminGroup is one full group in the management result.
type AdminGroup struct {
	GroupID int
	Members []GroupMember
}

// GroupRunResult is the successful execution summary.
type GroupRunResult struct {
	ActivityID       int64
	ActivityNo       string
	ActivityName     string
	Round            int
	MalesPerGroup    int
	FemalesPerGroup  int
	GroupCount       int
	ParticipantCount int
	Groups           []AdminGroup
}

// GroupPreview is a side-effect-free execution preview.
type GroupPreview struct {
	ActivityID           int64
	ActivityNo           string
	ActivityName         string
	GroupStatus          string
	Round                int
	MalesPerGroup        int
	FemalesPerGroup      int
	MaleCount            int
	FemaleCount          int
	ParticipantCount     int
	CheckedInMaleCount   int
	CheckedInFemaleCount int
	SkippedNoGender      int
	ExpectedGroups       int
	ExistingRounds       []int
	ExistingRoundResults int
	HistoricalResults    int
	CanExecute           bool
	Blockers             []string
}

const groupSelectionColumns = `id, activity_id, selector_id, gender, choices, round, updated_at`

// decodeChoiceIDs accepts both the current numeric JSON array and legacy
// string arrays so migrated rows never fail to prefill.
func decodeChoiceIDs(raw []byte) ([]int64, error) {
	if len(raw) == 0 {
		return []int64{}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("db: decode grouping choices: %w", err)
	}
	out := make([]int64, 0, len(items))
	for _, item := range items {
		var id int64
		if err := json.Unmarshal(item, &id); err == nil && id > 0 {
			out = append(out, id)
			continue
		}
		var text string
		if err := json.Unmarshal(item, &text); err == nil {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil && parsed > 0 {
				out = append(out, parsed)
			}
		}
	}
	return out, nil
}

func scanGroupSelection(row rowScanner) (GroupSelection, error) {
	var selection GroupSelection
	var raw []byte
	err := row.Scan(&selection.ID, &selection.ActivityID, &selection.SelectorID,
		&selection.Gender, &raw, &selection.Round, &selection.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupSelection{}, ErrGroupSelectionAbsent
	}
	if err != nil {
		return GroupSelection{}, fmt.Errorf("db: scan group selection: %w", err)
	}
	choices, err := decodeChoiceIDs(raw)
	if err != nil {
		return GroupSelection{}, err
	}
	selection.Choices = choices
	return selection, nil
}

func (d *DB) loadGroupSelection(ctx context.Context, activityID, userID int64) (GroupSelection, error) {
	return scanGroupSelection(d.pool.QueryRow(ctx,
		`SELECT `+groupSelectionColumns+` FROM group_selections
		  WHERE activity_id = $1 AND selector_id = $2`, activityID, userID))
}

// groupingGate closes the user-side grouping entry once the activity has ended
// or grouping is not enabled. Admin console methods intentionally bypass it so
// results stay visible for management.
func groupingGate(activity Activity, now time.Time) error {
	if activity.EndedAt(now) {
		return ErrActivityEnded
	}
	if !activity.GroupEnabled {
		return ErrActivityNotOpen
	}
	return nil
}

// loadSignupGate verifies the user registered and, when sign-in is enabled, is
// present. It uses the same rule as the legacy endpoints: non-cancelled signup
// plus a checked-in attendance value.
func (d *DB) loadSignupGate(ctx context.Context, activity Activity, userID int64) (Signup, error) {
	signup, err := d.GetSignup(ctx, activity.ID, userID)
	if errors.Is(err, ErrNotSignedUp) {
		return Signup{}, ErrGroupNotSignedUp
	}
	if err != nil {
		return Signup{}, err
	}
	if signup.Status == SignupStatusCancelled {
		return Signup{}, ErrGroupNotSignedUp
	}
	if activity.SigninEnabled && !signup.Attendance.CheckedIn() {
		return Signup{}, ErrGroupSigninRequired
	}
	return signup, nil
}

func (d *DB) loadViewerGender(ctx context.Context, userID int64) (string, error) {
	var gender string
	err := d.pool.QueryRow(ctx, `SELECT COALESCE(gender, '') FROM users WHERE id = $1`, userID).
		Scan(&gender)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load viewer gender: %w", err)
	}
	return strings.TrimSpace(gender), nil
}

func oppositeGender(gender string) string {
	switch strings.TrimSpace(gender) {
	case "男性":
		return "女性"
	case "女性":
		return "男性"
	default:
		return ""
	}
}

// GroupCandidates returns the activity's signed-up opposite-gender members.
// When sign-in is enabled only checked-in people are included, including the
// caller (the caller must have checked in first).
func (d *DB) GroupCandidates(ctx context.Context, userID int64, activityRef string) (Activity, []GroupCandidate, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return Activity{}, nil, err
	}
	if err := groupingGate(activity, time.Now()); err != nil {
		return Activity{}, nil, err
	}
	if _, err := d.loadSignupGate(ctx, activity, userID); err != nil {
		return Activity{}, nil, err
	}
	gender, err := d.loadViewerGender(ctx, userID)
	if err != nil {
		return Activity{}, nil, err
	}
	target := oppositeGender(gender)
	if target == "" {
		return activity, []GroupCandidate{}, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT u.id, COALESCE(u.legacy_user_id, ''), COALESCE(u.legacy_open_id, ''),
		        u.nickname, u.name, u.gender, u.birthday, u.height_cm, u.education,
		        u.native_place, u.city, u.self_hobbies, COALESCE(s.attendance, ''),
		        COALESCE((
		            SELECT pp.media_id FROM profile_photos pp
		             WHERE pp.user_id = u.id
		             ORDER BY pp.is_cover DESC, pp.sort_order, pp.id
		             LIMIT 1
		        ), '')
		   FROM signups s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.activity_id = $1
		    AND s.status <> $2
		    AND u.id <> $3
		    AND u.gender = $4
		    AND ($5 = false OR s.attendance IN ($6, $7))
		  ORDER BY s.created_at, s.id`,
		activity.ID, SignupStatusCancelled, userID, target,
		activity.SigninEnabled, string(AttendanceOnTime), string(AttendanceLate))
	if err != nil {
		return Activity{}, nil, fmt.Errorf("db: list group candidates: %w", err)
	}
	defer rows.Close()

	candidates := []GroupCandidate{}
	for rows.Next() {
		var candidate GroupCandidate
		var rawHobbies []byte
		if err := rows.Scan(&candidate.UserID, &candidate.LegacyUserID,
			&candidate.LegacyOpenID, &candidate.Nickname, &candidate.Name,
			&candidate.Gender, &candidate.Birthday, &candidate.HeightCM,
			&candidate.Education, &candidate.NativePlace, &candidate.City, &rawHobbies,
			&candidate.Attendance, &candidate.PhotoID); err != nil {
			return Activity{}, nil, fmt.Errorf("db: scan group candidate: %w", err)
		}
		hobbies, err := decodeStringList(rawHobbies)
		if err != nil {
			return Activity{}, nil, fmt.Errorf("db: decode candidate hobbies: %w", err)
		}
		candidate.Hobbies = hobbies
		candidate.CheckedIn = IsCheckedIn(candidate.Attendance)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return Activity{}, nil, fmt.Errorf("db: iterate group candidates: %w", err)
	}
	return activity, candidates, nil
}

// GroupStatusForUser returns the activity group status plus the caller's own
// choices. It never exposes another user's selection.
func (d *DB) GroupStatusForUser(ctx context.Context, userID int64, activityRef string) (GroupStatus, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return GroupStatus{}, err
	}
	if err := groupingGate(activity, time.Now()); err != nil {
		return GroupStatus{}, err
	}
	status := GroupStatus{
		ActivityID:  activity.ID,
		GroupStatus: activity.GroupStatus,
		Choices:     []int64{},
	}
	selection, err := d.loadGroupSelection(ctx, activity.ID, userID)
	if errors.Is(err, ErrGroupSelectionAbsent) {
		return status, nil
	}
	if err != nil {
		return GroupStatus{}, err
	}
	status.Selected = true
	status.Choices = selection.Choices
	status.Round = selection.Round
	return status, nil
}

// SelectGroupChoices stores exactly seven unique candidates. The unique
// constraint plus ON CONFLICT makes double submits idempotent.
func (d *DB) SelectGroupChoices(ctx context.Context, userID int64, activityRef string,
	choices []int64) (GroupSelection, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return GroupSelection{}, err
	}
	if err := groupingGate(activity, time.Now()); err != nil {
		return GroupSelection{}, err
	}
	if _, err := d.loadSignupGate(ctx, activity, userID); err != nil {
		return GroupSelection{}, err
	}
	if len(choices) != 7 {
		return GroupSelection{}, ErrGroupChoiceCount
	}
	seen := make(map[int64]bool, len(choices))
	for i, id := range choices {
		if id <= 0 || id == userID {
			return GroupSelection{}, &GroupChoiceError{Index: i, Err: ErrGroupChoiceInvalid}
		}
		if seen[id] {
			return GroupSelection{}, &GroupChoiceError{Index: i, Err: ErrGroupChoiceDuplicate}
		}
		seen[id] = true
	}

	gender, err := d.loadViewerGender(ctx, userID)
	if err != nil {
		return GroupSelection{}, err
	}
	target := oppositeGender(gender)
	if target == "" {
		return GroupSelection{}, &GroupChoiceError{Index: 0, Err: ErrGroupChoiceInvalid}
	}
	rows, err := d.pool.Query(ctx,
		`SELECT u.id
		   FROM signups s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.activity_id = $1
		    AND s.status <> $2
		    AND u.id <> $3
		    AND u.gender = $4
		    AND ($5 = false OR s.attendance IN ($6, $7))`,
		activity.ID, SignupStatusCancelled, userID, target,
		activity.SigninEnabled, string(AttendanceOnTime), string(AttendanceLate))
	if err != nil {
		return GroupSelection{}, fmt.Errorf("db: list group candidate ids: %w", err)
	}
	defer rows.Close()
	allowed := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return GroupSelection{}, fmt.Errorf("db: scan group candidate id: %w", err)
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return GroupSelection{}, fmt.Errorf("db: iterate group candidate ids: %w", err)
	}
	for i, id := range choices {
		if !allowed[id] {
			return GroupSelection{}, &GroupChoiceError{Index: i, Err: ErrGroupChoiceInvalid}
		}
	}

	encoded, err := json.Marshal(choices)
	if err != nil {
		return GroupSelection{}, fmt.Errorf("db: encode grouping choices: %w", err)
	}
	return scanGroupSelection(d.pool.QueryRow(ctx,
		`INSERT INTO group_selections (activity_id, selector_id, gender, choices, round, updated_at)
		 VALUES ($1, $2, $3, $4, 1, now())
		 ON CONFLICT (activity_id, selector_id) DO UPDATE
		   SET gender = EXCLUDED.gender,
		       choices = EXCLUDED.choices,
		       updated_at = now()
		 RETURNING `+groupSelectionColumns,
		activity.ID, userID, gender, encoded))
}

func scanGroupMember(row rowScanner) (GroupMember, error) {
	var member GroupMember
	err := row.Scan(&member.UserID, &member.LegacyUserID, &member.LegacyOpenID,
		&member.Nickname, &member.Name, &member.UserName, &member.Gender, &member.PhotoID)
	if err != nil {
		return GroupMember{}, fmt.Errorf("db: scan group member: %w", err)
	}
	return member, nil
}

const groupMemberColumns = `gr.user_id, COALESCE(u.legacy_user_id, ''),
		COALESCE(u.legacy_open_id, ''), COALESCE(u.nickname, ''), COALESCE(u.name, ''),
		gr.user_name, gr.gender,
		COALESCE((
		    SELECT pp.media_id FROM profile_photos pp
		     WHERE pp.user_id = gr.user_id
		     ORDER BY pp.is_cover DESC, pp.sort_order, pp.id
		     LIMIT 1
		), '')`

const groupMemberSelect = `SELECT ` + groupMemberColumns + `
	  FROM group_results gr
	  LEFT JOIN users u ON u.id = gr.user_id
	 WHERE gr.activity_id = $1 AND gr.round = $2`

func (d *DB) groupRounds(ctx context.Context, activityID int64) ([]int, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT DISTINCT round FROM group_results WHERE activity_id = $1 ORDER BY round`,
		activityID)
	if err != nil {
		return nil, fmt.Errorf("db: list grouping rounds: %w", err)
	}
	defer rows.Close()
	rounds := []int{}
	for rows.Next() {
		var round int
		if err := rows.Scan(&round); err != nil {
			return nil, fmt.Errorf("db: scan grouping round: %w", err)
		}
		if round >= 1 {
			rounds = append(rounds, round)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate grouping rounds: %w", err)
	}
	return rounds, nil
}

func latestRoundOf(rounds []int) int {
	if len(rounds) == 0 {
		return 0
	}
	return rounds[len(rounds)-1]
}

func resolveResultRound(rounds []int, requested int) int {
	if len(rounds) == 0 {
		return 0
	}
	if requested <= 0 {
		return latestRoundOf(rounds)
	}
	for _, round := range rounds {
		if round == requested {
			return requested
		}
	}
	return latestRoundOf(rounds)
}

// GroupResultForUser returns only the caller's group for the requested (or
// latest) round. Other groups are never loaded into this projection.
func (d *DB) GroupResultForUser(ctx context.Context, userID int64, activityRef string,
	round int) (UserGroupResult, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return UserGroupResult{}, err
	}
	if err := groupingGate(activity, time.Now()); err != nil {
		return UserGroupResult{}, err
	}
	result := UserGroupResult{
		ActivityID:   activity.ID,
		ActivityNo:   activity.ActivityNo,
		ActivityName: activity.Name,
		GroupStatus:  activity.GroupStatus,
		Rounds:       []int{},
		Members:      []GroupMember{},
	}
	if activity.GroupStatus != "已完成" {
		return result, ErrGroupNotComplete
	}
	rounds, err := d.groupRounds(ctx, activity.ID)
	if err != nil {
		return UserGroupResult{}, err
	}
	result.Rounds = rounds
	result.Round = resolveResultRound(rounds, round)
	if result.Round == 0 {
		return result, nil
	}

	var myGroup int
	err = d.pool.QueryRow(ctx,
		`SELECT group_no FROM group_results
		  WHERE activity_id = $1 AND round = $2 AND user_id = $3`,
		activity.ID, result.Round, userID).Scan(&myGroup)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return UserGroupResult{}, fmt.Errorf("db: load my group: %w", err)
	}
	result.MyGroup = myGroup
	rows, err := d.pool.Query(ctx, groupMemberSelect+
		` AND gr.group_no = $3
		  ORDER BY CASE gr.gender WHEN '男性' THEN 0 WHEN '女性' THEN 1 ELSE 2 END, gr.id`,
		activity.ID, result.Round, result.MyGroup)
	if err != nil {
		return UserGroupResult{}, fmt.Errorf("db: list my group members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		member, err := scanGroupMember(rows)
		if err != nil {
			return UserGroupResult{}, err
		}
		result.Members = append(result.Members, member)
	}
	if err := rows.Err(); err != nil {
		return UserGroupResult{}, fmt.Errorf("db: iterate my group members: %w", err)
	}
	if err := d.pool.QueryRow(ctx,
		`SELECT count(DISTINCT group_no) FROM group_results
		  WHERE activity_id = $1 AND round = $2`,
		activity.ID, result.Round).Scan(&result.TotalGroups); err != nil {
		return UserGroupResult{}, fmt.Errorf("db: count groups: %w", err)
	}
	return result, nil
}

// LatestGroupRound returns the newest result round, or 0 when none exists.
func (d *DB) LatestGroupRound(ctx context.Context, activityRef string) (int, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return 0, err
	}
	rounds, err := d.groupRounds(ctx, activity.ID)
	if err != nil {
		return 0, err
	}
	return latestRoundOf(rounds), nil
}

// AdminGroupResult returns every group of one round (latest by default).
func (d *DB) AdminGroupResult(ctx context.Context, activityRef string, round int) (AdminGroupResult, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return AdminGroupResult{}, err
	}
	result := AdminGroupResult{
		ActivityID:   activity.ID,
		ActivityNo:   activity.ActivityNo,
		ActivityName: activity.Name,
		GroupStatus:  activity.GroupStatus,
		Rounds:       []int{},
		Groups:       []AdminGroup{},
	}
	rounds, err := d.groupRounds(ctx, activity.ID)
	if err != nil {
		return AdminGroupResult{}, err
	}
	result.Rounds = rounds
	result.Round = resolveResultRound(rounds, round)
	if result.Round == 0 {
		return result, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT gr.group_no, `+groupMemberColumns+`
		   FROM group_results gr
		   LEFT JOIN users u ON u.id = gr.user_id
		  WHERE gr.activity_id = $1 AND gr.round = $2
		  ORDER BY gr.group_no,
		           CASE gr.gender WHEN '男性' THEN 0 WHEN '女性' THEN 1 ELSE 2 END, gr.id`,
		activity.ID, result.Round)
	if err != nil {
		return AdminGroupResult{}, fmt.Errorf("db: list admin group results: %w", err)
	}
	defer rows.Close()
	byGroup := map[int]*AdminGroup{}
	order := []int{}
	for rows.Next() {
		var groupNo int
		var member GroupMember
		if err := rows.Scan(&groupNo, &member.UserID, &member.LegacyUserID,
			&member.LegacyOpenID, &member.Nickname, &member.Name, &member.UserName, &member.Gender,
			&member.PhotoID); err != nil {
			return AdminGroupResult{}, fmt.Errorf("db: scan admin group member: %w", err)
		}
		group := byGroup[groupNo]
		if group == nil {
			group = &AdminGroup{GroupID: groupNo, Members: []GroupMember{}}
			byGroup[groupNo] = group
			order = append(order, groupNo)
		}
		group.Members = append(group.Members, member)
	}
	if err := rows.Err(); err != nil {
		return AdminGroupResult{}, fmt.Errorf("db: iterate admin group results: %w", err)
	}
	for _, groupNo := range order {
		result.Groups = append(result.Groups, *byGroup[groupNo])
	}
	return result, nil
}
