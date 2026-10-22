package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const adminPermMatchingRun = "matching.run"

func init() { registerModule(registerGroupingAdminRoutes) }

func registerGroupingAdminRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/group/preview",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupPreview))
	mux.HandleFunc("POST /api/admin/group/run",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupRun))
	mux.HandleFunc("POST /api/admin/group/reset",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupReset))
	mux.HandleFunc("GET /api/admin/group/result",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupResult))
	mux.HandleFunc("GET /api/admin/group/latest_round",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupLatestRound))
}

type adminGroupPayload struct {
	ActivityID      json.RawMessage `json:"activity_id"`
	Round           *int            `json:"round"`
	MalesPerGroup   *int            `json:"male_per_group"`
	FemalesPerGroup *int            `json:"female_per_group"`
}

func decodeAdminActivityRef(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	return ""
}

func readAdminGroupPayload(r *http.Request) (adminGroupPayload, error) {
	var payload adminGroupPayload
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		dec := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
		if err := dec.Decode(&payload); err != nil && err != io.EOF {
			return payload, err
		}
		return payload, nil
	}
	if err := r.ParseForm(); err != nil {
		return payload, err
	}
	if raw := strings.TrimSpace(r.PostFormValue("activity_id")); raw != "" {
		payload.ActivityID, _ = json.Marshal(raw)
	}
	if raw := strings.TrimSpace(r.PostFormValue("round")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			payload.Round = &value
		}
	}
	if raw := strings.TrimSpace(r.PostFormValue("male_per_group")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			payload.MalesPerGroup = &value
		}
	}
	if raw := strings.TrimSpace(r.PostFormValue("female_per_group")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			payload.FemalesPerGroup = &value
		}
	}
	return payload, nil
}

func adminGroupRefFromRequest(r *http.Request, payload adminGroupPayload) string {
	if ref := decodeAdminActivityRef(payload.ActivityID); ref != "" {
		return ref
	}
	return strings.TrimSpace(r.URL.Query().Get("activity_id"))
}

func adminQueryInt(r *http.Request, key string) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil {
		return 0
	}
	return value
}

func adminGroupError(w http.ResponseWriter, r *http.Request, err error) {
	var notEnough *db.GroupNotEnoughPeopleError
	switch {
	case err == nil:
		return
	case errors.Is(err, db.ErrActivityNotFound):
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
	case errors.Is(err, db.ErrGroupBusy):
		writeAPIError(w, http.StatusConflict, "group_busy", "此活动的分组作业正在进行中，请稍后再试。")
	case errors.Is(err, db.ErrGroupAlreadyDone):
		writeAPIError(w, http.StatusConflict, "group_already_done", "此轮次已有分组结果，请勿重复执行。")
	case errors.Is(err, db.ErrGroupSizeInvalid):
		writeAPIError(w, http.StatusBadRequest, "group_size_invalid", "每组男女人数必须大于 0。")
	case errors.Is(err, db.ErrGroupRoundInvalid):
		writeAPIError(w, http.StatusBadRequest, "invalid_round", "轮次必须是 ≥1 的整数。")
	case errors.Is(err, db.ErrGroupModeConflict):
		writeAPIError(w, http.StatusConflict, "group_mode_conflict", "一对一功能已开启，分组与一对一互斥。")
	case errors.Is(err, db.ErrGroupEmpty):
		writeAPIError(w, http.StatusConflict, "empty_groups", "人数不足，无法产生分组。")
	case errors.As(err, &notEnough):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": apiError{
				Code: "not_enough_people",
				Message: fmt.Sprintf("人数不足：目前 %d 男 %d 女，每组需要 %d 男 %d 女。",
					notEnough.Males, notEnough.Females, notEnough.NeedM, notEnough.NeedF),
			},
			"males":   notEnough.Males,
			"females": notEnough.Females,
		})
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
	}
}

func (a *App) apiAdminGroupPreview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	preview, err := a.db.PreviewGrouping(r.Context(), ref,
		adminQueryInt(r, "round"), adminQueryInt(r, "male_per_group"),
		adminQueryInt(r, "female_per_group"))
	if err != nil {
		a.logger(r).Error("group preview failed", "error", err)
		adminGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, buildGroupPreviewView(preview))
}

func (a *App) apiAdminGroupRun(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "表单已过期，请重新整理页面后再试。")
		return
	}
	payload, err := readAdminGroupPayload(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_payload", "请求内容不合法。")
		return
	}
	ref := adminGroupRefFromRequest(r, payload)
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	round, male, female := 0, 0, 0
	if payload.Round != nil {
		round = *payload.Round
	}
	if payload.MalesPerGroup != nil {
		male = *payload.MalesPerGroup
	}
	if payload.FemalesPerGroup != nil {
		female = *payload.FemalesPerGroup
	}
	result, err := a.db.RunGrouping(r.Context(), ref, round, male, female)
	if err != nil {
		a.logger(r).Error("group run failed", "error", err, "actor", access.User.ID)
		adminGroupError(w, r, err)
		return
	}
	a.audit(r, &access.User.ID, "group.run", "activity",
		strconv.FormatInt(result.ActivityID, 10), map[string]any{
			"activity_no":       result.ActivityNo,
			"round":             result.Round,
			"group_count":       result.GroupCount,
			"participant_count": result.ParticipantCount,
		})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": buildGroupRunView(result)})
}

func (a *App) apiAdminGroupReset(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "表单已过期，请重新整理页面后再试。")
		return
	}
	payload, err := readAdminGroupPayload(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_payload", "请求内容不合法。")
		return
	}
	ref := adminGroupRefFromRequest(r, payload)
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	cleared, err := a.db.ResetGrouping(r.Context(), ref)
	if err != nil {
		a.logger(r).Error("group reset failed", "error", err, "actor", access.User.ID)
		adminGroupError(w, r, err)
		return
	}
	a.audit(r, &access.User.ID, "group.reset", "activity", ref,
		map[string]any{"cleared_results": cleared})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared_results": cleared})
}

func (a *App) apiAdminGroupResult(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	result, err := a.db.AdminGroupResult(r.Context(), ref, adminQueryInt(r, "round"))
	if err != nil {
		a.logger(r).Error("admin group result failed", "error", err)
		adminGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, buildAdminGroupResultView(result))
}

func (a *App) apiAdminGroupLatestRound(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	round, err := a.db.LatestGroupRound(r.Context(), ref)
	if err != nil {
		a.logger(r).Error("latest group round failed", "error", err)
		adminGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity_id": ref, "round": round})
}
