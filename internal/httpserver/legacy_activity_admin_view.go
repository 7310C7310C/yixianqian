package httpserver

import (
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The legacy activity console (bundled v1 SPA) reads a fixed JSON contract:
// snake_case keys, "是"/"否" flags, arrays that are always [] and nullable
// rounds. These builders are the single place that contract lives; do not
// reuse the v2 view structs where their keys differ.

var legacyActivityStatuses = []string{
	"未开始报名", "报名中", "已满员", "已结束", "已取消",
}

func legacyActivityRecordID(activity db.Activity) string {
	if activity.LegacyRecordID != nil && strings.TrimSpace(*activity.LegacyRecordID) != "" {
		return strings.TrimSpace(*activity.LegacyRecordID)
	}
	return activity.ActivityNo
}

func legacyLatestRound(rounds []int) any {
	if len(rounds) == 0 {
		return nil
	}
	return rounds[len(rounds)-1]
}

func legacyRounds(rounds []int) []int {
	if rounds == nil {
		return []int{}
	}
	return rounds
}

func legacyActivityModeFields(activity db.Activity) (string, bool) {
	mode := db.LegacyActivityMode(activity.GroupEnabled, activity.OnetooneEnabled)
	return mode, mode == "conflict"
}

func legacyUnsubmittedCount(counts db.AdminActivityCounts) int {
	value := counts.SignupCount - counts.SubmittedCount
	if value < 0 {
		return 0
	}
	return value
}

// buildLegacyActivityListItem mirrors GET /api/admin/group/activities. The
// numeric id is additive for the "edit activity" entry; v1 only had the
// activity number.
func buildLegacyActivityListItem(activity db.Activity, counts db.AdminActivityCounts) map[string]any {
	mode, conflict := legacyActivityModeFields(activity)
	rounds := legacyRounds(counts.GroupRounds)
	return map[string]any{
		"record_id":                legacyActivityRecordID(activity),
		"id":                       activity.ID,
		"activity_id":              activity.ActivityNo,
		"name":                     activity.Name,
		"activity_status":          activity.Status,
		"group_status":             activity.GroupStatus,
		"group_flag":               legacyYesNo(activity.GroupEnabled),
		"onetoone_status":          activity.OnetooneStatus,
		"onetoone_flag":            legacyYesNo(activity.OnetooneEnabled),
		"onetoone_submitted_count": counts.SubmittedCount,
		"mode":                     mode,
		"conflict":                 conflict,
		"males_per_group":          activity.MalePerGroup,
		"females_per_group":        activity.FemalePerGroup,
		"signup_count":             counts.SignupCount,
		"submitted_count":          counts.SubmittedCount,
		"has_results":              len(rounds) > 0,
		"latest_round":             legacyLatestRound(rounds),
		"available_rounds":         rounds,
	}
}

// buildLegacyActivityStatus mirrors GET /api/admin/group/status. signin_code
// is blank unless the caller holds activity.manage; the matching-only role can
// see the check-in screen but not the code.
func buildLegacyActivityStatus(activity db.Activity, counts db.AdminActivityCounts,
	includeSigninCode bool) map[string]any {
	mode, conflict := legacyActivityModeFields(activity)
	rounds := legacyRounds(counts.GroupRounds)
	otoRounds := legacyRounds(counts.OnetooneRounds)
	unsubmitted := legacyUnsubmittedCount(counts)
	signinCode := ""
	if includeSigninCode {
		signinCode = activity.SigninCode
	}
	return map[string]any{
		"activity_id":                        activity.ActivityNo,
		"id":                                 activity.ID,
		"name":                               activity.Name,
		"group_status":                       activity.GroupStatus,
		"group_flag":                         legacyYesNo(activity.GroupEnabled),
		"onetoone_status":                    activity.OnetooneStatus,
		"onetoone_flag":                      legacyYesNo(activity.OnetooneEnabled),
		"signin_flag":                        legacyYesNo(activity.SigninEnabled),
		"signin_code":                        signinCode,
		"activity_status":                    activity.Status,
		"activity_status_options":            legacyActivityStatuses,
		"signup_count":                       counts.SignupCount,
		"submitted_count":                    counts.SubmittedCount,
		"unsubmitted_count":                  unsubmitted,
		"unsubmitted_on_site_count":          counts.UnsubmittedCheckedIn,
		"male_count":                         counts.MaleCount,
		"female_count":                       counts.FemaleCount,
		"males_per_group":                    activity.MalePerGroup,
		"females_per_group":                  activity.FemalePerGroup,
		"rounds":                             rounds,
		"latest_round":                       legacyLatestRound(rounds),
		"onetoone_submitted_count":           counts.SubmittedCount,
		"onetoone_unsubmitted_count":         unsubmitted,
		"onetoone_unsubmitted_on_site_count": counts.UnsubmittedCheckedIn,
		"onetoone_result_count":              counts.OnetooneResultCount,
		"onetoone_matched_count":             counts.OnetooneMatchedCount,
		"onetoone_rounds":                    otoRounds,
		"onetoone_latest_round":              legacyLatestRound(otoRounds),
		"mode":                               mode,
		"conflict":                           conflict,
		"active_job":                         nil,
	}
}

func legacyRosterEntryMap(entry db.MatchingRosterEntry) map[string]any {
	return map[string]any{
		"open_id":    legacyOpenID("", entry.UserID, entry.ID),
		"user_id":    entry.UserID,
		"gender":     entry.Gender,
		"nickname":   entry.Nickname,
		"real_name":  entry.Name,
		"attendance": entry.Attendance,
		"checked_in": entry.CheckedIn,
	}
}

// buildLegacyUnsubmitted mirrors GET /api/admin/{group,onetoone}/unsubmitted.
// Both modes share group_selections in v2, so both endpoints return the same
// split; v1 had two tables but the console only reads the fields below.
func buildLegacyUnsubmitted(activity db.Activity, roster db.MatchingRoster) map[string]any {
	unsubmitted := make([]map[string]any, 0, roster.Unsubmitted)
	submitted := make([]map[string]any, 0, roster.Submitted)
	for _, entry := range roster.Entries {
		item := legacyRosterEntryMap(entry)
		if entry.Submitted {
			submitted = append(submitted, item)
		} else {
			unsubmitted = append(unsubmitted, item)
		}
	}
	return map[string]any{
		"activity_id":            activity.ActivityNo,
		"name":                   activity.Name,
		"total":                  len(unsubmitted),
		"unsubmitted":            unsubmitted,
		"submitted":              submitted,
		"all_submitted":          len(unsubmitted) == 0,
		"signin_enabled":         activity.SigninEnabled,
		"unsubmitted_checked_in": roster.UnsubmittedCheckedIn,
	}
}

// buildLegacySigninRoster mirrors GET /api/admin/activity/signin. Phone and
// other private fields never leave the database.
func buildLegacySigninRoster(activity db.Activity,
	entries []db.AdminActivityRosterEntry) map[string]any {
	roster := make([]map[string]any, 0, len(entries))
	checked := 0
	for _, entry := range entries {
		state := string(entry.Attendance)
		checkedIn := db.IsCheckedIn(state)
		if checkedIn {
			checked++
		}
		nickname := entry.Nickname
		if strings.TrimSpace(nickname) == "" {
			nickname = entry.Name
		}
		roster = append(roster, map[string]any{
			"open_id":    legacyOpenID("", entry.LegacyUserID, entry.UserID),
			"nickname":   nickname,
			"user_id":    entry.LegacyUserID,
			"gender":     entry.Gender,
			"real_name":  entry.Name,
			"state":      state,
			"checked_in": checkedIn,
		})
	}
	return map[string]any{
		"ok":             true,
		"activity_id":    activity.ActivityNo,
		"name":           activity.Name,
		"signin_enabled": activity.SigninEnabled,
		"stats": map[string]any{
			"checked_in":     checked,
			"signed_up":      len(roster),
			"not_checked_in": len(roster) - checked,
		},
		"roster": roster,
	}
}

func legacyGroupMemberMap(member db.GroupMember) map[string]any {
	nickname := member.Nickname
	if strings.TrimSpace(nickname) == "" {
		nickname = member.UserName
	}
	return map[string]any{
		"open_id":      legacyOpenID(member.LegacyOpenID, member.LegacyUserID, member.UserID),
		"display_name": member.DisplayName(),
		"nickname":     nickname,
		"user_id":      member.LegacyUserID,
		"gender":       member.Gender,
	}
}

func legacyGroupMembersMap(members []db.GroupMember) []map[string]any {
	out := make([]map[string]any, 0, len(members))
	for _, member := range members {
		out = append(out, legacyGroupMemberMap(member))
	}
	return out
}

// buildLegacyGroupResult mirrors GET /api/admin/group/result.
func buildLegacyGroupResult(result db.AdminGroupResult) map[string]any {
	groups := make([]map[string]any, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, map[string]any{
			"group_id": group.GroupID,
			"members":  legacyGroupMembersMap(group.Members),
		})
	}
	return map[string]any{
		"activity_id": result.ActivityNo,
		"id":          result.ActivityID,
		"name":        result.ActivityName,
		"round":       result.Round,
		"rounds":      legacyRounds(result.Rounds),
		"groups":      groups,
	}
}

// buildLegacyGroupRunResult is the synchronous stand-in for the v1 job result.
// The SPA adapter feeds it into the same handler the job poller used.
func buildLegacyGroupRunResult(result db.GroupRunResult) map[string]any {
	groups := make([]map[string]any, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, map[string]any{
			"group_id": group.GroupID,
			"members":  legacyGroupMembersMap(group.Members),
		})
	}
	return map[string]any{
		"code":        "OK",
		"activity_id": result.ActivityNo,
		"round":       result.Round,
		"group_count": result.GroupCount,
		"groups":      groups,
	}
}

// buildLegacyOnetooneResult mirrors GET /api/admin/onetoone/result.
func buildLegacyOnetooneResult(view onetooneResultView) map[string]any {
	people := make([]map[string]any, 0, len(view.People))
	for _, person := range view.People {
		partners := make([]map[string]any, 0, len(person.Partners))
		for _, partner := range person.Partners {
			partners = append(partners, map[string]any{
				"open_id":  partner.OpenID,
				"nickname": partner.Nickname,
				"user_id":  partner.UserID,
				"rank":     partner.Rank,
			})
		}
		people = append(people, map[string]any{
			"open_id":  person.OpenID,
			"nickname": person.Nickname,
			"user_id":  person.UserID,
			"gender":   person.Gender,
			"partners": partners,
		})
	}
	return map[string]any{
		"activity_id":     view.ActivityNo,
		"id":              view.ActivityID,
		"name":            view.Name,
		"onetoone_status": view.OnetooneStatus,
		"round":           view.Round,
		"rounds":          legacyRounds(view.Rounds),
		"result_count":    view.ResultCount,
		"matched_count":   view.MatchedCount,
		"people":          people,
	}
}

// buildLegacyChatOverview mirrors GET /api/admin/onetoone/chat-overview. The
// typed view already carries open_id (added for the v1 console) and the same
// nested keys.
func buildLegacyChatOverview(view onetooneChatOverviewView) map[string]any {
	return map[string]any{
		"activity_id": view.ActivityNo,
		"id":          view.ActivityID,
		"name":        view.Name,
		"round":       view.Round,
		"rounds":      legacyRounds(view.Rounds),
		"summary":     view.Summary,
		"people":      view.People,
	}
}

// buildLegacyGroupPreview mirrors POST /api/admin/group/preview.
func buildLegacyGroupPreview(preview db.GroupPreview) map[string]any {
	return map[string]any{
		"activity_id":             preview.ActivityNo,
		"id":                      preview.ActivityID,
		"name":                    preview.ActivityName,
		"group_status":            preview.GroupStatus,
		"round":                   preview.Round,
		"males_per_group":         preview.MalesPerGroup,
		"females_per_group":       preview.FemalesPerGroup,
		"male_count":              preview.MaleCount,
		"female_count":            preview.FemaleCount,
		"checked_in_male_count":   preview.CheckedInMaleCount,
		"checked_in_female_count": preview.CheckedInFemaleCount,
		"participant_count":       preview.ParticipantCount,
		"skipped_no_gender":       preview.SkippedNoGender,
		"expected_groups":         preview.ExpectedGroups,
		"existing_rounds":         legacyRounds(preview.ExistingRounds),
		"existing_round_results":  preview.ExistingRoundResults,
		"historical_results":      preview.HistoricalResults,
		"can_execute":             preview.CanExecute,
		"blockers":                nonNilStrings(preview.Blockers),
	}
}

// buildLegacyOnetoonePreview mirrors POST /api/admin/onetoone/preview.
func buildLegacyOnetoonePreview(view onetoonePreviewView) map[string]any {
	return map[string]any{
		"activity_id":                 view.ActivityNo,
		"id":                          view.ActivityID,
		"name":                        view.Name,
		"onetoone_status":             view.OnetooneStatus,
		"round":                       view.Round,
		"participant_count":           view.ParticipantCount,
		"male_count":                  view.MaleCount,
		"female_count":                view.FemaleCount,
		"female_chats":                view.FemaleChats,
		"total_pairs_expected":        view.TotalPairsExpected,
		"existing_results":            view.ExistingResults,
		"existing_round_results":      view.ExistingRoundResults,
		"existing_rounds":             legacyRounds(view.ExistingRounds),
		"historical_results_to_clear": view.HistoricalResultsToClear,
		"historical_rounds_to_clear":  legacyRounds(view.HistoricalRoundsToClear),
		"rounds_preserved":            legacyRounds(view.RoundsPreserved),
		"results_preserved":           view.ResultsPreserved,
		"can_execute":                 view.CanExecute,
		"blockers":                    nonNilStrings(view.Blockers),
		"signin_enabled":              view.SigninEnabled,
		"registered_male_count":       view.RegisteredMaleCount,
		"registered_female_count":     view.RegisteredFemaleCount,
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
