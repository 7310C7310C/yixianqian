package db

import (
	"context"
	"fmt"
)

type mutualWechatPair struct {
	InitiatorID     int64
	TargetID        int64
	InitiatorName   string
	InitiatorWechat string
	TargetName      string
	TargetWechat    string
}

// mutualWechatDedupeKey mirrors the key used by CreateNotificationOnce callers
// in the httpserver package: mutual-wechat:<low>:<high>:<recipient>.
func mutualWechatDedupeKey(a, b, recipient int64) string {
	low, high := a, b
	if low > high {
		low, high = high, low
	}
	return fmt.Sprintf("mutual-wechat:%d:%d:%d", low, high, recipient)
}

// BackfillMutualWechatNotifications creates the one-time "exchange wechat"
// notification for ready mutual pairs that are still missing it on at least one
// side. It is idempotent (CreateNotificationOnce dedupes by key), bounded by
// limit, and exists so a missed write-path trigger can never lose the notice.
func (d *DB) BackfillMutualWechatNotifications(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := d.pool.Query(ctx,
		`SELECT l.initiator_id, l.target_id,
		        COALESCE(NULLIF(u1.nickname, ''), '一位会员'), COALESCE(u1.wechat, ''),
		        COALESCE(NULLIF(u2.nickname, ''), '一位会员'), COALESCE(u2.wechat, '')
		   FROM likes l
		   JOIN users u1 ON u1.id = l.initiator_id
		   JOIN users u2 ON u2.id = l.target_id
		  WHERE l.status = $2
		    AND COALESCE(u1.wechat, '') <> '' AND u1.contact_consent
		    AND COALESCE(u2.wechat, '') <> '' AND u2.contact_consent
		    AND (
		      NOT EXISTS (
		        SELECT 1 FROM notifications n
		         WHERE n.user_id = l.initiator_id
		           AND n.payload->>'dedupe_key' = 'mutual-wechat:' ||
		               LEAST(l.initiator_id, l.target_id) || ':' ||
		               GREATEST(l.initiator_id, l.target_id) || ':' || l.initiator_id)
		      OR NOT EXISTS (
		        SELECT 1 FROM notifications n
		         WHERE n.user_id = l.target_id
		           AND n.payload->>'dedupe_key' = 'mutual-wechat:' ||
		               LEAST(l.initiator_id, l.target_id) || ':' ||
		               GREATEST(l.initiator_id, l.target_id) || ':' || l.target_id)
		    )
		  ORDER BY l.id
		  LIMIT $1`, limit, LikeStatusMutual)
	if err != nil {
		return 0, fmt.Errorf("db: list mutual wechat pairs: %w", err)
	}
	var pairs []mutualWechatPair
	for rows.Next() {
		var p mutualWechatPair
		if err := rows.Scan(&p.InitiatorID, &p.TargetID, &p.InitiatorName,
			&p.InitiatorWechat, &p.TargetName, &p.TargetWechat); err != nil {
			rows.Close()
			return 0, fmt.Errorf("db: scan mutual wechat pair: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("db: iterate mutual wechat pairs: %w", err)
	}
	rows.Close()

	created := 0
	for _, p := range pairs {
		sides := []struct {
			recipient   int64
			otherID     int64
			otherName   string
			otherWechat string
		}{
			{p.InitiatorID, p.TargetID, p.TargetName, p.TargetWechat},
			{p.TargetID, p.InitiatorID, p.InitiatorName, p.InitiatorWechat},
		}
		for _, side := range sides {
			inserted, err := d.CreateNotificationOnce(ctx, NotificationInput{
				UserID: side.recipient,
				Kind:   "wechat_exchange",
				Title:  "已交换微信号",
				Body: fmt.Sprintf("你和 %s 已相互同意交换微信号：%s，快去加好友吧。",
					side.otherName, side.otherWechat),
				DedupeKey: mutualWechatDedupeKey(p.InitiatorID, p.TargetID, side.recipient),
				Payload:   map[string]any{"other_id": side.otherID},
			})
			if err != nil {
				return created, fmt.Errorf("db: backfill mutual wechat notification: %w", err)
			}
			if inserted {
				created++
			}
		}
	}
	return created, nil
}
