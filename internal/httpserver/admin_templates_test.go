package httpserver

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestAdminTemplatesRender(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	legacy := "U-0006"
	user := &db.User{ID: 1, LegacyUserID: &legacy, Name: "管理员", Phone: "13800000006"}
	now := time.Now()

	cases := []struct {
		name string
		page string
		data PageData
		want []string
	}{
		{
			name: "dashboard",
			page: "admin",
			data: PageData{Title: "管理台", User: user, CSRFToken: "csrf-value", ActiveNav: "admin",
				Data: adminHomeData{
					Stats:      db.AdminStats{Users: db.AdminUserCounts{Pending: 2, Total: 10}, PendingReports: 1, PendingSuggestions: 3, SuperAdmins: 1},
					Roles:      []string{"super_admin"},
					SuperAdmin: true, CanReview: true, CanManage: true, CanModerate: true, CanAdmin: true,
				}},
			want: []string{"管理台", "待审核用户", "待处理举报", "待处理反馈", "super_admin"},
		},
		{
			name: "users",
			page: "admin_users",
			data: PageData{Title: "用户管理", User: user, CSRFToken: "csrf-value", ActiveNav: "users",
				Data: adminUsersPageData{
					Users: []db.AdminUser{{
						ID: 7, LegacyUserID: "U-0007", Name: "待审核", Phone: "13800000007",
						AccountStatus: "pending", LoginStatus: "active", CreatedAt: now,
					}},
					Counts:       db.AdminUserCounts{Pending: 1, Total: 1},
					StatusFilter: "pending",
					Statuses:     adminUserStatusOptions(),
					CanReview:    true,
					CanManage:    true,
				}},
			want: []string{"用户审核", "待审核", "U-0007", `action="/admin/users/7/approve"`},
		},
		{
			name: "reports",
			page: "admin_reports",
			data: PageData{Title: "举报处理", User: user, CSRFToken: "csrf-value", ActiveNav: "reports",
				Data: adminReportsPageData{
					Mode:         "reports",
					StatusFilter: "pending",
					Statuses:     reportStatusOptions(),
					Reports: []db.Report{{
						ID: 9, ReporterName: "举报人", TargetName: "对象", Reason: "不当行为",
						Status: "待处理", CreatedAt: now,
					}},
				}},
			want: []string{"举报处理", "不当行为", `action="/admin/reports/9/handle"`},
		},
		{
			name: "suggestions",
			page: "admin_reports",
			data: PageData{Title: "意见反馈", User: user, CSRFToken: "csrf-value", ActiveNav: "suggestions",
				Data: adminReportsPageData{
					Mode:         "suggestions",
					StatusFilter: "pending",
					Statuses:     reportStatusOptions(),
					Suggestions: []db.AdminSuggestion{{
						ID: 11, AuthorName: "反馈人", Type: "功能建议", Content: "希望有夜间模式",
						Status: "待处理", CreatedAt: now,
					}},
				}},
			want: []string{"意见反馈", "希望有夜间模式", `action="/admin/suggestions/11/handle"`},
		},
		{
			name: "roles",
			page: "admin_roles",
			data: PageData{Title: "角色权限", User: user, CSRFToken: "csrf-value", ActiveNav: "roles",
				Data: adminRolesPageData{
					Roles: []db.AdminRole{{
						Code: "super_admin", Name: "最高管理员",
						Permissions: []string{"admin.manage", "user.review"}, UserCount: 1,
					}},
					Assignments: []db.AdminAssignment{{
						UserID: 1, LegacyUserID: "U-0006", Name: "管理员", Roles: []string{"super_admin"},
					}},
					SuperAdmin: true,
				}},
			want: []string{"角色与权限", "super_admin", "指派角色", `action="/admin/roles/assign"`},
		},
		{
			name: "password",
			page: "password",
			data: PageData{Title: "修改密码", CSRFToken: "csrf-value", ScanSafe: true, CopyPage: "password",
				Data: passwordPageData{Required: true}},
			want: []string{"修改密码", `data-copy-text="password.required"`, `action="/password"`},
		},
		{
			name: "admin error",
			page: "admin_error",
			data: PageData{Title: "登录已过期", User: user, Error: "登录状态已失效，请重新登录后再打开管理台。",
				ActiveNav: "admin", Data: adminErrorData{Relogin: true}},
			want: []string{"admin-topbar", "登录已过期", "返回管理台", "重新登录", `target="_top"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tpl.Render(&buf, tc.page, tc.data); err != nil {
				t.Fatalf("render: %v", err)
			}
			html := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Fatalf("missing %q in output", want)
				}
			}
		})
	}
}
