package httpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// Phase B/C：剩余管理页必须套用同一设计系统（页头 / 分区 / 筛选 / 记录卡 / 空态），
// 同时保留路由与表单契约（字段名、action、data-* 钩子），避免视觉重构破坏功能。
func TestAdminPhaseBCTemplatesUseDesignSystem(t *testing.T) {
	now := time.Now()
	activity := db.Activity{
		ID: 5, ActivityNo: "A-0005", Name: "模板测试活动", Status: "报名中",
		Location: "市图书馆", MaxSignup: 20, CurrentSignup: 3,
		SigninEnabled: true, SigninCode: "0123", GroupEnabled: true, OnetooneEnabled: true,
		StartAt: &now,
	}

	cases := []struct {
		name string
		page string
		data PageData
		want []string
		// notWant 只用于确认旧样式骨架已移除；不要放业务字段。
		notWant []string
	}{
		{
			name: "users",
			page: "admin_users",
			data: PageData{Title: "用户管理", ActiveNav: "users", Data: adminUsersPageData{
				Users: []db.AdminUser{{
					ID: 7, LegacyUserID: "U-0007", Name: "张三", Nickname: "小张",
					Phone: "13800000007", AccountStatus: "pending", LoginStatus: "active",
					CreatedAt: now, Roles: []string{"reviewer"},
				}},
				Counts:       db.AdminUserCounts{Pending: 1, Active: 2, Rejected: 0, Disabled: 0, Total: 3},
				StatusFilter: "pending",
				Statuses:     adminUserStatusOptions(),
				CanReview:    true,
				CanManage:    true,
			}},
			want: []string{
				`class="admin-page-head"`, "用户审核与管理", "admin-stat-grid",
				`class="admin-filter-bar admin-filter-bar--inline"`, "admin-filter-chips",
				`name="q"`, `name="status"`, "admin-record-list", "admin-record-actions",
				`data-adm-confirm="确认停用 张三 的账号？停用后该用户无法登录。"`,
				`href="/admin/users/7"`, `action="/admin/users/7/approve"`,
				`action="/admin/users/7/reject"`, `action="/admin/users/7/temp-password"`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-table`},
		},
		{
			name: "user-detail",
			page: "admin_user_detail",
			data: PageData{Title: "用户详情", ActiveNav: "users", Data: adminUserDetailPageData{
				User:          db.AdminUser{ID: 7, LegacyUserID: "U-0007", Name: "张三", Phone: "13800000007", AccountStatus: "pending", LoginStatus: "active"},
				AccountStatus: "待审核", LoginStatus: "正常", Roles: "—", CanManage: true,
				Next: "/admin/users/7", Groups: []adminUserDetailGroup{{Title: "基本资料", Fields: []adminUserDetailField{{Label: "城市", Value: "杭州"}}}},
			}},
			want: []string{
				`class="admin-page-head"`, "admin-stats-inline", `class="admin-kv"`,
				`action="/admin/users/7/approve"`, `name="require_note" value="1"`,
				`action="/admin/users/7/reject"`, `action="/admin/users/7/temp-password"`,
				"审核记录",
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `admin-audit-table`},
		},
		{
			name: "matching",
			page: "admin_matching",
			data: PageData{Title: "匹配控制台", ActiveNav: "matching", Data: adminMatchingPageData{
				Rows: []adminMatchingRow{{Activity: activity, Mode: "双开", SignupCount: 3, CheckedInCount: 1}},
			}},
			want: []string{
				`class="admin-page-head"`, "admin-stats-inline", "admin-record-list",
				`href="/admin/matching/group/5"`, `href="/admin/matching/onetoone/5"`,
				"双开时执行会失败",
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-table`},
		},
		{
			name: "matching-group",
			page: "admin_matching_group",
			data: PageData{Title: "分组控制台", ActiveNav: "matching", Data: adminMatchingGroupPageData{
				Activity: &activity,
				Preview:  GroupPreviewView{Round: 2, MalesPerGroup: 3, FemalesPerGroup: 3, ExpectedGroups: 1},
				Roster: matchingRosterView{
					Total:           1,
					Submitted:       1,
					SubmittedList:   []matchingRosterEntryView{{ID: 1, UserID: "U-0007", Name: "张三", Gender: "女性", Attendance: "按时", CheckedIn: true, Submitted: true}},
					UnsubmittedList: []matchingRosterEntryView{},
				},
			}},
			want: []string{
				`class="admin-page-head"`, "admin-stat-grid", `data-matching-console="group"`,
				`data-mode-enabled="true"`, `data-matching-action="run"`, `data-matching-action="reset"`,
				`name="preview_round"`, `name="male_per_group"`, `name="female_per_group"`,
				`data-matching-preview-form`, `data-roster-filter`, `data-roster-entry`,
				`data-matching-status`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`},
		},
		{
			name: "matching-onetoone",
			page: "admin_matching_onetoone",
			data: PageData{Title: "一对一控制台", ActiveNav: "matching", Data: adminMatchingOnetoonePageData{
				Activity:    &activity,
				FemaleChats: 2,
				Preview:     onetoonePreviewView{Round: 2},
			}},
			want: []string{
				`class="admin-page-head"`, "admin-stat-grid", `data-matching-console="onetoone"`,
				`name="preview_round"`, `name="female_chats"`,
				`data-matching-action="run"`, `data-matching-action="reset"`, `data-matching-status`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`},
		},
		{
			name: "points",
			page: "admin_points",
			data: PageData{Title: "麦穗管理", ActiveNav: "points", Data: adminPointsPageData{
				Tab:  "users",
				Tabs: adminPointsTabs(),
				Users: []adminPointUserView{{
					AdminPointUser: db.AdminPointUser{ID: 7, Name: "张三", Balance: 20, AccountStatus: "active", LoginStatus: "active"},
					StatusLabel:    "已通过",
				}},
				Selected: &adminPointUserView{
					AdminPointUser: db.AdminPointUser{ID: 7, Name: "张三", Balance: 20},
					StatusLabel:    "已通过",
				},
				AdjustKey: "adjust-key",
			}},
			want: []string{
				`class="admin-page-head"`, "admin-points-tabs", "admin-record-list",
				`name="delta"`, `name="reason"`, `name="op_key"`, `name="user_id"`,
				`action="/admin/points/adjust"`, `href="/admin/points?tab=users&amp;user_id=7"`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-table`},
		},
		{
			name: "reports",
			page: "admin_reports",
			data: PageData{Title: "举报处理", ActiveNav: "reports", Data: adminReportsPageData{
				Mode:         "reports",
				StatusFilter: "pending",
				Statuses:     reportStatusOptions(),
				Reports: func() []db.Report {
					messageID := int64(9)
					return []db.Report{{
						ID: 3, ReporterName: "李四", TargetName: "王五", Reason: "不礼貌",
						Status: "待处理", TargetMessageID: &messageID, MessageContent: "示例留言", CreatedAt: now,
					}}
				}(),
			}},
			want: []string{
				`class="admin-page-head"`, "admin-filter-chips", `name="note"`,
				`name="action" value="delete"`, `name="action" value="dismiss"`,
				`action="/admin/reports/3/handle"`, "admin-record-list",
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-tabs`},
		},
		{
			name: "suggestions",
			page: "admin_reports",
			data: PageData{Title: "意见反馈", ActiveNav: "suggestions", Data: adminReportsPageData{
				Mode:         "suggestions",
				StatusFilter: "pending",
				Statuses: []adminStatusOption{
					{Value: "pending", Label: "待处理"}, {Value: "handled", Label: "已处理"},
					{Value: "ignored", Label: "已忽略"}, {Value: "all", Label: "全部"},
				},
				Suggestions: []db.AdminSuggestion{{
					ID: 4, AuthorName: "赵六", Type: "功能建议", Content: "希望有照片墙", Status: "待处理", CreatedAt: now,
				}},
			}},
			want: []string{
				`class="admin-page-head"`, "意见反馈", `name="action" value="handle"`,
				`name="action" value="ignore"`, `action="/admin/suggestions/4/handle"`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`},
		},
		{
			name: "roles",
			page: "admin_roles",
			data: PageData{Title: "角色与权限", ActiveNav: "roles", Data: adminRolesPageData{
				Roles: []db.AdminRole{{
					Code: "super_admin", Name: "最高管理员", UserCount: 1,
					Permissions: []string{"admin.manage", "user.review"},
				}},
				Assignments: []db.AdminAssignment{{UserID: 7, Name: "张三", Phone: "13800000007", Roles: []string{"super_admin"}}},
				SuperAdmin:  true,
			}},
			want: []string{
				`class="admin-page-head"`, "admin-role-grid", "admin-role-card", "admin-perm-list",
				`action="/admin/roles/assign"`, `name="user_ref"`, `name="role"`, `name="action"`,
				"admin-record-list",
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-table`},
		},
		{
			name: "audit",
			page: "admin_audit",
			data: PageData{Title: "审计日志", ActiveNav: "audit", Data: adminAuditPageData{
				Total: 120, Page: 1, PageCount: 2, ExportURL: "/admin/audit.csv?actor=x",
				Filter: adminAuditFilterForm{},
				Rows: []adminAuditRow{{
					ID: 1, CreatedAt: "2026-10-24 10:00:00", Action: "admin.user.approve",
					ActorName: "林小满", ActorLegacyID: "U-0006", ActorURL: "/admin/users/1",
					TargetType: "user", TargetID: "U-0007", TargetURL: "/admin/users/7",
					IP: "203.0.113.10", Metadata: `{"note":"ok"}`,
				}},
				NextURL: "/admin/audit?page=2",
			}},
			want: []string{
				`class="admin-page-head"`, `href="/admin/audit.csv?actor=x"`,
				`name="actor"`, `name="action"`, `name="target_type"`, `name="target_id"`,
				`name="from"`, `name="to"`, "admin-record-list", "admin-audit-detail",
				`href="/admin/audit?page=2"`,
			},
			notWant: []string{`class="card"`, `class="eyebrow"`, `class="admin-table`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := renderAdminPage(t, tc.page, tc.data)
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Fatalf("%s missing %q", tc.name, want)
				}
			}
			for _, unwanted := range tc.notWant {
				if strings.Contains(html, unwanted) {
					t.Fatalf("%s still renders legacy markup %q", tc.name, unwanted)
				}
			}
		})
	}
}

// 总览顶栏不再用裸文本显示单字姓名（staging 管理员姓名是「一」，看起来像空按钮）。
func TestAdminTopbarIdentityChip(t *testing.T) {
	html := renderAdminPage(t, "admin", PageData{Title: "管理台", ActiveNav: "admin",
		Data: adminHomeData{Roles: []string{"super_admin"}, SuperAdmin: true}})
	for _, want := range []string{
		`class="admin-identity"`, `aria-label="当前管理员：管理员"`,
		`class="admin-identity-role">管理员`, `class="admin-identity-name">管理员`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("topbar identity missing %q", want)
		}
	}
	if strings.Contains(html, `class="admin-topbar-user"`) {
		t.Fatal("topbar still renders the bare user text")
	}
}

// 麦穗各 tab 的表单/字段契约在视觉重构后必须保持不变。
func TestAdminPointsTabsKeepFormContract(t *testing.T) {
	now := time.Now()
	redemption := adminRedemptionView{
		AdminRedemptionRow: db.AdminRedemptionRow{
			ID: 9, UserID: 7, UserName: "张三", Item: db.RedeemItemWish, ItemLabel: "心愿名额",
			Cost: 30, Status: db.RedeemStatusActive, StatusLabel: "生效中", CreatedAt: now,
			ActivityTitle: "秋日徒步", TargetUserID: 8, TargetName: "李四", Note: "希望认识",
		},
		CanRefund: true,
		CanMark:   true,
		MarkOptions: []adminStatusOption{
			{Value: db.RedeemStatusArranged, Label: "已安排"},
			{Value: db.RedeemStatusDone, Label: "已完成"},
		},
	}
	cases := []struct {
		name string
		data adminPointsPageData
		want []string
	}{
		{
			name: "ledger",
			data: adminPointsPageData{
				Tab: "ledger", LedgerKinds: adminPointsLedgerKinds(),
				Ledger: []db.AdminLedgerRow{{
					ID: 1, UserID: 7, UserName: "张三", KindLabel: "管理员调整",
					Delta: 20, BalanceAfter: 40, Reason: "活动奖励", OperatorName: "林小满", CreatedAt: now,
				}},
				LedgerPage: 2, LedgerPrevURL: "/admin/points?tab=ledger&page=1", LedgerNextURL: "/admin/points?tab=ledger&page=3",
			},
			want: []string{
				`name="q"`, `name="user_id"`, `name="kind"`, "活动奖励", "林小满",
				`href="/admin/points?tab=ledger&amp;page=3"`, "第 2 页",
			},
		},
		{
			name: "invites",
			data: adminPointsPageData{
				Tab: "invites", InviteStatuses: adminPointsInviteStatusOptions(),
				Invites: []db.AdminInviteRow{{
					ID: 1, InviterID: 7, InviterName: "张三", InviterLegacyID: "U-0007",
					PhoneMasked: "137****1001", StatusLabel: "待确认", Reward: 20, CreatedAt: now,
				}},
				InviteCounts: db.AdminInviteCounts{Total: 1, Awaiting: 1},
			},
			want: []string{
				"邀请记录", `name="status"`, "137****1001", "待确认",
				`href="/admin/users/7"`,
			},
		},
		{
			name: "redemptions",
			data: adminPointsPageData{
				Tab: "redemptions", ItemOptions: adminPointsItemOptions(), StatusOptions: adminPointsStatusOptions(),
				Redemptions: []adminRedemptionView{redemption}, Page: 1,
			},
			want: []string{
				"兑换单", `name="item"`, `name="status"`, "心愿名额", "秋日徒步",
				`action="/admin/points/redemptions/9/refund"`, `action="/admin/points/redemptions/9/mark"`,
				`name="reason"`, `name="note"`, "退穗",
			},
		},
		{
			name: "wishes",
			data: adminPointsPageData{
				Tab: "wishes", StatusOptions: adminPointsStatusOptions(),
				Redemptions: []adminRedemptionView{redemption},
			},
			want: []string{
				"心愿单", `name="status"`, "心愿名额",
				`action="/admin/points/redemptions/9/mark"`,
			},
		},
		{
			name: "recommendations",
			data: adminPointsPageData{
				Tab: "recommendations", StatusOptions: adminPointsStatusOptions(),
				Redemptions: []adminRedemptionView{redemption},
			},
			want: []string{
				"推荐单", `name="status"`, `action="/admin/points/redemptions/9/refund"`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := renderAdminPage(t, "admin_points", PageData{
				Title: "麦穗管理", ActiveNav: "points", Data: tc.data,
			})
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Fatalf("%s missing %q", tc.name, want)
				}
			}
		})
	}
}
