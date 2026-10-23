package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// registerLegacyActivityAdmin restores the backend contract of the bundled v1
// activity console. The SPA prefixes every call with /legacy; these routes are
// thin adapters over the existing db layer and never reimplement matching.
func registerLegacyActivityAdmin(a *App, mux *http.ServeMux) {
	activityRead := []string{adminPermActivity, adminPermMatchingRun}
	matching := []string{adminPermMatchingRun}
	activity := []string{adminPermActivity}

	mux.HandleFunc("GET /legacy/api/admin/group/activities",
		a.legacyAdminAPI(activityRead, a.handleLegacyAdminActivities))
	mux.HandleFunc("GET /legacy/api/admin/group/status",
		a.legacyAdminAPI(activityRead, a.handleLegacyAdminActivityStatus))
	mux.HandleFunc("GET /legacy/api/admin/group/unsubmitted",
		a.legacyAdminAPI(matching, a.handleLegacyGroupUnsubmitted))
	mux.HandleFunc("GET /legacy/api/admin/onetoone/unsubmitted",
		a.legacyAdminAPI(matching, a.handleLegacyOnetooneUnsubmitted))
	mux.HandleFunc("POST /legacy/api/admin/group/preview",
		a.legacyAdminAPI(matching, a.handleLegacyGroupPreview))
	mux.HandleFunc("POST /legacy/api/admin/group/run",
		a.legacyAdminAPI(matching, a.handleLegacyGroupRun))
	mux.HandleFunc("POST /legacy/api/admin/group/reset",
		a.legacyAdminAPI(matching, a.handleLegacyGroupReset))
	mux.HandleFunc("GET /legacy/api/admin/group/job",
		a.legacyAdminAPI(matching, a.handleLegacyGroupJob))
	mux.HandleFunc("GET /legacy/api/admin/group/result",
		a.legacyAdminAPI(matching, a.handleLegacyAdminGroupResult))
	mux.HandleFunc("POST /legacy/api/admin/onetoone/preview",
		a.legacyAdminAPI(matching, a.handleLegacyOnetoonePreview))
	mux.HandleFunc("POST /legacy/api/admin/onetoone/run",
		a.legacyAdminAPI(matching, a.handleLegacyOnetooneRun))
	mux.HandleFunc("POST /legacy/api/admin/onetoone/reset",
		a.legacyAdminAPI(matching, a.handleLegacyOnetooneReset))
	mux.HandleFunc("GET /legacy/api/admin/onetoone/job",
		a.legacyAdminAPI(matching, a.handleLegacyOnetooneJob))
	mux.HandleFunc("GET /legacy/api/admin/onetoone/result",
		a.legacyAdminAPI(matching, a.handleLegacyAdminOnetooneResult))
	mux.HandleFunc("GET /legacy/api/admin/onetoone/chat-overview",
		a.legacyAdminAPI(matching, a.handleLegacyOnetooneChatOverview))

	mux.HandleFunc("POST /legacy/api/admin/activity/mode",
		a.legacyAdminAPI(activity, a.handleLegacyActivityMode))
	mux.HandleFunc("POST /legacy/api/admin/activity/status",
		a.legacyAdminAPI(activity, a.handleLegacyActivityStatus))
	mux.HandleFunc("POST /legacy/api/admin/activity/signin-code",
		a.legacyAdminAPI(activity, a.handleLegacyActivitySigninCode))
	mux.HandleFunc("GET /legacy/api/admin/activity/signin",
		a.legacyAdminAPI(activity, a.handleLegacyActivitySignin))
	mux.HandleFunc("POST /legacy/api/admin/activity/signin",
		a.legacyAdminAPI(activity, a.handleLegacyActivitySigninWrite))
	mux.HandleFunc("POST /legacy/api/admin/activity/signin/flag",
		a.legacyAdminAPI(activity, a.handleLegacyActivitySigninFlag))
}

func init() { registerModule(registerLegacyActivityAdmin) }

func containsAny(values []string, wanted ...string) bool {
	for _, want := range wanted {
		if containsString(values, want) {
			return true
		}
	}
	return false
}

// legacyAdminAPI wraps a handler with v1 auth semantics: anonymous 401
// "未登录", no admin role or a missing permission 403 "没有权限". The SPA's
// gaAuthBack() branches on those two strings, so they must not change.
func (a *App) legacyAdminAPI(perms []string,
	next func(http.ResponseWriter, *http.Request, adminAccess)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok {
			legacyError(w, http.StatusUnauthorized, "未登录")
			return
		}
		access, err := a.loadAdminAccess(r.Context(), user)
		if err != nil {
			a.logger(r).Error("legacy admin access failed", "error", err, "user_id", user.ID)
			legacyError(w, http.StatusInternalServerError, "系统暂时无法处理，请稍后再试")
			return
		}
		if len(access.Roles) == 0 {
			legacyError(w, http.StatusForbidden, "没有权限")
			return
		}
		if len(perms) > 0 && !containsAny(access.Permissions, perms...) {
			legacyError(w, http.StatusForbidden, "没有权限")
			return
		}
		next(w, r, access)
	}
}

type legacyAdminBody struct {
	ActivityID      json.RawMessage `json:"activity_id"`
	Round           *int            `json:"round"`
	MalesPerGroup   *int            `json:"males_per_group"`
	FemalesPerGroup *int            `json:"females_per_group"`
	MalePerGroup    *int            `json:"male_per_group"`
	FemalePerGroup  *int            `json:"female_per_group"`
	FemaleChats     *int            `json:"female_chats"`
	Mode            string          `json:"mode"`
	Status          string          `json:"status"`
	Code            json.RawMessage `json:"code"`
	Enabled         *bool           `json:"enabled"`
	OpenID          string          `json:"open_id"`
	State           *string         `json:"state"`
	ClearAll        bool            `json:"clear_all"`
}

func readLegacyAdminBody(w http.ResponseWriter, r *http.Request) (legacyAdminBody, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body legacyAdminBody
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return legacyAdminBody{}, false
	}
	return body, true
}

func (a *App) legacyActivityByRef(w http.ResponseWriter, r *http.Request, ref string) (db.Activity, bool) {
	activity, err := a.db.GetActivity(r.Context(), strings.TrimSpace(ref))
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return db.Activity{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin resolve activity failed")
		return db.Activity{}, false
	}
	return activity, true
}

func (a *App) legacyActivityCounts(w http.ResponseWriter, r *http.Request, activityID int64) (db.AdminActivityCounts, bool) {
	countsByID, err := a.db.AdminActivityCountsByID(r.Context(), []int64{activityID})
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin activity counts failed")
		return db.AdminActivityCounts{}, false
	}
	counts, ok := countsByID[activityID]
	if !ok {
		counts = db.AdminActivityCounts{ActivityID: activityID, GroupRounds: []int{}, OnetooneRounds: []int{}}
	}
	if counts.GroupRounds == nil {
		counts.GroupRounds = []int{}
	}
	if counts.OnetooneRounds == nil {
		counts.OnetooneRounds = []int{}
	}
	return counts, true
}

// ---------------------------------------------------------------------------
// Read endpoints
// ---------------------------------------------------------------------------

func (a *App) handleLegacyAdminActivities(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	activities, err := a.db.AdminListActivities(r.Context())
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin activities failed")
		return
	}
	countsByID, err := a.db.AdminActivityCountsByID(r.Context(), nil)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin activity counts failed")
		return
	}
	items := make([]map[string]any, 0, len(activities))
	for _, activity := range activities {
		counts := countsByID[activity.ID]
		counts.GroupRounds = legacyRounds(counts.GroupRounds)
		counts.OnetooneRounds = legacyRounds(counts.OnetooneRounds)
		items = append(items, buildLegacyActivityListItem(activity, counts))
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": items})
}

func (a *App) handleLegacyAdminActivityStatus(w http.ResponseWriter, r *http.Request, access adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	counts, ok := a.legacyActivityCounts(w, r, activity.ID)
	if !ok {
		return
	}
	includeCode := containsString(access.Permissions, adminPermActivity)
	writeJSON(w, http.StatusOK, buildLegacyActivityStatus(activity, counts, includeCode))
}

func (a *App) legacyRosterByRef(w http.ResponseWriter, r *http.Request) (db.Activity, db.MatchingRoster, bool) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return db.Activity{}, db.MatchingRoster{}, false
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return db.Activity{}, db.MatchingRoster{}, false
	}
	roster, err := a.db.MatchingRoster(r.Context(), activity.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin matching roster failed")
		return db.Activity{}, db.MatchingRoster{}, false
	}
	return activity, roster, true
}

func (a *App) handleLegacyGroupUnsubmitted(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	activity, roster, ok := a.legacyRosterByRef(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyUnsubmitted(activity, roster))
}

func (a *App) handleLegacyOnetooneUnsubmitted(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	activity, roster, ok := a.legacyRosterByRef(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyUnsubmitted(activity, roster))
}

func (a *App) handleLegacyAdminGroupResult(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return
	}
	round, ok := legacyRoundQuery(w, r)
	if !ok {
		return
	}
	result, err := a.db.AdminGroupResult(r.Context(), ref, round)
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin group result failed")
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyGroupResult(result))
}

func (a *App) handleLegacyAdminOnetooneResult(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	round, ok := legacyRoundQuery(w, r)
	if !ok {
		return
	}
	view, err := a.buildOnetooneResultView(r.Context(), activity, round)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin onetoone result failed")
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyOnetooneResult(view))
}

func (a *App) handleLegacyOnetooneChatOverview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	round := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			round = parsed
		}
	}
	view, err := a.buildOnetooneChatOverviewView(r.Context(), activity, round)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy admin chat overview failed")
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyChatOverview(view))
}

func legacyRoundQuery(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("round"))
	if raw == "" {
		return 0, true
	}
	round, err := strconv.Atoi(raw)
	if err != nil {
		legacyError(w, http.StatusBadRequest, "round 必须是整数")
		return 0, false
	}
	if round < 1 {
		legacyError(w, http.StatusBadRequest, "round 必须大于0")
		return 0, false
	}
	return round, true
}

// ---------------------------------------------------------------------------
// Group preview / run / reset / job
// ---------------------------------------------------------------------------

func (a *App) handleLegacyGroupPreview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if body.Round != nil && *body.Round < 1 {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	round := 0
	if body.Round != nil {
		round = *body.Round
	}
	preview, err := a.db.PreviewGrouping(r.Context(), ref, round, 0, 0)
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group preview failed")
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyGroupPreview(preview))
}

func (a *App) handleLegacyGroupRun(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	males := body.MalesPerGroup
	if males == nil {
		males = body.MalePerGroup
	}
	females := body.FemalesPerGroup
	if females == nil {
		females = body.FemalePerGroup
	}
	if males == nil || females == nil || *males < 1 || *females < 1 {
		legacyError(w, http.StatusBadRequest, "每组人数必须是大于0的整数")
		return
	}
	round := 0
	if body.Round != nil {
		if *body.Round < 1 {
			legacyError(w, http.StatusBadRequest, "请求体不合法")
			return
		}
		round = *body.Round
	}
	if round == 0 {
		// Resolve the default round before running so a duplicate round is
		// reported with the same number the SPA shows on its button.
		latest, err := a.db.LatestGroupRound(r.Context(), ref)
		if errors.Is(err, db.ErrActivityNotFound) {
			legacyError(w, http.StatusNotFound, "活动不存在")
			return
		}
		if err != nil {
			legacyServerError(a, w, r, err, "legacy latest group round failed")
			return
		}
		round = latest + 1
	}
	result, err := a.db.RunGrouping(r.Context(), ref, round, *males, *females)
	if err != nil {
		a.writeLegacyGroupRunError(w, r, err, round, *males, *females)
		return
	}
	a.audit(r, &access.User.ID, "group.run", "activity",
		strconv.FormatInt(result.ActivityID, 10), map[string]any{
			"activity_no":       result.ActivityNo,
			"round":             result.Round,
			"group_count":       result.GroupCount,
			"participant_count": result.ParticipantCount,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": buildLegacyGroupRunResult(result),
	})
}

// writeLegacyGroupRunError maps grouping failures to the v1 job result codes.
// Business failures stay HTTP 200 so the SPA's job-done branch handles them;
// only request/permission problems use 4xx.
func (a *App) writeLegacyGroupRunError(w http.ResponseWriter, r *http.Request,
	err error, round, male, female int) {
	var notEnough *db.GroupNotEnoughPeopleError
	switch {
	case errors.Is(err, db.ErrActivityNotFound):
		legacyError(w, http.StatusNotFound, "活动不存在")
	case errors.Is(err, db.ErrGroupBusy):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "另一场分组正在执行中，请稍候",
		})
	case errors.Is(err, db.ErrGroupSizeInvalid):
		legacyError(w, http.StatusBadRequest, "每组人数必须是大于0的整数")
	case errors.Is(err, db.ErrGroupRoundInvalid):
		legacyError(w, http.StatusBadRequest, "请求体不合法")
	case errors.Is(err, db.ErrGroupAlreadyDone):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code":  "ALREADY_DONE",
				"round": round,
			},
		})
	case errors.Is(err, db.ErrGroupModeConflict):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": map[string]any{"code": "EXCLUSIVE_MODE"},
		})
	case errors.Is(err, db.ErrGroupEmpty):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": map[string]any{"code": "EMPTY_GROUPS"},
		})
	case errors.As(err, &notEnough):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code":  "NOT_ENOUGH_PEOPLE",
				"males": notEnough.Males, "females": notEnough.Females,
				"males_per_group": male, "females_per_group": female,
			},
		})
	default:
		a.logger(r).Error("legacy group run failed", "error", err)
		legacyError(w, http.StatusInternalServerError, "分组执行失败，请稍后再试")
	}
}

func (a *App) handleLegacyGroupReset(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	cleared, err := a.db.ResetGrouping(r.Context(), activity.ActivityNo)
	if errors.Is(err, db.ErrGroupBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "另一场分组正在执行中，请稍候", "busy_activity_id": activity.ActivityNo,
		})
		return
	}
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy group reset failed", "error", err, "activity_id", activity.ID)
		legacyError(w, http.StatusInternalServerError, "清空分组结果失败，请稍后重试")
		return
	}
	a.audit(r, &access.User.ID, "group.reset", "activity", activity.ActivityNo,
		map[string]any{"cleared_results": cleared})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "activity_id": activity.ActivityNo, "cleared_results": cleared,
	})
}

func legacyJobNotSupported(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("job_id")) == "" {
		legacyError(w, http.StatusBadRequest, "缺少 job_id")
		return
	}
	// Execution is synchronous in v2: no job is ever created, so any id the
	// browser kept in localStorage is stale. v1 returned the same body.
	legacyError(w, http.StatusNotFound, "任务不存在")
}

func (a *App) handleLegacyGroupJob(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	legacyJobNotSupported(w, r)
}

func (a *App) handleLegacyOnetooneJob(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	legacyJobNotSupported(w, r)
}

// ---------------------------------------------------------------------------
// One-to-one preview / run / reset
// ---------------------------------------------------------------------------

func (a *App) handleLegacyOnetoonePreview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	round, ok := a.legacyOnetooneRound(w, r, activity, body.Round)
	if !ok {
		return
	}
	view, err := a.buildOnetoonePreviewView(r.Context(), activity, round, body.FemaleChats)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone preview failed")
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyOnetoonePreview(view))
}

func (a *App) legacyOnetooneRound(w http.ResponseWriter, r *http.Request,
	activity db.Activity, requested *int) (int, bool) {
	if requested != nil {
		if *requested < 1 {
			legacyError(w, http.StatusBadRequest, "请求体不合法")
			return 0, false
		}
		return *requested, true
	}
	latest, err := a.db.LatestOnetooneRound(r.Context(), activity.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy latest onetoone round failed")
		return 0, false
	}
	return latest + 1, true
}

func (a *App) handleLegacyOnetooneRun(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if body.FemaleChats == nil || *body.FemaleChats < 1 {
		legacyError(w, http.StatusBadRequest, "请求体不合法（缺少女生场次）")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	round, ok := a.legacyOnetooneRound(w, r, activity, body.Round)
	if !ok {
		return
	}
	result, err := a.db.ExecuteOnetoone(r.Context(), activity.ID, round, *body.FemaleChats)
	if err != nil {
		a.writeLegacyOnetooneRunError(w, r, err, round)
		return
	}
	a.audit(r, &access.User.ID, "onetoone.run", "activity",
		strconv.FormatInt(activity.ID, 10), map[string]any{
			"activity_no": activity.ActivityNo, "round": round,
			"female_chats": *body.FemaleChats, "pairs": result.Pairs,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"result": map[string]any{
			"code":              "OK",
			"activity_id":       activity.ActivityNo,
			"round":             result.Round,
			"total_pairs":       result.Pairs,
			"participant_count": result.Participants,
			"male_count":        result.MaleCount,
			"female_count":      result.FemaleCount,
		},
	})
}

func (a *App) writeLegacyOnetooneRunError(w http.ResponseWriter, r *http.Request,
	err error, round int) {
	switch {
	case errors.Is(err, db.ErrOnetooneRoundExists):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": map[string]any{"code": "ALREADY_DONE", "round": round},
		})
	case errors.Is(err, db.ErrOnetooneGroupMode):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": map[string]any{"code": "EXCLUSIVE_MODE"},
		})
	case errors.Is(err, db.ErrOnetooneNoParticipants):
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": map[string]any{"code": "NO_PARTICIPANTS"},
		})
	case errors.Is(err, db.ErrOnetooneFemaleChats):
		message := strings.TrimPrefix(err.Error(), "db: invalid female chats: ")
		legacyError(w, http.StatusBadRequest, message)
	default:
		a.logger(r).Error("legacy onetoone run failed", "error", err)
		legacyError(w, http.StatusInternalServerError, "一对一执行失败，请稍后再试")
	}
}

func (a *App) handleLegacyOnetooneReset(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	cleared, err := a.db.ResetOnetooneResults(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("legacy onetoone reset failed", "error", err, "activity_id", activity.ID)
		legacyError(w, http.StatusInternalServerError, "清空一对一结果失败，请稍后重试")
		return
	}
	a.audit(r, &access.User.ID, "onetoone.reset", "activity", activity.ActivityNo,
		map[string]any{"cleared_results": cleared})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "activity_id": activity.ActivityNo, "cleared_results": cleared,
	})
}

// ---------------------------------------------------------------------------
// Activity settings
// ---------------------------------------------------------------------------

func (a *App) handleLegacyActivityMode(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if body.Mode != "group" && body.Mode != "onetoone" && body.Mode != "none" {
		legacyError(w, http.StatusBadRequest, "mode 必须是 group/onetoone/none")
		return
	}
	before, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	updated, changed, err := a.db.SetActivityMode(r.Context(), before.ActivityNo, body.Mode)
	if errors.Is(err, db.ErrGroupBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "当前有分组/一对一任务在执行中，请稍候再试",
			"code":  "MODE_SWITCH_BLOCKED",
		})
		return
	}
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy activity mode failed", "error", err, "activity_id", before.ID)
		legacyError(w, http.StatusInternalServerError, "模式切换写入失败，请稍后重试")
		return
	}
	if changed {
		a.audit(r, &access.User.ID, "admin.activity.mode", "activity", updated.ActivityNo,
			map[string]any{
				"from": db.LegacyActivityMode(before.GroupEnabled, before.OnetooneEnabled),
				"to":   body.Mode,
			})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"activity_id":   updated.ActivityNo,
		"name":          updated.Name,
		"mode":          db.LegacyActivityMode(updated.GroupEnabled, updated.OnetooneEnabled),
		"group_flag":    legacyYesNo(updated.GroupEnabled),
		"onetoone_flag": legacyYesNo(updated.OnetooneEnabled),
		"changed":       changed,
	})
}

func (a *App) handleLegacyActivityStatus(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if !containsString(legacyActivityStatuses, body.Status) {
		legacyError(w, http.StatusBadRequest, "状态不合法")
		return
	}
	before, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	updated, changed, err := a.db.SetActivityStatus(r.Context(), before.ActivityNo, body.Status)
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy activity status failed", "error", err, "activity_id", before.ID)
		legacyError(w, http.StatusInternalServerError, "活动状态更新失败，请稍后重试")
		return
	}
	if changed {
		a.audit(r, &access.User.ID, "admin.activity.status", "activity", updated.ActivityNo,
			map[string]any{"from": before.Status, "to": updated.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": updated.Status})
}

func (a *App) handleLegacyActivitySigninCode(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	code, ok := legacySigninCode(body.Code)
	if !ok || !isFourDigits(code) {
		legacyError(w, http.StatusBadRequest, "签到码须为4位数字")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	updated, changed, err := a.db.SetActivitySigninCode(r.Context(), activity.ActivityNo, code)
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy signin code failed", "error", err, "activity_id", activity.ID)
		legacyError(w, http.StatusInternalServerError, "签到码保存失败，请稍后重试")
		return
	}
	if changed {
		// The code itself must never reach the audit log or a request log.
		a.audit(r, &access.User.ID, "admin.activity.signin_code", "activity", updated.ActivityNo, nil)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func legacySigninCode(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, true
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String(), true
	}
	return "", false
}

func isFourDigits(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (a *App) handleLegacyActivitySigninFlag(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if body.Enabled == nil {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	before, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	updated, changed, err := a.db.SetActivitySigninEnabled(r.Context(), before.ActivityNo, *body.Enabled)
	if errors.Is(err, db.ErrGroupBusy) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "当前有分组/一对一任务在执行中，请稍候再试",
		})
		return
	}
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy signin flag failed", "error", err, "activity_id", before.ID)
		legacyError(w, http.StatusInternalServerError, "签到开关写入失败，请稍后重试")
		return
	}
	if changed {
		a.audit(r, &access.User.ID, "admin.activity.signin_flag", "activity", updated.ActivityNo,
			map[string]any{"from": before.SigninEnabled, "to": updated.SigninEnabled})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "activity_id": updated.ActivityNo, "name": updated.Name,
		"signin_flag": legacyYesNo(updated.SigninEnabled),
	})
}

// ---------------------------------------------------------------------------
// On-site check-in
// ---------------------------------------------------------------------------

func (a *App) handleLegacyActivitySignin(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少 activity_id")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	roster, err := a.db.AdminActivityRoster(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("legacy signin roster failed", "error", err, "activity_id", activity.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "签到信息查询失败，请重试", "code": "SIGNIN_QUERY_FAILED",
		})
		return
	}
	writeJSON(w, http.StatusOK, buildLegacySigninRoster(activity, roster))
}

func (a *App) handleLegacyActivitySigninWrite(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	body, ok := readLegacyAdminBody(w, r)
	if !ok {
		return
	}
	ref := decodeAdminActivityRef(body.ActivityID)
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	activity, ok := a.legacyActivityByRef(w, r, ref)
	if !ok {
		return
	}
	if body.ClearAll {
		cleared, err := a.db.ClearActivityAttendance(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("legacy clear attendance failed", "error", err, "activity_id", activity.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "签到信息查询失败，请重试", "code": "SIGNIN_QUERY_FAILED",
			})
			return
		}
		total, err := a.db.CountActivitySignups(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("legacy count signups failed", "error", err, "activity_id", activity.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "签到信息查询失败，请重试", "code": "SIGNIN_QUERY_FAILED",
			})
			return
		}
		a.audit(r, &access.User.ID, "admin.activity.attendance_clear", "activity",
			activity.ActivityNo, map[string]any{"cleared": cleared, "signed_up": total})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "activity_id": activity.ActivityNo,
			"cleared": cleared, "failed": 0, "total": total,
		})
		return
	}

	target := strings.TrimSpace(body.OpenID)
	if target == "" || body.State == nil {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	state := *body.State
	if state != "按时" && state != "未到" && state != "" {
		legacyError(w, http.StatusBadRequest, "state 只能为「按时」「未到」或空（取消签到）")
		return
	}
	userID, err := a.resolveLegacyUserID(r.Context(), target)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			legacyError(w, http.StatusNotFound, "该用户没有报名此活动")
			return
		}
		legacyServerError(a, w, r, err, "legacy signin resolve user failed")
		return
	}
	updated, err := a.db.AdminSetActivityAttendance(r.Context(), activity.ID, userID,
		db.Attendance(state))
	if errors.Is(err, db.ErrNotSignedUp) {
		legacyError(w, http.StatusNotFound, "该用户没有报名此活动")
		return
	}
	if errors.Is(err, db.ErrInvalidAttendance) {
		legacyError(w, http.StatusBadRequest, "state 只能为「按时」「未到」或空（取消签到）")
		return
	}
	if err != nil {
		a.logger(r).Error("legacy set attendance failed", "error", err,
			"activity_id", activity.ID, "user_id", userID)
		legacyError(w, http.StatusInternalServerError, "写入失败，请稍后重试")
		return
	}
	a.audit(r, &access.User.ID, "admin.activity.attendance", "activity", activity.ActivityNo,
		map[string]any{"user_id": userID, "attendance": string(updated)})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "activity_id": activity.ActivityNo,
		"open_id": target, "state": string(updated),
	})
}
