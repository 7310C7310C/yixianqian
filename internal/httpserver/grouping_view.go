package httpserver

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// GroupCandidateView is the JSON/template projection of a candidate. It never
// contains phone numbers, ID card data or credentials.
type GroupCandidateView struct {
	ID          int64    `json:"id"`
	UserID      string   `json:"user_id"`
	OpenID      string   `json:"openid"`
	Nickname    string   `json:"nickname"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Gender      string   `json:"gender"`
	Birthday    string   `json:"birthday"`
	HeightCM    *int     `json:"height_cm,omitempty"`
	Education   string   `json:"education"`
	NativePlace string   `json:"native_place"`
	City        string   `json:"city"`
	Hobbies     []string `json:"hobbies"`
	HobbiesText string   `json:"hobbies_text"`
	PhotoURL    string   `json:"photo"`
	ProfileURL  string   `json:"profile_url"`
	Attendance  string   `json:"attendance"`
	CheckedIn   bool     `json:"checked_in"`
}

// GroupChoiceSlot is one of the seven ordered selection slots.
type GroupChoiceSlot struct {
	Index    int
	Number   int
	UserID   int64
	Name     string
	Selected bool
}

// GroupSelectPageData drives group_select.tmpl.
type GroupSelectPageData struct {
	Activity    ActivityView
	GroupStatus string
	Saved       bool
	Candidates  []GroupCandidateView
	Slots       []GroupChoiceSlot
	Selected    int
	HasExisting bool
}

// GroupStatusView is the user-facing status JSON.
type GroupStatusView struct {
	GroupStatus string  `json:"group_status"`
	MySelected  bool    `json:"my_selected"`
	MyChoices   []int64 `json:"my_choices"`
	Round       int     `json:"round"`
}

// GroupMemberView is one group member with a public profile link.
type GroupMemberView struct {
	ID          int64  `json:"id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"nickname"`
	Gender      string `json:"gender"`
	PhotoURL    string `json:"photo"`
	ProfileURL  string `json:"profile_url"`
}

// GroupResultView is the owner-only result JSON.
type GroupResultView struct {
	Activity    ActivityView      `json:"activity"`
	GroupStatus string            `json:"group_status"`
	Complete    bool              `json:"complete"`
	Round       int               `json:"round"`
	Rounds      []int             `json:"rounds"`
	MyGroup     int               `json:"my_group"`
	Members     []GroupMemberView `json:"members"`
	TotalGroups int               `json:"total_groups"`
}

func groupPhotoURL(mediaID string, thumb bool) string {
	if strings.TrimSpace(mediaID) == "" {
		return ""
	}
	path := "/cards/photo/" + url.PathEscape(mediaID)
	if thumb {
		path += "?size=thumb"
	}
	return path
}

func groupProfileURL(legacyID string, userID int64) string {
	if strings.TrimSpace(legacyID) != "" {
		return "/u/" + url.PathEscape(legacyID)
	}
	if userID > 0 {
		return "/u/" + strconv.FormatInt(userID, 10)
	}
	return ""
}

func groupBirthdayText(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02")
}

func buildGroupCandidateView(candidate db.GroupCandidate) GroupCandidateView {
	name := strings.TrimSpace(candidate.Name)
	nickname := strings.TrimSpace(candidate.Nickname)
	display := nickname
	if name != "" {
		display = name
	}
	if display == "" {
		display = candidate.LegacyUserID
	}
	return GroupCandidateView{
		ID:          candidate.UserID,
		UserID:      candidate.LegacyUserID,
		OpenID:      candidate.LegacyOpenID,
		Nickname:    nickname,
		Name:        name,
		DisplayName: display,
		Gender:      candidate.Gender,
		Birthday:    groupBirthdayText(candidate.Birthday),
		HeightCM:    candidate.HeightCM,
		Education:   candidate.Education,
		NativePlace: candidate.NativePlace,
		City:        candidate.City,
		Hobbies:     candidate.Hobbies,
		HobbiesText: strings.Join(candidate.Hobbies, "、"),
		PhotoURL:    groupPhotoURL(candidate.PhotoID, true),
		ProfileURL:  groupProfileURL(candidate.LegacyUserID, candidate.UserID),
		Attendance:  candidate.Attendance,
		CheckedIn:   candidate.CheckedIn,
	}
}

func buildGroupCandidateViews(candidates []db.GroupCandidate) []GroupCandidateView {
	out := make([]GroupCandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, buildGroupCandidateView(candidate))
	}
	return out
}

func buildGroupMemberView(member db.GroupMember) GroupMemberView {
	return GroupMemberView{
		ID:          member.UserID,
		UserID:      member.LegacyUserID,
		DisplayName: member.DisplayName(),
		Gender:      member.Gender,
		PhotoURL:    groupPhotoURL(member.PhotoID, true),
		ProfileURL:  groupProfileURL(member.LegacyUserID, member.UserID),
	}
}

func buildGroupMemberViews(members []db.GroupMember) []GroupMemberView {
	out := make([]GroupMemberView, 0, len(members))
	for _, member := range members {
		out = append(out, buildGroupMemberView(member))
	}
	return out
}

func buildGroupChoiceSlots(candidates []GroupCandidateView, choices []int64) []GroupChoiceSlot {
	names := make(map[int64]string, len(candidates))
	for _, candidate := range candidates {
		names[candidate.ID] = candidate.DisplayName
	}
	slots := make([]GroupChoiceSlot, 0, 7)
	for i := 0; i < 7; i++ {
		var id int64
		if i < len(choices) {
			id = choices[i]
		}
		slots = append(slots, GroupChoiceSlot{
			Index:    i,
			Number:   i + 1,
			UserID:   id,
			Name:     names[id],
			Selected: id > 0,
		})
	}
	return slots
}

func countSelectedChoices(choices []int64) int {
	count := 0
	for _, id := range choices {
		if id > 0 {
			count++
		}
	}
	return count
}

func buildGroupResultView(result db.UserGroupResult) GroupResultView {
	return GroupResultView{
		Activity: ActivityView{
			ID:          result.ActivityID,
			ActivityNo:  result.ActivityNo,
			RecordID:    result.ActivityNo,
			Name:        result.ActivityName,
			GroupStatus: result.GroupStatus,
		},
		GroupStatus: result.GroupStatus,
		Complete:    result.GroupStatus == "已完成",
		Round:       result.Round,
		Rounds:      result.Rounds,
		MyGroup:     result.MyGroup,
		Members:     buildGroupMemberViews(result.Members),
		TotalGroups: result.TotalGroups,
	}
}

// AdminGroupView is one full group in the management API.
type AdminGroupView struct {
	GroupID int               `json:"group_id"`
	Members []GroupMemberView `json:"members"`
}

// AdminGroupResultView is the management result payload.
type AdminGroupResultView struct {
	ActivityID  int64            `json:"activity_id"`
	ActivityNo  string           `json:"activity_no"`
	Name        string           `json:"name"`
	GroupStatus string           `json:"group_status"`
	Round       int              `json:"round"`
	Rounds      []int            `json:"rounds"`
	Groups      []AdminGroupView `json:"groups"`
}

func buildAdminGroupResultView(result db.AdminGroupResult) AdminGroupResultView {
	groups := make([]AdminGroupView, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, AdminGroupView{
			GroupID: group.GroupID,
			Members: buildGroupMemberViews(group.Members),
		})
	}
	return AdminGroupResultView{
		ActivityID:  result.ActivityID,
		ActivityNo:  result.ActivityNo,
		Name:        result.ActivityName,
		GroupStatus: result.GroupStatus,
		Round:       result.Round,
		Rounds:      result.Rounds,
		Groups:      groups,
	}
}

// GroupPreviewView is the admin preview payload. Field names mirror the legacy
// management endpoint so clients can migrate without learning a new shape.
type GroupPreviewView struct {
	ActivityID           int64    `json:"activity_id"`
	ActivityNo           string   `json:"activity_no"`
	Name                 string   `json:"name"`
	GroupStatus          string   `json:"group_status"`
	Round                int      `json:"round"`
	MalesPerGroup        int      `json:"males_per_group"`
	FemalesPerGroup      int      `json:"females_per_group"`
	MaleCount            int      `json:"male_count"`
	FemaleCount          int      `json:"female_count"`
	CheckedInMaleCount   int      `json:"checked_in_male_count"`
	CheckedInFemaleCount int      `json:"checked_in_female_count"`
	ParticipantCount     int      `json:"participant_count"`
	SkippedNoGender      int      `json:"skipped_no_gender"`
	ExpectedGroups       int      `json:"expected_groups"`
	ExistingRounds       []int    `json:"existing_rounds"`
	ExistingRoundResults int      `json:"existing_round_results"`
	HistoricalResults    int      `json:"historical_results"`
	CanExecute           bool     `json:"can_execute"`
	Blockers             []string `json:"blockers"`
}

func buildGroupPreviewView(preview db.GroupPreview) GroupPreviewView {
	return GroupPreviewView{
		ActivityID:           preview.ActivityID,
		ActivityNo:           preview.ActivityNo,
		Name:                 preview.ActivityName,
		GroupStatus:          preview.GroupStatus,
		Round:                preview.Round,
		MalesPerGroup:        preview.MalesPerGroup,
		FemalesPerGroup:      preview.FemalesPerGroup,
		MaleCount:            preview.MaleCount,
		FemaleCount:          preview.FemaleCount,
		CheckedInMaleCount:   preview.CheckedInMaleCount,
		CheckedInFemaleCount: preview.CheckedInFemaleCount,
		ParticipantCount:     preview.ParticipantCount,
		SkippedNoGender:      preview.SkippedNoGender,
		ExpectedGroups:       preview.ExpectedGroups,
		ExistingRounds:       preview.ExistingRounds,
		ExistingRoundResults: preview.ExistingRoundResults,
		HistoricalResults:    preview.HistoricalResults,
		CanExecute:           preview.CanExecute,
		Blockers:             preview.Blockers,
	}
}

// GroupRunView is the run response payload.
type GroupRunView struct {
	ActivityID       int64            `json:"activity_id"`
	ActivityNo       string           `json:"activity_no"`
	Name             string           `json:"name"`
	Round            int              `json:"round"`
	MalesPerGroup    int              `json:"males_per_group"`
	FemalesPerGroup  int              `json:"females_per_group"`
	GroupCount       int              `json:"group_count"`
	ParticipantCount int              `json:"participant_count"`
	Groups           []AdminGroupView `json:"groups"`
}

func buildGroupRunView(result db.GroupRunResult) GroupRunView {
	groups := make([]AdminGroupView, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, AdminGroupView{
			GroupID: group.GroupID,
			Members: buildGroupMemberViews(group.Members),
		})
	}
	return GroupRunView{
		ActivityID:       result.ActivityID,
		ActivityNo:       result.ActivityNo,
		Name:             result.ActivityName,
		Round:            result.Round,
		MalesPerGroup:    result.MalesPerGroup,
		FemalesPerGroup:  result.FemalesPerGroup,
		GroupCount:       result.GroupCount,
		ParticipantCount: result.ParticipantCount,
		Groups:           groups,
	}
}
