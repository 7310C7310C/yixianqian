package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerLegacyPointsAPI) }

func registerLegacyPointsAPI(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /legacy/api/points/rules", a.handleLegacyPointsRules)
	mux.HandleFunc("GET /legacy/api/points/invite", a.handleLegacyPointsInvite)
	mux.HandleFunc("GET /legacy/api/points/redemptions", a.handleLegacyPointsRedemptions)
	mux.HandleFunc("GET /legacy/api/points/wish-targets", a.handleLegacyPointsWishTargets)
	mux.HandleFunc("POST /legacy/api/points/redeem", a.handleLegacyPointsRedeem)
}

func (a *App) legacyPointsViewer(w http.ResponseWriter, r *http.Request, active bool) (db.User, db.SocialViewer, bool) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return db.User{}, db.SocialViewer{}, false
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return db.User{}, db.SocialViewer{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points viewer failed")
		return db.User{}, db.SocialViewer{}, false
	}
	if _, message, ok := socialAccountState(viewer, active); !ok {
		legacyError(w, http.StatusForbidden, message)
		return db.User{}, db.SocialViewer{}, false
	}
	return user, viewer, true
}

// legacyWritePointsMe fills the v1 /points/me shape. The route itself lives in
// legacy_api.go; this helper keeps the projection in the profile module.
func (a *App) legacyWritePointsMe(w http.ResponseWriter, r *http.Request, user db.User) {
	viewer, ok := a.legacyPointsViewerFor(w, r, user, false)
	if !ok {
		return
	}
	ctx := r.Context()
	a.settlePointsForViewer(r, viewer.ID)
	summary, err := a.db.PointsSummary(ctx, viewer.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points summary failed")
		return
	}
	entries, err := a.db.ListPointsEntries(ctx, viewer.ID, 50, 0, nil)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points entries failed")
		return
	}
	progress, err := a.db.InviteProgress(ctx, viewer.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points invite progress failed")
		return
	}
	extra, err := a.db.ExtraRealLikeQuota(ctx, viewer.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points real-like quota failed")
		return
	}
	quota, err := a.db.PointsLikeQuota(ctx, viewer.ID, a.clock.Now())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points like quota failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"balance":         summary.Balance,
		"earned":          summary.Earned,
		"spent":           summary.Spent,
		"pending_points":  progress.PendingPoints,
		"pending_count":   progress.Confirmed,
		"extra_real_like": extra,
		"real_like_quota": extra,
		"real_left":       quota.RealLeft,
		"real_total":      quota.RealTotal,
		"entries":         legacyPointsEntries(entries),
	})
}

// legacyPointsViewerFor reuses an already-authenticated user so the /points/me
// handler does not read the session twice.
func (a *App) legacyPointsViewerFor(w http.ResponseWriter, r *http.Request, user db.User, active bool) (db.SocialViewer, bool) {
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return db.SocialViewer{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points viewer failed")
		return db.SocialViewer{}, false
	}
	if _, message, ok := socialAccountState(viewer, active); !ok {
		legacyError(w, http.StatusForbidden, message)
		return db.SocialViewer{}, false
	}
	return viewer, true
}

func legacyPointsEntries(entries []db.PointsEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, map[string]any{
			"id":            entry.ID,
			"delta":         entry.Delta,
			"balance_after": entry.BalanceAfter,
			"kind":          entry.Kind,
			"kind_label":    entry.KindLabel,
			"reason":        entry.Reason,
			"ref_type":      entry.RefType,
			"ref_id":        entry.RefID,
			"created_at":    legacyFormatTime(entry.CreatedAt),
		})
	}
	return out
}

func (a *App) handleLegacyPointsRules(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	a.settlePointsForViewer(r, user.ID)
	cfg, err := a.db.GetPointsConfig(r.Context())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points rules config failed")
		return
	}
	items := make([]map[string]any, 0, len(pointsItemOrder))
	for _, item := range pointsItemOrder {
		cost, err := pointsItemCostJSON(cfg, item)
		if err != nil {
			continue
		}
		items = append(items, map[string]any{
			"item":  item,
			"label": db.RedeemItemLabels[item],
			"cost":  cost,
			"desc":  pointsItemDescriptions[item],
		})
	}
	balance, err := a.db.PointsBalance(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points rules balance failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"config":  cfg.Values(),
		"notes":   pointsRuleNotes,
		"balance": balance,
	})
}

func (a *App) handleLegacyPointsInvite(w http.ResponseWriter, r *http.Request) {
	user, viewer, ok := a.legacyPointsViewer(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	a.settlePointsForViewer(r, viewer.ID)
	progress, err := a.db.InviteProgress(ctx, viewer.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points invite progress failed")
		return
	}
	list, err := a.db.ListInvitesFor(ctx, viewer.ID, 100)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points invite list failed")
		return
	}
	code := legacyID(&user)
	if code == "—" {
		code = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inviter_id":   code,
		"invite_code":  code,
		"share_text":   pointsInviteShareText,
		"register_url": a.pointsRegisterURL(code),
		"progress":     progress,
		"list":         list,
	})
}

func (a *App) handleLegacyPointsRedemptions(w http.ResponseWriter, r *http.Request) {
	_, viewer, ok := a.legacyPointsViewer(w, r, false)
	if !ok {
		return
	}
	list, err := a.db.ListRedemptionsFor(r.Context(), viewer.ID, 100, "")
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points redemptions failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"list": legacyRedemptions(list)})
}

func legacyRedemptions(list []db.Redemption) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, order := range list {
		out = append(out, map[string]any{
			"id":           order.ID,
			"item":         order.Item,
			"item_label":   order.ItemLabel,
			"cost":         order.Cost,
			"status":       order.Status,
			"status_label": order.StatusLabel,
			"params":       order.Params,
			"ref_id":       order.RefID,
			"created_at":   legacyFormatTime(order.CreatedAt),
			"resolved_at":  legacyFormatTimePtr(order.ResolvedAt),
			"note":         order.Note,
		})
	}
	return out
}

func legacyFormatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return legacyFormatTime(*t)
}

func (a *App) handleLegacyPointsWishTargets(w http.ResponseWriter, r *http.Request) {
	_, viewer, ok := a.legacyPointsViewer(w, r, true)
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}
	targets, err := a.db.ListWishTargets(r.Context(), viewer.ID,
		strings.TrimSpace(r.URL.Query().Get("q")), limit)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy points wish targets failed")
		return
	}
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	profiles := map[int64]db.SocialProfile{}
	if len(ids) > 0 {
		if loaded, err := a.db.GetSocialProfilesByIDs(r.Context(), ids); err != nil {
			a.logger(r).Warn("legacy wish target profiles failed", "error", err, "user_id", viewer.ID)
		} else {
			profiles = loaded
		}
	}
	list := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		openID := strconv.FormatInt(target.ID, 10)
		if profile, ok := profiles[target.ID]; ok {
			openID = socialCardOpenID(profile)
			if target.Nickname == "" {
				target.Nickname = profile.DisplayName()
			}
		}
		list = append(list, map[string]any{
			"openid":   openID,
			"nickname": target.Nickname,
			"user_id":  target.UserID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"list": list, "total": len(list)})
}

type legacyRedeemRequest struct {
	Item             string `json:"item"`
	RequestKey       string `json:"request_key"`
	ActivityRecordID string `json:"activity_record_id"`
	ActivityID       string `json:"activity_id"`
	TargetOpenID     string `json:"target_open_id"`
	Condition        string `json:"condition"`
}

func (a *App) handleLegacyPointsRedeem(w http.ResponseWriter, r *http.Request) {
	_, viewer, ok := a.legacyPointsViewer(w, r, true)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if ok, retry := pointsRedeemLimiter.Allow(fmt.Sprintf("points_redeem:%d", viewer.ID)); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		legacyError(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
		return
	}
	var req legacyRedeemRequest
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	req.Item = strings.TrimSpace(req.Item)
	if _, ok := db.RedeemItemLabels[req.Item]; !ok {
		legacyError(w, http.StatusBadRequest, "没有这个兑换项")
		return
	}
	activityID := strings.TrimSpace(req.ActivityRecordID)
	if activityID == "" {
		activityID = strings.TrimSpace(req.ActivityID)
	}
	var targetID int64
	if req.Item == db.RedeemItemWish {
		id, err := a.resolveLegacyUserID(r.Context(), req.TargetOpenID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				legacyError(w, http.StatusBadRequest, "请选择心愿对象")
			} else {
				legacyServerError(a, w, r, err, "legacy wish target resolve failed")
			}
			return
		}
		targetID = id
	}
	order, err := a.db.RedeemPoints(r.Context(), viewer.ID, db.RedeemRequest{
		Item:         req.Item,
		RequestKey:   strings.TrimSpace(req.RequestKey),
		ActivityID:   activityID,
		TargetUserID: targetID,
		Condition:    strings.TrimSpace(req.Condition),
	})
	if err != nil {
		if legacyPointsBusinessError(err) {
			legacyPointsError(w, err)
		} else {
			legacyServerError(a, w, r, err, "legacy points redeem failed")
		}
		return
	}
	balance, err := a.db.PointsBalance(r.Context(), viewer.ID)
	if err != nil {
		a.logger(r).Warn("legacy points balance after redeem failed", "error", err, "user_id", viewer.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": order.ItemLabel + "兑换成功",
		"balance": balance,
		"order":   order,
	})
}

func legacyPointsBusinessError(err error) bool {
	return errors.Is(err, db.ErrPointsInsufficient) ||
		errors.Is(err, db.ErrPointsUnavailable) ||
		errors.Is(err, db.ErrPointsAlreadyRedeemed) ||
		errors.Is(err, db.ErrPointsDuplicate) ||
		errors.Is(err, db.ErrPointsInviteDue) ||
		errors.Is(err, db.ErrPointsInvalidKind) ||
		errors.Is(err, db.ErrPointsInvalidDelta) ||
		errors.Is(err, db.ErrPointsNotFound) ||
		errors.Is(err, db.ErrNotFound)
}

func (a *App) resolveLegacyUserID(ctx context.Context, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, db.ErrNotFound
	}
	if social, err := a.db.GetSocialProfileByLegacyOpenID(ctx, ref); err == nil {
		return social.ID, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return 0, err
	}
	if profile, err := a.db.GetProfileByLegacyID(ctx, ref); err == nil {
		return profile.ID, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return 0, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	return 0, db.ErrNotFound
}
