package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

func init() { registerModule(registerProfile) }

func registerProfile(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /profile/photo/{id}", a.handleProfilePhotoServe)

}

type profilePhotoJSON struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ThumbURL  string `json:"thumb_url"`
	SortOrder int    `json:"sort_order"`
	IsCover   bool   `json:"is_cover"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

func profilePhotoJSONFromDB(photo db.ProfilePhoto) profilePhotoJSON {
	url := "/profile/photo/" + photo.MediaID
	return profilePhotoJSON{
		ID:        photo.MediaID,
		URL:       url,
		ThumbURL:  url + "?size=thumb",
		SortOrder: photo.SortOrder,
		IsCover:   photo.IsCover,
		Width:     photo.Width,
		Height:    photo.Height,
	}
}

func profilePhotosJSON(photos []db.ProfilePhoto) []profilePhotoJSON {
	out := make([]profilePhotoJSON, 0, len(photos))
	for _, photo := range photos {
		out = append(out, profilePhotoJSONFromDB(photo))
	}
	return out
}

type profileJSON struct {
	ID              int64              `json:"id"`
	UserID          string             `json:"user_id"`
	Name            string             `json:"name"`
	Nickname        string             `json:"nickname"`
	Gender          string             `json:"gender"`
	Birthday        string             `json:"birthday"`
	BirthdayDisplay string             `json:"birthday_display"`
	HeightCM        *int               `json:"height_cm"`
	Education       string             `json:"education"`
	BaptismalName   string             `json:"baptismal_name"`
	Church          string             `json:"church"`
	ChurchLocation  string             `json:"church_location"`
	ChurchGroup     string             `json:"church_group"`
	NativePlace     string             `json:"native_place"`
	City            string             `json:"city"`
	Industry        string             `json:"industry"`
	Position        string             `json:"position"`
	Income          string             `json:"income"`
	House           string             `json:"house"`
	Driving         string             `json:"driving"`
	HouseNoteHave   string             `json:"house_note_have"`
	HouseNoteNone   string             `json:"house_note_none"`
	DrivingNoteHave string             `json:"driving_note_have"`
	DrivingNoteNone string             `json:"driving_note_none"`
	Marriage        string             `json:"marriage"`
	Family          string             `json:"family"`
	LiveWithParents string             `json:"live_with_parents"`
	Personality     string             `json:"personality"`
	SelfTraits      []string           `json:"self_traits"`
	SelfHobbies     []string           `json:"self_hobbies"`
	SelfSports      []string           `json:"self_sports"`
	MBTI            []string           `json:"mbti"`
	PartnerCriteria string             `json:"partner_criteria"`
	PartnerTraits   []string           `json:"partner_traits"`
	PartnerHobbies  []string           `json:"partner_hobbies"`
	PartnerSports   []string           `json:"partner_sports"`
	Wechat          string             `json:"wechat"`
	Phone           string             `json:"phone"`
	IDCardLast4     string             `json:"id_card_last4"`
	IDCardValid     bool               `json:"id_card_valid"`
	AccountKind     string             `json:"account_kind"`
	AccountStatus   string             `json:"account_status"`
	ContactConsent  bool               `json:"contact_consent"`
	ReferralSource  string             `json:"referral_source"`
	IsForChild      string             `json:"is_for_child"`
	WechatPayment   string             `json:"wechat_payment"`
	RegisteredAt    string             `json:"registered_at"`
	LastActiveAt    string             `json:"last_active_at"`
	CreatedAt       string             `json:"created_at"`
	UpdatedAt       string             `json:"updated_at"`
	Photos          []profilePhotoJSON `json:"photos"`
	CSRFToken       string             `json:"csrf_token,omitempty"`
}

func timeJSON(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func buildProfileJSON(p db.Profile) profileJSON {
	return profileJSON{
		ID:              p.ID,
		UserID:          profileUserID(p),
		Name:            p.Name,
		Nickname:        p.Nickname,
		Gender:          p.Gender,
		Birthday:        profileDateISO(p.Birthday),
		BirthdayDisplay: profileBirthdayShort(p.Birthday),
		HeightCM:        p.HeightCM,
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
		House:           p.House,
		Driving:         p.Driving,
		HouseNoteHave:   p.HouseNoteHave,
		HouseNoteNone:   p.HouseNoteNone,
		DrivingNoteHave: p.DrivingNoteHave,
		DrivingNoteNone: p.DrivingNoteNone,
		Marriage:        p.Marriage,
		Family:          p.Family,
		LiveWithParents: p.LiveWithParents,
		Personality:     p.Personality,
		SelfTraits:      p.SelfTraits,
		SelfHobbies:     p.SelfHobbies,
		SelfSports:      p.SelfSports,
		MBTI:            p.MBTI,
		PartnerCriteria: p.PartnerCriteria,
		PartnerTraits:   p.PartnerTraits,
		PartnerHobbies:  p.PartnerHobbies,
		PartnerSports:   p.PartnerSports,
		Wechat:          p.Wechat,
		Phone:           p.Phone,
		IDCardLast4:     p.IDCardLast4,
		IDCardValid:     p.IDCardValid,
		AccountKind:     p.AccountKind,
		AccountStatus:   p.AccountStatus,
		ContactConsent:  p.ContactConsent,
		ReferralSource:  p.ReferralSource,
		IsForChild:      p.IsForChild,
		WechatPayment:   p.WechatPayment,
		RegisteredAt:    timeJSON(profileRegisteredAt(p)),
		LastActiveAt:    timeJSON(p.LastActiveAt),
		CreatedAt:       p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       p.UpdatedAt.UTC().Format(time.RFC3339),
		Photos:          profilePhotosJSON(p.Photos),
	}
}

// publicProfileJSON deliberately has no phone, wechat, real name, ID card or
// consent fields. Mutual-match wechat disclosure belongs to the social module.
type publicProfileJSON struct {
	UserID          string             `json:"user_id"`
	Nickname        string             `json:"nickname"`
	Gender          string             `json:"gender"`
	Birthday        string             `json:"birthday"`
	HeightCM        *int               `json:"height_cm"`
	Education       string             `json:"education"`
	BaptismalName   string             `json:"baptismal_name"`
	City            string             `json:"city"`
	NativePlace     string             `json:"native_place"`
	Church          string             `json:"church"`
	ChurchGroup     string             `json:"church_group"`
	ChurchLocation  string             `json:"church_location"`
	Industry        string             `json:"industry"`
	Position        string             `json:"position"`
	Income          string             `json:"income"`
	House           string             `json:"house"`
	Driving         string             `json:"driving"`
	Personality     string             `json:"personality"`
	SelfTraits      []string           `json:"self_traits"`
	SelfHobbies     []string           `json:"self_hobbies"`
	SelfSports      []string           `json:"self_sports"`
	MBTI            []string           `json:"mbti"`
	LiveWithParents string             `json:"live_with_parents"`
	Family          string             `json:"family"`
	PartnerCriteria string             `json:"partner_criteria"`
	PartnerTraits   []string           `json:"partner_traits"`
	PartnerHobbies  []string           `json:"partner_hobbies"`
	PartnerSports   []string           `json:"partner_sports"`
	Photos          []profilePhotoJSON `json:"photos"`
	IsSelf          bool               `json:"is_self"`
}

func buildPublicProfileJSON(p db.Profile, isSelf bool) publicProfileJSON {
	return publicProfileJSON{
		UserID:          profileUserID(p),
		Nickname:        p.Nickname,
		Gender:          p.Gender,
		Birthday:        profileBirthdayShort(p.Birthday),
		HeightCM:        p.HeightCM,
		Education:       p.Education,
		BaptismalName:   p.BaptismalName,
		City:            p.City,
		NativePlace:     p.NativePlace,
		Church:          p.Church,
		ChurchGroup:     p.ChurchGroup,
		ChurchLocation:  p.ChurchLocation,
		Industry:        p.Industry,
		Position:        p.Position,
		Income:          p.Income,
		House:           profileSelectWithNote(p.House, p.HouseNoteHave, p.HouseNoteNone),
		Driving:         profileSelectWithNote(p.Driving, p.DrivingNoteHave, p.DrivingNoteNone),
		Personality:     p.Personality,
		SelfTraits:      p.SelfTraits,
		SelfHobbies:     p.SelfHobbies,
		SelfSports:      p.SelfSports,
		MBTI:            p.MBTI,
		LiveWithParents: p.LiveWithParents,
		Family:          p.Family,
		PartnerCriteria: p.PartnerCriteria,
		PartnerTraits:   p.PartnerTraits,
		PartnerHobbies:  p.PartnerHobbies,
		PartnerSports:   p.PartnerSports,
		Photos:          profilePhotosJSON(p.Photos),
		IsSelf:          isSelf,
	}
}

func profileIsObserver(p db.Profile) bool {
	return p.AccountKind == "observer" || p.AccountStatus == "observer"
}

func profileAccountCanBrowse(status string) bool {
	switch status {
	case "active", "matched", "observer":
		return true
	}
	return false
}

func profileCanBrowse(p db.Profile) bool {
	return profileAccountCanBrowse(p.AccountStatus)
}

func profilePubliclyVisible(p db.Profile) bool {
	return profileAccountCanBrowse(p.AccountStatus)
}

func profileCanEdit(p db.Profile) bool {
	if profileIsObserver(p) {
		return false
	}
	return p.AccountStatus == "active" || p.AccountStatus == "matched"
}

func profileGateMessage(p db.Profile) string {
	if profileIsObserver(p) {
		return "观察员账号仅可浏览，无法修改资料。"
	}
	switch p.AccountStatus {
	case "pending":
		return "资料审核中，通过后即可修改。"
	case "rejected":
		return "很抱歉，你的资料未通过审核，如有疑问请联系管理员。"
	case "left":
		return "你已暂时退出相亲市场，如需恢复请联系管理员。"
	case "disabled":
		return "账号已停用，请联系管理员。"
	}
	return "目前账号状态无法使用此功能。"
}

func (a *App) loadProfileHTML(w http.ResponseWriter, r *http.Request, userID int64) (db.Profile, bool) {
	p, err := a.db.GetProfile(r.Context(), userID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		a.renderError(w, r, http.StatusNotFound, "找不到资料", "你的个人资料不存在，请联系管理员。")
		return db.Profile{}, false
	case err != nil:
		a.logger(r).Error("load profile failed", "error", err, "user_id", userID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return db.Profile{}, false
	}
	return p, true
}

func (a *App) loadProfileAPI(w http.ResponseWriter, r *http.Request, userID int64) (db.Profile, bool) {
	p, err := a.db.GetProfile(r.Context(), userID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		return db.Profile{}, false
	case err != nil:
		a.logger(r).Error("load profile failed", "error", err, "user_id", userID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return db.Profile{}, false
	}
	return p, true
}

func (a *App) requireProfileEditor(w http.ResponseWriter, r *http.Request, api bool) (db.User, db.Profile, bool) {
	var (
		user db.User
		ok   bool
	)
	if api {
		user, ok = a.requireUserAPI(w, r)
	} else {
		user, ok = a.requireUserHTML(w, r)
	}
	if !ok {
		return db.User{}, db.Profile{}, false
	}
	var (
		p         db.Profile
		profileOK bool
	)
	if api {
		p, profileOK = a.loadProfileAPI(w, r, user.ID)
	} else {
		p, profileOK = a.loadProfileHTML(w, r, user.ID)
	}
	if !profileOK {
		return db.User{}, db.Profile{}, false
	}
	if !profileCanEdit(p) {
		message := profileGateMessage(p)
		if api {
			code := "account_gated"
			if profileIsObserver(p) {
				code = "observer_readonly"
			}
			writeAPIError(w, http.StatusForbidden, code, message)
		} else {
			a.renderError(w, r, http.StatusForbidden, "无法修改资料", message)
		}
		return db.User{}, db.Profile{}, false
	}
	return user, p, true
}

// fillProfileDashboard adds the legacy "我的" numbers. A missing subsystem
// renders zero instead of hiding the whole section, so the old layout stays.
func (a *App) fillProfileDashboard(r *http.Request, user db.User, view *profileView) {
	ctx := r.Context()
	now := a.clock.Now()
	if quota, err := a.db.LikeQuotaFor(ctx, user.ID, now); err != nil {
		a.logger(r).Warn("profile like quota failed", "error", err, "user_id", user.ID)
	} else {
		view.AnonTotal = quota.AnonTotal
		view.AnonLeft = quota.AnonLeft
		view.RealTotal = quota.RealTotal
		view.RealLeft = quota.RealLeft
	}
	if praise, err := a.db.PraiseStateFor(ctx, user.ID, now); err != nil {
		a.logger(r).Warn("profile praise state failed", "error", err, "user_id", user.ID)
	} else {
		view.PraiseReceivedWeek = praise.ReceivedWeek
	}
	if summary, err := a.db.PointsSummary(ctx, user.ID); err != nil {
		a.logger(r).Warn("profile points summary failed", "error", err, "user_id", user.ID)
	} else {
		view.PointsBalance = summary.Balance
	}
	if access, err := a.loadAdminAccess(ctx, user); err == nil && len(access.Roles) > 0 {
		view.IsAdmin = true
	}
}

func (a *App) handleProfileAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	p, ok := a.loadProfileAPI(w, r, user.ID)
	if !ok {
		return
	}
	if p.AccountStatus == "disabled" {
		writeAPIError(w, http.StatusForbidden, "account_disabled", profileGateMessage(p))
		return
	}
	csrf := a.ensureCSRF(w, r)
	payload := buildProfileJSON(p)
	payload.CSRFToken = csrf
	writeJSON(w, http.StatusOK, payload)
}

type profileUpdateRequest struct {
	Nickname        *string   `json:"nickname"`
	HeightCM        *int      `json:"height_cm"`
	Education       *string   `json:"education"`
	City            *string   `json:"city"`
	Church          *string   `json:"church"`
	ChurchGroup     *string   `json:"church_group"`
	ChurchLocation  *string   `json:"church_location"`
	NativePlace     *string   `json:"native_place"`
	Industry        *string   `json:"industry"`
	Position        *string   `json:"position"`
	Income          *string   `json:"income"`
	House           *string   `json:"house"`
	Driving         *string   `json:"driving"`
	Personality     *string   `json:"personality"`
	Family          *string   `json:"family"`
	LiveWithParents *string   `json:"live_with_parents"`
	PartnerCriteria *string   `json:"partner_criteria"`
	SelfTraits      *[]string `json:"self_traits"`
	SelfHobbies     *[]string `json:"self_hobbies"`
	SelfSports      *[]string `json:"self_sports"`
	MBTI            *[]string `json:"mbti"`
	PartnerTraits   *[]string `json:"partner_traits"`
	PartnerHobbies  *[]string `json:"partner_hobbies"`
	PartnerSports   *[]string `json:"partner_sports"`
	Wechat          *string   `json:"wechat"`
	Phone           *string   `json:"phone"`
}

func (u profileUpdateRequest) profileUpdate() db.ProfileUpdate {
	return db.ProfileUpdate{
		Nickname:        u.Nickname,
		HeightCM:        u.HeightCM,
		Education:       u.Education,
		City:            u.City,
		Church:          u.Church,
		ChurchGroup:     u.ChurchGroup,
		ChurchLocation:  u.ChurchLocation,
		NativePlace:     u.NativePlace,
		Industry:        u.Industry,
		Position:        u.Position,
		Income:          u.Income,
		House:           u.House,
		Driving:         u.Driving,
		Personality:     u.Personality,
		Family:          u.Family,
		LiveWithParents: u.LiveWithParents,
		PartnerCriteria: u.PartnerCriteria,
		SelfTraits:      u.SelfTraits,
		SelfHobbies:     u.SelfHobbies,
		SelfSports:      u.SelfSports,
		MBTI:            u.MBTI,
		PartnerTraits:   u.PartnerTraits,
		PartnerHobbies:  u.PartnerHobbies,
		PartnerSports:   u.PartnerSports,
		Wechat:          u.Wechat,
		Phone:           u.Phone,
	}
}

func (a *App) handleProfileUpdateAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	p, ok := a.loadProfileAPI(w, r, user.ID)
	if !ok {
		return
	}
	if !profileCanEdit(p) {
		code := "account_gated"
		if profileIsObserver(p) {
			code = "observer_readonly"
		}
		writeAPIError(w, http.StatusForbidden, code, profileGateMessage(p))
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeAPIError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请以 application/json 送出。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	var req profileUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return
	}
	upd := req.profileUpdate()
	if upd.Empty() {
		writeAPIError(w, http.StatusBadRequest, "no_fields", "没有可更新的字段。")
		return
	}

	_, err := a.db.UpdateProfile(r.Context(), user.ID, upd)
	var invalid *db.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": apiError{Code: "invalid_profile", Message: invalid.Message},
			"field": invalid.Field,
		})
		return
	case errors.Is(err, db.ErrPhoneConflict):
		writeAPIError(w, http.StatusConflict, "phone_conflict", "手机号码已被其他账号使用。")
		return
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		return
	case err != nil:
		a.logger(r).Error("update profile failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "资料更新失败，请稍后再试。")
		return
	}
	a.audit(r, &user.ID, "profile.updated", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"fields": profileUpdateFieldNames(upd)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "资料已更新。"})
}

func (a *App) handlePublicProfileAPI(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	viewerProfile, ok := a.loadProfileAPI(w, r, viewer.ID)
	if !ok {
		return
	}
	if !profileCanBrowse(viewerProfile) {
		writeAPIError(w, http.StatusForbidden, "account_gated", profileGateMessage(viewerProfile))
		return
	}
	target, err := a.resolveProfile(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, errInvalidProfileRef):
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		return
	case err != nil:
		a.logger(r).Error("load public profile failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	if target.ID != viewer.ID && !profilePubliclyVisible(target) {
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		return
	}
	writeJSON(w, http.StatusOK, buildPublicProfileJSON(target, target.ID == viewer.ID))
}

var errInvalidProfileRef = errors.New("httpserver: invalid profile reference")

func (a *App) resolveProfile(ctx context.Context, raw string) (db.Profile, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" || len(ref) > 64 {
		return db.Profile{}, errInvalidProfileRef
	}
	for _, c := range ref {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return db.Profile{}, errInvalidProfileRef
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		p, err := a.db.GetProfile(ctx, id)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return db.Profile{}, err
		}
	}
	return a.db.GetProfileByLegacyID(ctx, ref)
}

func profilePhotoUploadRedirectCode(err error) string {
	switch {
	case errors.Is(err, db.ErrPhotoLimit):
		return "limit"
	case errors.Is(err, media.ErrHEIC):
		return "heic"
	case errors.Is(err, media.ErrFileTooLarge):
		return "too_large"
	case errors.Is(err, media.ErrUnsupportedImage):
		return "unsupported"
	case errors.Is(err, media.ErrImageDimensions):
		return "dimensions"
	case errors.Is(err, errNoFile):
		return "no_file"
	default:
		return "failed"
	}
}

func (a *App) respondProfilePhotoError(w http.ResponseWriter, r *http.Request, api bool, err error) {
	if api {
		switch {
		case errors.Is(err, db.ErrPhotoLimit):
			writeAPIError(w, http.StatusBadRequest, "photo_limit",
				fmt.Sprintf("最多只能上传 %d 张照片。", db.MaxProfilePhotos))
			return
		}
		status, code, message := uploadErrorDetails(err)
		if status >= 500 {
			a.logger(r).Error("profile photo upload failed", "error", err)
		}
		writeAPIError(w, status, code, message)
		return
	}
	if !errors.Is(err, db.ErrPhotoLimit) {
		status, _, _ := uploadErrorDetails(err)
		if status >= 500 {
			a.logger(r).Error("profile photo upload failed", "error", err)
		}
	}
	http.Redirect(w, r, "/profile?photo_error="+profilePhotoUploadRedirectCode(err), http.StatusSeeOther)
}

// handleProfilePhotoUpload accepts multipart/form-data. The media row is
// created by receiveUpload; it is attached to the profile or rolled back.
func (a *App) handleProfilePhotoUpload(w http.ResponseWriter, r *http.Request, api bool) {
	user, profile, ok := a.requireProfileEditor(w, r, api)
	if !ok {
		return
	}
	if api && !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if len(profile.Photos) >= db.MaxProfilePhotos {
		a.respondProfilePhotoError(w, r, api, db.ErrPhotoLimit)
		return
	}
	if !a.allowUpload(w, r, user.ID, api) {
		return
	}
	if !a.acquireMedia(w, r, api) {
		return
	}
	mediaReleased := false
	releaseMedia := func() {
		if !mediaReleased {
			a.releaseMedia()
			mediaReleased = true
		}
	}
	defer releaseMedia()
	if err := a.parseMultipartRequest(w, r); err != nil {
		if api {
			status, code, message := uploadParseErrorDetails(err)
			writeAPIError(w, status, code, message)
		} else {
			a.renderUploadParseError(w, r, err)
		}
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !api && !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}

	saved, err := a.receiveUpload(r, user)
	if err != nil {
		a.respondProfilePhotoError(w, r, api, err)
		return
	}
	photo, err := a.db.AddProfilePhoto(r.Context(), user.ID, saved.ID)
	if err != nil {
		if m, removed, derr := a.db.RemoveMediaOwned(r.Context(), user.ID, saved.ID); derr == nil && removed {
			a.removeObject(r, m.ID, m.ObjectKey)
			a.removeObject(r, m.ID, m.ThumbKey)
		}
		if !errors.Is(err, db.ErrPhotoLimit) {
			a.logger(r).Error("attach profile photo failed", "error", err, "user_id", user.ID)
		}
		a.respondProfilePhotoError(w, r, api, err)
		return
	}
	// probe 使用独立的 faceSem，必须先把 mediaSem 释放，避免慢 probe
	// 让其他上传排队超时（429）。
	releaseMedia()
	a.probeProfilePhotoFace(r.Context(), user.ID, photo.MediaID)
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("reload profile photos failed", "error", err, "user_id", user.ID)
	}
	a.audit(r, &user.ID, "profile.photo_added", "media", photo.MediaID,
		map[string]any{"media_id": photo.MediaID})
	if api {
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":     true,
			"photo":  profilePhotoJSONFromDB(photo),
			"photos": profilePhotosJSON(photos),
		})
		return
	}
	http.Redirect(w, r, "/profile?photo_added=1", http.StatusSeeOther)
}

func (a *App) handleProfilePhotoUploadAPI(w http.ResponseWriter, r *http.Request) {
	a.handleProfilePhotoUpload(w, r, true)
}

func (a *App) handleProfilePhotoDelete(w http.ResponseWriter, r *http.Request, api bool) {
	user, _, ok := a.requireProfileEditor(w, r, api)
	if !ok {
		return
	}
	if api && !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if !api {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil || !a.formCSRFValid(r) {
			a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
			return
		}
	}
	mediaID := r.PathValue("id")
	if !validMediaID(mediaID) {
		if api {
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到照片。")
		} else {
			a.renderError(w, r, http.StatusNotFound, "找不到照片", "照片不存在或已删除。")
		}
		return
	}
	media, objectsRemoved, err := a.db.DeleteProfilePhoto(r.Context(), user.ID, mediaID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		if api {
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到照片。")
		} else {
			a.renderError(w, r, http.StatusNotFound, "找不到照片", "照片不存在或已删除。")
		}
		return
	case err != nil:
		a.logger(r).Error("delete profile photo failed", "error", err, "user_id", user.ID, "media_id", mediaID)
		if api {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "删除失败，请稍后再试。")
		} else {
			http.Redirect(w, r, "/profile?photo_error=failed", http.StatusSeeOther)
		}
		return
	}
	if objectsRemoved {
		a.removeObject(r, media.ID, media.ObjectKey)
		a.removeObject(r, media.ID, media.ThumbKey)
	}
	a.audit(r, &user.ID, "profile.photo_deleted", "media", mediaID, map[string]any{"media_id": mediaID})
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("reload profile photos failed", "error", err, "user_id", user.ID)
	}
	if api {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "photos": profilePhotosJSON(photos)})
		return
	}
	http.Redirect(w, r, "/profile?photo_deleted=1", http.StatusSeeOther)
}

func (a *App) handleProfilePhotoDeleteAPI(w http.ResponseWriter, r *http.Request) {
	a.handleProfilePhotoDelete(w, r, true)
}

func (a *App) handleProfilePhotoCover(w http.ResponseWriter, r *http.Request, api bool) {
	user, _, ok := a.requireProfileEditor(w, r, api)
	if !ok {
		return
	}
	if api && !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if !api {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil || !a.formCSRFValid(r) {
			a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
			return
		}
	}
	mediaID := r.PathValue("id")
	if !validMediaID(mediaID) {
		if api {
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到照片。")
		} else {
			a.renderError(w, r, http.StatusNotFound, "找不到照片", "照片不存在或已删除。")
		}
		return
	}
	photos, err := a.db.SetProfilePhotoCover(r.Context(), user.ID, mediaID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		if api {
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到照片。")
		} else {
			a.renderError(w, r, http.StatusNotFound, "找不到照片", "照片不存在或已删除。")
		}
		return
	case err != nil:
		a.logger(r).Error("set profile cover failed", "error", err, "user_id", user.ID, "media_id", mediaID)
		if api {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "设定封面失败，请稍后再试。")
		} else {
			http.Redirect(w, r, "/profile?photo_error=failed", http.StatusSeeOther)
		}
		return
	}
	a.audit(r, &user.ID, "profile.photo_cover", "media", mediaID, map[string]any{"media_id": mediaID})
	if api {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "photos": profilePhotosJSON(photos)})
		return
	}
	http.Redirect(w, r, "/profile?photo_cover=1", http.StatusSeeOther)
}

func (a *App) handleProfilePhotoCoverAPI(w http.ResponseWriter, r *http.Request) {
	a.handleProfilePhotoCover(w, r, true)
}

func (a *App) handleProfilePhotoServe(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "需要登录", http.StatusUnauthorized)
		return
	}
	mediaID := r.PathValue("id")
	if !validMediaID(mediaID) {
		http.NotFound(w, r)
		return
	}
	photo, err := a.db.GetProfilePhotoFile(r.Context(), mediaID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load profile photo failed", "error", err, "media_id", mediaID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if photo.UserID != user.ID {
		_, status, err := a.db.GetAccountState(r.Context(), user.ID)
		if err != nil {
			a.logger(r).Error("load viewer account state failed", "error", err, "user_id", user.ID)
			http.NotFound(w, r)
			return
		}
		if !profileAccountCanBrowse(status) || !profileAccountCanBrowse(photo.AccountStatus) {
			http.NotFound(w, r)
			return
		}
	}

	a.serveMediaObject(w, r, mediaObject{
		ID:         mediaID,
		ObjectKey:  photo.ObjectKey,
		ObjectSize: photo.ByteSize,
		ObjectType: photo.ContentType,
		ThumbKey:   photo.ThumbKey,
		ThumbSize:  photo.ThumbSize,
	}, r.URL.Query().Get("size") == "thumb")
}

type accountStatusRequest struct {
	AccountStatus string `json:"account_status"`
}

func (a *App) handleAccountStatusAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	p, ok := a.loadProfileAPI(w, r, user.ID)
	if !ok {
		return
	}
	if profileIsObserver(p) {
		writeAPIError(w, http.StatusForbidden, "observer_readonly", "观察员账号不可切换账号状态。")
		return
	}
	if p.AccountStatus != "active" && p.AccountStatus != "matched" {
		writeAPIError(w, http.StatusForbidden, "account_gated", profileGateMessage(p))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	var req accountStatusRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
			return
		}
	}
	target := strings.TrimSpace(req.AccountStatus)
	if target == "" {
		if p.AccountStatus == "active" {
			target = "matched"
		} else {
			target = "active"
		}
	}
	previous := p.AccountStatus
	status, err := a.db.SetAccountStatus(r.Context(), user.ID, target)
	var invalid *db.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeAPIError(w, http.StatusBadRequest, "invalid_status", invalid.Message)
		return
	case errors.Is(err, db.ErrActiveSignupBlocked):
		writeAPIError(w, http.StatusBadRequest, "active_signup", "你已报名活动，请先取消报名再脱单。")
		return
	case errors.Is(err, db.ErrObserverNoSwitch):
		writeAPIError(w, http.StatusForbidden, "observer_readonly", "观察员账号不可切换账号状态。")
		return
	case errors.Is(err, db.ErrStatusNotSwitchable):
		writeAPIError(w, http.StatusForbidden, "account_gated", "目前账号状态不支持切换。")
		return
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		return
	case err != nil:
		a.logger(r).Error("set account status failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "账号状态更新失败，请稍后再试。")
		return
	}
	a.audit(r, &user.ID, "account.status_changed", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"from": previous, "to": status})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account_status": status})
}

func (a *App) handleAccountSwitchAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireUserAPI(w, r); !ok {
		return
	}
	writeAPIError(w, http.StatusNotImplemented, "not_implemented",
		"观察员身份切换尚未开放，请联系管理员。")
}
