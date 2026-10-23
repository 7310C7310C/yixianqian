package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The v1 麦穗管理 page is a read-only console over /legacy/api/admin/points/*.
// These tests pin the payload shape, the RBAC split, CSRF, idempotency and the
// report-handling action that the same SPA page set uses.

type legacyPointsStatsJSON struct {
	UserCount       int   `json:"user_count"`
	EntryCount      int   `json:"entry_count"`
	Earned          int64 `json:"earned"`
	Spent           int64 `json:"spent"`
	Outstanding     int64 `json:"outstanding"`
	RedemptionCount int   `json:"redemption_count"`
	InviteCount     int   `json:"invite_count"`
}

type legacyPointsOverviewHTTP struct {
	Stats  legacyPointsStatsJSON `json:"stats"`
	Config []db.PointsConfigItem `json:"config"`
	Users  []struct {
		UserOID    string `json:"user_oid"`
		UserID     string `json:"user_id"`
		Nickname   string `json:"nickname"`
		Earned     int    `json:"earned"`
		Spent      int    `json:"spent"`
		EntryCount int    `json:"entry_count"`
		LastAt     string `json:"last_at"`
		Balance    int    `json:"balance"`
	} `json:"users"`
}

func legacyPointsAssertOK(t *testing.T, resp *http.Response, dst any) string {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeBody(t, resp, dst)
}

func decodeBody(t *testing.T, resp *http.Response, dst any) string {
	t.Helper()
	raw := readBody(t, resp)
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		t.Fatalf("decode response: %v body=%s", err, raw)
	}
	return raw
}

func legacyPointsAuditCount(t *testing.T, env *profileHTTPEnv, actorID int64, action string) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM audit_log WHERE actor_user_id = $1 AND action = $2`,
		actorID, action).Scan(&count); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return count
}

func legacyPointsLedgerCount(t *testing.T, env *profileHTTPEnv, userID int64) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM points_ledger WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return count
}

func legacyPointsNotificationCount(t *testing.T, env *profileHTTPEnv, userID int64) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'points'`, userID).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return count
}

// legacyPointsFixture inserts a small ledger, a redemption and an invite for
// one user. Direct SQL keeps the contract test independent of the redeem flow.
func legacyPointsFixture(t *testing.T, env *profileHTTPEnv, userID int64) (redeemLedgerID, redemptionID int64) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO points_ledger (user_id, delta, balance_after, kind, reason, ref_type, created_at)
		 VALUES ($1, 30, 30, 'invite', '邀请好友到账', 'invite', now() - interval '2 hours')`,
		userID); err != nil {
		t.Fatalf("insert invite ledger: %v", err)
	}
	if err := env.pool.QueryRow(env.ctx,
		`INSERT INTO points_ledger (user_id, delta, balance_after, kind, reason, ref_type, created_at)
		 VALUES ($1, -20, 10, 'redeem', '兑换额外实名喜欢', 'redeem', now() - interval '1 hour')
		 RETURNING id`, userID).Scan(&redeemLedgerID); err != nil {
		t.Fatalf("insert redeem ledger: %v", err)
	}
	if err := env.pool.QueryRow(env.ctx,
		`INSERT INTO points_redemptions (user_id, item, cost, status, params, idempotency_key, ledger_id, ref_id)
		 VALUES ($1, 'real_like', 20, 'active', '{}'::jsonb, $2, $3, '')
		 RETURNING id`, userID, "it-redemption-"+randomHTTPDigits(t, 12), redeemLedgerID).Scan(&redemptionID); err != nil {
		t.Fatalf("insert redemption: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO points_redemptions (user_id, item, cost, status, params, idempotency_key, ref_id)
		 VALUES ($1, 'wish', 30, 'pending', '{}'::jsonb, $2, '')`,
		userID, "it-wish-"+randomHTTPDigits(t, 12)); err != nil {
		t.Fatalf("insert wish redemption: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO invites (inviter_id, invitee_phone, status, gender, source)
		 VALUES ($1, $2, 'pending', 'female', 'test')`,
		userID, "138"+randomHTTPDigits(t, 8)); err != nil {
		t.Fatalf("insert invite: %v", err)
	}
	return redeemLedgerID, redemptionID
}

func TestIntegrationLegacyPointsAdminHTTP(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	admin, adminClient := env.createUser(t, "U-PTADM", "麦穗管理员", "active", "user")
	target, _ := env.createUser(t, "U-PTTARGET", "被调整用户", "active", "user")
	plain, plainClient := env.createUser(t, "U-PTPLAIN", "普通用户", "active", "user")
	cleanupHTTPUsers(t, env, admin.ID, target.ID, plain.ID)
	if changed, err := env.database.AssignAdminRole(env.ctx, admin.ID, "points_manager", true); err != nil || !changed {
		t.Fatalf("assign points_manager: changed=%v err=%v", changed, err)
	}
	_, redemptionID := legacyPointsFixture(t, env, target.ID)

	targetLegacyID := ""
	if target.LegacyUserID != nil {
		targetLegacyID = *target.LegacyUserID
	}
	if targetLegacyID == "" {
		t.Fatal("fixture user has no legacy id")
	}
	targetPhone := target.Phone

	// Anonymous: every read and write route keeps the v1 401 body.
	anon := newWSClient(t, env.server.URL)
	readPaths := []string{
		"/legacy/api/admin/points/overview",
		"/legacy/api/admin/points/ledger",
		"/legacy/api/admin/points/redemptions",
		"/legacy/api/admin/points/invites",
		"/legacy/api/admin/points/user?user_oid=" + targetLegacyID,
	}
	for _, path := range readPaths {
		resp := anon.get(path)
		if resp.StatusCode != http.StatusUnauthorized || legacyITError(t, resp) != "未登录" {
			t.Fatalf("anonymous %s = %d body=%s", path, resp.StatusCode, readBody(t, resp))
		}
	}
	writePaths := []string{
		"/legacy/api/admin/points/adjust",
		"/legacy/api/admin/points/redemptions/" + strconv.FormatInt(redemptionID, 10) + "/refund",
		"/legacy/api/admin/points/redemptions/" + strconv.FormatInt(redemptionID, 10) + "/mark",
	}
	for _, path := range writePaths {
		resp := anon.postJSON(path, "", map[string]any{})
		if resp.StatusCode != http.StatusUnauthorized || legacyITError(t, resp) != "未登录" {
			t.Fatalf("anonymous %s = %d body=%s", path, resp.StatusCode, readBody(t, resp))
		}
	}

	// Plain user: 403 with the v1 "没有权限" string, not the v2 envelope.
	resp := plainClient.get("/legacy/api/admin/points/overview")
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "没有权限" {
		t.Fatalf("plain overview = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	// is_admin now means "has any admin role"; it must not leak to normal users.
	var home struct {
		User struct {
			IsAdmin  bool `json:"is_admin"`
			CanAdmin bool `json:"can_admin"`
		} `json:"user"`
	}
	resp = plainClient.get("/legacy/api/home")
	legacyPointsAssertOK(t, resp, &home)
	if home.User.IsAdmin || home.User.CanAdmin {
		t.Fatalf("plain user admin flags = %+v", home.User)
	}
	resp = adminClient.get("/legacy/api/home")
	legacyPointsAssertOK(t, resp, &home)
	if !home.User.IsAdmin || !home.User.CanAdmin {
		t.Fatalf("admin user admin flags = %+v", home.User)
	}

	// Overview: stats identity, config shape, aggregated user row.
	var overview legacyPointsOverviewHTTP
	raw := legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/overview"), &overview)
	if overview.Stats.Outstanding != overview.Stats.Earned-overview.Stats.Spent {
		t.Fatalf("stats identity broken: %+v", overview.Stats)
	}
	if overview.Stats.Earned < 50 || overview.Stats.Spent < 20 || overview.Stats.EntryCount < 2 {
		t.Fatalf("stats = %+v", overview.Stats)
	}
	if len(overview.Config) != 15 {
		t.Fatalf("config len = %d", len(overview.Config))
	}
	var inviteCfg *db.PointsConfigItem
	for i := range overview.Config {
		if overview.Config[i].Key == "invite_reward_female" {
			inviteCfg = &overview.Config[i]
		}
	}
	if inviteCfg == nil || inviteCfg.Label == "" || inviteCfg.Overridden {
		t.Fatalf("invite config = %+v", inviteCfg)
	}
	var targetRow *struct {
		UserOID    string `json:"user_oid"`
		UserID     string `json:"user_id"`
		Nickname   string `json:"nickname"`
		Earned     int    `json:"earned"`
		Spent      int    `json:"spent"`
		EntryCount int    `json:"entry_count"`
		LastAt     string `json:"last_at"`
		Balance    int    `json:"balance"`
	}
	for i := range overview.Users {
		if overview.Users[i].UserOID == strconv.FormatInt(target.ID, 10) {
			targetRow = &overview.Users[i]
			break
		}
	}
	if targetRow == nil {
		t.Fatalf("target missing from overview users: %+v", overview.Users)
	}
	if targetRow.UserID != targetLegacyID || targetRow.Nickname != "被调整用户" ||
		targetRow.Earned != 30 || targetRow.Spent != 20 || targetRow.EntryCount != 2 ||
		targetRow.Balance != 10 || !strings.Contains(targetRow.LastAt, ":") {
		t.Fatalf("target row = %+v", targetRow)
	}
	if strings.Contains(raw, targetPhone) {
		t.Fatalf("overview leaked plaintext phone")
	}

	// Ledger: newest first, user filter, pagination and the empty [] contract.
	var ledger struct {
		Rows []struct {
			ID        int64  `json:"id"`
			Nickname  string `json:"nickname"`
			UserID    string `json:"user_id"`
			KindLabel string `json:"kind_label"`
			Reason    string `json:"reason"`
			CreatedAt string `json:"created_at"`
			Delta     int    `json:"delta"`
		} `json:"rows"`
	}
	raw = legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/ledger?user_oid="+targetLegacyID), &ledger)
	if len(ledger.Rows) != 2 || ledger.Rows[0].Delta != -20 || ledger.Rows[0].KindLabel != "兑换" {
		t.Fatalf("ledger rows = %+v", ledger.Rows)
	}
	if ledger.Rows[0].UserID != targetLegacyID || ledger.Rows[0].CreatedAt == "" {
		t.Fatalf("ledger row = %+v", ledger.Rows[0])
	}
	if strings.Contains(raw, targetPhone) {
		t.Fatalf("ledger leaked plaintext phone")
	}
	raw = legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/ledger?user_oid="+targetLegacyID+"&limit=1&offset=1"), &ledger)
	if len(ledger.Rows) != 1 || ledger.Rows[0].Delta != 30 {
		t.Fatalf("paged ledger rows = %+v", ledger.Rows)
	}
	raw = legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/ledger?user_oid="+strconv.FormatInt(admin.ID, 10)), &ledger)
	if !strings.Contains(raw, `"rows":[]`) {
		t.Fatalf("empty ledger body = %s", raw)
	}
	resp = adminClient.get("/legacy/api/admin/points/ledger?user_oid=no-such-user")
	if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "找不到用户" {
		t.Fatalf("bad ledger user = %d", resp.StatusCode)
	}

	// Redemptions and invites keep their display labels and masking.
	var redemptions struct {
		Rows []struct {
			ID          int64  `json:"id"`
			Nickname    string `json:"nickname"`
			UserID      string `json:"user_id"`
			ItemLabel   string `json:"item_label"`
			StatusLabel string `json:"status_label"`
			CreatedAt   string `json:"created_at"`
			Cost        int    `json:"cost"`
		} `json:"rows"`
	}
	legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/redemptions"), &redemptions)
	if len(redemptions.Rows) < 2 {
		t.Fatalf("redemption rows = %+v", redemptions.Rows)
	}
	foundRealLike := false
	for _, row := range redemptions.Rows {
		if row.ID == redemptionID {
			foundRealLike = row.Cost == 20 && row.ItemLabel == "额外实名喜欢" &&
				row.StatusLabel == "生效中" && row.UserID == targetLegacyID && row.CreatedAt != ""
		}
	}
	if !foundRealLike {
		t.Fatalf("fixture redemption not mapped: %+v", redemptions.Rows)
	}
	var invites struct {
		Rows []struct {
			ID          int64  `json:"id"`
			Nickname    string `json:"nickname"`
			UserID      string `json:"user_id"`
			PhoneMasked string `json:"phone_masked"`
			StatusLabel string `json:"status_label"`
			CreatedAt   string `json:"created_at"`
		} `json:"rows"`
	}
	raw = legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/invites"), &invites)
	if len(invites.Rows) == 0 || invites.Rows[0].PhoneMasked == "" ||
		!strings.Contains(invites.Rows[0].PhoneMasked, "*") ||
		invites.Rows[0].StatusLabel == "" {
		t.Fatalf("invite rows = %+v", invites.Rows)
	}
	var latestInvitePhone string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT invitee_phone FROM invites WHERE inviter_id = $1 ORDER BY id DESC LIMIT 1`,
		target.ID).Scan(&latestInvitePhone); err != nil {
		t.Fatalf("load invite phone: %v", err)
	}
	if latestInvitePhone != "" && strings.Contains(raw, latestInvitePhone) {
		t.Fatalf("invites leaked plaintext phone")
	}

	// Detail: every section the bottom sheet renders.
	var detail struct {
		User struct {
			Nickname string `json:"nickname"`
			UserID   string `json:"user_id"`
		} `json:"user"`
		Summary struct {
			Balance int `json:"balance"`
			Earned  int `json:"earned"`
			Spent   int `json:"spent"`
		} `json:"summary"`
		ExtraRealLike int `json:"extra_real_like"`
		EarnedByKind  []struct {
			Kind      string `json:"kind"`
			KindLabel string `json:"kind_label"`
			Total     int    `json:"total"`
			Count     int    `json:"count"`
		} `json:"earned_by_kind"`
		RedeemedByItem []struct {
			Item      string `json:"item"`
			ItemLabel string `json:"item_label"`
			Total     int    `json:"total"`
			Count     int    `json:"count"`
		} `json:"redeemed_by_item"`
		Invites struct {
			Total      int `json:"total"`
			Pending    int `json:"pending"`
			Confirmed  int `json:"confirmed"`
			Finalized  int `json:"finalized"`
			ClawedBack int `json:"clawed_back"`
		} `json:"invites"`
		InviteList []struct {
			ID          int64  `json:"id"`
			PhoneMasked string `json:"phone_masked"`
			StatusLabel string `json:"status_label"`
		} `json:"invite_list"`
		Redemptions []struct {
			ID          int64  `json:"id"`
			ItemLabel   string `json:"item_label"`
			StatusLabel string `json:"status_label"`
			Cost        int    `json:"cost"`
			CreatedAt   string `json:"created_at"`
		} `json:"redemptions"`
		Entries []struct {
			ID        int64  `json:"id"`
			KindLabel string `json:"kind_label"`
			Reason    string `json:"reason"`
			Delta     int    `json:"delta"`
			CreatedAt string `json:"created_at"`
		} `json:"entries"`
	}
	raw = legacyPointsAssertOK(t, adminClient.get("/legacy/api/admin/points/user?user_oid="+targetLegacyID), &detail)
	if detail.User.Nickname != "被调整用户" || detail.User.UserID != targetLegacyID {
		t.Fatalf("detail user = %+v", detail.User)
	}
	if detail.Summary.Balance != 10 || detail.Summary.Earned != 30 || detail.Summary.Spent != 20 {
		t.Fatalf("detail summary = %+v", detail.Summary)
	}
	if len(detail.EarnedByKind) != 1 || detail.EarnedByKind[0].KindLabel != "邀请好友" ||
		detail.EarnedByKind[0].Total != 30 || detail.EarnedByKind[0].Count != 1 {
		t.Fatalf("earned_by_kind = %+v", detail.EarnedByKind)
	}
	if len(detail.RedeemedByItem) != 2 || detail.RedeemedByItem[0].ItemLabel == "" {
		t.Fatalf("redeemed_by_item = %+v", detail.RedeemedByItem)
	}
	if detail.Invites.Total != 1 || detail.Invites.Pending != 1 || len(detail.InviteList) != 1 {
		t.Fatalf("invite detail = %+v %+v", detail.Invites, detail.InviteList)
	}
	if len(detail.Redemptions) != 2 || detail.Redemptions[0].CreatedAt == "" {
		t.Fatalf("detail redemptions = %+v", detail.Redemptions)
	}
	if len(detail.Entries) != 2 || detail.Entries[0].Delta != -20 {
		t.Fatalf("detail entries = %+v", detail.Entries)
	}
	if strings.Contains(raw, targetPhone) {
		t.Fatalf("detail leaked plaintext phone")
	}
	resp = adminClient.get("/legacy/api/admin/points/user?user_oid=no-such-user")
	if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "找不到用户" {
		t.Fatalf("bad detail user = %d", resp.StatusCode)
	}

	// Write path: CSRF, validation, idempotent adjust, refund, mark.
	adjustPath := "/legacy/api/admin/points/adjust"
	resp = adminClient.postJSON(adjustPath, "", map[string]any{
		"user_oid": targetLegacyID, "delta": 10, "reason": "测试调整", "request_key": "key-nocsrf",
	})
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "表单已过期，请重新整理后再试" {
		t.Fatalf("adjust without csrf = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	csrf := adminClient.csrfCookie()
	resp = adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": 0, "reason": "零", "request_key": "key-zero",
	})
	if resp.StatusCode != http.StatusBadRequest || legacyITError(t, resp) != "麦穗数量不能为 0" {
		t.Fatalf("adjust zero = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": 10, "reason": " ", "request_key": "key-noreason",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("adjust empty reason = %d", resp.StatusCode)
	}
	resp = adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": 10, "reason": "测试调整",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("adjust missing request key = %d", resp.StatusCode)
	}

	beforeLedger := legacyPointsLedgerCount(t, env, target.ID)
	var adjustResp struct {
		OK        bool  `json:"ok"`
		Duplicate bool  `json:"duplicate"`
		Balance   int   `json:"balance"`
		EntryID   int64 `json:"entry_id"`
	}
	legacyPointsAssertOK(t, adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": 10, "reason": "测试调整", "request_key": "key-adjust-1",
	}), &adjustResp)
	if !adjustResp.OK || adjustResp.Duplicate || adjustResp.Balance != 20 || adjustResp.EntryID == 0 {
		t.Fatalf("adjust resp = %+v", adjustResp)
	}
	if got := legacyPointsLedgerCount(t, env, target.ID); got != beforeLedger+1 {
		t.Fatalf("ledger count after adjust = %d want %d", got, beforeLedger+1)
	}
	if got := legacyPointsAuditCount(t, env, admin.ID, "admin.points.adjust"); got != 1 {
		t.Fatalf("adjust audit = %d", got)
	}
	if got := legacyPointsNotificationCount(t, env, target.ID); got != 1 {
		t.Fatalf("adjust notifications = %d", got)
	}
	raw = legacyPointsAssertOK(t, adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": 10, "reason": "测试调整", "request_key": "key-adjust-1",
	}), &adjustResp)
	if !adjustResp.OK || !adjustResp.Duplicate || adjustResp.Balance != 20 {
		t.Fatalf("duplicate adjust resp = %+v body=%s", adjustResp, raw)
	}
	if got := legacyPointsLedgerCount(t, env, target.ID); got != beforeLedger+1 {
		t.Fatalf("duplicate adjust wrote ledger: %d", got)
	}
	if got := legacyPointsAuditCount(t, env, admin.ID, "admin.points.adjust"); got != 1 {
		t.Fatalf("duplicate adjust audited: %d", got)
	}
	if got := legacyPointsNotificationCount(t, env, target.ID); got != 1 {
		t.Fatalf("duplicate adjust notified: %d", got)
	}
	resp = adminClient.postJSON(adjustPath, csrf, map[string]any{
		"user_oid": targetLegacyID, "delta": -1000, "reason": "超额", "request_key": "key-overdraw",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("overdraw adjust = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	refundPath := "/legacy/api/admin/points/redemptions/" + strconv.FormatInt(redemptionID, 10) + "/refund"
	var refundResp struct {
		OK      bool `json:"ok"`
		Changed bool `json:"changed"`
		Balance int  `json:"balance"`
	}
	legacyPointsAssertOK(t, adminClient.postJSON(refundPath, csrf, map[string]any{"reason": "活动取消"}), &refundResp)
	if !refundResp.OK || !refundResp.Changed || refundResp.Balance != 40 {
		t.Fatalf("refund resp = %+v", refundResp)
	}
	ledgerAfterRefund := legacyPointsLedgerCount(t, env, target.ID)
	var refundAgain struct {
		OK      bool `json:"ok"`
		Changed bool `json:"changed"`
	}
	legacyPointsAssertOK(t, adminClient.postJSON(refundPath, csrf, map[string]any{}), &refundAgain)
	if !refundAgain.OK || refundAgain.Changed {
		t.Fatalf("second refund resp = %+v", refundAgain)
	}
	if got := legacyPointsLedgerCount(t, env, target.ID); got != ledgerAfterRefund {
		t.Fatalf("second refund wrote ledger: %d vs %d", got, ledgerAfterRefund)
	}

	markPath := "/legacy/api/admin/points/redemptions/" + strconv.FormatInt(redemptionID, 10) + "/mark"
	resp = adminClient.postJSON(markPath, csrf, map[string]any{"status": "已安排"})
	if resp.StatusCode != http.StatusBadRequest || legacyITError(t, resp) != "已退穗的兑换单不能再标记状态" {
		t.Fatalf("mark refunded = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var wishID int64
	if err := env.pool.QueryRow(env.ctx,
		`SELECT id FROM points_redemptions WHERE user_id = $1 AND item = 'wish' ORDER BY id DESC LIMIT 1`,
		target.ID).Scan(&wishID); err != nil {
		t.Fatalf("load wish redemption: %v", err)
	}
	wishMarkPath := "/legacy/api/admin/points/redemptions/" + strconv.FormatInt(wishID, 10) + "/mark"
	resp = adminClient.postJSON(wishMarkPath, csrf, map[string]any{"status": "已退穗"})
	if resp.StatusCode != http.StatusBadRequest || legacyITError(t, resp) != "此兑换单不支持该状态" {
		t.Fatalf("mark invalid status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var markResp struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	legacyPointsAssertOK(t, adminClient.postJSON(wishMarkPath, csrf, map[string]any{
		"status": "已安排", "note": "已排期",
	}), &markResp)
	if !markResp.OK || markResp.Status != "已安排" {
		t.Fatalf("mark resp = %+v", markResp)
	}
	if got := legacyPointsAuditCount(t, env, admin.ID, "admin.points.mark"); got != 1 {
		t.Fatalf("mark audit = %d", got)
	}
	resp = adminClient.postJSON("/legacy/api/admin/points/redemptions/99999999/mark", csrf,
		map[string]any{"status": "已安排"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("mark missing redemption = %d", resp.StatusCode)
	}
}

func TestIntegrationLegacyReportHandleHTTP(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	moderator, moderatorClient := env.createUser(t, "U-RPTHANDLE", "内容管理员", "active", "user")
	author, _ := env.createUser(t, "U-RPTAUTH", "留言作者", "active", "user")
	target, _ := env.createUser(t, "U-RPTTARGET", "被留言者", "active", "user")
	plain, plainClient := env.createUser(t, "U-RPTPLAIN", "普通用户", "active", "user")
	cleanupHTTPUsers(t, env, moderator.ID, author.ID, target.ID, plain.ID)
	if changed, err := env.database.AssignAdminRole(env.ctx, moderator.ID, "content_moderator", true); err != nil || !changed {
		t.Fatalf("assign content_moderator: changed=%v err=%v", changed, err)
	}

	messageIDs := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		var id int64
		if err := env.pool.QueryRow(env.ctx,
			`INSERT INTO messages (target_user_id, author_id, author_name, content, status)
			 VALUES ($1, $2, '留言作者', $3, '正常') RETURNING id`,
			target.ID, author.ID, fmt.Sprintf("待处理举报留言 %d", i)).
			Scan(&id); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		messageIDs = append(messageIDs, id)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = env.pool.Exec(cleanupCtx, `DELETE FROM reports WHERE target_message_id = ANY($1)`, messageIDs)
	})
	var replyID int64
	if err := env.pool.QueryRow(env.ctx,
		`INSERT INTO messages (target_user_id, author_id, author_name, content, status, parent_id)
		 VALUES ($1, $2, '留言作者', '回复内容', '正常', $3) RETURNING id`,
		target.ID, author.ID, messageIDs[1]).Scan(&replyID); err != nil {
		t.Fatalf("insert reply: %v", err)
	}
	for _, messageID := range messageIDs {
		if _, err := env.pool.Exec(env.ctx,
			`INSERT INTO reports (reporter_id, target_user_id, target_message_id,
			                      reporter_name, target_name, reason, status)
			 VALUES ($1, $2, $3, '留言作者', '被留言者', '不当内容', '待处理')`,
			author.ID, target.ID, messageID); err != nil {
			t.Fatalf("insert report: %v", err)
		}
	}

	handlePath := func(id int64) string {
		return "/legacy/api/messages/" + strconv.FormatInt(id, 10) + "/handle"
	}
	// The reported list stays readable for moderators; plain users are 403.
	resp := moderatorClient.get("/legacy/api/messages/reported")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reported list = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = plainClient.postJSON(handlePath(messageIDs[0]), plainClient.csrfCookie(), map[string]any{"action": "delete"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain handle = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = moderatorClient.postJSON(handlePath(messageIDs[0]), "", map[string]any{"action": "delete"})
	if resp.StatusCode != http.StatusForbidden || legacyITError(t, resp) != "表单已过期，请重新整理后再试" {
		t.Fatalf("handle without csrf = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	csrf := moderatorClient.csrfCookie()
	resp = moderatorClient.postJSON(handlePath(messageIDs[0]), csrf, map[string]any{"action": "maybe"})
	if resp.StatusCode != http.StatusBadRequest || legacyITError(t, resp) != "action 需为 approve 或 delete" {
		t.Fatalf("handle bad action = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = moderatorClient.postJSON("/legacy/api/messages/99999999/handle", csrf, map[string]any{"action": "approve"})
	if resp.StatusCode != http.StatusNotFound || legacyITError(t, resp) != "找不到待处理的举报" {
		t.Fatalf("handle missing report = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	// Reject keeps the message; the second call must not create a second audit.
	var handleResp struct {
		OK       bool  `json:"ok"`
		ReportID int64 `json:"report_id"`
		Deleted  bool  `json:"deleted"`
	}
	legacyPointsAssertOK(t, moderatorClient.postJSON(handlePath(messageIDs[0]), csrf,
		map[string]any{"action": "approve"}), &handleResp)
	if !handleResp.OK || handleResp.Deleted || handleResp.ReportID == 0 {
		t.Fatalf("approve resp = %+v", handleResp)
	}
	msg, err := env.database.GetMessageByID(env.ctx, messageIDs[0])
	if err != nil || msg.Status != db.MessageStatusNormal {
		t.Fatalf("message after approve = %+v err=%v", msg, err)
	}
	assertReportStatus(t, env, messageIDs[0], db.ReportStatusRejected)
	if got := legacyPointsAuditCount(t, env, moderator.ID, "message.report.handle"); got != 1 {
		t.Fatalf("approve audit = %d", got)
	}
	resp = moderatorClient.postJSON(handlePath(messageIDs[0]), csrf, map[string]any{"action": "approve"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("repeat approve = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := legacyPointsAuditCount(t, env, moderator.ID, "message.report.handle"); got != 1 {
		t.Fatalf("repeat approve audited: %d", got)
	}

	// Delete removes the message and its replies, and approves the report.
	legacyPointsAssertOK(t, moderatorClient.postJSON(handlePath(messageIDs[1]), csrf,
		map[string]any{"action": "delete"}), &handleResp)
	if !handleResp.OK || !handleResp.Deleted {
		t.Fatalf("delete resp = %+v", handleResp)
	}
	assertReportStatus(t, env, messageIDs[1], db.ReportStatusApproved)
	deleted, err := env.database.GetMessageByID(env.ctx, messageIDs[1])
	if err != nil || deleted.Status != db.MessageStatusDeleted {
		t.Fatalf("deleted message = %+v err=%v", deleted, err)
	}
	deletedReply, err := env.database.GetMessageByID(env.ctx, replyID)
	if err != nil || deletedReply.Status != db.MessageStatusDeleted {
		t.Fatalf("deleted reply = %+v err=%v", deletedReply, err)
	}
	if got := legacyPointsAuditCount(t, env, moderator.ID, "message.report.handle"); got != 2 {
		t.Fatalf("delete audit = %d", got)
	}
}

func assertReportStatus(t *testing.T, env *profileHTTPEnv, messageID int64, want string) {
	t.Helper()
	var status string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT status FROM reports WHERE target_message_id = $1 ORDER BY id DESC LIMIT 1`,
		messageID).Scan(&status); err != nil {
		t.Fatalf("load report status: %v", err)
	}
	if status != want {
		t.Fatalf("report status = %q want %q", status, want)
	}
}
