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

func (a *App) legacyEditableProfile(w http.ResponseWriter, r *http.Request) (db.User, db.Profile, bool) {
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
	user, profile, ok := a.legacyEditableProfile(w, r)
	if !ok {
		return
	}
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	var body map[string]any
	if !legacyDecodeJSONBody(w, r, &body) {
		return
	}
	upd, err := legacyProfileUpdateFromJSON(body)
	if err != nil {
		legacyError(w, http.StatusBadRequest, err.Error())
		return
	}
	if upd.Empty() {
		legacyError(w, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	_, err = a.db.UpdateProfile(r.Context(), user.ID, upd)
	var invalid *db.ValidationError
	switch {
	case errors.As(err, &invalid):
		legacyError(w, http.StatusBadRequest, invalid.Message)
		return
	case errors.Is(err, db.ErrPhoneConflict):
		legacyError(w, http.StatusConflict, "手机号码已被其他账号使用")
		return
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusNotFound, "用户不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy profile update failed")
		return
	}
	a.audit(r, &user.ID, "profile.updated", "user", strconv.FormatInt(profile.ID, 10),
		map[string]any{"fields": profileUpdateFieldNames(upd), "source": "legacy"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "资料已更新"})
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
	select {
	case a.mediaSem <- struct{}{}:
		defer a.releaseMedia()
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
		if m, derr := a.db.RemoveMediaOwned(r.Context(), user.ID, saved.ID); derr == nil {
			a.removeObject(r, m.ObjectKey)
			a.removeObject(r, m.ThumbKey)
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
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("legacy profile photo reload failed", "error", err, "user_id", user.ID)
		photos = append(profile.Photos, photo)
	}
	a.audit(r, &user.ID, "profile.photo_added", "media", photo.MediaID,
		map[string]any{"media_id": photo.MediaID, "source": "legacy"})
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos))
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
		legacyError(w, http.StatusBadRequest, "HEIC/HEIF 格式尚未支持，请改用 JPEG 或 PNG")
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
	deleted, err := a.db.DeleteProfilePhoto(r.Context(), user.ID, mediaID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		legacyError(w, http.StatusBadRequest, "照片不存在")
		return
	case err != nil:
		legacyServerError(a, w, r, err, "legacy profile photo delete failed")
		return
	}
	a.removeObject(r, deleted.ObjectKey)
	a.removeObject(r, deleted.ThumbKey)
	a.audit(r, &user.ID, "profile.photo_deleted", "media", mediaID,
		map[string]any{"media_id": mediaID, "source": "legacy"})
	photos, err := a.db.ProfilePhotos(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Warn("legacy profile photo reload failed", "error", err, "user_id", user.ID)
	}
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos))
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
	writeJSON(w, http.StatusOK, legacyPhotoResponse(photos))
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
	photos := legacyProfilePhotoURLs(target.Photos)
	writeJSON(w, http.StatusOK, map[string]any{
		"openid":         socialCardOpenID(social),
		"user_id":        legacyProfileID(target, db.User{}),
		"nickname":       social.DisplayName(),
		"gender":         target.Gender,
		"photos":         photos,
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
