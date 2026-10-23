package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// registerLegacyPointsAdmin restores the endpoints consumed by the bundled v1
// 麦穗管理 page. The page only reads; the write routes exist so the adapter can
// support the same adjust/refund/mark actions as the SSR console, with the
// v1 error envelope and the same permission model.
func registerLegacyPointsAdmin(a *App, mux *http.ServeMux) {
	points := []string{adminPermPoints}
	mux.HandleFunc("GET /legacy/api/admin/points/overview",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsOverview))
	mux.HandleFunc("GET /legacy/api/admin/points/ledger",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsLedger))
	mux.HandleFunc("GET /legacy/api/admin/points/redemptions",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsRedemptions))
	mux.HandleFunc("GET /legacy/api/admin/points/invites",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsInvites))
	mux.HandleFunc("GET /legacy/api/admin/points/user",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsUser))
	mux.HandleFunc("POST /legacy/api/admin/points/adjust",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsAdjust))
	mux.HandleFunc("POST /legacy/api/admin/points/redemptions/{id}/refund",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsRefund))
	mux.HandleFunc("POST /legacy/api/admin/points/redemptions/{id}/mark",
		a.legacyAdminAPI(points, a.handleLegacyAdminPointsMark))
}

func init() { registerModule(registerLegacyPointsAdmin) }

const (
	// The SPA lists every user/row in one call; these caps keep a pathological
	// ledger from turning the admin page into a memory spike.
	legacyPointsRowLimit  = 200
	legacyPointsRowMax    = 500
	legacyPointsUserLimit = 200
	legacyPointsAdjustMax = 100000
	legacyPointsJSONBytes = 64 << 10
	legacyPointsReasonMax = 200
)

type legacyPointsAdjustRequest struct {
	UserOID    string `json:"user_oid"`
	Delta      int    `json:"delta"`
	Reason     string `json:"reason"`
	RequestKey string `json:"request_key"`
}

type legacyPointsRefundRequest struct {
	Reason string `json:"reason"`
}

type legacyPointsMarkRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func readLegacyPointsBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, legacyPointsJSONBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return false
	}
	return true
}

// legacyPointsRedeemStatusCode accepts both the persisted status code and the
// Chinese label. The SSR console posts codes, while a v1-era client may echo
// the label it rendered; both must resolve to the same transition.
func legacyPointsRedeemStatusCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if _, ok := db.RedeemStatusLabels[raw]; ok {
		return raw
	}
	for code, label := range db.RedeemStatusLabels {
		if label == raw {
			return code
		}
	}
	return raw
}

func legacyPointsPageLimit(raw string, def, max int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func legacyPointsPageOffset(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func (a *App) handleLegacyAdminPointsOverview(w http.ResponseWriter, r *http.Request, access adminAccess) {
	ctx := r.Context()
	stats, err := a.db.LegacyPointsOverviewStats(ctx)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points overview stats failed")
		return
	}
	users, err := a.db.ListLegacyPointsUsers(ctx, legacyPointsUserLimit)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points overview users failed")
		return
	}
	config, err := a.db.DescribePointsConfig(ctx)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points config failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats": legacyPointsOverviewStats{
			UserCount:       stats.UserCount,
			EntryCount:      stats.EntryCount,
			Earned:          stats.Earned,
			Spent:           stats.Spent,
			Outstanding:     stats.Outstanding,
			RedemptionCount: stats.RedemptionCount,
			InviteCount:     stats.InviteCount,
		},
		"config": config,
		"users":  legacyPointsOverviewUsers(users),
	})
}

func (a *App) handleLegacyAdminPointsLedger(w http.ResponseWriter, r *http.Request, access adminAccess) {
	ctx := r.Context()
	query := r.URL.Query()
	var userID int64
	if ref := strings.TrimSpace(query.Get("user_oid")); ref != "" {
		resolved, err := a.resolveLegacyUserID(ctx, ref)
		if errors.Is(err, db.ErrNotFound) {
			legacyError(w, http.StatusNotFound, "找不到用户")
			return
		}
		if err != nil {
			legacyServerError(a, w, r, err, "legacy points ledger resolve user failed")
			return
		}
		userID = resolved
	}
	rows, err := a.db.ListAdminLedger(ctx, db.AdminLedgerFilter{
		UserID: userID,
		Limit:  legacyPointsPageLimit(query.Get("limit"), legacyPointsRowLimit, legacyPointsRowMax),
		Offset: legacyPointsPageOffset(query.Get("offset")),
	})
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points ledger failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": legacyPointsLedgerRows(rows)})
}

func (a *App) handleLegacyAdminPointsRedemptions(w http.ResponseWriter, r *http.Request, access adminAccess) {
	query := r.URL.Query()
	rows, err := a.db.ListAdminRedemptions(r.Context(), db.AdminRedemptionFilter{
		Limit:  legacyPointsPageLimit(query.Get("limit"), legacyPointsRowLimit, legacyPointsRowMax),
		Offset: legacyPointsPageOffset(query.Get("offset")),
	})
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points redemptions failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": legacyPointsRedemptionRows(rows)})
}

func (a *App) handleLegacyAdminPointsInvites(w http.ResponseWriter, r *http.Request, access adminAccess) {
	limit := legacyPointsPageLimit(r.URL.Query().Get("limit"), legacyPointsRowLimit, legacyPointsRowLimit)
	rows, err := a.db.ListAdminInvites(r.Context(), "", limit)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points invites failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": legacyPointsInviteRows(rows)})
}

func (a *App) handleLegacyAdminPointsUser(w http.ResponseWriter, r *http.Request, access adminAccess) {
	ctx := r.Context()
	ref := strings.TrimSpace(r.URL.Query().Get("user_oid"))
	userID, err := a.resolveLegacyUserID(ctx, ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到用户")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user resolve failed")
		return
	}
	user, err := a.db.GetPointUser(ctx, userID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到用户")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user failed")
		return
	}
	summary, err := a.db.PointsSummary(ctx, userID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user summary failed")
		return
	}
	extraRealLike, err := a.db.ExtraRealLikeQuota(ctx, userID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user quota failed")
		return
	}
	earnedByKind, err := a.db.LegacyPointsEarnedByKind(ctx, userID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user earned kinds failed")
		return
	}
	redeemedByItem, err := a.db.LegacyPointsRedeemedByItem(ctx, userID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user redeemed items failed")
		return
	}
	progress, err := a.db.InviteProgress(ctx, userID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user invite progress failed")
		return
	}
	invites, err := a.db.ListInvitesFor(ctx, userID, 20)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user invites failed")
		return
	}
	redemptions, err := a.db.ListRedemptionsFor(ctx, userID, 20, "")
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user redemptions failed")
		return
	}
	entries, err := a.db.ListPointsEntries(ctx, userID, 50, 0, nil)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points user entries failed")
		return
	}
	writeJSON(w, http.StatusOK, legacyPointsUserDetailJSON{
		User: legacyPointsUserRef{
			Nickname: legacyPointsNickname(user.Nickname, user.Name),
			UserID:   legacyPointsUserID(user.LegacyUserID, user.ID),
		},
		Summary: legacyPointsSummary{
			Balance: summary.Balance,
			Earned:  summary.Earned,
			Spent:   summary.Spent,
		},
		ExtraRealLike:  extraRealLike,
		EarnedByKind:   legacyPointsKindTotals(earnedByKind),
		RedeemedByItem: legacyPointsItemTotals(redeemedByItem),
		Invites: legacyPointsInviteProgress{
			Total:      progress.Total,
			Pending:    progress.Pending,
			Confirmed:  progress.Confirmed,
			Finalized:  progress.Finalized,
			ClawedBack: progress.ClawedBack,
			Rejected:   progress.Rejected,
		},
		InviteList:  legacyPointsInviteList(invites),
		Redemptions: legacyPointsUserRedemptions(redemptions),
		Entries:     legacyPointsUserEntries(entries),
	})
}

func (a *App) handleLegacyAdminPointsAdjust(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var body legacyPointsAdjustRequest
	if !readLegacyPointsBody(w, r, &body) {
		return
	}
	ctx := r.Context()
	userID, err := a.resolveLegacyUserID(ctx, body.UserOID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到用户")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points adjust resolve user failed")
		return
	}
	if body.Delta == 0 {
		legacyError(w, http.StatusBadRequest, "麦穗数量不能为 0")
		return
	}
	if body.Delta > legacyPointsAdjustMax || body.Delta < -legacyPointsAdjustMax {
		legacyError(w, http.StatusBadRequest, "麦穗数量超出范围")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		legacyError(w, http.StatusBadRequest, "请填写调整原因")
		return
	}
	if utf8.RuneCountInString(reason) > legacyPointsReasonMax {
		legacyError(w, http.StatusBadRequest, "调整原因最多 200 字")
		return
	}
	requestKey := strings.TrimSpace(body.RequestKey)
	if requestKey == "" || len(requestKey) > 128 {
		legacyError(w, http.StatusBadRequest, "缺少请求标识，请刷新后重试")
		return
	}
	entry, balance, err := a.db.AdjustPoints(ctx, userID, body.Delta, reason,
		&access.User.ID, "admin_adjust:"+requestKey)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrPointsDuplicate):
			current, balanceErr := a.db.PointsBalance(ctx, userID)
			if balanceErr != nil {
				legacyServerError(a, w, r, balanceErr, "legacy points adjust balance failed")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"duplicate": true,
				"balance":   current,
				"message":   "这笔调整已经记过账，未重复入账",
			})
		case errors.Is(err, db.ErrPointsNotFound):
			legacyError(w, http.StatusNotFound, "找不到用户")
		case errors.Is(err, db.ErrPointsInsufficient), errors.Is(err, db.ErrPointsInvalidDelta):
			message := db.PointsMessage(err)
			if message == "" {
				message = "麦穗数量不正确"
			}
			legacyError(w, http.StatusConflict, message)
		default:
			legacyServerError(a, w, r, err, "legacy points adjust failed")
		}
		return
	}
	a.audit(r, &access.User.ID, "admin.points.adjust", "user",
		strconv.FormatInt(userID, 10), map[string]any{
			"delta":         entry.Delta,
			"balance_after": balance,
			"reason":        reason,
		})
	a.adminNotifyUser(r, userID, "points", "麦穗有变动",
		fmt.Sprintf("管理员为你调整了 %+d 穗：%s（当前余额 %d 穗）", entry.Delta, reason, balance), "")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"duplicate": false,
		"balance":   balance,
		"entry_id":  entry.ID,
		"message":   "已调整",
	})
}

func (a *App) handleLegacyAdminPointsRefund(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	redemptionID, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "兑换单编号不正确")
		return
	}
	var body legacyPointsRefundRequest
	if !readLegacyPointsBody(w, r, &body) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if utf8.RuneCountInString(reason) > legacyPointsReasonMax {
		legacyError(w, http.StatusBadRequest, "退穗原因最多 200 字")
		return
	}
	ctx := r.Context()
	order, err := a.db.GetAdminRedemption(ctx, redemptionID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到兑换单")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points refund load failed")
		return
	}
	result, err := a.db.RefundRedemption(ctx, redemptionID, reason,
		db.PointsKindRefund, &access.User.ID, db.RedeemStatusRefunded)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrPointsNotFound):
			legacyError(w, http.StatusNotFound, "找不到兑换单")
		case errors.Is(err, db.ErrPointsUnavailable):
			message := db.PointsMessage(err)
			if message == "" {
				message = "这笔兑换当前不能退穗"
			}
			legacyError(w, http.StatusConflict, message)
		default:
			legacyServerError(a, w, r, err, "legacy points refund failed")
		}
		return
	}
	if result == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"changed": false,
			"message": "已退穗或当前状态不可退",
		})
		return
	}
	a.audit(r, &access.User.ID, "admin.points.refund", "redemption",
		strconv.FormatInt(redemptionID, 10), map[string]any{
			"item":         order.Item,
			"cost":         order.Cost,
			"status":       result.Redemption.Status,
			"refund_entry": result.RefundEntryID,
			"balance":      result.Balance,
			"reason":       reason,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"changed": true,
		"balance": result.Balance,
		"status":  result.Redemption.Status,
		"message": "已退穗",
	})
}

func (a *App) handleLegacyAdminPointsMark(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	redemptionID, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "兑换单编号不正确")
		return
	}
	var body legacyPointsMarkRequest
	if !readLegacyPointsBody(w, r, &body) {
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(body.Note)) > adminMaxNoteRunes {
		legacyError(w, http.StatusBadRequest, "备注最多 500 字")
		return
	}
	ctx := r.Context()
	order, err := a.db.GetAdminRedemption(ctx, redemptionID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到兑换单")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points mark load failed")
		return
	}
	if order.Status == db.RedeemStatusRefunded {
		legacyError(w, http.StatusBadRequest, "已退穗的兑换单不能再标记状态")
		return
	}
	status := legacyPointsRedeemStatusCode(strings.TrimSpace(body.Status))
	allowed := false
	for _, option := range adminRedemptionMarkOptions(order.Item) {
		if option.Value == status {
			allowed = true
			break
		}
	}
	if !allowed {
		legacyError(w, http.StatusBadRequest, "此兑换单不支持该状态")
		return
	}
	updated, err := a.db.MarkRedemption(ctx, redemptionID, status, strings.TrimSpace(body.Note))
	if err != nil {
		switch {
		case errors.Is(err, db.ErrPointsNotFound):
			legacyError(w, http.StatusNotFound, "找不到兑换单")
		case errors.Is(err, db.ErrPointsUnavailable):
			message := db.PointsMessage(err)
			if message == "" {
				message = "兑换单状态更新失败"
			}
			legacyError(w, http.StatusConflict, message)
		default:
			legacyServerError(a, w, r, err, "legacy points mark failed")
		}
		return
	}
	a.audit(r, &access.User.ID, "admin.points.mark", "redemption",
		strconv.FormatInt(redemptionID, 10), map[string]any{
			"item":   updated.Item,
			"status": updated.Status,
			"note":   strings.TrimSpace(body.Note),
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"status":      updated.StatusLabel,
		"status_code": updated.Status,
		"message":     "兑换单状态已更新",
	})
}
