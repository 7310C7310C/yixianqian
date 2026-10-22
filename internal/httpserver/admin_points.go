package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

const (
	adminPermPoints           = "points.adjust"
	adminPointsLedgerPageSize = 50
	adminPointsInviteLimit    = 100
	adminPointsRecentLimit    = 10
)

func init() { registerModule(registerAdminPoints) }

func registerAdminPoints(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/points", a.adminHTML(adminPermPoints, a.handleAdminPointsPage))
	mux.HandleFunc("POST /admin/points/adjust", a.adminHTML(adminPermPoints, a.handleAdminPointsAdjust))
	mux.HandleFunc("POST /admin/points/redemptions/{id}/refund", a.adminHTML(adminPermPoints, a.handleAdminPointsRefund))
	mux.HandleFunc("POST /admin/points/redemptions/{id}/mark", a.adminHTML(adminPermPoints, a.handleAdminPointsMark))
}

type adminPointUserView struct {
	db.AdminPointUser
	StatusLabel string
}

type adminRedemptionView struct {
	db.AdminRedemptionRow
	CanRefund   bool
	CanMark     bool
	MarkOptions []adminStatusOption
}

type adminPointsPageData struct {
	Tab                string
	Tabs               []adminStatusOption
	Stats              db.AdminPointsStats
	RecentLedger       []db.AdminLedgerRow
	RecentRedemptions  []adminRedemptionView
	PendingRedemptions []adminRedemptionView

	Query           string
	Users           []adminPointUserView
	Selected        *adminPointUserView
	SelectedEntries []db.PointsEntry
	AdjustKey       string
	SelectedMessage string

	Ledger        []db.AdminLedgerRow
	LedgerQuery   string
	LedgerUserID  string
	LedgerKind    string
	LedgerKinds   []adminStatusOption
	LedgerPage    int
	LedgerPrevURL string
	LedgerNextURL string

	Invites        []db.AdminInviteRow
	InviteCounts   db.AdminInviteCounts
	InviteStatus   string
	InviteStatuses []adminStatusOption

	Redemptions   []adminRedemptionView
	ItemFilter    string
	StatusFilter  string
	ItemOptions   []adminStatusOption
	StatusOptions []adminStatusOption
	Page          int
	PrevURL       string
	NextURL       string
}

func adminPointsTabs() []adminStatusOption {
	return []adminStatusOption{
		{Value: "overview", Label: "总览"},
		{Value: "users", Label: "用户"},
		{Value: "ledger", Label: "流水"},
		{Value: "invites", Label: "邀请"},
		{Value: "redemptions", Label: "兑换"},
		{Value: "wishes", Label: "心愿单"},
		{Value: "recommendations", Label: "推荐单"},
	}
}

func normalizeAdminPointsTab(raw string) string {
	switch raw {
	case "users", "ledger", "invites", "redemptions", "wishes", "recommendations":
		return raw
	default:
		return "overview"
	}
}

func adminPointsLedgerKinds() []adminStatusOption {
	return []adminStatusOption{
		{Value: "", Label: "全部类型"},
		{Value: db.PointsKindInvite, Label: "邀请好友"},
		{Value: db.PointsKindAssist, Label: "协助组织"},
		{Value: db.PointsKindAdmin, Label: "管理员调整"},
		{Value: db.PointsKindRedeem, Label: "兑换"},
		{Value: db.PointsKindRefund, Label: "退还"},
		{Value: db.PointsKindClawback, Label: "收回"},
	}
}

func normalizeAdminPointsKind(raw string) string {
	switch raw {
	case db.PointsKindInvite, db.PointsKindAssist, db.PointsKindAdmin,
		db.PointsKindRedeem, db.PointsKindRefund, db.PointsKindClawback:
		return raw
	default:
		return ""
	}
}

func adminPointsItemOptions() []adminStatusOption {
	out := []adminStatusOption{{Value: "", Label: "全部项目"}}
	for _, item := range []string{db.RedeemItemRealLike, db.RedeemItemPriority,
		db.RedeemItemWish, db.RedeemItemMatchmaker} {
		out = append(out, adminStatusOption{Value: item, Label: db.RedeemItemLabels[item]})
	}
	return out
}

func adminPointsStatusOptions() []adminStatusOption {
	out := []adminStatusOption{{Value: "", Label: "全部状态"}}
	for _, status := range []string{db.RedeemStatusActive, db.RedeemStatusPending,
		db.RedeemStatusArranged, db.RedeemStatusRecommended,
		db.RedeemStatusDone, db.RedeemStatusCancelled, db.RedeemStatusRefunded} {
		out = append(out, adminStatusOption{Value: status, Label: db.RedeemStatusLabels[status]})
	}
	return out
}

func adminPointsInviteStatusOptions() []adminStatusOption {
	return []adminStatusOption{
		{Value: "", Label: "全部状态"},
		{Value: db.InviteStatusPending, Label: "待确认"},
		{Value: db.InviteStatusConfirmed, Label: "待确认（已绑定）"},
		{Value: db.InviteStatusFinalized, Label: "已到账"},
		{Value: db.InviteStatusClawedBack, Label: "已收回"},
		{Value: db.InviteStatusRejected, Label: "无效邀请"},
	}
}

func normalizeAdminPointsItem(raw string) string {
	if _, ok := db.RedeemItemLabels[raw]; ok {
		return raw
	}
	return ""
}

func normalizeAdminPointsStatus(raw string) string {
	if _, ok := db.RedeemStatusLabels[raw]; ok {
		return raw
	}
	return ""
}

func normalizeAdminPointsInviteStatus(raw string) string {
	switch raw {
	case db.InviteStatusPending, db.InviteStatusConfirmed, db.InviteStatusFinalized,
		db.InviteStatusClawedBack, db.InviteStatusRejected:
		return raw
	default:
		return ""
	}
}

func adminPointsUserStatusLabel(u db.AdminPointUser) string {
	var label string
	switch u.AccountStatus {
	case "pending":
		label = "待审核"
	case "active":
		label = "已通过"
	case "rejected":
		label = "已拒绝"
	case "disabled":
		label = "已停用"
	case "matched":
		label = "已脱单"
	case "left":
		label = "已退出"
	case "observer":
		label = "观察员"
	default:
		label = u.AccountStatus
	}
	if u.LoginStatus == "disabled" {
		label += " · 禁止登录"
	}
	return label
}

func adminPointUserViews(list []db.AdminPointUser) []adminPointUserView {
	out := make([]adminPointUserView, 0, len(list))
	for _, u := range list {
		out = append(out, adminPointUserView{
			AdminPointUser: u,
			StatusLabel:    adminPointsUserStatusLabel(u),
		})
	}
	return out
}

func adminRedemptionMarkOptions(item string) []adminStatusOption {
	switch item {
	case db.RedeemItemWish:
		return []adminStatusOption{
			{Value: db.RedeemStatusArranged, Label: "已安排"},
			{Value: db.RedeemStatusDone, Label: "已完成"},
			{Value: db.RedeemStatusCancelled, Label: "已取消"},
		}
	case db.RedeemItemMatchmaker:
		return []adminStatusOption{
			{Value: db.RedeemStatusRecommended, Label: "已推荐"},
			{Value: db.RedeemStatusDone, Label: "已完成"},
			{Value: db.RedeemStatusCancelled, Label: "已取消"},
		}
	default:
		return nil
	}
}

func adminCanRefundRedemption(status string) bool {
	return status == db.RedeemStatusActive ||
		status == db.RedeemStatusPending ||
		status == db.RedeemStatusRecommended
}

func adminRedemptionViews(list []db.AdminRedemptionRow) []adminRedemptionView {
	out := make([]adminRedemptionView, 0, len(list))
	for _, row := range list {
		view := adminRedemptionView{
			AdminRedemptionRow: row,
			CanRefund:          adminCanRefundRedemption(row.Status),
		}
		view.MarkOptions = adminRedemptionMarkOptions(row.Item)
		if len(view.MarkOptions) > 0 &&
			row.Status != db.RedeemStatusRefunded &&
			row.Status != db.RedeemStatusDone &&
			row.Status != db.RedeemStatusCancelled {
			view.CanMark = true
		}
		out = append(out, view)
	}
	return out
}

func adminPointsNotice(code string) string {
	switch code {
	case "adjusted":
		return "麦穗调整已入账。"
	case "duplicate":
		return "这笔调整已经记过账，未重复入账。"
	case "refunded":
		return "已退穗，麦穗已退回用户账户。"
	case "refund_none":
		return "这笔兑换已退穗或当前状态不可退，未做变更。"
	case "marked":
		return "兑换单状态已更新。"
	default:
		return ""
	}
}

func adminPointsURL(tab string, values url.Values) string {
	out := url.Values{}
	for key, list := range values {
		for _, value := range list {
			if strings.TrimSpace(value) != "" {
				out.Add(key, value)
			}
		}
	}
	out.Set("tab", tab)
	return "/admin/points?" + out.Encode()
}

func adminPointsPageNumber(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}
	if page > 100000 {
		return 100000
	}
	return page
}

func (a *App) handleAdminPointsPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	tab := normalizeAdminPointsTab(r.URL.Query().Get("tab"))
	data, err := a.loadAdminPointsData(r, tab, r.URL.Query())
	if err != nil {
		a.logger(r).Error("load admin points page failed", "tab", tab, "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "admin_points", PageData{
		Title:     "麦穗管理",
		User:      &access.User,
		Data:      data,
		Notice:    adminPointsNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin_points.css",
		ActiveNav: "points",
	})
}

func (a *App) loadAdminPointsData(r *http.Request, tab string, q url.Values) (adminPointsPageData, error) {
	ctx := r.Context()
	data := adminPointsPageData{
		Tab:  tab,
		Tabs: adminPointsTabs(),
	}
	stats, err := a.db.AdminPointsStats(ctx)
	if err != nil {
		return data, err
	}
	data.Stats = stats

	switch tab {
	case "users":
		if err := a.loadAdminPointsUsers(ctx, &data, q); err != nil {
			return data, err
		}
	case "ledger":
		if err := a.loadAdminPointsLedger(ctx, &data, q); err != nil {
			return data, err
		}
	case "invites":
		if err := a.loadAdminPointsInvites(r, &data, q); err != nil {
			return data, err
		}
	case "redemptions", "wishes", "recommendations":
		if err := a.loadAdminPointsRedemptions(ctx, &data, q); err != nil {
			return data, err
		}
	default:
		recent, err := a.db.ListAdminLedger(ctx, db.AdminLedgerFilter{Limit: adminPointsRecentLimit})
		if err != nil {
			return data, err
		}
		data.RecentLedger = recent
		recentOrders, err := a.db.ListAdminRedemptions(ctx, db.AdminRedemptionFilter{Limit: adminPointsRecentLimit})
		if err != nil {
			return data, err
		}
		data.RecentRedemptions = adminRedemptionViews(recentOrders)
		pendingOrders, err := a.db.ListAdminRedemptions(ctx, db.AdminRedemptionFilter{
			Status: db.RedeemStatusPending,
			Limit:  adminPointsRecentLimit,
		})
		if err != nil {
			return data, err
		}
		data.PendingRedemptions = adminRedemptionViews(pendingOrders)
	}
	return data, nil
}

func (a *App) loadAdminPointsUsers(ctx context.Context, data *adminPointsPageData, q url.Values) error {
	data.Query = strings.TrimSpace(q.Get("q"))
	users, err := a.db.SearchPointUsers(ctx, data.Query, 50)
	if err != nil {
		return err
	}
	data.Users = adminPointUserViews(users)

	rawID := strings.TrimSpace(q.Get("user_id"))
	if rawID == "" {
		return nil
	}
	userID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || userID <= 0 {
		data.SelectedMessage = "用户识别码格式不正确。"
		return nil
	}
	user, err := a.db.GetPointUser(ctx, userID)
	if errors.Is(err, db.ErrNotFound) {
		data.SelectedMessage = "找不到该用户。"
		return nil
	}
	if err != nil {
		return err
	}
	view := adminPointUserViews([]db.AdminPointUser{user})[0]
	data.Selected = &view
	entries, err := a.db.ListPointsEntries(ctx, userID, 50, 0, nil)
	if err != nil {
		return err
	}
	data.SelectedEntries = entries
	key, err := platform.NewUUID()
	if err != nil {
		return err
	}
	data.AdjustKey = key
	return nil
}

func (a *App) loadAdminPointsLedger(ctx context.Context, data *adminPointsPageData, q url.Values) error {
	data.LedgerQuery = strings.TrimSpace(q.Get("q"))
	data.LedgerUserID = strings.TrimSpace(q.Get("user_id"))
	data.LedgerKind = normalizeAdminPointsKind(q.Get("kind"))
	data.LedgerKinds = adminPointsLedgerKinds()
	data.LedgerPage = adminPointsPageNumber(q.Get("page"))

	var userID int64
	if data.LedgerUserID != "" {
		if id, err := strconv.ParseInt(data.LedgerUserID, 10, 64); err == nil && id > 0 {
			userID = id
		}
	}
	rows, err := a.db.ListAdminLedger(ctx, db.AdminLedgerFilter{
		UserID: userID,
		Query:  data.LedgerQuery,
		Kind:   data.LedgerKind,
		Limit:  adminPointsLedgerPageSize + 1,
		Offset: (data.LedgerPage - 1) * adminPointsLedgerPageSize,
	})
	if err != nil {
		return err
	}
	if len(rows) > adminPointsLedgerPageSize {
		rows = rows[:adminPointsLedgerPageSize]
		if data.LedgerPage < 100000 {
			data.LedgerNextURL = adminPointsLedgerURL(data, data.LedgerPage+1)
		}
	}
	data.Ledger = rows
	if data.LedgerPage > 1 {
		data.LedgerPrevURL = adminPointsLedgerURL(data, data.LedgerPage-1)
	}
	return nil
}

func adminPointsLedgerURL(data *adminPointsPageData, page int) string {
	values := url.Values{}
	if data.LedgerQuery != "" {
		values.Set("q", data.LedgerQuery)
	}
	if data.LedgerUserID != "" {
		values.Set("user_id", data.LedgerUserID)
	}
	if data.LedgerKind != "" {
		values.Set("kind", data.LedgerKind)
	}
	values.Set("page", strconv.Itoa(page))
	return adminPointsURL("ledger", values)
}

func (a *App) loadAdminPointsInvites(r *http.Request, data *adminPointsPageData, q url.Values) error {
	ctx := r.Context()
	if _, _, err := a.db.SettleDueInvites(ctx, 200); err != nil {
		a.logger(r).Warn("settle due invites before admin list failed", "error", err)
	}
	data.InviteStatus = normalizeAdminPointsInviteStatus(q.Get("status"))
	data.InviteStatuses = adminPointsInviteStatusOptions()
	counts, err := a.db.AdminInviteCounts(ctx)
	if err != nil {
		return err
	}
	data.InviteCounts = counts
	list, err := a.db.ListAdminInvites(ctx, data.InviteStatus, adminPointsInviteLimit)
	if err != nil {
		return err
	}
	data.Invites = list
	return nil
}

func (a *App) loadAdminPointsRedemptions(ctx context.Context, data *adminPointsPageData, q url.Values) error {
	data.ItemOptions = adminPointsItemOptions()
	data.StatusOptions = adminPointsStatusOptions()
	data.StatusFilter = normalizeAdminPointsStatus(q.Get("status"))
	data.Page = adminPointsPageNumber(q.Get("page"))

	switch data.Tab {
	case "wishes":
		data.ItemFilter = db.RedeemItemWish
	case "recommendations":
		data.ItemFilter = db.RedeemItemMatchmaker
	default:
		data.ItemFilter = normalizeAdminPointsItem(q.Get("item"))
	}

	rows, err := a.db.ListAdminRedemptions(ctx, db.AdminRedemptionFilter{
		Item:   data.ItemFilter,
		Status: data.StatusFilter,
		Limit:  adminPointsLedgerPageSize + 1,
		Offset: (data.Page - 1) * adminPointsLedgerPageSize,
	})
	if err != nil {
		return err
	}
	if len(rows) > adminPointsLedgerPageSize {
		rows = rows[:adminPointsLedgerPageSize]
		if data.Page < 100000 {
			data.NextURL = adminPointsRedemptionURL(data, data.Page+1)
		}
	}
	data.Redemptions = adminRedemptionViews(rows)
	if data.Page > 1 {
		data.PrevURL = adminPointsRedemptionURL(data, data.Page-1)
	}
	return nil
}

func adminPointsRedemptionURL(data *adminPointsPageData, page int) string {
	values := url.Values{}
	if data.ItemFilter != "" && data.Tab == "redemptions" {
		values.Set("item", data.ItemFilter)
	}
	if data.StatusFilter != "" {
		values.Set("status", data.StatusFilter)
	}
	values.Set("page", strconv.Itoa(page))
	return adminPointsURL(data.Tab, values)
}

func (a *App) renderAdminPointsFormError(w http.ResponseWriter, r *http.Request, access adminAccess,
	tab string, q url.Values, message string, status int) {
	data, err := a.loadAdminPointsData(r, tab, q)
	if err != nil {
		a.logger(r).Error("load admin points page after error failed", "tab", tab, "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.render(w, r, status, "admin_points", PageData{
		Title:     "麦穗管理",
		User:      &access.User,
		Data:      data,
		Error:     message,
		PageCSS:   "admin_points.css",
		ActiveNav: "points",
	})
}

func adminPointsReasonValue(r *http.Request) string {
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if len([]rune(reason)) > adminMaxNoteRunes {
		reason = string([]rune(reason)[:adminMaxNoteRunes])
	}
	return reason
}

func adminPointsUserFormValues(r *http.Request) url.Values {
	values := url.Values{}
	if userID := strings.TrimSpace(r.PostFormValue("user_id")); userID != "" {
		values.Set("user_id", userID)
	}
	if query := strings.TrimSpace(r.PostFormValue("q")); query != "" {
		values.Set("q", query)
	}
	return values
}

func adminPointsUserURL(userID int64, query, notice string) string {
	values := url.Values{}
	values.Set("user_id", strconv.FormatInt(userID, 10))
	if query != "" {
		values.Set("q", query)
	}
	if notice != "" {
		values.Set("notice", notice)
	}
	return adminPointsURL("users", values)
}

func adminPointsTabURL(tab, notice string) string {
	values := url.Values{}
	if notice != "" {
		values.Set("notice", notice)
	}
	return adminPointsURL(tab, values)
}

func (a *App) handleAdminPointsAdjust(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.adminFormPost(w, r) {
		return
	}
	values := adminPointsUserFormValues(r)
	userID, err := strconv.ParseInt(values.Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		a.renderAdminPointsFormError(w, r, access, "users", values,
			"用户识别码格式不正确。", http.StatusBadRequest)
		return
	}
	delta, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("delta")))
	if err != nil {
		a.renderAdminPointsFormError(w, r, access, "users", values,
			"请输入整数麦穗数量，可为负数。", http.StatusBadRequest)
		return
	}
	if delta == 0 {
		a.renderAdminPointsFormError(w, r, access, "users", values,
			"麦穗数量不能为 0。", http.StatusBadRequest)
		return
	}
	reason := adminPointsReasonValue(r)
	if reason == "" {
		a.renderAdminPointsFormError(w, r, access, "users", values,
			"请填写调整原因。", http.StatusBadRequest)
		return
	}
	opKey := strings.TrimSpace(r.PostFormValue("op_key"))
	if opKey == "" || len(opKey) > 128 {
		a.renderAdminPointsFormError(w, r, access, "users", values,
			"表单已过期，请重新整理页面后再试。", http.StatusBadRequest)
		return
	}
	entry, balance, err := a.db.AdjustPoints(r.Context(), userID, delta, reason,
		&access.User.ID, "admin_adjust:"+opKey)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrPointsDuplicate):
			http.Redirect(w, r, adminPointsUserURL(userID, values.Get("q"), "duplicate"),
				http.StatusSeeOther)
		case errors.Is(err, db.ErrPointsNotFound):
			a.renderError(w, r, http.StatusNotFound, "找不到用户", "此用户不存在。")
		case errors.Is(err, db.ErrPointsInsufficient), errors.Is(err, db.ErrPointsInvalidDelta):
			message := db.PointsMessage(err)
			if message == "" {
				message = "麦穗数量不正确。"
			}
			a.renderAdminPointsFormError(w, r, access, "users", values,
				message, http.StatusConflict)
		default:
			a.logger(r).Error("admin adjust points failed", "error", err, "user_id", userID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
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
	http.Redirect(w, r, adminPointsUserURL(userID, values.Get("q"), "adjusted"),
		http.StatusSeeOther)
}

func (a *App) handleAdminPointsRefund(w http.ResponseWriter, r *http.Request, access adminAccess) {
	redemptionID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	tab := normalizeAdminPointsTab(r.PostFormValue("tab"))
	order, err := a.db.GetAdminRedemption(r.Context(), redemptionID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.renderError(w, r, http.StatusNotFound, "找不到兑换单", "此兑换单不存在。")
		} else {
			a.logger(r).Error("load admin redemption failed", "error", err, "redemption_id", redemptionID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return
	}
	reason := adminPointsReasonValue(r)
	result, err := a.db.RefundRedemption(r.Context(), redemptionID, reason,
		db.PointsKindRefund, &access.User.ID, db.RedeemStatusRefunded)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrPointsNotFound):
			a.renderError(w, r, http.StatusNotFound, "找不到兑换单", "此兑换单不存在。")
		case errors.Is(err, db.ErrPointsUnavailable):
			message := db.PointsMessage(err)
			if message == "" {
				message = "这笔兑换当前不能退穗。"
			}
			a.renderAdminPointsFormError(w, r, access, tab, url.Values{},
				message, http.StatusConflict)
		default:
			a.logger(r).Error("refund admin redemption failed", "error", err, "redemption_id", redemptionID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return
	}
	if result == nil {
		http.Redirect(w, r, adminPointsTabURL(tab, "refund_none"), http.StatusSeeOther)
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
	http.Redirect(w, r, adminPointsTabURL(tab, "refunded"), http.StatusSeeOther)
}

func (a *App) handleAdminPointsMark(w http.ResponseWriter, r *http.Request, access adminAccess) {
	redemptionID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	tab := normalizeAdminPointsTab(r.PostFormValue("tab"))
	order, err := a.db.GetAdminRedemption(r.Context(), redemptionID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.renderError(w, r, http.StatusNotFound, "找不到兑换单", "此兑换单不存在。")
		} else {
			a.logger(r).Error("load admin redemption failed", "error", err, "redemption_id", redemptionID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return
	}
	if order.Status == db.RedeemStatusRefunded {
		a.renderAdminPointsFormError(w, r, access, tab, url.Values{},
			"已退穗的兑换单不能再标记状态。", http.StatusBadRequest)
		return
	}
	status := strings.TrimSpace(r.PostFormValue("status"))
	allowed := false
	for _, option := range adminRedemptionMarkOptions(order.Item) {
		if option.Value == status {
			allowed = true
			break
		}
	}
	if !allowed {
		a.renderAdminPointsFormError(w, r, access, tab, url.Values{},
			"此兑换单不支持该状态。", http.StatusBadRequest)
		return
	}
	note := noteValue(r)
	updated, err := a.db.MarkRedemption(r.Context(), redemptionID, status, note)
	if err != nil {
		if errors.Is(err, db.ErrPointsUnavailable) || errors.Is(err, db.ErrPointsNotFound) {
			message := db.PointsMessage(err)
			if message == "" {
				message = "兑换单状态更新失败。"
			}
			a.renderAdminPointsFormError(w, r, access, tab, url.Values{},
				message, http.StatusConflict)
			return
		}
		a.logger(r).Error("mark admin redemption failed", "error", err, "redemption_id", redemptionID)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		return
	}
	meta := map[string]any{
		"item": order.Item,
		"from": order.Status,
		"to":   updated.Status,
	}
	if note != "" {
		meta["note"] = note
	}
	a.audit(r, &access.User.ID, "admin.points.mark", "redemption",
		strconv.FormatInt(redemptionID, 10), meta)
	http.Redirect(w, r, adminPointsTabURL(tab, "marked"), http.StatusSeeOther)
}
