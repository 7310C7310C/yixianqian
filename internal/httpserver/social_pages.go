package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

var errGenderMissing = errors.New("httpserver: viewer gender is missing")

// CardFilterOption is one selectable filter tag.
type CardFilterOption struct {
	Value    string
	Selected bool
}

// CardFilterView keeps submitted values so the SSR filter form can be refilled.
type CardFilterView struct {
	Nickname         string
	UserID           string
	City             string
	Church           string
	NativePlace      string
	Industry         string
	Education        string
	AgeMin           string
	AgeMax           string
	HeightMin        string
	HeightMax        string
	EducationOptions []CardFilterOption
	IncomeOptions    []CardFilterOption
	HouseOptions     []CardFilterOption
	DrivingOptions   []CardFilterOption
}

// CardsPageData is the legacy JSON payload used by /legacy/api/cards and /legacy/api/home.
type CardsPageData struct {
	Cards         []CardView
	Total         int
	Offset        int
	Limit         int
	PrevOffset    int
	NextOffset    int
	HasPrev       bool
	HasNext       bool
	PrevURL       string
	NextURL       string
	Seed          string
	Version       int64
	Filter        CardFilterView
	FilterActive  bool
	GenderFilter  string
	IsObserver    bool
	CanLike       bool
	CanPraise     bool
	AnonTotal     int
	AnonUsed      int
	AnonLeft      int
	RealTotal     int
	RealLeft      int
	PraiseLimit   int
	PraiseLeft    int
	StartNumber   int
	DisplayNumber int
	Index         int
	ReturnPath    string
	Notice        string
	Error         string
}

// MutualView is one mutual match, with contact fields gated by consent.
type MutualView struct {
	Other          SocialUserRef
	LikeType       string
	Message        string
	MyWechat       string
	TheirWechat    string
	WechatRequired bool
	WechatStatus   string
	WaitingOther   bool
	Since          string
}

// LikedMeView is one incoming like. Anonymous rows never carry an identity.
type LikedMeView struct {
	Anonymous bool
	Nickname  string
	UserID    string
	OpenID    string
	LikeType  string
	Real      bool
	Status    string
	Since     string
}

// MyLikeView is one outgoing like, including expired anonymous rows.
type MyLikeView struct {
	ID          int64
	Target      SocialUserRef
	Mutual      bool
	Active      bool
	LikeType    string
	Message     string
	Status      string
	StatusLabel string
	Since       string
	Age         int
	Church      string
}

// SocialNotificationView is a safe notification projection.
type SocialNotificationView struct {
	ID         int64
	Kind       string
	Title      string
	Body       string
	Link       string
	Read       bool
	Created    string
	IsActivity bool
}

// LikesPageData backs the /likes hub.
type LikesPageData struct {
	MutualCount           int
	LikedMeCount          int
	MyLikesCount          int
	MyMessagesCount       int
	ReceivedMessagesCount int
	Notifications         []SocialNotificationView
	Unread                int
	Praise                db.PraiseState
	CanInteract           bool
}

// MutualPageData backs /mutual.
type MutualPageData struct {
	Items []MutualView
}

// LikedMePageData backs /liked_me.
type LikedMePageData struct {
	Items []LikedMeView
}

// MyLikesPageData backs /my_likes.
type MyLikesPageData struct {
	Items []MyLikeView
}

// cardPageDeps lets aggregated callers (home) reuse quota/praise they already
// loaded instead of paying for the same two queries twice.
type cardPageDeps struct {
	quota  *db.LikeQuota
	praise *db.PraiseState
}

func (a *App) buildCardsPageData(ctx context.Context, viewer db.SocialViewer, q url.Values,
	now time.Time, pageLimit int, deps cardPageDeps) (CardsPageData, error) {
	filter := parseCardFilter(q)
	targetGender, err := socialTargetGender(viewer, strings.TrimSpace(q.Get("gender")))
	if err != nil {
		return CardsPageData{Filter: cardFilterView(filter)}, err
	}
	candidateIDs, err := a.db.ListCardCandidateIDs(ctx, viewer.ID, targetGender, filter, now)
	if err != nil {
		return CardsPageData{}, err
	}
	state, err := a.db.LoadCardSocialState(ctx, viewer.ID, now)
	if err != nil {
		return CardsPageData{}, err
	}
	seed := socialSeed(strings.TrimSpace(q.Get("seed")), viewer.ID, now)
	version := cardVersion(seed, filter, targetGender)
	// 本周推荐位：必显（匿名喜欢我的人）在前、算法位在后，整体置顶。
	// pinnedIDs 的顺序就是展示顺序，OrderCardIDs 不会打乱它。
	recommendedIDs, err := a.db.WeeklyRecommendationTargets(ctx, viewer.ID, db.WeekKey(now))
	if err != nil {
		return CardsPageData{}, err
	}
	recommended := make(map[int64]bool, len(recommendedIDs))
	for _, id := range recommendedIDs {
		recommended[id] = true
	}
	ordered := db.OrderCardIDs(candidateIDs, state.LikedMe, recommendedIDs, fmt.Sprintf("%s|%d", seed, version))

	offset := socialNonNegative(q.Get("offset"), 0)
	if offset > len(ordered) {
		offset = len(ordered)
	}
	limit := pageLimit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	end := offset + limit
	if end > len(ordered) {
		end = len(ordered)
	}
	pageIDs := ordered[offset:end]
	profiles, err := a.db.GetSocialProfilesByIDs(ctx, pageIDs)
	if err != nil {
		return CardsPageData{}, err
	}
	// 留言数只统计当前页收件人（语义仍是「该收件人收到的留言总数」）。
	msgCounts, err := a.db.MessageCountsFor(ctx, pageIDs)
	if err != nil {
		return CardsPageData{}, err
	}
	for id, n := range msgCounts {
		state.MessageCounts[id] = n
	}
	views := make([]CardView, 0, len(pageIDs))
	for _, id := range pageIDs {
		p, ok := profiles[id]
		if !ok {
			continue
		}
		view := buildCardView(p, state.LikedTargets[id], state.PraisedTargets[id],
			state.MessageCounts[id], now)
		view.IsSelf = id == viewer.ID
		view.Recommended = recommended[id]
		views = append(views, view)
	}

	quota := db.LikeQuota{}
	if deps.quota != nil {
		quota = *deps.quota
	} else {
		quota, err = a.db.LikeQuotaFor(ctx, viewer.ID, now)
		if err != nil {
			return CardsPageData{}, err
		}
	}
	praise := db.PraiseState{}
	if deps.praise != nil {
		praise = *deps.praise
	} else {
		praise, err = a.db.PraiseStateFor(ctx, viewer.ID, now)
		if err != nil {
			return CardsPageData{}, err
		}
	}
	prevValues := socialCloneValues(q)
	prevValues.Set("offset", strconv.Itoa(socialMaxInt(0, offset-limit)))
	prevValues.Set("seed", seed)
	nextValues := socialCloneValues(q)
	nextValues.Set("offset", strconv.Itoa(offset+limit))
	nextValues.Set("seed", seed)
	data := CardsPageData{
		Cards:        views,
		Total:        len(ordered),
		Offset:       offset,
		Limit:        limit,
		PrevOffset:   socialMaxInt(0, offset-limit),
		NextOffset:   offset + limit,
		HasPrev:      offset > 0,
		HasNext:      offset+limit < len(ordered),
		PrevURL:      "",
		NextURL:      "",
		Seed:         seed,
		Version:      version,
		Filter:       cardFilterView(filter),
		FilterActive: cardFilterActive(filter),
		GenderFilter: strings.TrimSpace(q.Get("gender")),
		IsObserver:   viewer.AccountKind == "observer" || viewer.AccountStatus == "observer",
		CanLike:      socialCanInteract(viewer),
		CanPraise:    socialCanInteract(viewer),
		AnonTotal:    quota.AnonTotal,
		AnonUsed:     quota.AnonUsed,
		AnonLeft:     quota.AnonLeft,
		RealTotal:    quota.RealTotal,
		RealLeft:     quota.RealLeft,
		PraiseLimit:  praise.Limit,
		PraiseLeft:   praise.Left,
		StartNumber:  socialStartNumber(offset, len(views)),
		Index:        cardIndexInPage(q, len(views)),
	}
	if data.HasPrev {
		data.PrevURL = "/cards?" + prevValues.Encode()
	}
	if data.HasNext {
		data.NextURL = "/cards?" + nextValues.Encode()
	}
	data.DisplayNumber = data.StartNumber + data.Index
	return data, nil
}

func socialCanInteract(u db.SocialViewer) bool {
	return u.Status == "active" && u.AccountKind != "observer" &&
		u.AccountStatus != "observer" && u.AccountStatus == "active"
}

func socialTargetGender(u db.SocialViewer, requested string) (string, error) {
	if u.AccountKind == "observer" || u.AccountStatus == "observer" {
		if requested == db.GenderMale || requested == db.GenderFemale {
			return requested, nil
		}
		return "", nil
	}
	switch u.Gender {
	case db.GenderMale:
		return db.GenderFemale, nil
	case db.GenderFemale:
		return db.GenderMale, nil
	default:
		return "", errGenderMissing
	}
}

func parseCardFilter(q url.Values) db.CardFilter {
	return db.CardFilter{
		Nickname:    socialClipText(q.Get("nickname"), 50),
		UserID:      socialFilterUserID(q.Get("user_id")),
		City:        socialClipText(q.Get("city"), 50),
		Church:      socialClipText(q.Get("church"), 50),
		NativePlace: socialClipText(q.Get("native_place"), 50),
		Industry:    socialClipText(q.Get("industry"), 50),
		Education:   socialClipList(q["education"], 30, 20),
		Income:      socialClipList(q["income"], 30, 20),
		House:       socialClipList(q["house"], 30, 20),
		Driving:     socialClipList(q["driving"], 30, 20),
		AgeMin:      socialBoundedInt(q.Get("age_min"), 0, 120),
		AgeMax:      socialBoundedInt(q.Get("age_max"), 0, 120),
		HeightMin:   socialBoundedInt(q.Get("height_min"), 0, 300),
		HeightMax:   socialBoundedInt(q.Get("height_max"), 0, 300),
	}
}

func cardFilterView(f db.CardFilter) CardFilterView {
	education := ""
	if len(f.Education) > 0 {
		education = f.Education[0]
	}
	return CardFilterView{
		Nickname: f.Nickname, UserID: f.UserID, City: f.City, Church: f.Church,
		NativePlace: f.NativePlace, Industry: f.Industry, Education: education,
		AgeMin: socialIntString(f.AgeMin), AgeMax: socialIntString(f.AgeMax),
		HeightMin: socialIntString(f.HeightMin), HeightMax: socialIntString(f.HeightMax),
		EducationOptions: cardFilterOptions([]string{"大专以下", "大专", "本科", "硕士", "博士"}, f.Education),
		IncomeOptions:    cardFilterOptions([]string{"10W以下", "10W - 20W", "20W - 30W", "30W - 50W", "50W以上", "未填"}, f.Income),
		HouseOptions:     cardFilterOptions([]string{"有", "无", "未填"}, f.House),
		DrivingOptions:   cardFilterOptions([]string{"有", "无", "未填"}, f.Driving),
	}
}

func cardFilterOptions(values, selected []string) []CardFilterOption {
	out := make([]CardFilterOption, 0, len(values))
	for _, value := range values {
		option := CardFilterOption{Value: value}
		for _, want := range selected {
			if value == want {
				option.Selected = true
				break
			}
		}
		out = append(out, option)
	}
	return out
}

// socialFilterUserID accepts both the legacy "U-0006" form and a bare number.
func socialFilterUserID(raw string) string {
	value := socialClipText(raw, 50)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "u-") {
		return "U-" + value[2:]
	}
	return "U-" + value
}

func socialStartNumber(offset, count int) int {
	if count == 0 {
		return 0
	}
	return offset + 1
}

func cardIndexInPage(q url.Values, count int) int {
	if count <= 0 {
		return 0
	}
	index := socialNonNegative(q.Get("index"), 0)
	if index >= count {
		return 0
	}
	return index
}

func cardFilterActive(f db.CardFilter) bool {
	return f.Nickname != "" || f.UserID != "" || f.City != "" || f.Church != "" ||
		f.NativePlace != "" || f.Industry != "" || len(f.Education) > 0 ||
		len(f.Income) > 0 || len(f.House) > 0 || len(f.Driving) > 0 ||
		f.AgeMin > 0 || f.AgeMax > 0 || f.HeightMin > 0 || f.HeightMax > 0
}

func socialClipText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}

func socialClipList(values []string, maxRunes, maxItems int) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = socialClipText(v, maxRunes)
		if v == "" {
			continue
		}
		out = append(out, v)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func socialBoundedInt(raw string, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < min || n > max {
		return 0
	}
	return n
}

func socialNonNegative(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func socialIntString(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func socialSeed(raw string, viewerID int64, now time.Time) string {
	if raw != "" && len(raw) <= 64 {
		valid := true
		for _, r := range raw {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
				r == '-' || r == '_' || r == '.' || r == ':' {
				continue
			}
			valid = false
			break
		}
		if valid {
			return raw
		}
	}
	return fmt.Sprintf("%d-%s", viewerID, db.DayKey(now))
}

func cardVersion(seed string, filter db.CardFilter, targetGender string) int64 {
	raw, err := json.Marshal(filter)
	if err != nil {
		raw = []byte(fmt.Sprintf("%#v", filter))
	}
	return int64(socialFNV32a(seed + "|" + targetGender + "|" + string(raw)))
}

func socialFNV32a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func socialCloneValues(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, values := range q {
		out[k] = append([]string(nil), values...)
	}
	return out
}

func socialMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func socialFlash(notice, errMsg string, q url.Values) (string, string) {
	if v := q.Get("liked"); v == "1" {
		notice = "喜欢已送出。"
	}
	if q.Get("mutual") == "1" {
		notice = "你们相互喜欢了！可以去「相互喜欢」查看微信号。"
	}
	if q.Get("praised") == "1" {
		notice = "已点赞。"
	}
	if q.Get("unpraised") == "1" {
		notice = "已取消点赞。"
	}
	switch q.Get("like_error") {
	case "":
	case "self":
		errMsg = "不能喜欢自己。"
	case "same_gender":
		errMsg = "仅限异性之间喜欢。"
	case "target":
		errMsg = "对方目前不在牵线名单中。"
	case "already":
		errMsg = "你已经喜欢过TA了。"
	case "quota":
		errMsg = "本月喜欢额度已用完，月初会补满。"
	case "message":
		errMsg = "附言需 10 至 500 字。"
	case "type":
		errMsg = "喜欢类型不正确。"
	default:
		errMsg = "喜欢没有送出，请稍后再试。"
	}
	if q.Get("feedback") == "1" {
		notice = "反馈已提交，谢谢你的提醒。"
	}
	switch q.Get("praise_error") {
	case "":
	case "self":
		errMsg = "不能给自己点赞。"
	case "same_gender":
		errMsg = "仅限异性之间点赞。"
	case "target":
		errMsg = "对方目前不在牵线名单中。"
	case "quota":
		errMsg = "今天已经点满 10 个赞了，明天再来。"
	default:
		errMsg = "点赞没有送出，请稍后再试。"
	}
	return notice, errMsg
}

func mutualViews(o db.SocialOverview) []MutualView {
	pairs := uniqueMutualPairs(o)
	out := make([]MutualView, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, mutualView(o, pair))
	}
	return out
}

func mutualView(o db.SocialOverview, pair db.LikePair) MutualView {
	view := MutualView{
		Other:    socialUserRef(pair.Other),
		LikeType: pair.Like.LikeType,
		Message:  mutualMessage(o, pair),
		Since:    formatSocialTime(pair.Like.CreatedAt),
	}
	status, required, waiting := mutualWechatStatus(o.Me, pair.Other)
	view.WechatStatus = status
	view.WechatRequired = required
	view.WaitingOther = waiting
	if strings.TrimSpace(o.Me.Wechat) != "" {
		view.MyWechat = o.Me.Wechat
	}
	if status == wechatStatusReady {
		view.TheirWechat = pair.Other.Wechat
	}
	return view
}

func likedMeViews(o db.SocialOverview) []LikedMeView {
	out := make([]LikedMeView, 0, len(o.LikesMe))
	for _, pair := range o.LikesMe {
		if pair.Mutual {
			continue
		}
		view := LikedMeView{
			LikeType: pair.Like.LikeType,
			Real:     pair.Like.LikeType == db.LikeTypeReal,
			Status:   socialLikeStatusLabel(pair),
			Since:    formatSocialTime(pair.Like.CreatedAt),
		}
		if view.Real {
			view.Nickname = pair.Other.DisplayName()
			view.UserID = pair.Other.LegacyID
			view.OpenID = socialCardOpenID(pair.Other)
		} else {
			view.Anonymous = true
			view.Nickname = "匿名用户"
		}
		out = append(out, view)
	}
	return out
}

func myLikeViews(o db.SocialOverview, now ...time.Time) []MyLikeView {
	at := time.Time{}
	if len(now) > 0 {
		at = now[0]
	}
	out := make([]MyLikeView, 0, len(o.MyLikes))
	for _, pair := range o.MyLikes {
		out = append(out, MyLikeView{
			ID:          pair.Like.ID,
			Target:      socialUserRef(pair.Other),
			Mutual:      pair.Mutual,
			Active:      pair.Active,
			LikeType:    pair.Like.LikeType,
			Message:     pair.Like.Message,
			Status:      pair.Like.Status,
			StatusLabel: socialLikeStatusLabel(pair),
			Since:       formatSocialTime(pair.Like.CreatedAt),
			Age:         socialAgeOn(pair.Other.Birthday, at),
			Church:      pair.Other.Church,
		})
	}
	return out
}

func mutualMessage(o db.SocialOverview, pair db.LikePair) string {
	// Only the other party's message is a mutual-list message. A status-only
	// mutual row without the incoming row must not expose the viewer's own note
	// as if the other person wrote it.
	if pair.Like.TargetID == o.Me.ID {
		return pair.Like.Message
	}
	return ""
}

func notificationViews(rows []db.SocialNotification) []SocialNotificationView {
	out := make([]SocialNotificationView, 0, len(rows))
	for _, n := range rows {
		out = append(out, SocialNotificationView{
			ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body, Link: n.Link,
			Read: n.ReadAt != nil, Created: formatSocialTime(n.CreatedAt),
			IsActivity: strings.HasPrefix(n.Link, "/activities/"),
		})
	}
	return out
}
