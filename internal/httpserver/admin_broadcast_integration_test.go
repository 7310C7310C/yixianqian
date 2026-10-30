package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestIntegrationAdminBroadcast(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)

	admin := createMessageUser(t, ctx, database, pool, "广播管理员")
	alice := createMessageUser(t, ctx, database, pool, "广播用户甲")
	bob := createMessageUser(t, ctx, database, pool, "广播用户乙")
	if changed, err := database.AssignAdminRole(ctx, admin.ID, "content_moderator", true); err != nil || !changed {
		t.Fatalf("assign moderator: changed=%v err=%v", changed, err)
	}
	adminClient := loginMessageClient(t, database, server, admin)
	aliceClient := loginMessageClient(t, database, server, alice)
	ids := []int64{admin.ID, alice.ID, bob.ID}

	var broadcastID int64
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup,
			`DELETE FROM notifications WHERE payload->>'broadcast_id' = $1`, strconv.FormatInt(broadcastID, 10))
		_, _ = pool.Exec(cleanup, `DELETE FROM broadcasts WHERE id = $1`, broadcastID)
		_, _ = pool.Exec(cleanup,
			`DELETE FROM media WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM admin_user_roles WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM sessions WHERE user_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	title := "周末活动提醒 " + randomHTTPDigits(t, 6)
	body := "本周六下午两点，记得提前签到。"
	image := testJPEG(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{
		"csrf_token": adminClient.csrfCookie(),
		"title":      title,
		"body":       body,
		"confirm":    "1",
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="banner.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/admin/broadcasts", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp := adminClient.do(req)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("broadcast status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/admin/broadcasts?sent=") {
		t.Fatalf("broadcast redirect = %q", loc)
	}

	var mediaID string
	var recipientCount int
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(media_id, ''), recipient_count FROM broadcasts WHERE title = $1`, title).
		Scan(&mediaID, &recipientCount); err != nil {
		t.Fatalf("load broadcast: %v", err)
	}
	if mediaID == "" || recipientCount < 2 {
		t.Fatalf("broadcast media=%q recipients=%d", mediaID, recipientCount)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM broadcasts WHERE title = $1`, title).Scan(&broadcastID); err != nil {
		t.Fatalf("load broadcast id: %v", err)
	}

	for _, u := range []struct {
		name string
		id   int64
	}{{"alice", alice.ID}, {"bob", bob.ID}} {
		var count int
		var payload []byte
		if err := pool.QueryRow(ctx,
			`SELECT count(*), COALESCE(max(payload::text), '{}') FROM notifications
			  WHERE user_id = $1 AND kind = 'admin_broadcast'
			    AND payload->>'broadcast_id' = $2`,
			u.id, strconv.FormatInt(broadcastID, 10)).Scan(&count, &payload); err != nil {
			t.Fatalf("load %s notification: %v", u.name, err)
		}
		if count != 1 {
			t.Fatalf("%s broadcast notifications = %d, want 1", u.name, count)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("decode %s payload: %v", u.name, err)
		}
		if decoded["media_id"] != mediaID {
			t.Fatalf("%s payload media_id = %v, want %q", u.name, decoded["media_id"], mediaID)
		}
	}

	// Broadcast media is visible to every signed-in user, not only its uploader.
	for _, client := range []*wsClient{aliceClient, adminClient} {
		resp := client.get("/media/" + mediaID + "?size=thumb")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("broadcast media status = %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
			t.Fatalf("broadcast media content-type = %q", ct)
		}
	}

	// The admin console page renders in the iframe and lists the sent message.
	resp = adminClient.getWithHeaders("/admin/broadcasts", map[string]string{"Sec-Fetch-Dest": "iframe"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin broadcasts page status = %d", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, title) || !strings.Contains(body, "全员消息") {
		t.Fatalf("admin broadcasts page missing title/header")
	}
	resp = aliceClient.getWithHeaders("/admin/broadcasts", map[string]string{"Sec-Fetch-Dest": "iframe"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("normal user broadcasts page status = %d, want 403", resp.StatusCode)
	}

	// Missing confirmation must be rejected before writing anything.
	form := url.Values{}
	form.Set("csrf_token", adminClient.csrfCookie())
	form.Set("title", "未确认"+randomHTTPDigits(t, 5))
	form.Set("body", "不应发送")
	resp = adminClient.postForm("/admin/broadcasts", form)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing confirm status = %d, want 400", resp.StatusCode)
	}
}
