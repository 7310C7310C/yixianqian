package httpserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// profileDisplayZone keeps rendered timestamps stable regardless of the host
// timezone. The product is used in China (UTC+8).
var profileDisplayZone = time.FixedZone("CST", 8*3600)

var profileStatusLabels = map[string]string{
	"pending":  "待审核",
	"active":   "单身",
	"matched":  "已脱单",
	"rejected": "审核不通过",
	"left":     "已退出",
	"observer": "观察员",
	"disabled": "已停用",
}

func profileStatusLabel(status string) string {
	if label, ok := profileStatusLabels[status]; ok {
		return label
	}
	return "未知"
}

func profileUserID(p db.Profile) string {
	if p.LegacyUserID != "" {
		return p.LegacyUserID
	}
	return strconv.FormatInt(p.ID, 10)
}

func profileBirthdayShort(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("%02d-%d", t.Year()%100, int(t.Month()))
}

func profileDateISO(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func profileTimeValue(t time.Time) string {
	return t.In(profileDisplayZone).Format("2006-01-02 15:04")
}

func profileRegisteredAt(p db.Profile) *time.Time {
	if p.RegisteredAt != nil {
		return p.RegisteredAt
	}
	return &p.CreatedAt
}

func profileJoin(list []string) string { return strings.Join(list, "、") }

func profileSelectWithNote(value, noteHave, noteNone string) string {
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

type profileFieldView struct {
	Label string
	Value string
}

type profileSectionView struct {
	Title  string
	Icon   string
	Fields []profileFieldView
}

type profilePhotoView struct {
	MediaID  string
	URL      string
	ThumbURL string
	Width    int
	Height   int
	IsCover  bool
}

type profileView struct {
	Nickname           string
	Gender             string
	Birthday           string
	Height             string
	Education          string
	BaptismalName      string
	UserID             string
	Name               string
	AccountStatus      string
	AccountStatusLabel string
	AccountKindLabel   string
	RegisteredAt       string
	Phone              string
	Wechat             string
	IDCardLast4        string
	IDCardValid        bool
	ContactConsent     bool
	MetaLine           string
	Sections           []profileSectionView
	Photos             []profilePhotoView
	CoverURL           string
	IsObserver         bool
	PointsBalance      int
	AnonTotal          int
	AnonLeft           int
	RealTotal          int
	RealLeft           int
	PraiseReceivedWeek int
	IsAdmin            bool
	HasActiveSignup    bool
	CanToggleStatus    bool
	NextStatus         string
	ToggleLabel        string
}

type publicProfileView struct {
	Nickname     string
	Gender       string
	Birthday     string
	Height       string
	UserID       string
	MetaLine     string
	CoverURL     string
	CoverFullURL string
	Sections     []profileSectionView
	Photos       []profilePhotoView
}

type profileEditOption struct {
	Value    string
	Selected bool
}

type profileEditField struct {
	Name        string
	Label       string
	Type        string
	Value       string
	Placeholder string
	Required    bool
	Rows        int
	Min         int
	Max         int
	Options     []profileEditOption
}

type profileEditMulti struct {
	Name    string
	Label   string
	Options []profileEditOption
}

type profileEditSection struct {
	Title  string
	Icon   string
	Fields []profileEditField
	Multi  []profileEditMulti
}

type profileEditView struct {
	Sections    []profileEditSection
	Photos      []profilePhotoView
	CanAddPhoto bool
	MaxPhotos   int
	Birthday    string
	IDCardLast4 string
	IDCardValid bool
}

func profilePhotoViews(photos []db.ProfilePhoto) []profilePhotoView {
	out := make([]profilePhotoView, 0, len(photos))
	for _, ph := range photos {
		url := "/profile/photo/" + ph.MediaID
		out = append(out, profilePhotoView{
			MediaID:  ph.MediaID,
			URL:      url,
			ThumbURL: url + "?size=thumb",
			Width:    ph.Width,
			Height:   ph.Height,
			IsCover:  ph.IsCover,
		})
	}
	return out
}

func profileSections(p db.Profile, includePrivate bool) []profileSectionView {
	basic := []profileFieldView{}
	if includePrivate && p.Name != "" {
		basic = append(basic, profileFieldView{"姓名", p.Name})
	}
	if p.BaptismalName != "" {
		basic = append(basic, profileFieldView{"圣名", p.BaptismalName})
	}
	if p.Gender != "" {
		basic = append(basic, profileFieldView{"性别", p.Gender})
	}
	if birthday := profileBirthdayShort(p.Birthday); birthday != "" {
		basic = append(basic, profileFieldView{"生日", birthday})
	}
	if p.HeightCM != nil {
		basic = append(basic, profileFieldView{"身高", fmt.Sprintf("%d cm", *p.HeightCM)})
	}
	if p.Education != "" {
		basic = append(basic, profileFieldView{"学历", p.Education})
	}
	if includePrivate && p.Marriage != "" {
		basic = append(basic, profileFieldView{"婚姻状况", p.Marriage})
	}
	if p.City != "" {
		basic = append(basic, profileFieldView{"现居/工作城市", p.City})
	}
	if p.NativePlace != "" {
		basic = append(basic, profileFieldView{"家乡", p.NativePlace})
	}
	if p.Church != "" {
		basic = append(basic, profileFieldView{"经常去的教会", p.Church})
	}
	if p.ChurchGroup != "" {
		basic = append(basic, profileFieldView{"参加的团体", p.ChurchGroup})
	}
	if p.ChurchLocation != "" {
		basic = append(basic, profileFieldView{"教会所在省-市-区", p.ChurchLocation})
	}

	work := []profileFieldView{}
	if p.Industry != "" {
		work = append(work, profileFieldView{"从事行业", p.Industry})
	}
	if p.Position != "" {
		work = append(work, profileFieldView{"职位", p.Position})
	}
	if p.Income != "" {
		work = append(work, profileFieldView{"年收入", p.Income})
	}
	if house := profileSelectWithNote(p.House, p.HouseNoteHave, p.HouseNoteNone); house != "" {
		work = append(work, profileFieldView{"房产状况", house})
	}
	if driving := profileSelectWithNote(p.Driving, p.DrivingNoteHave, p.DrivingNoteNone); driving != "" {
		work = append(work, profileFieldView{"汽车", driving})
	}

	about := []profileFieldView{}
	if p.Personality != "" {
		about = append(about, profileFieldView{"我是怎样的人", p.Personality})
	}
	if len(p.SelfTraits) > 0 {
		about = append(about, profileFieldView{"性格", profileJoin(p.SelfTraits)})
	}
	if len(p.SelfHobbies) > 0 {
		about = append(about, profileFieldView{"爱好", profileJoin(p.SelfHobbies)})
	}
	if len(p.SelfSports) > 0 {
		about = append(about, profileFieldView{"运动", profileJoin(p.SelfSports)})
	}
	if len(p.MBTI) > 0 {
		about = append(about, profileFieldView{"MBTI", profileJoin(p.MBTI)})
	}
	if p.LiveWithParents != "" {
		about = append(about, profileFieldView{"婚后与父母同住", p.LiveWithParents})
	}
	if p.Family != "" {
		about = append(about, profileFieldView{"家庭情况", p.Family})
	}

	partner := []profileFieldView{}
	if p.PartnerCriteria != "" {
		partner = append(partner, profileFieldView{"理想中的 TA", p.PartnerCriteria})
	}
	if len(p.PartnerTraits) > 0 {
		partner = append(partner, profileFieldView{"性格", profileJoin(p.PartnerTraits)})
	}
	if len(p.PartnerHobbies) > 0 {
		partner = append(partner, profileFieldView{"爱好", profileJoin(p.PartnerHobbies)})
	}
	if len(p.PartnerSports) > 0 {
		partner = append(partner, profileFieldView{"运动", profileJoin(p.PartnerSports)})
	}

	var sections []profileSectionView
	appendSection := func(title, icon string, fields []profileFieldView) {
		if len(fields) > 0 {
			sections = append(sections, profileSectionView{Title: title, Icon: icon, Fields: fields})
		}
	}
	appendSection("基本信息", "👤", basic)
	appendSection("工作与经济", "💼", work)
	appendSection("关于我", "✨", about)
	appendSection("理想中的 TA", "💖", partner)

	if includePrivate {
		contact := []profileFieldView{}
		if p.Wechat != "" {
			contact = append(contact, profileFieldView{"微信号", p.Wechat})
		}
		if p.Phone != "" {
			contact = append(contact, profileFieldView{"手机号码", p.Phone})
		}
		if p.IDCardLast4 != "" {
			mask := "尾号 " + p.IDCardLast4
			if p.IDCardValid {
				mask += "（已验证）"
			} else {
				mask += "（待验证）"
			}
			contact = append(contact, profileFieldView{"身份证", mask})
		}
		consent := "未同意"
		if p.ContactConsent {
			consent = "已同意"
		}
		contact = append(contact, profileFieldView{"联络意愿", consent})
		appendSection("联络方式", "📞", contact)
	}
	return sections
}

func buildProfileView(p db.Profile, hasActiveSignup bool) profileView {
	v := profileView{
		Nickname:           p.Nickname,
		Gender:             p.Gender,
		Birthday:           profileBirthdayShort(p.Birthday),
		Education:          p.Education,
		BaptismalName:      p.BaptismalName,
		UserID:             profileUserID(p),
		Name:               p.Name,
		AccountStatus:      p.AccountStatus,
		AccountStatusLabel: profileStatusLabel(p.AccountStatus),
		AccountKindLabel:   "会员",
		Phone:              p.Phone,
		Wechat:             p.Wechat,
		IDCardLast4:        p.IDCardLast4,
		IDCardValid:        p.IDCardValid,
		ContactConsent:     p.ContactConsent,
		Sections:           profileSections(p, true),
		Photos:             profilePhotoViews(p.Photos),
		HasActiveSignup:    hasActiveSignup,
	}
	if p.HeightCM != nil {
		v.Height = fmt.Sprintf("%d cm", *p.HeightCM)
	}
	if p.AccountKind == "observer" || p.AccountStatus == "observer" {
		v.AccountKindLabel = "观察员"
	}
	if len(v.Photos) > 0 {
		v.CoverURL = v.Photos[0].ThumbURL
		for _, photo := range v.Photos {
			if photo.IsCover {
				v.CoverURL = photo.ThumbURL
				break
			}
		}
	}
	v.IsObserver = p.AccountKind == "observer" || p.AccountStatus == "observer"
	v.RegisteredAt = profileTimeValue(*profileRegisteredAt(p))
	meta := []string{v.UserID}
	if v.Gender != "" {
		meta = append(meta, v.Gender)
	}
	meta = append(meta, v.AccountStatusLabel)
	v.MetaLine = strings.Join(meta, " · ")

	switch p.AccountStatus {
	case "active":
		v.CanToggleStatus = true
		v.NextStatus = "matched"
		v.ToggleLabel = "切换为已脱单"
	case "matched":
		v.CanToggleStatus = true
		v.NextStatus = "active"
		v.ToggleLabel = "恢复单身"
	}
	return v
}

func buildPublicProfileView(p db.Profile) publicProfileView {
	v := publicProfileView{
		Nickname: p.Nickname,
		Gender:   p.Gender,
		Birthday: profileBirthdayShort(p.Birthday),
		UserID:   profileUserID(p),
		Sections: profileSections(p, false),
		Photos:   profilePhotoViews(p.Photos),
	}
	if p.HeightCM != nil {
		v.Height = fmt.Sprintf("%d cm", *p.HeightCM)
	}
	if len(v.Photos) > 0 {
		v.CoverURL = v.Photos[0].ThumbURL
		v.CoverFullURL = v.Photos[0].URL
	}
	meta := []string{v.UserID}
	if v.Birthday != "" {
		meta = append(meta, v.Birthday)
	}
	if v.Gender != "" {
		meta = append(meta, v.Gender)
	}
	v.MetaLine = strings.Join(meta, " · ")
	return v
}

func profileSelectOptions(current string, values []string) []profileEditOption {
	out := make([]profileEditOption, 0, len(values))
	for _, value := range values {
		out = append(out, profileEditOption{Value: value, Selected: value == current})
	}
	return out
}

func profileMultiOptions(current, values []string) []profileEditOption {
	present := make(map[string]struct{}, len(current))
	for _, item := range current {
		present[item] = struct{}{}
	}
	out := make([]profileEditOption, 0, len(values))
	for _, value := range values {
		_, ok := present[value]
		out = append(out, profileEditOption{Value: value, Selected: ok})
	}
	return out
}

func profileNumberValue(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func buildProfileEditView(p db.Profile) profileEditView {
	text := func(name, label, value string) profileEditField {
		return profileEditField{Name: name, Label: label, Type: "text", Value: value, Placeholder: "请输入" + label}
	}
	basic := []profileEditField{
		{Name: "nickname", Label: "昵称", Type: "text", Value: p.Nickname, Placeholder: "请输入昵称", Required: true},
		{Name: "height_cm", Label: "身高（cm）", Type: "number", Value: profileNumberValue(p.HeightCM), Min: 100, Max: 250, Placeholder: "100-250"},
		{Name: "education", Label: "学历", Type: "select", Value: p.Education, Options: profileSelectOptions(p.Education, db.ProfileEducationOptions)},
		text("city", "现居/工作城市", p.City),
		text("native_place", "家乡", p.NativePlace),
		text("church", "经常去的教会", p.Church),
		text("church_group", "参加的团体", p.ChurchGroup),
		text("church_location", "教会所在省-市-区", p.ChurchLocation),
	}
	work := []profileEditField{
		text("industry", "从事行业", p.Industry),
		text("position", "职位", p.Position),
		{Name: "income", Label: "年收入", Type: "select", Value: p.Income, Options: profileSelectOptions(p.Income, db.ProfileIncomeOptions)},
		{Name: "house", Label: "房产状况", Type: "select", Value: p.House, Options: profileSelectOptions(p.House, db.ProfileYesNoOptions)},
		text("house_note_have", "房产补充（有房，公开）", p.HouseNoteHave),
		text("house_note_none", "房产补充（无房，公开）", p.HouseNoteNone),
		{Name: "driving", Label: "汽车", Type: "select", Value: p.Driving, Options: profileSelectOptions(p.Driving, db.ProfileYesNoOptions)},
		text("driving_note_have", "车辆补充（有车，公开）", p.DrivingNoteHave),
		text("driving_note_none", "车辆补充（无车，公开）", p.DrivingNoteNone),
	}
	about := []profileEditField{
		{Name: "personality", Label: "我是怎样的人", Type: "textarea", Value: p.Personality, Rows: 4, Placeholder: "介绍一下自己"},
		{Name: "family", Label: "家庭情况", Type: "textarea", Value: p.Family, Rows: 3, Placeholder: "家庭成员与情况"},
		{Name: "live_with_parents", Label: "婚后与父母同住", Type: "select", Value: p.LiveWithParents, Options: profileSelectOptions(p.LiveWithParents, db.ProfileLiveWithOptions)},
	}
	aboutMulti := []profileEditMulti{
		{Name: "self_traits", Label: "性格", Options: profileMultiOptions(p.SelfTraits, db.ProfileTraitOptions)},
		{Name: "self_hobbies", Label: "爱好", Options: profileMultiOptions(p.SelfHobbies, db.ProfileHobbyOptions)},
		{Name: "self_sports", Label: "运动", Options: profileMultiOptions(p.SelfSports, db.ProfileSportOptions)},
		{Name: "mbti", Label: "MBTI", Options: profileMultiOptions(p.MBTI, db.ProfileMBTIOptions)},
	}
	partner := []profileEditField{
		{Name: "partner_criteria", Label: "理想中的 TA", Type: "textarea", Value: p.PartnerCriteria, Rows: 4, Placeholder: "描述你理想中的另一半"},
	}
	partnerMulti := []profileEditMulti{
		{Name: "partner_traits", Label: "TA 的性格", Options: profileMultiOptions(p.PartnerTraits, db.ProfileTraitOptions)},
		{Name: "partner_hobbies", Label: "TA 的爱好", Options: profileMultiOptions(p.PartnerHobbies, db.ProfileHobbyOptions)},
		{Name: "partner_sports", Label: "TA 的运动", Options: profileMultiOptions(p.PartnerSports, db.ProfileSportOptions)},
	}
	contact := []profileEditField{
		text("wechat", "微信号", p.Wechat),
		{Name: "phone", Label: "手机号码", Type: "tel", Value: p.Phone, Placeholder: "手机号码", Required: true},
	}

	v := profileEditView{
		Sections: []profileEditSection{
			{Title: "基本信息", Icon: "👤", Fields: basic},
			{Title: "工作与经济", Icon: "💼", Fields: work},
			{Title: "关于我", Icon: "✨", Fields: about, Multi: aboutMulti},
			{Title: "理想中的 TA", Icon: "💖", Fields: partner, Multi: partnerMulti},
			{Title: "联络方式", Icon: "📞", Fields: contact},
		},
		Photos:      profilePhotoViews(p.Photos),
		CanAddPhoto: len(p.Photos) < db.MaxProfilePhotos,
		MaxPhotos:   db.MaxProfilePhotos,
		Birthday:    profileBirthdayShort(p.Birthday),
		IDCardLast4: p.IDCardLast4,
		IDCardValid: p.IDCardValid,
	}
	return v
}

// applyProfileUpdate lets an invalid form re-render the values the user just
// submitted instead of silently discarding them.
func applyProfileUpdate(p *db.Profile, u db.ProfileUpdate) {
	setString := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	setList := func(dst *[]string, src *[]string) {
		if src != nil {
			*dst = append([]string(nil), (*src)...)
		}
	}
	setString(&p.Nickname, u.Nickname)
	if u.HeightCM != nil {
		value := *u.HeightCM
		p.HeightCM = &value
	}
	setString(&p.Education, u.Education)
	setString(&p.City, u.City)
	setString(&p.Church, u.Church)
	setString(&p.ChurchGroup, u.ChurchGroup)
	setString(&p.ChurchLocation, u.ChurchLocation)
	setString(&p.NativePlace, u.NativePlace)
	setString(&p.Industry, u.Industry)
	setString(&p.Position, u.Position)
	setString(&p.Income, u.Income)
	setString(&p.House, u.House)
	setString(&p.Driving, u.Driving)
	setString(&p.Personality, u.Personality)
	setString(&p.Family, u.Family)
	setString(&p.LiveWithParents, u.LiveWithParents)
	setString(&p.PartnerCriteria, u.PartnerCriteria)
	setList(&p.SelfTraits, u.SelfTraits)
	setList(&p.SelfHobbies, u.SelfHobbies)
	setList(&p.SelfSports, u.SelfSports)
	setList(&p.MBTI, u.MBTI)
	setList(&p.PartnerTraits, u.PartnerTraits)
	setList(&p.PartnerHobbies, u.PartnerHobbies)
	setList(&p.PartnerSports, u.PartnerSports)
	setString(&p.Wechat, u.Wechat)
	setString(&p.Phone, u.Phone)
}

// profileUpdateFromForm reads only the whitelisted form fields. A blank text
// input clears that field; a blank height means "leave unchanged".
func profileUpdateFromForm(r *http.Request) (db.ProfileUpdate, error) {
	text := func(name string) *string {
		value := r.PostFormValue(name)
		return &value
	}
	list := func(name string) *[]string {
		values := append([]string(nil), r.PostForm[name]...)
		return &values
	}
	upd := db.ProfileUpdate{
		Nickname:        text("nickname"),
		Education:       text("education"),
		City:            text("city"),
		Church:          text("church"),
		ChurchGroup:     text("church_group"),
		ChurchLocation:  text("church_location"),
		NativePlace:     text("native_place"),
		Industry:        text("industry"),
		Position:        text("position"),
		Income:          text("income"),
		House:           text("house"),
		Driving:         text("driving"),
		Personality:     text("personality"),
		Family:          text("family"),
		LiveWithParents: text("live_with_parents"),
		PartnerCriteria: text("partner_criteria"),
		SelfTraits:      list("self_traits"),
		SelfHobbies:     list("self_hobbies"),
		SelfSports:      list("self_sports"),
		MBTI:            list("mbti"),
		PartnerTraits:   list("partner_traits"),
		PartnerHobbies:  list("partner_hobbies"),
		PartnerSports:   list("partner_sports"),
		Wechat:          text("wechat"),
		Phone:           text("phone"),
	}
	if raw := strings.TrimSpace(r.PostFormValue("height_cm")); raw != "" {
		height, err := strconv.Atoi(raw)
		if err != nil {
			return db.ProfileUpdate{}, &db.ValidationError{
				Field:   "height_cm",
				Message: "身高需为 100-250 的整数。",
			}
		}
		upd.HeightCM = &height
	}
	return upd, nil
}

func profileUpdateFieldNames(upd db.ProfileUpdate) []string {
	var names []string
	add := func(name string, present bool) {
		if present {
			names = append(names, name)
		}
	}
	add("nickname", upd.Nickname != nil)
	add("height_cm", upd.HeightCM != nil)
	add("birthday", upd.Birthday != nil)
	add("education", upd.Education != nil)
	add("city", upd.City != nil)
	add("church", upd.Church != nil)
	add("church_group", upd.ChurchGroup != nil)
	add("church_location", upd.ChurchLocation != nil)
	add("native_place", upd.NativePlace != nil)
	add("industry", upd.Industry != nil)
	add("position", upd.Position != nil)
	add("income", upd.Income != nil)
	add("house", upd.House != nil)
	add("driving", upd.Driving != nil)
	add("personality", upd.Personality != nil)
	add("family", upd.Family != nil)
	add("live_with_parents", upd.LiveWithParents != nil)
	add("partner_criteria", upd.PartnerCriteria != nil)
	add("self_traits", upd.SelfTraits != nil)
	add("self_hobbies", upd.SelfHobbies != nil)
	add("self_sports", upd.SelfSports != nil)
	add("mbti", upd.MBTI != nil)
	add("partner_traits", upd.PartnerTraits != nil)
	add("partner_hobbies", upd.PartnerHobbies != nil)
	add("partner_sports", upd.PartnerSports != nil)
	add("wechat", upd.Wechat != nil)
	add("phone", upd.Phone != nil)
	return names
}

// profileFlash maps fixed query codes to messages so arbitrary query text is
// never reflected back into a page.
func profileFlash(r *http.Request) (notice, errMessage string) {
	q := r.URL.Query()
	switch q.Get("updated") {
	case "1":
		notice = "资料已更新。"
	}
	switch q.Get("photo_added") {
	case "1":
		notice = "照片已新增。"
	}
	switch q.Get("photo_deleted") {
	case "1":
		notice = "照片已删除。"
	}
	switch q.Get("photo_cover") {
	case "1":
		notice = "封面已更新。"
	}
	if q.Get("feedback") == "1" {
		notice = "反馈已提交，谢谢你的提醒。"
	}
	switch q.Get("status") {
	case "active":
		notice = "已恢复单身。"
	case "matched":
		notice = "已切换为已脱单。"
	}
	switch q.Get("photo_error") {
	case "":
	case "heic":
		errMessage = "iPhone HEIC 照片请先在相册转成 JPEG，或关闭「高效」格式后再上传。"
	case "too_large":
		errMessage = "文件超过大小上限。"
	case "unsupported":
		errMessage = "图片格式不支持，请使用 JPEG/PNG/WebP/GIF/BMP/TIFF。"
	case "dimensions":
		errMessage = "图片尺寸过大。"
	case "no_file":
		errMessage = "请选择要上传的文件。"
	case "limit":
		errMessage = fmt.Sprintf("最多只能上传 %d 张照片。", db.MaxProfilePhotos)
	default:
		errMessage = "照片处理失败，请稍后再试。"
	}
	switch q.Get("status_error") {
	case "":
	case "signup":
		errMessage = "你已报名活动，请先取消报名再脱单。"
	case "observer":
		errMessage = "观察员账号不可切换账号状态。"
	case "not_allowed":
		errMessage = "目前账号状态不支持切换。"
	case "invalid":
		errMessage = "账号状态不正确。"
	default:
		errMessage = "账号状态更新失败，请稍后再试。"
	}
	return notice, errMessage
}
