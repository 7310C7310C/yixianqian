package db

import (
	"context"
	"fmt"
	"time"
)

// MatchingRosterEntry is one active signup enriched with its volunteer
// submission state. It carries only the fields the matching console displays.
type MatchingRosterEntry struct {
	ID         int64
	UserID     string
	Name       string
	Nickname   string
	Gender     string
	Attendance string
	CheckedIn  bool
	Submitted  bool
	SignupAt   time.Time
}

// MatchingRoster is the full active signup list plus the counts the matching
// console shows. Cancelled signups are history and never included.
type MatchingRoster struct {
	Entries              []MatchingRosterEntry
	Total                int
	Submitted            int
	Unsubmitted          int
	CheckedIn            int
	UnsubmittedCheckedIn int
}

// MatchingRoster returns every active signup for one activity, with
// group_selections joined so the console can split submitted and unsubmitted
// people without a second query.
func (d *DB) MatchingRoster(ctx context.Context, activityID int64) (MatchingRoster, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT s.user_id, COALESCE(u.legacy_user_id, ''), COALESCE(u.name, ''),
		       COALESCE(u.nickname, ''), COALESCE(u.gender, ''),
		       COALESCE(s.attendance, ''), (gs.selector_id IS NOT NULL),
		       s.created_at
		  FROM signups s
		  JOIN users u ON u.id = s.user_id
		  LEFT JOIN group_selections gs
		    ON gs.activity_id = s.activity_id AND gs.selector_id = s.user_id
		 WHERE s.activity_id = $1 AND s.status = $2
		 ORDER BY s.created_at, s.id`, activityID, SignupStatusSigned)
	if err != nil {
		return MatchingRoster{}, fmt.Errorf("db: load matching roster: %w", err)
	}
	defer rows.Close()

	out := MatchingRoster{Entries: make([]MatchingRosterEntry, 0)}
	for rows.Next() {
		var entry MatchingRosterEntry
		var attendance string
		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.Name, &entry.Nickname,
			&entry.Gender, &attendance, &entry.Submitted, &entry.SignupAt); err != nil {
			return MatchingRoster{}, fmt.Errorf("db: scan matching roster: %w", err)
		}
		entry.Attendance = attendance
		entry.CheckedIn = IsCheckedIn(attendance)
		out.Entries = append(out.Entries, entry)
		out.Total++
		if entry.Submitted {
			out.Submitted++
		} else {
			out.Unsubmitted++
			if entry.CheckedIn {
				out.UnsubmittedCheckedIn++
			}
		}
		if entry.CheckedIn {
			out.CheckedIn++
		}
	}
	if err := rows.Err(); err != nil {
		return MatchingRoster{}, fmt.Errorf("db: iterate matching roster: %w", err)
	}
	return out, nil
}

// LatestOnetooneFemaleChats returns the highest female rank in the newest
// one-to-one round. It doubles as the console's default K when there is
// history; no results yields 0 so callers can fall back to 3.
func (d *DB) LatestOnetooneFemaleChats(ctx context.Context, activityID int64) (int, error) {
	var chats int
	err := d.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(rank), 0)
		  FROM onetoone_results
		 WHERE activity_id = $1 AND gender = '女性'
		   AND round = (
		       SELECT COALESCE(MAX(round), 0)
		         FROM onetoone_results
		        WHERE activity_id = $1
		   )`, activityID).Scan(&chats)
	if err != nil {
		return 0, fmt.Errorf("db: latest onetoone female chats: %w", err)
	}
	return chats, nil
}
