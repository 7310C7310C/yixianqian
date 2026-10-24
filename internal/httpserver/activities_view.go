package httpserver

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// activityTimeZone keeps the legacy display in China Standard Time regardless
// of the server's TZ setting.
var activityTimeZone = time.FixedZone("CST", 8*60*60)

// ActivityView is the safe user-facing projection of an activity. It never
// carries signin_code.
type ActivityView struct {
	ID            int64  `json:"id"`
	ActivityNo    string `json:"activity_id"`
	RecordID      string `json:"record_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Location      string `json:"location"`
	Condition     string `json:"condition"`
	MaxSignup     int    `json:"max_signup"`
	CurrentSignup int    `json:"current_signup"`
	Fee           string `json:"fee"`
	Food          string `json:"food"`
	Status        string `json:"status"`

	PosterURL       string `json:"poster"`
	CapacityLabel   string `json:"-"`
	FeeLabel        string `json:"-"`
	StatusClass     string `json:"-"`
	PublishAtText   string `json:"publish_at,omitempty"`
	StartAtText     string `json:"start_time"`
	EndAtText       string `json:"end_time"`
	GroupStatus     string `json:"group_status"`
	GroupEnabled    bool   `json:"group_enabled"`
	MalePerGroup    int    `json:"male_per_group"`
	FemalePerGroup  int    `json:"female_per_group"`
	OnetooneStatus  string `json:"onetoone_status"`
	OnetooneEnabled bool   `json:"onetoone_enabled"`
	SigninEnabled   bool   `json:"signin_enabled"`

	MySignup     bool   `json:"my_signup"`
	SignupStatus string `json:"signup_status,omitempty"`
	Attendance   string `json:"my_attendance"`
	CheckedIn    bool   `json:"checked_in"`
	CanSignup    bool   `json:"can_signup"`
	CanCancel    bool   `json:"can_cancel"`
}

// ActivitiesPageData is the payload for GET /activities.
type ActivitiesPageData struct {
	OpenActivities  []ActivityView
	OtherActivities []ActivityView
}

// ActivityDetailPageData is the payload for GET /activities/{id}.
type ActivityDetailPageData struct {
	Activity      ActivityView
	RosterVisible bool
	RosterCount   int
	PaymentQR     bool
}

// MyActivitiesPageData is the payload for GET /my_activities.
type MyActivitiesPageData struct {
	Activities []ActivityView
}

// ActivitySignupView is the privacy-safe roster row. It intentionally has no
// phone, real name or open id.
type ActivitySignupView struct {
	UserID     string `json:"user_id"`
	Nickname   string `json:"nickname"`
	Gender     string `json:"gender"`
	Initial    string `json:"-"`
	ProfileURL string `json:"-"`
}

// ActivitySignupsPageData is the payload for GET /activities/{id}/signups.
type ActivitySignupsPageData struct {
	Activity ActivityView
	Signups  []ActivitySignupView
	Visible  bool
	Count    int
}

func buildActivityView(a db.Activity) ActivityView {
	poster := ""
	if a.PosterMediaID != nil && strings.TrimSpace(*a.PosterMediaID) != "" {
		poster = "/activities/" + url.PathEscape(a.ActivityNo) + "/poster"
	}
	return ActivityView{
		ID:              a.ID,
		ActivityNo:      a.ActivityNo,
		RecordID:        a.ActivityNo,
		Name:            a.Name,
		Description:     a.Description,
		Location:        a.Location,
		Condition:       a.Condition,
		MaxSignup:       a.MaxSignup,
		CurrentSignup:   a.CurrentSignup,
		Fee:             a.Fee,
		Food:            a.Food,
		Status:          a.Status,
		PosterURL:       poster,
		CapacityLabel:   activityCapacityLabel(a.MaxSignup),
		FeeLabel:        activityFeeLabel(a.Fee),
		StatusClass:     activityStatusClass(a.Status),
		PublishAtText:   activityTimeText(a.PublishAt),
		StartAtText:     activityTimeText(a.StartAt),
		EndAtText:       activityTimeText(a.EndAt),
		GroupStatus:     a.GroupStatus,
		GroupEnabled:    a.GroupEnabled,
		MalePerGroup:    a.MalePerGroup,
		FemalePerGroup:  a.FemalePerGroup,
		OnetooneStatus:  a.OnetooneStatus,
		OnetooneEnabled: a.OnetooneEnabled,
		SigninEnabled:   a.SigninEnabled,
		CanSignup: a.Status == db.ActivityStatusOpen &&
			(a.MaxSignup <= 0 || a.CurrentSignup < a.MaxSignup),
	}
}

func applySignupToView(v *ActivityView, status string, attendance db.Attendance) {
	cancelled := status == db.SignupStatusCancelled
	v.MySignup = status == db.SignupStatusSigned
	v.SignupStatus = status
	v.Attendance = string(attendance)
	v.CheckedIn = attendance.CheckedIn()
	v.CanSignup = !v.MySignup && v.Status == db.ActivityStatusOpen &&
		(v.MaxSignup <= 0 || v.CurrentSignup < v.MaxSignup)
	v.CanCancel = v.MySignup &&
		(v.Status == db.ActivityStatusOpen || v.Status == db.ActivityStatusFull) &&
		!cancelled
}

func activityStatusClass(status string) string {
	switch status {
	case db.ActivityStatusOpen:
		return "is-open"
	case db.ActivityStatusFull:
		return "is-full"
	case db.ActivityStatusFinished, db.ActivityStatusCancelled:
		return "is-ended"
	case db.ActivityStatusNotStarted:
		return "is-soon"
	default:
		return "is-other"
	}
}

func activityCapacityLabel(max int) string {
	if max <= 0 {
		return "不限"
	}
	return strconv.Itoa(max)
}

func activityFeeLabel(fee string) string {
	fee = strings.TrimSpace(fee)
	if fee == "" {
		return "免费"
	}
	if strings.ContainsAny(fee, "元¥￥") || strings.Contains(fee, "免费") || strings.Contains(fee, "免费") {
		return fee
	}
	if _, err := strconv.Atoi(fee); err == nil {
		return fee + " 元"
	}
	return fee
}

func activityTimeText(t *time.Time) string {
	if t == nil {
		return "待定"
	}
	return t.In(activityTimeZone).Format("2006-01-02 15:04")
}

// activityBackURL keeps subpage back-links on the canonical activity path.
func activityBackURL(activityNo string) string {
	return "/activities/" + url.PathEscape(activityNo)
}

func buildActivitySignupViews(rows []db.ActivitySignup) []ActivitySignupView {
	out := make([]ActivitySignupView, 0, len(rows))
	for _, row := range rows {
		nickname := strings.TrimSpace(row.Nickname)
		if nickname == "" {
			nickname = "未命名"
		}
		initial := "?"
		if r, _ := utf8.DecodeRuneInString(nickname); r != utf8.RuneError {
			initial = string(r)
		}
		profile := ""
		if row.LegacyUserID != "" {
			profile = "/u/" + url.PathEscape(row.LegacyUserID)
		}
		out = append(out, ActivitySignupView{
			UserID:     row.LegacyUserID,
			Nickname:   nickname,
			Gender:     row.Gender,
			Initial:    initial,
			ProfileURL: profile,
		})
	}
	return out
}

func activityGateMessage(kind, status string) string {
	if kind == "observer" || status == "observer" {
		return "观察员账号仅可浏览，不能报名或取消报名。"
	}
	switch status {
	case "matched":
		return "你目前为已脱单状态，请先在「我的」页恢复单身。"
	case "pending":
		return "资料审核中，通过后即可使用一线牵。"
	case "rejected":
		return "很抱歉，你的资料未通过审核，如有疑问请联络管理员。"
	case "left":
		return "你已暂时退出相亲市场，如需恢复请联络管理员。"
	case "disabled":
		return "账号已停用，请联络管理员。"
	default:
		return "账号目前无法使用，请联络管理员。"
	}
}

func activityViewGateCode(kind, status string) string {
	if kind == "observer" || status == "observer" {
		return "observer_readonly"
	}
	switch status {
	case "matched":
		return "account_matched"
	case "pending":
		return "account_pending"
	case "rejected":
		return "account_rejected"
	case "left":
		return "account_left"
	case "disabled":
		return "account_disabled"
	default:
		return "account_unavailable"
	}
}

// activityActionError maps database errors to HTTP status, stable API code and
// a user-safe message. action is shown only for the state-machine message.
func activityActionError(err error, action string) (int, string, string) {
	var notOpen *db.ActivityStatusError
	switch {
	case err == nil:
		return 200, "", ""
	case errors.Is(err, db.ErrActivityNotFound):
		return 404, "activity_not_found", "活动不存在。"
	case errors.As(err, &notOpen):
		return 409, "activity_not_open",
			fmt.Sprintf("活动目前状态为「%s」，无法%s。", notOpen.Status, action)
	case errors.Is(err, db.ErrActivityFull):
		return 409, "activity_full", "报名人数已满。"
	case errors.Is(err, db.ErrAlreadySignedUp):
		return 409, "already_signed_up", "你已经报名了这个活动。"
	case errors.Is(err, db.ErrAccountNotActive):
		return 403, "account_gated", "账号目前无法报名，请联络管理员。"
	case errors.Is(err, db.ErrNotSignedUp):
		return 409, "not_signed_up", "你没有报名这个活动。"
	case errors.Is(err, db.ErrAlreadyCancelled):
		return 409, "already_cancelled", "你已经取消过这次报名。"
	case errors.Is(err, db.ErrSigninDisabled):
		return 400, "signin_disabled", "本活动未开启现场签到。"
	case errors.Is(err, db.ErrSigninCodeNotSet):
		return 409, "signin_code_not_set", "本活动未设置签到码，请联系管理员。"
	case errors.Is(err, db.ErrSigninCodeInvalid):
		return 400, "signin_code_invalid", "签到码不正确，请向现场工作人员确认。"
	case errors.Is(err, db.ErrMarkedAbsent):
		return 409, "attendance_marked_absent", "你已被标记为未到，请联系管理员。"
	case errors.Is(err, db.ErrActivityStarted):
		return 409, "activity_started", fmt.Sprintf("活动已经开始，无法%s。", action)
	case errors.Is(err, db.ErrActivityEnded):
		return 409, "activity_ended", fmt.Sprintf("活动已经结束，无法%s。", action)
	case errors.Is(err, db.ErrActivityNotStarted):
		return 409, "activity_not_started", fmt.Sprintf("活动尚未开始，暂时无法%s。", action)
	default:
		return 500, "internal_error", "系统暂时无法处理，请稍后再试。"
	}
}
