package httpserver

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	webassets "github.com/7310C7310C/yixianqian/web"
)

// adminUITestUser is shared by the management console template tests.
func adminUITestUser() *db.User {
	legacy := "U-0006"
	return &db.User{ID: 1, LegacyUserID: &legacy, Name: "管理员", Phone: "13800000006"}
}

func renderAdminPage(t *testing.T, page string, data PageData) string {
	t.Helper()
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	data.User = adminUITestUser()
	if data.CSRFToken == "" {
		data.CSRFToken = "csrf-value"
	}
	var buf bytes.Buffer
	if err := tpl.Render(&buf, page, data); err != nil {
		t.Fatalf("render %s: %v", page, err)
	}
	return buf.String()
}

// The dashboard is the single entry: grouped cards and four key counters, no
// flat nine-item subnav.
func TestAdminDashboardGroupedNavigation(t *testing.T) {
	html := renderAdminPage(t, "admin", PageData{
		Title: "管理台", ActiveNav: "admin",
		Data: adminHomeData{
			Stats: db.AdminStats{
				Users:              db.AdminUserCounts{Pending: 2, Total: 10},
				PendingReports:     1,
				PendingSuggestions: 3,
				ActivityOpen:       4,
			},
			Roles:       []string{"super_admin"},
			SuperAdmin:  true,
			CanReview:   true,
			CanManage:   true,
			CanModerate: true,
			CanAdmin:    true,
			CanActivity: true,
			CanPoints:   true,
			CanAudit:    true,
		},
	})

	for _, want := range []string{
		`class="admin-topbar"`,
		"管理台",
		"待审核用户", "待处理举报", "待处理反馈", "进行中活动",
		"用户与权限", "活动", "匹配", "麦穗", "内容与安全",
		`href="/admin/users?status=pending"`,
		`href="/admin/roles"`,
		`href="/admin/activities"`, `href="/admin/activities/new"`,
		`href="/admin/matching"`, `href="/admin/points"`, `href="/admin/audit"`,
		"super_admin", "新建活动", "活动列表",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
	for _, unwanted := range []string{`class="subnav`, `class="admin-nav`, `aria-label="管理台"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("dashboard still renders flat nav %q", unwanted)
		}
	}
}

// Permissions decide which entry cards exist; the page must not link to a
// console the role cannot open.
func TestAdminDashboardHidesForbiddenEntries(t *testing.T) {
	html := renderAdminPage(t, "admin", PageData{
		Title: "管理台", ActiveNav: "admin",
		Data: adminHomeData{
			Stats:       db.AdminStats{Users: db.AdminUserCounts{Pending: 1}},
			Roles:       []string{"reviewer"},
			CanReview:   true,
			CanAudit:    true,
			CanPoints:   false,
			CanModerate: false,
		},
	})
	for _, want := range []string{"用户审核", "内容与安全", "审计日志"} {
		if !strings.Contains(html, want) {
			t.Fatalf("reviewer dashboard missing %q", want)
		}
	}
	for _, unwanted := range []string{`href="/admin/roles"`, `href="/admin/points"`, `href="/admin/activities/new"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("reviewer dashboard leaked %q", unwanted)
		}
	}
}

func TestAdminSubpageTopbar(t *testing.T) {
	html := renderAdminPage(t, "admin_activities", PageData{
		Title: "活动管理", ActiveNav: "activities",
		Data: adminActivitiesPageData{},
	})
	for _, want := range []string{
		`class="admin-topbar"`, "活动管理", "返回总览", `href="/admin"`,
		"活动列表", "新建活动",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("subpage topbar missing %q", want)
		}
	}
	if strings.Contains(html, `class="admin-nav`) || strings.Contains(html, `class="subnav`) {
		t.Fatal("subpage still renders the old flat nav")
	}
}

func TestAdminActivitiesPageElements(t *testing.T) {
	now := time.Now()
	activity := db.Activity{
		ID: 5, ActivityNo: "A-0005", Name: "模板测试活动", Status: "报名中",
		Location: "市图书馆", MaxSignup: 20, CurrentSignup: 3,
		SigninEnabled: true, SigninCode: "0123", GroupEnabled: true,
		StartAt: &now,
	}
	html := renderAdminPage(t, "admin_activities", PageData{
		Title: "活动管理", ActiveNav: "activities",
		Data: adminActivitiesPageData{
			Activities: []adminActivityRow{{Activity: activity, SignupCount: 3, CheckedInCount: 1}},
			Summary:    adminActivitiesSummary{Total: 1, Open: 1, Signups: 3, CheckedIn: 1},
		},
	})
	for _, want := range []string{
		`class="admin-stats-inline"`, "活动总数", "有效报名", "已签到",
		`data-adm-activity-search`, `data-adm-activity-filter="报名中"`,
		`class="admin-activity-card"`, `data-adm-status="报名中"`,
		"模板测试活动", "A-0005", "市图书馆",
		"已签到 1", "工作台", "编辑", "新建活动",
		`href="/admin/activities/5"`, `href="/admin/activities/5/edit"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("activity list missing %q", want)
		}
	}
}

func TestAdminWorkbenchTabPanels(t *testing.T) {
	now := time.Now()
	activity := db.Activity{
		ID: 5, ActivityNo: "A-0005", Name: "模板测试活动", Status: "报名中",
		MaxSignup: 20, CurrentSignup: 1, SigninEnabled: true, SigninCode: "0123",
		GroupEnabled: true, StartAt: &now,
	}
	html := renderAdminPage(t, "admin_activity_detail", PageData{
		Title: "活动工作台", ActiveNav: "activities",
		Data: adminActivityDetailPageData{
			Activity: &activity,
			Roster: []db.AdminActivityRosterEntry{{
				UserID: 7, LegacyUserID: "U-0007", Name: "张三", Gender: "女",
				PhoneMasked: "138****1234", SignupAt: now, Attendance: db.AttendanceOnTime,
			}},
			Stats:                 db.AdminActivitySigninStats{SignupCount: 1, CheckedInCount: 1},
			Statuses:              adminActivityStatuses,
			RosterAllCount:        1,
			RosterFilteredCount:   1,
			RosterPage:            1,
			RosterPageCount:       1,
			RosterReturnTo:        "/admin/activities/5#roster",
			RosterClearURL:        "/admin/activities/5#roster",
			BroadcastKey:          "123e4567-e89b-42d3-a456-426614174000",
			BroadcastPreview:      true,
			BroadcastPreviewToken: "signed-preview-token",
			BroadcastPreviewCount: 2,
			AudienceTotal:         2,
			SignupAudienceCount:   1,
			ObserverAudienceCount: 1,
		},
		MediaMaxMB: 10,
	})
	for _, want := range []string{
		`data-adm-tabs`, `data-adm-tab="roster"`, `data-adm-tab="signin"`,
		`data-adm-tab="broadcast"`, `data-adm-tab="settings"`,
		`data-adm-panel="roster"`, `data-adm-panel="signin"`,
		`data-adm-panel="broadcast"`, `data-adm-panel="settings"`,
		`id="broadcast"`, `id="signin"`, `id="settings"`,
		"报名/签到名单", "现场签到", "站内广播", "快速设置", "分组 / 一对一",
		`data-adm-copy="0123"`, `data-adm-signin-stats`,
		`data-adm-confirm=`, "确认发送？", "回到顶部",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("workbench missing %q", want)
		}
	}
	if got := strings.Count(html, `id="member-`); got != 1 {
		t.Fatalf("member anchors = %d, want 1 (signin list must not duplicate ids)", got)
	}
}

func TestAdminEditFormGroupedSections(t *testing.T) {
	html := renderAdminPage(t, "admin_activity_edit", PageData{
		Title: "活动编辑", ActiveNav: "activities", MediaMaxMB: 10,
		Data: adminActivityEditPageData{
			Activity: &db.Activity{Status: "报名中"}, IsNew: true,
			Statuses: adminActivityStatuses,
		},
	})
	for _, want := range []string{
		"基本信息", "时间与地点", "名额与条件", "签到与匹配", "活动海报", "发布状态",
		`class="admin-form-grid"`, `class="admin-form-actions admin-form-actions--sticky"`,
		`type="text" name="name"`, `type="datetime-local" name="publish_at"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("edit form missing %q", want)
		}
	}
}

// /admin CSP is style-src 'self'; inline style attributes would be blocked and
// show up as console errors in the real browser.
func TestAdminTemplatesHaveNoInlineStyles(t *testing.T) {
	files, err := fs.Glob(webassets.FS, "templates/pages/admin*.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "templates/partials/admin_nav.tmpl", "templates/partials/admin_ui.tmpl")
	for _, name := range files {
		raw, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), `style="`) {
			t.Fatalf("%s contains an inline style attribute", name)
		}
	}
}
