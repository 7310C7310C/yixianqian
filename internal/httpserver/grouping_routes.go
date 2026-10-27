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

func init() { registerModule(registerGroupingRoutes) }

func registerGroupingRoutes(a *App, mux *http.ServeMux) {

}

func groupPageError(err error) (int, string) {
	var choiceErr *db.GroupChoiceError
	switch {
	case errors.Is(err, db.ErrActivityNotFound):
		return http.StatusNotFound, "活动不存在。"
	case errors.Is(err, db.ErrActivityEnded):
		return http.StatusConflict, "活动已经结束，无法进行分组操作。"
	case errors.Is(err, db.ErrActivityNotOpen):
		return http.StatusConflict, "活动未开启分组，无法进行该操作。"
	case errors.Is(err, db.ErrGroupNotSignedUp), errors.Is(err, db.ErrNotSignedUp):
		return http.StatusForbidden, "你未报名此活动。"
	case errors.Is(err, db.ErrGroupSigninRequired):
		return http.StatusForbidden, "本活动需现场签到后才能填志愿，请联系管理员。"
	case errors.Is(err, db.ErrGroupChoiceCount):
		return http.StatusBadRequest, "请选满 7 个志愿。"
	case errors.Is(err, db.ErrGroupChoiceDuplicate):
		return http.StatusBadRequest, "不能重复选择同一人。"
	case errors.As(err, &choiceErr):
		return http.StatusBadRequest,
			fmt.Sprintf("第 %d 个志愿不在可选名单中（须为本活动已报名的异性），请重新整理后再试。",
				choiceErr.Index+1)
	case errors.Is(err, db.ErrGroupNotComplete):
		return http.StatusConflict, "分组尚未完成。"
	default:
		return http.StatusInternalServerError, "系统暂时无法处理，请稍后再试。"
	}
}

func groupActionError(err error) (int, string, string) {
	var choiceErr *db.GroupChoiceError
	switch {
	case errors.Is(err, db.ErrActivityNotFound):
		return http.StatusNotFound, "activity_not_found", "活动不存在。"
	case errors.Is(err, db.ErrActivityEnded):
		return http.StatusConflict, "activity_ended", "活动已经结束，无法进行分组操作。"
	case errors.Is(err, db.ErrActivityNotOpen):
		return http.StatusConflict, "activity_not_open", "活动未开启分组，无法进行该操作。"
	case errors.Is(err, db.ErrGroupNotSignedUp), errors.Is(err, db.ErrNotSignedUp):
		return http.StatusForbidden, "not_signed_up", "你未报名此活动。"
	case errors.Is(err, db.ErrGroupSigninRequired):
		return http.StatusForbidden, "signin_required", "本活动需现场签到后才能填志愿，请联系管理员。"
	case errors.Is(err, db.ErrGroupChoiceCount):
		return http.StatusBadRequest, "select_not_full", "请选满 7 个志愿。"
	case errors.Is(err, db.ErrGroupChoiceDuplicate):
		return http.StatusBadRequest, "select_duplicate", "不能重复选择同一人。"
	case errors.As(err, &choiceErr):
		return http.StatusBadRequest, "select_invalid",
			fmt.Sprintf("第 %d 个志愿不在可选名单中（须为本活动已报名的异性）。",
				choiceErr.Index+1)
	case errors.Is(err, db.ErrGroupNotComplete):
		return http.StatusConflict, "group_not_complete", "分组尚未完成。"
	default:
		return http.StatusInternalServerError, "internal_error", "系统暂时无法处理。"
	}
}

func (a *App) apiGroupCandidates(w http.ResponseWriter, r *http.Request) {
	a.writeGroupCandidates(w, r, true)
}

func (a *App) apiGroupStatus(w http.ResponseWriter, r *http.Request) {
	a.writeGroupStatus(w, r, true)
}

func (a *App) writeGroupCandidates(w http.ResponseWriter, r *http.Request, api bool) {
	var user db.User
	var ok bool
	if api {
		user, ok = a.requireUserAPI(w, r)
		if ok {
			ok = a.requireActivityActiveAPI(w, r, user)
		}
	} else {
		user, ok = a.requireUserHTML(w, r)
		if ok {
			ok = a.requireActivityActiveHTML(w, r, user)
		}
	}
	if !ok {
		return
	}
	_, candidates, err := a.db.GroupCandidates(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		status, code, message := groupActionError(err)
		if status >= http.StatusInternalServerError {
			a.logger(r).Error("group candidates failed", "error", err, "user_id", user.ID)
		}
		writeAPIError(w, status, code, message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": buildGroupCandidateViews(candidates)})
}

func (a *App) writeGroupStatus(w http.ResponseWriter, r *http.Request, api bool) {
	var user db.User
	var ok bool
	if api {
		user, ok = a.requireUserAPI(w, r)
		if ok {
			ok = a.requireActivityViewAPI(w, r, user)
		}
	} else {
		user, ok = a.requireUserHTML(w, r)
		if ok {
			ok = a.requireActivityViewHTML(w, r, user)
		}
	}
	if !ok {
		return
	}
	status, err := a.db.GroupStatusForUser(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		if httpStatus, code, message := groupActionError(err); httpStatus < http.StatusInternalServerError {
			writeAPIError(w, httpStatus, code, message)
			return
		}
		a.logger(r).Error("group status failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	choices := status.Choices
	if choices == nil {
		choices = []int64{}
	}
	writeJSON(w, http.StatusOK, GroupStatusView{
		GroupStatus: status.GroupStatus,
		MySelected:  status.Selected,
		MyChoices:   choices,
		Round:       status.Round,
	})
}

func decodeGroupChoicePayload(r *http.Request) ([]int64, error) {
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	var payload struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	choices := make([]int64, 0, len(payload.Choices))
	for _, raw := range payload.Choices {
		var id int64
		if err := json.Unmarshal(raw, &id); err == nil && id > 0 {
			choices = append(choices, id)
			continue
		}
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			parsed, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if parseErr == nil && parsed > 0 {
				choices = append(choices, parsed)
				continue
			}
		}
		return nil, errors.New("invalid choice")
	}
	return choices, nil
}

func (a *App) apiGroupSelect(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityActiveAPI(w, r, user) {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "表单已过期，请重新整理页面后再试。")
		return
	}
	choices, err := decodeGroupChoicePayload(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_choices", "志愿格式不合法。")
		return
	}
	if len(choices) != 7 {
		writeAPIError(w, http.StatusBadRequest, "select_not_full",
			fmt.Sprintf("请选满 7 个志愿（目前已选 %d 个）。", len(choices)))
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if err != nil {
		status, code, message := groupActionError(err)
		writeAPIError(w, status, code, message)
		return
	}
	selection, err := a.db.SelectGroupChoices(r.Context(), user.ID, activityRefFromRequest(r), choices)
	if err != nil {
		status, code, message := groupActionError(err)
		var choiceErr *db.GroupChoiceError
		if errors.Is(err, db.ErrGroupChoiceCount) {
			message = fmt.Sprintf("请选满 7 个志愿（目前已选 %d 个）。", len(choices))
		}
		if errors.As(err, &choiceErr) {
			message = fmt.Sprintf("第 %d 个志愿不在可选名单中（须为本活动已报名的异性）。",
				choiceErr.Index+1)
		}
		if status >= http.StatusInternalServerError {
			a.logger(r).Error("group select failed", "error", err, "user_id", user.ID)
		}
		writeAPIError(w, status, code, message)
		return
	}
	a.audit(r, &user.ID, "group.select", "activity", strconv.FormatInt(activity.ID, 10),
		map[string]any{"activity_no": activity.ActivityNo, "choices": choices})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "志愿提交成功",
		"choices": selection.Choices,
	})
}

func groupRoundParam(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("round"))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0
	}
	return value
}

func (a *App) apiGroupResult(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.requireActivityViewAPI(w, r, user) {
		return
	}
	round := groupRoundParam(r)
	result, err := a.db.GroupResultForUser(r.Context(), user.ID, activityRefFromRequest(r), round)
	if err != nil {
		status, code, message := groupActionError(err)
		if status >= http.StatusInternalServerError {
			a.logger(r).Error("group result failed", "error", err, "user_id", user.ID)
		}
		writeAPIError(w, status, code, message)
		return
	}
	writeJSON(w, http.StatusOK, buildGroupResultView(result))
}
