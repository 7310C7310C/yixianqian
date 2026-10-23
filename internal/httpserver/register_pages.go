package httpserver

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

const (
	registerMinPhotos = 3
	registerMaxPhotos = 9
	registerAngelAck  = "我会记得的"
)

type registerFieldKind string

const (
	registerKindText     registerFieldKind = "text"
	registerKindTextarea registerFieldKind = "textarea"
	registerKindTel      registerFieldKind = "tel"
	registerKindDate     registerFieldKind = "date"
	registerKindNumber   registerFieldKind = "number"
	registerKindSelect   registerFieldKind = "select"
	registerKindMulti    registerFieldKind = "multi"
	registerKindCheckbox registerFieldKind = "checkbox"
)

type registerFieldSpec struct {
	Name        string
	Label       string
	Kind        registerFieldKind
	Required    bool
	MaxRunes    int
	Placeholder string
	Hint        string
	Options     []string
	Rows        int
}

type registerSectionSpec struct {
	Title  string
	Fields []registerFieldSpec
}

var (
	registerGenderOptions   = []string{"男性", "女性"}
	registerMarriageOptions = []string{"没结过婚", "结过婚"}
	registerChildOptions    = []string{
		"是的，我替子女报名，我会让子女注册",
		"不是，我为自己报名",
	}
)

// registerSections mirrors the legacy Feishu registration form. The order and
// option values stay compatible with the old data so migration and profile
// editing see the same strings.
func registerSections() []registerSectionSpec {
	return []registerSectionSpec{
		{
			Title: "基本资料",
			Fields: []registerFieldSpec{
				{Name: "name", Label: "姓名", Kind: registerKindText, Required: true, MaxRunes: 100,
					Hint: "不公开，请填写真实姓名。"},
				{Name: "nickname", Label: "昵称", Kind: registerKindText, Required: true, MaxRunes: 30,
					Placeholder: "大家怎么称呼你", Hint: "会公开；如介意可使用不常用的名字。"},
				{Name: "gender", Label: "性别", Kind: registerKindSelect, Required: true, Options: registerGenderOptions},
				{Name: "birthday", Label: "生日", Kind: registerKindDate, Required: true},
				{Name: "phone", Label: "手机号码", Kind: registerKindTel, Required: true, MaxRunes: 20,
					Placeholder: "手机号", Hint: "不公开；登录账号与审核联络使用。"},
				{Name: "id_card", Label: "身份证号码", Kind: registerKindText, Required: true, MaxRunes: 30,
					Hint: "不公开，仅供活动保险与人工审核；系统只显示尾号。"},
				{Name: "wechat", Label: "微信号", Kind: registerKindText, Required: true, MaxRunes: 200,
					Hint: "不公开，仅在相互喜欢后公开。"},
			},
		},
		{
			Title: "教会与家乡",
			Fields: []registerFieldSpec{
				{Name: "baptismal_name", Label: "圣名", Kind: registerKindText, Required: true, MaxRunes: 200},
				{Name: "church", Label: "经常去的教堂", Kind: registerKindText, Required: true, MaxRunes: 200,
					Placeholder: "例如，深圳南头堂"},
				{Name: "church_group", Label: "参加的团体", Kind: registerKindText, MaxRunes: 200,
					Placeholder: "例如，圣咏团、服务组"},
				{Name: "church_location", Label: "教堂所在市-区", Kind: registerKindText, Required: true, MaxRunes: 200,
					Placeholder: "例如，深圳南山"},
				{Name: "native_place", Label: "家乡", Kind: registerKindText, Required: true, MaxRunes: 200,
					Placeholder: "例如，广东广州"},
				{Name: "city", Label: "现居/工作城市", Kind: registerKindText, Required: true, MaxRunes: 200,
					Placeholder: "例如，广东深圳"},
				{Name: "family", Label: "家庭成员情况", Kind: registerKindTextarea, Required: true,
					MaxRunes: 2000, Rows: 3, Placeholder: "例如，有几个兄弟姐妹"},
			},
		},
		{
			Title: "工作与生活",
			Fields: []registerFieldSpec{
				{Name: "height_cm", Label: "身高（cm）", Kind: registerKindNumber, Required: true},
				{Name: "education", Label: "学历", Kind: registerKindSelect, Required: true,
					Options: db.ProfileEducationOptions},
				{Name: "industry", Label: "从事行业", Kind: registerKindText, Required: true, MaxRunes: 200,
					Hint: "如自己做生意，请注明。"},
				{Name: "position", Label: "职位", Kind: registerKindText, Required: true, MaxRunes: 200},
				{Name: "income", Label: "年收入", Kind: registerKindSelect, Options: db.ProfileIncomeOptions,
					Hint: "如不方便可留空。"},
				{Name: "house", Label: "房产状况", Kind: registerKindSelect, Options: db.ProfileYesNoOptions,
					Hint: "如不方便可留空。"},
				{Name: "driving", Label: "是否有车", Kind: registerKindSelect, Options: db.ProfileYesNoOptions,
					Hint: "如不方便可留空。"},
				{Name: "marriage", Label: "你结过婚吗？", Kind: registerKindSelect, Required: true,
					Options: registerMarriageOptions, Hint: "不公开。"},
				{Name: "live_with_parents", Label: "婚后是否与父母同住", Kind: registerKindSelect, Required: true,
					Options: db.ProfileLiveWithOptions},
			},
		},
		{
			Title: "关于我",
			Fields: []registerFieldSpec{
				{Name: "personality", Label: "我是一个怎样的人", Kind: registerKindTextarea, Required: true,
					MaxRunes: 2000, Rows: 5},
				{Name: "self_traits", Label: "我的性格", Kind: registerKindMulti, Required: true,
					Options: db.ProfileTraitOptions, Hint: "可多选。"},
				{Name: "self_hobbies", Label: "我的爱好", Kind: registerKindMulti, Required: true,
					Options: db.ProfileHobbyOptions, Hint: "可多选。"},
				{Name: "self_sports", Label: "我的运动", Kind: registerKindMulti, Required: true,
					Options: db.ProfileSportOptions, Hint: "可多选。"},
				{Name: "mbti", Label: "我的 MBTI 人格", Kind: registerKindMulti, Required: true,
					Options: db.ProfileMBTIOptions, Hint: "一般选 4 个。"},
			},
		},
		{
			Title: "理想中的 TA",
			Fields: []registerFieldSpec{
				{Name: "partner_criteria", Label: "我理想中的 TA", Kind: registerKindTextarea, Required: true,
					MaxRunes: 2000, Rows: 5},
				{Name: "partner_traits", Label: "TA 的性格", Kind: registerKindMulti, Required: true,
					Options: db.ProfileTraitOptions, Hint: "可多选。"},
				{Name: "partner_hobbies", Label: "TA 的爱好", Kind: registerKindMulti, Required: true,
					Options: db.ProfileHobbyOptions, Hint: "可多选。"},
				{Name: "partner_sports", Label: "TA 的运动", Kind: registerKindMulti, Required: true,
					Options: db.ProfileSportOptions, Hint: "可多选。"},
			},
		},
		{
			Title: "其他",
			Fields: []registerFieldSpec{
				{Name: "is_for_child", Label: "您替子女注册吗？", Kind: registerKindSelect, Required: true,
					Options: registerChildOptions, Hint: "如替子女注册，请使用子女的手机号码。"},
				{Name: "referral_source", Label: "你是怎么知道一线牵的？", Kind: registerKindText, Required: true,
					MaxRunes: 200},
				{Name: "inviter", Label: "邀请人 ID", Kind: registerKindText, MaxRunes: 50,
					Placeholder: "例如：U-0001", Hint: "没有邀请人可留空。"},
				{Name: "angel_ack", Label: "记得加小天使微信号，并备注「真实姓名」。", Kind: registerKindCheckbox,
					Required: true, Options: []string{registerAngelAck}},
			},
		},
	}
}

func registerFields() []registerFieldSpec {
	var out []registerFieldSpec
	for _, section := range registerSections() {
		out = append(out, section.Fields...)
	}
	return out
}

func registerSpecByName(name string) (registerFieldSpec, bool) {
	for _, spec := range registerFields() {
		if spec.Name == name {
			return spec, true
		}
	}
	return registerFieldSpec{}, false
}

type registerOptionView struct {
	Value    string
	Selected bool
}

type registerFieldView struct {
	Name        string
	Label       string
	Type        string
	Required    bool
	Value       string
	Option      string
	Checked     bool
	MaxLength   int
	Placeholder string
	Hint        string
	Rows        int
	Options     []registerOptionView
}

type registerSectionView struct {
	Title  string
	Fields []registerFieldView
}

type registerPageData struct {
	Sections  []registerSectionView
	MinPhotos int
	MaxPhotos int
	MaxMB     int
}

// registerFormValues keeps only safe-to-reflect values when a submission is
// rejected. The ID card and passwords are deliberately never echoed back.
func registerFormValues(values url.Values) url.Values {
	if values == nil {
		values = url.Values{}
	}
	clean := url.Values{}
	for _, spec := range registerFields() {
		items, ok := values[spec.Name]
		if !ok {
			continue
		}
		if spec.Name == "id_card" {
			continue
		}
		copied := make([]string, 0, len(items))
		for _, item := range items {
			copied = append(copied, item)
		}
		clean[spec.Name] = copied
	}
	return clean
}

func buildRegisterPageData(values url.Values) registerPageData {
	values = registerFormValues(values)
	data := registerPageData{MinPhotos: registerMinPhotos, MaxPhotos: registerMaxPhotos}
	for _, section := range registerSections() {
		view := registerSectionView{Title: section.Title}
		for _, spec := range section.Fields {
			field := registerFieldView{
				Name:        spec.Name,
				Label:       spec.Label,
				Type:        string(spec.Kind),
				Required:    spec.Required,
				MaxLength:   spec.MaxRunes,
				Placeholder: spec.Placeholder,
				Hint:        spec.Hint,
				Rows:        spec.Rows,
			}
			switch spec.Kind {
			case registerKindMulti:
				selected := make(map[string]bool)
				for _, item := range values[spec.Name] {
					selected[strings.TrimSpace(item)] = true
				}
				for _, option := range spec.Options {
					field.Options = append(field.Options, registerOptionView{
						Value: option, Selected: selected[option],
					})
				}
			case registerKindCheckbox:
				field.Option = spec.Options[0]
				for _, item := range values[spec.Name] {
					if strings.TrimSpace(item) == field.Option {
						field.Checked = true
					}
				}
			case registerKindSelect:
				value := strings.TrimSpace(values.Get(spec.Name))
				field.Value = value
				for _, option := range spec.Options {
					field.Options = append(field.Options, registerOptionView{
						Value: option, Selected: option == value,
					})
				}
			default:
				field.Value = strings.TrimSpace(values.Get(spec.Name))
			}
			view.Fields = append(view.Fields, field)
		}
		data.Sections = append(data.Sections, view)
	}
	return data
}

type registerFormError struct {
	Field   string
	Message string
}

func (e *registerFormError) Error() string { return e.Message }

type registerInput struct {
	Phone           string
	Name            string
	Nickname        string
	Gender          string
	Birthday        time.Time
	HeightCM        int
	Education       string
	BaptismalName   string
	Church          string
	ChurchLocation  string
	ChurchGroup     string
	NativePlace     string
	City            string
	Industry        string
	Position        string
	Income          string
	House           string
	Driving         string
	Marriage        string
	Family          string
	LiveWithParents string
	Personality     string
	SelfTraits      []string
	SelfHobbies     []string
	SelfSports      []string
	MBTI            []string
	PartnerCriteria string
	PartnerTraits   []string
	PartnerHobbies  []string
	PartnerSports   []string
	Wechat          string
	IDCard          string
	Inviter         string
	ReferralSource  string
	IsForChild      string
	Password        string
}

func registerFormErr(field, message string) *registerFormError {
	return &registerFormError{Field: field, Message: message}
}

func validateRegisterText(values url.Values, name string) (string, *registerFormError) {
	spec, ok := registerSpecByName(name)
	if !ok {
		return "", registerFormErr(name, "表单字段不正确。")
	}
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		if spec.Required {
			return "", registerFormErr(name, "请填写："+spec.Label)
		}
		return "", nil
	}
	if spec.MaxRunes > 0 && utf8.RuneCountInString(raw) > spec.MaxRunes {
		return "", registerFormErr(name, fmt.Sprintf("「%s」最多 %d 个字。", spec.Label, spec.MaxRunes))
	}
	return raw, nil
}

func validateRegisterSelect(values url.Values, name string) (string, *registerFormError) {
	spec, ok := registerSpecByName(name)
	if !ok {
		return "", registerFormErr(name, "表单字段不正确。")
	}
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		if spec.Required {
			return "", registerFormErr(name, "请选择："+spec.Label)
		}
		return "", nil
	}
	if !containsString(spec.Options, raw) {
		return "", registerFormErr(name, "「"+spec.Label+"」选项不正确。")
	}
	return raw, nil
}

func validateRegisterMulti(values url.Values, name string) ([]string, *registerFormError) {
	spec, ok := registerSpecByName(name)
	if !ok {
		return nil, registerFormErr(name, "表单字段不正确。")
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(values[name]))
	for _, raw := range values[name] {
		item := strings.TrimSpace(raw)
		if item == "" || seen[item] {
			continue
		}
		if !containsString(spec.Options, item) {
			return nil, registerFormErr(name, "「"+spec.Label+"」选项不正确。")
		}
		seen[item] = true
		out = append(out, item)
	}
	if spec.Required && len(out) == 0 {
		return nil, registerFormErr(name, "请选择："+spec.Label)
	}
	if len(out) > 30 {
		return nil, registerFormErr(name, "「"+spec.Label+"」选项过多。")
	}
	return out, nil
}

func parseRegisterInput(values url.Values) (registerInput, *registerFormError) {
	var in registerInput
	var err *registerFormError

	if in.Name, err = validateRegisterText(values, "name"); err != nil {
		return in, err
	}
	if in.Nickname, err = validateRegisterText(values, "nickname"); err != nil {
		return in, err
	}
	if in.Gender, err = validateRegisterSelect(values, "gender"); err != nil {
		return in, err
	}

	birthdayRaw, err := validateRegisterText(values, "birthday")
	if err != nil {
		return in, err
	}
	birthday, perr := time.Parse("2006-01-02", birthdayRaw)
	if perr != nil || birthday.Year() < 1900 || birthday.After(time.Now().AddDate(0, 0, 1)) {
		return in, registerFormErr("birthday", "生日格式不正确。")
	}
	in.Birthday = birthday

	phoneRaw, err := validateRegisterText(values, "phone")
	if err != nil {
		return in, err
	}
	in.Phone = auth.NormalizePhone(phoneRaw)
	if in.Phone == "" {
		return in, registerFormErr("phone", "手机号码格式不正确。")
	}

	// The ID card is normalised before length checking because users often
	// paste it with spaces.
	idRaw := strings.ToUpper(strings.Join(strings.Fields(values.Get("id_card")), ""))
	if idRaw == "" {
		return in, registerFormErr("id_card", "请填写：身份证号码")
	}
	normalizedID, idBirth, idErr := validateChineseIDCard(idRaw)
	if idErr != nil {
		return in, registerFormErr("id_card", idErr.Error())
	}
	if !sameCalendarDay(idBirth, birthday) {
		return in, registerFormErr("id_card", "身份证上的出生日期与填写的生日不一致。")
	}
	in.IDCard = normalizedID

	if in.Wechat, err = validateRegisterText(values, "wechat"); err != nil {
		return in, err
	}
	if in.BaptismalName, err = validateRegisterText(values, "baptismal_name"); err != nil {
		return in, err
	}
	if in.Church, err = validateRegisterText(values, "church"); err != nil {
		return in, err
	}
	if in.ChurchGroup, err = validateRegisterText(values, "church_group"); err != nil {
		return in, err
	}
	if in.ChurchLocation, err = validateRegisterText(values, "church_location"); err != nil {
		return in, err
	}
	if in.NativePlace, err = validateRegisterText(values, "native_place"); err != nil {
		return in, err
	}
	if in.City, err = validateRegisterText(values, "city"); err != nil {
		return in, err
	}
	if in.Family, err = validateRegisterText(values, "family"); err != nil {
		return in, err
	}

	heightRaw, err := validateRegisterText(values, "height_cm")
	if err != nil {
		return in, err
	}
	height, convErr := strconv.Atoi(heightRaw)
	if convErr != nil || height < 100 || height > 250 {
		return in, registerFormErr("height_cm", "身高需在 100-250 公分之间。")
	}
	in.HeightCM = height

	if in.Education, err = validateRegisterSelect(values, "education"); err != nil {
		return in, err
	}
	if in.Industry, err = validateRegisterText(values, "industry"); err != nil {
		return in, err
	}
	if in.Position, err = validateRegisterText(values, "position"); err != nil {
		return in, err
	}
	if in.Income, err = validateRegisterSelect(values, "income"); err != nil {
		return in, err
	}
	if in.House, err = validateRegisterSelect(values, "house"); err != nil {
		return in, err
	}
	if in.Driving, err = validateRegisterSelect(values, "driving"); err != nil {
		return in, err
	}
	if in.Marriage, err = validateRegisterSelect(values, "marriage"); err != nil {
		return in, err
	}
	if in.LiveWithParents, err = validateRegisterSelect(values, "live_with_parents"); err != nil {
		return in, err
	}

	if in.Personality, err = validateRegisterText(values, "personality"); err != nil {
		return in, err
	}
	if in.SelfTraits, err = validateRegisterMulti(values, "self_traits"); err != nil {
		return in, err
	}
	if in.SelfHobbies, err = validateRegisterMulti(values, "self_hobbies"); err != nil {
		return in, err
	}
	if in.SelfSports, err = validateRegisterMulti(values, "self_sports"); err != nil {
		return in, err
	}
	if in.MBTI, err = validateRegisterMulti(values, "mbti"); err != nil {
		return in, err
	}

	if in.PartnerCriteria, err = validateRegisterText(values, "partner_criteria"); err != nil {
		return in, err
	}
	if in.PartnerTraits, err = validateRegisterMulti(values, "partner_traits"); err != nil {
		return in, err
	}
	if in.PartnerHobbies, err = validateRegisterMulti(values, "partner_hobbies"); err != nil {
		return in, err
	}
	if in.PartnerSports, err = validateRegisterMulti(values, "partner_sports"); err != nil {
		return in, err
	}

	if in.IsForChild, err = validateRegisterSelect(values, "is_for_child"); err != nil {
		return in, err
	}
	if in.ReferralSource, err = validateRegisterText(values, "referral_source"); err != nil {
		return in, err
	}
	if in.Inviter, err = validateRegisterText(values, "inviter"); err != nil {
		return in, err
	}
	in.Inviter = strings.ToUpper(in.Inviter)

	ack := values["angel_ack"]
	acked := false
	for _, item := range ack {
		if strings.TrimSpace(item) == registerAngelAck {
			acked = true
		}
	}
	if !acked {
		return in, registerFormErr("angel_ack", "请确认已加小天使微信号并备注真实姓名。")
	}

	in.Password = values.Get("password")
	if in.Password != values.Get("password_confirm") {
		return in, registerFormErr("password", "两次输入的密码不一致。")
	}
	if pwErr := auth.ValidatePassword(in.Password); pwErr != nil {
		return in, registerFormErr("password", passwordPolicyMessage(pwErr))
	}
	return in, nil
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
