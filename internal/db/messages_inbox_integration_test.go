package db

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationMessageInboxAndNotifications covers the two inbox sources
// (messages on my card + replies to my messages anywhere), per-user unread
// state, and the clear-feed persistence contract.
func TestIntegrationMessageInboxAndNotifications(t *testing.T) {
	database := openMessageTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	me, err := database.UpsertLegacyUser(ctx, "U-INBOX-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "收件人")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.UpsertLegacyUser(ctx, "U-INBOX-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "留言者")
	if err != nil {
		t.Fatal(err)
	}
	third, err := database.UpsertLegacyUser(ctx, "U-INBOX-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "第三人")
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{me.ID, other.ID, third.ID}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = database.pool.Exec(cleanupCtx,
			`DELETE FROM messages WHERE author_id = ANY($1) OR target_user_id = ANY($1)`, ids)
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM notifications WHERE user_id = ANY($1)`, ids)
		_, _ = database.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	// 1. Someone writes on my card.
	onMyCard, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: me.ID,
		AuthorID:     other.ID,
		Content:      "在你的卡片留言 " + randomDigits(t, 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2. I write on someone else's card.
	mineOnOther, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: other.ID,
		AuthorID:     me.ID,
		Content:      "我在你的卡片留言 " + randomDigits(t, 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 3. The card owner replies to my message.
	replyToMine, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: other.ID,
		AuthorID:     other.ID,
		Content:      "回复你的留言 " + randomDigits(t, 6),
		ReplyToID:    &mineOnOther.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 4. A third person replies to the card owner's reply, not to my message.
	if _, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: other.ID,
		AuthorID:     third.ID,
		Content:      "回复卡片主人 " + randomDigits(t, 6),
		ReplyToID:    &replyToMine.ID,
	}); err != nil {
		t.Fatal(err)
	}

	inbox, err := database.ListMessagesReceived(ctx, me.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 {
		t.Fatalf("inbox len = %d, want 2 (%+v)", len(inbox), inbox)
	}
	seen := map[int64]bool{}
	for _, m := range inbox {
		seen[m.ID] = true
		if !m.Unread {
			t.Fatalf("message %d should be unread on first load", m.ID)
		}
	}
	if !seen[onMyCard.ID] || !seen[replyToMine.ID] {
		t.Fatalf("inbox ids = %v, want card message %d + reply %d", seen, onMyCard.ID, replyToMine.ID)
	}

	unread, err := database.CountUnreadReceivedMessages(ctx, me.ID)
	if err != nil || unread != 2 {
		t.Fatalf("unread = %d err=%v, want 2", unread, err)
	}
	if n, err := database.MarkReceivedMessagesRead(ctx, me.ID); err != nil || n != 2 {
		t.Fatalf("mark read n=%d err=%v, want 2", n, err)
	}
	if unread, err := database.CountUnreadReceivedMessages(ctx, me.ID); err != nil || unread != 0 {
		t.Fatalf("unread after read = %d err=%v, want 0", unread, err)
	}

	// Notifications: card message + reply, no notification for the third-party reply.
	notes, err := database.ListNotifications(ctx, me.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("notifications len = %d, want 2 (%+v)", len(notes), notes)
	}
	kinds := map[string]int{}
	for _, n := range notes {
		kinds[n.Kind]++
		if n.Link == "" || n.Payload["message_id"] == nil {
			t.Fatalf("message notification missing link/payload: %+v", n)
		}
	}
	if kinds["message"] != 1 || kinds["message_reply"] != 1 {
		t.Fatalf("notification kinds = %v, want one message + one message_reply", kinds)
	}

	// Clearing the feed hides existing rows forever, but a later message still appears.
	if cleared, err := database.ClearNotifications(ctx, me.ID); err != nil || cleared != 2 {
		t.Fatalf("clear notifications = %d err=%v, want 2", cleared, err)
	}
	if notes, err := database.ListNotifications(ctx, me.ID, 50); err != nil || len(notes) != 0 {
		t.Fatalf("notifications after clear = %d err=%v, want 0", len(notes), err)
	}
	if unread, err := database.UnreadNotificationCount(ctx, me.ID); err != nil || unread != 0 {
		t.Fatalf("feed unread after clear = %d err=%v, want 0", unread, err)
	}
	if _, err := database.CreateMessage(ctx, CreateMessageParams{
		TargetUserID: me.ID,
		AuthorID:     third.ID,
		Content:      "清空后的新留言 " + randomDigits(t, 6),
	}); err != nil {
		t.Fatal(err)
	}
	notes, err = database.ListNotifications(ctx, me.ID, 50)
	if err != nil || len(notes) != 1 || notes[0].Kind != "message" {
		t.Fatalf("notifications after new message = %+v err=%v", notes, err)
	}
	if unread, err := database.CountUnreadReceivedMessages(ctx, me.ID); err != nil || unread != 1 {
		t.Fatalf("message unread after new message = %d err=%v, want 1", unread, err)
	}
	if n, err := database.MarkThreadMessagesRead(ctx, me.ID, me.ID); err != nil || n != 1 {
		t.Fatalf("mark thread read n=%d err=%v, want 1", n, err)
	}
	if unread, err := database.CountUnreadReceivedMessages(ctx, me.ID); err != nil || unread != 0 {
		t.Fatalf("message unread after thread read = %d err=%v, want 0", unread, err)
	}
}

// TestIntegrationFindUserByDuplicateLegacyOpenID protects card refs when a real
// user and their observer shadow share a migrated legacy_open_id.
func TestIntegrationFindUserByDuplicateLegacyOpenID(t *testing.T) {
	database := openMessageTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	user, err := database.UpsertLegacyUser(ctx, "U-DUP-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "重复绑定用户")
	if err != nil {
		t.Fatal(err)
	}
	observer, err := database.UpsertLegacyUser(ctx, "C-DUP-"+randomDigits(t, 10), "1"+randomDigits(t, 10), "重复绑定观察员")
	if err != nil {
		t.Fatal(err)
	}
	dup := "ou_dup_" + randomDigits(t, 8)
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = database.pool.Exec(cleanup, `DELETE FROM users WHERE id = ANY($1)`, []int64{user.ID, observer.ID})
	})
	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET legacy_open_id = $2, account_kind = 'user', account_status = 'active' WHERE id = $1`,
		user.ID, dup); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET legacy_open_id = $2, account_kind = 'observer', account_status = 'observer' WHERE id = $1`,
		observer.ID, dup); err != nil {
		t.Fatal(err)
	}
	got, err := database.FindUserByRef(ctx, dup)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != user.ID {
		t.Fatalf("FindUserByRef(%q) = %d (%s), want non-observer %d", dup, got.ID, got.Name, user.ID)
	}
	if got, err := database.FindUserByRef(ctx, *user.LegacyUserID); err != nil || got.ID != user.ID {
		t.Fatalf("FindUserByRef(legacy id) = %+v err=%v", got, err)
	}
	profile, err := database.GetSocialProfileByLegacyOpenID(ctx, dup)
	if err != nil || profile.ID != user.ID {
		t.Fatalf("GetSocialProfileByLegacyOpenID = %+v err=%v, want user %d", profile, err, user.ID)
	}
}
