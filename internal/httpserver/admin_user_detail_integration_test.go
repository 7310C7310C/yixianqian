package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

// TestIntegrationAdminUserDetailHTTP covers the review detail page, the
// permission-checked photo stream and the review actions initiated from it.
func TestIntegrationAdminUserDetailHTTP(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	suffix := randomHTTPDigits(t, 8)

	reviewer, reviewerClient := env.createUser(t, "U-UDETREV", "详情审核员"+suffix, "active", "user")
	plain, plainClient := env.createUser(t, "U-UDETPLAIN", "详情普通用户"+suffix, "active", "user")
	pointsManager, pointsClient := env.createUser(t, "U-UDETPM", "详情积分管理员"+suffix, "active", "user")
	other, otherClient := env.createUser(t, "U-UDETOTHER", "详情其他用户"+suffix, "active", "user")
	target, _ := env.createUser(t, "U-UDETTARGET", "详情目标"+suffix, "pending", "user")
	rejectTarget, rejectClient := env.createUser(t, "U-UDETREJECT", "详情待拒绝"+suffix, "pending", "user")

	ids := []int64{reviewer.ID, plain.ID, pointsManager.ID, other.ID, target.ID, rejectTarget.ID}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		targetIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			targetIDs = append(targetIDs, strconv.FormatInt(id, 10))
		}
		_, _ = env.pool.Exec(ctx,
			`DELETE FROM audit_log
			 WHERE actor_user_id = ANY($1)
			    OR (target_type = 'user' AND target_id = ANY($2))`, ids, targetIDs)
		_, _ = env.pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, ids)
	})

	mustAssign := func(userID int64, role string) {
		t.Helper()
		changed, err := env.database.AssignAdminRole(env.ctx, userID, role, true)
		if err != nil || !changed {
			t.Fatalf("assign %s to %d: changed=%v err=%v", role, userID, changed, err)
		}
	}
	mustAssign(reviewer.ID, "reviewer")
	mustAssign(pointsManager.ID, "points_manager")

	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users
		 SET nickname = '详情昵称', gender = '女', birthday = '1995-06-15',
		     height_cm = 165, education = '本科', baptismal_name = '恩典',
		     church = '测试教会', church_group = '青年组', church_location = '上海',
		     native_place = '江苏', city = '上海', industry = '教育', position = '教师',
		     income = '20W - 30W', house = '有', driving = '无', marriage = '未婚',
		     family = '家庭和睦', live_with_parents = '独立生活', personality = '性格温和',
		     self_traits = '["喜欢安静","正直"]'::jsonb,
		     self_hobbies = '["看书"]'::jsonb,
		     self_sports = '["跑步"]'::jsonb,
		     mbti = '["I","N","F","J"]'::jsonb,
		     partner_criteria = '善良体贴',
		     partner_traits = '["正直"]'::jsonb,
		     partner_hobbies = '["看书"]'::jsonb,
		     partner_sports = '["跑步"]'::jsonb,
		     wechat = 'detail-wechat', id_card_last4 = '1234', id_card_valid = true,
		     referral_source = '朋友推荐', is_for_child = '否', wechat_payment = '已开通',
		     contact_consent = true, registered_at = now() - interval '30 days',
		     last_active_at = now() - interval '2 hours'
		 WHERE id = $1`, target.ID); err != nil {
		t.Fatalf("prepare target profile: %v", err)
	}

	mediaID, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	objectKey := "udetail/" + mediaID + "/original.png"
	thumbKey := "udetail/" + mediaID + "/thumb.jpg"
	originalBytes := []byte("detail-original-image-bytes")
	thumbBytes := []byte("detail-thumb-image-bytes")
	if err := env.store.Put(env.ctx, objectKey, originalBytes, "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Put(env.ctx, thumbKey, thumbBytes, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.database.CreateMedia(env.ctx, db.Media{
		ID: mediaID, UserID: target.ID, OriginalName: "detail.png",
		ObjectKey: objectKey, ThumbKey: thumbKey, ContentType: "image/png",
		ByteSize: int64(len(originalBytes)), ThumbSize: int64(len(thumbBytes)),
		Width: 800, Height: 600,
	}); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := env.database.AddProfilePhoto(env.ctx, target.ID, mediaID); err != nil {
		t.Fatalf("attach profile photo: %v", err)
	}

	detailPath := "/admin/users/" + strconv.FormatInt(target.ID, 10)
	thumbPath := detailPath + "/photos/" + mediaID + "?size=thumb"
	fullPath := detailPath + "/photos/" + mediaID

	// Reviewer sees the full profile, the photo and the audit section.
	resp := reviewerClient.get(detailPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviewer detail status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	for _, want := range []string{
		target.Name, "详情昵称", "测试教会", "青年组", "上海", "善良体贴",
		"1234", "已同意", "审核记录", "照片（1）", "/photos/" + mediaID,
		`href="` + fullPath + `" target="_blank"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, objectKey) || strings.Contains(body, thumbKey) {
		t.Fatal("detail page leaked an object key")
	}

	// Non-admin accounts cannot open the detail page or its photos.
	if resp := plainClient.get(detailPath); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain detail status = %d", resp.StatusCode)
	}
	if resp := plainClient.get(thumbPath); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain photo status = %d", resp.StatusCode)
	}
	// A management role without user.review is still denied.
	if resp := pointsClient.get(detailPath); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("points manager detail status = %d", resp.StatusCode)
	}
	if resp := pointsClient.get(thumbPath); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("points manager photo status = %d", resp.StatusCode)
	}

	// The user list links to the detail page.
	resp = reviewerClient.get("/admin/users?status=all&q=" + url.QueryEscape(target.Name))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user list status = %d", resp.StatusCode)
	}
	listBody := readBody(t, resp)
	if !strings.Contains(listBody, detailPath) || !strings.Contains(listBody, "详情") {
		t.Fatalf("user list missing detail link: %s", listBody)
	}

	// The authorised stream returns the stored bytes with the right type.
	resp = reviewerClient.get(thumbPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumb status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("thumb content type = %q", got)
	}
	if !bytes.Equal([]byte(readBody(t, resp)), thumbBytes) {
		t.Fatal("thumb body mismatch")
	}
	resp = reviewerClient.get(fullPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("full status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("full content type = %q", got)
	}
	if !bytes.Equal([]byte(readBody(t, resp)), originalBytes) {
		t.Fatal("original body mismatch")
	}

	// 「新窗口查看原图」：target="_blank" 让浏览器以 Sec-Fetch-Dest: document
	// 打开照片串流，媒体路由不能落进顶层页面 303，仍按同一套 RBAC 返回原图。
	resp = reviewerClient.getWithHeaders(fullPath, map[string]string{"Sec-Fetch-Dest": "document"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("document full status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("document full content type = %q, want image/png", got)
	}
	if !bytes.Equal([]byte(readBody(t, resp)), originalBytes) {
		t.Fatal("document full body mismatch")
	}
	// 未登录的 document 请求同样先鉴权，不是 303 回 SPA。
	anon := newWSClient(t, env.server.URL)
	resp = anon.getWithHeaders(fullPath, map[string]string{"Sec-Fetch-Dest": "document"})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous document photo status = %d, want login redirect", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?next="+url.QueryEscape(fullPath) {
		t.Fatalf("anonymous document photo location = %q", loc)
	}
	// 页面路由本身仍是顶层 303 回 SPA，媒体豁免没有放宽管理台页面。
	resp = reviewerClient.getWithHeaders(detailPath, map[string]string{"Sec-Fetch-Dest": "document"})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("document detail page status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/?admin=1" {
		t.Fatalf("document detail page location = %q, want /?admin=1", loc)
	}

	// Another user still cannot use the owner-only /media route.
	if resp := otherClient.get("/media/" + mediaID); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other user /media status = %d", resp.StatusCode)
	}

	// Writes require CSRF.
	if resp := reviewerClient.postForm(detailPath+"/approve", url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("approve without csrf status = %d", resp.StatusCode)
	}

	// Approve from the detail page redirects back and records the note/audit.
	csrf := reviewerClient.pageCSRF(detailPath)
	resp = reviewerClient.postForm(detailPath+"/approve", url.Values{
		"csrf_token": {csrf},
		"note":       {"资料齐全"},
		"next":       {detailPath},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != detailPath+"?notice=approved" {
		t.Fatalf("approve status=%d location=%q body=%s",
			resp.StatusCode, resp.Header.Get("Location"), readBody(t, resp))
	}
	var (
		accountStatus, loginStatus, reviewNote string
		reviewedAt                             *time.Time
		reviewedBy                             *int64
	)
	if err := env.pool.QueryRow(env.ctx,
		`SELECT account_status, status, reviewed_at, reviewed_by, review_note
		 FROM users WHERE id = $1`, target.ID).
		Scan(&accountStatus, &loginStatus, &reviewedAt, &reviewedBy, &reviewNote); err != nil {
		t.Fatalf("load approved user: %v", err)
	}
	if accountStatus != "active" || loginStatus != "active" || reviewedAt == nil ||
		reviewedBy == nil || *reviewedBy != reviewer.ID || reviewNote != "资料齐全" {
		t.Fatalf("approved user state: status=%q login=%q reviewedAt=%v reviewedBy=%v note=%q",
			accountStatus, loginStatus, reviewedAt, reviewedBy, reviewNote)
	}
	var auditNote string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT COALESCE(metadata->>'note', '') FROM audit_log
		 WHERE action = 'admin.user.approve' AND target_type = 'user'
		   AND target_id = $1 AND actor_user_id = $2
		 ORDER BY id DESC LIMIT 1`,
		strconv.FormatInt(target.ID, 10), reviewer.ID).Scan(&auditNote); err != nil {
		t.Fatalf("load approve audit: %v", err)
	}
	if auditNote != "资料齐全" {
		t.Fatalf("approve audit note = %q", auditNote)
	}
	resp = reviewerClient.get(detailPath + "?notice=approved")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approved detail status = %d", resp.StatusCode)
	}
	approvedBody := readBody(t, resp)
	if !strings.Contains(approvedBody, "已审核通过") ||
		!strings.Contains(approvedBody, "资料齐全") ||
		!strings.Contains(approvedBody, reviewer.Name) {
		t.Fatalf("approved detail missing review information: %s", approvedBody)
	}

	// Rejection from the detail page demands a reason when require_note=1.
	rejectPath := "/admin/users/" + strconv.FormatInt(rejectTarget.ID, 10)
	rejectCSRF := reviewerClient.pageCSRF(rejectPath)
	resp = reviewerClient.postForm(rejectPath+"/reject", url.Values{
		"csrf_token":   {rejectCSRF},
		"require_note": {"1"},
		"next":         {rejectPath},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty reject note status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	errBody := readBody(t, resp)
	if !strings.Contains(errBody, "请填写拒绝原因") {
		t.Fatalf("empty reject note did not explain the error: %s", errBody)
	}
	if got := adminUserAccountStatus(t, env, rejectTarget.ID); got != "pending" {
		t.Fatalf("user status changed after empty reject: %q", got)
	}

	resp = reviewerClient.postForm(rejectPath+"/reject", url.Values{
		"csrf_token":   {rejectCSRF},
		"note":         {"资料不完整"},
		"require_note": {"1"},
		"next":         {rejectPath},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != rejectPath+"?notice=rejected" {
		t.Fatalf("reject status=%d location=%q body=%s",
			resp.StatusCode, resp.Header.Get("Location"), readBody(t, resp))
	}
	if got := adminUserAccountStatus(t, env, rejectTarget.ID); got != "rejected" {
		t.Fatalf("user status after reject = %q", got)
	}
	var rejectNote string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT review_note FROM users WHERE id = $1`, rejectTarget.ID).Scan(&rejectNote); err != nil {
		t.Fatalf("load reject note: %v", err)
	}
	if rejectNote != "资料不完整" {
		t.Fatalf("reject note = %q", rejectNote)
	}
	var rejectAudit int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.user.reject' AND target_type = 'user'
		   AND target_id = $1 AND actor_user_id = $2`,
		strconv.FormatInt(rejectTarget.ID, 10), reviewer.ID).Scan(&rejectAudit); err != nil {
		t.Fatalf("query reject audit: %v", err)
	}
	if rejectAudit == 0 {
		t.Fatal("reject was not audited")
	}

	// Rejection revokes the target's existing session.
	resp = rejectClient.get("/")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("rejected user session still works")
	}
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rejected user home status = %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusSeeOther && !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("rejected user redirected to %q", resp.Header.Get("Location"))
	}
}

func adminUserAccountStatus(t *testing.T, env *profileHTTPEnv, userID int64) string {
	t.Helper()
	var status string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT account_status FROM users WHERE id = $1`, userID).Scan(&status); err != nil {
		t.Fatalf("load account status: %v", err)
	}
	return status
}
