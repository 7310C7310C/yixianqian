package httpserver

import (
	"errors"
	"net/http"

	"github.com/7310C7310C/yixianqian/internal/db"
)

type activityDetailJSON struct {
	ActivityView
	RosterVisible bool     `json:"roster_visible"`
	SignupCount   int      `json:"signup_count"`
	SignupUsers   []string `json:"signup_users,omitempty"`
}

func (a *App) apiListActivities(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	activities, err := a.db.ListActivities(r.Context())
	if err != nil {
		a.logger(r).Error("list activities failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	mine, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("list my activities failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	open, other := a.activityListViews(activities, activityMapFromSignups(mine))
	views := make([]ActivityView, 0, len(open)+len(other))
	views = append(views, open...)
	views = append(views, other...)
	writeJSON(w, http.StatusOK, map[string]any{"activities": views})
}

func (a *App) apiGetActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrActivityNotFound) {
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("load activity failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	view := a.activityView(activity)
	if signup, err := a.db.GetSignup(r.Context(), activity.ID, user.ID); err == nil {
		applySignupToView(&view, signup.Status, signup.Attendance)
	} else if !errors.Is(err, db.ErrNotSignedUp) {
		a.logger(r).Error("load own signup failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	count, err := a.db.CountActivitySignups(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("count signups failed", "error", err, "activity_id", activity.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := activityDetailJSON{
		ActivityView:  view,
		RosterVisible: !a.cfg.ActivityRosterDisabled,
		SignupCount:   count,
	}
	if out.RosterVisible {
		rows, err := a.db.ListActivitySignups(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("list signups failed", "error", err, "activity_id", activity.ID)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
			return
		}
		out.SignupUsers = make([]string, 0, len(rows))
		for _, row := range rows {
			out.SignupUsers = append(out.SignupUsers, row.Nickname)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) apiMyActivities(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	items, err := a.db.ListMyActivities(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("list my activities failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	views := make([]ActivityView, 0, len(items))
	for _, item := range items {
		view := a.activityView(item.Activity)
		applySignupToView(&view, item.SignupStatus, item.Attendance)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": views})
}

func (a *App) apiActivitySignups(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrActivityNotFound) {
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("load activity failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	count, err := a.db.CountActivitySignups(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("count signups failed", "error", err, "activity_id", activity.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := map[string]any{
		"activity_id": activity.ActivityNo,
		"count":       count,
		"visible":     !a.cfg.ActivityRosterDisabled,
		"signups":     []ActivitySignupView{},
	}
	if !a.cfg.ActivityRosterDisabled {
		rows, err := a.db.ListActivitySignups(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("list signups failed", "error", err, "activity_id", activity.ID)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
			return
		}
		out["signups"] = buildActivitySignupViews(rows)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) apiActivitySignup(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityActiveAPI(w, r, user) {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	signup, err := a.db.SignupActivity(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		a.writeActivityError(w, r, err, "报名", true)
		return
	}
	a.notifySignupLikes(r, signup)
	a.audit(r, &user.ID, "activity.signup", "activity",
		activityRefFromRequest(r), map[string]any{"signup_id": signup.ID})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"message":       "报名成功。",
		"signup_status": signup.Status,
	})
}

func (a *App) apiActivityCancel(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityActiveAPI(w, r, user) {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	result, err := a.db.CancelSignup(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		a.writeActivityError(w, r, err, "取消报名", true)
		return
	}
	if result.RefundErr != nil {
		a.logger(r).Error("activity cancel refund hook failed", "error", result.RefundErr,
			"user_id", user.ID)
	}
	a.audit(r, &user.ID, "activity.cancel", "activity",
		activityRefFromRequest(r), map[string]any{"signup_id": result.Signup.ID})
	payload := map[string]any{"ok": true, "message": "已取消报名。", "signup_status": result.Signup.Status}
	if result.RefundPoints > 0 {
		payload["refund_points"] = result.RefundPoints
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) apiActivitySelfSignin(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if a.activityRateLimited(w, r, user, true) {
		return
	}
	code := decodeSigninCode(r)
	if !validSigninCode(code) {
		writeAPIError(w, http.StatusBadRequest, "signin_code_invalid", "签到码须为 4 位数字。")
		return
	}
	attendance, already, err := a.db.SelfSignin(r.Context(), user.ID,
		activityRefFromRequest(r), code)
	if err != nil {
		a.writeActivityError(w, r, err, "签到", true)
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
