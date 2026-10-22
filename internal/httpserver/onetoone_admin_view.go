package httpserver

import (
	"context"
	"math"
	"sort"
	"strconv"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// onetoonePreviewView is shared by the admin JSON preview and the matching
// console. Keys mirror the legacy management endpoint.
type onetoonePreviewView struct {
	ActivityID               int64    `json:"activity_id"`
	ActivityNo               string   `json:"activity_no"`
	Name                     string   `json:"name"`
	OnetooneStatus           string   `json:"onetoone_status"`
	Round                    int      `json:"round"`
	ParticipantCount         int      `json:"participant_count"`
	MaleCount                int      `json:"male_count"`
	FemaleCount              int      `json:"female_count"`
	FemaleChats              *int     `json:"female_chats"`
	TotalPairsExpected       int      `json:"total_pairs_expected"`
	ExistingResults          int      `json:"existing_results"`
	ExistingRoundResults     int      `json:"existing_round_results"`
	ExistingRounds           []int    `json:"existing_rounds"`
	HistoricalResultsToClear int      `json:"historical_results_to_clear"`
	HistoricalRoundsToClear  []int    `json:"historical_rounds_to_clear"`
	RoundsPreserved          []int    `json:"rounds_preserved"`
	ResultsPreserved         int      `json:"results_preserved"`
	CanExecute               bool     `json:"can_execute"`
	Blockers                 []string `json:"blockers"`
	SigninEnabled            bool     `json:"signin_enabled"`
	RegisteredMaleCount      int      `json:"registered_male_count"`
	RegisteredFemaleCount    int      `json:"registered_female_count"`
}

// onetoonePreviewView assembles the preview payload from the same database
// reads the API used before the console existed.
func (a *App) buildOnetoonePreviewView(ctx context.Context, activity db.Activity,
	round int, femaleChats *int) (onetoonePreviewView, error) {
	participants, err := a.db.ListOnetooneParticipants(ctx, activity.ID)
	if err != nil {
		return onetoonePreviewView{}, err
	}
	males, females := onetooneParticipantCounts(participants, activity.SigninEnabled)
	registeredMales, registeredFemales := onetooneParticipantCounts(participants, false)
	rounds, err := a.db.OnetooneRounds(ctx, activity.ID)
	if err != nil {
		return onetoonePreviewView{}, err
	}
	roundCounts, err := a.db.OnetooneRoundCounts(ctx, activity.ID)
	if err != nil {
		return onetoonePreviewView{}, err
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
		if activity.SigninEnabled && registeredMales+registeredFemales > 0 {
			blockers = append(blockers,
				"开启签到后只有已签到者参与匹配，目前没有已签到的有效参与者")
		} else {
			blockers = append(blockers, "没有已报名且性别有效的参与者")
		}
	}
	if existingRound > 0 {
		blockers = append(blockers, "第"+strconv.Itoa(round)+"轮已生成一对一结果，请勿重复执行")
	}
	if femaleChats == nil {
		blockers = append(blockers, "请指定女生场次（每位女生接待几位男生）")
	} else if *femaleChats < 1 {
		blockers = append(blockers, "女生场次必须是 ≥1 的整数")
	} else if *femaleChats > males && males > 0 {
		blockers = append(blockers, "女生场次 "+strconv.Itoa(*femaleChats)+
			" 超过男生人数 "+strconv.Itoa(males)+"（每个女生最多和每位男生各聊一次）")
	}
	expected := 0
	if femaleChats != nil && *femaleChats >= 1 {
		expected = 2 * females * *femaleChats
	}
	return onetoonePreviewView{
		ActivityID:               activity.ID,
		ActivityNo:               activity.ActivityNo,
		Name:                     activity.Name,
		OnetooneStatus:           activity.OnetooneStatus,
		Round:                    round,
		ParticipantCount:         males + females,
		MaleCount:                males,
		FemaleCount:              females,
		FemaleChats:              femaleChats,
		TotalPairsExpected:       expected,
		ExistingResults:          totalResults,
		ExistingRoundResults:     existingRound,
		ExistingRounds:           rounds,
		HistoricalResultsToClear: totalResults,
		HistoricalRoundsToClear:  rounds,
		RoundsPreserved:          rounds,
		ResultsPreserved:         totalResults,
		CanExecute:               len(blockers) == 0,
		Blockers:                 blockers,
		SigninEnabled:            activity.SigninEnabled,
		RegisteredMaleCount:      registeredMales,
		RegisteredFemaleCount:    registeredFemales,
	}, nil
}

type onetooneResultPartnerView struct {
	ID       int64  `json:"id"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Rank     int    `json:"rank"`
}

type onetoonePersonView struct {
	ID       int64                       `json:"id"`
	UserID   string                      `json:"user_id"`
	Name     string                      `json:"name"`
	Gender   string                      `json:"gender"`
	Partners []onetooneResultPartnerView `json:"partners"`
}

type onetooneResultView struct {
	ActivityID      int64                `json:"activity_id"`
	ActivityNo      string               `json:"activity_no"`
	Name            string               `json:"name"`
	OnetooneStatus  string               `json:"onetoone_status"`
	Round           int                  `json:"round"`
	Rounds          []int                `json:"rounds"`
	ResultCount     int                  `json:"result_count"`
	MatchedCount    int                  `json:"matched_count"`
	People          []onetoonePersonView `json:"people"`
	RoundHasResults bool                 `json:"round_has_results"`
}

// buildOnetooneResultView resolves the requested (or latest) round and groups
// the flat result rows by person. The JSON keys are unchanged.
func (a *App) buildOnetooneResultView(ctx context.Context, activity db.Activity,
	requested int) (onetooneResultView, error) {
	rounds, err := a.db.OnetooneRounds(ctx, activity.ID)
	if err != nil {
		return onetooneResultView{}, err
	}
	round := 0
	if len(rounds) > 0 {
		round = rounds[len(rounds)-1]
	}
	if requested >= 1 {
		round = requested
	}
	rows, err := a.db.ListOnetooneResults(ctx, activity.ID, round)
	if err != nil {
		return onetooneResultView{}, err
	}
	summary := db.SummarizeOnetooneResults(rows)
	people := make([]onetoonePersonView, 0, len(summary.People))
	for _, person := range summary.People {
		partners := make([]onetooneResultPartnerView, 0, len(person.Partners))
		for _, row := range person.Partners {
			partners = append(partners, onetooneResultPartnerView{
				ID:       row.TargetID,
				UserID:   row.TargetLegacyID,
				Nickname: row.TargetAdminDisplayName(),
				Rank:     row.Rank,
			})
		}
		people = append(people, onetoonePersonView{
			ID:       person.UserID,
			UserID:   person.UserIDLabel,
			Name:     person.Name,
			Gender:   person.Gender,
			Partners: partners,
		})
	}
	sort.SliceStable(people, func(i, j int) bool {
		if people[i].UserID != people[j].UserID {
			return people[i].UserID < people[j].UserID
		}
		return people[i].ID < people[j].ID
	})
	return onetooneResultView{
		ActivityID:      activity.ID,
		ActivityNo:      activity.ActivityNo,
		Name:            activity.Name,
		OnetooneStatus:  activity.OnetooneStatus,
		Round:           round,
		Rounds:          rounds,
		ResultCount:     summary.ResultCount,
		MatchedCount:    summary.MatchedCount,
		People:          people,
		RoundHasResults: roundHasResults(round, rounds),
	}, nil
}

type onetooneChatSummaryView struct {
	People       int     `json:"people"`
	Pairs        int     `json:"pairs"`
	ChattedPairs int     `json:"chatted_pairs"`
	Rate         float64 `json:"rate"`
	ZeroPeople   int     `json:"zero_people"`
	DonePeople   int     `json:"done_people"`
	MutualPairs  int     `json:"mutual_pairs"`
	Messages     int     `json:"messages"`
}

type onetooneChatOverviewView struct {
	ActivityID int64                   `json:"activity_id"`
	ActivityNo string                  `json:"activity_no"`
	Name       string                  `json:"name"`
	Round      int                     `json:"round"`
	Rounds     []int                   `json:"rounds"`
	Summary    onetooneChatSummaryView `json:"summary"`
	People     []adminOnetoonePerson   `json:"people"`
	// RatePercent is a template helper; it is not part of the JSON payload.
	RatePercent int `json:"-"`
}

// buildOnetooneChatOverviewView assembles the chat overview for one round. It
// is shared by the API and the server-rendered console.
func (a *App) buildOnetooneChatOverviewView(ctx context.Context, activity db.Activity,
	requested int) (onetooneChatOverviewView, error) {
	rounds, err := a.db.OnetooneRounds(ctx, activity.ID)
	if err != nil {
		return onetooneChatOverviewView{}, err
	}
	round := 0
	if len(rounds) > 0 {
		round = rounds[len(rounds)-1]
	}
	if requested >= 1 && onetooneRoundInList(requested, rounds) {
		round = requested
	}
	rows, err := a.db.ListOnetooneResults(ctx, activity.ID, round)
	if err != nil {
		return onetooneChatOverviewView{}, err
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
	stats, err := a.db.OnetooneMessageStats(ctx, authorIDs, targetIDs)
	if err != nil {
		return onetooneChatOverviewView{}, err
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
	return onetooneChatOverviewView{
		ActivityID: activity.ID,
		ActivityNo: activity.ActivityNo,
		Name:       activity.Name,
		Round:      round,
		Rounds:     rounds,
		Summary: onetooneChatSummaryView{
			People:       len(people),
			Pairs:        totalPairs,
			ChattedPairs: totalChatted,
			Rate:         rate,
			ZeroPeople:   zeroPeople,
			DonePeople:   donePeople,
			MutualPairs:  len(mutual),
			Messages:     totalMessages,
		},
		People:      people,
		RatePercent: int(math.Round(rate * 100)),
	}, nil
}
