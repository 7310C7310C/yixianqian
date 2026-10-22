package httpserver

import (
	"errors"
	"math"
	"net/http"
	"sort"
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
	participants, err := a.db.ListOnetooneParticipants(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone participants failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	males, females := onetooneParticipantCounts(participants, activity.SigninEnabled)
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone rounds failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	roundCounts, err := a.db.OnetooneRoundCounts(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("count onetoone results failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	existingRound := roundCounts[round]
	totalResults := 0
	for _, count := range roundCounts {
		totalResults += count
	}

	blockers := make([]string, 0)
	if activity.GroupEnabled {
		blockers = append(blockers, "活动目前为分组模式，一对一与分组互斥")
	}
	if males+females == 0 {
		blockers = append(blockers, "没有已报名且性别有效的参与者")
	}
	if existingRound > 0 {
		blockers = append(blockers, "第"+strconv.Itoa(round)+"轮已生成一对一结果，请勿重复执行")
	}
	if body.FemaleChats == nil {
		blockers = append(blockers, "请指定女生场次（每位女生接待几位男生）")
	} else if *body.FemaleChats < 1 {
		blockers = append(blockers, "女生场次必须是 ≥1 的整数")
	} else if breakRound := *body.FemaleChats; breakRound > males && males > 0 {
		blockers = append(blockers, "女生场次 "+strconv.Itoa(breakRound)+
			" 超过男生人数 "+strconv.Itoa(males)+"（每个女生最多和每位男生各聊一次）")
	}
	expected := 0
	if body.FemaleChats != nil && *body.FemaleChats >= 1 {
		expected = 2 * females * *body.FemaleChats
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"activity_id":                 activity.ID,
		"activity_no":                 activity.ActivityNo,
		"name":                        activity.Name,
		"onetoone_status":             activity.OnetooneStatus,
		"round":                       round,
		"participant_count":           males + females,
		"male_count":                  males,
		"female_count":                females,
		"female_chats":                body.FemaleChats,
		"total_pairs_expected":        expected,
		"existing_results":            totalResults,
		"existing_round_results":      existingRound,
		"existing_rounds":             rounds,
		"historical_results_to_clear": totalResults,
		"historical_rounds_to_clear":  rounds,
		"rounds_preserved":            rounds,
		"results_preserved":           totalResults,
		"can_execute":                 len(blockers) == 0,
		"blockers":                    blockers,
	})
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
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone rounds failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	round := 0
	if len(rounds) > 0 {
		round = rounds[len(rounds)-1]
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_round", "round 必须是 ≥1 的整数。")
			return
		}
		round = parsed
	}
	rows, err := a.db.ListOnetooneResults(r.Context(), activity.ID, round)
	if err != nil {
		a.logger(r).Error("list onetoone results failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	summary := db.SummarizeOnetooneResults(rows)
	people := make([]map[string]any, 0, len(summary.People))
	for _, person := range summary.People {
		partners := make([]map[string]any, 0, len(person.Partners))
		for _, row := range person.Partners {
			partners = append(partners, map[string]any{
				"id":       row.TargetID,
				"user_id":  row.TargetLegacyID,
				"nickname": row.TargetAdminDisplayName(),
				"rank":     row.Rank,
			})
		}
		people = append(people, map[string]any{
			"id":       person.UserID,
			"user_id":  person.UserIDLabel,
			"name":     person.Name,
			"gender":   person.Gender,
			"partners": partners,
		})
	}
	sort.SliceStable(people, func(i, j int) bool {
		left, _ := people[i]["user_id"].(string)
		right, _ := people[j]["user_id"].(string)
		if left != right {
			return left < right
		}
		return people[i]["id"].(int64) < people[j]["id"].(int64)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"activity_id":       activity.ID,
		"activity_no":       activity.ActivityNo,
		"name":              activity.Name,
		"onetoone_status":   activity.OnetooneStatus,
		"round":             round,
		"rounds":            rounds,
		"result_count":      summary.ResultCount,
		"matched_count":     summary.MatchedCount,
		"people":            people,
		"round_has_results": roundHasResults(round, rounds),
	})
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
	OpenID   int64  `json:"id"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Rank     int    `json:"rank"`
	Chatted  bool   `json:"chatted"`
	MsgCount int    `json:"msg_count"`
	LastAt   string `json:"last_at"`
}

type adminOnetoonePerson struct {
	OpenID    int64               `json:"id"`
	UserID    string              `json:"user_id"`
	Name      string              `json:"name"`
	Assigned  int                 `json:"assigned"`
	Chatted   int                 `json:"chatted"`
	MsgCount  int                 `json:"msg_count"`
	Pairs     []adminOnetoonePair `json:"pairs"`
	Unchatted []map[string]any    `json:"unchatted"`
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
	rounds, err := a.db.OnetooneRounds(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("list onetoone rounds failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	round := 0
	if len(rounds) > 0 {
		round = rounds[len(rounds)-1]
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && onetooneRoundInList(parsed, rounds) {
			round = parsed
		}
	}
	rows, err := a.db.ListOnetooneResults(r.Context(), activity.ID, round)
	if err != nil {
		a.logger(r).Error("list onetoone results failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	summary := db.SummarizeOnetooneResults(rows)
	authorIDs := make([]int64, 0, len(summary.People))
	targetSet := map[int64]struct{}{}
	for _, person := range summary.People {
		authorIDs = append(authorIDs, person.UserID)
		for _, row := range person.Partners {
			targetSet[row.TargetID] = struct{}{}
		}
	}
	targetIDs := make([]int64, 0, len(targetSet))
	for id := range targetSet {
		targetIDs = append(targetIDs, id)
	}
	stats, err := a.db.OnetooneMessageStats(r.Context(), authorIDs, targetIDs)
	if err != nil {
		a.logger(r).Error("load onetoone message stats failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	required := map[db.OnetoonePairKey]bool{}
	mutual := map[[2]int64]bool{}
	people := make([]adminOnetoonePerson, 0, len(summary.People))
	totalPairs, totalChatted, totalMessages := 0, 0, 0
	for _, person := range summary.People {
		seenTargets := map[int64]bool{}
		pairs := make([]adminOnetoonePair, 0, len(person.Partners))
		unchatted := make([]map[string]any, 0)
		personMessages := 0
		for _, row := range person.Partners {
			if seenTargets[row.TargetID] {
				continue
			}
			seenTargets[row.TargetID] = true
			key := db.OnetoonePairKey{AuthorID: person.UserID, TargetID: row.TargetID}
			required[key] = true
			stat := stats[key]
			chatted := stat.Count > 0
			lastAt := ""
			if chatted {
				lastAt = stat.LastAt.Format("2006-01-02 15:04:05")
			}
			pairs = append(pairs, adminOnetoonePair{
				OpenID: row.TargetID, UserID: row.TargetLegacyID,
				Name: row.TargetAdminDisplayName(), Rank: row.Rank,
				Chatted: chatted, MsgCount: stat.Count, LastAt: lastAt,
			})
			personMessages += stat.Count
			if !chatted {
				unchatted = append(unchatted, map[string]any{
					"id": row.TargetID, "user_id": row.TargetLegacyID,
					"name": row.TargetAdminDisplayName(), "rank": row.Rank,
				})
			}
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			if pairs[i].Rank != pairs[j].Rank {
				return pairs[i].Rank < pairs[j].Rank
			}
			return pairs[i].OpenID < pairs[j].OpenID
		})
		chatted := len(pairs) - len(unchatted)
		totalPairs += len(pairs)
		totalChatted += chatted
		totalMessages += personMessages
		people = append(people, adminOnetoonePerson{
			OpenID: person.UserID, UserID: person.UserIDLabel, Name: person.Name,
			Assigned: len(pairs), Chatted: chatted, MsgCount: personMessages,
			Pairs: pairs, Unchatted: unchatted,
		})
	}
	for key := range required {
		reverse := db.OnetoonePairKey{AuthorID: key.TargetID, TargetID: key.AuthorID}
		if key.AuthorID == key.TargetID || !required[reverse] {
			continue
		}
		if stats[key].Count > 0 && stats[reverse].Count > 0 {
			low, high := key.AuthorID, key.TargetID
			if low > high {
				low, high = high, low
			}
			mutual[[2]int64{low, high}] = true
		}
	}
	sort.SliceStable(people, func(i, j int) bool {
		if len(people[i].Unchatted) != len(people[j].Unchatted) {
			return len(people[i].Unchatted) > len(people[j].Unchatted)
		}
		leftID, rightID := people[i].UserID, people[j].UserID
		if leftID == "" {
			leftID = "~"
		}
		if rightID == "" {
			rightID = "~"
		}
		if leftID != rightID {
			return leftID < rightID
		}
		return people[i].OpenID < people[j].OpenID
	})
	rate := 0.0
	if totalPairs > 0 {
		rate = math.Round(float64(totalChatted)/float64(totalPairs)*10000) / 10000
	}
	zeroPeople := 0
	donePeople := 0
	for _, person := range people {
		if person.Chatted == 0 {
			zeroPeople++
		}
		if person.Assigned > 0 && person.Chatted == person.Assigned {
			donePeople++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"activity_id": activity.ID,
		"activity_no": activity.ActivityNo,
		"name":        activity.Name,
		"round":       round,
		"rounds":      rounds,
		"summary": map[string]any{
			"people":        len(people),
			"pairs":         totalPairs,
			"chatted_pairs": totalChatted,
			"rate":          rate,
			"zero_people":   zeroPeople,
			"done_people":   donePeople,
			"mutual_pairs":  len(mutual),
			"messages":      totalMessages,
		},
		"people": people,
	})
}
