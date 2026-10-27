package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

const maxFormBytes = 64 << 10

const (
	loginFailureThreshold = 5
	loginFailureWindow    = 15 * time.Minute
	loginLockDuration     = 15 * time.Minute
)

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	noticeKey := ""
	switch r.URL.Query().Get("logged_out") {
	case "1":
		noticeKey = "login.notice.logged_out"
	case "":
		if r.URL.Query().Get("activated") == "1" {
			noticeKey = "login.notice.activated"
		}
	}
	next := safeNext(r.URL.Query().Get("next"))
	if next == "/" {
		next = ""
	}
	a.render(w, r, http.StatusOK, "login", PageData{
		Title:         "登录",
		NoticeCopyKey: noticeKey,
		Next:          next,
		ScanSafe:      true,
		CopyPage:      "login",
	})
}

func (a *App) renderLogin(w http.ResponseWriter, r *http.Request, status int, message, phone, next string) {
	a.render(w, r, status, "login", PageData{
		Title:    "登录",
		Error:    message,
		Phone:    phone,
		Next:     next,
		ScanSafe: true,
		CopyPage: "login",
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		a.renderLogin(w, r, http.StatusBadRequest, "登录资料格式不正确。", "", "")
		return
	}
	if !a.formCSRFValid(r) {
		a.renderLogin(w, r, http.StatusForbidden, "表单已过期，请重新整理后再试。", "", "")
		return
	}

	phoneRaw := r.PostFormValue("phone")
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))
	phone := auth.NormalizePhone(phoneRaw)
	if phone == "" || password == "" {
		a.renderLogin(w, r, http.StatusBadRequest, "请输入手机号码与密码。", phoneRaw, next)
		return
	}

	ip := clientIP(r)
	if ok, retry := a.loginIPLimiter.Allow("ip:" + ip); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		a.renderLogin(w, r, http.StatusTooManyRequests, "尝试次数过多，请稍后再试。", phoneRaw, next)
		return
	}
	if ok, retry := a.loginPhoneLimiter.Allow("phone:" + phone); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		a.renderLogin(w, r, http.StatusTooManyRequests, "尝试次数过多，请稍后再试。", phoneRaw, next)
		return
	}
	if utf8.RuneCountInString(password) > auth.MaxPasswordLength {
		if !a.acquireHash(w, r) {
			return
		}
		auth.EqualizeTiming(password)
		a.releaseHash()
		a.renderLogin(w, r, http.StatusUnauthorized, "手机号码或密码错误。", phoneRaw, next)
		return
	}

	const genericFailure = "手机号码或密码错误。"

	ctx := r.Context()
	user, err := a.db.GetUserByPhone(ctx, phone)
	if errors.Is(err, db.ErrNotFound) {
		if !a.acquireHash(w, r) {
			return
		}
		auth.EqualizeTiming(password)
		a.releaseHash()
		a.audit(r, nil, "auth.login.failed", "phone", auth.MaskPhone(phone),
			map[string]any{"reason": "unknown_account"})
		a.logger(r).Warn("login failed", "reason", "unknown_account", "phone", auth.MaskPhone(phone))
		a.renderLogin(w, r, http.StatusUnauthorized, genericFailure, phoneRaw, next)
		return
	}
	if err != nil {
		a.logger(r).Error("load user for login failed", "error", err)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}

	if user.Locked(a.clock.Now()) {
		w.Header().Set("Retry-After", retryAfterSeconds(int(loginLockDuration.Seconds())))
		a.audit(r, &user.ID, "auth.login.locked", "user", strconv.FormatInt(user.ID, 10),
			map[string]any{"reason": "lockout_active"})
		a.renderLogin(w, r, http.StatusTooManyRequests, "尝试次数过多，请 15 分钟后再试。", phoneRaw, next)
		return
	}

	hash, err := a.db.GetPasswordHash(ctx, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		if !a.acquireHash(w, r) {
			return
		}
		auth.EqualizeTiming(password)
		a.releaseHash()
		a.audit(r, &user.ID, "auth.login.failed", "user", strconv.FormatInt(user.ID, 10),
			map[string]any{"reason": "no_credential"})
		a.logger(r).Warn("login failed", "reason", "no_credential", "user_id", user.ID)
		a.renderLogin(w, r, http.StatusUnauthorized, genericFailure, phoneRaw, next)
		return
	}
	if err != nil {
		a.logger(r).Error("load credential failed", "error", err, "user_id", user.ID)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}

	if !a.acquireHash(w, r) {
		return
	}
	valid, err := auth.VerifyPassword(password, hash)
	a.releaseHash()
	if err != nil {
		a.logger(r).Error("verify password failed", "error", err, "user_id", user.ID)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}
	if !valid {
		count, lockedUntil, ferr := a.db.RecordFailedLogin(ctx, user.ID,
			loginFailureThreshold, loginLockDuration, loginFailureWindow)
		if ferr != nil {
			a.logger(r).Error("record failed login failed", "error", ferr, "user_id", user.ID)
		}
		a.audit(r, &user.ID, "auth.login.failed", "user", strconv.FormatInt(user.ID, 10),
			map[string]any{"reason": "bad_password", "failed_count": count})
		a.logger(r).Warn("login failed", "reason", "bad_password", "user_id", user.ID)
		if ferr == nil && lockedUntil != nil && lockedUntil.After(a.clock.Now()) {
			w.Header().Set("Retry-After", retryAfterSeconds(int(loginLockDuration.Seconds())))
			a.renderLogin(w, r, http.StatusTooManyRequests, "尝试次数过多，请 15 分钟后再试。", phoneRaw, next)
			return
		}
		a.renderLogin(w, r, http.StatusUnauthorized, genericFailure, phoneRaw, next)
		return
	}

	if user.Status != "active" {
		a.audit(r, &user.ID, "auth.login.failed", "user", strconv.FormatInt(user.ID, 10),
			map[string]any{"reason": "account_disabled"})
		a.logger(r).Warn("login rejected for disabled account", "user_id", user.ID)
		a.renderLogin(w, r, http.StatusUnauthorized, genericFailure, phoneRaw, next)
		return
	}

	mustChange, tempExpiresAt, err := a.db.GetCredentialState(ctx, user.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		a.logger(r).Error("load credential state failed", "error", err, "user_id", user.ID)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}
	if mustChange && tempExpiresAt != nil && !tempExpiresAt.After(a.clock.Now()) {
		a.audit(r, &user.ID, "auth.login.failed", "user", strconv.FormatInt(user.ID, 10),
			map[string]any{"reason": "temp_password_expired"})
		a.renderLogin(w, r, http.StatusUnauthorized, "临时密码已过期，请联络管理员重新设定。", phoneRaw, next)
		return
	}

	if err := a.db.ResetLoginFailures(ctx, user.ID); err != nil {
		a.logger(r).Error("reset login failures failed", "error", err, "user_id", user.ID)
	}
	if sess, ok := currentSession(r); ok {
		if err := a.db.RevokeSessionByID(ctx, sess.ID); err != nil {
			a.logger(r).Warn("revoke previous session failed", "error", err)
		}
	}

	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		a.logger(r).Error("create session token failed", "error", err)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}
	session, err := a.db.CreateSession(ctx, user.ID, tokenHash, a.cfg.SessionTTL, ip, r.UserAgent())
	if err != nil {
		a.logger(r).Error("create session failed", "error", err, "user_id", user.ID)
		a.renderLogin(w, r, http.StatusInternalServerError, "系统暂时无法登录，请稍后再试。", phoneRaw, next)
		return
	}
	a.setSessionCookie(w, raw, session.ExpiresAt)
	a.rotateCSRF(w, r)
	a.audit(r, &user.ID, "auth.login.success", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"via": "password"})
	a.logger(r).Info("login succeeded", "user_id", user.ID)
	if state, err := a.db.GetRegistrationState(ctx, user.ID); err == nil &&
		state.RegisteredAt != nil {
		switch state.AccountStatus {
		case "pending", "rejected", "left":
			next = "/register/done"
		}
	}
	if mustChange {
		next = "/password?required=1"
	}
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}
	if sess, ok := currentSession(r); ok {
		if err := a.db.RevokeSessionByID(r.Context(), sess.ID); err != nil {
			a.logger(r).Error("revoke session on logout failed", "error", err)
		}
	}
	if user, ok := currentUser(r); ok {
		a.audit(r, &user.ID, "auth.logout", "user", strconv.FormatInt(user.ID, 10), nil)
	}
	a.clearSessionCookie(w)
	a.clearCSRFCookie(w)
	http.Redirect(w, r, "/login?logged_out=1", http.StatusSeeOther)
}

func (a *App) handleActivatePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		a.renderError(w, r, http.StatusBadRequest, "启用链接不完整", "请使用管理员提供的完整启用链接。")
		return
	}
	a.renderActivate(w, r, http.StatusOK, token, "")
}

func (a *App) renderActivate(w http.ResponseWriter, r *http.Request, status int, token, message string) {
	at, err := a.db.GetActivationToken(r.Context(), auth.HashToken(token))
	if errors.Is(err, db.ErrInvalidActivation) {
		a.renderError(w, r, http.StatusBadRequest, "启用链接无效", "请向管理员索取新的启用链接。")
		return
	}
	if err != nil {
		a.logger(r).Error("load activation token failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	if !at.Valid {
		if at.ConsumedAt != nil {
			a.renderError(w, r, http.StatusGone, "启用链接已使用", "此链接只能使用一次，请直接登录。")
		} else {
			a.renderError(w, r, http.StatusGone, "启用链接已过期", "请向管理员索取新的启用链接。")
		}
		return
	}
	if at.UserID != nil {
		has, err := a.db.HasCredential(r.Context(), *at.UserID)
		if err != nil {
			a.logger(r).Error("check credential failed", "error", err)
			a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
			return
		}
		if has {
			a.renderError(w, r, http.StatusGone, "账号已启用", "此账号已设定密码，请直接登录。")
			return
		}
	}
	a.render(w, r, status, "activate", PageData{
		Title:    "设定密码",
		Token:    token,
		Phone:    auth.MaskPhone(at.Phone),
		Error:    message,
		ScanSafe: true,
		CopyPage: "activate",
		Data:     activatePageData{NameHTML: asciiEntityText(at.Name)},
	})
}

// activatePageData carries the activation page copy inputs. NameHTML is the
// account name rendered with ASCII-only HTML numeric references (see
// asciiEntityText): the page body must stay Han-free while still showing the
// name to the token holder.
type activatePageData struct {
	NameHTML template.HTML
}

func (a *App) handleActivate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "启用失败", "表单格式不正确，请重新整理后再试。")
		return
	}
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}

	token := r.PostFormValue("token")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")
	if token == "" {
		a.renderError(w, r, http.StatusBadRequest, "启用链接不完整", "请使用管理员提供的完整启用链接。")
		return
	}
	if password != confirm {
		a.renderActivate(w, r, http.StatusBadRequest, token, "两次输入的密码不一致。")
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		a.renderActivate(w, r, http.StatusBadRequest, token, passwordPolicyMessage(err))
		return
	}

	ip := clientIP(r)
	if ok, retry := a.activationLimiter.Allow("activate:" + ip); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
		a.renderError(w, r, http.StatusTooManyRequests, "尝试次数过多", "请稍后再试。")
		return
	}

	// Validate the token before spending Argon2 memory/CPU. Otherwise an
	// attacker can force a 64 MiB hash for every bogus activation request.
	tokenHash := auth.HashToken(token)
	at, err := a.db.GetActivationToken(r.Context(), tokenHash)
	if errors.Is(err, db.ErrInvalidActivation) {
		a.renderError(w, r, http.StatusGone, "启用链接无效或已过期", "请向管理员索取新的启用链接。")
		return
	}
	if err != nil {
		a.logger(r).Error("load activation token failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	if !at.Valid {
		a.renderError(w, r, http.StatusGone, "启用链接无效或已过期", "请向管理员索取新的启用链接。")
		return
	}
	if at.UserID != nil {
		has, err := a.db.HasCredential(r.Context(), *at.UserID)
		if err != nil {
			a.logger(r).Error("check credential failed", "error", err)
			a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
			return
		}
		if has {
			a.renderError(w, r, http.StatusConflict, "账号已启用", "此账号已设定密码，请直接登录。")
			return
		}
	}

	if !a.acquireHash(w, r) {
		return
	}
	hash, err := auth.HashPassword(password)
	a.releaseHash()
	if err != nil {
		a.logger(r).Error("hash password failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}

	user, err := a.db.ActivateUser(r.Context(), db.ActivateParams{
		TokenHash:    tokenHash,
		PasswordHash: hash,
		IP:           ip,
		UserAgent:    r.UserAgent(),
	})
	switch {
	case errors.Is(err, db.ErrInvalidActivation):
		a.renderError(w, r, http.StatusGone, "启用链接无效或已过期", "请向管理员索取新的启用链接。")
		return
	case errors.Is(err, db.ErrAlreadyActivated):
		a.renderError(w, r, http.StatusConflict, "账号已启用", "此账号已设定密码，请直接登录。")
		return
	case errors.Is(err, db.ErrAccountDisabled):
		a.renderError(w, r, http.StatusForbidden, "账号已停用", "请联络管理员。")
		return
	case errors.Is(err, db.ErrPhoneConflict):
		a.renderError(w, r, http.StatusConflict, "手机号码已被使用", "请联络管理员处理。")
		return
	case err != nil:
		a.logger(r).Error("activate user failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}

	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		a.logger(r).Error("create activation session token failed", "error", err)
		http.Redirect(w, r, "/login?activated=1", http.StatusSeeOther)
		return
	}
	session, err := a.db.CreateSession(r.Context(), user.ID, tokenHash, a.cfg.SessionTTL, ip, r.UserAgent())
	if err != nil {
		a.logger(r).Error("create activation session failed", "error", err, "user_id", user.ID)
		http.Redirect(w, r, "/login?activated=1", http.StatusSeeOther)
		return
	}
	a.setSessionCookie(w, raw, session.ExpiresAt)
	a.rotateCSRF(w, r)
	a.audit(r, &user.ID, "auth.login.success", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"via": "activation"})
	a.logger(r).Info("activation succeeded", "user_id", user.ID)
	http.Redirect(w, r, "/?activated=1", http.StatusSeeOther)
}

func passwordPolicyMessage(err error) string {
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort):
		return fmt.Sprintf("密码至少需要 %d 个字元。", auth.MinPasswordLength)
	case errors.Is(err, auth.ErrPasswordTooLong):
		return fmt.Sprintf("密码最多 %d 个字元。", auth.MaxPasswordLength)
	case errors.Is(err, auth.ErrPasswordInvalid):
		return "密码含有不允许的控制字元。"
	default:
		return "密码不符合规则。"
	}
}

// audit writes an append-only audit row without blocking forever on a broken
// database. Errors are logged with the action name only.
func (a *App) audit(r *http.Request, actor *int64, action, targetType, targetID string, meta map[string]any) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	err := a.db.InsertAudit(ctx, db.AuditEntry{
		ActorUserID: actor,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		IP:          clientIP(r),
		UserAgent:   r.UserAgent(),
		Metadata:    meta,
	})
	if err != nil {
		a.logger(r).Error("audit write failed", "action", action, "error", err)
	}
}

// isEmbeddedAdminRequest reports whether the browser is navigating inside the
// admin iframe. Newer browsers send Sec-Fetch-Dest; the SPA's first load adds
// embed=1, which covers older browsers for the entry navigation.
func isEmbeddedAdminRequest(r *http.Request) bool {
	return r.Header.Get("Sec-Fetch-Dest") == "iframe" || r.URL.Query().Get("embed") == "1"
}

// requireUserHTML redirects anonymous visitors to the login page. Admin pages
// are reached through the SPA iframe, where /login is blocked by
// frame-ancestors 'none'; those requests get an admin-styled session error
// with an explicit re-login link instead of a blank iframe.
func (a *App) requireUserHTML(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	u, ok := currentUser(r)
	if ok {
		return u, true
	}
	// 管理台只在同源 iframe 内使用；但 iframe 内跳 /login 会被
	// frame-ancestors 'none' 拒绝，留下空白 overlay。仅在「带了会话 cookie 但
	// 已失效」（会话过期/被撤销）时渲染管理台错误页；从未登录的无 cookie
	// 请求保持登录跳转语义（CLI/测试同样适用）。
	if isAdminPagePath(r.URL.Path) && isEmbeddedAdminRequest(r) {
		if _, err := r.Cookie(a.cfg.SessionCookieName); err == nil && a.cfg.SessionCookieName != "" {
			a.renderAdminError(w, r, http.StatusUnauthorized, "登录已过期", "登录状态已失效，请重新登录后再打开管理台。")
			return db.User{}, false
		}
	}
	next := "/"
	if r.Method == http.MethodGet {
		if uri := r.URL.RequestURI(); uri != "" && uri != "/" {
			next = uri
		}
	}
	dest := "/login"
	if next != "/" {
		dest += "?next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
	return db.User{}, false
}

func (a *App) requireUserAPI(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	u, ok := currentUser(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "authentication_required", "请先登录。")
		return db.User{}, false
	}
	return u, true
}
