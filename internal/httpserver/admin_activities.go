package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const adminPermActivity = "activity.manage"

type adminActivitiesPageData struct {
	Activities []db.Activity
}

type adminActivityEditPageData struct {
	Activity *db.Activity
	IsNew    bool
	Error    string
	Statuses []string
}

var adminActivityStatuses = []string{"报名中", "已结束", "已取消", "草稿"}

func (a *App) handleAdminActivitiesPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activities, err := a.db.ListActivities(r.Context())
	if err != nil {
		a.logger(r).Error("list admin activities failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "admin_activities", PageData{
		Title: "活动管理", User: &access.User,
		Data:    adminActivitiesPageData{Activities: activities},
		PageCSS: "admin.css", ActiveNav: "activities",
		Notice: r.URL.Query().Get("notice"),
	})
}

func (a *App) handleAdminActivityNew(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.renderAdminActivityEdit(w, r, access, nil, "", "报名中")
}

func (a *App) handleAdminActivityEdit(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, err := a.db.GetActivity(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrActivityNotFound) {
		a.renderError(w, r, http.StatusNotFound, "找不到活动", "这个活动不存在或已下架。")
		return
	}
	if err != nil {
		a.logger(r).Error("load admin activity failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	a.renderAdminActivityEdit(w, r, access, &activity, "", "")
}

func (a *App) renderAdminActivityEdit(w http.ResponseWriter, r *http.Request, access adminAccess,
	activity *db.Activity, errMessage, newStatus string) {
	data := adminActivityEditPageData{Activity: activity, IsNew: activity == nil, Error: errMessage, Statuses: adminActivityStatuses}
	if activity == nil {
		data.Activity = &db.Activity{Status: newStatus}
	}
	a.render(w, r, http.StatusOK, "admin_activity_edit", PageData{
		Title: "活动编辑", User: &access.User, Data: data,
		PageCSS: "admin.css", ActiveNav: "activities",
	})
}

func (a *App) handleAdminActivityCreate(w http.ResponseWriter, r *http.Request, access adminAccess) {
	in, err := a.parseAdminActivityForm(r)
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
	http.Redirect(w, r, "/admin/activities?notice="+url.QueryEscape("活动已创建。"), http.StatusSeeOther)
}

func (a *App) handleAdminActivityUpdate(w http.ResponseWriter, r *http.Request, access adminAccess) {
	existing, err := a.db.GetActivity(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrActivityNotFound) {
		a.renderError(w, r, http.StatusNotFound, "找不到活动", "这个活动不存在或已下架。")
		return
	}
	if err != nil {
		a.logger(r).Error("load admin activity failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	in, err := a.parseAdminActivityForm(r)
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
	http.Redirect(w, r, "/admin/activities?notice="+url.QueryEscape("活动已保存。"), http.StatusSeeOther)
}

func (a *App) parseAdminActivityForm(r *http.Request) (db.AdminActivityInput, error) {
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
	if !containsString(adminActivityStatuses, status) {
		return db.AdminActivityInput{}, errors.New("活动状态无效")
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
