package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerLegacyActivities) }

// registerLegacyActivities mounts the activity endpoints the bundled v1 SPA
// still calls. The read-only /legacy/api/activities and /{id} routes live in
// legacy_api.go; this module fills in the signup, roster, grouping and
// one-to-one compatibility surface.
func registerLegacyActivities(a *App, mux *http.ServeMux) {
	// Literal paths are registered alongside the {id} route: ServeMux prefers
	// the more specific literal, and keeping them here keeps the module
	// self-contained.
	mux.HandleFunc("GET /legacy/api/activities/mine", a.handleLegacyMineActivities)
	mux.HandleFunc("GET /legacy/api/activities/mine/groups", a.handleLegacyMineGroups)
	mux.HandleFunc("GET /legacy/api/activities/mine/groups/flag", a.handleLegacyMineGroupsFlag)

	mux.HandleFunc("POST /legacy/api/activities/{id}/signup", a.handleLegacyActivitySignup)
	mux.HandleFunc("DELETE /legacy/api/activities/{id}/signup", a.handleLegacyActivityCancel)
	mux.HandleFunc("POST /legacy/api/activities/{id}/self-signin", a.handleLegacyActivitySelfSignin)
	mux.HandleFunc("GET /legacy/api/activities/{id}/signups", a.handleLegacyActivitySignups)

	registerLegacyMatching(a, mux)
}

// legacyAccountGateMessage mirrors the v1 browse/action gate copy. The new Go
// pages use activityGateMessage; the legacy JSON keeps the old wording so the
// bundled SPA toasts read the same.
func legacyAccountGateMessage(kind, status string) string {
	if kind == "observer" || status == "observer" {
		return "你是村情六处账号，无此操作权限"
	}
	switch status {
	case "matched":
		return "你当前处于已脱单状态（他人看不到你），请先在「我的」页恢复单身后再操作"
	case "pending":
		return "资料审核中，通过后即可使用一线牵App"
	case "rejected":
		return "很抱歉，你的资料未通过审核，如有疑问请联系管理员"
	case "left":
		return "你已暂时退出相亲市场，如需恢复请联系管理员"
	case "disabled":
		return "账号已停用，请联络管理员"
	default:
		return "账号目前无法使用，请联络管理员"
	}
}

// legacyActivityViewer applies the v1 browse gate and answers in the v1 error
// envelope. active, matched and observer accounts may read activities.
func (a *App) legacyActivityViewer(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return db.User{}, false
	}
	kind, status, err := a.db.GetAccountState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return db.User{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity account state failed")
		return db.User{}, false
	}
	switch status {
	case "active", "matched", "observer":
		return user, true
	}
	legacyError(w, http.StatusForbidden, legacyAccountGateMessage(kind, status))
	return db.User{}, false
}

// legacyActivityEditor applies the v1 action gate: a single, active account.
func (a *App) legacyActivityEditor(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return db.User{}, false
	}
	kind, status, err := a.db.GetAccountState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return db.User{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity account state failed")
		return db.User{}, false
	}
	if kind == "observer" || status != "active" {
		legacyError(w, http.StatusForbidden, legacyAccountGateMessage(kind, status))
		return db.User{}, false
	}
	return user, true
}

func (a *App) writeLegacyActivityError(w http.ResponseWriter, r *http.Request, err error, action string) {
	var notOpen *db.ActivityStatusError
	switch {
	case errors.Is(err, db.ErrActivityNotFound):
		legacyError(w, http.StatusNotFound, "活动不存在")
	case errors.As(err, &notOpen):
		legacyError(w, http.StatusConflict,
			fmt.Sprintf("活动当前状态为「%s」，无法%s", notOpen.Status, action))
	case errors.Is(err, db.ErrActivityFull):
		legacyError(w, http.StatusConflict, "报名人数已满")
	case errors.Is(err, db.ErrAlreadySignedUp):
		legacyError(w, http.StatusConflict, "你已经报名了这个活动")
	case errors.Is(err, db.ErrAccountNotActive):
		legacyError(w, http.StatusForbidden, "账号目前无法报名，请联络管理员")
	case errors.Is(err, db.ErrNotSignedUp):
		legacyError(w, http.StatusConflict, "你没有报名这个活动")
	case errors.Is(err, db.ErrAlreadyCancelled):
		legacyError(w, http.StatusConflict, "你已经取消过这次报名")
	case errors.Is(err, db.ErrSigninDisabled):
		legacyError(w, http.StatusBadRequest, "本活动未开启现场签到")
	case errors.Is(err, db.ErrSigninCodeNotSet):
		legacyError(w, http.StatusBadRequest, "本活动未设置签到码，请联系管理员")
	case errors.Is(err, db.ErrSigninCodeInvalid):
		legacyError(w, http.StatusBadRequest, "签到码不正确，请向现场工作人员确认")
	case errors.Is(err, db.ErrMarkedAbsent):
		legacyError(w, http.StatusBadRequest, "你已被标记为未到，请联系管理员")
	case errors.Is(err, db.ErrActivityStarted):
		legacyError(w, http.StatusConflict, "活动已经开始，无法"+action)
	case errors.Is(err, db.ErrActivityEnded):
		legacyError(w, http.StatusConflict, "活动已经结束，无法"+action)
	case errors.Is(err, db.ErrActivityNotStarted):
		legacyError(w, http.StatusConflict, "活动尚未开始，暂时无法"+action)
	default:
		legacyServerError(a, w, r, err, "legacy activity action failed")
		return
	}
	if action == "报名" || action == "取消报名" {
		a.logger(r).Info("legacy activity action rejected", "error", err, "action", action)
	}
}

// legacyChoiceCount returns how many volunteers the user stored for one
// activity. It is display-only (my activities card), so a read failure degrades
// to zero instead of breaking the whole list.
func (a *App) legacyChoiceCount(ctx context.Context, userID int64, activityNo string) int {
	status, err := a.db.GroupStatusForUser(ctx, userID, activityNo)
	if err != nil {
		return 0
	}
	return len(status.Choices)
}

func (a *App) handleLegacyMineActivities(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	items, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy my activities failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		view := buildActivityView(item.Activity)
		applySignupToView(&view, item.SignupStatus, item.Attendance)
		payload := legacyActivityMap(view)
		payload["my_choice_count"] = a.legacyChoiceCount(r.Context(), user.ID, item.ActivityNo)
		out = append(out, payload)
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": out})
}

func (a *App) handleLegacyMineGroups(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	items, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy my groups failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		// The v1 page only lists activities whose grouping feature has started.
		if item.GroupStatus == "" || item.GroupStatus == "未开始" {
			continue
		}
		view := buildActivityView(item.Activity)
		applySignupToView(&view, item.SignupStatus, item.Attendance)
		payload := legacyActivityMap(view)
		payload["my_choice_count"] = a.legacyChoiceCount(r.Context(), user.ID, item.ActivityNo)
		out = append(out, payload)
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": out})
}

func (a *App) handleLegacyMineGroupsFlag(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	items, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group flag failed")
		return
	}
	for _, item := range items {
		if item.GroupEnabled {
			writeJSON(w, http.StatusOK, map[string]any{"has_group_activity": true})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"has_group_activity": false})
}

// notifySignupLikes sends the two-way signup notices after the signup has
// already committed; a notification failure is logged, never returned.
func (a *App) notifySignupLikes(r *http.Request, signup db.Signup) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if _, err := a.db.NotifySignupLikes(ctx, signup.ID, signup.UserID, signup.ActivityID); err != nil {
		a.logger(r).Error("signup notifications failed", "error", err, "signup_id", signup.ID)
	}
}

func (a *App) handleLegacyActivitySignup(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if user, ok := currentUser(r); ok {
			a.invalidateReadCaches(user.ID)
		}
	}()
	user, ok := a.legacyActivityEditor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	// The v1 SPA POSTs an empty body. Paid activities show the QR before the
	// request; any optional payment hint is accepted and ignored here.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	_, _ = io.Copy(io.Discard, r.Body)

	signup, err := a.db.SignupActivity(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		a.writeLegacyActivityError(w, r, err, "报名")
		return
	}
	a.notifySignupLikes(r, signup)
	a.audit(r, &user.ID, "activity.signup", "activity",
		activityRefFromRequest(r), map[string]any{"signup_id": signup.ID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "报名成功"})
}

func (a *App) handleLegacyActivityCancel(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if user, ok := currentUser(r); ok {
			a.invalidateReadCaches(user.ID)
		}
	}()
	user, ok := a.legacyActivityEditor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	result, err := a.db.CancelSignup(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		a.writeLegacyActivityError(w, r, err, "取消报名")
		return
	}
	if result.RefundErr != nil {
		a.logger(r).Error("activity cancel refund hook failed", "error", result.RefundErr,
			"user_id", user.ID)
	}
	a.audit(r, &user.ID, "activity.cancel", "activity",
		activityRefFromRequest(r), map[string]any{"signup_id": result.Signup.ID})
	payload := map[string]any{"ok": true, "message": "已取消报名"}
	if result.RefundPoints > 0 {
		payload["message"] = fmt.Sprintf("已取消报名，优先名额的 %d 穗已退回", result.RefundPoints)
		payload["refund_points"] = result.RefundPoints
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleLegacyActivitySelfSignin(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if ok, retry := activitySigninLimiter.Allow("legacy_self_signin:" + strconv.FormatInt(user.ID, 10)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry/time.Second)+1))
		legacyError(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
		return
	}
	code := decodeSigninCode(r)
	if !validSigninCode(code) {
		legacyError(w, http.StatusBadRequest, "签到码须为 4 位数字")
		return
	}
	attendance, already, err := a.db.SelfSignin(r.Context(), user.ID,
		activityRefFromRequest(r), code)
	if err != nil {
		a.writeLegacyActivityError(w, r, err, "签到")
		return
	}
	a.audit(r, &user.ID, "activity.self_signin", "activity",
		activityRefFromRequest(r), map[string]any{"already": already})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"attendance": string(attendance),
		"already":    already,
	})
}

func (a *App) legacyActivitySignupProfiles(ctx context.Context, rows []db.ActivitySignup) ([]map[string]any, error) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	profiles, err := a.db.GetSocialProfilesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		profile := profiles[row.UserID]
		photo := ""
		if len(profile.Photos) > 0 {
			photo = socialPhotoView(profile.Photos[0]).ThumbURL
		}
		out = append(out, map[string]any{
			"openid":    legacyOpenID(profile.LegacyOpenID, row.LegacyUserID, row.UserID),
			"nickname":  row.Nickname,
			"user_id":   row.LegacyUserID,
			"real_name": "",
			"photo":     photo,
			"gender":    row.Gender,
		})
	}
	return out, nil
}

func (a *App) handleLegacyActivitySignups(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.legacyActivityViewer(w, r); !ok {
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity signups activity failed")
		return
	}
	rows, err := a.db.ListActivitySignups(r.Context(), activity.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity signups failed")
		return
	}
	payload := map[string]any{
		"activity_id": activity.ActivityNo,
		"count":       len(rows),
		"visible":     !a.cfg.ActivityRosterDisabled,
		"signups":     []any{},
	}
	if a.cfg.ActivityRosterDisabled {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	list, err := a.legacyActivitySignupProfiles(r.Context(), rows)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy activity signup profiles failed")
		return
	}
	payload["signups"] = list
	writeJSON(w, http.StatusOK, payload)
}
