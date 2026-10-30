package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Message statuses mirror the legacy Bitable select values so migrated rows and
// API clients keep working unchanged.
const (
	MessageStatusNormal   = "正常"
	MessageStatusDeleted  = "已删除"
	MessageStatusReported = "已举报"

	ReportStatusPending  = "待处理"
	ReportStatusApproved = "已同意"
	ReportStatusRejected = "已驳回"

	// MessageMaxRunes matches the legacy 500-character limit.
	MessageMaxRunes = 500
)

var (
	ErrMessageDeleted   = errors.New("db: message is deleted")
	ErrCannotReportSelf = errors.New("db: cannot report own message")
)

// Message is one comment left on a target user's card.
type Message struct {
	ID                 int64
	TargetUserID       int64
	TargetName         string
	TargetLegacyID     string
	TargetLegacyOpenID string
	AuthorID           int64
	AuthorName         string
	AuthorLegacyID     string
	AuthorLegacyOpenID string
	ParentID           *int64
	ReplyToID          *int64
	ReplyToName        string
	ReplyToLegacyID    string
	Content            string
	Status             string
	CreatedAt          time.Time
}

// ReceivedMessage is one message in a user's inbox plus its per-user unread
// state. A message can be received because it is on the user's card or because
// it replies to a message the user wrote elsewhere.
type ReceivedMessage struct {
	Message
	Unread  bool
	Replied bool
}

// Report is one user-submitted report against a message.
type Report struct {
	ID                int64
	ReporterID        *int64
	ReporterName      string
	TargetUserID      *int64
	TargetName        string
	TargetMessageID   *int64
	Reason            string
	Status            string
	HandledBy         *int64
	HandledByName     string
	HandledAt         *time.Time
	CreatedAt         time.Time
	MessageAuthorID   *int64
	MessageAuthorName string
	MessageContent    string
	MessageStatus     string
	MessageCreatedAt  *time.Time
}

// CreateMessageParams carries a validated comment request.
type CreateMessageParams struct {
	TargetUserID int64
	AuthorID     int64
	Content      string
	ParentID     *int64
	ReplyToID    *int64
}

// ReportMessageParams carries one report submission.
type ReportMessageParams struct {
	MessageID  int64
	ReporterID int64
	Reason     string
}

const messageSelect = `
SELECT m.id, m.target_user_id,
       COALESCE(NULLIF(t.nickname, ''), t.name), COALESCE(t.legacy_user_id, ''),
       COALESCE(t.legacy_open_id, ''),
       m.author_id,
       COALESCE(NULLIF(m.author_name, ''), NULLIF(a.nickname, ''), a.name),
       COALESCE(a.legacy_user_id, ''), COALESCE(a.legacy_open_id, ''),
       m.parent_id, m.reply_to_id,
       COALESCE(NULLIF(r.author_name, ''), NULLIF(ra.nickname, ''), ra.name, ''),
       COALESCE(ra.legacy_user_id, ''),
       m.content, m.status, m.created_at
FROM messages m
JOIN users t ON t.id = m.target_user_id
JOIN users a ON a.id = m.author_id
LEFT JOIN messages r ON r.id = m.reply_to_id
LEFT JOIN users ra ON ra.id = r.author_id`

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.TargetUserID, &m.TargetName, &m.TargetLegacyID,
		&m.TargetLegacyOpenID, &m.AuthorID, &m.AuthorName, &m.AuthorLegacyID,
		&m.AuthorLegacyOpenID, &m.ParentID, &m.ReplyToID, &m.ReplyToName,
		&m.ReplyToLegacyID, &m.Content, &m.Status, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return m, nil
}

// FindUserByRef resolves a numeric user id or a legacy user/open id.
func (d *DB) FindUserByRef(ctx context.Context, ref string) (User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return User{}, ErrNotFound
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return d.GetUserByID(ctx, id)
	}
	// A few migrated accounts share a legacy_open_id (e.g. a real user and
	// their observer shadow). Prefer the exact legacy_user_id, then the
	// non-observer account, then the oldest row so a card ref always resolves
	// to the person the UI meant.
	return scanUser(d.pool.QueryRow(ctx,
		"SELECT "+userColumns+` FROM users
		  WHERE legacy_user_id = $1 OR legacy_open_id = $1
		  ORDER BY (legacy_user_id = $1) DESC,
		           (COALESCE(account_kind, '') <> 'observer') DESC,
		           id ASC
		  LIMIT 1`, ref))
}

// DisplayName returns the product-facing name (nickname, then real name).
func (d *DB) DisplayName(ctx context.Context, userID int64) (string, error) {
	var name string
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name) FROM users WHERE id = $1`, userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: display name: %w", err)
	}
	return name, nil
}

// GetAccountStatus returns the business state used by product gates.
func (d *DB) GetAccountStatus(ctx context.Context, userID int64) (string, error) {
	var status string
	err := d.pool.QueryRow(ctx,
		`SELECT account_status FROM users WHERE id = $1`, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: get account status: %w", err)
	}
	return status, nil
}

// GetMessageByID loads one message even when it has been soft-deleted; callers
// decide whether deleted rows are visible.
func (d *DB) GetMessageByID(ctx context.Context, id int64) (Message, error) {
	return scanMessage(d.pool.QueryRow(ctx, messageSelect+" WHERE m.id = $1", id))
}

// ListMessages returns the visible (non-deleted) comments for one card,
// oldest first so threads render in conversation order.
func (d *DB) ListMessages(ctx context.Context, targetUserID int64) ([]Message, error) {
	rows, err := d.pool.Query(ctx,
		messageSelect+" WHERE m.target_user_id = $1 AND m.status <> $2 ORDER BY m.created_at ASC, m.id ASC",
		targetUserID, MessageStatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("db: list messages: %w", err)
	}
	defer rows.Close()
	return collectMessages(rows)
}

// CountMessagesByAuthor counts the current user's visible sent messages.
func (d *DB) CountMessagesByAuthor(ctx context.Context, authorID int64) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE author_id = $1 AND status <> $2`,
		authorID, MessageStatusDeleted).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: count author messages: %w", err)
	}
	return count, nil
}

// CountMessagesReceived counts visible comments left on one user's card.
func (d *DB) CountMessagesReceived(ctx context.Context, targetUserID int64) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE target_user_id = $1 AND status <> $2`,
		targetUserID, MessageStatusDeleted).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: count received messages: %w", err)
	}
	return count, nil
}

// ListMessagesByAuthor returns the current user's sent messages, newest first.
func (d *DB) ListMessagesByAuthor(ctx context.Context, authorID int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx,
		messageSelect+" WHERE m.author_id = $1 AND m.status <> $2 ORDER BY m.created_at DESC, m.id DESC LIMIT $3",
		authorID, MessageStatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list author messages: %w", err)
	}
	defer rows.Close()
	return collectMessages(rows)
}

// receivedMessageWhere is the inbox predicate. A user receives a message when
// someone writes on their card, or when someone replies to a message they
// wrote on any card. Own messages and deleted messages never enter the inbox.
//
// $1 = viewer user id, $2 = deleted status.
const receivedMessageWhere = `
	m.status <> $2
	AND m.author_id <> $1
	AND (
		m.target_user_id = $1
		OR m.reply_to_id IN (
			SELECT id FROM messages WHERE author_id = $1 AND status <> $2
		)
		OR (
			m.reply_to_id IS NULL
			AND m.parent_id IN (
				SELECT id FROM messages WHERE author_id = $1 AND status <> $2
			)
		)
	)`

const receivedMessageSelect = `
SELECT m.id, m.target_user_id,
       COALESCE(NULLIF(t.nickname, ''), t.name), COALESCE(t.legacy_user_id, ''),
       COALESCE(t.legacy_open_id, ''),
       m.author_id,
       COALESCE(NULLIF(m.author_name, ''), NULLIF(a.nickname, ''), a.name),
       COALESCE(a.legacy_user_id, ''), COALESCE(a.legacy_open_id, ''),
       m.parent_id, m.reply_to_id,
       COALESCE(NULLIF(r.author_name, ''), NULLIF(ra.nickname, ''), ra.name, ''),
       COALESCE(ra.legacy_user_id, ''),
       m.content, m.status, m.created_at,
       NOT EXISTS (
         SELECT 1 FROM message_reads mr
          WHERE mr.message_id = m.id AND mr.user_id = $1
       ) AS unread,
       EXISTS (
         SELECT 1 FROM messages rep
          WHERE rep.reply_to_id = m.id AND rep.author_id = $1 AND rep.status <> $2
       ) AS replied
FROM messages m
JOIN users t ON t.id = m.target_user_id
JOIN users a ON a.id = m.author_id
LEFT JOIN messages r ON r.id = m.reply_to_id
LEFT JOIN users ra ON ra.id = r.author_id`

func scanReceivedMessage(row rowScanner) (ReceivedMessage, error) {
	var m ReceivedMessage
	err := row.Scan(&m.ID, &m.TargetUserID, &m.TargetName, &m.TargetLegacyID,
		&m.TargetLegacyOpenID, &m.AuthorID, &m.AuthorName, &m.AuthorLegacyID,
		&m.AuthorLegacyOpenID, &m.ParentID, &m.ReplyToID, &m.ReplyToName,
		&m.ReplyToLegacyID, &m.Content, &m.Status, &m.CreatedAt, &m.Unread, &m.Replied)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceivedMessage{}, ErrNotFound
	}
	if err != nil {
		return ReceivedMessage{}, err
	}
	return m, nil
}

// ListMessagesReceived returns the current user's inbox, newest first.
func (d *DB) ListMessagesReceived(ctx context.Context, userID int64, limit int) ([]ReceivedMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx,
		receivedMessageSelect+" WHERE "+receivedMessageWhere+
			" ORDER BY m.created_at DESC, m.id DESC LIMIT $3",
		userID, MessageStatusDeleted, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list received messages: %w", err)
	}
	defer rows.Close()
	out := make([]ReceivedMessage, 0)
	for rows.Next() {
		m, err := scanReceivedMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountUnreadReceivedMessages counts inbox rows without a message_reads row.
func (d *DB) CountUnreadReceivedMessages(ctx context.Context, userID int64) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM messages m
		  WHERE `+receivedMessageWhere+`
		    AND NOT EXISTS (
		      SELECT 1 FROM message_reads mr
		       WHERE mr.message_id = m.id AND mr.user_id = $1
		    )`, userID, MessageStatusDeleted).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: count unread received messages: %w", err)
	}
	return count, nil
}

// MarkReceivedMessagesRead marks every current inbox row read for the user.
func (d *DB) MarkReceivedMessagesRead(ctx context.Context, userID int64) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin mark received messages read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`INSERT INTO message_reads (user_id, message_id)
		 SELECT $1, m.id FROM messages m
		  WHERE `+receivedMessageWhere+`
		    AND NOT EXISTS (
		      SELECT 1 FROM message_reads mr
		       WHERE mr.message_id = m.id AND mr.user_id = $1
		    )
		 ON CONFLICT DO NOTHING`, userID, MessageStatusDeleted)
	if err != nil {
		return 0, fmt.Errorf("db: mark received messages read: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if err := notifyUserTx(ctx, tx, userID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit mark received messages read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkThreadMessagesRead marks every message on one card read for the viewer.
// The thread page shows the whole card conversation, so opening it should clear
// both card-inbox rows and replies to the viewer's messages in that thread.
func (d *DB) MarkThreadMessagesRead(ctx context.Context, userID, targetUserID int64) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin mark thread messages read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`INSERT INTO message_reads (user_id, message_id)
		 SELECT $1, m.id FROM messages m
		  WHERE m.target_user_id = $2 AND m.status <> $3 AND m.author_id <> $1
		 ON CONFLICT DO NOTHING`, userID, targetUserID, MessageStatusDeleted)
	if err != nil {
		return 0, fmt.Errorf("db: mark thread messages read: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if err := notifyUserTx(ctx, tx, userID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit mark thread messages read: %w", err)
	}
	return tag.RowsAffected(), nil
}

func collectMessages(rows pgx.Rows) ([]Message, error) {
	out := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// HasAuthoredMessage reports whether the viewer participated in a card thread.
func (d *DB) HasAuthoredMessage(ctx context.Context, authorID, targetUserID int64) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM messages
		   WHERE author_id = $1 AND target_user_id = $2 AND status <> $3
		 )`, authorID, targetUserID, MessageStatusDeleted).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: has authored message: %w", err)
	}
	return exists, nil
}

// CreateMessage inserts a normal comment. Replies are normalised to a single
// parent level and repeated submissions within five seconds are idempotent,
// matching the legacy behaviour.
func (d *DB) CreateMessage(ctx context.Context, p CreateMessageParams) (Message, error) {
	content := strings.TrimSpace(p.Content)
	if content == "" {
		return Message{}, fmt.Errorf("db: message content is required")
	}
	if utf8.RuneCountInString(content) > MessageMaxRunes {
		return Message{}, fmt.Errorf("db: message content is too long")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("db: begin create message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var authorName, authorRef string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name),
		        COALESCE(NULLIF(legacy_open_id, ''), legacy_user_id, '')
		   FROM users WHERE id = $1 AND status = 'active'`,
		p.AuthorID).Scan(&authorName, &authorRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("db: load message author: %w", err)
	}

	var targetName, targetRef string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name),
		        COALESCE(NULLIF(legacy_open_id, ''), legacy_user_id, '')
		   FROM users WHERE id = $1 AND status = 'active'`,
		p.TargetUserID).Scan(&targetName, &targetRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("db: load message target: %w", err)
	}

	// replyAuthorID is the person whose message this submission directly
	// replies to. It is used for the inbox notification on other people's cards.
	var replyAuthorID int64
	parentID, replyToID := p.ParentID, p.ReplyToID
	if replyToID != nil {
		var replyParent *int64
		var replyStatus string
		var replyTarget int64
		err = tx.QueryRow(ctx,
			`SELECT parent_id, status, target_user_id, author_id FROM messages WHERE id = $1`, *replyToID).
			Scan(&replyParent, &replyStatus, &replyTarget, &replyAuthorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		if err != nil {
			return Message{}, fmt.Errorf("db: load reply target: %w", err)
		}
		if replyStatus == MessageStatusDeleted || replyTarget != p.TargetUserID {
			return Message{}, ErrNotFound
		}
		if replyParent != nil {
			parentID = replyParent
		} else {
			id := *replyToID
			parentID = &id
		}
	}
	if parentID != nil {
		var rootParent *int64
		var parentStatus string
		var parentTarget, parentAuthorID int64
		err = tx.QueryRow(ctx,
			`SELECT parent_id, status, target_user_id, author_id FROM messages WHERE id = $1`, *parentID).
			Scan(&rootParent, &parentStatus, &parentTarget, &parentAuthorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		if err != nil {
			return Message{}, fmt.Errorf("db: load parent message: %w", err)
		}
		if parentStatus == MessageStatusDeleted || parentTarget != p.TargetUserID {
			return Message{}, ErrNotFound
		}
		if replyToID == nil {
			replyAuthorID = parentAuthorID
		}
		// Never create a third-level chain: a reply's parent is its thread root.
		if rootParent != nil {
			parentID = rootParent
		}
	}

	// Legacy duplicate suppression: same author/target/content/thread within 5s.
	var existingID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM messages
		 WHERE author_id = $1 AND target_user_id = $2 AND content = $3
		   AND parent_id IS NOT DISTINCT FROM $4
		   AND reply_to_id IS NOT DISTINCT FROM $5
		   AND status = $6
		   AND created_at > now() - interval '5 seconds'
		 ORDER BY id DESC LIMIT 1`,
		p.AuthorID, p.TargetUserID, content, parentID, replyToID, MessageStatusNormal).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Message{}, fmt.Errorf("db: commit idempotent message: %w", err)
		}
		return d.GetMessageByID(ctx, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Message{}, fmt.Errorf("db: check duplicate message: %w", err)
	}

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO messages (target_user_id, author_id, author_name, parent_id, reply_to_id, content, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		p.TargetUserID, p.AuthorID, authorName, parentID, replyToID, content, MessageStatusNormal).Scan(&id)
	if err != nil {
		return Message{}, fmt.Errorf("db: create message: %w", err)
	}

	// Inbox notifications are written in the same transaction as the message:
	// a committed message must never leave the recipient without a feed row.
	excerpt := messageNotificationExcerpt(content)
	link := "/app"
	basePayload := map[string]any{
		"message_id":     id,
		"target_user_id": p.TargetUserID,
		"target_ref":     targetRef,
		"target_name":    targetName,
		"author_id":      p.AuthorID,
		"author_name":    authorName,
		"author_ref":     authorRef,
	}
	notify := func(recipientID int64, kind, title string, onMyCard bool) error {
		payload := make(map[string]any, len(basePayload)+2)
		for k, v := range basePayload {
			payload[k] = v
		}
		eventKey := fmt.Sprintf("message:%d:%d", id, recipientID)
		payload["event_key"] = eventKey
		payload["on_my_card"] = onMyCard
		return insertNotificationTx(ctx, tx, recipientID, kind, title, excerpt, link, payload, eventKey)
	}
	if p.TargetUserID != p.AuthorID {
		if err := notify(p.TargetUserID, "message", authorName+" 在你的卡片留言", true); err != nil {
			return Message{}, err
		}
	}
	if replyAuthorID != 0 && replyAuthorID != p.AuthorID && replyAuthorID != p.TargetUserID {
		if err := notify(replyAuthorID, "message_reply", authorName+" 回复了你的留言", false); err != nil {
			return Message{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("db: commit message: %w", err)
	}
	return d.GetMessageByID(ctx, id)
}

func messageNotificationExcerpt(content string) string {
	const max = 160
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// DeleteMessage soft-deletes a message and every reply below it.
func (d *DB) DeleteMessage(ctx context.Context, messageID int64) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin delete message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE messages SET status = $2 WHERE id = $1 AND status <> $2`,
		messageID, MessageStatusDeleted)
	if err != nil {
		return 0, fmt.Errorf("db: delete message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}

	replies, err := tx.Exec(ctx,
		`WITH RECURSIVE doomed AS (
		   SELECT id FROM messages WHERE parent_id = $1 AND status <> $2
		   UNION ALL
		   SELECT m.id FROM messages m JOIN doomed d ON m.parent_id = d.id WHERE m.status <> $2
		 )
		 UPDATE messages SET status = $2 WHERE id IN (SELECT id FROM doomed)`,
		messageID, MessageStatusDeleted)
	if err != nil {
		return 0, fmt.Errorf("db: delete message replies: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit delete message: %w", err)
	}
	return replies.RowsAffected(), nil
}

// CreateMessageReport records a report and flags the message for moderation.
// If the message is already flagged it returns already=true without inserting
// another report, matching the legacy one-report-per-message behaviour.
func (d *DB) CreateMessageReport(ctx context.Context, p ReportMessageParams) (reportID int64, already bool, err error) {
	reason := strings.TrimSpace(p.Reason)
	if utf8.RuneCountInString(reason) > MessageMaxRunes {
		return 0, false, fmt.Errorf("db: report reason is too long")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("db: begin report message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var authorID, targetUserID int64
	var status string
	err = tx.QueryRow(ctx,
		`SELECT author_id, target_user_id, status FROM messages WHERE id = $1 FOR UPDATE`,
		p.MessageID).Scan(&authorID, &targetUserID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err != nil {
		return 0, false, fmt.Errorf("db: load reported message: %w", err)
	}
	if status == MessageStatusDeleted {
		return 0, false, ErrMessageDeleted
	}
	if status == MessageStatusReported {
		return 0, true, nil
	}
	if authorID == p.ReporterID {
		return 0, false, ErrCannotReportSelf
	}

	var reporterName, targetName string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name) FROM users WHERE id = $1`, p.ReporterID).Scan(&reporterName)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err != nil {
		return 0, false, fmt.Errorf("db: load reporter: %w", err)
	}
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name) FROM users WHERE id = $1`, targetUserID).Scan(&targetName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("db: load reported target: %w", err)
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO reports (reporter_id, target_user_id, target_message_id, reporter_name, target_name, reason)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		p.ReporterID, targetUserID, p.MessageID, reporterName, targetName, reason).Scan(&reportID)
	if err != nil {
		return 0, false, fmt.Errorf("db: create report: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE messages SET status = $2 WHERE id = $1`,
		p.MessageID, MessageStatusReported); err != nil {
		return 0, false, fmt.Errorf("db: flag reported message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("db: commit report: %w", err)
	}
	return reportID, false, nil
}

// ListReports returns reports for the moderation console, newest first.
func (d *DB) ListReports(ctx context.Context, status string, limit int) ([]Report, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx,
		`SELECT rp.id, rp.reporter_id,
		        COALESCE(NULLIF(rp.reporter_name, ''), NULLIF(rep.nickname, ''), rep.name, ''),
		        rp.target_user_id,
		        COALESCE(NULLIF(rp.target_name, ''), NULLIF(tgt.nickname, ''), tgt.name, ''),
		        rp.target_message_id, rp.reason, rp.status,
		        rp.handled_by, COALESCE(NULLIF(h.nickname, ''), h.name, ''),
		        rp.handled_at, rp.created_at,
		        m.author_id,
		        COALESCE(NULLIF(m.author_name, ''), NULLIF(ma.nickname, ''), ma.name, ''),
		        COALESCE(m.content, ''), COALESCE(m.status, ''), m.created_at
		 FROM reports rp
		 LEFT JOIN users rep ON rep.id = rp.reporter_id
		 LEFT JOIN users tgt ON tgt.id = rp.target_user_id
		 LEFT JOIN users h ON h.id = rp.handled_by
		 LEFT JOIN messages m ON m.id = rp.target_message_id
		 LEFT JOIN users ma ON ma.id = m.author_id
		 WHERE ($1 = '' OR rp.status = $1)
		 ORDER BY rp.created_at DESC, rp.id DESC
		 LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list reports: %w", err)
	}
	defer rows.Close()

	out := make([]Report, 0)
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.ReporterName, &r.TargetUserID,
			&r.TargetName, &r.TargetMessageID, &r.Reason, &r.Status, &r.HandledBy,
			&r.HandledByName, &r.HandledAt, &r.CreatedAt, &r.MessageAuthorID,
			&r.MessageAuthorName, &r.MessageContent, &r.MessageStatus,
			&r.MessageCreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// HandleReportForMessage resolves the pending report for a message. Deleting
// the message also cascades its replies; rejecting restores it to normal.
func (d *DB) HandleReportForMessage(ctx context.Context, messageID, handlerID int64, deleteMessage bool) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin handle report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var reportID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM reports
		 WHERE target_message_id = $1 AND status = $2
		 ORDER BY id DESC LIMIT 1 FOR UPDATE`,
		messageID, ReportStatusPending).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("db: load pending report: %w", err)
	}

	reportStatus := ReportStatusRejected
	messageStatus := MessageStatusNormal
	if deleteMessage {
		reportStatus = ReportStatusApproved
		messageStatus = MessageStatusDeleted
	}
	if _, err := tx.Exec(ctx,
		`UPDATE reports SET status = $2, handled_by = $3, handled_at = now() WHERE id = $1`,
		reportID, reportStatus, handlerID); err != nil {
		return 0, fmt.Errorf("db: update report: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE messages SET status = $2 WHERE id = $1`,
		messageID, messageStatus)
	if err != nil {
		return 0, fmt.Errorf("db: update reported message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	if deleteMessage {
		if _, err := tx.Exec(ctx,
			`WITH RECURSIVE doomed AS (
			   SELECT id FROM messages WHERE parent_id = $1 AND status <> $2
			   UNION ALL
			   SELECT m.id FROM messages m JOIN doomed d ON m.parent_id = d.id WHERE m.status <> $2
			 )
			 UPDATE messages SET status = $2 WHERE id IN (SELECT id FROM doomed)`,
			messageID, MessageStatusDeleted); err != nil {
			return 0, fmt.Errorf("db: cascade reported message: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit handle report: %w", err)
	}
	return reportID, nil
}

// UserHasAdminPermission reports whether the user holds a RBAC permission.
func (d *DB) UserHasAdminPermission(ctx context.Context, userID int64, permission string) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM admin_user_roles ur
		   JOIN admin_roles r ON r.id = ur.role_id
		   LEFT JOIN admin_role_permissions rp
		          ON rp.role_id = ur.role_id AND rp.permission = $2
		   WHERE ur.user_id = $1 AND (r.code = 'super_admin' OR rp.permission IS NOT NULL)
		 )`, userID, permission).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: check admin permission: %w", err)
	}
	return exists, nil
}

// ListAdminUserIDs returns distinct users holding a permission, used for
// in-app moderation notifications.
func (d *DB) ListAdminUserIDs(ctx context.Context, permission string) ([]int64, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT DISTINCT ur.user_id
		 FROM admin_user_roles ur
		 JOIN admin_roles r ON r.id = ur.role_id
		 LEFT JOIN admin_role_permissions rp
		        ON rp.role_id = ur.role_id AND rp.permission = $1
		 WHERE r.code = 'super_admin' OR rp.permission IS NOT NULL
		 ORDER BY ur.user_id`, permission)
	if err != nil {
		return nil, fmt.Errorf("db: list admin users: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
