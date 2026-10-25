package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

func init() { registerModule(registerLegacyAPI) }

// registerLegacyAPI mounts the v1-compatible JSON endpoints consumed by the
// bundled Vue SPA. Paths are namespaced under /legacy so the Go API stays free
// to evolve.
func registerLegacyAPI(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /legacy/api/home", a.handleLegacyHome)
	mux.HandleFunc("GET /legacy/api/cards", a.handleLegacyCards)
	mux.HandleFunc("GET /legacy/api/profile", a.handleLegacyProfile)
	mux.HandleFunc("GET /legacy/api/user/me", a.handleLegacyUserMe)
	mux.HandleFunc("GET /legacy/api/likes/me", a.handleLegacyLikesMe)
	mux.HandleFunc("GET /legacy/api/likes/mine", a.handleLegacyMyLikes)
	mux.HandleFunc("GET /legacy/api/activities", a.handleLegacyActivities)
	mux.HandleFunc("GET /legacy/api/activities/{id}", a.handleLegacyActivityDetail)
	mux.HandleFunc("GET /legacy/api/points/me", a.handleLegacyPointsMe)
	mux.HandleFunc("GET /legacy/api/praise/me", a.handleLegacyPraiseMe)
	mux.HandleFunc("POST /legacy/api/client-events", a.handleLegacyClientEvents)
	mux.HandleFunc("POST /legacy/api/password", a.handleLegacyPasswordChange)
	mux.HandleFunc("GET /legacy/api/version", a.handleLegacyVersion)
}

// legacyError keeps the v1 error envelope: {"error": "..."}.
func legacyError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// legacyActor returns the signed-in user or writes the v1 401 body.
func (a *App) legacyActor(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	u, ok := currentUser(r)
	if !ok {
		legacyError(w, http.StatusUnauthorized, "未登录")
		return db.User{}, false
	}
	return u, true
}

// legacyWriteCSRF guards POST/DELETE/PUT endpoints. The v1 api() helper sends
// the yxq_csrf cookie value in X-CSRF-Token.
func (a *App) legacyWriteCSRF(w http.ResponseWriter, r *http.Request) bool {
	if !a.headerCSRFValid(r) {
		legacyError(w, http.StatusForbidden, "表单已过期，请重新整理后再试")
		return false
	}
	return true
}

func legacyServerError(a *App, w http.ResponseWriter, r *http.Request, err error, msg string) {
	a.logger(r).Error(msg, "error", err)
	legacyError(w, http.StatusInternalServerError, "系统暂时无法处理，请稍后再试")
}

func (a *App) handleLegacyVersion(w http.ResponseWriter, r *http.Request) {
	// 返回本次构建的版本号；与 SPA 壳内注入的 APP_VERSION 一致，
	// 新版本部署后旧标签页会比对出差异，由前端提示用户手动更新。
	writeJSON(w, http.StatusOK, map[string]string{"version": platform.Version})
}

var errLegacyHomeAccountUnavailable = errors.New("legacy home: account unavailable")

// buildLegacyHomePayload builds the same aggregate as GET /legacy/api/home.
// serveLegacyApp reuses it for window.__YXQ_BOOTSTRAP__; the handler below is a
// thin HTTP wrapper so both paths cannot drift.
func (a *App) buildLegacyHomePayload(ctx context.Context, r *http.Request, user db.User, now time.Time) (map[string]any, error) {
	profile, err := a.db.GetProfile(ctx, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		profile = db.Profile{
			ID: user.ID, Name: user.Name, Phone: user.Phone,
			AccountKind: "user", AccountStatus: "active",
		}
	} else if err != nil {
		return nil, err
	}
	bundle, err := a.db.LoadViewerBundle(ctx, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, errLegacyHomeAccountUnavailable
	}
	if err != nil {
		return nil, err
	}
	viewer := bundle.Viewer
	// 额度与点赞额度是同一次只读快照，合并成一条 SQL；失败时保持批次1 的
	// 「不把错误额度下发」语义：留空 deps，由 buildCardsPageData 重试。
	quota, praise, countersErr := a.db.LoadInteractionCounters(ctx, user.ID, now)
	if countersErr != nil {
		a.logger(r).Warn("legacy home counters failed", "error", countersErr, "user_id", user.ID)
	}
	viewRole := a.legacyViewRole(r)
	if !viewer.IDCardValid {
		return map[string]any{
			"user":              legacyUserMap(user, profile, viewer, quota, bundle.IsAdmin, bundle.ObserverEnabled, viewRole, bundle.HasFace),
			"cards":             []any{},
			"cards_total":       0,
			"likes":             map[string]any{"liked_me": []any{}, "mutual": []any{}},
			"activities":        []any{},
			"register_form_url": "",
			"promo_activity_id": "",
		}, nil
	}
	pageViewer := viewer
	if viewRole == "observer" {
		pageViewer.AccountKind = "observer"
		pageViewer.AccountStatus = "observer"
	}
	deps := cardPageDeps{includeSelf: true}
	if countersErr == nil {
		deps.quota = &quota
		deps.praise = &praise
	}
	page, err := a.buildCardsPageData(ctx, pageViewer, r.URL.Query(), now, 20, deps)
	if errors.Is(err, errGenderMissing) {
		// 性别未填：牌堆为空，但首页仍要显示自己的卡（旧实现的行为：
		// self 卡 1 张 + cards_total 1）。批次7 合并查询时把 self 一起丢了，
		// reviewer N2 指出这是无意的行为变化，这里显式补回。
		page = CardsPageData{}
		if selfProfile, selfErr := a.db.GetSocialProfile(ctx, user.ID); selfErr == nil {
			page.Self = &selfProfile
		} else if !errors.Is(selfErr, db.ErrNotFound) {
			a.logger(r).Warn("legacy home self card failed", "error", selfErr, "user_id", user.ID)
		}
	} else if err != nil {
		return nil, err
	}
	views := page.Cards
	if page.Self != nil {
		selfView := buildCardView(*page.Self, false, false, 0, now)
		selfView.IsSelf = true
		views = append([]CardView{selfView}, views...)
		if page.Limit > 0 && len(views) > page.Limit {
			views = views[:page.Limit]
		}
		page.Total++
	} else if countersErr == nil {
		a.logger(r).Warn("legacy home self card missing", "user_id", user.ID)
	}
	cards := a.legacyCards(ctx, views)
	likes, err := a.legacyLikes(ctx, user.ID, now)
	if err != nil {
		return nil, err
	}
	activities, err := a.legacyActivities(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"user":              legacyUserMap(user, profile, viewer, quota, bundle.IsAdmin, bundle.ObserverEnabled, viewRole, bundle.HasFace),
		"cards":             cards,
		"cards_total":       page.Total,
		"likes":             likes,
		"activities":        activities,
		"register_form_url": "",
		"promo_activity_id": "",
	}, nil
}

func (a *App) handleLegacyHome(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if body, hit := a.readCacheHit(r, a.homeCache, "home"); hit {
		writeCachedJSON(w, body)
		return
	}
	now := a.clock.Now()
	key := legacyInflightKey(user.ID, a.legacyViewRole(r), r.URL.RawQuery)
	value, err := a.homeInflight.Do(r.Context(), key, func() (any, error) {
		return a.buildLegacyHomePayload(r.Context(), r, user, now)
	})
	payload, _ := value.(map[string]any)
	switch {
	case errors.Is(err, errLegacyHomeAccountUnavailable):
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
	case err != nil:
		legacyServerError(a, w, r, err, "legacy home failed")
	default:
		a.writeReadCache(w, r, a.homeCache, "home", payload)
	}
}

func (a *App) handleLegacyCards(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards viewer failed")
		return
	}
	limit := socialNonNegative(r.URL.Query().Get("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	if a.legacyViewRole(r) == "observer" {
		viewer.AccountKind = "observer"
		viewer.AccountStatus = "observer"
	}
	if body, hit := a.readCacheHit(r, a.cardsCache, "cards"); hit {
		writeCachedJSON(w, body)
		return
	}
	key := legacyInflightKey(user.ID, a.legacyViewRole(r), r.URL.RawQuery)
	value, err := a.cardsInflight.Do(r.Context(), key, func() (any, error) {
		return a.buildCardsPageData(r.Context(), viewer, r.URL.Query(), a.clock.Now(), limit, cardPageDeps{})
	})
	page, _ := value.(CardsPageData)
	if errors.Is(err, errGenderMissing) {
		legacyError(w, http.StatusBadRequest, "你的性别资料尚未完善，请先到「我的资料」填写")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards failed")
		return
	}
	a.writeReadCache(w, r, a.cardsCache, "cards", map[string]any{
		"cards":       a.legacyCards(r.Context(), page.Cards),
		"total":       page.Total,
		"cards_total": page.Total,
		"offset":      page.Offset,
		"limit":       page.Limit,
		"version":     page.Version,
	})
}

func (a *App) handleLegacyProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy profile failed")
		return
	}
	quota, err := a.db.LikeQuotaFor(r.Context(), user.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Warn("legacy profile quota failed", "error", err, "user_id", user.ID)
	}
	faces, err := a.db.FaceCentersForProfile(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("legacy profile face lookup failed", "error", err, "user_id", user.ID)
	}
	// has_face 只来自数据库检测结果与账号本身的 observer 状态，
	// yxq_view_role 只是 UI 视角，不能用来绕过人脸门禁。
	hasFace := a.legacyProfileHasFace(r.Context(), user.ID)
	payload := legacyProfileMap(profile, quota, hasFace, faces)
	payload["editable"] = legacyProfileEditable(profile)
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleLegacyUserMe(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		legacyServerError(a, w, r, err, "legacy user me profile failed")
		return
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		legacyServerError(a, w, r, err, "legacy user me viewer failed")
		return
	}
	quota, err := a.db.LikeQuotaFor(r.Context(), user.ID, a.clock.Now())
	if err != nil {
		a.logger(r).Warn("legacy user me quota failed", "error", err, "user_id", user.ID)
	}
	writeJSON(w, http.StatusOK, legacyUserMap(user, profile, viewer, quota, a.legacyIsAdmin(r.Context(), user.ID), a.legacyObserverEnabled(r.Context(), user.ID), a.legacyViewRole(r), a.legacyProfileHasFace(r.Context(), user.ID)))
}

func (a *App) handleLegacyLikesMe(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	likes, err := a.legacyLikes(r.Context(), user.ID, a.clock.Now())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy likes/me failed")
		return
	}
	writeJSON(w, http.StatusOK, likes)
}

func (a *App) handleLegacyMyLikes(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	now := a.clock.Now()
	overview, err := a.db.SocialOverview(r.Context(), user.ID, now)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy likes/mine failed")
		return
	}
	list := make([]map[string]any, 0, len(overview.MyLikes))
	for _, pair := range overview.MyLikes {
		item := legacyBriefMap(pair.Other)
		item["mutual"] = pair.Mutual
		item["active"] = pair.Active
		item["like_record_id"] = pair.Like.ID
		item["like_type"] = pair.Like.LikeType
		item["message"] = pair.Like.Message
		item["status"] = socialLikeStatusLabel(pair)
		list = append(list, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"likes": list})
}

func (a *App) handleLegacyActivities(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	activities, err := a.legacyActivities(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activities failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": activities})
}

// handleLegacyClientEvents accepts the SPA's diagnostic batches. The payload
// is intentionally discarded; it exists so the v1 client does not log 404s.
func (a *App) handleLegacyClientEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.legacyActor(w, r); !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_, _ = io.Copy(io.Discard, r.Body)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type legacyPasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

// handleLegacyPasswordChange 供内嵌的 v1 SPA 修改密码；核心校验与 SSR 共用
// applyPasswordChange，成功时保持当前会话并返回 JSON。
func (a *App) handleLegacyPasswordChange(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacyPasswordChangeRequest
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	mustChange, _, err := a.db.GetCredentialState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusConflict, "尚未设定密码，请使用启用链接。")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy password credential state failed")
		return
	}
	failure := a.applyPasswordChange(w, r, user, mustChange, req.CurrentPassword, req.Password, req.PasswordConfirm)
	if failure != nil {
		if failure.Status != 0 {
			legacyError(w, failure.Status, failure.Message)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleLegacyPointsMe(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	a.legacyWritePointsMe(w, r, user)
}

func (a *App) handleLegacyPraiseMe(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	state, err := a.db.PraiseStateFor(r.Context(), user.ID, a.clock.Now())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy praise/me failed")
		return
	}
	praised := make([]string, 0, len(state.Praised))
	if len(state.Praised) > 0 {
		if profiles, err := a.db.GetSocialProfilesByIDs(r.Context(), state.Praised); err == nil {
			for _, id := range state.Praised {
				if p, ok := profiles[id]; ok {
					praised = append(praised, socialCardOpenID(p))
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit":         state.Limit,
		"left":          state.Left,
		"used":          state.Used,
		"praised":       praised,
		"received_week": state.ReceivedWeek,
	})
}

func (a *App) handleLegacyNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.legacyActor(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": []any{}, "unread": 0})
}

// handleLegacyEmptyMessages keeps the v1 likes tab free of 404s until the
// message compatibility layer lands. Counts stay at zero rather than failing.
func (a *App) handleLegacyEmptyMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.legacyActor(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": []any{}})
}

func (a *App) handleLegacyGroupFlag(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	mine, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group flag failed")
		return
	}
	has := false
	for _, item := range mine {
		if item.GroupEnabled {
			has = true
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"has_group_activity": has})
}

func (a *App) handleLegacyActivityDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	activity, err := a.db.GetActivity(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity detail failed")
		return
	}
	view := buildActivityView(activity)
	if signup, err := a.db.GetSignup(r.Context(), activity.ID, user.ID); err == nil {
		applySignupToView(&view, signup.Status, signup.Attendance)
	} else if !errors.Is(err, db.ErrNotSignedUp) {
		legacyServerError(a, w, r, err, "legacy activity signup failed")
		return
	}
	out := legacyActivityMap(view)
	if rows, err := a.db.ListActivitySignups(r.Context(), activity.ID); err == nil {
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.Nickname)
		}
		out["signup_users"] = names
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) legacyLikes(ctx context.Context, userID int64, now time.Time) (map[string]any, error) {
	// 只读：从 mutual 状态读取展示数据；微信号交换通知由写入路径与后台回填补发，
	// 不再在 GET 里创建通知（旧实现每次 home 都可能开事务写库）。
	overview, err := a.db.SocialOverview(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	mutual := make([]map[string]any, 0)
	for _, pair := range uniqueMutualPairs(overview) {
		status, required, waiting := mutualWechatStatus(overview.Me, pair.Other)
		myWechat := strings.TrimSpace(overview.Me.Wechat)
		theirWechat := ""
		if status == wechatStatusReady {
			theirWechat = pair.Other.Wechat
		}
		mutual = append(mutual, map[string]any{
			"nickname":         pair.Other.DisplayName(),
			"message":          mutualMessage(overview, pair),
			"status":           socialLikeStatusLabel(pair),
			"mutual":           true,
			"real":             pair.Like.LikeType == db.LikeTypeReal,
			"initiator_openid": socialCardOpenID(pair.Other),
			"my_wechat":        myWechat,
			"their_wechat":     theirWechat,
			"wechat_required":  required,
			"wechat_status":    status,
			"waiting_other":    waiting,
		})
	}
	// Sending is idempotent but belongs to write paths and the background
	// backfill; GET must stay read-only.
	likedMe := make([]map[string]any, 0)
	for _, view := range likedMeViews(overview) {
		likedMe = append(likedMe, map[string]any{
			"nickname":         view.Nickname,
			"message":          "",
			"status":           view.Status,
			"mutual":           false,
			"real":             view.Real,
			"initiator_openid": view.OpenID,
		})
	}
	return map[string]any{"liked_me": likedMe, "mutual": mutual}, nil
}

func (a *App) legacyActivities(ctx context.Context, userID int64) ([]map[string]any, error) {
	// 活动列表与我的报名用一条 LEFT JOIN 查询返回，语义与原两次查询一致。
	items, err := a.db.ListActivitiesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		view := buildActivityView(item.Activity)
		if item.SignupStatus != "" {
			applySignupToView(&view, item.SignupStatus, item.Attendance)
		}
		out = append(out, legacyActivityMap(view))
	}
	return out, nil
}

// legacyCards 批量取卡片人脸中心，随卡片一起返回给 v1 SPA 做照片对准。
// 查询失败时降级为空 faces（前端居中显示），不影响卡片本身。
func (a *App) legacyCards(ctx context.Context, items []CardView) []map[string]any {
	ids := make([]int64, 0, len(items))
	for _, card := range items {
		if card.ID > 0 {
			ids = append(ids, card.ID)
		}
	}
	facesByUser, err := a.db.FaceCentersForProfiles(ctx, ids)
	if err != nil {
		a.log.Warn("legacy cards face lookup failed", "error", err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, card := range items {
		out = append(out, legacyCardMap(card, facesByUser[card.ID]))
	}
	return out
}

func legacyHeight(value int) any {
	if value <= 0 {
		return ""
	}
	return value
}

func legacyCardMap(card CardView, faces []db.ProfilePhotoFace) map[string]any {
	return map[string]any{
		"id":                card.ID,
		"openid":            card.OpenID,
		"user_id":           card.UserID,
		"nickname":          card.Nickname,
		"gender":            card.Gender,
		"birthday":          card.Birthday,
		"age":               card.Age,
		"height":            legacyHeight(card.Height),
		"education":         card.Education,
		"baptismal_name":    card.BaptismalName,
		"church":            card.Church,
		"group":             card.ChurchGroup,
		"church_location":   card.ChurchLocation,
		"city":              card.City,
		"native_place":      card.NativePlace,
		"industry":          card.Industry,
		"position":          card.Position,
		"income":            card.Income,
		"house":             card.House,
		"driving":           card.Driving,
		"marriage":          card.Marriage,
		"family":            card.Family,
		"live_with_parents": card.LiveWithParents,
		"personality":       card.Personality,
		"self_traits":       card.SelfTraits,
		"hobbies":           card.SelfHobbies,
		"self_hobbies":      card.SelfHobbies,
		"self_sports":       card.SelfSports,
		"mbti":              card.MBTI,
		"partner_criteria":  card.PartnerCriteria,
		"partner_traits":    card.PartnerTraits,
		"partner_hobbies":   card.PartnerHobbies,
		"partner_sports":    card.PartnerSports,
		"subtitle":          card.Subtitle,
		"display_fields":    card.DisplayFields,
		"photo":             card.Photo,
		"photos":            card.PhotoURLs,
		"photos_thumb":      card.PhotoThumbs,
		"liked":             card.Liked,
		"praised":           card.Praised,
		"msg_count":         card.MsgCount,
		"recommended":       card.Recommended,
		"is_self":           card.IsSelf,
		"record_id":         strconv.FormatInt(card.ID, 10),
		"faces":             legacyCardFaces(card, faces),
	}
}

// legacyCardFaces 按 photos 顺序返回每张照片的人脸数据，格式与 v1 一致：
// {c:[x,y],box:[w,h]} 或 null（未检测/未检出）。
func legacyCardFaces(card CardView, faces []db.ProfilePhotoFace) []any {
	byMediaID := make(map[string]db.ProfilePhotoFace, len(faces))
	for _, item := range faces {
		byMediaID[item.MediaID] = item
	}
	out := make([]any, 0, len(card.Photos))
	for _, photo := range card.Photos {
		out = append(out, legacyFaceItem(byMediaID[photo.ID]))
	}
	return out
}

func legacyFaceItem(item db.ProfilePhotoFace) any {
	if !item.Checked || item.CenterX == nil || item.CenterY == nil || item.BoxW == nil || item.BoxH == nil {
		return nil
	}
	// v1 的 faces 还带 tb/lr（黑边比例）。黑边裁切尚未移植，这里返回
	// [0,0] 表示无黑边，前端会按原图构图；不要声称与 v1 的黑边裁切一致。
	return map[string]any{
		"c":   []float64{*item.CenterX, *item.CenterY},
		"box": []float64{*item.BoxW, *item.BoxH},
		"tb":  []float64{0, 0},
		"lr":  []float64{0, 0},
	}
}

func legacyBriefMap(p db.SocialProfile) map[string]any {
	photo := ""
	if len(p.Photos) > 0 {
		photo = socialPhotoView(p.Photos[0]).ThumbURL
	}
	return map[string]any{
		"openid":   socialCardOpenID(p),
		"user_id":  p.LegacyID,
		"nickname": p.DisplayName(),
		"gender":   p.Gender,
		"church":   p.Church,
		"photo":    photo,
		"hearts":   0,
	}
}

func legacyUserMap(user db.User, profile db.Profile, viewer db.SocialViewer, quota db.LikeQuota, isAdmin, observerEnabled bool, viewRole string, hasFace bool) map[string]any {
	name := profile.Nickname
	if strings.TrimSpace(name) == "" {
		name = viewer.Nickname
	}
	if strings.TrimSpace(name) == "" {
		name = user.Name
	}
	accountKind := profile.AccountKind
	accountStatus := profile.AccountStatus
	if accountKind == "" {
		accountKind = viewer.AccountKind
	}
	if accountStatus == "" {
		accountStatus = viewer.AccountStatus
	}
	// 账号本身是 observer 时沿用 v1 的放行语义（是否继续豁免待 PM 裁定）；
	// viewRole 只是观察员视角，不得影响 has_face。
	accountObserver := accountKind == "observer" || accountStatus == "observer"
	observer := accountObserver || viewRole == "observer"
	if observer {
		accountStatus = "observer"
	}
	if accountObserver {
		hasFace = true
	}
	photo := ""
	if len(profile.Photos) > 0 {
		photo = "/profile/photo/" + profile.Photos[0].MediaID + "?size=thumb"
	}
	// is_admin 与 can_admin 同义：v1 SPA 用它决定举报管理等入口，
	// 语义是「有任一管理角色」；具体操作仍由服务端权限码校验。
	out := map[string]any{
		"user_id":         legacyProfileID(profile, user),
		"nickname":        name,
		"gender":          firstNonEmpty(profile.Gender, viewer.Gender),
		"height":          legacyHeight(profileHeight(profile)),
		"education":       profile.Education,
		"hobbies":         strings.Join(profile.SelfHobbies, "、"),
		"hearts":          quota.AnonLeft,
		"hearts_total":    quota.AnonTotal,
		"anon_left":       quota.AnonLeft,
		"anon_total":      quota.AnonTotal,
		"real_left":       quota.RealLeft,
		"real_total":      quota.RealTotal,
		"real_permanent":  quota.RealPermanent,
		"account_status":  legacyAccountStatus(accountStatus),
		"is_observer":     observer,
		"photo":           photo,
		"record_id":       legacyProfileID(profile, user),
		"is_admin":        isAdmin,
		"can_admin":       isAdmin,
		"available_roles": legacyRoles(observerEnabled, observer),
		"has_face":        hasFace,
		"id_valid":        profile.IDCardValid,
		"wechat":          profile.Wechat,
		"phone":           firstNonEmpty(profile.Phone, user.Phone),
	}
	return out
}

func (a *App) legacyObserverEnabled(ctx context.Context, userID int64) bool {
	enabled, err := a.db.ObserverEnabled(ctx, userID)
	if err != nil {
		a.log.Warn("legacy observer flag lookup failed", "error", err, "user_id", userID)
		return false
	}
	return enabled
}

func (a *App) legacyViewRole(r *http.Request) string {
	if c, err := r.Cookie("yxq_view_role"); err == nil && c.Value == "observer" {
		return "observer"
	}
	return "user"
}

func legacyRoles(observerEnabled, observerView bool) []string {
	roles := []string{"user"}
	if observerEnabled {
		roles = append(roles, "observer")
	}
	return roles
}

func (a *App) legacyIsAdmin(ctx context.Context, userID int64) bool {
	roles, err := a.db.UserAdminRoles(ctx, userID)
	if err != nil {
		a.log.Warn("legacy admin role lookup failed", "error", err, "user_id", userID)
		return false
	}
	return len(roles) > 0
}

func legacyProfileMap(p db.Profile, quota db.LikeQuota, hasFace bool, faces []db.ProfilePhotoFace) map[string]any {
	photos := make([]string, 0, len(p.Photos))
	photoThumbs := make([]string, 0, len(p.Photos))
	for _, photo := range p.Photos {
		base := "/profile/photo/" + photo.MediaID
		photos = append(photos, base)
		photoThumbs = append(photoThumbs, base+"?size=thumb")
	}
	photo := ""
	if len(photos) > 0 {
		photo = photos[0]
	}
	// 观察员沿用 v1 的 has_face 放行；id_valid 不做 observer 特殊处理。
	// PM 2026-10-22 裁定身份证该拦者拦，不 grandfather。
	observer := p.AccountKind == "observer" || p.AccountStatus == "observer"
	if observer {
		hasFace = true
	}
	registerTime := ""
	if registered := profileRegisteredAt(p); registered != nil {
		registerTime = profileTimeValue(*registered)
	}
	mask := ""
	if p.IDCardLast4 != "" {
		mask = "********" + p.IDCardLast4
	}
	return map[string]any{
		"user_id":           legacyProfileID(p, db.User{}),
		"nickname":          p.Nickname,
		"gender":            p.Gender,
		"height":            legacyHeight(profileHeight(p)),
		"education":         p.Education,
		"hobbies":           strings.Join(p.SelfHobbies, "、"),
		"hearts":            quota.AnonLeft,
		"hearts_total":      quota.AnonTotal,
		"anon_left":         quota.AnonLeft,
		"anon_total":        quota.AnonTotal,
		"real_left":         quota.RealLeft,
		"real_total":        quota.RealTotal,
		"real_permanent":    quota.RealPermanent,
		"account_status":    legacyAccountStatus(p.AccountStatus),
		"is_observer":       observer,
		"photo":             photo,
		"record_id":         legacyProfileID(p, db.User{}),
		"baptismal_name":    p.BaptismalName,
		"church":            p.Church,
		"group":             p.ChurchGroup,
		"church_location":   p.ChurchLocation,
		"city":              p.City,
		"native_place":      p.NativePlace,
		"industry":          p.Industry,
		"position":          p.Position,
		"income":            p.Income,
		"personality":       p.Personality,
		"self_traits":       strings.Join(p.SelfTraits, "、"),
		"self_hobbies":      strings.Join(p.SelfHobbies, "、"),
		"self_sports":       strings.Join(p.SelfSports, "、"),
		"mbti":              strings.Join(p.MBTI, "、"),
		"partner_criteria":  p.PartnerCriteria,
		"partner_traits":    strings.Join(p.PartnerTraits, "、"),
		"partner_hobbies":   strings.Join(p.PartnerHobbies, "、"),
		"partner_sports":    strings.Join(p.PartnerSports, "、"),
		"marriage":          p.Marriage,
		"house":             profileSelectWithNote(p.House, p.HouseNoteHave, p.HouseNoteNone),
		"driving":           profileSelectWithNote(p.Driving, p.DrivingNoteHave, p.DrivingNoteNone),
		"family":            p.Family,
		"live_with_parents": p.LiveWithParents,
		"birthday":          profileBirthdayShort(p.Birthday),
		"photos":            photos,
		"photos_thumb":      photoThumbs,
		"faces":             legacyProfileFaces(p.Photos, faces),
		"real_name":         p.Name,
		"wechat":            p.Wechat,
		"phone":             p.Phone,
		"register_time":     registerTime,
		"id_valid":          p.IDCardValid,
		"id_card_mask":      mask,
		"has_face":          hasFace,
		"editable":          []any{},
	}
}

// legacyProfileFaces 与 photos 顺序一一对应；未检测/未检出为 null。
func legacyProfileFaces(photos []db.ProfilePhoto, faces []db.ProfilePhotoFace) []any {
	byMediaID := make(map[string]db.ProfilePhotoFace, len(faces))
	for _, item := range faces {
		byMediaID[item.MediaID] = item
	}
	out := make([]any, 0, len(photos))
	for _, photo := range photos {
		out = append(out, legacyFaceItem(byMediaID[photo.MediaID]))
	}
	return out
}

// legacyProfileHasFace 读取真实检测结果；查询失败按 false 处理，绝不假 true。
func (a *App) legacyProfileHasFace(ctx context.Context, userID int64) bool {
	has, err := a.db.ProfileHasFace(ctx, userID)
	if err != nil {
		a.log.Warn("legacy has_face lookup failed", "error", err, "user_id", userID)
		return false
	}
	return has
}

func profileHeight(p db.Profile) int {
	if p.HeightCM == nil {
		return 0
	}
	return *p.HeightCM
}

func legacyProfileID(p db.Profile, user db.User) string {
	if p.LegacyUserID != "" {
		return p.LegacyUserID
	}
	if user.LegacyUserID != nil && *user.LegacyUserID != "" {
		return *user.LegacyUserID
	}
	id := p.ID
	if id == 0 {
		id = user.ID
	}
	return strconv.FormatInt(id, 10)
}

func legacyAccountStatus(status string) string {
	switch status {
	case "active":
		return "单身"
	case "matched":
		return "已脱单"
	case "pending":
		return "待审核"
	case "rejected":
		return "审核不通过"
	case "left":
		return "已退出"
	case "disabled":
		return "已停用"
	case "observer":
		return "观察员"
	default:
		return status
	}
}

func legacyActivityMap(view ActivityView) map[string]any {
	return map[string]any{
		"activity_id":      view.ActivityNo,
		"record_id":        view.RecordID,
		"name":             view.Name,
		"description":      view.Description,
		"location":         view.Location,
		"condition":        view.Condition,
		"max_signup":       view.MaxSignup,
		"current_signup":   view.CurrentSignup,
		"fee":              legacyActivityFee(view.Fee),
		"food":             view.Food,
		"status":           view.Status,
		"poster":           view.PosterURL,
		"group_status":     view.GroupStatus,
		"onetoone_status":  view.OnetooneStatus,
		"group_flag":       legacyYesNo(view.GroupEnabled),
		"onetoone_flag":    legacyYesNo(view.OnetooneEnabled),
		"male_per_group":   view.MalePerGroup,
		"female_per_group": view.FemalePerGroup,
		"start_time":       view.StartAtText,
		"end_time":         view.EndAtText,
		"signin_enabled":   view.SigninEnabled,
		"my_signup":        view.MySignup,
		"signup_status":    view.SignupStatus,
		"my_attendance":    view.Attendance,
		"can_signup":       view.CanSignup,
		"can_cancel":       view.CanCancel,
		"my_choice_count":  0,
	}
}

func legacyYesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func legacyActivityFee(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	for _, suffix := range []string{"元", "¥", "￥"} {
		s = strings.TrimSuffix(s, suffix)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return int(f)
	}
	return 0
}

// writeReadCache 写 home/cards 的成功响应：先按 10s TTL 命中缓存直接回字节，
// 未命中则序列化、缓存并写出。错误/未授权路径不经过这里，因此不会被缓存。
// 响应头与 writeJSON 完全一致（no-store、Vary: Cookie 由中间件加），语义不变。
func (a *App) writeReadCache(w http.ResponseWriter, r *http.Request, cache *readCache,
	kind string, payload any) {
	if cache == nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	user, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	parts := readCacheParts(user.ID, kind, a.legacyViewRole(r), r.URL.RawQuery)
	if body, hit := cache.Get(parts); hit {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	body = append(body, '\n') // 与 json.Encoder 输出保持一致
	cache.Put(parts, body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// bumpReadCacheEpoch 在成功的非 GET /legacy/api/* 写请求后调用（由中间件统一做）：
// 全局 epoch++ 让所有用户的旧条目立即失效，避免逐用户失效遗漏。
func (a *App) bumpReadCacheEpoch() {
	if a.homeCache != nil {
		a.homeCache.BumpEpoch()
	}
	if a.cardsCache != nil {
		a.cardsCache.BumpEpoch()
	}
}

// readCacheHit 在构建前查缓存（键含用户/viewRole/query）。
func (a *App) readCacheHit(r *http.Request, cache *readCache, kind string) ([]byte, bool) {
	if cache == nil {
		return nil, false
	}
	user, ok := currentUser(r)
	if !ok {
		return nil, false
	}
	return cache.Get(readCacheParts(user.ID, kind, a.legacyViewRole(r), r.URL.RawQuery))
}

// writeCachedJSON 用与 writeJSON 相同的头回写缓存字节。
func writeCachedJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
