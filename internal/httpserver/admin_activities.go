package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

const adminPermActivity = "activity.manage"

const (
	adminBroadcastTitleMax = 100
	adminBroadcastBodyMax  = 2000
)

type adminActivityRow struct {
	db.Activity
	SignupCount    int
	CheckedInCount int
}

type adminActivitiesPageData struct {
	Activities []adminActivityRow
}

type adminActivityEditPageData struct {
	Activity *db.Activity
	IsNew    bool
	Error    string
	Statuses []string
	// StatusUnknown is set when the stored status is not in the option list so
	// the form can offer an explicit "keep current" choice instead of silently
	// submitting the first option.
	StatusUnknown string
}

type adminActivityBroadcastForm struct {
	Key              string
	Title            string
	Body             string
	IncludeSignups   bool
	IncludeObservers bool
	Preview          bool
}

type adminActivityDetailPageData struct {
	Activity                  *db.Activity
	Roster                    []db.AdminActivityRosterEntry
	Stats                     db.AdminActivitySigninStats
	Statuses                  []string
	StatusUnknown             string
	RosterAllCount            int
	RosterFilteredCount       int
	RosterQuery               string
	RosterAttendance          string
	RosterHasFilter           bool
	RosterPage                int
	RosterPageCount           int
	RosterPrevURL             string
	RosterNextURL             string
	RosterClearURL            string
	RosterNotice              string
	SignupAudienceCount       int
	ObserverAudienceCount     int
	AudienceTotal             int
	BroadcastKey              string
	BroadcastTitle            string
	BroadcastBody             string
	BroadcastIncludeSignups   bool
	BroadcastIncludeObservers bool
	BroadcastPreview          bool
	BroadcastPreviewCount     int
	BroadcastNotice           string
	BroadcastError            string
}

const (
	adminActivityStatusDraft = "草稿"
	adminRosterPageSize      = 10
)

// adminActivityStatuses is the complete editable status set. The DB constants
// are the source of truth; 草稿 is kept for activities created as drafts by the
// previous admin.
var adminActivityStatuses = []string{
	db.ActivityStatusOpen,
	db.ActivityStatusNotStarted,
	db.ActivityStatusFull,
	db.ActivityStatusFinished,
	db.ActivityStatusCancelled,
	adminActivityStatusDraft,
}

func adminActivityStatusKnown(status string) bool {
	return containsString(adminActivityStatuses, status)
}

func (a *App) handleAdminActivitiesPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activities, err := a.db.ListActivities(r.Context())
	if err != nil {
		a.logger(r).Error("list admin activities failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	stats, err := a.db.AdminActivitySigninStats(r.Context())
	if err != nil {
		a.logger(r).Error("load admin activity stats failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	rows := make([]adminActivityRow, 0, len(activities))
	for _, activity := range activities {
		row := adminActivityRow{Activity: activity, SignupCount: activity.CurrentSignup}
		if stat, ok := stats[activity.ID]; ok {
			row.SignupCount = stat.SignupCount
			row.CheckedInCount = stat.CheckedInCount
		}
		rows = append(rows, row)
	}
	a.render(w, r, http.StatusOK, "admin_activities", PageData{
		Title: "活动管理", User: &access.User,
		Data:         adminActivitiesPageData{Activities: rows},
		PageCSS:      "admin.css",
		ActiveNav:    "activities",
		ActiveSubnav: "list",
		Notice:       r.URL.Query().Get("notice"),
	})
}

func (a *App) handleAdminActivityNew(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.renderAdminActivityEdit(w, r, access, nil, "", "报名中")
}

func (a *App) handleAdminActivityEdit(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	a.renderAdminActivityEdit(w, r, access, &activity, "", "")
}

func (a *App) renderAdminActivityEdit(w http.ResponseWriter, r *http.Request, access adminAccess,
	activity *db.Activity, errMessage, newStatus string) {
	data := adminActivityEditPageData{Activity: activity, IsNew: activity == nil, Error: errMessage, Statuses: adminActivityStatuses}
	if activity == nil {
		data.Activity = &db.Activity{Status: newStatus}
	} else if activity.Status != "" && !adminActivityStatusKnown(activity.Status) {
		data.StatusUnknown = activity.Status
	}
	subnav := ""
	if data.IsNew {
		subnav = "new"
	}
	a.render(w, r, http.StatusOK, "admin_activity_edit", PageData{
		Title: "活动编辑", User: &access.User, Data: data,
		PageCSS: "admin.css", ActiveNav: "activities", ActiveSubnav: subnav,
	})
}

func (a *App) handleAdminActivityCreate(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.adminFormPost(w, r) {
		return
	}
	in, err := a.parseAdminActivityForm(r, nil)
	if err != nil {
		a.renderAdminActivityEdit(w, r, access, nil, err.Error(), "报名中")
		return
	}
	activity, err := a.db.CreateActivity(r.Context(), in)
	if err != nil {
		a.logger(r).Error("create admin activity failed", "error", err)
		a.renderAdminActivityEdit(w, r, access, nil, "创建失败，请稍后再试。", "报名中")
		return
	}
	a.audit(r, &access.User.ID, "admin.activity.create", "activity", activity.ActivityNo, nil)
	http.Redirect(w, r, adminActivityURL(activity.ID, "活动已创建。"), http.StatusSeeOther)
}

func (a *App) handleAdminActivityUpdate(w http.ResponseWriter, r *http.Request, access adminAccess) {
	existing, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	in, err := a.parseAdminActivityForm(r, &existing)
	if err != nil {
		a.renderAdminActivityEdit(w, r, access, &existing, err.Error(), "")
		return
	}
	updated, err := a.db.UpdateActivity(r.Context(), existing.ID, in)
	if err != nil {
		a.logger(r).Error("update admin activity failed", "error", err)
		a.renderAdminActivityEdit(w, r, access, &existing, "保存失败，请稍后再试。", "")
		return
	}
	a.audit(r, &access.User.ID, "admin.activity.update", "activity", updated.ActivityNo, nil)
	http.Redirect(w, r, adminActivityURL(updated.ID, "活动已保存。"), http.StatusSeeOther)
}

func adminActivityURL(activityID int64, notice string) string {
	return fmt.Sprintf("/admin/activities/%d?notice=%s", activityID, url.QueryEscape(notice))
}

// adminActivityRosterHref keeps the roster filter and page in the URL so the
// browser can return to the list (and the #roster anchor) after a redirect.
func adminActivityRosterHref(activityID int64, query, attendance string, page int) string {
	values := url.Values{}
	if query != "" {
		values.Set("q", query)
	}
	if attendance != "" && attendance != "all" {
		values.Set("attendance", attendance)
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	target := fmt.Sprintf("/admin/activities/%d", activityID)
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return target + "#roster"
}

// adminActivityMemberHref redirects to the exact roster row. The roster query
// keeps the notice next to the list instead of at the top of the page.
func adminActivityMemberHref(activityID int64, notice string, page int, userID int64) string {
	values := url.Values{}
	values.Set("notice", notice)
	values.Set("roster", "attendance")
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	return fmt.Sprintf("/admin/activities/%d?%s#member-%d", activityID, values.Encode(), userID)
}

// adminActivityBroadcastHref pins the send result to the broadcast card.
func adminActivityBroadcastHref(activityID int64, notice string) string {
	values := url.Values{}
	values.Set("notice", notice)
	values.Set("broadcast", "sent")
	return fmt.Sprintf("/admin/activities/%d?%s#broadcast", activityID, values.Encode())
}

func parseAdminRosterPage(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// filterAdminRoster applies the server-side roster search and attendance
// filter. Query matching is case-insensitive across name, nickname, legacy id
// and the already-masked phone number.
func filterAdminRoster(roster []db.AdminActivityRosterEntry, query, attendance string) []db.AdminActivityRosterEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	attendance = strings.TrimSpace(attendance)
	if query == "" && (attendance == "" || attendance == "all") {
		return roster
	}
	out := make([]db.AdminActivityRosterEntry, 0, len(roster))
	for _, entry := range roster {
		switch attendance {
		case "", "all":
		case "checked":
			if !entry.Attendance.CheckedIn() {
				continue
			}
		case "unchecked":
			if entry.Attendance != "" {
				continue
			}
		case "late":
			if entry.Attendance != db.AttendanceLate {
				continue
			}
		case "absent":
			if entry.Attendance != db.AttendanceAbsent {
				continue
			}
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				entry.Name, entry.Nickname, entry.LegacyUserID, entry.PhoneMasked,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

func (a *App) adminRosterPageForUser(ctx context.Context, activityID, userID int64) int {
	roster, err := a.db.AdminActivityRoster(ctx, activityID)
	if err != nil {
		return 1
	}
	for i, entry := range roster {
		if entry.UserID == userID {
			return i/adminRosterPageSize + 1
		}
	}
	return 1
}

// adminActivityByPath resolves the numeric id from the route. It writes the
// error response itself and reports false so callers can return.
func (a *App) adminActivityByPath(w http.ResponseWriter, r *http.Request) (db.Activity, bool) {
	id, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return db.Activity{}, false
	}
	activity, err := a.db.GetActivity(r.Context(), strconv.FormatInt(id, 10))
	if errors.Is(err, db.ErrActivityNotFound) {
		a.renderError(w, r, http.StatusNotFound, "找不到活动", "这个活动不存在或已下架。")
		return db.Activity{}, false
	}
	if err != nil {
		a.logger(r).Error("load admin activity failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return db.Activity{}, false
	}
	return activity, true
}

func (a *App) handleAdminActivityDetail(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	form := adminActivityBroadcastFormFromRequest(r)
	// Default to the signup audience on a fresh workbench page; explicit
	// preview/send submissions always carry their own checkbox state.
	if query.Get("preview") == "" && query.Get("include_signups") == "" &&
		query.Get("include_observers") == "" {
		form.IncludeSignups = true
	}
	notice := query.Get("notice")
	rosterNotice, broadcastNotice := "", ""
	switch query.Get("roster") {
	case "attendance":
		rosterNotice, notice = notice, ""
	}
	switch query.Get("broadcast") {
	case "sent":
		broadcastNotice, notice = notice, ""
	}
	a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusOK, "", notice, rosterNotice, broadcastNotice)
}

func adminActivityBroadcastFormFromRequest(r *http.Request) adminActivityBroadcastForm {
	return adminActivityBroadcastForm{
		Key:              strings.TrimSpace(r.FormValue("broadcast_key")),
		Title:            strings.TrimSpace(r.FormValue("broadcast_title")),
		Body:             strings.TrimSpace(r.FormValue("broadcast_body")),
		IncludeSignups:   r.FormValue("include_signups") == "1",
		IncludeObservers: r.FormValue("include_observers") == "1",
		Preview:          r.FormValue("preview") == "1" || r.FormValue("broadcast_action") == "preview",
	}
}

func (a *App) renderAdminActivityDetail(w http.ResponseWriter, r *http.Request, access adminAccess,
	activity db.Activity, form adminActivityBroadcastForm, status int, errMessage, notice, rosterNotice, broadcastNotice string) {
	roster, err := a.db.AdminActivityRoster(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("load admin activity roster failed", "error", err, "activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	stats, err := a.db.AdminActivitySigninStats(r.Context())
	if err != nil {
		a.logger(r).Error("load admin activity stats failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	audience, err := a.db.AdminActivityBroadcastAudience(r.Context(), activity.ID, true, true)
	if err != nil {
		a.logger(r).Error("load admin activity audience failed", "error", err, "activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	if form.Key == "" {
		key, err := platform.NewUUID()
		if err != nil {
			a.logger(r).Error("generate broadcast key failed", "error", err)
			a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
			return
		}
		form.Key = key
	}
	previewCount := 0
	if form.Preview {
		previewCount = len(adminBroadcastRecipients(audience, form.IncludeSignups, form.IncludeObservers))
	}
	query := r.URL.Query()
	rosterQuery := strings.TrimSpace(query.Get("q"))
	rosterAttendance := strings.TrimSpace(query.Get("attendance"))
	if rosterAttendance == "all" {
		rosterAttendance = ""
	}
	filteredRoster := filterAdminRoster(roster, rosterQuery, rosterAttendance)
	page := parseAdminRosterPage(query.Get("page"))
	pageCount := (len(filteredRoster) + adminRosterPageSize - 1) / adminRosterPageSize
	if pageCount == 0 {
		pageCount = 1
	}
	if page > pageCount {
		page = pageCount
	}
	start := (page - 1) * adminRosterPageSize
	end := start + adminRosterPageSize
	if start > len(filteredRoster) {
		start = len(filteredRoster)
	}
	if end > len(filteredRoster) {
		end = len(filteredRoster)
	}
	var rosterPage []db.AdminActivityRosterEntry
	if start < end {
		rosterPage = filteredRoster[start:end]
	}
	prevURL, nextURL := "", ""
	if page > 1 {
		prevURL = adminActivityRosterHref(activity.ID, rosterQuery, rosterAttendance, page-1)
	}
	if page < pageCount {
		nextURL = adminActivityRosterHref(activity.ID, rosterQuery, rosterAttendance, page+1)
	}
	statusUnknown := ""
	if activity.Status != "" && !adminActivityStatusKnown(activity.Status) {
		statusUnknown = activity.Status
	}
	data := adminActivityDetailPageData{
		Activity:                  &activity,
		Roster:                    rosterPage,
		Stats:                     stats[activity.ID],
		Statuses:                  adminActivityStatuses,
		StatusUnknown:             statusUnknown,
		RosterAllCount:            len(roster),
		RosterFilteredCount:       len(filteredRoster),
		RosterQuery:               rosterQuery,
		RosterAttendance:          rosterAttendance,
		RosterHasFilter:           rosterQuery != "" || rosterAttendance != "",
		RosterPage:                page,
		RosterPageCount:           pageCount,
		RosterPrevURL:             prevURL,
		RosterNextURL:             nextURL,
		RosterClearURL:            adminActivityRosterHref(activity.ID, "", "", 1),
		RosterNotice:              rosterNotice,
		SignupAudienceCount:       len(audience.SignupUserIDs),
		ObserverAudienceCount:     len(audience.ObserverUserIDs),
		AudienceTotal:             len(adminBroadcastRecipients(audience, true, true)),
		BroadcastKey:              form.Key,
		BroadcastTitle:            form.Title,
		BroadcastBody:             form.Body,
		BroadcastIncludeSignups:   form.IncludeSignups,
		BroadcastIncludeObservers: form.IncludeObservers,
		BroadcastPreview:          form.Preview,
		BroadcastPreviewCount:     previewCount,
		BroadcastNotice:           broadcastNotice,
		BroadcastError:            errMessage,
	}
	mediaMaxMB := int(a.cfg.MediaMaxBytes / (1 << 20))
	if mediaMaxMB < 1 {
		mediaMaxMB = 1
	}
	a.render(w, r, status, "admin_activity_detail", PageData{
		Title: "活动工作台", User: &access.User, Data: data,
		Notice:  notice,
		PageCSS: "admin.css", ActiveNav: "activities", MediaMaxMB: mediaMaxMB,
	})
}

func (a *App) handleAdminActivityPoster(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	poster, err := a.db.GetActivityPoster(r.Context(), strconv.FormatInt(activity.ID, 10))
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load admin activity poster failed", "error", err, "activity_id", activity.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	key, size, contentType := poster.ObjectKey, poster.ByteSize, poster.ContentType
	if r.URL.Query().Get("size") == "thumb" && poster.ThumbKey != "" {
		key, size, contentType = poster.ThumbKey, poster.ThumbSize, "image/jpeg"
	}
	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("open admin activity poster failed", "error", err, "activity_id", activity.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer obj.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, obj, size); err != nil {
		a.logger(r).Debug("stream admin activity poster interrupted", "error", err)
	}
}

func (a *App) handleAdminActivityPosterUpload(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	if !a.allowUpload(w, r, access.User.ID, false) {
		return
	}
	if !a.acquireMedia(w, r, false) {
		return
	}
	defer a.releaseMedia()
	// Multipart CSRF lives in the parsed form, so parse before checking it.
	if err := a.parseMultipartRequest(w, r); err != nil {
		status, _, message := uploadParseErrorDetails(err)
		a.renderError(w, r, status, "上传格式错误", message)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}
	saved, err := a.receiveUpload(r, access.User)
	if err != nil {
		status, _, message := uploadErrorDetails(err)
		if status >= http.StatusInternalServerError {
			a.logger(r).Error("admin activity poster upload failed", "error", err, "activity_id", activity.ID)
		}
		a.renderError(w, r, status, "上传失败", message)
		return
	}
	if err := a.db.SetActivityPoster(r.Context(), activity.ID, saved.ID); err != nil {
		if errors.Is(err, db.ErrActivityNotFound) {
			a.renderError(w, r, http.StatusNotFound, "找不到活动", "这个活动不存在或已下架。")
			return
		}
		a.logger(r).Error("link admin activity poster failed", "error", err, "activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "上传失败", "系统暂时无法处理，请稍后再试。")
		return
	}
	a.audit(r, &access.User.ID, "admin.activity.poster", "activity", activity.ActivityNo,
		map[string]any{"media_id": saved.ID})
	http.Redirect(w, r, adminActivityURL(activity.ID, "海报已更新。"), http.StatusSeeOther)
}

func (a *App) handleAdminActivityAttendance(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("user_id")), 10, 64)
	if err != nil || userID <= 0 {
		a.renderError(w, r, http.StatusBadRequest, "操作失败", "用户识别码格式不正确。")
		return
	}
	state := db.Attendance(strings.TrimSpace(r.PostFormValue("attendance")))
	updated, err := a.db.AdminSetActivityAttendance(r.Context(), activity.ID, userID, state)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrInvalidAttendance):
			a.renderError(w, r, http.StatusBadRequest, "操作失败", "签到状态不正确。")
		case errors.Is(err, db.ErrNotSignedUp):
			a.renderError(w, r, http.StatusNotFound, "找不到有效报名", "该用户没有这个活动的有效报名。")
		default:
			a.logger(r).Error("set admin activity attendance failed", "error", err,
				"activity_id", activity.ID, "user_id", userID)
			a.renderError(w, r, http.StatusInternalServerError, "操作失败", "系统暂时无法处理，请稍后再试。")
		}
		return
	}
	a.audit(r, &access.User.ID, "admin.activity.attendance", "activity", activity.ActivityNo,
		map[string]any{"user_id": userID, "attendance": string(updated)})
	notice := "签到状态已更新。"
	if updated == "" {
		notice = "已取消该用户的签到。"
	}
	page := a.adminRosterPageForUser(r.Context(), activity.ID, userID)
	http.Redirect(w, r, adminActivityMemberHref(activity.ID, notice, page, userID), http.StatusSeeOther)
}

func (a *App) handleAdminActivityBroadcast(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	if !a.adminFormPost(w, r) {
		return
	}
	form := adminActivityBroadcastFormFromRequest(r)
	action := strings.TrimSpace(r.PostFormValue("broadcast_action"))
	previewOnly := action == "preview" || (action == "" && form.Preview)
	form.Preview = false
	if !form.IncludeSignups && !form.IncludeObservers {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"请至少选择一个收件人群组。", "", "", "")
		return
	}
	audience, err := a.db.AdminActivityBroadcastAudience(r.Context(), activity.ID,
		form.IncludeSignups, form.IncludeObservers)
	if err != nil {
		a.logger(r).Error("load admin activity audience failed", "error", err, "activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	recipients := adminBroadcastRecipients(audience, form.IncludeSignups, form.IncludeObservers)
	if len(recipients) == 0 {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"当前没有符合条件的收件人，未发送。", "", "", "")
		return
	}
	if previewOnly {
		// Keep the preview count next to the form instead of using the flash
		// area at the top of the page.
		form.Preview = true
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusOK, "", "", "", "")
		return
	}
	// Sending is a second, explicit step after the preview so an accidental
	// click cannot reach every signup and observer on the site.
	if action != "send" {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"请先预览收件人数，确认对象后再发送。", "", "", "")
		return
	}
	if form.Title == "" {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"请填写通知标题。", "", "", "")
		return
	}
	if len([]rune(form.Title)) > adminBroadcastTitleMax {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"通知标题过长。", "", "", "")
		return
	}
	if len([]rune(form.Body)) > adminBroadcastBodyMax {
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"通知内容过长。", "", "", "")
		return
	}
	if !validBroadcastKey(form.Key) {
		form.Key = ""
		a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusBadRequest,
			"广播请求已过期，请重新提交。", "", "", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	sent := 0
	for _, userID := range recipients {
		created, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
			UserID: userID,
			Kind:   "activity_broadcast",
			Title:  form.Title,
			Body:   form.Body,
			Link:   "/activities/" + activity.ActivityNo,
			Payload: map[string]any{
				"activity_id":   activity.ActivityNo,
				"activity_name": activity.Name,
			},
			DedupeKey: fmt.Sprintf("activity-broadcast:%d:%s:%d", activity.ID, form.Key, userID),
		})
		if err != nil {
			a.logger(r).Error("send activity broadcast failed", "error", err,
				"activity_id", activity.ID, "user_id", userID)
			a.audit(r, &access.User.ID, "admin.activity.broadcast", "activity", activity.ActivityNo,
				map[string]any{
					"audience":        adminBroadcastAudienceLabels(form),
					"recipient_count": sent,
					"title":           form.Title,
				})
			a.renderAdminActivityDetail(w, r, access, activity, form, http.StatusInternalServerError,
				fmt.Sprintf("广播发送中断：已发送 %d 人，请重新提交同一表单继续（不会重复发送）。", sent),
				"", "", "")
			return
		}
		if created {
			sent++
		}
	}
	a.audit(r, &access.User.ID, "admin.activity.broadcast", "activity", activity.ActivityNo,
		map[string]any{
			"audience":        adminBroadcastAudienceLabels(form),
			"recipient_count": sent,
			"title":           form.Title,
		})
	notice := fmt.Sprintf("广播已发送：新增 %d 位收件人（本次对象 %d 人）。", sent, len(recipients))
	if sent == 0 {
		notice = fmt.Sprintf("该广播已发送过，本次未重复发送（对象 %d 人）。", len(recipients))
	}
	http.Redirect(w, r, adminActivityBroadcastHref(activity.ID, notice), http.StatusSeeOther)
}

func adminBroadcastAudienceLabels(form adminActivityBroadcastForm) []string {
	labels := make([]string, 0, 2)
	if form.IncludeSignups {
		labels = append(labels, "signups")
	}
	if form.IncludeObservers {
		labels = append(labels, "observers")
	}
	return labels
}

// adminBroadcastRecipients unions both sets while preserving signup-first
// order; the handler uses it for both preview counts and sending.
func adminBroadcastRecipients(audience db.AdminActivityBroadcastAudience, includeSignups, includeObservers bool) []int64 {
	seen := make(map[int64]struct{}, len(audience.SignupUserIDs)+len(audience.ObserverUserIDs))
	out := make([]int64, 0, len(audience.SignupUserIDs)+len(audience.ObserverUserIDs))
	appendIDs := func(ids []int64) {
		for _, userID := range ids {
			if _, ok := seen[userID]; ok {
				continue
			}
			seen[userID] = struct{}{}
			out = append(out, userID)
		}
	}
	if includeSignups {
		appendIDs(audience.SignupUserIDs)
	}
	if includeObservers {
		appendIDs(audience.ObserverUserIDs)
	}
	return out
}

// validBroadcastKey accepts the UUID shape generated by platform.NewUUID.
func validBroadcastKey(key string) bool {
	key = strings.TrimSpace(key)
	if len(key) != 36 {
		return false
	}
	for i, c := range key {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func (a *App) parseAdminActivityForm(r *http.Request, existing *db.Activity) (db.AdminActivityInput, error) {
	if err := r.ParseForm(); err != nil {
		return db.AdminActivityInput{}, errors.New("表单格式不正确")
	}
	if !a.formCSRFValid(r) {
		return db.AdminActivityInput{}, errors.New("表单已过期，请重新整理后再试")
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		return db.AdminActivityInput{}, errors.New("活动名称必填")
	}
	status := strings.TrimSpace(r.PostFormValue("status"))
	if status == "" {
		// The quick-settings select omits unknown stored statuses; an empty
		// value means "keep the current status" on an update.
		if existing == nil {
			return db.AdminActivityInput{}, errors.New("请选择活动状态")
		}
		status = existing.Status
	} else if !adminActivityStatusKnown(status) {
		// Unknown legacy statuses may only be kept as-is; the form's
		// "keep current" option posts the stored value back unchanged.
		if existing == nil || status != existing.Status {
			return db.AdminActivityInput{}, errors.New("活动状态无效")
		}
	}
	maxSignup, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("max_signup")))
	if err != nil || maxSignup < 0 {
		return db.AdminActivityInput{}, errors.New("报名上限必须是非负整数")
	}
	male, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("male_per_group")))
	if err != nil || male < 0 {
		return db.AdminActivityInput{}, errors.New("每组男生数必须是非负整数")
	}
	female, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("female_per_group")))
	if err != nil || female < 0 {
		return db.AdminActivityInput{}, errors.New("每组女生数必须是非负整数")
	}
	publish, err := parseAdminActivityTime(r.PostFormValue("publish_at"))
	if err != nil {
		return db.AdminActivityInput{}, errors.New("发布时间格式不正确")
	}
	start, err := parseAdminActivityTime(r.PostFormValue("start_at"))
	if err != nil {
		return db.AdminActivityInput{}, errors.New("开始时间格式不正确")
	}
	end, err := parseAdminActivityTime(r.PostFormValue("end_at"))
	if err != nil {
		return db.AdminActivityInput{}, errors.New("结束时间格式不正确")
	}
	return db.AdminActivityInput{
		Name: name, Description: r.PostFormValue("description"), Location: r.PostFormValue("location"),
		Condition: r.PostFormValue("condition"), MaxSignup: maxSignup, Fee: r.PostFormValue("fee"),
		Food: r.PostFormValue("food"), Status: status,
		GroupEnabled: r.PostFormValue("group_enabled") == "1", MalePerGroup: male, FemalePerGroup: female,
		OnetooneEnabled: r.PostFormValue("onetoone_enabled") == "1",
		SigninEnabled:   r.PostFormValue("signin_enabled") == "1", SigninCode: r.PostFormValue("signin_code"),
		PublishAt: publish, StartAt: start, EndAt: end,
	}, nil
}

// aFormCSRFValid is kept separate so the activity form can be unit-tested.
func parseAdminActivityTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
