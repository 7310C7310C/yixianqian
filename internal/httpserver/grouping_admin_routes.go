package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	mux.HandleFunc("GET /api/admin/group/roster",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupRoster))
	mux.HandleFunc("GET /api/admin/group/chat-overview",
		a.adminAPI(adminPermMatchingRun, a.apiAdminGroupChatOverview))
}

type adminGroupPayload struct {
	ActivityID json.RawMessage `json:"activity_id"`
	Round      *int            `json:"round"`
	// The v1 console sends plural keys; the v2 console sends singular ones.
	// Both are accepted so one endpoint serves both clients.
	MalesPerGroup         *int `json:"male_per_group"`
	FemalesPerGroup       *int `json:"female_per_group"`
	MalesPerGroupPlural   *int `json:"males_per_group"`
	FemalesPerGroupPlural *int `json:"females_per_group"`
}

func (p adminGroupPayload) groupSize() (male, female int) {
	maleValue := p.MalesPerGroup
	if maleValue == nil {
		maleValue = p.MalesPerGroupPlural
	}
	femaleValue := p.FemalesPerGroup
	if femaleValue == nil {
		femaleValue = p.FemalesPerGroupPlural
	}
	if maleValue != nil {
		male = *maleValue
	}
	if femaleValue != nil {
		female = *femaleValue
	}
	return male, female
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
	if raw := strings.TrimSpace(firstNonEmpty(r.PostFormValue("male_per_group"),
		r.PostFormValue("males_per_group"))); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			payload.MalesPerGroup = &value
		}
	}
	if raw := strings.TrimSpace(firstNonEmpty(r.PostFormValue("female_per_group"),
		r.PostFormValue("females_per_group"))); raw != "" {
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
	malePerGroup := adminQueryInt(r, "male_per_group")
	if malePerGroup == 0 {
		malePerGroup = adminQueryInt(r, "males_per_group")
	}
	femalePerGroup := adminQueryInt(r, "female_per_group")
	if femalePerGroup == 0 {
		femalePerGroup = adminQueryInt(r, "females_per_group")
	}
	preview, err := a.db.PreviewGrouping(r.Context(), ref,
		adminQueryInt(r, "round"), malePerGroup, femalePerGroup)
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
	round := 0
	if payload.Round != nil {
		round = *payload.Round
	}
	male, female := payload.groupSize()
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

// ---------------------------------------------------------------------------
// Roster
// ---------------------------------------------------------------------------

type matchingRosterEntryView struct {
	ID         int64  `json:"id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Nickname   string `json:"nickname"`
	Gender     string `json:"gender"`
	Attendance string `json:"attendance"`
	CheckedIn  bool   `json:"checked_in"`
	Submitted  bool   `json:"submitted"`
	SignupAt   string `json:"signup_at"`
}

type matchingRosterView struct {
	Total                int                       `json:"total"`
	Submitted            int                       `json:"submitted"`
	Unsubmitted          int                       `json:"unsubmitted"`
	CheckedIn            int                       `json:"checked_in"`
	UnsubmittedCheckedIn int                       `json:"unsubmitted_checked_in"`
	SigninEnabled        bool                      `json:"signin_enabled"`
	SubmittedList        []matchingRosterEntryView `json:"submitted_list"`
	UnsubmittedList      []matchingRosterEntryView `json:"unsubmitted_list"`
}

func buildMatchingRosterView(activity db.Activity, roster db.MatchingRoster) matchingRosterView {
	view := matchingRosterView{
		Total:                roster.Total,
		Submitted:            roster.Submitted,
		Unsubmitted:          roster.Unsubmitted,
		CheckedIn:            roster.CheckedIn,
		UnsubmittedCheckedIn: roster.UnsubmittedCheckedIn,
		SigninEnabled:        activity.SigninEnabled,
		SubmittedList:        make([]matchingRosterEntryView, 0, roster.Submitted),
		UnsubmittedList:      make([]matchingRosterEntryView, 0, roster.Unsubmitted),
	}
	for _, entry := range roster.Entries {
		item := matchingRosterEntryView{
			ID:         entry.ID,
			UserID:     entry.UserID,
			Name:       entry.Name,
			Nickname:   entry.Nickname,
			Gender:     entry.Gender,
			Attendance: entry.Attendance,
			CheckedIn:  entry.CheckedIn,
			Submitted:  entry.Submitted,
			SignupAt:   entry.SignupAt.Format("2006-01-02 15:04"),
		}
		if entry.Submitted {
			view.SubmittedList = append(view.SubmittedList, item)
		} else {
			view.UnsubmittedList = append(view.UnsubmittedList, item)
		}
	}
	return view
}

func (a *App) adminMatchingActivityByRef(w http.ResponseWriter, r *http.Request,
	ref string) (db.Activity, bool) {
	activity, err := a.db.GetActivity(r.Context(), strings.TrimSpace(ref))
	if errors.Is(err, db.ErrActivityNotFound) {
		writeAPIError(w, http.StatusNotFound, "activity_not_found", "活动不存在。")
		return db.Activity{}, false
	}
	if err != nil {
		a.logger(r).Error("resolve matching activity failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return db.Activity{}, false
	}
	return activity, true
}

func (a *App) apiAdminGroupRoster(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.adminMatchingActivityByRef(w, r, ref)
	if !ok {
		return
	}
	roster, err := a.db.MatchingRoster(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("load matching roster failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	writeJSON(w, http.StatusOK, buildMatchingRosterView(activity, roster))
}

// ---------------------------------------------------------------------------
// Group chat overview
// ---------------------------------------------------------------------------

type groupChatMemberView struct {
	ID       int64  `json:"id"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Gender   string `json:"gender"`
	Assigned int    `json:"assigned"`
	Chatted  int    `json:"chatted"`
	MsgCount int    `json:"msg_count"`
}

type groupChatGroupView struct {
	GroupID      int                   `json:"group_id"`
	MemberCount  int                   `json:"member_count"`
	Pairs        int                   `json:"pairs"`
	ChattedPairs int                   `json:"chatted_pairs"`
	Messages     int                   `json:"messages"`
	Members      []groupChatMemberView `json:"members"`
}

type groupChatSummaryView struct {
	Groups       int     `json:"groups"`
	Members      int     `json:"members"`
	Pairs        int     `json:"pairs"`
	ChattedPairs int     `json:"chatted_pairs"`
	Rate         float64 `json:"rate"`
	Messages     int     `json:"messages"`
}

type groupChatOverviewView struct {
	ActivityID  int64                `json:"activity_id"`
	ActivityNo  string               `json:"activity_no"`
	Name        string               `json:"name"`
	GroupStatus string               `json:"group_status"`
	Round       int                  `json:"round"`
	Rounds      []int                `json:"rounds"`
	Summary     groupChatSummaryView `json:"summary"`
	Groups      []groupChatGroupView `json:"groups"`
	// RatePercent is a template helper; it is not part of the JSON payload.
	RatePercent int `json:"-"`
}

func (a *App) buildGroupChatOverview(ctx context.Context, activity db.Activity,
	round int) (groupChatOverviewView, error) {
	result, err := a.db.AdminGroupResult(ctx, activity.ActivityNo, round)
	if err != nil {
		return groupChatOverviewView{}, err
	}
	view := groupChatOverviewView{
		ActivityID:  result.ActivityID,
		ActivityNo:  result.ActivityNo,
		Name:        result.ActivityName,
		GroupStatus: result.GroupStatus,
		Round:       result.Round,
		Rounds:      result.Rounds,
		Groups:      make([]groupChatGroupView, 0, len(result.Groups)),
		Summary:     groupChatSummaryView{Groups: len(result.Groups)},
	}
	if len(result.Groups) == 0 {
		return view, nil
	}

	allIDs := make([]int64, 0)
	seen := map[int64]bool{}
	for _, group := range result.Groups {
		for _, member := range group.Members {
			if !seen[member.UserID] {
				seen[member.UserID] = true
				allIDs = append(allIDs, member.UserID)
			}
		}
	}
	stats, err := a.db.OnetooneMessageStats(ctx, allIDs, allIDs)
	if err != nil {
		return groupChatOverviewView{}, err
	}
	for _, group := range result.Groups {
		item := groupChatGroupView{
			GroupID:     group.GroupID,
			MemberCount: len(group.Members),
			Members:     make([]groupChatMemberView, 0, len(group.Members)),
		}
		item.Pairs = item.MemberCount * (item.MemberCount - 1) / 2
		memberSet := make(map[int64]bool, item.MemberCount)
		for _, member := range group.Members {
			memberSet[member.UserID] = true
		}
		for _, member := range group.Members {
			row := groupChatMemberView{
				ID:       member.UserID,
				UserID:   member.LegacyUserID,
				Name:     member.DisplayName(),
				Gender:   member.Gender,
				Assigned: item.MemberCount - 1,
			}
			for _, other := range group.Members {
				if other.UserID == member.UserID {
					continue
				}
				sent := stats[db.OnetoonePairKey{AuthorID: member.UserID, TargetID: other.UserID}]
				received := stats[db.OnetoonePairKey{AuthorID: other.UserID, TargetID: member.UserID}]
				row.MsgCount += sent.Count
				if sent.Count > 0 || received.Count > 0 {
					row.Chatted++
				}
			}
			item.Messages += row.MsgCount
			item.Members = append(item.Members, row)
		}
		for i := 0; i < len(group.Members); i++ {
			for j := i + 1; j < len(group.Members); j++ {
				left := group.Members[i].UserID
				right := group.Members[j].UserID
				if stats[db.OnetoonePairKey{AuthorID: left, TargetID: right}].Count > 0 ||
					stats[db.OnetoonePairKey{AuthorID: right, TargetID: left}].Count > 0 {
					item.ChattedPairs++
				}
			}
		}
		view.Summary.Members += item.MemberCount
		view.Summary.Pairs += item.Pairs
		view.Summary.ChattedPairs += item.ChattedPairs
		view.Summary.Messages += item.Messages
		view.Groups = append(view.Groups, item)
	}
	if view.Summary.Pairs > 0 {
		view.Summary.Rate = math.Round(float64(view.Summary.ChattedPairs)/
			float64(view.Summary.Pairs)*10000) / 10000
	}
	view.RatePercent = int(math.Round(view.Summary.Rate * 100))
	return view, nil
}

func (a *App) apiAdminGroupChatOverview(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	ref := strings.TrimSpace(r.URL.Query().Get("activity_id"))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity", "缺少 activity_id。")
		return
	}
	activity, ok := a.adminMatchingActivityByRef(w, r, ref)
	if !ok {
		return
	}
	overview, err := a.buildGroupChatOverview(r.Context(), activity, adminQueryInt(r, "round"))
	if err != nil {
		a.logger(r).Error("load group chat overview failed", "error", err)
		adminGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}
