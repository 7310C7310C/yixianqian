package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The v1 profile form speaks Bitable field names. Keep the mapping in one
// place so the PUT handler and the editable schema cannot drift apart.
const (
	legacyFieldNickname        = "昵称"
	legacyFieldHeight          = "身高（cm）"
	legacyFieldEducation       = "学历"
	legacyFieldCity            = "现居/工作城市"
	legacyFieldChurch          = "经常去的教堂"
	legacyFieldChurchGroup     = "参加的团体"
	legacyFieldChurchLocation  = "教堂所在市-区"
	legacyFieldIndustry        = "从事行业"
	legacyFieldPosition        = "职位"
	legacyFieldIncome          = "年收入"
	legacyFieldHouse           = "房产状况"
	legacyFieldDriving         = "是否有车"
	legacyFieldPersonality     = "我是一个怎样的人"
	legacyFieldSelfTraits      = "我是一个怎样的人-性格"
	legacyFieldSelfHobbies     = "我是一个怎样的人-爱好"
	legacyFieldSelfSports      = "我是一个怎样的人-运动"
	legacyFieldMBTI            = "我是个怎样的人-MBTI人格"
	legacyFieldLiveWithParents = "婚后是否与父母同住"
	legacyFieldFamily          = "家庭成员情况"
	legacyFieldPartnerCriteria = "理想中的TA"
	legacyFieldPartnerTraits   = "理想中的TA-性格"
	legacyFieldPartnerHobbies  = "理想中的TA-爱好"
	legacyFieldPartnerSports   = "理想中的TA-运动"
	legacyFieldWechat          = "微信号"
	legacyFieldPhone           = "手机号"
	legacyFieldIDCard          = "身份证号"
)

type legacyEditableField struct {
	Field   string `json:"field"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Options any    `json:"options"`
	Long    bool   `json:"long"`
	Value   any    `json:"value"`
	Hint    string `json:"hint,omitempty"`
}

// legacyProfileEditable mirrors the v1 `_editable_schema` payload. The SPA
// builds the whole edit form from this list, so it must keep the legacy field
// names and value types.
func legacyProfileEditable(p db.Profile) []legacyEditableField {
	height := any("")
	if p.HeightCM != nil && *p.HeightCM > 0 {
		height = *p.HeightCM
	}
	fields := []legacyEditableField{
		{Field: legacyFieldNickname, Label: "昵称", Type: "text", Value: p.Nickname},
		{Field: legacyFieldHeight, Label: "身高", Type: "number", Value: height},
		{Field: legacyFieldEducation, Label: "学历", Type: "select", Options: db.ProfileEducationOptions, Value: p.Education},
		{Field: legacyFieldCity, Label: "现居/工作城市", Type: "text", Value: p.City},
		{Field: legacyFieldChurch, Label: "经常去的教堂", Type: "text", Value: p.Church},
		{Field: legacyFieldChurchGroup, Label: "参加的团体", Type: "text", Value: p.ChurchGroup},
		{Field: legacyFieldChurchLocation, Label: "教堂所在市-区", Type: "text", Value: p.ChurchLocation},
		{Field: legacyFieldIndustry, Label: "从事行业", Type: "text", Value: p.Industry},
		{Field: legacyFieldPosition, Label: "职位", Type: "text", Value: p.Position},
		{Field: legacyFieldIncome, Label: "年收入", Type: "select", Options: db.ProfileIncomeOptions, Value: p.Income},
		{Field: legacyFieldHouse, Label: "房产状况", Type: "select", Options: db.ProfileYesNoOptions, Value: p.House},
		{Field: legacyFieldDriving, Label: "汽车", Type: "select", Options: db.ProfileYesNoOptions, Value: p.Driving},
		{Field: legacyFieldPersonality, Label: "我是怎样的人", Type: "text", Long: true, Value: p.Personality},
		{Field: legacyFieldSelfTraits, Label: "性格", Type: "multi", Options: db.ProfileTraitOptions, Value: legacyStringList(p.SelfTraits)},
		{Field: legacyFieldSelfHobbies, Label: "爱好", Type: "multi", Options: db.ProfileHobbyOptions, Value: legacyStringList(p.SelfHobbies)},
		{Field: legacyFieldSelfSports, Label: "运动", Type: "multi", Options: db.ProfileSportOptions, Value: legacyStringList(p.SelfSports)},
		{Field: legacyFieldMBTI, Label: "MBTI", Type: "multi", Options: db.ProfileMBTIOptions, Value: legacyStringList(p.MBTI)},
		{Field: legacyFieldLiveWithParents, Label: "婚后与父母同住", Type: "select", Options: db.ProfileLiveWithOptions, Value: p.LiveWithParents},
		{Field: legacyFieldFamily, Label: "家庭情况", Type: "text", Long: true, Value: p.Family},
		{Field: legacyFieldPartnerCriteria, Label: "理想的TA", Type: "text", Long: true, Value: p.PartnerCriteria},
		{Field: legacyFieldPartnerTraits, Label: "TA的性格", Type: "multi", Options: db.ProfileTraitOptions, Value: legacyStringList(p.PartnerTraits)},
		{Field: legacyFieldPartnerHobbies, Label: "TA的爱好", Type: "multi", Options: db.ProfileHobbyOptions, Value: legacyStringList(p.PartnerHobbies)},
		{Field: legacyFieldPartnerSports, Label: "TA的运动", Type: "multi", Options: db.ProfileSportOptions, Value: legacyStringList(p.PartnerSports)},
		{Field: legacyFieldWechat, Label: "微信号", Type: "text", Value: p.Wechat},
		{Field: legacyFieldPhone, Label: "手机号", Type: "phone", Value: p.Phone},
	}
	// v1 exposed the ID card only while it was missing/invalid; the input is
	// intentionally empty so the server never echoes plaintext back.
	observer := p.AccountKind == "observer" || p.AccountStatus == "observer"
	if !p.IDCardValid && !observer {
		fields = append([]legacyEditableField{{
			Field: legacyFieldIDCard,
			Label: "身份证号",
			Type:  "text",
			Value: "",
			Hint:  "身份证号有误或缺失，请重新填写正确的18位身份证号；仅核验使用，不公开展示。",
		}}, fields...)
	}
	return fields
}

func legacyStringList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func legacyProfilePhotoURL(id string) string {
	return "/profile/photo/" + id
}

func legacyProfilePhotoURLs(photos []db.ProfilePhoto) []string {
	out := make([]string, 0, len(photos))
	for _, photo := range photos {
		out = append(out, legacyProfilePhotoURL(photo.MediaID))
	}
	return out
}

// legacyProfileUpdateFromJSON maps the v1 edit-form payload to the whitelisted
// db.ProfileUpdate. Unknown keys are ignored like the old backend; malformed
// values are rejected instead of being written blindly.
func legacyProfileUpdateFromJSON(body map[string]any) (db.ProfileUpdate, error) {
	var upd db.ProfileUpdate

	if v, ok, err := legacyStringValue(body, legacyFieldNickname, "nickname"); err != nil {
		return upd, err
	} else if ok && *v != "" {
		upd.Nickname = v
	}
	if v, ok, err := legacyIntValue(body, legacyFieldHeight, "height（cm）", "height", "height_cm"); err != nil {
		return upd, err
	} else if ok {
		upd.HeightCM = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldEducation, "education"); err != nil {
		return upd, err
	} else if ok {
		upd.Education = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldCity, "city"); err != nil {
		return upd, err
	} else if ok {
		upd.City = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldChurch, "church"); err != nil {
		return upd, err
	} else if ok {
		upd.Church = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldChurchGroup, "church_group"); err != nil {
		return upd, err
	} else if ok {
		upd.ChurchGroup = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldChurchLocation, "church_location"); err != nil {
		return upd, err
	} else if ok {
		upd.ChurchLocation = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldIndustry, "industry"); err != nil {
		return upd, err
	} else if ok {
		upd.Industry = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldPosition, "position"); err != nil {
		return upd, err
	} else if ok {
		upd.Position = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldIncome, "income"); err != nil {
		return upd, err
	} else if ok {
		upd.Income = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldHouse, "house"); err != nil {
		return upd, err
	} else if ok {
		upd.House = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldDriving, "汽车", "driving"); err != nil {
		return upd, err
	} else if ok {
		upd.Driving = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldPersonality, "personality"); err != nil {
		return upd, err
	} else if ok {
		upd.Personality = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldSelfTraits, "self_traits"); err != nil {
		return upd, err
	} else if ok {
		upd.SelfTraits = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldSelfHobbies, "self_hobbies"); err != nil {
		return upd, err
	} else if ok {
		upd.SelfHobbies = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldSelfSports, "self_sports"); err != nil {
		return upd, err
	} else if ok {
		upd.SelfSports = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldMBTI, "mbti"); err != nil {
		return upd, err
	} else if ok {
		upd.MBTI = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldLiveWithParents, "live_with_parents"); err != nil {
		return upd, err
	} else if ok {
		upd.LiveWithParents = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldFamily, "family"); err != nil {
		return upd, err
	} else if ok {
		upd.Family = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldPartnerCriteria, "partner_criteria"); err != nil {
		return upd, err
	} else if ok {
		upd.PartnerCriteria = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldPartnerTraits, "partner_traits"); err != nil {
		return upd, err
	} else if ok {
		upd.PartnerTraits = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldPartnerHobbies, "partner_hobbies"); err != nil {
		return upd, err
	} else if ok {
		upd.PartnerHobbies = v
	}
	if v, ok, err := legacyStringListValue(body, legacyFieldPartnerSports, "partner_sports"); err != nil {
		return upd, err
	} else if ok {
		upd.PartnerSports = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldWechat, "wechat"); err != nil {
		return upd, err
	} else if ok {
		upd.Wechat = v
	}
	if v, ok, err := legacyStringValue(body, legacyFieldPhone, "phone"); err != nil {
		return upd, err
	} else if ok && *v != "" {
		upd.Phone = v
	}
	return upd, nil
}

// legacyIDCardValue extracts the temporary ID-card backfill field. Spaces are
// stripped because users often paste the number in groups.
func legacyIDCardValue(body map[string]any) (string, bool, error) {
	value, key, ok := legacyLookup(body, legacyFieldIDCard, "id_card")
	if !ok {
		return "", false, nil
	}
	switch v := value.(type) {
	case nil:
		return "", false, nil
	case string:
		normalized := strings.ToUpper(strings.Join(strings.Fields(v), ""))
		return normalized, true, nil
	default:
		return "", false, fmt.Errorf("「%s」格式不正确", key)
	}
}

func legacyLookup(body map[string]any, keys ...string) (any, string, bool) {
	for _, key := range keys {
		value, ok := body[key]
		if ok {
			return value, key, true
		}
	}
	return nil, "", false
}

func legacyStringValue(body map[string]any, keys ...string) (*string, bool, error) {
	value, key, ok := legacyLookup(body, keys...)
	if !ok {
		return nil, false, nil
	}
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case string:
		out := strings.TrimSpace(v)
		return &out, true, nil
	default:
		return nil, false, fmt.Errorf("「%s」格式不正确", key)
	}
}

func legacyIntValue(body map[string]any, keys ...string) (*int, bool, error) {
	value, key, ok := legacyLookup(body, keys...)
	if !ok {
		return nil, false, nil
	}
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case json.Number:
		return parseIntPointer(v.String(), key)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, false, nil
		}
		return parseIntPointer(trimmed, key)
	case float64:
		if math.Trunc(v) != v {
			return nil, false, fmt.Errorf("「%s」格式不正确", key)
		}
		n := int(v)
		return &n, true, nil
	default:
		return nil, false, fmt.Errorf("「%s」格式不正确", key)
	}
}

func parseIntPointer(raw, key string) (*int, bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil, false, fmt.Errorf("「%s」格式不正确", key)
	}
	return &n, true, nil
}

func legacyStringListValue(body map[string]any, keys ...string) (*[]string, bool, error) {
	value, key, ok := legacyLookup(body, keys...)
	if !ok {
		return nil, false, nil
	}
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, false, fmt.Errorf("「%s」选项格式不正确", key)
			}
			text = strings.TrimSpace(text)
			if text != "" {
				out = append(out, text)
			}
		}
		return &out, true, nil
	case []string:
		out := append([]string(nil), v...)
		return &out, true, nil
	default:
		return nil, false, fmt.Errorf("「%s」选项格式不正确", key)
	}
}

func legacyDecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return false
	}
	return true
}

func legacyClipText(raw string, max int) string {
	raw = strings.TrimSpace(raw)
	if utf8.RuneCountInString(raw) <= max {
		return raw
	}
	runes := []rune(raw)
	return string(runes[:max])
}

// legacyPointsError keeps the v1 `{"error": "..."}` envelope for business
// failures from the points package.
func legacyPointsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrPointsInsufficient), errors.Is(err, db.ErrPointsUnavailable),
		errors.Is(err, db.ErrPointsAlreadyRedeemed), errors.Is(err, db.ErrPointsDuplicate),
		errors.Is(err, db.ErrPointsInviteDue), errors.Is(err, db.ErrPointsInvalidKind),
		errors.Is(err, db.ErrPointsInvalidDelta), errors.Is(err, db.ErrPointsNotFound):
		message := db.PointsMessage(err)
		if message == "" {
			message = "无法完成操作，请稍后再试。"
		}
		legacyError(w, http.StatusBadRequest, message)
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "找不到资料")
	default:
		legacyError(w, http.StatusInternalServerError, "系统暂时无法处理，请稍后再试")
	}
}

func legacyPhotoResponse(photos []db.ProfilePhoto, hasFace bool) map[string]any {
	urls := legacyProfilePhotoURLs(photos)
	photo := ""
	if len(urls) > 0 {
		photo = urls[0]
	}
	return map[string]any{
		"ok":       true,
		"photo":    photo,
		"photos":   urls,
		"has_face": hasFace,
	}
}

func legacyFormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
