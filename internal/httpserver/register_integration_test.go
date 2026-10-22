package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func postRegisterMultipart(t *testing.T, client *wsClient, fields url.Values, photos [][]byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for key, items := range fields {
		for _, item := range items {
			if err := w.WriteField(key, item); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, photo := range photos {
		part, err := w.CreateFormFile("photos", fmt.Sprintf("photo-%d.jpg", i+1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(photo); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, client.base+"/register", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return client.do(req)
}

func TestIntegrationHTTPRegisterFlow(t *testing.T) {
	env := openPointsHTTPEnv(t)
	ctx := env.ctx
	inviter, _ := env.user(t, db.GenderMale, "active", "user")
	inviterRef := ""
	if inviter.LegacyUserID != nil {
		inviterRef = *inviter.LegacyUserID
	}

	anon := newWSClient(t, env.server.URL)
	resp := anon.get("/welcome")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "一线牵") ||
		!strings.Contains(body, "/register?from=welcome") || !strings.Contains(body, "/login") {
		t.Fatalf("welcome status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "飞书") || strings.Contains(body, "飞书") {
		t.Fatal("public page depends on Feishu copy")
	}

	resp = anon.get("/register?from=welcome")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register from welcome status = %d", resp.StatusCode)
	}
	var joinClicks int
	if err := env.conn.QueryRow(ctx,
		`SELECT count(*) FROM track_events WHERE event = 'join_click' AND source = 'welcome'`).
		Scan(&joinClicks); err != nil {
		t.Fatal(err)
	}
	if joinClicks == 0 {
		t.Fatal("welcome join_click event not recorded")
	}

	resp = anon.get("/register")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="photos"`) ||
		!strings.Contains(body, "身份证号码") {
		t.Fatalf("register page status=%d body=%s", resp.StatusCode, body)
	}
	csrf := anon.pageCSRF("/register")

	phone := "1" + randomHTTPDigits(t, 10)
	birthday := "1990-01-01"
	idCard := makeTestIDCard(t, "19900101", "002")
	password := "register-pass-123"
	fields := validRegisterValues(t, phone, idCard, birthday)
	fields.Set("inviter", inviterRef)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = env.conn.Exec(cleanupCtx, `DELETE FROM users WHERE phone = $1`, phone)
	})

	// Missing CSRF must not create an account.
	resp = postRegisterMultipart(t, anon, fields, [][]byte{testJPEG(t), testJPEG(t), testJPEG(t)})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d", resp.StatusCode)
	}
	if _, err := env.database.GetUserByPhone(ctx, phone); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("user created without csrf: err=%v", err)
	}

	// Photo count is enforced server-side.
	fields.Set("csrf_token", csrf)
	resp = postRegisterMultipart(t, anon, fields, [][]byte{testJPEG(t), testJPEG(t)})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "请上传 3-9") {
		t.Fatalf("two-photo status=%d body=%s", resp.StatusCode, body)
	}
	if _, err := env.database.GetUserByPhone(ctx, phone); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("user created with too few photos: err=%v", err)
	}

	// Valid submission creates a pending, login-enabled account with photos.
	resp = postRegisterMultipart(t, anon, fields,
		[][]byte{testJPEG(t), testJPEG(t), testJPEG(t)})
	if resp.StatusCode != http.StatusSeeOther ||
		resp.Header.Get("Location") != "/register/done?submitted=1" {
		t.Fatalf("register status=%d location=%q body=%s",
			resp.StatusCode, resp.Header.Get("Location"), readBody(t, resp))
	}
	if anon.sessionCookie() == "" {
		t.Fatal("registration did not create a session")
	}

	user, err := env.database.GetUserByPhone(ctx, phone)
	if err != nil {
		t.Fatalf("load registered user: %v", err)
	}
	if user.Status != "active" {
		t.Fatalf("login status = %q", user.Status)
	}
	state, err := env.database.GetRegistrationState(ctx, user.ID)
	if err != nil || state.AccountStatus != "pending" || state.RegisteredAt == nil {
		t.Fatalf("state = %+v err=%v", state, err)
	}
	hasCredential, err := env.database.HasCredential(ctx, user.ID)
	if err != nil || !hasCredential {
		t.Fatalf("credential = %v err=%v", hasCredential, err)
	}
	hash, err := env.database.GetPasswordHash(ctx, user.ID)
	if err != nil || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("password hash = %q err=%v", hash, err)
	}
	photos, err := env.database.ProfilePhotos(ctx, user.ID)
	if err != nil || len(photos) != 3 {
		t.Fatalf("profile photos = %d err=%v", len(photos), err)
	}
	if !photos[0].IsCover {
		t.Fatal("first registration photo is not the cover")
	}

	var sealedID []byte
	var last4 string
	if err := env.conn.QueryRow(ctx,
		`SELECT id_card_ciphertext, id_card_last4 FROM users WHERE id = $1`,
		user.ID).Scan(&sealedID, &last4); err != nil {
		t.Fatalf("load id card: %v", err)
	}
	if last4 != idCard[len(idCard)-4:] {
		t.Fatalf("id last4 = %q", last4)
	}
	if bytes.Contains(sealedID, []byte(idCard)) {
		t.Fatal("full id card stored in plaintext")
	}
	opened, err := openIDCard(env.cfg.SessionSecret, sealedID)
	if err != nil || opened != idCard {
		t.Fatalf("decrypt id = %q err=%v", opened, err)
	}

	var auditCount, notificationCount int
	if err := env.conn.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'register.submitted' AND target_id = $1`,
		fmt.Sprint(user.ID)).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit rows = %d", auditCount)
	}
	if err := env.conn.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'review'`,
		user.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount == 0 {
		t.Fatal("registration notification missing")
	}
	var contactConsent bool
	if err := env.conn.QueryRow(ctx,
		`SELECT contact_consent FROM users WHERE id = $1`, user.ID).Scan(&contactConsent); err != nil {
		t.Fatal(err)
	}
	if !contactConsent {
		t.Fatal("wechat contact consent was not recorded")
	}
	var inviteInviter int64
	var inviteStatus string
	if err := env.conn.QueryRow(ctx,
		`SELECT inviter_id, status FROM invites WHERE invitee_id = $1`, user.ID).
		Scan(&inviteInviter, &inviteStatus); err != nil {
		t.Fatalf("load invite: %v", err)
	}
	if inviteInviter != inviter.ID || inviteStatus != db.InviteStatusPending {
		t.Fatalf("invite = %d/%s want %d/%s", inviteInviter, inviteStatus,
			inviter.ID, db.InviteStatusPending)
	}

	// Pending users cannot enter the app, but may see the review page.
	for _, path := range []string{"/", "/cards", "/profile"} {
		resp = anon.get(path)
		if resp.StatusCode != http.StatusSeeOther ||
			resp.Header.Get("Location") != "/register/done" {
			t.Fatalf("%s status=%d location=%q", path, resp.StatusCode,
				resp.Header.Get("Location"))
		}
	}
	resp = anon.get("/register/done?submitted=1")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "资料审核中") ||
		!strings.Contains(body, auth.MaskPhone(phone)) {
		t.Fatalf("done page status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, idCard) {
		t.Fatal("done page leaked the full id card")
	}

	// Duplicate phone is rejected before any second account is written.
	dup := newWSClient(t, env.server.URL)
	dupCSRF := dup.pageCSRF("/register")
	dupFields := validRegisterValues(t, phone, idCard, birthday)
	dupFields.Set("csrf_token", dupCSRF)
	resp = postRegisterMultipart(t, dup, dupFields,
		[][]byte{testJPEG(t), testJPEG(t), testJPEG(t)})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var samePhone int
	if err := env.conn.QueryRow(ctx, `SELECT count(*) FROM users WHERE phone = $1`, phone).
		Scan(&samePhone); err != nil {
		t.Fatal(err)
	}
	if samePhone != 1 {
		t.Fatalf("users with phone = %d", samePhone)
	}

	// Log out, then log in while pending: the login lands on the review page.
	logoutCSRF := anon.pageCSRF("/register/done")
	resp = anon.postForm("/logout", url.Values{"csrf_token": {logoutCSRF}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	loginCSRF := anon.pageCSRF("/login")
	resp = anon.postForm("/login", url.Values{
		"csrf_token": {loginCSRF},
		"phone":      {phone},
		"password":   {password},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/register/done" {
		t.Fatalf("pending login status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Administrator approval lifts the gate for the existing session.
	if _, err := env.database.ApproveAdminUser(ctx, user.ID, inviter.ID, "integration test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	resp = anon.get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approved home status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = anon.get("/legacy/api/profile")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy profile status = %d body=%s", resp.StatusCode, body)
	}
	var profile struct {
		IDCardMask string `json:"id_card_mask"`
	}
	if err := json.Unmarshal([]byte(body), &profile); err != nil {
		t.Fatalf("legacy profile decode: %v", err)
	}
	if !strings.HasSuffix(profile.IDCardMask, idCard[len(idCard)-4:]) || strings.Contains(body, idCard) {
		t.Fatalf("legacy profile missing masked id or leaked full id: %s", body)
	}

	// A fresh login after approval goes straight to the app.
	loginClient := newWSClient(t, env.server.URL)
	loginCSRF = loginClient.pageCSRF("/login")
	resp = loginClient.postForm("/login", url.Values{
		"csrf_token": {loginCSRF},
		"phone":      {phone},
		"password":   {password},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("approved login status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
