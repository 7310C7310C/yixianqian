package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// NotifySignupLikes ports v1 auto_notify_signup: after a signup commits, tell
// the people who liked the signup user ("你喜欢的 X 报名了 Y") and the people
// the signup user liked ("喜欢你的人报名了 Y"). The second direction stays
// anonymous: the recipient never learns who signed up.
//
// Dedupe keys include the signup row id, so cancelling and signing up again
// does not spam the same recipients. A mutual pair only gets the named notice.
func (d *DB) NotifySignupLikes(ctx context.Context, signupID, userID, activityID int64) (int, error) {
	if signupID <= 0 || userID <= 0 || activityID <= 0 {
		return 0, nil
	}
	var activityNo, activityName string
	err := d.pool.QueryRow(ctx,
		`SELECT activity_no, name FROM activities WHERE id = $1`, activityID).
		Scan(&activityNo, &activityName)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("db: load signup activity for notify: %w", err)
	}
	var nickname string
	if err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name) FROM users WHERE id = $1`, userID).
		Scan(&nickname); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("db: load signup user for notify: %w", err)
	}

	rows, err := d.pool.Query(ctx,
		`SELECT initiator_id, target_id, status, like_type, COALESCE(month, '')
		   FROM likes
		  WHERE status = ANY($1) AND (initiator_id = $2 OR target_id = $2)`,
		[]string{LikeStatusSingle, LikeStatusMutual}, userID)
	if err != nil {
		return 0, fmt.Errorf("db: list signup likes: %w", err)
	}
	currentMonth := MonthKey(SocialNow())
	likers := make(map[int64]bool) // users who liked the signup user
	liked := make(map[int64]bool)  // users the signup user liked
	for rows.Next() {
		var initiatorID, targetID int64
		var status, likeType, month string
		if err := rows.Scan(&initiatorID, &targetID, &status, &likeType, &month); err != nil {
			rows.Close()
			return 0, fmt.Errorf("db: scan signup like: %w", err)
		}
		if !IsLikeActive(status, likeType, month, currentMonth) {
			continue
		}
		if targetID == userID && initiatorID != userID {
			likers[initiatorID] = true
		}
		if initiatorID == userID && targetID != userID {
			liked[targetID] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("db: iterate signup likes: %w", err)
	}

	sent := 0
	insert := func(recipientID int64, anonymous bool, dedupeKey string) error {
		title := fmt.Sprintf("你喜欢的 %s 报名了 %s 活动", socialDisplayName(nickname), activityName)
		body := "你也去看看吧，说不定能线下偶遇。"
		payload := map[string]any{
			"activity_id":   activityNo,
			"activity_name": activityName,
			"anonymous":     false,
		}
		if anonymous {
			title = fmt.Sprintf("喜欢你的人报名了 %s 活动", activityName)
			body = "你也去看看吧，万一你也喜欢 TA 呢。"
			// Never carry the signup user's id or nickname in the anonymous
			// notice; the payload is rendered by the SPA too.
			payload = map[string]any{
				"activity_id":   activityNo,
				"activity_name": activityName,
				"anonymous":     true,
			}
		}
		created, err := d.CreateNotificationOnce(ctx, NotificationInput{
			UserID:    recipientID,
			Kind:      "signup",
			Title:     title,
			Body:      body,
			Link:      "/activities/" + activityNo,
			Payload:   payload,
			DedupeKey: dedupeKey,
		})
		if err != nil {
			return err
		}
		if created {
			sent++
		}
		return nil
	}

	for likerID := range likers {
		if err := insert(likerID, false, fmt.Sprintf("signup:%d:%d:real", signupID, likerID)); err != nil {
			return sent, err
		}
	}
	for targetID := range liked {
		if likers[targetID] {
			continue // mutual: the named notice wins
		}
		if err := insert(targetID, true, fmt.Sprintf("signup:%d:%d:anon", signupID, targetID)); err != nil {
			return sent, err
		}
	}
	return sent, nil
}
