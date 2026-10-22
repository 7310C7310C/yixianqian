package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

type socialLikeRequest struct {
	TargetID     int64  `json:"target_id"`
	TargetOpenID string `json:"target_openid"`
	LikeType     string `json:"like_type"`
	Message      string `json:"message"`
}

type socialPraiseRequest struct {
	TargetID     int64  `json:"target_id"`
	TargetOpenID string `json:"target_openid"`
}

func decodeSocialJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return false
	}
	return true
}

func (a *App) handleAPICards(w http.ResponseWriter, r *http.Request) {
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
	if !a.socialRateLimit(w, r, viewer, socialCardLimiter, "cards", true) {
		return
	}
	limit := socialBoundedInt(r.URL.Query().Get("limit"), 1, 200)
	if limit == 0 {
		limit = 50
	}
	page, err := a.buildCardsPageData(r.Context(), viewer, r.URL.Query(), a.clock.Now(), limit)
	if errors.Is(err, errGenderMissing) {
		writeAPIError(w, http.StatusBadRequest, "gender_missing", "你的性别资料尚未完善，请先完善资料。")
		return
	}
	if err != nil {
		a.logger(r).Error("api cards failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入卡片，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cards":   page.Cards,
		"total":   page.Total,
		"offset":  page.Offset,
		"limit":   page.Limit,
		"version": page.Version,
		"seed":    page.Seed,
	})
}

func (a *App) handleAPILike(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	viewer, ok := a.socialViewerAPI(w, r, user)
	if !ok {
		return
	}
	if !a.socialAPIGate(w, r, viewer, true) {
		return
	}
	if !a.socialAPICSRF(w, r) {
		return
	}
	if !a.socialRateLimit(w, r, viewer, socialLikeLimiter, "like", true) {
		return
	}
	var req socialLikeRequest
	if !decodeSocialJSON(w, r, &req) {
		return
	}
	if req.LikeType == "" {
		req.LikeType = db.LikeTypeAnon
	}
	target, err := a.resolveSocialTarget(r.Context(), req.TargetID, req.TargetOpenID)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "target_not_found", "用户不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("resolve like target failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
		return
	}
	result, err := a.db.CreateLike(r.Context(), db.CreateLikeParams{
		InitiatorID: viewer.ID,
		TargetID:    target.ID,
		LikeType:    req.LikeType,
		Message:     req.Message,
		Now:         a.clock.Now(),
	})
	if err != nil {
		writeLikeError(w, err, req.LikeType)
		return
	}
	message := "喜欢成功。"
	if result.Mutual {
		message = "你们相互喜欢了！"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"mutual":         result.Mutual,
		"message":        message,
		"like":           map[string]any{"id": result.Like.ID, "status": result.Like.Status, "like_type": result.Like.LikeType},
		"anon_total":     result.Quota.AnonTotal,
		"anon_used":      result.Quota.AnonUsed,
		"anon_left":      result.Quota.AnonLeft,
		"real_total":     result.Quota.RealTotal,
		"real_left":      result.Quota.RealLeft,
		"real_permanent": result.Quota.RealPermanent,
	})
}

func writeLikeError(w http.ResponseWriter, err error, likeType string) {
	switch {
	case errors.Is(err, db.ErrSelfAction):
		writeAPIError(w, http.StatusBadRequest, "self_like", "不能喜欢自己。")
	case errors.Is(err, db.ErrSameGender):
		writeAPIError(w, http.StatusBadRequest, "same_gender", "仅限异性之间喜欢。")
	case errors.Is(err, db.ErrTargetUnavailable):
		writeAPIError(w, http.StatusBadRequest, "target_unavailable", "对方目前不在牵线名单中。")
	case errors.Is(err, db.ErrAlreadyLiked):
		writeAPIError(w, http.StatusConflict, "already_liked", "你已经喜欢过TA了。")
	case errors.Is(err, db.ErrInvalidLikeType):
		writeAPIError(w, http.StatusBadRequest, "invalid_like_type", "喜欢类型不正确。")
	case errors.Is(err, db.ErrLikeMessageShort):
		writeAPIError(w, http.StatusBadRequest, "message_too_short", "附言需 10 字以上。")
	case errors.Is(err, db.ErrLikeMessageLong):
		writeAPIError(w, http.StatusBadRequest, "message_too_long", "附言最多 500 字。")
	case errors.Is(err, db.ErrLikeQuota):
		if likeType == db.LikeTypeReal {
			writeAPIError(w, http.StatusBadRequest, "quota_exhausted", "本月实名喜欢次数已用完（每月 1 次，月初重置）。")
		} else {
			writeAPIError(w, http.StatusBadRequest, "quota_exhausted", "本月匿名喜欢额度已用完（每月 10 颗，月初补满）。")
		}
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "target_not_found", "用户不存在。")
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
	}
}

func (a *App) handleAPILikesMe(w http.ResponseWriter, r *http.Request) {
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
	overview, err := a.db.SocialOverview(r.Context(), viewer.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Error("api likes me failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"liked_me": likedMeAPI(overview),
		"mutual":   mutualAPI(overview),
	})
}

type socialLikedMeItem struct {
	Nickname        string `json:"nickname"`
	Message         string `json:"message"`
	Status          string `json:"status"`
	Mutual          bool   `json:"mutual"`
	Real            bool   `json:"real"`
	InitiatorOpenID string `json:"initiator_openid"`
	UserID          string `json:"user_id,omitempty"`
	TargetID        int64  `json:"target_id,omitempty"`
	LikeType        string `json:"like_type"`
	CreatedAt       string `json:"created_at"`
}

func likedMeAPI(o db.SocialOverview) []socialLikedMeItem {
	out := make([]socialLikedMeItem, 0, len(o.LikesMe))
	for _, pair := range o.LikesMe {
		if pair.Mutual {
			continue
		}
		item := socialLikedMeItem{
			Nickname:  "匿名用户",
			Status:    socialLikeStatusLabel(pair),
			Mutual:    false,
			Real:      pair.Like.LikeType == db.LikeTypeReal,
			LikeType:  pair.Like.LikeType,
			CreatedAt: formatSocialTime(pair.Like.CreatedAt),
		}
		if item.Real {
			item.Nickname = pair.Other.DisplayName()
			item.UserID = pair.Other.LegacyID
			item.InitiatorOpenID = socialCardOpenID(pair.Other)
			item.TargetID = pair.Other.ID
		}
		out = append(out, item)
	}
	return out
}

type socialMutualItem struct {
	Nickname        string `json:"nickname"`
	Message         string `json:"message"`
	Status          string `json:"status"`
	Mutual          bool   `json:"mutual"`
	Real            bool   `json:"real"`
	InitiatorOpenID string `json:"initiator_openid"`
	UserID          string `json:"user_id,omitempty"`
	TargetID        int64  `json:"target_id"`
	LikeType        string `json:"like_type"`
	CreatedAt       string `json:"created_at"`
	MyWechat        string `json:"my_wechat"`
	TheirWechat     string `json:"their_wechat"`
}

func mutualAPI(o db.SocialOverview) []socialMutualItem {
	pairs := uniqueMutualPairs(o)
	out := make([]socialMutualItem, 0, len(pairs))
	for _, pair := range pairs {
		item := socialMutualItem{
			Nickname:        pair.Other.DisplayName(),
			Message:         mutualMessage(o, pair),
			Status:          db.LikeStatusMutual,
			Mutual:          true,
			Real:            pair.Like.LikeType == db.LikeTypeReal,
			InitiatorOpenID: socialCardOpenID(pair.Other),
			UserID:          pair.Other.LegacyID,
			TargetID:        pair.Other.ID,
			LikeType:        pair.Like.LikeType,
			CreatedAt:       formatSocialTime(pair.Like.CreatedAt),
		}
		if o.Me.ContactConsent {
			item.MyWechat = o.Me.Wechat
		}
		if pair.Other.ContactConsent {
			item.TheirWechat = pair.Other.Wechat
		}
		out = append(out, item)
	}
	return out
}

func uniqueMutualPairs(o db.SocialOverview) []db.LikePair {
	seen := make(map[int64]bool)
	out := make([]db.LikePair, 0, len(o.LikesMe))
	for _, pair := range o.LikesMe {
		if !pair.Mutual || pair.Other.ID == 0 || seen[pair.Other.ID] {
			continue
		}
		seen[pair.Other.ID] = true
		out = append(out, pair)
	}
	for _, pair := range o.MyLikes {
		if !pair.Mutual || pair.Other.ID == 0 || seen[pair.Other.ID] {
			continue
		}
		seen[pair.Other.ID] = true
		out = append(out, pair)
	}
	return out
}

func (a *App) handleAPIMyLikes(w http.ResponseWriter, r *http.Request) {
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
	overview, err := a.db.SocialOverview(r.Context(), viewer.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Error("api my likes failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入，请稍后再试。")
		return
	}
	list := make([]map[string]any, 0, len(overview.MyLikes))
	for _, pair := range overview.MyLikes {
		item := map[string]any{
			"like_record_id": pair.Like.ID,
			"target_id":      pair.Other.ID,
			"openid":         socialCardOpenID(pair.Other),
			"user_id":        pair.Other.LegacyID,
			"nickname":       pair.Other.DisplayName(),
			"gender":         pair.Other.Gender,
			"mutual":         pair.Mutual,
			"active":         pair.Active,
			"like_type":      pair.Like.LikeType,
			"status":         pair.Like.Status,
			"message":        pair.Like.Message,
			"created_at":     formatSocialTime(pair.Like.CreatedAt),
		}
		if len(pair.Other.Photos) > 0 {
			item["photo"] = socialPhotoView(pair.Other.Photos[0]).ThumbURL
		} else {
			item["photo"] = ""
		}
		list = append(list, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"likes": list})
}

func (a *App) handleAPIPraise(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	viewer, ok := a.socialViewerAPI(w, r, user)
	if !ok {
		return
	}
	if !a.socialAPIGate(w, r, viewer, true) {
		return
	}
	if !a.socialAPICSRF(w, r) {
		return
	}
	if !a.socialRateLimit(w, r, viewer, socialPraiseLimiter, "praise", true) {
		return
	}
	var req socialPraiseRequest
	if !decodeSocialJSON(w, r, &req) {
		return
	}
	target, err := a.resolveSocialTarget(r.Context(), req.TargetID, req.TargetOpenID)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "target_not_found", "用户不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("resolve praise target failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
		return
	}
	result, err := a.db.CreatePraise(r.Context(), viewer.ID, target.ID, a.clock.Now())
	if err != nil {
		writePraiseError(w, err)
		return
	}
	message := "点赞成功。"
	if result.Already {
		message = "已经点过赞了。"
	}
	writePraiseState(w, http.StatusOK, true, result.Already, message, result.State)
}

func writePraiseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSelfAction):
		writeAPIError(w, http.StatusBadRequest, "self_praise", "不能给自己点赞。")
	case errors.Is(err, db.ErrSameGender):
		writeAPIError(w, http.StatusBadRequest, "same_gender", "仅限异性之间点赞。")
	case errors.Is(err, db.ErrTargetUnavailable):
		writeAPIError(w, http.StatusBadRequest, "target_unavailable", "对方目前不在牵线名单中。")
	case errors.Is(err, db.ErrPraiseQuota):
		writeAPIError(w, http.StatusBadRequest, "quota_exhausted", "今天已经点满 10 个赞了，明天再来。")
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "target_not_found", "用户不存在。")
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
	}
}

func writePraiseState(w http.ResponseWriter, status int, ok, already bool, message string, state db.PraiseState) {
	writeJSON(w, status, map[string]any{
		"ok":            ok,
		"already":       already,
		"message":       message,
		"limit":         state.Limit,
		"left":          state.Left,
		"used":          state.Used,
		"praised":       state.Praised,
		"received_week": state.ReceivedWeek,
	})
}

func (a *App) handleAPIPraiseCancel(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	viewer, ok := a.socialViewerAPI(w, r, user)
	if !ok {
		return
	}
	if !a.socialAPIGate(w, r, viewer, true) {
		return
	}
	if !a.socialAPICSRF(w, r) {
		return
	}
	if !a.socialRateLimit(w, r, viewer, socialPraiseLimiter, "praise", true) {
		return
	}
	targetID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("targetId")), 10, 64)
	if err != nil || targetID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_target", "目标用户不正确。")
		return
	}
	state, err := a.db.CancelPraise(r.Context(), viewer.ID, targetID, a.clock.Now())
	if err != nil {
		a.logger(r).Error("cancel praise failed", "error", err, "user_id", user.ID, "target_id", targetID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
		return
	}
	writePraiseState(w, http.StatusOK, true, false, "已取消点赞。", state)
}

func (a *App) handleAPIPraiseMe(w http.ResponseWriter, r *http.Request) {
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
	state, err := a.db.PraiseStateFor(r.Context(), viewer.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Error("api praise me failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入，请稍后再试。")
		return
	}
	writePraiseState(w, http.StatusOK, true, false, "", state)
}

func (a *App) handleAPINotifications(w http.ResponseWriter, r *http.Request) {
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
	rows, err := a.db.ListNotifications(r.Context(), viewer.ID, 50)
	if err != nil {
		a.logger(r).Error("api notifications failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入，请稍后再试。")
		return
	}
	unread, err := a.db.UnreadNotificationCount(r.Context(), viewer.ID)
	if err != nil {
		a.logger(r).Error("api notifications unread failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法载入，请稍后再试。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": notificationViews(rows),
		"unread":        unread,
	})
}
