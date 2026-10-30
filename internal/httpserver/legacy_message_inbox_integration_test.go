package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestIntegrationLegacyMessageInboxHTTP(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)

	alice := createMessageUser(t, ctx, database, pool, "收件箱甲")
	bob := createMessageUser(t, ctx, database, pool, "收件箱乙")
	ids := []int64{alice.ID, bob.ID}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM messages WHERE author_id = ANY($1) OR target_user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM notifications WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM sessions WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})
	// Match the legacy display names used by message views.
	suffix := randomHTTPDigits(t, 6)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET nickname = CASE WHEN id = $1 THEN '收件箱甲' ELSE '收件箱乙' END,
		        legacy_open_id = CASE WHEN id = $1 THEN 'ou_inbox_a_' || $3 ELSE 'ou_inbox_b_' || $3 END
		  WHERE id = ANY($2)`,
		alice.ID, ids, suffix); err != nil {
		t.Fatalf("set inbox profiles: %v", err)
	}
	aliceClient := loginMessageClient(t, database, server, alice)
	bobClient := loginMessageClient(t, database, server, bob)

	decode := func(resp *http.Response, dst any) {
		t.Helper()
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}

	// Alice writes on Bob's card.
	resp := aliceClient.postJSON("/legacy/api/messages", aliceClient.csrfCookie(), map[string]any{
		"target_openid": "ou_inbox_b_" + suffix,
		"content":       "你好，这是第一条留言 " + randomHTTPDigits(t, 6),
	})
	var created struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	decode(resp, &created)
	if resp.StatusCode != http.StatusOK || !created.OK || created.ID == 0 {
		t.Fatalf("create message status=%d resp=%+v", resp.StatusCode, created)
	}

	// Bob's inbox contains the message, marks it unread and knows the card owner.
	resp = bobClient.get("/legacy/api/messages/received")
	var inbox struct {
		Messages []legacyThreadMessage `json:"messages"`
		Unread   int                   `json:"unread"`
	}
	decode(resp, &inbox)
	if resp.StatusCode != http.StatusOK || len(inbox.Messages) != 1 || inbox.Unread != 1 {
		t.Fatalf("bob inbox status=%d messages=%+v unread=%d", resp.StatusCode, inbox.Messages, inbox.Unread)
	}
	if !inbox.Messages[0].Unread || !inbox.Messages[0].OnMyCard || inbox.Messages[0].TargetOpenID == "" {
		t.Fatalf("bob inbox item = %+v", inbox.Messages[0])
	}
	if inbox.Messages[0].Replied {
		t.Fatalf("bob inbox item marked replied before reply: %+v", inbox.Messages[0])
	}

	// The feed exposes the same unread count and a structured message row.
	resp = bobClient.get("/legacy/api/notifications")
	var feed struct {
		Notifications []legacyNotificationJSON `json:"notifications"`
		Unread        int                      `json:"unread"`
		UnreadMessage int                      `json:"unread_messages"`
	}
	decode(resp, &feed)
	if resp.StatusCode != http.StatusOK || feed.UnreadMessage != 1 {
		t.Fatalf("bob feed status=%d unread=%d unread_messages=%d", resp.StatusCode, feed.Unread, feed.UnreadMessage)
	}
	foundMessage := false
	for _, n := range feed.Notifications {
		if n.Kind == "message" {
			foundMessage = true
			if n.Payload["message_id"] == nil || n.Link == "" {
				t.Fatalf("message notification payload/link = %+v", n)
			}
		}
	}
	if !foundMessage {
		t.Fatalf("bob feed missing message notification: %+v", feed.Notifications)
	}

	// Opening the inbox marks it read; the feed unread count is independent.
	resp = bobClient.postJSON("/legacy/api/messages/read", bobClient.csrfCookie(), nil)
	var readResp struct {
		OK             bool `json:"ok"`
		UnreadMessages int  `json:"unread_messages"`
	}
	decode(resp, &readResp)
	if resp.StatusCode != http.StatusOK || !readResp.OK || readResp.UnreadMessages != 0 {
		t.Fatalf("mark read status=%d resp=%+v", resp.StatusCode, readResp)
	}
	resp = bobClient.get("/legacy/api/notifications")
	feed = struct {
		Notifications []legacyNotificationJSON `json:"notifications"`
		Unread        int                      `json:"unread"`
		UnreadMessage int                      `json:"unread_messages"`
	}{}
	decode(resp, &feed)
	if feed.UnreadMessage != 0 || feed.Unread == 0 {
		t.Fatalf("after read: unread=%d unread_messages=%d, want feed unread still >0", feed.Unread, feed.UnreadMessage)
	}

	// Bob replies on his own card; Alice's inbox receives the reply even though
	// the card is not hers.
	resp = bobClient.postJSON("/legacy/api/messages", bobClient.csrfCookie(), map[string]any{
		"target_openid": "ou_inbox_b_" + suffix,
		"content":       "回复你的留言 " + randomHTTPDigits(t, 6),
		"parent_id":     created.ID,
		"reply_to_id":   created.ID,
	})
	var reply struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	decode(resp, &reply)
	if resp.StatusCode != http.StatusOK || !reply.OK || reply.ID == 0 {
		t.Fatalf("reply status=%d resp=%+v", resp.StatusCode, reply)
	}
	resp = bobClient.get("/legacy/api/messages/received")
	inbox = struct {
		Messages []legacyThreadMessage `json:"messages"`
		Unread   int                   `json:"unread"`
	}{}
	decode(resp, &inbox)
	if len(inbox.Messages) != 1 || !inbox.Messages[0].Replied {
		t.Fatalf("bob inbox after reply = %+v", inbox.Messages)
	}
	resp = aliceClient.get("/legacy/api/messages/received")
	inbox = struct {
		Messages []legacyThreadMessage `json:"messages"`
		Unread   int                   `json:"unread"`
	}{}
	decode(resp, &inbox)
	if resp.StatusCode != http.StatusOK || len(inbox.Messages) != 1 || inbox.Unread != 1 {
		t.Fatalf("alice inbox status=%d messages=%+v unread=%d", resp.StatusCode, inbox.Messages, inbox.Unread)
	}
	if inbox.Messages[0].OnMyCard || inbox.Messages[0].ID != strconv.FormatInt(reply.ID, 10) {
		t.Fatalf("alice inbox item = %+v", inbox.Messages[0])
	}

	// Thread response carries the target summary for the detail page.
	resp = aliceClient.get("/legacy/api/messages?target=" + inbox.Messages[0].TargetOpenID)
	var thread struct {
		Messages []legacyThreadMessage `json:"messages"`
		Target   map[string]any        `json:"target"`
	}
	decode(resp, &thread)
	if resp.StatusCode != http.StatusOK || len(thread.Messages) != 2 || thread.Target["nickname"] != "收件箱乙" {
		t.Fatalf("thread status=%d messages=%d target=%+v", resp.StatusCode, len(thread.Messages), thread.Target)
	}

	// Clearing the feed hides it forever, but does not resurrect message unread.
	resp = bobClient.postJSON("/legacy/api/notifications/clear", bobClient.csrfCookie(), nil)
	var clearResp struct {
		OK      bool `json:"ok"`
		Cleared int  `json:"cleared"`
	}
	decode(resp, &clearResp)
	if resp.StatusCode != http.StatusOK || !clearResp.OK || clearResp.Cleared == 0 {
		t.Fatalf("clear feed status=%d resp=%+v", resp.StatusCode, clearResp)
	}
	resp = bobClient.get("/legacy/api/notifications")
	feed = struct {
		Notifications []legacyNotificationJSON `json:"notifications"`
		Unread        int                      `json:"unread"`
		UnreadMessage int                      `json:"unread_messages"`
	}{}
	decode(resp, &feed)
	if len(feed.Notifications) != 0 || feed.Unread != 0 || feed.UnreadMessage != 0 {
		t.Fatalf("feed after clear = %+v", feed)
	}
}
