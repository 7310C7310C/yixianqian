package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

const (
	adminPermUserReview  = "user.review"
	adminPermUserManage  = "user.manage"
	adminPermContent     = "content.moderate"
	adminPermAdminManage = "admin.manage"
	adminTempPasswordTTL = 7 * 24 * time.Hour
	adminListLimit       = 100
	adminMaxNoteRunes    = 500
)

func init() { registerModule(registerAdminRoutes) }

func registerAdminRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", a.adminHTML("", a.handleAdminHome))
	mux.HandleFunc("GET /admin/users", a.adminHTML(adminPermUserReview, a.handleAdminUsersPage))
	mux.HandleFunc("POST /admin/users/{id}/approve", a.adminHTML(adminPermUserReview, a.handleAdminUserApprove))
	mux.HandleFunc("POST /admin/users/{id}/reject", a.adminHTML(adminPermUserReview, a.handleAdminUserReject))
	mux.HandleFunc("POST /admin/users/{id}/disable", a.adminHTML(adminPermUserManage, a.handleAdminUserDisable))
	mux.HandleFunc("POST /admin/users/{id}/enable", a.adminHTML(adminPermUserManage, a.handleAdminUserEnable))
	mux.HandleFunc("POST /admin/users/{id}/temp-password", a.adminHTML(adminPermUserManage, a.handleAdminUserTempPassword))
	mux.HandleFunc("GET /admin/reports", a.adminHTML(adminPermContent, a.handleAdminReportsPage))
	mux.HandleFunc("POST /admin/reports/{id}/handle", a.adminHTML(adminPermContent, a.handleAdminReportHandle))
	mux.HandleFunc("GET /admin/suggestions", a.adminHTML(adminPermContent, a.handleAdminSuggestionsPage))
	mux.HandleFunc("POST /admin/suggestions/{id}/handle", a.adminHTML(adminPermContent, a.handleAdminSuggestionHandle))
	mux.HandleFunc("GET /admin/activities", a.adminHTML(adminPermActivity, a.handleAdminActivitiesPage))
	mux.HandleFunc("GET /admin/activities/new", a.adminHTML(adminPermActivity, a.handleAdminActivityNew))
	mux.HandleFunc("GET /admin/activities/{id}", a.adminHTML(adminPermActivity, a.handleAdminActivityDetail))
	mux.HandleFunc("GET /admin/activities/{id}/edit", a.adminHTML(adminPermActivity, a.handleAdminActivityEdit))
	mux.HandleFunc("GET /admin/activities/{id}/poster", a.adminHTMLMedia(adminPermActivity, a.handleAdminActivityPoster))
	mux.HandleFunc("POST /admin/activities", a.adminHTML(adminPermActivity, a.handleAdminActivityCreate))
	mux.HandleFunc("POST /admin/activities/{id}", a.adminHTML(adminPermActivity, a.handleAdminActivityUpdate))
	mux.HandleFunc("POST /admin/activities/{id}/poster", a.adminHTML(adminPermActivity, a.handleAdminActivityPosterUpload))
	mux.HandleFunc("POST /admin/activities/{id}/attendance", a.adminHTML(adminPermActivity, a.handleAdminActivityAttendance))
	mux.HandleFunc("POST /admin/activities/{id}/broadcast", a.adminHTML(adminPermActivity, a.handleAdminActivityBroadcast))
	mux.HandleFunc("GET /admin/roles", a.adminHTML(adminPermAdminManage, a.handleAdminRolesPage))
	mux.HandleFunc("POST /admin/roles/assign", a.adminHTML(adminPermAdminManage, a.handleAdminRoleAssign))

	mux.HandleFunc("GET /password", a.handlePasswordPage)
	mux.HandleFunc("POST /password", a.handlePasswordSubmit)

	mux.HandleFunc("GET /api/admin/me", a.adminAPI("", a.apiAdminMe))
	mux.HandleFunc("GET /api/admin/users", a.adminAPI(adminPermUserReview, a.apiAdminUsers))
	mux.HandleFunc("POST /api/admin/users/{id}/approve", a.adminAPI(adminPermUserReview, a.apiAdminUserApprove))
	mux.HandleFunc("POST /api/admin/users/{id}/reject", a.adminAPI(adminPermUserReview, a.apiAdminUserReject))
	mux.HandleFunc("POST /api/admin/users/{id}/disable", a.adminAPI(adminPermUserManage, a.apiAdminUserDisable))
	mux.HandleFunc("POST /api/admin/users/{id}/enable", a.adminAPI(adminPermUserManage, a.apiAdminUserEnable))
	mux.HandleFunc("POST /api/admin/users/{id}/temp-password", a.adminAPI(adminPermUserManage, a.apiAdminUserTempPassword))
	mux.HandleFunc("GET /api/admin/reports", a.adminAPI(adminPermContent, a.apiAdminReports))
	mux.HandleFunc("POST /api/admin/reports/{id}/handle", a.adminAPI(adminPermContent, a.apiAdminReportHandle))
	mux.HandleFunc("GET /api/admin/suggestions", a.adminAPI(adminPermContent, a.apiAdminSuggestions))
	mux.HandleFunc("POST /api/admin/suggestions/{id}/handle", a.adminAPI(adminPermContent, a.apiAdminSuggestionHandle))
	mux.HandleFunc("GET /api/admin/roles", a.adminAPI(adminPermAdminManage, a.apiAdminRoles))
	mux.HandleFunc("POST /api/admin/roles/assign", a.adminAPI(adminPermAdminManage, a.apiAdminRoleAssign))
}

type adminAccess struct {
	User        db.User
	Roles       []string
	Permissions []string
	SuperAdmin  bool
}

func (a *App) loadAdminAccess(ctx context.Context, user db.User) (adminAccess, error) {
	roles, err := a.db.UserAdminRoles(ctx, user.ID)
	if err != nil {
		return adminAccess{}, err
	}
	perms, err := a.db.UserPermissions(ctx, user.ID)
	if err != nil {
		return adminAccess{}, err
	}
	return adminAccess{
		User:        user,
		Roles:       roles,
		Permissions: perms,
		SuperAdmin:  containsString(roles, "super_admin"),
	}, nil
}

// adminHTML 包装管理台页面路由。管理台只有一个入口：浏览器顶层 document 访问
// /admin、/admin/...（含 ?embed=1）一律 303 回 /?admin=1，由用户端 SPA 载入后
// 自动打开同源 iframe overlay；服务端不再提供独立顶层页面，也不返 404 死路。
// iframe 与无 header 请求（测试/CLI）仍照常鉴权并渲染 SSR。
func (a *App) adminHTML(permission string, next func(http.ResponseWriter, *http.Request, adminAccess)) http.HandlerFunc {
	return a.adminHTMLRoute(permission, false, next)
}

// adminHTMLMedia 包装管理台内的受权媒体串流（审核照片原图、活动海报）。管理台用
// target="_blank" 在新窗口打开时，浏览器发的正是 Sec-Fetch-Dest: document；媒体
// 不是页面入口，因此不做 303，直接串流。鉴权、RBAC 与页面路由完全一致，只是把
// 「顶层 303 回 SPA」的判定限定在页面路由上。
func (a *App) adminHTMLMedia(permission string, next func(http.ResponseWriter, *http.Request, adminAccess)) http.HandlerFunc {
	return a.adminHTMLRoute(permission, true, next)
}

func (a *App) adminHTMLRoute(permission string, allowDocument bool, next func(http.ResponseWriter, *http.Request, adminAccess)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowDocument {
			// 同一 URL 因 Sec-Fetch-Dest 返回不同内容，禁止中间缓存串味。
			w.Header().Add("Vary", "Sec-Fetch-Dest")
			if r.Header.Get("Sec-Fetch-Dest") == "document" {
				// 303 同样禁止缓存，避免顶层重定向被中间层缓存后串给 iframe。
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/?admin=1", http.StatusSeeOther)
				return
			}
		}
		user, ok := a.requireUserHTML(w, r)
		if !ok {
			return
		}
		access, err := a.loadAdminAccess(r.Context(), user)
		if err != nil {
			a.logger(r).Error("load admin access failed", "error", err, "user_id", user.ID)
			a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
			return
		}
		if len(access.Roles) == 0 {
			a.renderError(w, r, http.StatusForbidden, "没有管理权限", "你的账号没有管理台权限。")
			return
		}
		if permission != "" && !containsString(access.Permissions, permission) {
			a.renderError(w, r, http.StatusForbidden, "权限不足", "你没有执行此操作的权限。")
			return
		}
		next(w, r.WithContext(withAdminAccess(r.Context(), access)), access)
	}
}

func (a *App) adminAPI(permission string, next func(http.ResponseWriter, *http.Request, adminAccess)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.requireUserAPI(w, r)
		if !ok {
			return
		}
		access, err := a.loadAdminAccess(r.Context(), user)
		if err != nil {
			a.logger(r).Error("load admin access failed", "error", err, "user_id", user.ID)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
			return
		}
		if len(access.Roles) == 0 {
			writeAPIError(w, http.StatusForbidden, "forbidden", "你的账号没有管理台权限。")
			return
		}
		if permission != "" && !containsString(access.Permissions, permission) {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "你没有执行此操作的权限。")
			return
		}
		next(w, r, access)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

type adminHomeData struct {
	Stats       db.AdminStats
	Roles       []string
	Permissions []string
	SuperAdmin  bool
	CanReview   bool
	CanManage   bool
	CanModerate bool
	CanAdmin    bool
	CanActivity bool
	CanPoints   bool
	CanAudit    bool
}

func (a *App) handleAdminHome(w http.ResponseWriter, r *http.Request, access adminAccess) {
	stats, err := a.db.AdminStats(r.Context())
	if err != nil {
		a.logger(r).Error("load admin stats failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	data := adminHomeData{
		Stats:       stats,
		Roles:       access.Roles,
		Permissions: access.Permissions,
		SuperAdmin:  access.SuperAdmin,
		CanReview:   containsString(access.Permissions, adminPermUserReview),
		CanManage:   containsString(access.Permissions, adminPermUserManage),
		CanModerate: containsString(access.Permissions, adminPermContent),
		CanAdmin:    containsString(access.Permissions, adminPermAdminManage),
		CanActivity: containsString(access.Permissions, adminPermActivity),
		CanPoints:   containsString(access.Permissions, adminPermPointsAdjust),
		CanAudit:    containsString(access.Permissions, adminPermAudit),
	}
	a.render(w, r, http.StatusOK, "admin", PageData{
		Title:     "管理台",
		User:      &access.User,
		Data:      data,
		PageCSS:   "admin.css",
		ActiveNav: "admin",
	})
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

type adminUsersPageData struct {
	Users         []db.AdminUser
	Counts        db.AdminUserCounts
	StatusFilter  string
	Query         string
	CanReview     bool
	CanManage     bool
	SuperAdmin    bool
	TempPassword  string
	TempExpiresAt string
	Statuses      []adminStatusOption
}

type adminStatusOption struct {
	Value string
	Label string
}

func adminUserStatusOptions() []adminStatusOption {
	return []adminStatusOption{
		{Value: "pending", Label: "待审核"},
		{Value: "active", Label: "已通过"},
		{Value: "rejected", Label: "已拒绝"},
		{Value: "disabled", Label: "已停用"},
		{Value: "all", Label: "全部"},
	}
}

func normalizeAdminUserStatus(raw string) string {
	switch raw {
	case "pending", "active", "rejected", "disabled":
		return raw
	case "all", "":
		return ""
	}
	return "pending"
}

func (a *App) renderAdminUsersPage(w http.ResponseWriter, r *http.Request, access adminAccess,
	status, query, notice, errMessage, tempPassword string, tempExpires time.Time) {
	filter := db.AdminUserFilter{Status: status, Query: query, Limit: adminListLimit}
	users, err := a.db.ListAdminUsers(r.Context(), filter)
	if err != nil {
		a.logger(r).Error("list admin users failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	counts, err := a.db.CountAdminUsers(r.Context())
	if err != nil {
		a.logger(r).Error("count admin users failed", "error", err)
		counts = db.AdminUserCounts{}
	}
	statusValue := status
	if statusValue == "" {
		statusValue = "all"
	}
	data := adminUsersPageData{
		Users:        users,
		Counts:       counts,
		StatusFilter: statusValue,
		Query:        query,
		CanReview:    containsString(access.Permissions, adminPermUserReview),
		CanManage:    containsString(access.Permissions, adminPermUserManage),
		SuperAdmin:   access.SuperAdmin,
		TempPassword: tempPassword,
		Statuses:     adminUserStatusOptions(),
	}
	if !tempExpires.IsZero() {
		data.TempExpiresAt = tempExpires.Format("2006-01-02 15:04")
	}
	a.render(w, r, http.StatusOK, "admin_users", PageData{
		Title:     "用户管理",
		User:      &access.User,
		Data:      data,
		Notice:    notice,
		Error:     errMessage,
		PageCSS:   "admin.css",
		ActiveNav: "users",
	})
}

func (a *App) handleAdminUsersPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	status := normalizeAdminUserStatus(r.URL.Query().Get("status"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	notice := adminUserNotice(r.URL.Query().Get("notice"))
	a.renderAdminUsersPage(w, r, access, status, query, notice, "", "", time.Time{})
}

func adminUserNotice(code string) string {
	switch code {
	case "approved":
		return "已审核通过。"
	case "rejected":
		return "已拒绝该用户。"
	case "disabled":
		return "已停用该账号。"
	case "enabled":
		return "已恢复该账号。"
	case "temp":
		return "已生成临时密码，请立即转交用户。"
	default:
		return ""
	}
}

func (a *App) parseAdminPathID(w http.ResponseWriter, r *http.Request, api bool) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		if api {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "识别码格式不正确。")
		} else {
			a.renderError(w, r, http.StatusBadRequest, "操作失败", "识别码格式不正确。")
		}
		return 0, false
	}
	return id, true
}

func (a *App) adminFormPost(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "操作失败", "表单格式不正确，请重新整理后再试。")
		return false
	}
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return false
	}
	return true
}

func (a *App) adminAPIHeader(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_invalid", "CSRF token 无效，请重新整理后再试。")
		return false
	}
	return true
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return false
	}
	return true
}

func noteValue(r *http.Request) string {
	note := strings.TrimSpace(r.PostFormValue("note"))
	if len([]rune(note)) > adminMaxNoteRunes {
		note = string([]rune(note)[:adminMaxNoteRunes])
	}
	return note
}

func (a *App) handleAdminUserApprove(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.handleAdminUserAction(w, r, access, "approve")
}

func (a *App) handleAdminUserReject(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.handleAdminUserAction(w, r, access, "reject")
}

func (a *App) handleAdminUserDisable(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.handleAdminUserAction(w, r, access, "disable")
}

func (a *App) handleAdminUserEnable(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.handleAdminUserAction(w, r, access, "enable")
}

func (a *App) handleAdminUserAction(w http.ResponseWriter, r *http.Request, access adminAccess, action string) {
	userID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	if action == "reject" && r.PostFormValue("require_note") == "1" && noteValue(r) == "" {
		a.renderError(w, r, http.StatusBadRequest, "操作失败", "请填写拒绝原因。")
		return
	}
	if action == "disable" && userID == access.User.ID {
		a.renderError(w, r, http.StatusConflict, "不能停用自己", "请由另一位管理员执行停用。")
		return
	}
	var (
		previous string
		err      error
		audit    = "admin.user." + action
		notice   string
	)
	switch action {
	case "approve":
		previous, err = a.db.ApproveAdminUser(r.Context(), userID, access.User.ID, noteValue(r))
		notice = "approved"
	case "reject":
		previous, err = a.db.RejectAdminUser(r.Context(), userID, access.User.ID, noteValue(r))
		notice = "rejected"
	case "disable":
		previous, err = a.db.SetAdminUserEnabled(r.Context(), userID, access.User.ID, false)
		notice = "disabled"
	case "enable":
		previous, err = a.db.SetAdminUserEnabled(r.Context(), userID, access.User.ID, true)
		notice = "enabled"
	default:
		a.renderError(w, r, http.StatusBadRequest, "操作失败", "不支持的操作。")
		return
	}
	if err != nil {
		a.renderAdminUserError(w, r, access, err)
		return
	}
	meta := map[string]any{"previous_status": previous}
	if note := noteValue(r); note != "" && action != "disable" && action != "enable" {
		meta["note"] = note
	}
	a.audit(r, &access.User.ID, audit, "user", strconv.FormatInt(userID, 10), meta)
	a.adminNotifyUser(r, userID, "review", adminReviewTitle(action), adminReviewBody(action), "")
	adminUserActionRedirect(w, r, notice)
}

func adminReviewTitle(action string) string {
	switch action {
	case "approve":
		return "资料审核通过"
	case "reject":
		return "资料审核未通过"
	case "disable":
		return "账号已停用"
	case "enable":
		return "账号已恢复"
	default:
		return "账号状态更新"
	}
}

func adminReviewBody(action string) string {
	switch action {
	case "approve":
		return "你的资料已通过审核，可以开始使用一线牵。"
	case "reject":
		return "很抱歉，你的资料未通过审核，如有疑问请联系管理员。"
	case "disable":
		return "你的账号已被停用，请联系管理员。"
	case "enable":
		return "你的账号已恢复使用。"
	default:
		return ""
	}
}

func (a *App) renderAdminUserError(w http.ResponseWriter, r *http.Request, access adminAccess, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		a.renderError(w, r, http.StatusNotFound, "找不到用户", "此用户不存在。")
	case errors.Is(err, db.ErrLastSuperAdmin):
		a.renderError(w, r, http.StatusConflict, "无法停用最后一位最高管理员", "请先指派另一位最高管理员。")
	case errors.Is(err, db.ErrAdminPrivileged):
		a.renderError(w, r, http.StatusForbidden, "权限不足", "只有最高管理员可以操作管理员账号。")
	case errors.Is(err, db.ErrAdminUserNotReviewable):
		a.renderError(w, r, http.StatusConflict, "状态不允许", "该用户不处于待审核状态；停用或恢复请使用账号管理。")
	case errors.Is(err, db.ErrAdminUserPending):
		a.renderError(w, r, http.StatusConflict, "用户尚未审核", "待审核用户请先使用「通过」操作。")
	case errors.Is(err, db.ErrAdminUserRejected):
		a.renderError(w, r, http.StatusConflict, "用户已被拒绝", "被拒绝的用户请先使用「通过」操作。")
	default:
		a.logger(r).Error("admin user action failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
	}
}

func (a *App) handleAdminUserTempPassword(w http.ResponseWriter, r *http.Request, access adminAccess) {
	userID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	raw, expiresAt, err := a.issueTempPassword(w, r, access, userID)
	if err != nil {
		return
	}
	status := normalizeAdminUserStatus(r.PostFormValue("status_filter"))
	query := strings.TrimSpace(r.PostFormValue("q"))
	a.renderAdminUsersPage(w, r, access, status, query,
		"已生成临时密码，只会显示这一次，请立即转交用户。", "", raw, expiresAt)
}

// adminTempPasswordTarget loads the reset target and rejects protected admin
// accounts unless the actor is a super admin. AdminSetTempPassword repeats the
// check inside its transaction so a role change cannot race past it.
func (a *App) adminTempPasswordTarget(ctx context.Context, access adminAccess, userID int64) ([]string, error) {
	target, err := a.db.GetAdminUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if target.AdminProtected() && !access.SuperAdmin {
		return target.Roles, db.ErrAdminPrivileged
	}
	return target.Roles, nil
}

func (a *App) issueTempPassword(w http.ResponseWriter, r *http.Request, access adminAccess, userID int64) (string, time.Time, error) {
	targetRoles, err := a.adminTempPasswordTarget(r.Context(), access, userID)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			a.renderError(w, r, http.StatusNotFound, "找不到用户", "此用户不存在。")
		case errors.Is(err, db.ErrAdminPrivileged):
			a.renderError(w, r, http.StatusForbidden, "权限不足", "只有最高管理员可以重置管理员账号的密码。")
		default:
			a.logger(r).Error("load temp password target failed", "error", err, "user_id", userID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return "", time.Time{}, err
	}
	raw, err := auth.GenerateTempPassword()
	if err != nil {
		a.logger(r).Error("generate temp password failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "无法生成临时密码，请稍后再试。")
		return "", time.Time{}, err
	}
	if !a.acquireHash(w, r) {
		return "", time.Time{}, errors.New("busy")
	}
	hash, err := auth.HashPassword(raw)
	a.releaseHash()
	if err != nil {
		a.logger(r).Error("hash temp password failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "无法生成临时密码，请稍后再试。")
		return "", time.Time{}, err
	}
	expiresAt := a.clock.Now().Add(adminTempPasswordTTL)
	if err := a.db.AdminSetTempPassword(r.Context(), userID, hash, expiresAt, access.User.ID); err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			a.renderError(w, r, http.StatusNotFound, "找不到用户", "此用户不存在。")
		case errors.Is(err, db.ErrAdminPrivileged):
			a.renderError(w, r, http.StatusForbidden, "权限不足", "只有最高管理员可以重置管理员账号的密码。")
		default:
			a.logger(r).Error("set temp password failed", "error", err, "user_id", userID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return "", time.Time{}, err
	}
	a.audit(r, &access.User.ID, "admin.user.temp_password", "user",
		strconv.FormatInt(userID, 10), map[string]any{
			"must_change":  true,
			"expires_at":   expiresAt.UTC().Format(time.RFC3339),
			"target_roles": targetRoles,
			"actor_roles":  access.Roles,
			"actor_super":  access.SuperAdmin,
		})
	a.adminNotifyUser(r, userID, "security", "登录密码已重置",
		"管理员已为你设定临时密码，登录后必须立即修改。", "/password")
	return raw, expiresAt, nil
}

func (a *App) adminNotifyUser(r *http.Request, userID int64, kind, title, body, link string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if _, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
		UserID: userID,
		Kind:   kind,
		Title:  title,
		Body:   body,
		Link:   link,
	}); err != nil {
		a.logger(r).Error("admin notification failed", "error", err, "user_id", userID)
	}
}

// ---------------------------------------------------------------------------
// Reports and suggestions
// ---------------------------------------------------------------------------

type adminReportsPageData struct {
	Mode         string
	Reports      []db.Report
	Suggestions  []db.AdminSuggestion
	StatusFilter string
	Statuses     []adminStatusOption
}

func reportStatusOptions() []adminStatusOption {
	return []adminStatusOption{
		{Value: "pending", Label: "待处理"},
		{Value: "approved", Label: "已同意"},
		{Value: "rejected", Label: "已驳回"},
		{Value: "all", Label: "全部"},
	}
}

func normalizeReportStatus(raw string) string {
	switch raw {
	case "pending":
		return db.ReportStatusPending
	case "approved":
		return db.ReportStatusApproved
	case "rejected":
		return db.ReportStatusRejected
	case "all", "":
		return ""
	}
	return db.ReportStatusPending
}

func normalizeSuggestionStatus(raw string) string {
	switch raw {
	case "pending":
		return "待处理"
	case "handled":
		return db.AdminSuggestionHandled
	case "ignored":
		return db.AdminSuggestionIgnored
	case "all", "":
		return ""
	}
	return "待处理"
}

func (a *App) handleAdminReportsPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	statusKey := r.URL.Query().Get("status")
	if statusKey == "" {
		statusKey = "pending"
	}
	status := normalizeReportStatus(statusKey)
	reports, err := a.db.ListReports(r.Context(), status, adminListLimit)
	if err != nil {
		a.logger(r).Error("list admin reports failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	if statusKey != "pending" && statusKey != "approved" && statusKey != "rejected" && statusKey != "all" {
		statusKey = "pending"
	}
	a.render(w, r, http.StatusOK, "admin_reports", PageData{
		Title: "举报处理",
		User:  &access.User,
		Data: adminReportsPageData{
			Mode:         "reports",
			Reports:      reports,
			StatusFilter: statusKey,
			Statuses:     reportStatusOptions(),
		},
		Notice:    adminReportNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin.css",
		ActiveNav: "reports",
	})
}

func adminReportNotice(code string) string {
	switch code {
	case "handled":
		return "举报已处理。"
	case "dismissed":
		return "举报已驳回。"
	default:
		return ""
	}
}

func (a *App) handleAdminReportHandle(w http.ResponseWriter, r *http.Request, access adminAccess) {
	reportID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	action := r.PostFormValue("action")
	status, err := a.db.HandleAdminReport(r.Context(), reportID, access.User.ID, action)
	if err != nil {
		a.renderAdminReportError(w, r, err)
		return
	}
	meta := map[string]any{"action": action, "status": status}
	if note := noteValue(r); note != "" {
		meta["note"] = note
	}
	a.audit(r, &access.User.ID, "admin.report.handle", "report",
		strconv.FormatInt(reportID, 10), meta)
	notice := "handled"
	if status == db.ReportStatusRejected {
		notice = "dismissed"
	}
	http.Redirect(w, r, "/admin/reports?status=pending&notice="+notice, http.StatusSeeOther)
}

func (a *App) renderAdminReportError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		a.renderError(w, r, http.StatusNotFound, "找不到举报", "此举报不存在。")
	case errors.Is(err, db.ErrConflict):
		a.renderError(w, r, http.StatusConflict, "举报已处理", "此举报已经有处理结果。")
	case errors.Is(err, db.ErrAdminReportAction):
		a.renderError(w, r, http.StatusBadRequest, "操作不正确", "此举报不支持该处理方式。")
	default:
		a.logger(r).Error("handle report failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
	}
}

func (a *App) handleAdminSuggestionsPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	statusKey := r.URL.Query().Get("status")
	if statusKey == "" {
		statusKey = "pending"
	}
	status := normalizeSuggestionStatus(statusKey)
	suggestions, err := a.db.ListAdminSuggestions(r.Context(), status, adminListLimit)
	if err != nil {
		a.logger(r).Error("list admin suggestions failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	switch statusKey {
	case "pending", "handled", "ignored", "all":
	default:
		statusKey = "pending"
	}
	a.render(w, r, http.StatusOK, "admin_reports", PageData{
		Title: "意见反馈",
		User:  &access.User,
		Data: adminReportsPageData{
			Mode:         "suggestions",
			Suggestions:  suggestions,
			StatusFilter: statusKey,
			Statuses: []adminStatusOption{
				{Value: "pending", Label: "待处理"},
				{Value: "handled", Label: "已处理"},
				{Value: "ignored", Label: "已忽略"},
				{Value: "all", Label: "全部"},
			},
		},
		Notice:    adminSuggestionNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin.css",
		ActiveNav: "suggestions",
	})
}

func adminSuggestionNotice(code string) string {
	switch code {
	case "handled":
		return "意见反馈已标记为已处理。"
	case "ignored":
		return "意见反馈已忽略。"
	default:
		return ""
	}
}

func (a *App) handleAdminSuggestionHandle(w http.ResponseWriter, r *http.Request, access adminAccess) {
	suggestionID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	action := r.PostFormValue("action")
	if action != "handle" && action != "ignore" {
		a.renderError(w, r, http.StatusBadRequest, "操作不正确", "请选择处理或忽略。")
		return
	}
	status := db.AdminSuggestionHandled
	notice := "handled"
	if action == "ignore" {
		status = db.AdminSuggestionIgnored
		notice = "ignored"
	}
	if err := a.db.HandleAdminSuggestion(r.Context(), suggestionID, status); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.renderError(w, r, http.StatusNotFound, "找不到反馈", "此意见反馈不存在。")
		} else if errors.Is(err, db.ErrAdminSuggestionStatus) {
			a.renderError(w, r, http.StatusBadRequest, "操作不正确", "不支持的处理状态。")
		} else {
			a.logger(r).Error("handle suggestion failed", "error", err)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return
	}
	a.audit(r, &access.User.ID, "admin.suggestion.handle", "suggestion",
		strconv.FormatInt(suggestionID, 10), map[string]any{"status": status})
	http.Redirect(w, r, "/admin/suggestions?status=pending&notice="+notice, http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

type adminRolesPageData struct {
	Roles       []db.AdminRole
	Assignments []db.AdminAssignment
	SuperAdmin  bool
}

func (a *App) handleAdminRolesPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	roles, err := a.db.ListAdminRoles(r.Context())
	if err != nil {
		a.logger(r).Error("list admin roles failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	assignments, err := a.db.ListAdminAssignments(r.Context())
	if err != nil {
		a.logger(r).Error("list admin assignments failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "admin_roles", PageData{
		Title: "角色权限",
		User:  &access.User,
		Data: adminRolesPageData{
			Roles:       roles,
			Assignments: assignments,
			SuperAdmin:  access.SuperAdmin,
		},
		Notice:    adminRoleNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin.css",
		ActiveNav: "roles",
	})
}

func adminRoleNotice(code string) string {
	switch code {
	case "assigned":
		return "角色已指派。"
	case "removed":
		return "角色已移除。"
	case "unchanged":
		return "角色没有变更。"
	default:
		return ""
	}
}

func (a *App) handleAdminRoleAssign(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !access.SuperAdmin {
		a.renderError(w, r, http.StatusForbidden, "权限不足", "只有最高管理员可以指派角色。")
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	userRef := strings.TrimSpace(r.PostFormValue("user_ref"))
	roleCode := strings.TrimSpace(r.PostFormValue("role"))
	action := r.PostFormValue("action")
	if action != "assign" && action != "remove" {
		a.renderError(w, r, http.StatusBadRequest, "操作不正确", "请选择指派或移除。")
		return
	}
	user, err := a.db.FindAdminUserByRef(r.Context(), userRef)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.renderError(w, r, http.StatusNotFound, "找不到用户", "请确认会员编号、手机或 ID。")
		} else {
			a.logger(r).Error("resolve role user failed", "error", err)
			a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		}
		return
	}
	assign := action == "assign"
	changed, err := a.db.AssignAdminRole(r.Context(), user.ID, roleCode, assign)
	if err != nil {
		a.renderAdminRoleError(w, r, err)
		return
	}
	auditAction := "admin.role.assign"
	notice := "assigned"
	if !assign {
		auditAction = "admin.role.remove"
		notice = "removed"
	}
	if !changed {
		notice = "unchanged"
	}
	a.audit(r, &access.User.ID, auditAction, "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"role": roleCode, "changed": changed})
	http.Redirect(w, r, "/admin/roles?notice="+notice, http.StatusSeeOther)
}

func (a *App) renderAdminRoleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		a.renderError(w, r, http.StatusNotFound, "找不到角色或用户", "请确认角色代码与用户资料。")
	case errors.Is(err, db.ErrLastSuperAdmin):
		a.renderError(w, r, http.StatusConflict, "无法移除最后一位最高管理员",
			"系统必须保留至少一位最高管理员。请先指派另一位。")
	default:
		a.logger(r).Error("assign admin role failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
	}
}

// ---------------------------------------------------------------------------
// JSON API
// ---------------------------------------------------------------------------

type adminMeJSON struct {
	UserID      int64    `json:"user_id"`
	LegacyID    string   `json:"legacy_id"`
	Name        string   `json:"name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	SuperAdmin  bool     `json:"super_admin"`
}

func (a *App) apiAdminMe(w http.ResponseWriter, r *http.Request, access adminAccess) {
	writeJSON(w, http.StatusOK, adminMeJSON{
		UserID:      access.User.ID,
		LegacyID:    legacyID(&access.User),
		Name:        access.User.Name,
		Roles:       access.Roles,
		Permissions: access.Permissions,
		SuperAdmin:  access.SuperAdmin,
	})
}

type adminUserJSON struct {
	ID            int64    `json:"id"`
	LegacyID      string   `json:"legacy_id"`
	Name          string   `json:"name"`
	Nickname      string   `json:"nickname"`
	Phone         string   `json:"phone"`
	AccountKind   string   `json:"account_kind"`
	AccountStatus string   `json:"account_status"`
	LoginStatus   string   `json:"login_status"`
	Roles         []string `json:"roles"`
	ReviewedAt    string   `json:"reviewed_at,omitempty"`
	LastActiveAt  string   `json:"last_active_at,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

func adminUserToJSON(u db.AdminUser) adminUserJSON {
	out := adminUserJSON{
		ID:            u.ID,
		LegacyID:      u.LegacyUserID,
		Name:          u.Name,
		Nickname:      u.Nickname,
		Phone:         maskAdminPhone(u.Phone),
		AccountKind:   u.AccountKind,
		AccountStatus: u.AccountStatus,
		LoginStatus:   u.LoginStatus,
		Roles:         u.Roles,
		CreatedAt:     u.CreatedAt.Format(time.RFC3339),
	}
	if u.ReviewedAt != nil {
		out.ReviewedAt = u.ReviewedAt.Format(time.RFC3339)
	}
	if u.LastActiveAt != nil {
		out.LastActiveAt = u.LastActiveAt.Format(time.RFC3339)
	}
	return out
}

// maskAdminPhone limits exposure in JSON while the HTML console shows the full
// number for identity checks by authorised staff.
func maskAdminPhone(phone string) string {
	return auth.MaskPhone(phone)
}

func (a *App) apiAdminUsers(w http.ResponseWriter, r *http.Request, access adminAccess) {
	status := normalizeAdminUserStatus(r.URL.Query().Get("status"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	users, err := a.db.ListAdminUsers(r.Context(), db.AdminUserFilter{
		Status: status, Query: query, Limit: adminListLimit,
	})
	if err != nil {
		a.logger(r).Error("list admin users failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]adminUserJSON, 0, len(users))
	for _, u := range users {
		out = append(out, adminUserToJSON(u))
	}
	counts, err := a.db.CountAdminUsers(r.Context())
	if err != nil {
		a.logger(r).Error("count admin users failed", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":  out,
		"counts": counts,
	})
}

func (a *App) apiAdminUserApprove(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.apiAdminUserAction(w, r, access, "approve")
}

func (a *App) apiAdminUserReject(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.apiAdminUserAction(w, r, access, "reject")
}

func (a *App) apiAdminUserDisable(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.apiAdminUserAction(w, r, access, "disable")
}

func (a *App) apiAdminUserEnable(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.apiAdminUserAction(w, r, access, "enable")
}

type adminNoteJSON struct {
	Note string `json:"note"`
}

func (a *App) apiAdminUserAction(w http.ResponseWriter, r *http.Request, access adminAccess, action string) {
	userID, ok := a.parseAdminPathID(w, r, true)
	if !ok {
		return
	}
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body adminNoteJSON
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	if action == "disable" && userID == access.User.ID {
		writeAPIError(w, http.StatusConflict, "self_disable", "不能停用自己。")
		return
	}
	note := strings.TrimSpace(body.Note)
	if len([]rune(note)) > adminMaxNoteRunes {
		note = string([]rune(note)[:adminMaxNoteRunes])
	}
	var (
		previous string
		err      error
	)
	switch action {
	case "approve":
		previous, err = a.db.ApproveAdminUser(r.Context(), userID, access.User.ID, note)
	case "reject":
		previous, err = a.db.RejectAdminUser(r.Context(), userID, access.User.ID, note)
	case "disable":
		previous, err = a.db.SetAdminUserEnabled(r.Context(), userID, access.User.ID, false)
	case "enable":
		previous, err = a.db.SetAdminUserEnabled(r.Context(), userID, access.User.ID, true)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_action", "不支持的操作。")
		return
	}
	if err != nil {
		a.writeAdminUserAPIError(w, r, err)
		return
	}
	meta := map[string]any{"previous_status": previous}
	if note != "" && action != "disable" && action != "enable" {
		meta["note"] = note
	}
	a.audit(r, &access.User.ID, "admin.user."+action, "user",
		strconv.FormatInt(userID, 10), meta)
	a.adminNotifyUser(r, userID, "review", adminReviewTitle(action), adminReviewBody(action), "")
	updated, err := a.db.GetAdminUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user_id": userID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": adminUserToJSON(updated)})
}

func (a *App) writeAdminUserAPIError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
	case errors.Is(err, db.ErrLastSuperAdmin):
		writeAPIError(w, http.StatusConflict, "last_super_admin", "不能停用最后一位最高管理员。")
	case errors.Is(err, db.ErrAdminPrivileged):
		writeAPIError(w, http.StatusForbidden, "target_admin_forbidden", "只有最高管理员可以操作管理员账号。")
	case errors.Is(err, db.ErrAdminUserNotReviewable):
		writeAPIError(w, http.StatusConflict, "user_not_reviewable", "该用户不处于待审核状态；停用或恢复请使用账号管理。")
	case errors.Is(err, db.ErrAdminUserPending):
		writeAPIError(w, http.StatusConflict, "user_pending", "待审核用户请先使用通过操作。")
	case errors.Is(err, db.ErrAdminUserRejected):
		writeAPIError(w, http.StatusConflict, "user_rejected", "被拒绝的用户请先使用通过操作。")
	default:
		a.logger(r).Error("admin user action failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
	}
}

func (a *App) apiAdminUserTempPassword(w http.ResponseWriter, r *http.Request, access adminAccess) {
	userID, ok := a.parseAdminPathID(w, r, true)
	if !ok {
		return
	}
	if !a.adminAPIHeader(w, r) {
		return
	}
	raw, expiresAt, err := a.issueTempPasswordAPI(w, r, access, userID)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"user_id":       userID,
		"temp_password": raw,
		"expires_at":    expiresAt.UTC().Format(time.RFC3339),
		"must_change":   true,
	})
}

func (a *App) issueTempPasswordAPI(w http.ResponseWriter, r *http.Request, access adminAccess, userID int64) (string, time.Time, error) {
	targetRoles, err := a.adminTempPasswordTarget(r.Context(), access, userID)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		case errors.Is(err, db.ErrAdminPrivileged):
			writeAPIError(w, http.StatusForbidden, "target_admin_forbidden", "只有最高管理员可以重置管理员账号的密码。")
		default:
			a.logger(r).Error("load temp password target failed", "error", err, "user_id", userID)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return "", time.Time{}, err
	}
	raw, err := auth.GenerateTempPassword()
	if err != nil {
		a.logger(r).Error("generate temp password failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "无法生成临时密码。")
		return "", time.Time{}, err
	}
	if !a.acquireGateAPI(w, r, a.hashSem, 2*time.Second) {
		return "", time.Time{}, errors.New("busy")
	}
	hash, err := auth.HashPassword(raw)
	a.releaseHash()
	if err != nil {
		a.logger(r).Error("hash temp password failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "无法生成临时密码。")
		return "", time.Time{}, err
	}
	expiresAt := a.clock.Now().Add(adminTempPasswordTTL)
	if err := a.db.AdminSetTempPassword(r.Context(), userID, hash, expiresAt, access.User.ID); err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到用户。")
		case errors.Is(err, db.ErrAdminPrivileged):
			writeAPIError(w, http.StatusForbidden, "target_admin_forbidden", "只有最高管理员可以重置管理员账号的密码。")
		default:
			a.logger(r).Error("set temp password failed", "error", err, "user_id", userID)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return "", time.Time{}, err
	}
	a.audit(r, &access.User.ID, "admin.user.temp_password", "user",
		strconv.FormatInt(userID, 10), map[string]any{
			"must_change":  true,
			"expires_at":   expiresAt.UTC().Format(time.RFC3339),
			"target_roles": targetRoles,
			"actor_roles":  access.Roles,
			"actor_super":  access.SuperAdmin,
		})
	a.adminNotifyUser(r, userID, "security", "登录密码已重置",
		"管理员已为你设定临时密码，登录后必须立即修改。", "/password")
	return raw, expiresAt, nil
}

type adminReportJSON struct {
	ID              int64  `json:"id"`
	ReporterName    string `json:"reporter_name"`
	TargetName      string `json:"target_name"`
	TargetMessageID *int64 `json:"target_message_id,omitempty"`
	Reason          string `json:"reason"`
	Status          string `json:"status"`
	MessageContent  string `json:"message_content,omitempty"`
	CreatedAt       string `json:"created_at"`
	HandledAt       string `json:"handled_at,omitempty"`
}

func (a *App) apiAdminReports(w http.ResponseWriter, r *http.Request, access adminAccess) {
	status := normalizeReportStatus(r.URL.Query().Get("status"))
	reports, err := a.db.ListReports(r.Context(), status, adminListLimit)
	if err != nil {
		a.logger(r).Error("list admin reports failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]adminReportJSON, 0, len(reports))
	for _, rp := range reports {
		item := adminReportJSON{
			ID:              rp.ID,
			ReporterName:    rp.ReporterName,
			TargetName:      rp.TargetName,
			TargetMessageID: rp.TargetMessageID,
			Reason:          rp.Reason,
			Status:          rp.Status,
			MessageContent:  rp.MessageContent,
			CreatedAt:       rp.CreatedAt.Format(time.RFC3339),
		}
		if rp.HandledAt != nil {
			item.HandledAt = rp.HandledAt.Format(time.RFC3339)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": out})
}

type adminReportHandleJSON struct {
	Action string `json:"action"`
	Note   string `json:"note"`
}

func (a *App) apiAdminReportHandle(w http.ResponseWriter, r *http.Request, access adminAccess) {
	reportID, ok := a.parseAdminPathID(w, r, true)
	if !ok {
		return
	}
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body adminReportHandleJSON
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	status, err := a.db.HandleAdminReport(r.Context(), reportID, access.User.ID, body.Action)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到举报。")
		case errors.Is(err, db.ErrConflict):
			writeAPIError(w, http.StatusConflict, "already_handled", "此举报已经有处理结果。")
		case errors.Is(err, db.ErrAdminReportAction):
			writeAPIError(w, http.StatusBadRequest, "invalid_action", "不支持的处理方式。")
		default:
			a.logger(r).Error("handle report failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return
	}
	meta := map[string]any{"action": body.Action, "status": status}
	if note := strings.TrimSpace(body.Note); note != "" {
		meta["note"] = note
	}
	a.audit(r, &access.User.ID, "admin.report.handle", "report",
		strconv.FormatInt(reportID, 10), meta)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (a *App) apiAdminSuggestions(w http.ResponseWriter, r *http.Request, access adminAccess) {
	status := normalizeSuggestionStatus(r.URL.Query().Get("status"))
	items, err := a.db.ListAdminSuggestions(r.Context(), status, adminListLimit)
	if err != nil {
		a.logger(r).Error("list admin suggestions failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, s := range items {
		out = append(out, map[string]any{
			"id":          s.ID,
			"author_name": s.AuthorName,
			"type":        s.Type,
			"content":     s.Content,
			"status":      s.Status,
			"created_at":  s.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

type adminSuggestionHandleJSON struct {
	Action string `json:"action"`
}

func (a *App) apiAdminSuggestionHandle(w http.ResponseWriter, r *http.Request, access adminAccess) {
	suggestionID, ok := a.parseAdminPathID(w, r, true)
	if !ok {
		return
	}
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body adminSuggestionHandleJSON
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	if body.Action != "handle" && body.Action != "ignore" {
		writeAPIError(w, http.StatusBadRequest, "invalid_action", "请选择处理或忽略。")
		return
	}
	status := db.AdminSuggestionHandled
	if body.Action == "ignore" {
		status = db.AdminSuggestionIgnored
	}
	if err := a.db.HandleAdminSuggestion(r.Context(), suggestionID, status); err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到意见反馈。")
		case errors.Is(err, db.ErrAdminSuggestionStatus):
			writeAPIError(w, http.StatusBadRequest, "invalid_status", "不支持的处理状态。")
		default:
			a.logger(r).Error("handle suggestion failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return
	}
	a.audit(r, &access.User.ID, "admin.suggestion.handle", "suggestion",
		strconv.FormatInt(suggestionID, 10), map[string]any{"status": status})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

type adminRoleJSON struct {
	ID          int64    `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	UserCount   int64    `json:"user_count"`
}

type adminAssignmentJSON struct {
	UserID   int64    `json:"user_id"`
	LegacyID string   `json:"legacy_id"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
}

func (a *App) apiAdminRoles(w http.ResponseWriter, r *http.Request, access adminAccess) {
	roles, err := a.db.ListAdminRoles(r.Context())
	if err != nil {
		a.logger(r).Error("list admin roles failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	assignments, err := a.db.ListAdminAssignments(r.Context())
	if err != nil {
		a.logger(r).Error("list admin assignments failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	roleOut := make([]adminRoleJSON, 0, len(roles))
	for _, role := range roles {
		roleOut = append(roleOut, adminRoleJSON{
			ID: role.ID, Code: role.Code, Name: role.Name,
			Permissions: role.Permissions, UserCount: role.UserCount,
		})
	}
	assignmentOut := make([]adminAssignmentJSON, 0, len(assignments))
	for _, as := range assignments {
		assignmentOut = append(assignmentOut, adminAssignmentJSON{
			UserID: as.UserID, LegacyID: as.LegacyUserID, Name: as.Name, Roles: as.Roles,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":       roleOut,
		"assignments": assignmentOut,
	})
}

type adminRoleAssignJSON struct {
	UserRef string `json:"user_ref"`
	Role    string `json:"role"`
	Action  string `json:"action"`
}

func (a *App) apiAdminRoleAssign(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !access.SuperAdmin {
		writeAPIError(w, http.StatusForbidden, "super_admin_required", "只有最高管理员可以指派角色。")
		return
	}
	if !a.adminAPIHeader(w, r) {
		return
	}
	var body adminRoleAssignJSON
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	user, err := a.db.FindAdminUserByRef(r.Context(), strings.TrimSpace(body.UserRef))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "user_not_found", "找不到用户。")
		} else {
			a.logger(r).Error("resolve role user failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return
	}
	if body.Action != "assign" && body.Action != "remove" {
		writeAPIError(w, http.StatusBadRequest, "invalid_action", "请选择指派或移除。")
		return
	}
	assign := body.Action == "assign"
	changed, err := a.db.AssignAdminRole(r.Context(), user.ID, strings.TrimSpace(body.Role), assign)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "找不到角色或用户。")
		case errors.Is(err, db.ErrLastSuperAdmin):
			writeAPIError(w, http.StatusConflict, "last_super_admin", "不能移除最后一位最高管理员。")
		default:
			a.logger(r).Error("assign admin role failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		}
		return
	}
	auditAction := "admin.role.assign"
	if !assign {
		auditAction = "admin.role.remove"
	}
	a.audit(r, &access.User.ID, auditAction, "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"role": strings.TrimSpace(body.Role), "changed": changed})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"changed": changed,
		"user_id": user.ID,
		"role":    strings.TrimSpace(body.Role),
	})
}

// ---------------------------------------------------------------------------
// Forced password change
// ---------------------------------------------------------------------------

type passwordPageData struct {
	Required bool
}

func (a *App) handlePasswordPage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserHTML(w, r)
	if !ok {
		return
	}
	mustChange, _, err := a.db.GetCredentialState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusConflict, "尚未设定密码", "请使用启用链接设定密码。")
		return
	}
	if err != nil {
		a.logger(r).Error("load credential state failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "password", PageData{
		Title: "修改密码",
		User:  &user,
		Data:  passwordPageData{Required: mustChange || r.URL.Query().Get("required") == "1"},
	})
}

func (a *App) handlePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserHTML(w, r)
	if !ok {
		return
	}
	mustChange, _, err := a.db.GetCredentialState(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusConflict, "尚未设定密码", "请使用启用链接设定密码。")
		return
	}
	if err != nil {
		a.logger(r).Error("load credential state failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	renderPassword := func(status int, message string) {
		a.render(w, r, status, "password", PageData{
			Title: "修改密码",
			User:  &user,
			Error: message,
			Data:  passwordPageData{Required: mustChange},
		})
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		renderPassword(http.StatusBadRequest, "表单格式不正确，请重新整理后再试。")
		return
	}
	if !a.formCSRFValid(r) {
		renderPassword(http.StatusForbidden, "表单已过期，请重新整理后再试。")
		return
	}
	current := r.PostFormValue("current_password")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")
	if failure := a.applyPasswordChange(w, r, user, mustChange, current, password, confirm); failure != nil {
		switch failure.Status {
		case 0:
			// 请求已取消，无需写响应。
		case http.StatusTooManyRequests:
			a.renderError(w, r, failure.Status, "系统繁忙", failure.Message)
		default:
			renderPassword(failure.Status, failure.Message)
		}
		return
	}
	roles, _ := a.db.UserAdminRoles(r.Context(), user.ID)
	dest := "/"
	if len(roles) > 0 {
		dest = "/admin"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
