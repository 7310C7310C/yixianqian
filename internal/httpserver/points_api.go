package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const pointsInviteShareText = "我在一线牵脱单ing，实名认证的单身青年，你来看看有没有眼缘～"

var pointsItemDescriptions = map[string]string{
	db.RedeemItemRealLike:   "免费实名喜欢用完后，多一次实名喜欢的机会，不过期。",
	db.RedeemItemPriority:   "如果活动需要优化男女比例、满员等，你可直接报名，不受规则限制。",
	db.RedeemItemWish:       "报名成功后，你可以指定一位想认识的人，组织方去邀请TA参加活动，并安排同桌。对方不会知道是你想认识TA。",
	db.RedeemItemMatchmaker: "填写你的择偶条件，红娘人工帮你物色并牵线。",
}

var pointsRuleNotes = []string{
	"麦穗不过期、不能转赠、不能提现。",
	"邀请奖励在被邀请人账号正常满确认期后到账；到账前显示「待确认」，不可使用。",
	"被邀请人须是新人：没注册过 App、也没参加过以往活动。",
	"同一笔兑换的麦穗，只有在活动取消、或按规则允许退出的情况下才会退回原账户。",
}

var pointsItemOrder = []string{
	db.RedeemItemRealLike,
	db.RedeemItemPriority,
	db.RedeemItemWish,
	db.RedeemItemMatchmaker,
}

type pointsItemJSON struct {
	Item  string `json:"item"`
	Label string `json:"label"`
	Cost  int    `json:"cost"`
	Desc  string `json:"desc"`
}

type pointsMeJSON struct {
	Balance       int              `json:"balance"`
	Earned        int              `json:"earned"`
	Spent         int              `json:"spent"`
	PendingPoints int              `json:"pending_points"`
	PendingCount  int              `json:"pending_count"`
	ExtraRealLike int              `json:"extra_real_like"`
	RealLikeQuota int              `json:"real_like_quota"`
	RealLeft      int              `json:"real_left"`
	RealTotal     int              `json:"real_total"`
	Entries       []db.PointsEntry `json:"entries"`
}

type pointsInviteJSON struct {
	InviterID   string            `json:"inviter_id"`
	InviteCode  string            `json:"invite_code"`
	ShareText   string            `json:"share_text"`
	RegisterURL string            `json:"register_url"`
	Progress    db.InviteProgress `json:"progress"`
	List        []db.InviteView   `json:"list"`
}

type pointsRedeemJSON struct {
	Item       string `json:"item"`
	RequestKey string `json:"request_key"`
	ActivityID string `json:"activity_id"`
	ActivityNo string `json:"activity_no"`
	TargetID   int64  `json:"target_id"`
	Condition  string `json:"condition"`
}

func (a *App) handleAPIPointsMe(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.pointsViewerAPI(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	a.settlePointsForViewer(r, viewer.ID)
	summary, err := a.db.PointsSummary(ctx, viewer.ID)
	if err != nil {
		a.logger(r).Error("points summary failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	entries, err := a.db.ListPointsEntries(ctx, viewer.ID, 50, 0, nil)
	if err != nil {
		a.logger(r).Error("points entries failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	progress, err := a.db.InviteProgress(ctx, viewer.ID)
	if err != nil {
		a.logger(r).Error("points invite progress failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	extra, err := a.db.ExtraRealLikeQuota(ctx, viewer.ID)
	if err != nil {
		a.logger(r).Error("points real-like quota failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	quota, err := a.db.PointsLikeQuota(ctx, viewer.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Error("points like quota failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, pointsMeJSON{
		Balance:       summary.Balance,
		Earned:        summary.Earned,
		Spent:         summary.Spent,
		PendingPoints: progress.PendingPoints,
		PendingCount:  progress.Confirmed,
		ExtraRealLike: extra,
		RealLikeQuota: extra,
		RealLeft:      quota.RealLeft,
		RealTotal:     quota.RealTotal,
		Entries:       entries,
	})
}

func (a *App) handleAPIPointsRules(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	a.settlePointsForViewer(r, user.ID)
	cfg, err := a.db.GetPointsConfig(r.Context())
	if err != nil {
		a.logger(r).Error("points rules config failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "规则暂时读不到，请稍后再试。")
		return
	}
	items := make([]pointsItemJSON, 0, len(pointsItemOrder))
	for _, item := range pointsItemOrder {
		cost, err := pointsItemCostJSON(cfg, item)
		if err != nil {
			continue
		}
		items = append(items, pointsItemJSON{
			Item: item, Label: db.RedeemItemLabels[item], Cost: cost,
			Desc: pointsItemDescriptions[item],
		})
	}
	balance, err := a.db.PointsBalance(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("points rules balance failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "麦穗资料暂时读不到，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"config":  cfg.Values(),
		"notes":   pointsRuleNotes,
		"balance": balance,
	})
}

func pointsItemCostJSON(cfg db.PointsConfig, item string) (int, error) {
	switch item {
	case db.RedeemItemRealLike:
		return cfg.RedeemRealLike, nil
	case db.RedeemItemPriority:
		return cfg.RedeemPrioritySignup, nil
	case db.RedeemItemWish:
		return cfg.RedeemWish, nil
	case db.RedeemItemMatchmaker:
		return cfg.RedeemMatchmaker, nil
	default:
		return 0, errors.New("unknown item")
	}
}

func (a *App) handleAPIPointsInvite(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	viewer, ok := a.socialViewerAPI(w, r, user)
	if !ok {
		return
	}
	if !a.socialAPIGate(w, r, viewer, false) {
		return
	}
	ctx := r.Context()
	a.settlePointsForViewer(r, viewer.ID)
	progress, err := a.db.InviteProgress(ctx, viewer.ID)
	if err != nil {
		a.logger(r).Error("points invite progress failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "邀请资料暂时读不到，请稍后再试。")
		return
	}
	list, err := a.db.ListInvitesFor(ctx, viewer.ID, 100)
	if err != nil {
		a.logger(r).Error("points invite list failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "邀请资料暂时读不到，请稍后再试。")
		return
	}
	code := legacyID(&user)
	if code == "—" {
		code = ""
	}
	writeJSON(w, http.StatusOK, pointsInviteJSON{
		InviterID:   code,
		InviteCode:  code,
		ShareText:   pointsInviteShareText,
		RegisterURL: a.pointsRegisterURL(code),
		Progress:    progress,
		List:        list,
	})
}

func (a *App) pointsRegisterURL(code string) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	base := strings.TrimRight(a.cfg.BaseURL, "/")
	if base == "" {
		return "/register?inviter=" + url.QueryEscape(code)
	}
	return base + "/register?inviter=" + url.QueryEscape(code)
}

func decodePointsJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return false
	}
	return true
}

func (a *App) handleAPIPointsRedeem(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.pointsViewerAPI(w, r, true)
	if !ok {
		return
	}
	if !a.socialAPICSRF(w, r) {
		return
	}
	if !a.socialRateLimit(w, r, viewer, pointsRedeemLimiter, "points_redeem", true) {
		return
	}
	a.settlePointsForViewer(r, viewer.ID)
	var req pointsRedeemJSON
	if !decodePointsJSON(w, r, &req) {
		return
	}
	req.Item = strings.TrimSpace(req.Item)
	if _, ok := db.RedeemItemLabels[req.Item]; !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_item", "没有这个兑换项。")
		return
	}
	activityID := strings.TrimSpace(req.ActivityID)
	if activityID == "" {
		activityID = strings.TrimSpace(req.ActivityNo)
	}
	order, err := a.db.RedeemPoints(r.Context(), viewer.ID, db.RedeemRequest{
		Item:         req.Item,
		RequestKey:   strings.TrimSpace(req.RequestKey),
		ActivityID:   activityID,
		TargetUserID: req.TargetID,
		Condition:    strings.TrimSpace(req.Condition),
	})
	if err != nil {
		writePointsAPIError(w, err)
		return
	}
	balance, err := a.db.PointsBalance(r.Context(), viewer.ID)
	if err != nil {
		a.logger(r).Error("points balance after redeem failed", "error", err, "user_id", viewer.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": order.ItemLabel + "兑换成功",
		"balance": balance,
		"order":   order,
	})
}

func (a *App) handleAPIPointsRedemptions(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.pointsViewerAPI(w, r, false)
	if !ok {
		return
	}
	list, err := a.db.ListRedemptionsFor(r.Context(), viewer.ID, 100, "")
	if err != nil {
		a.logger(r).Error("points redemptions failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "兑换记录暂时读不到，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"list": list})
}

func (a *App) handleAPIPointsWishTargets(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.pointsViewerAPI(w, r, true)
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}
	list, err := a.db.ListWishTargets(r.Context(), viewer.ID,
		strings.TrimSpace(r.URL.Query().Get("q")), limit)
	if err != nil {
		a.logger(r).Error("points wish targets failed", "error", err, "user_id", viewer.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入名单，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"list": list, "total": len(list)})
}

func writePointsAPIError(w http.ResponseWriter, err error) {
	message := db.PointsMessage(err)
	status := http.StatusBadRequest
	code := "unavailable"
	switch {
	case errors.Is(err, db.ErrPointsAlreadyRedeemed), errors.Is(err, db.ErrPointsDuplicate):
		status, code = http.StatusConflict, "already_redeemed"
	case errors.Is(err, db.ErrPointsInsufficient):
		code = "insufficient_points"
	case errors.Is(err, db.ErrPointsUnavailable), errors.Is(err, db.ErrPointsInviteDue):
		code = "unavailable"
	case errors.Is(err, db.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, db.ErrPointsInvalidKind), errors.Is(err, db.ErrPointsInvalidDelta):
		code = "invalid_request"
	default:
		status, code = http.StatusInternalServerError, "internal_error"
		message = ""
	}
	if message == "" {
		if status == http.StatusInternalServerError {
			message = "暂时无法处理，请稍后再试。"
		} else {
			message = "无法完成操作，请稍后再试。"
		}
	}
	writeAPIError(w, status, code, message)
}
