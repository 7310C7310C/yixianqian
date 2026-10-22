package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerOnetoone) }

func registerOnetoone(a *App, mux *http.ServeMux) {
}

type onetoonePartnerView struct {
	ID         int64
	UserID     string
	Nickname   string
	Initial    string
	PhotoURL   string
	ProfileURL string
	MessageURL string
	Rank       int
	Chatted    bool
}

type onetooneResultPageData struct {
	Activity    ActivityView
	HasActivity bool
	Rounds      []int
	Round       int
	Status      string
	NotReady    bool
	Partners    []onetoonePartnerView
	CanMessage  bool
	MessageHint string
}

func onetooneInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(r)
	}
	return "👤"
}

func onetooneResultViews(rows []db.OnetooneResult, chatted map[int64]bool) []onetoonePartnerView {
	partners := make([]onetoonePartnerView, 0, len(rows))
	for _, row := range rows {
		view := onetoonePartnerView{
			ID:       row.TargetID,
			UserID:   row.TargetLegacyID,
			Nickname: row.TargetDisplayName(),
			Rank:     row.Rank,
			Chatted:  chatted[row.TargetID],
		}
		view.Initial = onetooneInitial(view.Nickname)
		if row.TargetPhotoID != "" {
			view.PhotoURL = "/profile/photo/" + row.TargetPhotoID + "?size=thumb"
		}
		if row.TargetLegacyID != "" {
			view.ProfileURL = "/u/" + row.TargetLegacyID
		} else {
			view.ProfileURL = "/u/" + strconv.FormatInt(row.TargetID, 10)
		}
		if view.Chatted {
			view.MessageURL = "/messages?target=" + strconv.FormatInt(row.TargetID, 10)
		}
		partners = append(partners, view)
	}
	return partners
}

func onetooneRoundFromQuery(raw string, rounds []int, fallbackLatest bool) int {
	if len(rounds) == 0 {
		return 0
	}
	if value := strings.TrimSpace(raw); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 {
			if !fallbackLatest || onetooneRoundInList(parsed, rounds) {
				return parsed
			}
		}
	}
	return rounds[len(rounds)-1]
}

func onetooneRoundInList(round int, rounds []int) bool {
	for _, value := range rounds {
		if value == round {
			return true
		}
	}
	return false
}

// handleOnetooneResultCompat keeps the legacy /onetoone/result entry working.
// It redirects to the canonical activity route, choosing the user's most recent
// result when no activity reference is supplied.
func onetooneAPIPartner(row db.OnetooneResult, chatted bool) map[string]any {
	photo := ""
	if row.TargetPhotoID != "" {
		photo = "/profile/photo/" + row.TargetPhotoID + "?size=thumb"
	}
	return map[string]any{
		"id":       row.TargetID,
		"user_id":  row.TargetLegacyID,
		"nickname": row.TargetDisplayName(),
		"photo":    photo,
		"rank":     row.Rank,
		"chatted":  chatted,
	}
}

func (a *App) apiOnetooneResult(w http.ResponseWriter, r *http.Request) {
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
		a.logger(r).Error("load onetoone activity failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	if strings.TrimSpace(activity.OnetooneStatus) != db.OnetooneStatusDone {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":           apiError{Code: "onetoone_not_completed", Message: "一对一匹配尚未完成。"},
			"onetoone_status": activity.OnetooneStatus,
		})
		return
	}
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone rounds failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	round := onetooneRoundFromQuery(r.URL.Query().Get("round"), rounds, true)
	rows, err := a.db.ListOnetooneResultsForUser(r.Context(), activity.ID, user.ID, round)
	if err != nil {
		a.logger(r).Error("list my onetoone results failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	targets := make([]int64, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, row.TargetID)
	}
	chatted, err := a.db.ChattedTargetIDs(r.Context(), user.ID, targets)
	if err != nil {
		a.logger(r).Error("load chatted targets failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	partners := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		partners = append(partners, onetooneAPIPartner(row, chatted[row.TargetID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"activity_id":     activity.ID,
		"activity_no":     activity.ActivityNo,
		"name":            activity.Name,
		"onetoone_status": activity.OnetooneStatus,
		"round":           round,
		"rounds":          rounds,
		"count":           len(partners),
		"partners":        partners,
	})
}

func (a *App) apiOnetooneResultCompat(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("activity"))
	if ref == "" {
		ref = strings.TrimSpace(r.URL.Query().Get("activity_id"))
	}
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "请指定活动。")
		return
	}
	activity, err := a.db.GetActivity(r.Context(), ref)
	if errors.Is(err, db.ErrActivityNotFound) {
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	target := "/api/activities/" + url.PathEscape(activity.ActivityNo) + "/onetoone/result"
	if round := strings.TrimSpace(r.URL.Query().Get("round")); round != "" {
		target += "?round=" + url.QueryEscape(round)
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}
