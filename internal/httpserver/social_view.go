package httpserver

import (
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// CardFieldItem / CardFieldSection mirror the v1 display_fields shape used by cards.
type CardFieldItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type CardFieldSection struct {
	Title  string          `json:"title"`
	Icon   string          `json:"icon"`
	Fields []CardFieldItem `json:"fields"`
}

type CardDisplayFields struct {
	Simple   string             `json:"simple"`
	Sections []CardFieldSection `json:"sections"`
}

// PhotoView is one profile photo exposed through /cards/photo/{id}.
type PhotoView struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
	// Width/Height are the original image dimensions. The SPA viewer sizes the
	// full-screen frame from them before the original is downloaded, so the
	// thumbnail -> original swap never changes the displayed rectangle.
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CardView is the template/JSON representation of one card.
type CardView struct {
	ID              int64             `json:"id"`
	OpenID          string            `json:"openid"`
	UserID          string            `json:"user_id"`
	Nickname        string            `json:"nickname"`
	Gender          string            `json:"gender"`
	Birthday        string            `json:"birthday"`
	Age             int               `json:"age,omitempty"`
	Height          int               `json:"height,omitempty"`
	Education       string            `json:"education"`
	BaptismalName   string            `json:"baptismal_name"`
	Church          string            `json:"church"`
	ChurchLocation  string            `json:"church_location"`
	ChurchGroup     string            `json:"church_group"`
	NativePlace     string            `json:"native_place"`
	City            string            `json:"city"`
	Industry        string            `json:"industry"`
	Position        string            `json:"position"`
	Income          string            `json:"income"`
	House           string            `json:"house"`
	Driving         string            `json:"driving"`
	Marriage        string            `json:"marriage"`
	Family          string            `json:"family"`
	LiveWithParents string            `json:"live_with_parents"`
	Personality     string            `json:"personality"`
	SelfTraits      string            `json:"self_traits"`
	SelfHobbies     string            `json:"self_hobbies"`
	SelfSports      string            `json:"self_sports"`
	MBTI            string            `json:"mbti"`
	PartnerCriteria string            `json:"partner_criteria"`
	PartnerTraits   string            `json:"partner_traits"`
	PartnerHobbies  string            `json:"partner_hobbies"`
	PartnerSports   string            `json:"partner_sports"`
	Subtitle        string            `json:"subtitle"`
	DisplayFields   CardDisplayFields `json:"display_fields"`
	Photo           string            `json:"photo"`
	PhotoURLs       []string          `json:"photos"`
	PhotoThumbs     []string          `json:"photos_thumb"`
	Photos          []PhotoView       `json:"-"`
	Liked           bool              `json:"liked"`
	Praised         bool              `json:"praised"`
	MsgCount        int               `json:"msg_count"`
	Recommended     bool              `json:"recommended"`
	IsSelf          bool              `json:"is_self"`
}

// SocialUserRef is a compact person reference used by likes/mutual pages.
type SocialUserRef struct {
	ID       int64  `json:"target_id"`
	OpenID   string `json:"openid"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Gender   string `json:"gender"`
	Photo    string `json:"photo"`
}

func socialCardOpenID(p db.SocialProfile) string {
	if p.LegacyOpenID != "" {
		return p.LegacyOpenID
	}
	if p.LegacyID != "" {
		return p.LegacyID
	}
	return strconv.FormatInt(p.ID, 10)
}

func socialPhotoView(id string) PhotoView {
	base := "/cards/photo/" + id
	return PhotoView{ID: id, URL: base, ThumbURL: base + "?size=thumb"}
}

func buildCardView(p db.SocialProfile, liked, praised bool, msgCount int, now time.Time) CardView {
	birthday := socialBirthdayLabel(p.Birthday)
	age := socialAgeOn(p.Birthday, now)
	height := 0
	if p.HeightCM != nil {
		height = *p.HeightCM
	}
	photos := make([]PhotoView, 0, len(p.Photos))
	urls := make([]string, 0, len(p.Photos))
	thumbs := make([]string, 0, len(p.Photos))
	photo := ""
	for i, id := range p.Photos {
		pv := socialPhotoView(id)
		if i < len(p.PhotoWidths) {
			pv.Width = p.PhotoWidths[i]
		}
		if i < len(p.PhotoHeights) {
			pv.Height = p.PhotoHeights[i]
		}
		photos = append(photos, pv)
		urls = append(urls, pv.URL)
		thumbs = append(thumbs, pv.ThumbURL)
		if i == 0 {
			photo = pv.ThumbURL
		}
	}
	simpleParts := make([]string, 0, 5)
	for _, v := range []string{
		p.LegacyID, p.BaptismalName, birthday, socialIntLabel(height), p.Education,
	} {
		if v != "" {
			simpleParts = append(simpleParts, v)
		}
	}
	subParts := make([]string, 0, 2)
	if birthday != "" {
		subParts = append(subParts, birthday)
	}
	if p.City != "" {
		subParts = append(subParts, p.City)
	}
	house := socialSelectWithNote(p.House, p.HouseNoteHave, p.HouseNoteNone)
	driving := socialSelectWithNote(p.Driving, p.DrivingNoteHave, p.DrivingNoteNone)
	view := CardView{
		ID:              p.ID,
		OpenID:          socialCardOpenID(p),
		UserID:          p.LegacyID,
		Nickname:        p.DisplayName(),
		Gender:          p.Gender,
		Birthday:        birthday,
		Age:             age,
		Height:          height,
		Education:       p.Education,
		BaptismalName:   p.BaptismalName,
		Church:          p.Church,
		ChurchLocation:  p.ChurchLocation,
		ChurchGroup:     p.ChurchGroup,
		NativePlace:     p.NativePlace,
		City:            p.City,
		Industry:        p.Industry,
		Position:        p.Position,
		Income:          p.Income,
		House:           house,
		Driving:         driving,
		Marriage:        p.Marriage,
		Family:          p.Family,
		LiveWithParents: p.LiveWithParents,
		Personality:     p.Personality,
		SelfTraits:      strings.Join(p.SelfTraits, "、"),
		SelfHobbies:     strings.Join(p.SelfHobbies, "、"),
		SelfSports:      strings.Join(p.SelfSports, "、"),
		MBTI:            strings.Join(p.MBTI, "、"),
		PartnerCriteria: p.PartnerCriteria,
		PartnerTraits:   strings.Join(p.PartnerTraits, "、"),
		PartnerHobbies:  strings.Join(p.PartnerHobbies, "、"),
		PartnerSports:   strings.Join(p.PartnerSports, "、"),
		Subtitle:        strings.Join(subParts, " · "),
		Photo:           photo,
		PhotoURLs:       urls,
		PhotoThumbs:     thumbs,
		Photos:          photos,
		Liked:           liked,
		Praised:         praised,
		MsgCount:        msgCount,
	}
	view.DisplayFields = CardDisplayFields{
		Simple:   strings.Join(simpleParts, " · "),
		Sections: cardSections(view),
	}
	return view
}

func cardSections(c CardView) []CardFieldSection {
	defs := []struct {
		title string
		icon  string
		items []CardFieldItem
	}{
		{"基本信息", "👤", cardFieldItems(
			"现居/工作城市", c.City,
			"家乡", c.NativePlace,
			"经常去的教堂", c.Church,
			"参加的团体", c.ChurchGroup,
			"教堂所在市-区", c.ChurchLocation,
		)},
		{"工作与经济", "💼", cardFieldItems(
			"从事行业", c.Industry,
			"职位", c.Position,
			"年收入", c.Income,
			"房产状况", c.House,
			"汽车", c.Driving,
		)},
		{"关于我", "✨", cardFieldItems(
			"我是怎样的人", c.Personality,
			"性格", c.SelfTraits,
			"爱好", c.SelfHobbies,
			"运动", c.SelfSports,
			"MBTI", c.MBTI,
			"婚后与父母同住", c.LiveWithParents,
			"家庭情况", c.Family,
		)},
		{"理想中的TA", "💖", cardFieldItems(
			"理想中的TA", c.PartnerCriteria,
			"性格", c.PartnerTraits,
			"爱好", c.PartnerHobbies,
			"运动", c.PartnerSports,
		)},
	}
	out := make([]CardFieldSection, 0, len(defs))
	for _, d := range defs {
		if len(d.items) > 0 {
			out = append(out, CardFieldSection{Title: d.title, Icon: d.icon, Fields: d.items})
		}
	}
	return out
}

func cardFieldItems(pairs ...string) []CardFieldItem {
	out := make([]CardFieldItem, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) != "" {
			out = append(out, CardFieldItem{Label: pairs[i], Value: pairs[i+1]})
		}
	}
	return out
}

func socialSelectWithNote(value, noteHave, noteNone string) string {
	if value == "" {
		return ""
	}
	note := noteNone
	if value == "有" {
		note = noteHave
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return value
	}
	return value + " · " + note
}

func socialIntLabel(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func socialBirthdayLabel(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return db.InSocialTZ(*t).Format("06-01")
}

func socialAgeOn(birthday *time.Time, now time.Time) int {
	if birthday == nil {
		return 0
	}
	now = db.InSocialTZ(now)
	age := now.Year() - birthday.Year()
	if now.Month() < birthday.Month() || (now.Month() == birthday.Month() && now.Day() < birthday.Day()) {
		age--
	}
	if age < 0 || age > 150 {
		return 0
	}
	return age
}

func socialUserRef(p db.SocialProfile) SocialUserRef {
	ref := SocialUserRef{
		ID:       p.ID,
		OpenID:   socialCardOpenID(p),
		UserID:   p.LegacyID,
		Nickname: p.DisplayName(),
		Gender:   p.Gender,
	}
	if len(p.Photos) > 0 {
		ref.Photo = socialPhotoView(p.Photos[0]).ThumbURL
	}
	return ref
}

func socialLikeStatusLabel(pair db.LikePair) string {
	if pair.Mutual {
		return "相互喜欢"
	}
	if !pair.Active {
		return "已失效"
	}
	return "单向喜欢"
}

func formatSocialTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(db.InSocialTZ(t).Location()).Format("2006-01-02 15:04")
}
