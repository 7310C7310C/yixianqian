package httpserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationAdminPointsHTTP covers RBAC/CSRF, the adjust form and its
// idempotency, invite masking, redemption refund/mark actions, audit rows and
// the rendered tabs/overview.
func TestIntegrationAdminPointsHTTP(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	ctx := env.ctx
	pool := env.pool
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE legacy_user_id LIKE 'U-ADMPTS-%'`); err != nil {
		t.Fatalf("clean stale users: %v", err)
	}

	adminUser, adminClient := env.createUser(t, "U-ADMPTS-ADMIN", "麦穗管理员", "active", "user")
	reviewer, reviewerClient := env.createUser(t, "U-ADMPTS-REVIEW", "麦穗审核员", "active", "user")
	plain, plainClient := env.createUser(t, "U-ADMPTS-PLAIN", "麦穗普通用户", "active", "user")
	target, _ := env.createUser(t, "U-ADMPTS-TARGET", "麦穗目标用户", "active", "user")

	mustAssign := func(user db.User, role string) {
		t.Helper()
		changed, err := env.database.AssignAdminRole(ctx, user.ID, role, true)
		if err != nil || !changed {
			t.Fatalf("assign %s to %d: changed=%v err=%v", role, user.ID, changed, err)
		}
	}
	mustAssign(adminUser, "points_manager")
	mustAssign(reviewer, "reviewer")

	cleanupHTTPUsers(t, env, adminUser.ID, reviewer.ID, plain.ID, target.ID)
	// Runs before cleanupHTTPUsers because it is registered later; audit rows
	// survive the ON DELETE SET NULL on actor_user_id.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE actor_user_id = ANY($1)`,
			[]int64{adminUser.ID, reviewer.ID, plain.ID, target.ID})
	})

	targetID := strconv.FormatInt(target.ID, 10)

	// Permission gate: reviewer/plain accounts cannot enter the page.
	resp := reviewerClient.get("/admin/points")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer /admin/points status = %d", resp.StatusCode)
	}
	resp = plainClient.get("/admin/points")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain /admin/points status = %d", resp.StatusCode)
	}
	resp = adminClient.get("/admin/points")
	overviewBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("points_manager /admin/points status = %d body=%s", resp.StatusCode, overviewBody)
	}
	for _, want := range []string{"麦穗管理", "总览", "累计发放", "累计消耗", "当前总余额",
		"待确认邀请", "待处理兑换", "生效中兑换", "心愿单", "推荐单", "流水", "邀请", "兑换"} {
		if !strings.Contains(overviewBody, want) {
			t.Fatalf("overview missing %q", want)
		}
	}

	// HTML writes require CSRF.
	resp = adminClient.postForm("/admin/points/adjust", url.Values{
		"user_id": {targetID}, "delta": {"5"}, "reason": {"ADMPTS 无 CSRF"},
		"op_key": {"nocsrf-" + randomHTTPDigits(t, 12)},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("adjust without csrf status = %d", resp.StatusCode)
	}

	csrf := adminClient.csrfCookie()
	if csrf == "" {
		t.Fatal("admin client has no csrf cookie")
	}

	// Addition: balance and audit row.
	reason := "ADMPTS-" + randomHTTPDigits(t, 8) + " 加穗"
	resp = adminClient.postForm("/admin/points/adjust", url.Values{
		"csrf_token": {csrf}, "user_id": {targetID}, "delta": {"25"},
		"reason": {reason}, "op_key": {"add-" + randomHTTPDigits(t, 12)},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add points status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if location := resp.Header.Get("Location"); !strings.Contains(location, "tab=users") ||
		!strings.Contains(location, "notice=adjusted") {
		t.Fatalf("add points location = %q", location)
	}
	balance, err := env.database.PointsBalance(ctx, target.ID)
	if err != nil || balance != 25 {
		t.Fatalf("balance after add = %d err=%v", balance, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.points.adjust' AND actor_user_id = $1 AND target_id = $2`,
		adminUser.ID, targetID).Scan(&auditCount); err != nil {
		t.Fatalf("query adjust audit: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("adjust was not audited")
	}
	var auditMeta string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.points.adjust' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`, targetID).Scan(&auditMeta); err != nil {
		t.Fatalf("query adjust metadata: %v", err)
	}
	if !strings.Contains(auditMeta, `"delta": 25`) && !strings.Contains(auditMeta, `"delta":25`) {
		t.Fatalf("adjust metadata missing delta: %s", auditMeta)
	}

	// Debit below zero is rejected and does not change the balance.
	resp = adminClient.postForm("/admin/points/adjust", url.Values{
		"csrf_token": {csrf}, "user_id": {targetID}, "delta": {"-1000"},
		"reason": {"ADMPTS 负余额测试"}, "op_key": {"neg-" + randomHTTPDigits(t, 12)},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("negative balance status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !strings.Contains(body, "麦穗不足") {
		t.Fatalf("negative balance missing friendly message: %s", body)
	}
	if balance, err := env.database.PointsBalance(ctx, target.ID); err != nil || balance != 25 {
		t.Fatalf("balance after failed debit = %d err=%v", balance, err)
	}

	// Idempotency: the same op_key posts only one ledger row.
	idemReason := "ADMPTS-" + randomHTTPDigits(t, 8) + " 幂等"
	idemKey := "idem-" + randomHTTPDigits(t, 12)
	values := url.Values{
		"csrf_token": {csrf}, "user_id": {targetID}, "delta": {"10"},
		"reason": {idemReason}, "op_key": {idemKey},
	}
	for i := 0; i < 2; i++ {
		resp = adminClient.postForm("/admin/points/adjust", values)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("idempotent post %d status = %d body=%s", i, resp.StatusCode, readBody(t, resp))
		}
	}
	if balance, err := env.database.PointsBalance(ctx, target.ID); err != nil || balance != 35 {
		t.Fatalf("balance after idempotent posts = %d err=%v", balance, err)
	}
	var idemRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM points_ledger WHERE user_id = $1 AND reason = $2`,
		target.ID, idemReason).Scan(&idemRows); err != nil {
		t.Fatalf("count idempotent rows: %v", err)
	}
	if idemRows != 1 {
		t.Fatalf("idempotent ledger rows = %d, want 1", idemRows)
	}

	// Invite list masks the full phone and never leaks it.
	phone := "138" + randomHTTPDigits(t, 8)
	masked := phone[:3] + "****" + phone[7:]
	if _, err := pool.Exec(ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, status, gender)
		 VALUES ($1, $2, $3, '女性')`,
		target.ID, phone, db.InviteStatusPending); err != nil {
		t.Fatalf("insert invite: %v", err)
	}
	resp = adminClient.get("/admin/points?tab=invites")
	inviteBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invites tab status = %d", resp.StatusCode)
	}
	if !strings.Contains(inviteBody, masked) {
		t.Fatalf("invites tab missing masked phone %q", masked)
	}
	if strings.Contains(inviteBody, phone) {
		t.Fatalf("invites tab leaked full phone %q", phone)
	}

	// Refund: add balance, redeem real_like, then refund through the page.
	if _, _, err := env.database.AddPointsEntry(ctx, db.AddPointsParams{
		UserID: target.ID, Delta: 100, Kind: db.PointsKindAdmin,
		Reason: "ADMPTS 退款准备", OperatorID: &adminUser.ID,
		IdempotencyKey: "refund-add-" + randomHTTPDigits(t, 12),
	}); err != nil {
		t.Fatalf("add points for refund: %v", err)
	}
	beforeRefund, err := env.database.PointsBalance(ctx, target.ID)
	if err != nil {
		t.Fatalf("balance before refund: %v", err)
	}
	order, err := env.database.RedeemPoints(ctx, target.ID, db.RedeemRequest{
		Item:       db.RedeemItemRealLike,
		RequestKey: "refund-" + randomHTTPDigits(t, 12),
	})
	if err != nil {
		t.Fatalf("redeem real_like: %v", err)
	}
	resp = adminClient.postForm("/admin/points/redemptions/"+strconv.FormatInt(order.ID, 10)+"/refund",
		url.Values{
			"csrf_token": {csrf}, "tab": {"redemptions"},
			"reason": {"ADMPTS 测试退穗"},
		})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("refund status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	refunded, err := env.database.GetRedemption(ctx, order.ID)
	if err != nil || refunded.Status != db.RedeemStatusRefunded {
		t.Fatalf("refunded order = %+v err=%v", refunded, err)
	}
	if refunded.RefundLedgerID == nil {
		t.Fatal("refund did not record a refund ledger entry")
	}
	if balance, err := env.database.PointsBalance(ctx, target.ID); err != nil || balance != beforeRefund {
		t.Fatalf("balance after refund = %d err=%v want %d", balance, err, beforeRefund)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.points.refund' AND actor_user_id = $1 AND target_id = $2`,
		adminUser.ID, strconv.FormatInt(order.ID, 10)).Scan(&auditCount); err != nil {
		t.Fatalf("query refund audit: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("refund was not audited")
	}

	// Matchmaker mark: pending -> recommended, with audit from/to/note.
	if _, _, err := env.database.AddPointsEntry(ctx, db.AddPointsParams{
		UserID: target.ID, Delta: 100, Kind: db.PointsKindAdmin,
		Reason: "ADMPTS 推荐单准备", OperatorID: &adminUser.ID,
		IdempotencyKey: "mark-add-" + randomHTTPDigits(t, 12),
	}); err != nil {
		t.Fatalf("add points for mark: %v", err)
	}
	condition := "ADMPTS 条件：" + randomHTTPDigits(t, 8)
	matchOrder, err := env.database.RedeemPoints(ctx, target.ID, db.RedeemRequest{
		Item:       db.RedeemItemMatchmaker,
		RequestKey: "mark-" + randomHTTPDigits(t, 12),
		Condition:  condition,
	})
	if err != nil {
		t.Fatalf("redeem matchmaker: %v", err)
	}
	if matchOrder.Status != db.RedeemStatusPending {
		t.Fatalf("matchmaker initial status = %q", matchOrder.Status)
	}
	matchID := strconv.FormatInt(matchOrder.ID, 10)

	// Illegal status for this item is rejected.
	resp = adminClient.postForm("/admin/points/redemptions/"+matchID+"/mark", url.Values{
		"csrf_token": {csrf}, "tab": {"recommendations"}, "status": {"arranged"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("illegal mark status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = adminClient.postForm("/admin/points/redemptions/"+matchID+"/mark", url.Values{
		"csrf_token": {csrf}, "tab": {"recommendations"},
		"status": {"recommended"}, "note": {"ADMPTS 已推荐"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("mark status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	marked, err := env.database.GetRedemption(ctx, matchOrder.ID)
	if err != nil || marked.Status != db.RedeemStatusRecommended {
		t.Fatalf("marked order = %+v err=%v", marked, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.points.mark' AND actor_user_id = $1 AND target_id = $2`,
		adminUser.ID, matchID).Scan(&auditCount); err != nil {
		t.Fatalf("query mark audit: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("mark was not audited")
	}
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.points.mark' AND target_id = $1
		 ORDER BY id DESC LIMIT 1`, matchID).Scan(&auditMeta); err != nil {
		t.Fatalf("query mark metadata: %v", err)
	}
	if (!strings.Contains(auditMeta, `"from": "pending"`) &&
		!strings.Contains(auditMeta, `"from":"pending"`)) ||
		(!strings.Contains(auditMeta, `"to": "recommended"`) &&
			!strings.Contains(auditMeta, `"to":"recommended"`)) ||
		!strings.Contains(auditMeta, "ADMPTS 已推荐") {
		t.Fatalf("mark metadata = %s", auditMeta)
	}

	// User search and selected-user view.
	resp = adminClient.get("/admin/points?tab=users&q=" + url.QueryEscape(target.Name))
	searchBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(searchBody, target.Name) {
		t.Fatalf("user search status = %d body missing user", resp.StatusCode)
	}
	resp = adminClient.get("/admin/points?tab=users&user_id=" + targetID)
	selectedBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(selectedBody, "当前用户："+target.Name) {
		t.Fatalf("selected user status = %d", resp.StatusCode)
	}

	// Recommendations tab shows the condition; wishes tab renders too.
	resp = adminClient.get("/admin/points?tab=recommendations")
	recommendBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(recommendBody, condition) {
		t.Fatalf("recommendations tab status = %d missing condition", resp.StatusCode)
	}
	resp = adminClient.get("/admin/points?tab=wishes")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "心愿单（心愿名额）") {
		t.Fatalf("wishes tab status = %d", resp.StatusCode)
	}
	resp = adminClient.get("/admin/points?tab=ledger")
	ledgerBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(ledgerBody, "管理员调整") {
		t.Fatalf("ledger tab status = %d", resp.StatusCode)
	}

	// Overview renders the aggregate numbers from the same query.
	stats, err := env.database.AdminPointsStats(ctx)
	if err != nil {
		t.Fatalf("admin points stats: %v", err)
	}
	resp = adminClient.get("/admin/points")
	overviewBody = readBody(t, resp)
	if !strings.Contains(overviewBody, strconv.FormatInt(stats.Balance, 10)) ||
		!strings.Contains(overviewBody, "心愿单待处理") {
		t.Fatalf("overview missing stats values: %s", overviewBody)
	}
}
