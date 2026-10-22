package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/auth"
)

func TestIntegrationLegacyPasswordChange(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	user, client := env.createUser(t, "U-LEGACYPASS", "改密用户", "active", "user")
	cleanupHTTPUsers(t, env, user.ID)

	oldPassword := "legacy-old-password-1"
	newPassword := "legacy-new-password-2"
	hash, err := auth.HashPassword(oldPassword)
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO auth_credentials (user_id, password_hash, must_change_password)
		 VALUES ($1, $2, true)`, user.ID, hash); err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	payload := func(current, password, confirm string) map[string]string {
		return map[string]string{
			"current_password": current,
			"password":         password,
			"password_confirm": confirm,
		}
	}
	decodeError := func(resp *http.Response) string {
		t.Helper()
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		return body.Error
	}

	// 缺少 CSRF 时不得进入改密逻辑。
	resp := client.postJSON("/legacy/api/password", "", payload(oldPassword, newPassword, newPassword))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no csrf status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if msg := decodeError(resp); msg != "表单已过期，请重新整理后再试" {
		t.Fatalf("no csrf error = %q", msg)
	}

	csrf := client.csrfCookie()
	resp = client.postJSON("/legacy/api/password", csrf, payload("wrong-old-password", newPassword, newPassword))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong current status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if msg := decodeError(resp); msg != "目前密码不正确。" {
		t.Fatalf("wrong current error = %q", msg)
	}

	resp = client.postJSON("/legacy/api/password", csrf, payload(oldPassword, newPassword, newPassword+"-diff"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatch status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if msg := decodeError(resp); msg != "两次输入的新密码不一致。" {
		t.Fatalf("mismatch error = %q", msg)
	}

	resp = client.postJSON("/legacy/api/password", csrf, payload(oldPassword, newPassword, newPassword))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var okBody struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&okBody); err != nil {
		t.Fatal(err)
	}
	if !okBody.OK {
		t.Fatalf("change response = %+v", okBody)
	}

	storedHash, err := env.database.GetPasswordHash(env.ctx, user.ID)
	if err != nil {
		t.Fatalf("load new hash: %v", err)
	}
	if ok, err := auth.VerifyPassword(newPassword, storedHash); err != nil || !ok {
		t.Fatalf("verify new password: ok=%v err=%v", ok, err)
	}
	if ok, err := auth.VerifyPassword(oldPassword, storedHash); err != nil || ok {
		t.Fatalf("old password still valid: ok=%v err=%v", ok, err)
	}
	mustChange, _, err := env.database.GetCredentialState(env.ctx, user.ID)
	if err != nil {
		t.Fatalf("load credential state: %v", err)
	}
	if mustChange {
		t.Fatal("must_change_password was not cleared")
	}

	// 成功改密后当前会话保持登录（原请求使用的 session 未被吊销）。
	resp = client.get("/legacy/api/user/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session after change status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	var auditMeta string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'auth.password.change' AND actor_user_id = $1
		 ORDER BY id DESC LIMIT 1`, user.ID).Scan(&auditMeta); err != nil {
		t.Fatalf("query password audit: %v", err)
	}
	if !strings.Contains(auditMeta, `"forced": true`) {
		t.Fatalf("audit metadata = %s", auditMeta)
	}

	anon := newWSClient(t, env.server.URL)
	resp = anon.postJSON("/legacy/api/password", "", payload(oldPassword, newPassword, newPassword))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if msg := decodeError(resp); msg != "未登录" {
		t.Fatalf("anonymous error = %q", msg)
	}
}
