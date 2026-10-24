package httpserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationSignupSendsNotifications proves the HTTP signup route is
// actually wired to the two-way notice: named for people who liked the signup
// user, anonymous for people the signup user liked.
func TestIntegrationSignupSendsNotifications(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	signer := createMessageUser(t, ctx, database, pool, "报名通知男生")
	liker := createMessageUser(t, ctx, database, pool, "喜欢报名者的女生")
	liked := createMessageUser(t, ctx, database, pool, "被报名者喜欢的女生")
	ids := []int64{signer.ID, liker.ID, liked.ID}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, ids) })
	if _, err := pool.Exec(ctx,
		`UPDATE users SET gender = '男性' WHERE id = $1`, signer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET gender = '女性' WHERE id = ANY($1)`, []int64{liker.ID, liked.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateLike(ctx, db.CreateLikeParams{
		InitiatorID: liker.ID, TargetID: signer.ID, LikeType: db.LikeTypeAnon,
		Message: "我喜欢这位报名男生。", Now: db.SocialNow(),
	}); err != nil {
		t.Fatalf("liker like: %v", err)
	}
	if _, err := database.CreateLike(ctx, db.CreateLikeParams{
		InitiatorID: signer.ID, TargetID: liked.ID, LikeType: db.LikeTypeAnon,
		Message: "报名男生喜欢这位女生。", Now: db.SocialNow(),
	}); err != nil {
		t.Fatalf("signer like: %v", err)
	}
	activity := createWorkbenchActivity(t, ctx, database, pool, "报名通知活动")

	client := loginMessageClient(t, database, server, signer)
	resp := client.postJSON("/legacy/api/activities/"+activity.ActivityNo+"/signup",
		client.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	_ = readBody(t, resp)

	if got := notificationCount(t, ctx, pool, liker.ID, "signup"); got != 1 {
		t.Fatalf("liker signup notifications = %d, want 1", got)
	}
	var namedTitle string
	if err := pool.QueryRow(ctx,
		`SELECT title FROM notifications WHERE user_id = $1 AND kind = 'signup'`,
		liker.ID).Scan(&namedTitle); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(namedTitle, signer.Name) {
		t.Fatalf("named signup title = %q, want to contain %q", namedTitle, signer.Name)
	}

	if got := notificationCount(t, ctx, pool, liked.ID, "signup"); got != 1 {
		t.Fatalf("liked signup notifications = %d, want 1", got)
	}
	var anonTitle, anonBody, anonPayload string
	if err := pool.QueryRow(ctx,
		`SELECT title, body, payload::text FROM notifications
		  WHERE user_id = $1 AND kind = 'signup'`, liked.ID).
		Scan(&anonTitle, &anonBody, &anonPayload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(anonTitle, "喜欢你的人报名了") {
		t.Fatalf("anonymous signup title = %q", anonTitle)
	}
	for _, leak := range []string{signer.Name, signer.Phone, strconv.FormatInt(signer.ID, 10)} {
		if strings.Contains(anonTitle, leak) || strings.Contains(anonBody, leak) ||
			strings.Contains(anonPayload, leak) {
			t.Fatalf("anonymous notice leaked %q: %s / %s / %s", leak, anonTitle, anonBody, anonPayload)
		}
	}
	// A duplicate signup attempt must not send a second notice.
	resp = client.postJSON("/legacy/api/activities/"+activity.ActivityNo+"/signup",
		client.csrfCookie(), map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate signup status = %d", resp.StatusCode)
	}
	if got := notificationCount(t, ctx, pool, liker.ID, "signup"); got != 1 {
		t.Fatalf("liker notifications after duplicate = %d, want 1", got)
	}
}
