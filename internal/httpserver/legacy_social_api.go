package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerLegacySocial) }

// registerLegacySocial mounts the v1 social write endpoints. The legacy SPA
// calls these through BASE_PATH=/legacy and expects the flat {"error": "..."}
// envelope, so they deliberately do not reuse the newer API handlers.
func registerLegacySocial(a *App, mux *http.ServeMux) {
	mux.HandleFunc("POST /legacy/api/like", a.handleLegacyLike)
	mux.HandleFunc("POST /legacy/api/praise", a.handleLegacyPraise)
	mux.HandleFunc("DELETE /legacy/api/praise/{openid}", a.handleLegacyPraiseCancel)
	mux.HandleFunc("POST /legacy/api/mutual/wechat", a.handleLegacyMutualWechat)

	mux.HandleFunc("GET /legacy/api/messages", a.handleLegacyMessages)
	mux.HandleFunc("POST /legacy/api/messages", a.handleLegacyMessageCreate)
	mux.HandleFunc("DELETE /legacy/api/messages/{id}", a.handleLegacyMessageDelete)
	mux.HandleFunc("POST /legacy/api/messages/{id}/report", a.handleLegacyMessageReport)
	mux.HandleFunc("GET /legacy/api/messages/mine", a.handleLegacyMessagesMine)
	mux.HandleFunc("GET /legacy/api/messages/received", a.handleLegacyMessagesReceived)
	mux.HandleFunc("GET /legacy/api/messages/reported", a.handleLegacyMessagesReported)

	mux.HandleFunc("GET /legacy/api/notifications", a.handleLegacyNotificationsList)
	mux.HandleFunc("GET /legacy/api/notifications/stream", a.handleLegacyNotificationsStream)
	mux.HandleFunc("POST /legacy/api/notifications/{id}/read", a.handleLegacyNotificationRead)
	mux.HandleFunc("POST /legacy/api/notifications/read-all", a.handleLegacyNotificationsReadAll)
}

type legacySocialLikeRequest struct {
	TargetID     int64  `json:"target_id"`
	TargetOpenID string `json:"target_openid"`
	LikeType     string `json:"like_type"`
	Message      string `json:"message"`
}

type legacySocialPraiseRequest struct {
	TargetID     int64  `json:"target_id"`
	TargetOpenID string `json:"target_openid"`
}

type legacySocialMessageRequest struct {
	Target       string          `json:"target"`
	TargetOpenID string          `json:"target_openid"`
	Content      string          `json:"content"`
	ParentID     json.RawMessage `json:"parent_id"`
	ReplyToID    json.RawMessage `json:"reply_to_id"`
}

type legacySocialReportRequest struct {
	Reason string `json:"reason"`
}

type legacyMutualWechatRequest struct {
	Wechat string `json:"wechat"`
	Agree  bool   `json:"agree"`
}

// handleLegacyMutualWechat fills a missing wechat and records the explicit
// mutual-page consent. The value is only ever disclosed through the mutual
// list once both sides are ready.
func (a *App) handleLegacyMutualWechat(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	viewer, ok := a.legacySocialViewerFor(w, r, user)
	if !ok {
		return
	}
	if !a.legacySocialGate(w, viewer, true) {
		return
	}
	var req legacyMutualWechatRequest
	if !decodeLegacyJSON(w, r, &req) {
		return
	}
	current, err := a.db.GetWechatConsentState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy mutual wechat state failed")
		return
	}
	wechat := strings.TrimSpace(current.Wechat)
	setWechat := false
	if raw := strings.TrimSpace(req.Wechat); raw != "" {
		if raw == current.Wechat {
			// Legacy/migrated values were not validated by v1; keep them when
			// the user only toggles consent, otherwise the form prefill would
			// block a perfectly valid existing contact.
			wechat = raw
		} else {
			validated, verr := validateWechatID(raw)
			if verr != nil {
				legacyError(w, http.StatusBadRequest, verr.Error())
				return
			}
			wechat = validated
			setWechat = true
		}
	}
	if req.Agree && wechat == "" {
		legacyError(w, http.StatusBadRequest, "请先填写微信号")
		return
	}
	if !setWechat && req.Agree == current.ContactConsent {
		// Nothing changed, still return the current state for the SPA.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":              true,
			"wechat_ready":    socialWechatReady(db.SocialProfile{Wechat: wechat, ContactConsent: req.Agree}),
			"wechat_required": !(wechat != "" && req.Agree),
			"message":         "已保存",
		})
		return
	}
	if err := a.db.UpdateWechatConsent(r.Context(), user.ID, wechat, setWechat, req.Agree); err != nil {
		legacyServerError(a, w, r, err, "legacy mutual wechat update failed")
		return
	}
	a.audit(r, &user.ID, "profile.wechat.exchange_consent", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"consent": req.Agree, "wechat_changed": setWechat})
	a.ensureMutualWechatForUser(r.Context(), user.ID)
	ready := wechat != "" && req.Agree
	message := "已保存，双方都同意后即可看到对方微信号"
	if !req.Agree {
		message = "已关闭微信号交换"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"wechat_ready":    ready,
		"wechat_required": !ready,
		"message":         message,
	})
}

func decodeLegacyJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return false
	}
	return true
}

func decodeOptionalLegacyJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return false
	}
	return true
}

func (a *App) legacySocialGate(w http.ResponseWriter, u db.SocialViewer, active bool) bool {
	if _, message, ok := socialAccountState(u, active); !ok {
		legacyError(w, http.StatusForbidden, message)
		return false
	}
	return true
}

func (a *App) legacyRateLimit(w http.ResponseWriter, limiterKey string, prefix string, userID int64) bool {
	var limiter = socialLikeLimiter
	switch limiterKey {
	case "like":
		limiter = socialLikeLimiter
	case "praise":
		limiter = socialPraiseLimiter
	case "message":
		limiter = messageCreateLimiter
	case "report":
		limiter = messageReportLimiter
	}
	ok, retry := limiter.Allow(prefix + ":" + strconv.FormatInt(userID, 10))
	if ok {
		return true
	}
	w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
	legacyError(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
	return false
}

func (a *App) legacyMessageViewGate(w http.ResponseWriter, r *http.Request, user db.User) bool {
	status, err := a.db.GetAccountStatus(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message status failed")
		return false
	}
	if message, blocked := messageViewBlock(status); blocked {
		legacyError(w, http.StatusForbidden, message)
		return false
	}
	return true
}

func (a *App) legacyMessagePostGate(w http.ResponseWriter, r *http.Request, user db.User) bool {
	status, err := a.db.GetAccountStatus(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message status failed")
		return false
	}
	if message, blocked := messagePostBlock(status); blocked {
		legacyError(w, http.StatusForbidden, message)
		return false
	}
	return true
}

// resolveLegacyTarget accepts the legacy open id, a U-xxxx legacy user id, or
// a numeric database id.
func (a *App) resolveLegacyTarget(ctx context.Context, ref string) (db.SocialProfile, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.SocialProfile{}, db.ErrNotFound
	}
	profile, err := a.db.GetSocialProfileByLegacyOpenID(ctx, ref)
	if err == nil || !errors.Is(err, db.ErrNotFound) {
		return profile, err
	}
	user, err := a.db.FindUserByRef(ctx, ref)
	if err != nil {
		return db.SocialProfile{}, err
	}
	return a.db.GetSocialProfile(ctx, user.ID)
}

func writeLegacyLikeError(a *App, w http.ResponseWriter, r *http.Request, err error, likeType string) {
	switch {
	case errors.Is(err, db.ErrSelfAction):
		legacyError(w, http.StatusBadRequest, "不能喜欢自己")
	case errors.Is(err, db.ErrSameGender):
		legacyError(w, http.StatusBadRequest, "仅限异性之间喜欢")
	case errors.Is(err, db.ErrTargetUnavailable):
		legacyError(w, http.StatusBadRequest, "对方当前不在牵线名单中")
	case errors.Is(err, db.ErrAlreadyLiked):
		legacyError(w, http.StatusBadRequest, "你已经喜欢过TA了")
	case errors.Is(err, db.ErrInvalidLikeType):
		legacyError(w, http.StatusBadRequest, "like_type 不合法")
	case errors.Is(err, db.ErrLikeMessageShort):
		legacyError(w, http.StatusBadRequest, "附言需 10 字以上")
	case errors.Is(err, db.ErrLikeMessageLong):
		legacyError(w, http.StatusBadRequest, "附言最多 500 字")
	case errors.Is(err, db.ErrLikeQuota):
		if likeType == db.LikeTypeReal {
			legacyError(w, http.StatusBadRequest, "本月实名喜欢机会已用完（每月 1 次，月初重置）")
		} else {
			legacyError(w, http.StatusBadRequest, "本月匿名喜欢额度已用完（每月 10 颗，月初补满）")
		}
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
	default:
		legacyServerError(a, w, r, err, "legacy like failed")
	}
}

func (a *App) handleLegacyLike(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacySocialLikeRequest
	if !decodeLegacyJSON(w, r, &req) {
		return
	}
	if req.LikeType == "" {
		req.LikeType = db.LikeTypeAnon
	}
	if req.LikeType != db.LikeTypeAnon && req.LikeType != db.LikeTypeReal {
		legacyError(w, http.StatusBadRequest, "like_type 不合法")
		return
	}
	ref := strings.TrimSpace(req.TargetOpenID)
	if ref == "" && req.TargetID > 0 {
		ref = strconv.FormatInt(req.TargetID, 10)
	}
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少目标用户")
		return
	}
	viewer, ok := a.legacySocialViewerFor(w, r, user)
	if !ok {
		return
	}
	if !a.legacySocialGate(w, viewer, true) {
		return
	}
	if !a.legacyRateLimit(w, "like", "like", user.ID) {
		return
	}
	target, err := a.resolveLegacyTarget(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy like target failed")
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
		writeLegacyLikeError(a, w, r, err, req.LikeType)
		return
	}
	message := "喜欢成功"
	if result.Mutual {
		message = "你们相互喜欢了！"
		a.ensureMutualWechatForUser(r.Context(), viewer.ID)
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

func (a *App) legacySocialViewerFor(w http.ResponseWriter, r *http.Request, user db.User) (db.SocialViewer, bool) {
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return db.SocialViewer{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy social viewer failed")
		return db.SocialViewer{}, false
	}
	return viewer, true
}

func writeLegacyPraiseError(a *App, w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrSelfAction):
		legacyError(w, http.StatusBadRequest, "不能给自己点赞")
	case errors.Is(err, db.ErrSameGender):
		legacyError(w, http.StatusBadRequest, "仅限异性之间点赞")
	case errors.Is(err, db.ErrTargetUnavailable):
		legacyError(w, http.StatusBadRequest, "对方当前不在牵线名单中")
	case errors.Is(err, db.ErrPraiseQuota):
		legacyError(w, http.StatusBadRequest, "今天已经点满 10 个赞了，明天再来")
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
	default:
		legacyServerError(a, w, r, err, "legacy praise failed")
	}
}

func (a *App) handleLegacyPraise(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacySocialPraiseRequest
	if !decodeLegacyJSON(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.TargetOpenID)
	if ref == "" && req.TargetID > 0 {
		ref = strconv.FormatInt(req.TargetID, 10)
	}
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少目标用户")
		return
	}
	viewer, ok := a.legacySocialViewerFor(w, r, user)
	if !ok {
		return
	}
	if !a.legacySocialGate(w, viewer, true) {
		return
	}
	if !a.legacyRateLimit(w, "praise", "praise", user.ID) {
		return
	}
	target, err := a.resolveLegacyTarget(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise target failed")
		return
	}
	result, err := a.db.CreatePraise(r.Context(), viewer.ID, target.ID, a.clock.Now())
	if err != nil {
		writeLegacyPraiseError(a, w, r, err)
		return
	}
	state, err := a.legacyPraiseStateMap(r.Context(), result.State)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise state failed")
		return
	}
	state["ok"] = true
	state["already"] = result.Already
	state["message"] = "点赞成功"
	if result.Already {
		state["message"] = "已经点过赞了"
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *App) handleLegacyPraiseCancel(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	ref := strings.TrimSpace(r.PathValue("openid"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少目标用户")
		return
	}
	viewer, ok := a.legacySocialViewerFor(w, r, user)
	if !ok {
		return
	}
	if !a.legacySocialGate(w, viewer, true) {
		return
	}
	if !a.legacyRateLimit(w, "praise", "praise", user.ID) {
		return
	}
	target, err := a.resolveLegacyTarget(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise cancel target failed")
		return
	}
	praiseState, err := a.db.CancelPraise(r.Context(), viewer.ID, target.ID, a.clock.Now())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise cancel failed")
		return
	}
	state, err := a.legacyPraiseStateMap(r.Context(), praiseState)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise state failed")
		return
	}
	state["ok"] = true
	state["already"] = false
	state["message"] = "已取消点赞"
	writeJSON(w, http.StatusOK, state)
}

func (a *App) handleLegacyMessageCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacySocialMessageRequest
	if !decodeLegacyJSON(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(firstNonEmpty(req.TargetOpenID, req.Target))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少目标用户")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		legacyError(w, http.StatusBadRequest, "内容不能为空")
		return
	}
	if utf8.RuneCountInString(content) > db.MessageMaxRunes {
		legacyError(w, http.StatusBadRequest, "留言最多500字")
		return
	}
	parentID, err := parseOptionalIDJSON(req.ParentID)
	if err != nil {
		legacyError(w, http.StatusBadRequest, "回复的留言格式不正确")
		return
	}
	replyToID, err := parseOptionalIDJSON(req.ReplyToID)
	if err != nil {
		legacyError(w, http.StatusBadRequest, "回复的留言格式不正确")
		return
	}
	if !a.legacyMessagePostGate(w, r, user) {
		return
	}
	if !a.legacyRateLimit(w, "message", "message:create", user.ID) {
		return
	}
	target, err := a.db.FindUserByRef(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusBadRequest, "目标用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message target failed")
		return
	}
	msg, err := a.db.CreateMessage(r.Context(), db.CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     user.ID,
		Content:      content,
		ParentID:     parentID,
		ReplyToID:    replyToID,
	})
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusBadRequest, "目标用户不存在或回复的留言已删除")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message create failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": msg.ID})
}

func (a *App) handleLegacyMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("target"))
	if ref == "" {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []legacyThreadMessage{}})
		return
	}
	target, err := a.db.FindUserByRef(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusBadRequest, "目标用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message target failed")
		return
	}
	// v1 exposed card threads to every signed-in member; keep that contract
	// here instead of applying the newer participant-only rule.
	admin := a.isModerator(r.Context(), user)
	msgs, err := a.db.ListMessages(r.Context(), target.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message list failed")
		return
	}
	avatars, err := a.legacyMessageAvatars(r.Context(), msgs)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message avatars failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": legacyThreadMessages(msgs, user.ID, admin, avatars)})
}

func (a *App) handleLegacyMessagesMine(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	msgs, err := a.db.ListMessagesByAuthor(r.Context(), user.ID, 200)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy my messages failed")
		return
	}
	avatars, err := a.legacyMessageTargetAvatars(r.Context(), msgs)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message avatars failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": legacySentMessages(msgs, avatars)})
}

func (a *App) handleLegacyMessagesReceived(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	msgs, err := a.db.ListMessagesReceived(r.Context(), user.ID, 200)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy received messages failed")
		return
	}
	avatars, err := a.legacyMessageAvatars(r.Context(), msgs)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message avatars failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": legacyReceivedMessages(msgs, user.ID, avatars)})
}

func (a *App) handleLegacyMessagesReported(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.isModerator(r.Context(), user) {
		legacyError(w, http.StatusForbidden, "需要内容管理权限")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	reports, err := a.db.ListReports(r.Context(), status, 200)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy reported messages failed")
		return
	}
	profiles, err := a.legacyReportProfiles(r.Context(), reports)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy report profiles failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": legacyReportedMessages(reports, profiles)})
}

func (a *App) handleLegacyMessageDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "留言编号不正确")
		return
	}
	msg, err := a.db.GetMessageByID(r.Context(), messageID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && msg.Status == db.MessageStatusDeleted) {
		legacyError(w, http.StatusNotFound, "留言不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy message load failed")
		return
	}
	admin := a.isModerator(r.Context(), user)
	if msg.AuthorID != user.ID && msg.TargetUserID != user.ID && !admin {
		legacyError(w, http.StatusForbidden, "无权删除")
		return
	}
	if _, err := a.db.DeleteMessage(r.Context(), messageID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			legacyError(w, http.StatusNotFound, "留言不存在")
			return
		}
		legacyServerError(a, w, r, err, "legacy message delete failed")
		return
	}
	a.audit(r, &user.ID, "message.delete", "message", strconv.FormatInt(messageID, 10), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleLegacyMessageReport(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "留言编号不正确")
		return
	}
	if !a.legacyRateLimit(w, "report", "message:report", user.ID) {
		return
	}
	var req legacySocialReportRequest
	if !decodeOptionalLegacyJSON(w, r, &req) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(reason) > db.MessageMaxRunes {
		legacyError(w, http.StatusBadRequest, "举报原因最多500字")
		return
	}
	msg, err := a.db.GetMessageByID(r.Context(), messageID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && msg.Status == db.MessageStatusDeleted) {
		legacyError(w, http.StatusNotFound, "留言不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy reported message load failed")
		return
	}
	reportID, already, err := a.db.CreateMessageReport(r.Context(), db.ReportMessageParams{
		MessageID:  messageID,
		ReporterID: user.ID,
		Reason:     reason,
	})
	switch {
	case errors.Is(err, db.ErrMessageDeleted):
		legacyError(w, http.StatusBadRequest, "该留言已删除")
		return
	case errors.Is(err, db.ErrCannotReportSelf):
		legacyError(w, http.StatusBadRequest, "不能举报自己的留言")
		return
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "留言不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy message report failed")
		return
	}
	if !already {
		a.audit(r, &user.ID, "message.report", "message", strconv.FormatInt(messageID, 10),
			map[string]any{"report_id": reportID})
		a.notifyMessageReport(r.Context(), user, msg, reason, reportID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "already": already})
}

func (a *App) handleLegacyNotificationsList(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	rows, err := a.db.ListNotifications(r.Context(), user.ID, 100)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy notifications failed")
		return
	}
	unread, err := a.db.UnreadNotificationCount(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy notifications unread failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": legacyNotifications(rows),
		"unread":        unread,
	})
}

func (a *App) handleLegacyNotificationRead(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	id, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "通知编号不正确")
		return
	}
	found, err := a.db.MarkNotificationRead(r.Context(), user.ID, id)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy notification read failed")
		return
	}
	if !found {
		legacyError(w, http.StatusNotFound, "通知不存在")
		return
	}
	unread, err := a.db.UnreadNotificationCount(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy notifications unread failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unread": unread})
}

func (a *App) handleLegacyNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if !a.legacyMessageViewGate(w, r, user) {
		return
	}
	updated, err := a.db.MarkAllNotificationsRead(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy notifications read all failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": updated, "unread": 0})
}
