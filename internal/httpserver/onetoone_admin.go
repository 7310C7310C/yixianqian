package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func registerOnetooneAdmin(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/onetoone/status",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetooneStatus))
	mux.HandleFunc("POST /api/admin/onetoone/preview",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetoonePreview))
	mux.HandleFunc("POST /api/admin/onetoone/run",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetooneRun))
	mux.HandleFunc("POST /api/admin/onetoone/reset",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetooneReset))
	mux.HandleFunc("GET /api/admin/onetoone/result",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetooneResult))
	mux.HandleFunc("GET /api/admin/onetoone/chat-overview",
		a.adminAPI(adminPermMatchingRun, a.apiAdminOnetooneChatOverview))
}

func init() { registerModule(registerOnetooneAdmin) }

type onetooneAdminBody struct {
	ActivityID  string `json:"activity_id"`
	Round       *int   `json:"round"`
	FemaleChats *int   `json:"female_chats"`
}

func (a *App) resolveAdminOnetooneActivity(w http.ResponseWriter, r *http.Request,
	ref string) (db.Activity, bool) {
	activity, err := a.db.GetActivity(r.Context(), strings.TrimSpace(ref))
	if errors.Is(err, db.ErrActivityNotFound) {
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
		return db.Activity{}, false
	}
	if err != nil {
		a.logger(r).Error("resolve admin onetoone activity failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return db.Activity{}, false
	}
	return activity, true
}

func validAdminOnetooneRound(value int) bool { return value >= 1 }

func (a *App) adminOnetooneRound(w http.ResponseWriter, r *http.Request,
	activity db.Activity, requested *int) (int, bool) {
	if requested != nil {
		if !validAdminOnetooneRound(*requested) {
			writeAPIError(w, http.StatusBadRequest, "invalid_round", "轮次必须是 ≥1 的整数。")
			return 0, false
		}
		return *requested, true
	}
	latest, err := a.db.LatestOnetooneRound(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("load latest onetoone round failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "读取历史结果失败。")
		return 0, false
	}
	return latest + 1, true
}

func onetooneParticipantCounts(participants []db.OnetooneParticipant, signin bool) (males, females int) {
	for _, p := range participants {
		gender := strings.TrimSpace(p.Gender)
		if gender != "男性" && gender != "女性" {
			continue
		}
		if signin && !db.IsCheckedIn(p.Attendance) {
			continue
		}
		if gender == "男性" {
			males++
		} else {
			females++
		}
	}
	return males, females
}

func (a *App) apiAdminOnetooneStatus(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone rounds failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	latest := 0
	if len(rounds) > 0 {
		latest = rounds[len(rounds)-1]
	}
	signups, err := a.db.CountActivitySignups(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("count onetoone signups failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	submitted, err := a.db.CountOnetooneSelections(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("count onetoone submissions failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	participants, err := a.db.ListOnetooneParticipants(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone participants failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	males, females := onetooneParticipantCounts(participants, activity.SigninEnabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"activity_id":       activity.ID,
		"activity_no":       activity.ActivityNo,
		"name":              activity.Name,
		"onetoone_status":   activity.OnetooneStatus,
		"onetoone_enabled":  activity.OnetooneEnabled,
		"group_enabled":     activity.GroupEnabled,
		"signup_count":      signups,
		"submitted_count":   submitted,
		"participant_count": males + females,
		"male_count":        males,
		"female_count":      females,
		"rounds":            rounds,
		"latest_round":      latest,
	})
}

func (a *App) apiAdminOnetoonePreview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body onetooneAdminBody
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	ref := strings.TrimSpace(body.ActivityID)
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	round, ok := a.adminOnetooneRound(w, r, activity, body.Round)
	if !ok {
		return
	}
	preview, err := a.buildOnetoonePreviewView(r.Context(), activity, round, body.FemaleChats)
	if err != nil {
		a.logger(r).Error("build onetoone preview failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (a *App) apiAdminOnetooneRun(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body onetooneAdminBody
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	ref := strings.TrimSpace(body.ActivityID)
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	if body.FemaleChats == nil || *body.FemaleChats < 1 {
		writeAPIError(w, http.StatusBadRequest, "invalid_female_chats",
			"女生场次必须是 ≥1 的整数（每位女生接待的男生数）。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	round, ok := a.adminOnetooneRound(w, r, activity, body.Round)
	if !ok {
		return
	}
	result, err := a.db.ExecuteOnetoone(r.Context(), activity.ID, round, *body.FemaleChats)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrOnetooneRoundExists):
			writeAPIError(w, http.StatusConflict, "already_done",
				"第"+strconv.Itoa(round)+"轮已生成过一对一结果，请勿重复执行。")
		case errors.Is(err, db.ErrOnetooneGroupMode):
			writeAPIError(w, http.StatusConflict, "exclusive_mode",
				"活动目前为分组模式，请先切换到一对一模式。")
		case errors.Is(err, db.ErrOnetooneNoParticipants):
			writeAPIError(w, http.StatusBadRequest, "no_participants",
				"没有已报名且性别有效的参与者。")
		case errors.Is(err, db.ErrOnetooneFemaleChats):
			message := strings.TrimPrefix(err.Error(), "db: invalid female chats: ")
			writeAPIError(w, http.StatusBadRequest, "invalid_female_chats", message)
		default:
			a.logger(r).Error("execute onetoone failed", "error", err,
				"activity_id", activity.ID, "round", round)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "一对一执行失败，请稍后再试。")
		}
		return
	}
	a.audit(r, &access.User.ID, "onetoone.run", "activity",
		strconv.FormatInt(activity.ID, 10), map[string]any{
			"activity_no": activity.ActivityNo, "round": round,
			"female_chats": *body.FemaleChats, "pairs": result.Pairs,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"activity_id":       activity.ID,
		"activity_no":       activity.ActivityNo,
		"round":             result.Round,
		"participant_count": result.Participants,
		"male_count":        result.MaleCount,
		"female_count":      result.FemaleCount,
		"total_pairs":       result.Pairs,
	})
}

func (a *App) apiAdminOnetooneReset(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body onetooneAdminBody
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	ref := strings.TrimSpace(body.ActivityID)
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	cleared, err := a.db.ResetOnetooneResults(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("reset onetoone failed", "error", err, "activity_id", activity.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "清空一对一结果失败。")
		return
	}
	a.audit(r, &access.User.ID, "onetoone.reset", "activity",
		strconv.FormatInt(activity.ID, 10), map[string]any{
			"activity_no": activity.ActivityNo, "cleared_results": cleared,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"activity_id":     activity.ID,
		"cleared_results": cleared,
	})
}

func (a *App) apiAdminOnetooneResult(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	requested := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_round", "round 必须是 ≥1 的整数。")
			return
		}
		requested = parsed
	}
	view, err := a.buildOnetooneResultView(r.Context(), activity, requested)
	if err != nil {
		a.logger(r).Error("build onetoone result failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func roundHasResults(round int, rounds []int) bool {
	for _, value := range rounds {
		if value == round {
			return true
		}
	}
	return false
}

type adminOnetoonePair struct {
	OpenID       int64  `json:"id"`
	LegacyOpenID string `json:"open_id"`
	UserID       string `json:"user_id"`
	Name         string `json:"name"`
	Rank         int    `json:"rank"`
	Chatted      bool   `json:"chatted"`
	MsgCount     int    `json:"msg_count"`
	LastAt       string `json:"last_at"`
}

type adminOnetoonePerson struct {
	OpenID       int64               `json:"id"`
	LegacyOpenID string              `json:"open_id"`
	UserID       string              `json:"user_id"`
	Name         string              `json:"name"`
	Assigned     int                 `json:"assigned"`
	Chatted      int                 `json:"chatted"`
	MsgCount     int                 `json:"msg_count"`
	Pairs        []adminOnetoonePair `json:"pairs"`
	Unchatted    []map[string]any    `json:"unchatted"`
}

func (a *App) apiAdminOnetooneChatOverview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.resolveAdminOnetooneActivity(w, r, ref)
	if !ok {
		return
	}
	requested := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			requested = parsed
		}
	}
	view, err := a.buildOnetooneChatOverviewView(r.Context(), activity, requested)
	if err != nil {
		a.logger(r).Error("build onetoone chat overview failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
