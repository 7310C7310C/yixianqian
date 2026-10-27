package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

var (
	registerIPLimiter    = auth.NewLimiter(10, time.Hour)
	registerPhoneLimiter = auth.NewLimiter(5, 15*time.Minute)
)

func init() { registerModule(registerRegistration) }

func registerRegistration(a *App, mux *http.ServeMux) {
	// /welcome was merged into the landing page. Keep the old URL alive for
	// bookmarks and bot links with a permanent redirect; /register?from=welcome
	// still records the join_click source.
	mux.HandleFunc("GET /welcome", handleWelcomeRedirect)
	mux.HandleFunc("GET /register", a.handleRegisterPage)
	mux.HandleFunc("POST /register", a.handleRegisterSubmit)
	mux.HandleFunc("GET /register/done", a.handleRegisterDone)
}

func handleWelcomeRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusMovedPermanently)
}

func (a *App) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	switch source := r.URL.Query().Get("from"); source {
	case "welcome", "home":
		a.trackPublicEvent(r, "join_click", source)
	default:
		a.trackPublicEvent(r, "page_view", "register")
	}
	a.renderRegister(w, r, http.StatusOK, nil, "")
}

func (a *App) renderRegister(w http.ResponseWriter, r *http.Request, status int, values url.Values, message string) {
	data := buildRegisterPageData(values)
	data.MaxMB = int(a.cfg.MediaMaxBytes / (1 << 20))
	a.render(w, r, status, "register", PageData{
		Title:   "注册",
		Error:   message,
		Data:    data,
		PageCSS: "register.css",
	})
}

func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	maxBody := int64(registerMaxPhotos)*a.cfg.MediaMaxBytes + (2 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	ip := clientIP(r)
	if ok, retry := registerIPLimiter.Allow("reg-ip:" + ip); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		a.renderRegister(w, r, http.StatusTooManyRequests, nil, "提交次数过多，请稍后再试。")
		return
	}

	if err := r.ParseMultipartForm(4 << 20); err != nil {
		status, _, message := uploadParseErrorDetails(err)
		if status == http.StatusRequestEntityTooLarge {
			message = "表单与照片总大小超过上限，请减少照片数量或尺寸。"
		}
		a.renderRegister(w, r, status, nil, message)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	values := url.Values(r.MultipartForm.Value)
	if !a.formCSRFValid(r) {
		a.renderRegister(w, r, http.StatusForbidden, values, "表单已过期，请重新整理后再提交。")
		return
	}

	input, ferr := parseRegisterInput(values)
	if ferr != nil {
		a.renderRegister(w, r, http.StatusBadRequest, values, ferr.Message)
		return
	}
	if ok, retry := registerPhoneLimiter.Allow("reg-phone:" + input.Phone); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		a.renderRegister(w, r, http.StatusTooManyRequests, values, "此手机号码提交次数过多，请稍后再试。")
		return
	}

	photos, ferr := registrationPhotoParts(r.MultipartForm.File["photos"])
	if ferr != nil {
		a.renderRegister(w, r, http.StatusBadRequest, values, ferr.Message)
		return
	}

	ctx := r.Context()
	if _, err := a.db.GetUserByPhone(ctx, input.Phone); err == nil {
		a.renderRegister(w, r, http.StatusConflict, values, "此手机号码已注册，请直接登录或联系小天使。")
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		a.logger(r).Error("registration duplicate check failed", "error", err)
		a.renderRegister(w, r, http.StatusInternalServerError, values, "系统暂时无法处理，请稍后再试。")
		return
	}

	// An unknown inviter must not block a valid registration: the raw ref is
	// already kept in referral_source for manual follow-up, and old clients
	// often arrived with stale or mistyped IDs.
	var inviter db.User
	if input.Inviter != "" {
		user, err := a.db.FindAdminUserByRef(ctx, input.Inviter)
		switch {
		case errors.Is(err, db.ErrNotFound):
			a.logger(r).Info("registration inviter not found")
		case err != nil:
			a.logger(r).Warn("resolve registration inviter failed", "error", err)
		default:
			inviter = user
		}
	}

	sealedID, err := sealIDCard(a.cfg.SessionSecret, input.IDCard)
	if err != nil {
		a.logger(r).Error("encrypt id card failed", "error", err)
		a.renderRegister(w, r, http.StatusInternalServerError, values, "系统暂时无法处理，请稍后再试。")
		return
	}

	if !a.acquireHash(w, r) {
		return
	}
	passwordHash, err := auth.HashPassword(input.Password)
	a.releaseHash()
	if err != nil {
		a.logger(r).Error("hash registration password failed", "error", err)
		a.renderRegister(w, r, http.StatusInternalServerError, values, "系统暂时无法处理，请稍后再试。")
		return
	}

	referral := input.ReferralSource
	if input.Inviter != "" {
		referral += " | 邀请人: " + input.Inviter
	}
	last4 := ""
	if len(input.IDCard) >= 4 {
		last4 = input.IDCard[len(input.IDCard)-4:]
	}

	user, err := a.db.CreateRegisteredUser(ctx, db.RegisterParams{
		Phone:           input.Phone,
		Name:            input.Name,
		Nickname:        input.Nickname,
		Gender:          input.Gender,
		Birthday:        input.Birthday,
		HeightCM:        input.HeightCM,
		Education:       input.Education,
		BaptismalName:   input.BaptismalName,
		Church:          input.Church,
		ChurchLocation:  input.ChurchLocation,
		ChurchGroup:     input.ChurchGroup,
		NativePlace:     input.NativePlace,
		City:            input.City,
		Industry:        input.Industry,
		Position:        input.Position,
		Income:          input.Income,
		House:           input.House,
		Driving:         input.Driving,
		Marriage:        input.Marriage,
		Family:          input.Family,
		LiveWithParents: input.LiveWithParents,
		Personality:     input.Personality,
		SelfTraits:      input.SelfTraits,
		SelfHobbies:     input.SelfHobbies,
		SelfSports:      input.SelfSports,
		MBTI:            input.MBTI,
		PartnerCriteria: input.PartnerCriteria,
		PartnerTraits:   input.PartnerTraits,
		PartnerHobbies:  input.PartnerHobbies,
		PartnerSports:   input.PartnerSports,
		Wechat:          input.Wechat,
		IDCardCipher:    sealedID,
		IDCardLast4:     last4,
		IDCardHash:      idCardHash(a.cfg.SessionSecret, input.IDCard),
		ReferralSource:  referral,
		IsForChild:      input.IsForChild,
		ContactConsent:  true,
		PasswordHash:    passwordHash,
	})
	switch {
	case errors.Is(err, db.ErrPhoneConflict):
		a.renderRegister(w, r, http.StatusConflict, values, "此手机号码已注册，请直接登录或联系小天使。")
		return
	case errors.Is(err, db.ErrIDCardConflict):
		a.renderRegister(w, r, http.StatusConflict, values, "此身份证号已被其他账号使用，请勿重复注册。")
		return
	case errors.Is(err, db.ErrConflict):
		a.renderRegister(w, r, http.StatusConflict, values, "资料与现有账号冲突，请联系小天使。")
		return
	case err != nil:
		a.logger(r).Error("create registered user failed", "error", err)
		a.renderRegister(w, r, http.StatusInternalServerError, values, "系统暂时无法处理，请稍后再试。")
		return
	}

	if !a.acquireMedia(w, r, false) {
		a.cleanupRegistration(r, user.ID)
		return
	}
	mediaIDs, photoErr := a.saveRegistrationPhotos(ctx, user.ID, photos)
	a.releaseMedia()
	if photoErr != nil {
		a.cleanupRegistration(r, user.ID)
		status, message := registrationPhotoError(photoErr)
		if status >= 500 {
			a.logger(r).Error("save registration photos failed", "error", photoErr, "user_id", user.ID)
		}
		a.renderRegister(w, r, status, values, message)
		return
	}
	// 注册照片刚写入 profile_photos，必须补 probe：否则新用户没有任何
	// face_checked 照片，会被 has_face 门禁锁死。此处 mediaSem 已释放，
	// probe 只受 faceSem 与总预算约束；失败不阻断注册，留给 facebackfill。
	a.probeRegistrationPhotos(ctx, user.ID, mediaIDs)

	inviteStatus := ""
	inviterBound := false
	if inviter.ID != 0 {
		inviteID, status, err := a.db.RecordInviteFromForm(ctx, user.ID, input.Phone,
			input.Gender, inviter.ID, "web-register")
		if err != nil {
			a.logger(r).Warn("record registration invite failed", "error", err, "user_id", user.ID)
		} else if inviteID > 0 {
			inviteStatus = status
			inviterBound = true
		}
	}

	a.notifyRegistration(ctx, r, user, input.Nickname, len(mediaIDs))

	meta := map[string]any{
		"photos":        len(mediaIDs),
		"inviter_bound": inviterBound,
	}
	if inviteStatus != "" {
		meta["invite_status"] = inviteStatus
	}
	a.audit(r, &user.ID, "register.submitted", "user", strconv.FormatInt(user.ID, 10), meta)

	// A logged-in member may register on behalf of a child; do not replace
	// their session with the new pending account.
	if _, loggedIn := currentSession(r); !loggedIn {
		a.startRegistrationSession(w, r, user)
	}
	http.Redirect(w, r, "/register/done?submitted=1", http.StatusSeeOther)
}

func (a *App) startRegistrationSession(w http.ResponseWriter, r *http.Request, user db.User) {
	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		a.logger(r).Error("create registration session token failed", "error", err)
		return
	}
	session, err := a.db.CreateSession(r.Context(), user.ID, tokenHash, a.cfg.SessionTTL,
		clientIP(r), r.UserAgent())
	if err != nil {
		a.logger(r).Error("create registration session failed", "error", err, "user_id", user.ID)
		return
	}
	a.setSessionCookie(w, raw, session.ExpiresAt)
	a.rotateCSRF(w, r)
	a.audit(r, &user.ID, "auth.login.success", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"via": "register"})
}

func (a *App) notifyRegistration(ctx context.Context, r *http.Request, user db.User, nickname string, photos int) {
	if _, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
		UserID:    user.ID,
		Kind:      "review",
		Title:     "注册资料已送出",
		Body:      "资料已收到，小天使/管理员会尽快审核；通过后会收到站内通知。",
		Link:      "/register/done",
		DedupeKey: fmt.Sprintf("register-submitted:%d", user.ID),
	}); err != nil {
		a.logger(r).Error("create registration notification failed", "error", err, "user_id", user.ID)
	}

	ids, err := a.db.ListAdminUserIDs(ctx, "user.review")
	if err != nil {
		a.logger(r).Error("list reviewers for registration notification failed", "error", err)
		return
	}
	body := fmt.Sprintf("%s（ID %d）提交了注册资料，共 %d 张照片。", nickname, user.ID, photos)
	for _, adminID := range ids {
		if _, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
			UserID:    adminID,
			Kind:      "review",
			Title:     "新用户待审核",
			Body:      body,
			Link:      "/admin/users?status=pending",
			DedupeKey: fmt.Sprintf("register-review:%d:%d", user.ID, adminID),
		}); err != nil {
			a.logger(r).Warn("notify reviewer failed", "error", err, "admin_id", adminID)
		}
	}
}

func registrationPhotoParts(files []*multipart.FileHeader) ([]*multipart.FileHeader, *registerFormError) {
	out := make([]*multipart.FileHeader, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.Filename) == "" {
			continue
		}
		out = append(out, file)
	}
	if len(out) < registerMinPhotos || len(out) > registerMaxPhotos {
		return nil, registerFormErr("photos",
			fmt.Sprintf("请上传 %d-%d 张个人照片。", registerMinPhotos, registerMaxPhotos))
	}
	return out, nil
}

// saveRegistrationPhotos 保存注册照片并返回 media ID 列表。调用方负责
// mediaSem；本函数不运行 probe。
func (a *App) saveRegistrationPhotos(ctx context.Context, userID int64, files []*multipart.FileHeader) ([]string, error) {
	mediaIDs := make([]string, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("register: open photo: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, a.cfg.MediaMaxBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("register: read photo: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("register: close photo: %w", closeErr)
		}
		if int64(len(data)) > a.cfg.MediaMaxBytes {
			return nil, media.ErrFileTooLarge
		}

		processed, err := media.ProcessWithLimits(data, a.cfg.MediaThumbSize,
			a.cfg.MediaMaxDim, a.cfg.MediaMaxPixels)
		if err != nil {
			return nil, err
		}

		id, err := platform.NewUUID()
		if err != nil {
			return nil, fmt.Errorf("register: generate media id: %w", err)
		}
		ext := "jpg"
		if processed.ContentType == "image/png" {
			ext = "png"
		}
		key := fmt.Sprintf("media/%d/%s.%s", userID, id, ext)
		thumbKey := fmt.Sprintf("media/%d/%s_thumb.jpg", userID, id)

		if err := a.store.Put(ctx, key, processed.Original, processed.ContentType); err != nil {
			return nil, err
		}
		if err := a.store.Put(ctx, thumbKey, processed.Thumbnail, "image/jpeg"); err != nil {
			a.removeStoredObject(ctx, id, key)
			return nil, err
		}
		saved, err := a.db.CreateMedia(ctx, db.Media{
			ID:           id,
			UserID:       userID,
			OriginalName: sanitizeFilename(header.Filename),
			ObjectKey:    key,
			ThumbKey:     thumbKey,
			ContentType:  processed.ContentType,
			ByteSize:     int64(len(processed.Original)),
			ThumbSize:    int64(len(processed.Thumbnail)),
			Width:        processed.Width,
			Height:       processed.Height,
		})
		if err != nil {
			a.removeStoredObject(ctx, id, key)
			a.removeStoredObject(ctx, id, thumbKey)
			return nil, err
		}
		photo, err := a.db.AddProfilePhoto(ctx, userID, saved.ID)
		if err != nil {
			if _, removed, rerr := a.db.RemoveMediaOwned(ctx, userID, saved.ID); rerr == nil && removed {
				a.removeStoredObject(ctx, id, key)
				a.removeStoredObject(ctx, id, thumbKey)
			}
			return nil, err
		}
		mediaIDs = append(mediaIDs, photo.MediaID)
	}
	return mediaIDs, nil
}

func (a *App) removeStoredObject(ctx context.Context, mediaID, key string) {
	if err := a.store.Remove(ctx, key); err != nil {
		a.log.Error("remove registration object failed", "error", err, "media_id", mediaID)
	}
}

func registrationPhotoError(err error) (int, string) {
	status, _, message := uploadErrorDetails(err)
	if status >= 500 {
		return status, "照片保存失败，请稍后再试。"
	}
	return status, message
}

func (a *App) cleanupRegistration(r *http.Request, userID int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	rows, err := a.db.ListMediaByUser(ctx, userID, 100)
	if err != nil {
		a.logger(r).Error("list media for registration cleanup failed", "error", err, "user_id", userID)
	}
	if err := a.db.DeleteRegisteredUser(ctx, userID); err != nil {
		a.logger(r).Error("delete failed registration failed", "error", err, "user_id", userID)
		return
	}
	for _, row := range rows {
		a.removeStoredObject(ctx, row.ID, row.ObjectKey)
		a.removeStoredObject(ctx, row.ID, row.ThumbKey)
	}
}

type registerDoneData struct {
	Heading     string
	Message     string
	Status      string
	CopyKey     string
	PhoneMasked string
	LoggedIn    bool
	CanEnter    bool
	Submitted   bool
}

// registerDoneCopyKey maps the account state to the public-copy.js table key.
// The Chinese heading/message stay server-side for <title> only; the body copy
// is injected at runtime so the HTML remains Han-free.
func registerDoneCopyKey(status string, submitted bool) string {
	if submitted {
		if status == "pending" {
			return "register_done.pending"
		}
		return "register_done.submitted"
	}
	switch status {
	case "pending", "rejected", "left", "active", "matched", "observer", "disabled":
		if status == "active" || status == "matched" || status == "observer" {
			return "register_done.active"
		}
		return "register_done." + status
	default:
		return "register_done.default"
	}
}

func (a *App) handleRegisterDone(w http.ResponseWriter, r *http.Request) {
	submitted := r.URL.Query().Get("submitted") == "1"
	data := registerDoneData{Submitted: submitted}
	if user, ok := currentUser(r); ok {
		data.LoggedIn = true
		data.PhoneMasked = auth.MaskPhone(user.Phone)
		if state, err := a.db.GetRegistrationState(r.Context(), user.ID); err == nil {
			data.Status = state.AccountStatus
			if state.RegisteredAt != nil {
				heading, message := registrationStatusText(state.AccountStatus)
				data.Heading, data.Message = heading, message
				switch state.AccountStatus {
				case "active", "matched", "observer":
					data.CanEnter = true
				}
			}
		} else if !errors.Is(err, db.ErrNotFound) {
			a.logger(r).Error("load registration state failed", "error", err, "user_id", user.ID)
		}
	}
	if submitted {
		data.Heading = "注册已送出"
		data.Message = "资料已收到，小天使/管理员会尽快审核；通过后会收到站内通知。"
		if data.Status == "pending" {
			heading, message := registrationStatusText("pending")
			data.Heading, data.Message = heading, message
		} else {
			// A logged-in member registered someone else; don't show their own
			// account under the new submission's status page.
			data.PhoneMasked = ""
		}
	} else if data.Heading == "" {
		data.Heading = "注册与审核"
		data.Message = "一线牵采人工审核，确认资料真实后才会开通使用。"
	}
	data.CopyKey = registerDoneCopyKey(data.Status, submitted)
	a.render(w, r, http.StatusOK, "register_done", PageData{
		Title:    data.Heading,
		ScanSafe: true,
		CopyPage: "register_done",
		Data:     data,
		PageCSS:  "register.css",
	})
}

// registrationGatePath reports whether an authenticated registered user must
// be stopped before entering the app.
func registrationGateApplies(path string) bool {
	switch path {
	case "/welcome", "/register", "/register/done", "/login", "/logout",
		"/activate", "/password", "/healthz", "/favicon.ico":
		return false
	}
	if strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/media/") {
		return false
	}
	return true
}

// registrationGate sends pending/rejected accounts to the review page instead
// of letting them browse the app. Legacy accounts have registered_at = NULL and
// keep their existing behaviour.
func (a *App) registrationGate(w http.ResponseWriter, r *http.Request, user db.User) bool {
	if !registrationGateApplies(r.URL.Path) {
		return false
	}
	state, err := a.db.GetRegistrationState(r.Context(), user.ID)
	if err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			a.logger(r).Error("registration gate lookup failed", "error", err, "user_id", user.ID)
		}
		return false
	}
	if state.RegisteredAt == nil {
		return false
	}
	switch state.AccountStatus {
	case "pending", "rejected", "left":
	default:
		return false
	}
	_, message := registrationStatusText(state.AccountStatus)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeAPIError(w, http.StatusForbidden, registrationGateCode(state.AccountStatus), message)
		return true
	}
	http.Redirect(w, r, "/register/done", http.StatusSeeOther)
	return true
}

func (a *App) trackPublicEvent(r *http.Request, event, source string) {
	if a.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := a.db.RecordTrackEvent(ctx, event, source, clientIP(r)); err != nil {
		a.logger(r).Debug("record public event failed", "error", err, "event", event)
	}
}
