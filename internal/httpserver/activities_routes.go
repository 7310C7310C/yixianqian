package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerActivities) }

// activitySigninLimiter limits failed and successful self sign-in attempts per
// user. Eight attempts per minute mirrors the v1 endpoint.
var activitySigninLimiter = auth.NewLimiter(8, time.Minute)

func registerActivities(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /activities/{id}/poster", a.handleActivityPoster)

}

// activitiesViewGate applies the legacy browse gate: pending, rejected, left
// and disabled accounts are blocked; active, matched and observer may view.
func (a *App) activitiesViewGate(ctx context.Context, user db.User) (kind, status string, ok bool) {
	kind, status, err := a.db.GetAccountState(ctx, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		return "", "", false
	}
	if err != nil {
		return "", "", false
	}
	switch status {
	case "active", "matched", "observer":
		return kind, status, true
	default:
		return kind, status, false
	}
}

func (a *App) requireActivityViewHTML(w http.ResponseWriter, r *http.Request, user db.User) bool {
	kind, status, ok := a.activitiesViewGate(r.Context(), user)
	if ok {
		return true
	}
	code := activityViewGateCode(kind, status)
	if code == "account_unavailable" && status == "" {
		a.renderError(w, r, http.StatusForbidden, "账号状态异常", "找不到账号状态，请联系管理员。")
		return false
	}
	a.renderError(w, r, http.StatusForbidden, "无法使用", activityGateMessage(kind, status))
	return false
}

func (a *App) requireActivityViewAPI(w http.ResponseWriter, r *http.Request, user db.User) bool {
	kind, status, ok := a.activitiesViewGate(r.Context(), user)
	if ok {
		return true
	}
	if status == "" {
		writeAPIError(w, http.StatusForbidden, "account_unavailable", "找不到账号状态。")
		return false
	}
	writeAPIError(w, http.StatusForbidden, activityViewGateCode(kind, status),
		activityGateMessage(kind, status))
	return false
}

// requireActivityActive applies the legacy action gate: only a single active
// user may sign up or cancel; observers and matched users are read-only.
func (a *App) requireActivityActiveHTML(w http.ResponseWriter, r *http.Request, user db.User) bool {
	kind, status, err := a.db.GetAccountState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusForbidden, "账号状态异常", "找不到账号状态，请联系管理员。")
		return false
	}
	if err != nil {
		a.logger(r).Error("load account state failed", "error", err, "user_id", user.ID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return false
	}
	if kind == "observer" || status != "active" {
		a.renderError(w, r, http.StatusForbidden, "无法操作", activityGateMessage(kind, status))
		return false
	}
	return true
}

func (a *App) requireActivityActiveAPI(w http.ResponseWriter, r *http.Request, user db.User) bool {
	kind, status, err := a.db.GetAccountState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusForbidden, "account_unavailable", "找不到账号状态。")
		return false
	}
	if err != nil {
		a.logger(r).Error("load account state failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return false
	}
	if kind == "observer" || status != "active" {
		writeAPIError(w, http.StatusForbidden, activityViewGateCode(kind, status),
			activityGateMessage(kind, status))
		return false
	}
	return true
}

func (a *App) loadActivityDetail(ctx context.Context, user db.User, ref string) (ActivityDetailPageData, error) {
	activity, err := a.db.GetActivity(ctx, ref)
	if err != nil {
		return ActivityDetailPageData{}, err
	}
	view := a.activityView(activity)
	if signup, err := a.db.GetSignup(ctx, activity.ID, user.ID); err == nil {
		applySignupToView(&view, signup.Status, signup.Attendance)
	} else if !errors.Is(err, db.ErrNotSignedUp) {
		return ActivityDetailPageData{}, err
	}
	count, err := a.db.CountActivitySignups(ctx, activity.ID)
	if err != nil {
		return ActivityDetailPageData{}, err
	}
	return ActivityDetailPageData{
		Activity:      view,
		RosterVisible: !a.cfg.ActivityRosterDisabled,
		RosterCount:   count,
		PaymentQR:     strings.TrimSpace(view.Fee) != "",
	}, nil
}

func activityNotice(raw string) string {
	switch raw {
	case "signup":
		return "报名成功。"
	case "cancelled":
		return "已取消报名。"
	case "signed_in":
		return "签到成功，已记录为按时。"
	case "already_signed_in":
		return "你已经签到过了。"
	default:
		return ""
	}
}

func activityRedirectURL(activityNo, notice string) string {
	u := "/activities/" + url.PathEscape(activityNo)
	if notice != "" {
		u += "?notice=" + url.QueryEscape(notice)
	}
	return u
}

func (a *App) activityRateLimited(w http.ResponseWriter, r *http.Request, user db.User, api bool) bool {
	ok, retry := activitySigninLimiter.Allow("self_signin:" + strconv.FormatInt(user.ID, 10))
	if ok {
		return false
	}
	seconds := int(retry.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	if api {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后再试。")
	} else {
		a.renderError(w, r, http.StatusTooManyRequests, "操作过于频繁", "签到尝试过于频繁，请稍后再试。")
	}
	return true
}

func validSigninCode(raw string) bool {
	code := strings.TrimSpace(raw)
	if len(code) != 4 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// decodeSigninCode accepts {"code":"0123"} or {"code":1234} for API clients and
// preserves string leading zeros. Invalid types yield an empty value.
func decodeSigninCode(r *http.Request) string {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
		dec.UseNumber()
		var payload struct {
			Code any `json:"code"`
		}
		if err := dec.Decode(&payload); err != nil {
			return ""
		}
		switch v := payload.Code.(type) {
		case string:
			return v
		case json.Number:
			return v.String()
		default:
			return ""
		}
	}
	if err := r.ParseForm(); err != nil {
		return ""
	}
	return r.PostFormValue("code")
}

func (a *App) writeActivityError(w http.ResponseWriter, r *http.Request, err error, action string, api bool) {
	status, code, message := activityActionError(err, action)
	if status >= 500 {
		a.logger(r).Error("activity action failed", "error", err, "action", action)
	}
	if api {
		writeAPIError(w, status, code, message)
		return
	}
	a.renderError(w, r, status, "无法完成操作", message)
}

func activityMapFromSignups(items []db.MyActivity) map[int64]db.MyActivity {
	out := make(map[int64]db.MyActivity, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func (a *App) activityListViews(items []db.Activity, mine map[int64]db.MyActivity) (open, other []ActivityView) {
	for _, item := range items {
		view := a.activityView(item)
		if my, ok := mine[item.ID]; ok {
			applySignupToView(&view, my.SignupStatus, my.Attendance)
		} else {
			view.CanSignup = view.Status == db.ActivityStatusOpen &&
				(view.MaxSignup <= 0 || view.CurrentSignup < view.MaxSignup)
		}
		switch view.Status {
		case db.ActivityStatusOpen, db.ActivityStatusNotStarted, db.ActivityStatusFull:
			open = append(open, view)
		default:
			other = append(other, view)
		}
	}
	return open, other
}

func activityRefFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("id"))
}
