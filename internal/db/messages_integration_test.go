package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"
)

func openMessageTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func TestIntegrationMessagesAndReports(t *testing.T) {
	database := openMessageTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	author, err := database.UpsertLegacyUser(ctx, "U-MSG-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "留言作者")
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.UpsertLegacyUser(ctx, "U-MSG-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "卡片主人")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.UpsertLegacyUser(ctx, "U-MSG-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "其他用户")
	if err != nil {
		t.Fatal(err)
	}

	root, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     author.ID,
		Content:      "第一则留言 " + randomDigits(t, 6),
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if root.ID == 0 || root.ParentID != nil || root.Status != MessageStatusNormal {
		t.Fatalf("unexpected root: %+v", root)
	}
	if root.AuthorName != "留言作者" || root.TargetName != "卡片主人" {
		t.Fatalf("names not resolved: %+v", root)
	}

	dup, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     author.ID,
		Content:      root.Content,
	})
	if err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if dup.ID != root.ID {
		t.Fatalf("duplicate id = %d, want %d", dup.ID, root.ID)
	}

	reply, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     target.ID,
		Content:      "卡片主人的回复 " + randomDigits(t, 6),
		ReplyToID:    &root.ID,
	})
	if err != nil {
		t.Fatalf("create reply: %v", err)
	}
	if reply.ParentID == nil || *reply.ParentID != root.ID || reply.ReplyToID == nil || *reply.ReplyToID != root.ID {
		t.Fatalf("reply thread fields = %+v", reply)
	}
	if reply.ReplyToName != "留言作者" {
		t.Fatalf("reply_to name = %q", reply.ReplyToName)
	}

	replyToReply, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     other.ID,
		Content:      "二级回复 " + randomDigits(t, 6),
		ReplyToID:    &reply.ID,
	})
	if err != nil {
		t.Fatalf("create nested reply: %v", err)
	}
	if replyToReply.ParentID == nil || *replyToReply.ParentID != root.ID {
		t.Fatalf("nested reply parent = %v, want root %d", replyToReply.ParentID, root.ID)
	}

	msgs, err := database.ListMessages(ctx, target.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("list len = %d, want 3", len(msgs))
	}
	if msgs[0].ID != root.ID || msgs[2].ID != replyToReply.ID {
		t.Fatalf("messages not oldest-first: %+v", msgs)
	}

	mine, err := database.ListMessagesByAuthor(ctx, author.ID, 10)
	if err != nil || len(mine) != 1 || mine[0].ID != root.ID {
		t.Fatalf("mine = %+v err=%v", mine, err)
	}
	received, err := database.ListMessagesReceived(ctx, target.ID, 10)
	if err != nil || len(received) != 2 {
		t.Fatalf("received len=%d err=%v", len(received), err)
	}
	if ok, err := database.HasAuthoredMessage(ctx, author.ID, target.ID); err != nil || !ok {
		t.Fatalf("author should be participant: ok=%v err=%v", ok, err)
	}
	if ok, err := database.HasAuthoredMessage(ctx, other.ID, target.ID); err != nil || !ok {
		t.Fatalf("other replied and should be participant: ok=%v err=%v", ok, err)
	}

	reporter, err := database.UpsertLegacyUser(ctx, "U-MSG-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "举报人")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateMessageReport(ctx, ReportMessageParams{
		MessageID: root.ID, ReporterID: author.ID,
	}); !errors.Is(err, ErrCannotReportSelf) {
		t.Fatalf("self report err = %v", err)
	}
	reportID, already, err := database.CreateMessageReport(ctx, ReportMessageParams{
		MessageID:  root.ID,
		ReporterID: reporter.ID,
		Reason:     "不当内容",
	})
	if err != nil || already || reportID == 0 {
		t.Fatalf("create report id=%d already=%v err=%v", reportID, already, err)
	}
	reported, err := database.GetMessageByID(ctx, root.ID)
	if err != nil || reported.Status != MessageStatusReported {
		t.Fatalf("message status = %+v err=%v", reported, err)
	}
	if _, again, err := database.CreateMessageReport(ctx, ReportMessageParams{
		MessageID: root.ID, ReporterID: other.ID,
	}); err != nil || !again {
		t.Fatalf("second report already=%v err=%v", again, err)
	}
	reports, err := database.ListReports(ctx, ReportStatusPending, 50)
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	found := false
	for _, rep := range reports {
		if rep.ID == reportID {
			found = true
			if rep.TargetMessageID == nil || *rep.TargetMessageID != root.ID {
				t.Fatalf("report message id = %v", rep.TargetMessageID)
			}
			if rep.MessageContent == "" || rep.MessageAuthorName == "" {
				t.Fatalf("report missing message snapshot: %+v", rep)
			}
		}
	}
	if !found {
		t.Fatalf("report %d not in pending list", reportID)
	}

	if _, err := database.HandleReportForMessage(ctx, root.ID, target.ID, true); err != nil {
		t.Fatalf("handle report delete: %v", err)
	}
	deleted, err := database.GetMessageByID(ctx, root.ID)
	if err != nil || deleted.Status != MessageStatusDeleted {
		t.Fatalf("root after handle = %+v err=%v", deleted, err)
	}
	replyAfter, err := database.GetMessageByID(ctx, reply.ID)
	if err != nil || replyAfter.Status != MessageStatusDeleted {
		t.Fatalf("cascade reply status = %+v err=%v", replyAfter, err)
	}
	remaining, err := database.ListMessages(ctx, target.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("visible after cascade = %d err=%v", len(remaining), err)
	}
	if _, err := database.HandleReportForMessage(ctx, root.ID, target.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second handle err = %v", err)
	}
	if _, _, err := database.CreateMessageReport(ctx, ReportMessageParams{
		MessageID: root.ID, ReporterID: reporter.ID,
	}); !errors.Is(err, ErrMessageDeleted) {
		t.Fatalf("report deleted err = %v", err)
	}

	// Deleting a root cascades replies even without a moderation report.
	secondRoot, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     author.ID,
		Content:      "第二串 " + randomDigits(t, 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondReply, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     other.ID,
		Content:      "第二串回复 " + randomDigits(t, 6),
		ReplyToID:    &secondRoot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DeleteMessage(ctx, secondRoot.ID); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	if got, err := database.GetMessageByID(ctx, secondReply.ID); err != nil || got.Status != MessageStatusDeleted {
		t.Fatalf("second reply status = %+v err=%v", got, err)
	}

	// Soft delete is idempotent only at the caller level.
	if _, err := database.DeleteMessage(ctx, secondRoot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete deleted err = %v", err)
	}

	// Legacy refs resolve numeric ids, legacy user ids and legacy open ids.
	if got, err := database.FindUserByRef(ctx, strconv.FormatInt(author.ID, 10)); err != nil || got.ID != author.ID {
		t.Fatalf("numeric lookup = %+v err=%v", got, err)
	}
	if got, err := database.FindUserByRef(ctx, *author.LegacyUserID); err != nil || got.ID != author.ID {
		t.Fatalf("legacy lookup = %+v err=%v", got, err)
	}
	legacyOpen := "open-" + randomDigits(t, 8)
	if _, err := database.pool.Exec(ctx, `UPDATE users SET legacy_open_id = $2 WHERE id = $1`, author.ID, legacyOpen); err != nil {
		t.Fatal(err)
	}
	if got, err := database.FindUserByRef(ctx, legacyOpen); err != nil || got.ID != author.ID {
		t.Fatalf("open id lookup = %+v err=%v", got, err)
	}

	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET account_status = 'matched' WHERE id = $1`, author.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := database.GetAccountStatus(ctx, author.ID); err != nil || got != "matched" {
		t.Fatalf("account status = %q err=%v", got, err)
	}

	if ok, err := database.UserHasAdminPermission(ctx, reporter.ID, contentModeratePermission); err != nil || ok {
		t.Fatalf("unexpected moderator permission: ok=%v err=%v", ok, err)
	}
	var roleID int64
	if err := database.pool.QueryRow(ctx,
		`SELECT id FROM admin_roles WHERE code = 'content_moderator'`).Scan(&roleID); err != nil {
		t.Fatalf("load moderator role: %v", err)
	}
	if _, err := database.pool.Exec(ctx,
		`INSERT INTO admin_user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		reporter.ID, roleID); err != nil {
		t.Fatal(err)
	}
	if ok, err := database.UserHasAdminPermission(ctx, reporter.ID, contentModeratePermission); err != nil || !ok {
		t.Fatalf("moderator permission = %v err=%v", ok, err)
	}
	admins, err := database.ListAdminUserIDs(ctx, contentModeratePermission)
	if err != nil {
		t.Fatal(err)
	}
	foundAdmin := false
	for _, id := range admins {
		if id == reporter.ID {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Fatalf("moderator %d missing from admin list", reporter.ID)
	}
}

const contentModeratePermission = "content.moderate"

func TestIntegrationNotifications(t *testing.T) {
	database := openMessageTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	owner, err := database.UpsertLegacyUser(ctx, "U-NOTIF-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "通知主人")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.UpsertLegacyUser(ctx, "U-NOTIF-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "其他用户")
	if err != nil {
		t.Fatal(err)
	}

	key := "notif-test-" + randomDigits(t, 10)
	created, err := database.CreateNotificationOnce(ctx, NotificationInput{
		UserID:    owner.ID,
		Kind:      "like",
		Title:     "有人喜欢你",
		Body:      "去看看是谁",
		Link:      "/notifications",
		Payload:   map[string]any{"actor_id": other.ID},
		DedupeKey: key,
	})
	if err != nil || !created {
		t.Fatalf("create notification created=%v err=%v", created, err)
	}
	created, err = database.CreateNotificationOnce(ctx, NotificationInput{
		UserID:    owner.ID,
		Kind:      "like",
		Title:     "有人喜欢你",
		DedupeKey: key,
	})
	if err != nil || created {
		t.Fatalf("duplicate notification created=%v err=%v", created, err)
	}
	if _, err := database.CreateNotificationOnce(ctx, NotificationInput{
		UserID: owner.ID,
		Kind:   "system",
		Title:  "第二则通知",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateNotificationOnce(ctx, NotificationInput{
		UserID: other.ID,
		Kind:   "system",
		Title:  "别人的通知",
	}); err != nil {
		t.Fatal(err)
	}

	if count, err := database.UnreadNotificationCount(ctx, owner.ID); err != nil || count != 2 {
		t.Fatalf("owner unread = %d err=%v", count, err)
	}
	if count, err := database.UnreadNotificationCount(ctx, other.ID); err != nil || count != 1 {
		t.Fatalf("other unread = %d err=%v", count, err)
	}

	rows, err := database.ListNotifications(ctx, owner.ID, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("owner notifications = %d, want 2", len(rows))
	}
	if rows[0].Title != "第二则通知" {
		t.Fatalf("newest notification = %q", rows[0].Title)
	}
	if got, _ := rows[1].Payload["dedupe_key"].(string); got != key {
		t.Fatalf("dedupe payload = %q, want %q", got, key)
	}

	if found, err := database.MarkNotificationRead(ctx, other.ID, rows[0].ID); err != nil || found {
		t.Fatalf("cross-owner read found=%v err=%v", found, err)
	}
	if found, err := database.MarkNotificationRead(ctx, owner.ID, rows[0].ID); err != nil || !found {
		t.Fatalf("owner read found=%v err=%v", found, err)
	}
	if found, err := database.MarkNotificationRead(ctx, owner.ID, rows[0].ID); err != nil || !found {
		t.Fatalf("idempotent read found=%v err=%v", found, err)
	}
	if count, err := database.UnreadNotificationCount(ctx, owner.ID); err != nil || count != 1 {
		t.Fatalf("unread after one read = %d err=%v", count, err)
	}
	updated, err := database.MarkAllNotificationsRead(ctx, owner.ID)
	if err != nil || updated != 1 {
		t.Fatalf("mark all updated=%d err=%v", updated, err)
	}
	if count, err := database.UnreadNotificationCount(ctx, owner.ID); err != nil || count != 0 {
		t.Fatalf("unread after all = %d err=%v", count, err)
	}
	if count, err := database.UnreadNotificationCount(ctx, other.ID); err != nil || count != 1 {
		t.Fatalf("other unread changed = %d err=%v", count, err)
	}

	if err := database.CreateNotification(ctx, owner.ID, "system", "旧界面", "内容"); err != nil {
		t.Fatalf("legacy create notification: %v", err)
	}
	if count, err := database.UnreadNotificationCount(ctx, owner.ID); err != nil || count != 1 {
		t.Fatalf("legacy notification unread = %d err=%v", count, err)
	}
}
