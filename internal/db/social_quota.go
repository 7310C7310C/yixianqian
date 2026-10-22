package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Social business constants. The string values are the persisted values in
// likes.status / likes.like_type / praises.status; do not translate them.
const (
	LikeStatusSingle   = "单向喜欢"
	LikeStatusMutual   = "相互喜欢"
	LikeStatusRejected = "被驳回"

	LikeTypeAnon = "匿名"
	LikeTypeReal = "实名"

	PraiseStatusActive    = "有效"
	PraiseStatusCancelled = "已取消"
	PraiseObjectProfile   = "profile"

	GenderMale   = "男性"
	GenderFemale = "女性"

	MonthlyAnonHearts = 10
	MonthlyRealHearts = 1
	AnonExpireMonths  = 3
	DailyPraiseLimit  = 10
)

// Shanghai is a fixed offset: China has not observed DST since 1991, and the
// legacy system explicitly pinned Asia/Shanghai instead of relying on the host
// timezone. Keeping the offset in code makes month/day/week buckets identical
// on every server.
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

// SocialNow returns the current time in the social timezone.
func SocialNow() time.Time { return time.Now().In(shanghai) }

// InSocialTZ converts t to the social timezone.
func InSocialTZ(t time.Time) time.Time { return t.In(shanghai) }

// MonthKey formats a time as the "YYYY-MM" bucket used by likes.month.
func MonthKey(t time.Time) string { return t.In(shanghai).Format("2006-01") }

// DayKey formats a time as the "YYYY-MM-DD" bucket used by praises.day.
func DayKey(t time.Time) string { return t.In(shanghai).Format("2006-01-02") }

// WeekKey formats a time as the ISO week "YYYY-Www" bucket used by praises.week.
func WeekKey(t time.Time) string {
	y, w := t.In(shanghai).ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// MonthAdd shifts a month bucket by n months (n may be negative).
func MonthAdd(ym string, n int) string {
	y, m, ok := parseMonth(ym)
	if !ok {
		return ""
	}
	m += n
	for m > 12 {
		m -= 12
		y++
	}
	for m < 1 {
		m += 12
		y--
	}
	return fmt.Sprintf("%04d-%02d", y, m)
}

// MonthsBetween reports how many months b is after a. The second result is
// false when either bucket is malformed.
func MonthsBetween(a, b string) (int, bool) {
	ya, ma, ok := parseMonth(a)
	if !ok {
		return 0, false
	}
	yb, mb, ok := parseMonth(b)
	if !ok {
		return 0, false
	}
	return (yb-ya)*12 + (mb - ma), true
}

func parseMonth(ym string) (year, month int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(ym), "-", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	y, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 1 || m > 12 {
		return 0, 0, false
	}
	return y, m, true
}

// IsLikeActive ports lib/quota.is_like_active exactly:
//   - only single/mutual are whitelisted;
//   - mutual likes never expire;
//   - real-name likes never expire;
//   - anonymous single likes expire after AnonExpireMonths month buckets;
//   - a missing month bucket is kept conservatively.
func IsLikeActive(status, likeType, month, currentMonth string) bool {
	if status != LikeStatusSingle && status != LikeStatusMutual {
		return false
	}
	if status == LikeStatusMutual {
		return true
	}
	if likeType != LikeTypeAnon {
		return true
	}
	if month == "" {
		return true
	}
	gap, ok := MonthsBetween(month, currentMonth)
	if !ok {
		return true
	}
	return gap < AnonExpireMonths
}

// LikeRecord is the quota-relevant projection of a like row.
type LikeRecord struct {
	InitiatorID int64
	TargetID    int64
	Status      string
	LikeType    string
	Month       string
}

// LikeQuota is a point-in-time view of the two independent like allowances.
type LikeQuota struct {
	AnonTotal     int `json:"anon_total"`
	AnonUsed      int `json:"anon_used"`
	AnonLeft      int `json:"anon_left"`
	RealTotal     int `json:"real_total"`
	RealLeft      int `json:"real_left"`
	RealPermanent int `json:"real_permanent"`
}

// ComputeLikeQuota ports lib/quota.anon_left / anon_used / real_left.
// records should contain all likes initiated by one user.
func ComputeLikeQuota(records []LikeRecord, permanent int, currentMonth string) LikeQuota {
	if permanent < 0 {
		permanent = 0
	}
	used := 0
	refunded := 0
	realUsed := 0
	for _, r := range records {
		if r.Status != LikeStatusSingle && r.Status != LikeStatusMutual {
			continue
		}
		if r.LikeType == LikeTypeAnon {
			if r.Month != "" {
				gap, ok := MonthsBetween(r.Month, currentMonth)
				if ok {
					switch gap {
					case 0:
						used++
					case AnonExpireMonths:
						refunded++
					}
				}
			}
			continue
		}
		if r.LikeType == LikeTypeReal && r.Month == currentMonth {
			realUsed++
		}
	}
	anonLeft := MonthlyAnonHearts + refunded - used
	if anonLeft < 0 {
		anonLeft = 0
	}
	realTotal := MonthlyRealHearts + permanent
	realLeft := realTotal - realUsed
	if realLeft < 0 {
		realLeft = 0
	}
	return LikeQuota{
		AnonTotal:     MonthlyAnonHearts,
		AnonUsed:      used,
		AnonLeft:      anonLeft,
		RealTotal:     realTotal,
		RealLeft:      realLeft,
		RealPermanent: permanent,
	}
}
