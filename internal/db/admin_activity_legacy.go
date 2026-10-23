package db

import (
	"context"
	"fmt"
)

// AdminActivityCounts aggregates the per-activity numbers the legacy activity
// console renders. It is a read-only projection: the console must not derive
// counts from the user-facing activity rows because submitted/unsubmitted
// splits live in group_selections and onetoone_results.
type AdminActivityCounts struct {
	ActivityID     int64
	SignupCount    int
	SubmittedCount int
	MaleCount      int
	FemaleCount    int
	// UnsubmittedCheckedIn counts active signups without a volunteer row whose
	// attendance is on-time or late (the on-site gate).
	UnsubmittedCheckedIn int
	GroupRounds          []int
	OnetooneResultCount  int
	OnetooneMatchedCount int
	OnetooneRounds       []int
}

// AdminListActivities returns every activity including cancelled ones, ordered
// by activity number. The public ListActivities hides cancelled rows, but the
// admin console must still find them to correct their state.
func (d *DB) AdminListActivities(ctx context.Context) ([]Activity, error) {
	rows, err := d.pool.Query(ctx,
		"SELECT "+activityColumns+` FROM activities ORDER BY activity_no, id`)
	if err != nil {
		return nil, fmt.Errorf("db: list admin activities: %w", err)
	}
	defer rows.Close()

	out := make([]Activity, 0)
	for rows.Next() {
		activity, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin activities: %w", err)
	}
	return out, nil
}

// AdminActivityCountsByID aggregates counts for the given activities. An empty
// id list aggregates every activity. Missing rows are not created: callers
// that need zero values for an activity should seed them.
func (d *DB) AdminActivityCountsByID(ctx context.Context, activityIDs []int64) (map[int64]AdminActivityCounts, error) {
	out := make(map[int64]AdminActivityCounts)
	filter := ""
	args := []any{SignupStatusSigned, string(AttendanceOnTime), string(AttendanceLate)}
	if len(activityIDs) > 0 {
		filter = " AND s.activity_id = ANY($4)"
		args = append(args, activityIDs)
	}
	rows, err := d.pool.Query(ctx, `
		SELECT s.activity_id,
		       count(*),
		       count(*) FILTER (WHERE gs.selector_id IS NOT NULL),
		       count(*) FILTER (WHERE gs.selector_id IS NOT NULL AND gs.gender = '男性'),
		       count(*) FILTER (WHERE gs.selector_id IS NOT NULL AND gs.gender = '女性'),
		       count(*) FILTER (WHERE gs.selector_id IS NULL AND COALESCE(s.attendance, '') IN ($2, $3))
		  FROM signups s
		  LEFT JOIN group_selections gs
		    ON gs.activity_id = s.activity_id AND gs.selector_id = s.user_id
		 WHERE s.status = $1`+filter+`
		 GROUP BY s.activity_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("db: aggregate admin activity counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item AdminActivityCounts
		if err := rows.Scan(&item.ActivityID, &item.SignupCount, &item.SubmittedCount,
			&item.MaleCount, &item.FemaleCount, &item.UnsubmittedCheckedIn); err != nil {
			return nil, fmt.Errorf("db: scan admin activity counts: %w", err)
		}
		item.GroupRounds = []int{}
		item.OnetooneRounds = []int{}
		out[item.ActivityID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin activity counts: %w", err)
	}

	if err := d.fillGroupRounds(ctx, activityIDs, out); err != nil {
		return nil, err
	}
	if err := d.fillOnetooneCounts(ctx, activityIDs, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) fillGroupRounds(ctx context.Context, activityIDs []int64,
	out map[int64]AdminActivityCounts) error {
	query := `SELECT activity_id, round FROM group_results`
	args := []any{}
	if len(activityIDs) > 0 {
		query += ` WHERE activity_id = ANY($1)`
		args = append(args, activityIDs)
	}
	query += ` GROUP BY activity_id, round ORDER BY activity_id, round`
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("db: aggregate admin group rounds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var activityID int64
		var round int
		if err := rows.Scan(&activityID, &round); err != nil {
			return fmt.Errorf("db: scan admin group round: %w", err)
		}
		item := out[activityID]
		item.ActivityID = activityID
		if item.GroupRounds == nil {
			item.GroupRounds = []int{}
		}
		if item.OnetooneRounds == nil {
			item.OnetooneRounds = []int{}
		}
		item.GroupRounds = append(item.GroupRounds, round)
		out[activityID] = item
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: iterate admin group rounds: %w", err)
	}
	return nil
}

func (d *DB) fillOnetooneCounts(ctx context.Context, activityIDs []int64,
	out map[int64]AdminActivityCounts) error {
	filter := ""
	args := []any{}
	if len(activityIDs) > 0 {
		filter = ` WHERE activity_id = ANY($1)`
		args = append(args, activityIDs)
	}
	rows, err := d.pool.Query(ctx, `
		SELECT activity_id, count(*), count(DISTINCT user_id)
		  FROM onetoone_results`+filter+`
		 GROUP BY activity_id`, args...)
	if err != nil {
		return fmt.Errorf("db: aggregate admin onetoone counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var activityID int64
		var resultCount, matchedCount int
		if err := rows.Scan(&activityID, &resultCount, &matchedCount); err != nil {
			return fmt.Errorf("db: scan admin onetoone counts: %w", err)
		}
		item := out[activityID]
		item.ActivityID = activityID
		if item.GroupRounds == nil {
			item.GroupRounds = []int{}
		}
		if item.OnetooneRounds == nil {
			item.OnetooneRounds = []int{}
		}
		item.OnetooneResultCount = resultCount
		item.OnetooneMatchedCount = matchedCount
		out[activityID] = item
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: iterate admin onetoone counts: %w", err)
	}

	roundQuery := `SELECT activity_id, round FROM onetoone_results` + filter + `
		 GROUP BY activity_id, round ORDER BY activity_id, round`
	roundRows, err := d.pool.Query(ctx, roundQuery, args...)
	if err != nil {
		return fmt.Errorf("db: aggregate admin onetoone rounds: %w", err)
	}
	defer roundRows.Close()
	for roundRows.Next() {
		var activityID int64
		var round int
		if err := roundRows.Scan(&activityID, &round); err != nil {
			return fmt.Errorf("db: scan admin onetoone round: %w", err)
		}
		item := out[activityID]
		item.ActivityID = activityID
		if item.GroupRounds == nil {
			item.GroupRounds = []int{}
		}
		if item.OnetooneRounds == nil {
			item.OnetooneRounds = []int{}
		}
		item.OnetooneRounds = append(item.OnetooneRounds, round)
		out[activityID] = item
	}
	if err := roundRows.Err(); err != nil {
		return fmt.Errorf("db: iterate admin onetoone rounds: %w", err)
	}
	return nil
}

// LegacyActivityMode derives the single mode value the v1 console expects from
// the two independent entry flags. Both flags on is the legacy conflict state,
// not an error: the console offers a one-click fix.
func LegacyActivityMode(groupEnabled, onetooneEnabled bool) string {
	switch {
	case groupEnabled && onetooneEnabled:
		return "conflict"
	case groupEnabled:
		return "group"
	case onetooneEnabled:
		return "onetoone"
	default:
		return "none"
	}
}
