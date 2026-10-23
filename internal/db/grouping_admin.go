package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/matching"
	"github.com/jackc/pgx/v5"
)

// DefaultGroupLockTimeout bounds how long an admin grouping call waits for
// the activity advisory lock. Grouping is a short deterministic computation
// on PostgreSQL, so a shorter budget than the legacy 180s is enough and keeps
// request handlers responsive.
const DefaultGroupLockTimeout = 5 * time.Second

// groupAdvisoryBase keeps grouping locks in their own advisory-lock namespace.
const groupAdvisoryBase int64 = 0x47525000

func groupLockKey(activityID int64) int64 { return groupAdvisoryBase + activityID }

// acquireGroupLock polls pg_try_advisory_xact_lock so a stuck peer can never
// pin a request thread indefinitely. The lock is released on commit/rollback.
func acquireGroupLock(ctx context.Context, tx pgx.Tx, activityID int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	key := groupLockKey(activityID)
	for {
		var acquired bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, key).Scan(&acquired); err != nil {
			return fmt.Errorf("db: acquire grouping lock: %w", err)
		}
		if acquired {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrGroupBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// lockActivityTx locks the advisory key first, then the activity row. The
// activity id is resolved without a row lock because the ref may be a text
// activity number.
func (d *DB) lockActivityTx(ctx context.Context, tx pgx.Tx, ref string,
	timeout time.Duration) (Activity, error) {
	text, numeric := activityRefQueryArgs(ref)
	var activityID int64
	err := tx.QueryRow(ctx,
		`SELECT id FROM activities WHERE `+activityRefFilter, text, numeric).Scan(&activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("db: resolve activity for grouping: %w", err)
	}
	if timeout <= 0 {
		timeout = DefaultGroupLockTimeout
	}
	if err := acquireGroupLock(ctx, tx, activityID, timeout); err != nil {
		return Activity{}, err
	}
	activity, err := scanActivity(tx.QueryRow(ctx,
		`SELECT `+activityColumns+` FROM activities WHERE id = $1 FOR UPDATE`, activityID))
	if err != nil {
		return Activity{}, err
	}
	return activity, nil
}

type groupTxQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type groupParticipant struct {
	ID           int64
	Gender       string
	Choices      []matching.Selection
	Name         string
	Nickname     string
	LegacyUserID string
	LegacyOpenID string
	Attendance   string
	CheckedIn    bool
}

// loadGroupParticipants reads the valid selection rows. Gender comes from the
// stored selection (legacy parity); rows with an unusable gender are skipped.
func loadGroupParticipants(ctx context.Context, q groupTxQuerier, activityID int64) (
	[]groupParticipant, map[int64][]matching.Selection, int, error) {
	rows, err := q.Query(ctx,
		`SELECT gs.selector_id, gs.gender, gs.choices,
		        COALESCE(u.name, ''), COALESCE(u.nickname, ''),
		        COALESCE(u.legacy_user_id, ''), COALESCE(u.legacy_open_id, ''),
		        COALESCE(s.attendance, '')
		   FROM group_selections gs
		   JOIN users u ON u.id = gs.selector_id
		   LEFT JOIN signups s
		     ON s.activity_id = gs.activity_id AND s.user_id = gs.selector_id
		    AND s.status <> $2
		  WHERE gs.activity_id = $1
		  ORDER BY gs.selector_id`,
		activityID, SignupStatusCancelled)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("db: load grouping participants: %w", err)
	}
	defer rows.Close()

	participants := []groupParticipant{}
	selections := map[int64][]matching.Selection{}
	skipped := 0
	for rows.Next() {
		var row groupParticipant
		var rawChoices []byte
		if err := rows.Scan(&row.ID, &row.Gender, &rawChoices, &row.Name,
			&row.Nickname, &row.LegacyUserID, &row.LegacyOpenID, &row.Attendance); err != nil {
			return nil, nil, 0, fmt.Errorf("db: scan grouping participant: %w", err)
		}
		row.CheckedIn = IsCheckedIn(row.Attendance)
		gender := strings.TrimSpace(row.Gender)
		if gender != "男性" && gender != "女性" {
			skipped++
			continue
		}
		ids, err := decodeChoiceIDs(rawChoices)
		if err != nil {
			return nil, nil, 0, err
		}
		choices := make([]matching.Selection, 0, len(ids))
		for i, id := range ids {
			choices = append(choices, matching.Selection{ID: id, Priority: i + 1})
		}
		if len(choices) > 0 {
			selections[row.ID] = choices
		}
		row.Gender = gender
		participants = append(participants, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("db: iterate grouping participants: %w", err)
	}
	return participants, selections, skipped, nil
}

func matchingGender(gender string) string {
	if gender == "男性" {
		return "male"
	}
	return "female"
}

func chineseGender(gender string) string {
	if gender == "male" {
		return "男性"
	}
	return "女性"
}

func participantDisplayName(row groupParticipant) string {
	if strings.TrimSpace(row.Name) != "" {
		return row.Name
	}
	if strings.TrimSpace(row.Nickname) != "" {
		return row.Nickname
	}
	return row.LegacyUserID
}

func countGenders(participants []groupParticipant, signedInOnly bool) (males, females int) {
	for _, participant := range participants {
		if signedInOnly && !participant.CheckedIn {
			continue
		}
		switch participant.Gender {
		case "男性":
			males++
		case "女性":
			females++
		}
	}
	return males, females
}

func groupingPool(participants []groupParticipant, signinEnabled bool) []groupParticipant {
	if !signinEnabled {
		return participants
	}
	out := make([]groupParticipant, 0, len(participants))
	for _, participant := range participants {
		if participant.CheckedIn {
			out = append(out, participant)
		}
	}
	return out
}

func defaultGroupSizes(activity Activity, male, female int) (int, int) {
	if male <= 0 {
		male = activity.MalePerGroup
	}
	if female <= 0 {
		female = activity.FemalePerGroup
	}
	return male, female
}

// PreviewGrouping reports the run-time pool and blockers without writing
// anything. Counts after the sign-in filter drive the blocker decision so the
// preview matches what RunGrouping will actually do.
func (d *DB) PreviewGrouping(ctx context.Context, activityRef string, round, male, female int) (GroupPreview, error) {
	activity, err := d.GetActivity(ctx, activityRef)
	if err != nil {
		return GroupPreview{}, err
	}
	male, female = defaultGroupSizes(activity, male, female)
	if round < 0 {
		return GroupPreview{}, ErrGroupRoundInvalid
	}
	if round == 0 {
		latest, err := d.LatestGroupRound(ctx, activity.ActivityNo)
		if err != nil {
			return GroupPreview{}, err
		}
		round = latest + 1
	}
	participants, _, skipped, err := loadGroupParticipants(ctx, d.pool, activity.ID)
	if err != nil {
		return GroupPreview{}, err
	}
	selectionMales, selectionFemales := countGenders(participants, false)
	poolMales, poolFemales := countGenders(participants, activity.SigninEnabled)
	rounds, err := d.groupRounds(ctx, activity.ID)
	if err != nil {
		return GroupPreview{}, err
	}
	var existingRoundResults, historical int
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE round = $2), count(*)
		   FROM group_results WHERE activity_id = $1`,
		activity.ID, round).Scan(&existingRoundResults, &historical); err != nil {
		return GroupPreview{}, fmt.Errorf("db: count grouping results: %w", err)
	}

	preview := GroupPreview{
		ActivityID:           activity.ID,
		ActivityNo:           activity.ActivityNo,
		ActivityName:         activity.Name,
		GroupStatus:          activity.GroupStatus,
		Round:                round,
		MalesPerGroup:        male,
		FemalesPerGroup:      female,
		MaleCount:            selectionMales,
		FemaleCount:          selectionFemales,
		CheckedInMaleCount:   poolMales,
		CheckedInFemaleCount: poolFemales,
		ParticipantCount:     selectionMales + selectionFemales,
		SkippedNoGender:      skipped,
		ExistingRounds:       rounds,
		ExistingRoundResults: existingRoundResults,
		HistoricalResults:    historical,
		Blockers:             []string{},
	}
	switch {
	case male < 1 || female < 1:
		preview.Blockers = append(preview.Blockers,
			fmt.Sprintf("每组男女人数必须大于 0（目前 %d 男 %d 女）", male, female))
	case poolMales < male || poolFemales < female:
		preview.Blockers = append(preview.Blockers,
			fmt.Sprintf("人数不足：目前 %d 男 %d 女，每组需要 %d 男 %d 女",
				poolMales, poolFemales, male, female))
	}
	if existingRoundResults > 0 {
		preview.Blockers = append(preview.Blockers,
			fmt.Sprintf("第 %d 轮已生成分组结果，请勿重复执行", round))
	}
	if activity.OnetooneEnabled {
		preview.Blockers = append(preview.Blockers, "一对一功能已开启，分组与一对一互斥")
	}
	if male > 0 && female > 0 && poolMales >= male && poolFemales >= female {
		byMales := poolMales / male
		byFemales := poolFemales / female
		if byFemales < byMales {
			preview.ExpectedGroups = byFemales
		} else {
			preview.ExpectedGroups = byMales
		}
	}
	preview.CanExecute = len(preview.Blockers) == 0
	return preview, nil
}

// RunGrouping executes one round atomically. The advisory lock, the same-round
// existence check and the inserts all live in one transaction, so concurrent
// double clicks cannot produce duplicate groups or partial state.
func (d *DB) RunGrouping(ctx context.Context, activityRef string, round, male, female int) (GroupRunResult, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return GroupRunResult{}, fmt.Errorf("db: begin grouping: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	activity, err := d.lockActivityTx(ctx, tx, activityRef, DefaultGroupLockTimeout)
	if err != nil {
		return GroupRunResult{}, err
	}
	male, female = defaultGroupSizes(activity, male, female)
	if male < 1 || female < 1 {
		return GroupRunResult{}, ErrGroupSizeInvalid
	}
	if activity.OnetooneEnabled {
		return GroupRunResult{}, ErrGroupModeConflict
	}
	if round < 0 {
		return GroupRunResult{}, ErrGroupRoundInvalid
	}
	if round == 0 {
		var latest int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(round), 0) FROM group_results WHERE activity_id = $1`,
			activity.ID).Scan(&latest); err != nil {
			return GroupRunResult{}, fmt.Errorf("db: load latest grouping round: %w", err)
		}
		round = latest + 1
	}
	if round < 1 {
		return GroupRunResult{}, ErrGroupRoundInvalid
	}

	var existing int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM group_results WHERE activity_id = $1 AND round = $2`,
		activity.ID, round).Scan(&existing); err != nil {
		return GroupRunResult{}, fmt.Errorf("db: check grouping round: %w", err)
	}
	if existing > 0 {
		return GroupRunResult{}, ErrGroupAlreadyDone
	}

	participants, selections, _, err := loadGroupParticipants(ctx, tx, activity.ID)
	if err != nil {
		return GroupRunResult{}, err
	}
	pool := groupingPool(participants, activity.SigninEnabled)
	males, females := countGenders(pool, false)
	if males < male || females < female {
		return GroupRunResult{}, &GroupNotEnoughPeopleError{
			Males: males, Females: females, NeedM: male, NeedF: female,
		}
	}

	algorithmParticipants := make([]matching.Participant, 0, len(pool))
	for _, participant := range pool {
		algorithmParticipants = append(algorithmParticipants,
			matching.Participant{ID: participant.ID, Gender: matchingGender(participant.Gender)})
	}
	groups, err := matching.RunGroupingAlgorithm(algorithmParticipants, selections, male, female, nil)
	if err != nil {
		return GroupRunResult{}, err
	}
	if len(groups) == 0 {
		return GroupRunResult{}, ErrGroupEmpty
	}

	byID := make(map[int64]groupParticipant, len(participants))
	for _, participant := range participants {
		byID[participant.ID] = participant
	}
	result := GroupRunResult{
		ActivityID:       activity.ID,
		ActivityNo:       activity.ActivityNo,
		ActivityName:     activity.Name,
		Round:            round,
		MalesPerGroup:    male,
		FemalesPerGroup:  female,
		GroupCount:       len(groups),
		ParticipantCount: len(pool),
		Groups:           make([]AdminGroup, 0, len(groups)),
	}
	for _, group := range groups {
		adminGroup := AdminGroup{GroupID: group.GroupID, Members: []GroupMember{}}
		for _, id := range group.Members() {
			participant := byID[id]
			gender := chineseGender(matchingGender(participant.Gender))
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_results
				    (activity_id, group_no, user_id, user_name, gender, round)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				activity.ID, group.GroupID, id, participantDisplayName(participant),
				gender, round); err != nil {
				return GroupRunResult{}, fmt.Errorf("db: insert group result: %w", err)
			}
			adminGroup.Members = append(adminGroup.Members, GroupMember{
				UserID:       id,
				LegacyUserID: participant.LegacyUserID,
				LegacyOpenID: participant.LegacyOpenID,
				Nickname:     participant.Nickname,
				Name:         participant.Name,
				UserName:     participantDisplayName(participant),
				Gender:       gender,
			})
		}
		result.Groups = append(result.Groups, adminGroup)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities
		    SET group_status = '已完成', male_per_group = $2, female_per_group = $3,
		        updated_at = now()
		  WHERE id = $1`, activity.ID, male, female); err != nil {
		return GroupRunResult{}, fmt.Errorf("db: mark grouping complete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GroupRunResult{}, fmt.Errorf("db: commit grouping: %w", err)
	}
	return result, nil
}

// ResetGrouping deletes every round and clears the activity status. Repeating
// it is safe; the returned count is the number of deleted result rows.
func (d *DB) ResetGrouping(ctx context.Context, activityRef string) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin grouping reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	activity, err := d.lockActivityTx(ctx, tx, activityRef, DefaultGroupLockTimeout)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM group_results WHERE activity_id = $1`, activity.ID)
	if err != nil {
		return 0, fmt.Errorf("db: delete group results: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities SET group_status = '', updated_at = now() WHERE id = $1`,
		activity.ID); err != nil {
		return 0, fmt.Errorf("db: clear grouping status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit grouping reset: %w", err)
	}
	return tag.RowsAffected(), nil
}
