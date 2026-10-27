package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func registerLegacyMatching(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /legacy/api/activities/{id}/group/status", a.handleLegacyGroupStatus)
	mux.HandleFunc("GET /legacy/api/activities/{id}/group/candidates", a.handleLegacyGroupCandidates)
	mux.HandleFunc("POST /legacy/api/activities/{id}/group/select", a.handleLegacyGroupSelect)
	mux.HandleFunc("GET /legacy/api/activities/{id}/group/result", a.handleLegacyGroupResult)
	mux.HandleFunc("GET /legacy/api/activities/{id}/onetoone/result", a.handleLegacyOnetooneResult)
	mux.HandleFunc("POST /legacy/api/activities/{id}/onetoone/chat", a.handleLegacyOnetooneChat)
}

func legacyErrorCode(w http.ResponseWriter, status int, code, message string) {
	payload := map[string]any{"error": message}
	if strings.TrimSpace(code) != "" {
		payload["code"] = code
	}
	writeJSON(w, status, payload)
}

func legacyOpenID(openID, legacyID string, userID int64) string {
	if v := strings.TrimSpace(openID); v != "" {
		return v
	}
	if v := strings.TrimSpace(legacyID); v != "" {
		return v
	}
	if userID > 0 {
		return strconv.FormatInt(userID, 10)
	}
	return ""
}

func (a *App) legacyProfilesByIDs(ctx context.Context, ids []int64) (map[int64]db.SocialProfile, error) {
	if len(ids) == 0 {
		return map[int64]db.SocialProfile{}, nil
	}
	return a.db.GetSocialProfilesByIDs(ctx, ids)
}

func legacyRoundParam(r *http.Request) int {
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

func legacyResolveRound(rounds []int, requested int) int {
	if len(rounds) == 0 {
		return 0
	}
	if requested <= 0 {
		return rounds[len(rounds)-1]
	}
	for _, round := range rounds {
		if round == requested {
			return requested
		}
	}
	return rounds[len(rounds)-1]
}

func legacyCandidateOpenID(candidate db.GroupCandidate, profile db.SocialProfile) string {
	if profile.ID != 0 {
		return legacyOpenID(profile.LegacyOpenID, profile.LegacyID, candidate.UserID)
	}
	return legacyOpenID(candidate.LegacyOpenID, candidate.LegacyUserID, candidate.UserID)
}

func legacyGroupError(w http.ResponseWriter, status int, code, message string) {
	legacyErrorCode(w, status, code, message)
}

func (a *App) writeLegacyGroupActionError(w http.ResponseWriter, r *http.Request, err error) {
	var choiceErr *db.GroupChoiceError
	switch {
	case errors.Is(err, db.ErrActivityNotFound):
		legacyGroupError(w, http.StatusNotFound, "", "活动不存在")
	case errors.Is(err, db.ErrActivityEnded):
		legacyGroupError(w, http.StatusConflict, "activity_ended", "活动已经结束，无法进行分组操作")
	case errors.Is(err, db.ErrActivityNotOpen):
		legacyGroupError(w, http.StatusConflict, "activity_not_open", "活动未开启分组，无法进行该操作")
	case errors.Is(err, db.ErrGroupNotSignedUp), errors.Is(err, db.ErrNotSignedUp):
		legacyGroupError(w, http.StatusForbidden, "", "你未报名此活动")
	case errors.Is(err, db.ErrGroupSigninRequired):
		legacyGroupError(w, http.StatusForbidden, "SIGNIN_REQUIRED",
			"本活动需现场签到后才能填志愿，请联系管理员")
	case errors.Is(err, db.ErrGroupChoiceCount):
		legacyGroupError(w, http.StatusBadRequest, "SELECT_NOT_FULL", "请选满7个志愿")
	case errors.Is(err, db.ErrGroupChoiceDuplicate):
		legacyGroupError(w, http.StatusBadRequest, "", "不能重复选择同一人")
	case errors.As(err, &choiceErr):
		legacyGroupError(w, http.StatusBadRequest, "",
			fmt.Sprintf("第%d个志愿不在可选列表中（须为本活动已报名的异性）", choiceErr.Index+1))
	default:
		legacyServerError(a, w, r, err, "legacy group action failed")
	}
}

func (a *App) handleLegacyGroupStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	status, err := a.db.GroupStatusForUser(r.Context(), user.ID, activityRefFromRequest(r))
	if err != nil {
		a.writeLegacyGroupActionError(w, r, err)
		return
	}
	profiles, err := a.legacyProfilesByIDs(r.Context(), status.Choices)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group status profiles failed")
		return
	}
	choices := make([]string, 0, len(status.Choices))
	for _, id := range status.Choices {
		profile := profiles[id]
		choices = append(choices, legacyOpenID(profile.LegacyOpenID, profile.LegacyID, id))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group_status": status.GroupStatus,
		"my_selected":  status.Selected,
		"my_choices":   choices,
		"round":        status.Round,
	})
}

func (a *App) handleLegacyGroupCandidates(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	activity, candidates, err := a.db.GroupCandidates(r.Context(), user.ID,
		activityRefFromRequest(r))
	if err != nil {
		a.writeLegacyGroupActionError(w, r, err)
		return
	}
	_ = activity
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.UserID)
	}
	profiles, err := a.legacyProfilesByIDs(r.Context(), ids)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group candidate profiles failed")
		return
	}
	out := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		profile := profiles[candidate.UserID]
		baptismalName := ""
		if profile.ID != 0 {
			baptismalName = profile.BaptismalName
		}
		birthday := ""
		if candidate.Birthday != nil {
			birthday = candidate.Birthday.Format("2006-01-02")
		}
		out = append(out, map[string]any{
			"openid":         legacyCandidateOpenID(candidate, profile),
			"nickname":       candidate.Nickname,
			"real_name":      candidate.Name,
			"photo":          groupPhotoURL(candidate.PhotoID, true),
			"user_id":        candidate.LegacyUserID,
			"baptismal_name": baptismalName,
			"birthday":       birthday,
			"education":      candidate.Education,
			"native_place":   candidate.NativePlace,
			"city":           candidate.City,
			"hobbies":        strings.Join(candidate.Hobbies, "、"),
			"gender":         candidate.Gender,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

func decodeLegacyGroupChoices(r *http.Request) ([]string, error) {
	var payload struct {
		Choices []string `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Choices, nil
}

func (a *App) handleLegacyGroupSelect(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityEditor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	choices, err := decodeLegacyGroupChoices(r)
	if err != nil {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if len(choices) != 7 {
		legacyGroupError(w, http.StatusBadRequest, "SELECT_NOT_FULL",
			fmt.Sprintf("请选满7个志愿（当前已选 %d 个）", len(choices)))
		return
	}
	ids := make([]int64, 0, len(choices))
	for i, ref := range choices {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			legacyGroupError(w, http.StatusBadRequest, "",
				fmt.Sprintf("第%d个志愿不在可选列表中（须为本活动已报名的异性）", i+1))
			return
		}
		target, err := a.db.FindUserByRef(r.Context(), ref)
		if errors.Is(err, db.ErrNotFound) {
			legacyGroupError(w, http.StatusBadRequest, "",
				fmt.Sprintf("第%d个志愿不在可选列表中（须为本活动已报名的异性）", i+1))
			return
		}
		if err != nil {
			legacyServerError(a, w, r, err, "legacy group resolve choice failed")
			return
		}
		ids = append(ids, target.ID)
	}
	selection, err := a.db.SelectGroupChoices(r.Context(), user.ID,
		activityRefFromRequest(r), ids)
	if err != nil {
		a.writeLegacyGroupActionError(w, r, err)
		return
	}
	a.audit(r, &user.ID, "group.select", "activity",
		activityRefFromRequest(r), map[string]any{"choices": ids})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "志愿提交成功",
		"choices": selection.Choices,
	})
}

func (a *App) handleLegacyGroupResult(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	result, err := a.db.GroupResultForUser(r.Context(), user.ID,
		activityRefFromRequest(r), legacyRoundParam(r))
	if errors.Is(err, db.ErrGroupNotComplete) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":        "分组尚未完成",
			"group_status": result.GroupStatus,
		})
		return
	}
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if errors.Is(err, db.ErrActivityEnded) || errors.Is(err, db.ErrActivityNotOpen) {
		a.writeLegacyGroupActionError(w, r, err)
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group result failed")
		return
	}
	rounds := result.Rounds
	if len(rounds) == 0 {
		rounds = []int{1}
		if result.Round == 0 {
			result.Round = 1
		}
	}
	ids := make([]int64, 0, len(result.Members))
	for _, member := range result.Members {
		ids = append(ids, member.UserID)
	}
	profiles, err := a.legacyProfilesByIDs(r.Context(), ids)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy group result profiles failed")
		return
	}
	members := make([]map[string]any, 0, len(result.Members))
	for _, member := range result.Members {
		profile := profiles[member.UserID]
		openid := legacyOpenID(profile.LegacyOpenID, profile.LegacyID, member.UserID)
		if profile.ID == 0 {
			openid = legacyOpenID("", member.LegacyUserID, member.UserID)
		}
		members = append(members, map[string]any{
			"openid":   openid,
			"nickname": member.DisplayName(),
			"gender":   member.Gender,
			"user_id":  member.LegacyUserID,
			"photo":    groupPhotoURL(member.PhotoID, true),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"my_group":     result.MyGroup,
		"members":      members,
		"total_groups": result.TotalGroups,
		"round":        result.Round,
		"rounds":       rounds,
	})
}

func legacyOnetooneName(row db.OnetooneResult) string {
	if name := strings.TrimSpace(row.TargetRealName); name != "" {
		return name
	}
	if name := strings.TrimSpace(row.TargetName); name != "" {
		return name
	}
	if name := strings.TrimSpace(row.TargetNickname); name != "" {
		return name
	}
	return strconv.FormatInt(row.TargetID, 10)
}

func legacyOnetoonePartner(row db.OnetooneResult, chatted bool) map[string]any {
	photo := ""
	if row.TargetPhotoID != "" {
		photo = socialPhotoView(row.TargetPhotoID).ThumbURL
	}
	return map[string]any{
		"openid":   legacyOpenID(row.TargetOpenID, row.TargetLegacyID, row.TargetID),
		"nickname": legacyOnetooneName(row),
		"photo":    photo,
		"user_id":  row.TargetLegacyID,
		"rank":     row.Rank,
		"chatted":  chatted,
	}
}

func (a *App) handleLegacyOnetooneResult(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityViewer(w, r)
	if !ok {
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone activity failed")
		return
	}
	if activity.EndedAt(a.clock.Now()) {
		legacyGroupError(w, http.StatusConflict, "activity_ended", "活动已经结束，无法查看必聊结果")
		return
	}
	if !activity.OnetooneEnabled {
		legacyGroupError(w, http.StatusConflict, "onetoone_disabled", "本活动未开启一对一匹配")
		return
	}
	if strings.TrimSpace(activity.OnetooneStatus) != db.OnetooneStatusDone {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":           "一对一匹配尚未完成",
			"onetoone_status": activity.OnetooneStatus,
		})
		return
	}
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone rounds failed")
		return
	}
	if len(rounds) == 0 {
		rounds = []int{1}
	}
	round := legacyResolveRound(rounds, legacyRoundParam(r))
	rows, err := a.db.ListOnetooneResultsForUser(r.Context(), activity.ID, user.ID, round)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone results failed")
		return
	}
	targets := make([]int64, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, row.TargetID)
	}
	chatted, err := a.db.ChattedTargetIDs(r.Context(), user.ID, targets)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone chatted failed")
		return
	}
	partners := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		partners = append(partners, legacyOnetoonePartner(row, chatted[row.TargetID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"partners": partners,
		"count":    len(partners),
		"round":    round,
		"rounds":   rounds,
	})
}

func (a *App) handleLegacyOnetooneChat(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActivityEditor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if ok, retry := takeRateLimit(messageCreateLimiter, "message:create:"+strconv.FormatInt(user.ID, 10)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		legacyError(w, http.StatusTooManyRequests, "留言太频繁，请稍后再试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var payload struct {
		TargetOpenID string `json:"target_openid"`
		Target       string `json:"target"`
		Content      string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		legacyError(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	ref := strings.TrimSpace(firstNonEmpty(payload.TargetOpenID, payload.Target))
	if ref == "" {
		legacyError(w, http.StatusBadRequest, "缺少目标用户")
		return
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		legacyError(w, http.StatusBadRequest, "内容不能为空")
		return
	}
	if utf8.RuneCountInString(content) > db.MessageMaxRunes {
		legacyError(w, http.StatusBadRequest, "留言最多 500 字")
		return
	}
	activity, err := a.db.GetActivity(r.Context(), activityRefFromRequest(r))
	if errors.Is(err, db.ErrActivityNotFound) {
		legacyError(w, http.StatusNotFound, "活动不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone chat activity failed")
		return
	}
	if activity.EndedAt(a.clock.Now()) {
		legacyGroupError(w, http.StatusConflict, "activity_ended", "活动已经结束，无法留言")
		return
	}
	if !activity.OnetooneEnabled {
		legacyGroupError(w, http.StatusConflict, "onetoone_disabled", "本活动未开启一对一匹配")
		return
	}
	if strings.TrimSpace(activity.OnetooneStatus) != db.OnetooneStatusDone {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":           "一对一匹配尚未完成",
			"onetoone_status": activity.OnetooneStatus,
		})
		return
	}
	target, err := a.db.FindUserByRef(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusBadRequest, "目标用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone chat target failed")
		return
	}
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone chat rounds failed")
		return
	}
	targetID := target.ID
	found := false
	if len(rounds) > 0 {
		round := legacyResolveRound(rounds, legacyRoundParam(r))
		rows, err := a.db.ListOnetooneResultsForUser(r.Context(), activity.ID, user.ID, round)
		if err != nil {
			legacyServerError(a, w, r, err, "legacy onetoone chat results failed")
			return
		}
		for _, row := range rows {
			if row.TargetID == targetID {
				found = true
				break
			}
		}
	}
	if !found {
		legacyError(w, http.StatusBadRequest, "TA 不在你的必聊名单中")
		return
	}
	msg, err := a.db.CreateMessage(r.Context(), db.CreateMessageParams{
		TargetUserID: targetID,
		AuthorID:     user.ID,
		Content:      content,
	})
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusBadRequest, "目标用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy onetoone chat message failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"chatted": true,
		"id":      msg.ID,
	})
}
