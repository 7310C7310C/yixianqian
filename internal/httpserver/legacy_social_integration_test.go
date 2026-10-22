package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestIntegrationLegacySocialHTTP(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)

	male := createMessageUser(t, ctx, database, pool, "男用户")
	female := createMessageUser(t, ctx, database, pool, "女用户")
	female2 := createMessageUser(t, ctx, database, pool, "另一位女用户")
	male2 := createMessageUser(t, ctx, database, pool, "另一位男用户")
	moderator := createMessageUser(t, ctx, database, pool, "内容管理员")

	ids := []int64{male.ID, female.ID, female2.ID, male2.ID, moderator.ID}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM reports WHERE reporter_id = ANY($1) OR target_message_id IN
			   (SELECT id FROM messages WHERE author_id = ANY($1) OR target_user_id = ANY($1))`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM messages WHERE author_id = ANY($1) OR target_user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM likes WHERE initiator_id = ANY($1) OR target_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM praises WHERE initiator_id = ANY($1) OR target_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM notifications WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_user_roles WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM sessions WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM activation_tokens WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	applyProfile := func(u db.User, nickname, gender, openID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE users SET nickname = $2, gender = $3, legacy_open_id = $4,
			        account_kind = 'user', account_status = 'active'
			  WHERE id = $1`, u.ID, nickname, gender, openID); err != nil {
			t.Fatalf("apply profile %s: %v", nickname, err)
		}
	}
	maleOpenID := "ou_legacy_male_" + randomHTTPDigits(t, 6)
	femaleOpenID := "ou_legacy_female_" + randomHTTPDigits(t, 6)
	female2OpenID := "ou_legacy_female2_" + randomHTTPDigits(t, 6)
	male2OpenID := "ou_legacy_male2_" + randomHTTPDigits(t, 6)
	applyProfile(male, "阿男", "男性", maleOpenID)
	applyProfile(female, "阿女", "女性", femaleOpenID)
	applyProfile(female2, "阿女二", "女性", female2OpenID)
	applyProfile(male2, "阿男二", "男性", male2OpenID)

	maleClient := loginMessageClient(t, database, server, male)
	femaleClient := loginMessageClient(t, database, server, female)
	female2Client := loginMessageClient(t, database, server, female2)
	moderatorClient := loginMessageClient(t, database, server, moderator)

	decode := func(resp *http.Response, dst any) {
		t.Helper()
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}

	// Likes: CSRF, success, duplicate, self, same gender, then mutual.
	resp := maleClient.postJSON("/legacy/api/like", "", map[string]any{
		"target_openid": *female.LegacyUserID, "like_type": "匿名", "message": "我很喜欢你的性格和兴趣",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("like without csrf status = %d", resp.StatusCode)
	}

	resp = maleClient.postJSON("/legacy/api/like", maleClient.csrfCookie(), map[string]any{
		"target_openid": *female.LegacyUserID, "like_type": "匿名", "message": "我很喜欢你的性格和兴趣",
	})
	var likeResp struct {
		OK       bool `json:"ok"`
		Mutual   bool `json:"mutual"`
		AnonLeft int  `json:"anon_left"`
	}
	decode(resp, &likeResp)
	if resp.StatusCode != http.StatusOK || !likeResp.OK || likeResp.Mutual {
		t.Fatalf("like status=%d resp=%+v", resp.StatusCode, likeResp)
	}
	if likeResp.AnonLeft != 9 {
		t.Fatalf("anon_left = %d, want 9", likeResp.AnonLeft)
	}

	resp = maleClient.postJSON("/legacy/api/like", maleClient.csrfCookie(), map[string]any{
		"target_openid": *female.LegacyUserID, "like_type": "匿名", "message": "重复的喜欢附言内容测试",
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "你已经喜欢过TA了") {
		t.Fatalf("duplicate like status = %d body=%s", resp.StatusCode, body)
	}

	resp = maleClient.postJSON("/legacy/api/like", maleClient.csrfCookie(), map[string]any{
		"target_openid": *male.LegacyUserID, "like_type": "匿名", "message": "给自己点一个喜欢的附言",
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "不能喜欢自己") {
		t.Fatalf("self like status = %d body=%s", resp.StatusCode, body)
	}

	resp = maleClient.postJSON("/legacy/api/like", maleClient.csrfCookie(), map[string]any{
		"target_openid": *male2.LegacyUserID, "like_type": "匿名", "message": "同性之间的喜欢附言内容",
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "仅限异性之间喜欢") {
		t.Fatalf("same gender like status = %d body=%s", resp.StatusCode, body)
	}

	resp = femaleClient.postJSON("/legacy/api/like", femaleClient.csrfCookie(), map[string]any{
		"target_openid": *male.LegacyUserID, "like_type": "实名", "message": "我也很喜欢你的性格和兴趣",
	})
	var mutualResp struct {
		OK     bool `json:"ok"`
		Mutual bool `json:"mutual"`
	}
	decode(resp, &mutualResp)
	if resp.StatusCode != http.StatusOK || !mutualResp.OK || !mutualResp.Mutual {
		t.Fatalf("mutual like status=%d resp=%+v", resp.StatusCode, mutualResp)
	}

	// Praises are anonymous and the state must expose praised open ids.
	resp = maleClient.postJSON("/legacy/api/praise", maleClient.csrfCookie(), map[string]any{
		"target_openid": female2OpenID,
	})
	var praiseResp struct {
		OK      bool     `json:"ok"`
		Already bool     `json:"already"`
		Praised []string `json:"praised"`
	}
	decode(resp, &praiseResp)
	if resp.StatusCode != http.StatusOK || !praiseResp.OK || praiseResp.Already {
		t.Fatalf("praise status=%d resp=%+v", resp.StatusCode, praiseResp)
	}
	if len(praiseResp.Praised) != 1 || praiseResp.Praised[0] != female2OpenID {
		t.Fatalf("praised = %v, want open id %q", praiseResp.Praised, female2OpenID)
	}

	resp = maleClient.postJSON("/legacy/api/praise", maleClient.csrfCookie(), map[string]any{
		"target_openid": female2OpenID,
	})
	praiseResp = struct {
		OK      bool     `json:"ok"`
		Already bool     `json:"already"`
		Praised []string `json:"praised"`
	}{}
	decode(resp, &praiseResp)
	if resp.StatusCode != http.StatusOK || !praiseResp.Already {
		t.Fatalf("duplicate praise status=%d resp=%+v", resp.StatusCode, praiseResp)
	}

	resp = maleClient.get("/legacy/api/praise/me")
	var praiseMe struct {
		Praised []string `json:"praised"`
	}
	decode(resp, &praiseMe)
	if len(praiseMe.Praised) != 1 || praiseMe.Praised[0] != female2OpenID {
		t.Fatalf("praise/me praised = %v", praiseMe.Praised)
	}

	resp = maleClient.deleteWithCSRF("/legacy/api/praise/"+url.PathEscape(female2OpenID), maleClient.csrfCookie())
	var cancelResp struct {
		OK      bool     `json:"ok"`
		Praised []string `json:"praised"`
	}
	decode(resp, &cancelResp)
	if resp.StatusCode != http.StatusOK || !cancelResp.OK || len(cancelResp.Praised) != 0 {
		t.Fatalf("cancel praise status=%d resp=%+v", resp.StatusCode, cancelResp)
	}

	// Messages: create, thread permissions, reply nesting, list views and delete.
	resp = maleClient.postJSON("/legacy/api/messages", "", map[string]any{
		"target_openid": *female.LegacyUserID, "content": "没有 CSRF 的留言",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("message without csrf status = %d", resp.StatusCode)
	}
	resp = maleClient.postJSON("/legacy/api/messages", maleClient.csrfCookie(), map[string]any{
		"target_openid": *female.LegacyUserID, "content": "第一条留言",
	})
	var created struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	decode(resp, &created)
	if resp.StatusCode != http.StatusOK || !created.OK || created.ID == 0 {
		t.Fatalf("create message status=%d resp=%+v", resp.StatusCode, created)
	}
	rootID := strconv.FormatInt(created.ID, 10)

	resp = femaleClient.get("/legacy/api/messages?target=" + url.QueryEscape(*female.LegacyUserID))
	var thread struct {
		Messages []legacyThreadMessage `json:"messages"`
	}
	decode(resp, &thread)
	if resp.StatusCode != http.StatusOK || len(thread.Messages) != 1 {
		t.Fatalf("female thread status=%d messages=%+v", resp.StatusCode, thread.Messages)
	}
	if thread.Messages[0].ID != rootID || thread.Messages[0].IsMine || !thread.Messages[0].CanDelete {
		t.Fatalf("female thread item = %+v", thread.Messages[0])
	}
	if thread.Messages[0].AuthorOpenID != maleOpenID || thread.Messages[0].AuthorNickname != "阿男" {
		t.Fatalf("female thread author = %+v", thread.Messages[0])
	}

	resp = maleClient.get("/legacy/api/messages?target=" + url.QueryEscape(*female.LegacyUserID))
	thread = struct {
		Messages []legacyThreadMessage `json:"messages"`
	}{}
	decode(resp, &thread)
	if resp.StatusCode != http.StatusOK || len(thread.Messages) != 1 || !thread.Messages[0].IsMine {
		t.Fatalf("male thread status=%d messages=%+v", resp.StatusCode, thread.Messages)
	}

	// v1 card threads are readable by any signed-in member.
	resp = female2Client.get("/legacy/api/messages?target=" + url.QueryEscape(*female.LegacyUserID))
	thread = struct {
		Messages []legacyThreadMessage `json:"messages"`
	}{}
	decode(resp, &thread)
	if resp.StatusCode != http.StatusOK || len(thread.Messages) != 1 {
		t.Fatalf("signed-in thread read status=%d messages=%+v", resp.StatusCode, thread.Messages)
	}

	resp = femaleClient.postJSON("/legacy/api/messages", femaleClient.csrfCookie(), map[string]any{
		"target_openid": *female.LegacyUserID, "content": "谢谢你的留言", "parent_id": rootID, "reply_to_id": rootID,
	})
	var reply struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	decode(resp, &reply)
	if resp.StatusCode != http.StatusOK || !reply.OK || reply.ID == 0 {
		t.Fatalf("reply status=%d resp=%+v", resp.StatusCode, reply)
	}

	resp = maleClient.get("/legacy/api/messages?target=" + url.QueryEscape(*female.LegacyUserID))
	thread = struct {
		Messages []legacyThreadMessage `json:"messages"`
	}{}
	decode(resp, &thread)
	if resp.StatusCode != http.StatusOK || len(thread.Messages) != 2 {
		t.Fatalf("thread after reply status=%d messages=%+v", resp.StatusCode, thread.Messages)
	}
	if thread.Messages[1].ParentID != rootID || thread.Messages[1].ReplyToID != rootID ||
		thread.Messages[1].ReplyToNickname != "阿男" {
		t.Fatalf("reply thread item = %+v", thread.Messages[1])
	}
	if len(thread.Messages[0].Replies) != 1 || thread.Messages[0].Replies[0].Content != "谢谢你的留言" {
		t.Fatalf("thread replies = %+v", thread.Messages[0].Replies)
	}

	resp = maleClient.get("/legacy/api/messages/mine")
	var sent struct {
		Messages []legacySentMessage `json:"messages"`
	}
	decode(resp, &sent)
	if resp.StatusCode != http.StatusOK || len(sent.Messages) == 0 || sent.Messages[0].Content != "第一条留言" {
		t.Fatalf("mine status=%d messages=%+v", resp.StatusCode, sent.Messages)
	}
	if sent.Messages[0].TargetNickname != "阿女" || sent.Messages[0].TargetOpenID != femaleOpenID {
		t.Fatalf("mine target = %+v", sent.Messages[0])
	}

	resp = femaleClient.get("/legacy/api/messages/received")
	var received struct {
		Messages []legacyThreadMessage `json:"messages"`
	}
	decode(resp, &received)
	if resp.StatusCode != http.StatusOK || len(received.Messages) != 2 {
		t.Fatalf("received status=%d messages=%+v", resp.StatusCode, received.Messages)
	}

	resp = female2Client.deleteWithCSRF("/legacy/api/messages/"+rootID, female2Client.csrfCookie())
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "无权删除") {
		t.Fatalf("outsider delete status = %d body=%s", resp.StatusCode, body)
	}
	resp = femaleClient.deleteWithCSRF("/legacy/api/messages/"+rootID, femaleClient.csrfCookie())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner delete status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	deletedRoot, err := database.GetMessageByID(ctx, created.ID)
	if err != nil || deletedRoot.Status != db.MessageStatusDeleted {
		t.Fatalf("deleted root = %+v err=%v", deletedRoot, err)
	}
	deletedReply, err := database.GetMessageByID(ctx, reply.ID)
	if err != nil || deletedReply.Status != db.MessageStatusDeleted {
		t.Fatalf("deleted reply = %+v err=%v", deletedReply, err)
	}

	// Reports and the moderator list.
	resp = maleClient.postJSON("/legacy/api/messages", maleClient.csrfCookie(), map[string]any{
		"target_openid": *female2.LegacyUserID, "content": "待举报的留言",
	})
	created = struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}{}
	decode(resp, &created)
	if resp.StatusCode != http.StatusOK || created.ID == 0 {
		t.Fatalf("report target create status=%d resp=%+v", resp.StatusCode, created)
	}
	reportTargetID := strconv.FormatInt(created.ID, 10)

	resp = female2Client.postJSON("/legacy/api/messages/"+reportTargetID+"/report", female2Client.csrfCookie(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = female2Client.postJSON("/legacy/api/messages/"+reportTargetID+"/report", female2Client.csrfCookie(), nil)
	var reportResp struct {
		OK      bool `json:"ok"`
		Already bool `json:"already"`
	}
	decode(resp, &reportResp)
	if resp.StatusCode != http.StatusOK || !reportResp.Already {
		t.Fatalf("duplicate report status=%d resp=%+v", resp.StatusCode, reportResp)
	}
	resp = maleClient.postJSON("/legacy/api/messages", maleClient.csrfCookie(), map[string]any{
		"target_openid": *female2.LegacyUserID, "content": "自举报目标留言",
	})
	var selfReportTarget struct {
		ID int64 `json:"id"`
	}
	decode(resp, &selfReportTarget)
	if resp.StatusCode != http.StatusOK || selfReportTarget.ID == 0 {
		t.Fatalf("self report target status=%d resp=%+v", resp.StatusCode, selfReportTarget)
	}
	resp = maleClient.postJSON("/legacy/api/messages/"+strconv.FormatInt(selfReportTarget.ID, 10)+"/report",
		maleClient.csrfCookie(), map[string]any{"reason": "自己的留言"})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "不能举报自己的留言") {
		t.Fatalf("self report status = %d body=%s", resp.StatusCode, body)
	}

	resp = femaleClient.get("/legacy/api/messages/reported")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-moderator reported status = %d, want 403", resp.StatusCode)
	}
	var roleID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM admin_roles WHERE code = 'content_moderator'`).Scan(&roleID); err != nil {
		t.Fatalf("load moderator role: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO admin_user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		moderator.ID, roleID); err != nil {
		t.Fatalf("grant moderator: %v", err)
	}
	resp = moderatorClient.get("/legacy/api/messages/reported")
	var reported struct {
		Messages []legacyReportedMessage `json:"messages"`
	}
	decode(resp, &reported)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reported status=%d messages=%+v", resp.StatusCode, reported.Messages)
	}
	var reportedItem *legacyReportedMessage
	for i := range reported.Messages {
		if reported.Messages[i].ID == reportTargetID {
			reportedItem = &reported.Messages[i]
			break
		}
	}
	if reportedItem == nil || reportedItem.Content != "待举报的留言" || reportedItem.Status != db.ReportStatusPending {
		t.Fatalf("reported item = %+v", reportedItem)
	}

	// Notifications: mapping, ownership and read/read-all.
	if _, err := database.CreateNotificationOnce(ctx, db.NotificationInput{
		UserID: male.ID, Kind: "system", Title: "活动提醒", Body: "记得报名",
		Link: "/activities/A-100", DedupeKey: "legacy-social-test-" + randomHTTPDigits(t, 8),
	}); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	resp = maleClient.get("/legacy/api/notifications")
	var notifications struct {
		Notifications []legacyNotificationJSON `json:"notifications"`
		Unread        int                      `json:"unread"`
	}
	decode(resp, &notifications)
	if resp.StatusCode != http.StatusOK || notifications.Unread == 0 {
		t.Fatalf("notifications status=%d unread=%d", resp.StatusCode, notifications.Unread)
	}
	var reminder *legacyNotificationJSON
	for i := range notifications.Notifications {
		if notifications.Notifications[i].Text == "活动提醒 记得报名" {
			reminder = &notifications.Notifications[i]
			break
		}
	}
	if reminder == nil || reminder.ActivityID != "A-100" {
		t.Fatalf("activity reminder = %+v", reminder)
	}

	before := notifications.Unread
	resp = maleClient.postJSON("/legacy/api/notifications/"+strconv.FormatInt(reminder.ID, 10)+"/read", maleClient.csrfCookie(), nil)
	var readResp struct {
		OK     bool `json:"ok"`
		Unread int  `json:"unread"`
	}
	decode(resp, &readResp)
	if resp.StatusCode != http.StatusOK || !readResp.OK || readResp.Unread != before-1 {
		t.Fatalf("read notification status=%d resp=%+v before=%d", resp.StatusCode, readResp, before)
	}
	resp = maleClient.postJSON("/legacy/api/notifications/read-all", maleClient.csrfCookie(), nil)
	var readAll struct {
		OK     bool `json:"ok"`
		Unread int  `json:"unread"`
	}
	decode(resp, &readAll)
	if resp.StatusCode != http.StatusOK || !readAll.OK || readAll.Unread != 0 {
		t.Fatalf("read all status=%d resp=%+v", resp.StatusCode, readAll)
	}
}
