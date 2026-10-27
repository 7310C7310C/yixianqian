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

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

func init() { registerModule(registerLegacyProfileAPI) }

func registerLegacyProfileAPI(a *App, mux *http.ServeMux) {
	mux.HandleFunc("PUT /legacy/api/profile", a.handleLegacyProfileUpdate)
	mux.HandleFunc("POST /legacy/api/profile/photo", a.handleLegacyProfilePhotoUpload)
	// The bundled v1 SPA posts the upload to an absolute /api/profile/photo
	// (uploadWithProgress does not apply BASE_PATH), so keep that alias.
	mux.HandleFunc("POST /api/profile/photo", a.handleLegacyProfilePhotoUpload)
	mux.HandleFunc("DELETE /legacy/api/profile/photo", a.handleLegacyProfilePhotoDelete)
	mux.HandleFunc("POST /legacy/api/profile/photo/cover", a.handleLegacyProfilePhotoCover)
	mux.HandleFunc("GET /legacy/api/users/{id}/public", a.handleLegacyPublicProfile)
	mux.HandleFunc("POST /legacy/api/account/status", a.handleLegacyAccountStatus)
	mux.HandleFunc("POST /legacy/api/account/switch", a.handleLegacyAccountSwitch)
	mux.HandleFunc("POST /legacy/api/suggestions", a.handleLegacySuggestion)
	mux.HandleFunc("POST /legacy/api/feedback", a.handleLegacyFeedback)
	mux.HandleFunc("POST /legacy/api/auth/claim", a.handleLegacyClaimDisabled)
}

// legacyEditableProfile is the read-write path for full profile edits (e.g.
// photo upload): observers stay read-only here. The PUT profile handler below
// uses legacyProfileActor instead so it can allow the ID-card-only backfill.
func (a *App) legacyEditableProfile(w http.ResponseWriter, r *http.Request) (db.User, db.Profile, bool) {
	user, profile, ok := a.legacyProfileActor(w, r)
	if !ok {
		return db.User{}, db.Profile{}, false
	}
	if !profileCanEdit(profile) {
		message := profileGateMessage(profile)
		if profileIsObserver(profile) {
			message = "你是村情六处账号，无此操作权限"
		}
		legacyError(w, http.StatusForbidden, message)
		return db.User{}, db.Profile{}, false
	}
	return user, profile, true
}

// legacyProfileActor authenticates the caller and loads their profile without
// deciding whether the profile is editable: observers may still PUT the single
// ID-card backfill field, which is checked against the request body below.
func (a *App) legacyProfileActor(w http.ResponseWriter, r *http.Request) (db.User, db.Profile, bool) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return db.User{}, db.Profile{}, false
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return db.User{}, db.Profile{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy profile load failed")
		return db.User{}, db.Profile{}, false
	}
	return user, profile, true
}

func (a *App) legacyPhotoActor(w http.ResponseWriter, r *http.Request) (db.User, db.Profile, bool) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return db.User{}, db.Profile{}, false
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return db.User{}, db.Profile{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy profile photo load failed")
		return db.User{}, db.Profile{}, false
	}
	if profileIsObserver(profile) {
		legacyError(w, http.StatusForbidden, "你是村情六处账号，无此操作权限")
		return db.User{}, db.Profile{}, false
	}
	if profile.AccountStatus == "disabled" {
		legacyError(w, http.StatusForbidden, profileGateMessage(profile))
		return db.User{}, db.Profile{}, false
	}
	return user, profile, true
}

func (a *App) handleLegacyProfileUpdate(w http.ResponseWriter, r *http.Request) {
	user, profile, ok := a.legacyProfileActor(w, r)
	if !ok {
		return
	}
	observer := profileIsObserver(profile)
	if !observer && !profileCanEdit(profile) {
		legacyError(w, http.StatusForbidden, profileGateMessage(profile))
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var body map[string]any
	if !legacyDecodeJSONBody(w, r, &body) {
		return
	}
	if observer && !legacyIDCardOnlyBody(body) {
		legacyError(w, http.StatusForbidden, "你是村情六处账号，无此操作权限")
		return
	}
	upd, err := legacyProfileUpdateFromJSON(body)
	if err != nil {
		legacyError(w, http.StatusBadRequest, err.Error())
		return
	}
	// v1 allowed the ID card to be filled only while the record was invalid.
	// The plaintext is validated here, encrypted immediately and never logged
	// or returned; only the last four digits stay queryable.
	idRaw, hasID, idErr := legacyIDCardValue(body)
	if idErr != nil {
		legacyError(w, http.StatusBadRequest, idErr.Error())
		return
	}
	backfilled := false
	birthdayFromID := false
	if hasID {
		if profile.IDCardValid {
			legacyErrorCode(w, http.StatusBadRequest, "id_card_locked", "身份证号已核验，不可修改")
			return
		}
		normalized, birth, verr := validateBackfillIDCard(idRaw)
		if verr != nil {
			legacyErrorCode(w, http.StatusBadRequest, "invalid_id_card", "身份证号码格式不正确，请填写18位有效身份证号")
			return
		}
		// Registration always asked for a birthday, but migrated v1 accounts
		// and test/observer accounts can have none, and there is no birthday
		// editor in the profile form. The ID card is authoritative, so fill a
		// missing birthday in the same update instead of dead-ending the gate.
		if profile.Birthday == nil {
			upd.Birthday = &birth
			birthdayFromID = true
		} else if !sameCalendarDay(*profile.Birthday, birth) {
			legacyErrorCode(w, http.StatusBadRequest, "id_card_birthday_mismatch", "身份证上的出生日期与资料生日不一致，请核对后重试")
			return
		}
		hash := idCardHash(a.cfg.SessionSecret, normalized)
		inUse, uerr := a.db.IDCardHashInUse(r.Context(), hash, user.ID)
		if uerr != nil {
			legacyServerError(a, w, r, uerr, "id card uniqueness check failed")
			return
		}
		if inUse {
			legacyErrorCode(w, http.StatusConflict, "id_card_taken", "此身份证号已被其他账号使用")
			return
		}
		sealed, serr := sealIDCard(a.cfg.SessionSecret, normalized)
		if serr != nil {
			legacyServerError(a, w, r, serr, "id card encrypt failed")
			return
		}
		upd.IDCardCipher = sealed
		upd.IDCardLast4 = normalized[len(normalized)-4:]
		upd.IDCardHash = hash
		backfilled = true
	}
	if upd.Empty() {
		legacyError(w, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	// 手机号同时是登录账号：变更前必须验证当前密码，否则一次误操作或会话
	// 盗用就能把登录标识改到他人号码上（审计 UD-05）。
	if upd.Phone != nil && !samePhone(profile.Phone, *upd.Phone) {
		if !a.verifyProfilePhoneChange(w, r, user.ID, body) {
			return
		}
	}
	oldPhone := profile.Phone
	_, err = a.db.UpdateProfile(r.Context(), user.ID, upd)
	var invalid *db.ValidationError
	switch {
	case errors.As(err, &invalid):
		legacyError(w, http.StatusBadRequest, invalid.Message)
		return
	case errors.Is(err, db.ErrPhoneConflict):
		legacyErrorCode(w, http.StatusConflict, "phone_taken", "手机号码已被其他账号使用")
		return
	case errors.Is(err, db.ErrIDCardConflict):
		legacyErrorCode(w, http.StatusConflict, "id_card_taken", "此身份证号已被其他账号使用")
		return
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy profile update failed")
		return
	}
	auditFields := profileUpdateFieldNames(upd)
	if backfilled {
		auditFields = append(auditFields, "id_card")
	}
	a.audit(r, &user.ID, "profile.updated", "user", strconv.FormatInt(profile.ID, 10),
		map[string]any{"fields": auditFields, "source": "legacy"})
	if backfilled {
		a.audit(r, &user.ID, "profile.id_card.backfilled", "user", strconv.FormatInt(profile.ID, 10),
			map[string]any{"last4": upd.IDCardLast4, "source": "legacy"})
	}
	if birthdayFromID {
		a.audit(r, &user.ID, "profile.birthday.from_id", "user", strconv.FormatInt(profile.ID, 10),
			map[string]any{"source": "legacy"})
	}
	if upd.Wechat != nil {
		a.ensureMutualWechatForUser(r.Context(), user.ID)
	}
	if upd.Phone != nil && !samePhone(oldPhone, *upd.Phone) {
		a.audit(r, &user.ID, "profile.phone.changed", "user", strconv.FormatInt(profile.ID, 10),
			map[string]any{
				"old_phone": maskAuditPhone(oldPhone),
				"new_phone": maskAuditPhone(*upd.Phone),
				"source":    "legacy",
			})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "资料已更新"})
}

// samePhone compares phones the same way login/registration do, so a value
// carrying "+86" or separators still counts as the same account. An unparsable
// value only matches its identical raw form, keeping a malformed change on the
// password-verification path.
func samePhone(a, b string) bool {
	na, nb := auth.NormalizePhone(a), auth.NormalizePhone(b)
	switch {
	case na != "" && nb != "":
		return na == nb
	case na != "" || nb != "":
		return false
	default:
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
}

// maskAuditPhone keeps the first three and last four digits for audit trails.
func maskAuditPhone(phone string) string {
	runes := []rune(strings.TrimSpace(phone))
	switch {
	case len(runes) >= 8:
		return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
	case len(runes) >= 4:
		return string(runes[:1]) + "***" + string(runes[len(runes)-1:])
	case len(runes) == 0:
		return ""
	default:
		return "***"
	}
}

// verifyProfilePhoneChange requires and verifies current_password for phone
// changes. It writes the JSON error response itself and returns false when the
// change must not proceed.
func (a *App) verifyProfilePhoneChange(w http.ResponseWriter, r *http.Request, userID int64, body map[string]any) bool {
	current := ""
	if raw, ok := body["current_password"]; ok {
		value, isString := raw.(string)
		if !isString {
			legacyErrorCode(w, http.StatusBadRequest, "password_required", "修改手机号需要输入当前密码。")
			return false
		}
		current = strings.TrimSpace(value)
	}
	if current == "" {
		legacyErrorCode(w, http.StatusBadRequest, "password_required", "修改手机号需要输入当前密码。")
		return false
	}
	if a.profilePasswordLimiter != nil {
		if ok, retry := a.profilePasswordLimiter.Allow("profile_phone:" + strconv.FormatInt(userID, 10)); !ok {
			w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
			legacyErrorCode(w, http.StatusTooManyRequests, "rate_limited", "密码尝试过于频繁，请稍后再试。")
			return false
		}
	}
	hash, err := a.db.GetPasswordHash(r.Context(), userID)
	if errors.Is(err, db.ErrNotFound) {
		legacyErrorCode(w, http.StatusConflict, "password_not_set", "该账号尚未设置密码，请联系管理员修改手机号。")
		return false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "load password hash for phone change failed")
		return false
	}
	acquired, busy := a.acquireHashQuiet(r)
	if !acquired {
		if busy {
			legacyErrorCode(w, http.StatusTooManyRequests, "busy", "目前请求较多，请稍后再试。")
		}
		return false
	}
	valid, verifyErr := auth.VerifyPassword(current, hash)
	a.releaseHash()
	if verifyErr != nil {
		a.logger(r).Error("verify current password for phone change failed", "error", verifyErr)
		legacyErrorCode(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理，请稍后再试。")
		return false
	}
	if !valid {
		a.audit(r, &userID, "profile.phone.change_denied", "user", strconv.FormatInt(userID, 10), nil)
		legacyErrorCode(w, http.StatusBadRequest, "password_invalid", "当前密码不正确。")
		return false
	}
	return true
}

func (a *App) handleLegacyProfilePhotoUpload(w http.ResponseWriter, r *http.Request) {
	user, profile, ok := a.legacyEditableProfile(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if len(profile.Photos) >= db.MaxProfilePhotos {
		legacyError(w, http.StatusBadRequest,
			fmt.Sprintf("最多只能上传 %d 张照片", db.MaxProfilePhotos))
		return
	}
	if ok, retry := a.uploadLimiter.Allow(fmt.Sprintf("upload:user:%d", user.ID)); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		legacyError(w, http.StatusTooManyRequests, "上传过于频繁，请稍后再试")
		return
	}
	mediaReleased := false
	releaseMedia := func() {
		if !mediaReleased {
			a.releaseMedia()
			mediaReleased = true
		}
	}
	select {
	case a.mediaSem <- struct{}{}:
		defer releaseMedia()
	case <-time.After(2 * time.Second):
		legacyError(w, http.StatusTooManyRequests, "目前上传较多，请稍后再试")
		return
	case <-r.Context().Done():
		return
	}
	if err := a.parseMultipartRequest(w, r); err != nil {
		status, _, message := uploadParseErrorDetails(err)
		legacyError(w, status, message)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	saved, err := a.receiveUpload(r, user)
	if err != nil {
		a.legacyPhotoUploadError(w, r, err)
		return
	}
	photo, err := a.db.AddProfilePhoto(r.Context(), user.ID, saved.ID)
	if err != nil {
		if m, removed, derr := a.db.RemoveMediaOwned(r.Context(), user.ID, saved.ID); derr == nil && removed {
			a.removeObject(r, m.ID, m.ObjectKey)
			a.removeObject(r, m.ID, m.ThumbKey)
		}
		if errors.Is(err, db.ErrPhotoLimit) {
			legacyError(w, http.StatusBadRequest,
				fmt.Sprintf("最多只能上传 %d 张照片", db.MaxProfilePhotos))
			return
		}
		legacyServerError(a, w, r, err, "legacy profile photo attach failed")
		return
	}
	if legacyPhotoWantsCover(r) {
		if _, err := a.db.SetProfilePhotoCover(r.Context(), user.ID, photo.MediaID); err != nil {
			a.logger(r).Warn("legacy profile photo cover failed", "error", err, "user_id", user.ID)
		}
	}
	// probe 使用独立的 faceSem，必须先把 mediaSem 释放，避免慢 probe
	// 让其他上传排队超时（429）。
	releaseMedia()
	a.probeProfilePhotoFace(r.Context(), user.ID, photo.MediaID)
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("legacy profile photo reload failed", "error", err, "user_id", user.ID)
		photos = append(profile.Photos, photo)
	}
	a.audit(r, &user.ID, "profile.photo_added", "media", photo.MediaID,
		map[string]any{"media_id": photo.MediaID, "source": "legacy"})
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos, a.legacyProfileHasFace(r.Context(), user.ID)))
}

func legacyPhotoWantsCover(r *http.Request) bool {
	if raw := strings.TrimSpace(r.FormValue("cover")); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	}
	if raw := strings.TrimSpace(r.FormValue("index")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n == 0
		}
	}
	return false
}

func (a *App) legacyPhotoUploadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNoFile):
		legacyError(w, http.StatusBadRequest, "未收到图片")
	case errors.Is(err, media.ErrHEIC):
		legacyError(w, http.StatusBadRequest, "iPhone HEIC 照片请先在相册转成 JPEG，或关闭「高效」格式后再上传。")
	case errors.Is(err, media.ErrFileTooLarge):
		legacyError(w, http.StatusBadRequest, "图片不能超过10MB")
	case errors.Is(err, media.ErrUnsupportedImage):
		legacyError(w, http.StatusBadRequest, "请上传 JPG/PNG 图片，暂不支持该格式")
	case errors.Is(err, media.ErrImageDimensions):
		legacyError(w, http.StatusBadRequest, "图片尺寸过大")
	default:
		legacyServerError(a, w, r, err, "legacy profile photo upload failed")
	}
}

type legacyPhotoIndexRequest struct {
	Index int `json:"index"`
}

func (a *App) handleLegacyProfilePhotoDelete(w http.ResponseWriter, r *http.Request) {
	user, profile, ok := a.legacyPhotoActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacyPhotoIndexRequest
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	if req.Index < 0 || req.Index >= len(profile.Photos) {
		legacyError(w, http.StatusBadRequest, "照片不存在")
		return
	}
	mediaID := profile.Photos[req.Index].MediaID
	deleted, objectsRemoved, err := a.db.DeleteProfilePhoto(r.Context(), user.ID, mediaID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusBadRequest, "照片不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy profile photo delete failed")
		return
	}
	if objectsRemoved {
		a.removeObject(r, deleted.ID, deleted.ObjectKey)
		a.removeObject(r, deleted.ID, deleted.ThumbKey)
	}
	a.audit(r, &user.ID, "profile.photo_deleted", "media", mediaID,
		map[string]any{"media_id": mediaID, "source": "legacy"})
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("legacy profile photo reload failed", "error", err, "user_id", user.ID)
	}
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos, a.legacyProfileHasFace(r.Context(), user.ID)))
}

func (a *App) handleLegacyProfilePhotoCover(w http.ResponseWriter, r *http.Request) {
	user, profile, ok := a.legacyPhotoActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req legacyPhotoIndexRequest
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	if req.Index < 0 || req.Index >= len(profile.Photos) {
		legacyError(w, http.StatusBadRequest, "照片不存在")
		return
	}
	photos := profile.Photos
	if req.Index > 0 {
		var err error
		photos, err = a.db.SetProfilePhotoCover(r.Context(), user.ID, profile.Photos[req.Index].MediaID)
		if errors.Is(err, db.ErrNotFound) {
			legacyError(w, http.StatusBadRequest, "照片不存在")
			return
		}
		if err != nil {
			legacyServerError(a, w, r, err, "legacy profile photo cover failed")
			return
		}
	}
	a.audit(r, &user.ID, "profile.photo_cover", "media", profile.Photos[req.Index].MediaID,
		map[string]any{"media_id": profile.Photos[req.Index].MediaID, "source": "legacy"})
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos, a.legacyProfileHasFace(r.Context(), user.ID)))
}

func (a *App) handleLegacyPublicProfile(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	viewerProfile, err := a.db.GetProfile(r.Context(), viewer.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy public profile viewer failed")
		return
	}
	if !profileCanBrowse(viewerProfile) {
		legacyError(w, http.StatusForbidden, profileGateMessage(viewerProfile))
		return
	}
	target, err := a.resolveLegacyPublicProfile(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, errInvalidProfileRef):
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy public profile resolve failed")
		return
	}
	if target.ID != viewer.ID && !profilePubliclyVisible(target) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	social, err := a.db.GetSocialProfile(r.Context(), target.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			legacyError(w, http.StatusNotFound, "用户不存在")
			return
		}
		legacyServerError(a, w, r, err, "legacy public social profile failed")
		return
	}

	liked := false
	if overview, err := a.db.SocialOverview(r.Context(), viewer.ID, a.clock.Now()); err != nil {
		a.logger(r).Warn("legacy public profile likes failed", "error", err, "user_id", viewer.ID)
	} else {
		for _, pair := range overview.MyLikes {
			if pair.Active && pair.Other.ID == target.ID {
				liked = true
				break
			}
		}
	}
	praised := false
	if state, err := a.db.PraiseStateFor(r.Context(), viewer.ID, a.clock.Now()); err != nil {
		a.logger(r).Warn("legacy public profile praise failed", "error", err, "user_id", viewer.ID)
	} else {
		for _, id := range state.Praised {
			if id == target.ID {
				praised = true
				break
			}
		}
	}
	isSelf := target.ID == viewer.ID
	canLike := !isSelf && !liked &&
		!profileIsObserver(viewerProfile) &&
		viewerProfile.AccountStatus == "active" &&
		viewerProfile.Gender != "" && target.Gender != "" &&
		viewerProfile.Gender != target.Gender

	card := buildCardView(social, liked, praised, 0, a.clock.Now())
	// 公开资料页的文字字段维持原可见性，但照片 URL 只有在本人、刚下发过该用户
	// 牌堆的短时 grant、或存在持久关系（点赞/同活动/消息）时才返回；否则返回空
	// 数组，避免该端点成为 /cards/photo/{uuid} 的 UUID 发现面。
	photosAllowed := target.ID == viewer.ID
	if !photosAllowed {
		photosAllowed = a.photoGrants != nil && a.photoGrants.AllowedOwner(viewer.ID, target.ID)
	}
	if !photosAllowed {
		// 公开页必须按「viewer 与 target 本人」的关系判断；不能复用
		// GetCardPhotoForViewer 的 media 级授权（共享 media 可能属于另一个
		// 画廊，viewer 只与那个画廊有 like/活动关系）。
		allowed, galleryErr := a.db.CanViewGallery(r.Context(), target.ID, viewer.ID)
		if galleryErr != nil {
			a.logger(r).Warn("legacy public profile gallery check failed", "error", galleryErr, "user_id", viewer.ID)
		} else {
			photosAllowed = allowed
		}
	}
	photos := []string{}
	photosThumb := []string{}
	photosMeta := []any{}
	if photosAllowed && len(target.Photos) > 0 {
		photos = legacyProfilePhotoURLs(target.Photos)
		photosThumb = legacyProfilePhotoThumbURLs(target.Photos)
		photosMeta = legacyProfilePhotoMeta(target.Photos)
		if a.photoGrants != nil {
			a.photoGrants.Grant(viewer.ID, []int64{target.ID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openid":         socialCardOpenID(social),
		"user_id":        legacyProfileID(target, db.User{}),
		"nickname":       social.DisplayName(),
		"gender":         target.Gender,
		"photos":         photos,
		"photos_thumb":   photosThumb,
		"photos_meta":    photosMeta,
		"display_fields": card.DisplayFields,
		"is_self":        isSelf,
		"liked":          liked,
		"praised":        praised,
		"can_like":       canLike,
	})
}

func (a *App) resolveLegacyPublicProfile(ctx context.Context, ref string) (db.Profile, error) {
	profile, err := a.resolveProfile(ctx, ref)
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return db.Profile{}, err
	}
	social, serr := a.db.GetSocialProfileByLegacyOpenID(ctx, strings.TrimSpace(ref))
	if serr != nil {
		if errors.Is(serr, db.ErrNotFound) {
			return db.Profile{}, db.ErrNotFound
		}
		return db.Profile{}, serr
	}
	return a.db.GetProfile(ctx, social.ID)
}

type legacyAccountStatusRequest struct {
	AccountStatus string `json:"account_status"`
	Status        string `json:"status"`
}

func (a *App) handleLegacyAccountStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy account status profile failed")
		return
	}
	if profileIsObserver(profile) {
		legacyError(w, http.StatusForbidden, "村情六处账号不可切换账号状态")
		return
	}
	if profile.AccountStatus != "active" && profile.AccountStatus != "matched" {
		legacyError(w, http.StatusBadRequest, "当前状态暂不支持切换")
		return
	}
	var req legacyAccountStatusRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
			return
		}
	}
	target := strings.TrimSpace(firstNonEmpty(req.AccountStatus, req.Status))
	if target == "" {
		if profile.AccountStatus == "active" {
			target = "matched"
		} else {
			target = "active"
		}
	}
	previous := profile.AccountStatus
	status, err := a.db.SetAccountStatus(r.Context(), user.ID, target)
	var invalid *db.ValidationError
	switch {
	case errors.As(err, &invalid):
		legacyError(w, http.StatusBadRequest, invalid.Message)
		return
	case errors.Is(err, db.ErrActiveSignupBlocked):
		legacyError(w, http.StatusBadRequest, "你已报名活动，请先取消报名再脱单")
		return
	case errors.Is(err, db.ErrObserverNoSwitch):
		legacyError(w, http.StatusForbidden, "村情六处账号不可切换账号状态")
		return
	case errors.Is(err, db.ErrStatusNotSwitchable):
		legacyError(w, http.StatusBadRequest, "当前状态暂不支持切换")
		return
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy account status update failed")
		return
	}
	a.audit(r, &user.ID, "account.status_changed", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"from": previous, "to": status, "source": "legacy"})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"account_status": legacyAccountStatus(status),
	})
}

func (a *App) handleLegacyAccountSwitch(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != "user" && role != "observer" {
		legacyError(w, http.StatusBadRequest, "无效的身份类型")
		return
	}
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy account switch profile failed")
		return
	}
	// Observer-only legacy accounts cannot switch into the member identity.
	if profileIsObserver(profile) && role == "user" {
		legacyError(w, http.StatusForbidden, "你尚未注册普通用户身份，请先完成对应注册")
		return
	}
	enabled, err := a.db.ObserverEnabled(r.Context(), user.ID)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy observer flag failed")
		return
	}
	if role == "observer" && !enabled {
		legacyError(w, http.StatusForbidden, "你尚未注册村情六处身份，请先完成对应注册")
		return
	}
	if role == "observer" {
		http.SetCookie(w, &http.Cookie{
			Name: "yxq_view_role", Value: "observer", Path: "/",
			HttpOnly: true, Secure: a.cfg.SessionCookieSecure,
			SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
		})
	} else {
		http.SetCookie(w, &http.Cookie{
			Name: "yxq_view_role", Value: "", Path: "/",
			HttpOnly: true, Secure: a.cfg.SessionCookieSecure,
			SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": role})
}

func (a *App) legacyProfileGate(w http.ResponseWriter, r *http.Request, user db.User) (db.Profile, bool) {
	profile, err := a.db.GetProfile(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "用户不存在")
		return db.Profile{}, false
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy profile gate failed")
		return db.Profile{}, false
	}
	if !profileCanBrowse(profile) {
		legacyError(w, http.StatusForbidden, profileGateMessage(profile))
		return db.Profile{}, false
	}
	return profile, true
}

func (a *App) handleLegacySuggestion(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	profile, ok := a.legacyProfileGate(w, r, user)
	if !ok {
		return
	}
	var req struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.Content = strings.TrimSpace(req.Content)
	switch req.Type {
	case "功能建议", "问题反馈", "其他":
	default:
		legacyError(w, http.StatusBadRequest, "请选择反馈类型")
		return
	}
	if req.Content == "" {
		legacyError(w, http.StatusBadRequest, "请填写反馈内容")
		return
	}
	if len([]rune(req.Content)) > 500 {
		legacyError(w, http.StatusBadRequest, "反馈内容最多 500 字")
		return
	}
	author := profile.Name
	if strings.TrimSpace(author) == "" {
		author = user.Name
	}
	if err := a.db.CreateSuggestion(r.Context(), user.ID, author, req.Type, req.Content); err != nil {
		legacyServerError(a, w, r, err, "legacy suggestion create failed")
		return
	}
	a.audit(r, &user.ID, "suggestion.created", "suggestion", "", map[string]any{
		"type": req.Type, "source": "legacy",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleLegacyFeedback(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	if _, ok := a.legacyProfileGate(w, r, user); !ok {
		return
	}
	var req struct {
		TargetOpenID string `json:"target_openid"`
		Reason       string `json:"reason"`
	}
	if !legacyDecodeJSONBody(w, r, &req) {
		return
	}
	req.TargetOpenID = strings.TrimSpace(req.TargetOpenID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.TargetOpenID == "" {
		legacyError(w, http.StatusBadRequest, "缺少被反馈用户")
		return
	}
	if req.Reason == "" {
		legacyError(w, http.StatusBadRequest, "请填写反馈原因")
		return
	}
	target, err := a.resolveLegacyPublicProfile(r.Context(), req.TargetOpenID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy feedback target failed")
		return
	}
	label := strings.TrimSpace(target.Nickname)
	if label == "" {
		label = target.Name
	}
	if id := legacyProfileID(target, db.User{}); id != "" && id != "0" {
		label = label + "（" + id + "）"
	}
	content := "目标 " + label + "；" + legacyClipText(req.Reason, 500)
	if err := a.db.CreateSuggestion(r.Context(), user.ID, user.Name, "卡片反馈", content); err != nil {
		legacyServerError(a, w, r, err, "legacy feedback create failed")
		return
	}
	a.audit(r, &user.ID, "feedback.created", "suggestion", "", map[string]any{
		"target_open_id": req.TargetOpenID, "source": "legacy",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// The Feishu pre-registration claim flow no longer exists in this product.
// Returning an explicit 410 keeps the old SPA from showing a bare 404.
func (a *App) handleLegacyClaimDisabled(w http.ResponseWriter, r *http.Request) {
	legacyError(w, http.StatusGone, "飞书认领入口已停用，请使用手机号登录")
}
