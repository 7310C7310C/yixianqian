package httpserver

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestValidBroadcastKey(t *testing.T) {
	valid := []string{
		"123e4567-e89b-42d3-a456-426614174000",
		"ABCDEF01-2345-4678-9ABC-DEF012345678",
	}
	for _, key := range valid {
		if !validBroadcastKey(key) {
			t.Fatalf("validBroadcastKey(%q) = false", key)
		}
	}
	invalid := []string{
		"",
		"123e4567e89b42d3a456426614174000",
		"123e4567-e89b-42d3-a456-42661417400",
		"123g4567-e89b-42d3-a456-426614174000",
		"123e4567+e89b+42d3+a456+426614174000",
	}
	for _, key := range invalid {
		if validBroadcastKey(key) {
			t.Fatalf("validBroadcastKey(%q) = true", key)
		}
	}
}

func TestAdminBroadcastRecipients(t *testing.T) {
	audience := db.AdminActivityBroadcastAudience{
		SignupUserIDs:   []int64{1, 2, 3},
		ObserverUserIDs: []int64{3, 4},
	}
	if got := adminBroadcastRecipients(audience, true, true); len(got) != 4 ||
		got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 4 {
		t.Fatalf("union = %v", got)
	}
	if got := adminBroadcastRecipients(audience, true, false); len(got) != 3 {
		t.Fatalf("signups only = %v", got)
	}
	if got := adminBroadcastRecipients(audience, false, true); len(got) != 2 ||
		got[0] != 3 || got[1] != 4 {
		t.Fatalf("observers only = %v", got)
	}
	if got := adminBroadcastRecipients(audience, false, false); len(got) != 0 {
		t.Fatalf("empty selection = %v", got)
	}
}

func TestAdminActivityNavMapping(t *testing.T) {
	if got := canonicalNav("activities"); got != "activities" {
		t.Fatalf("canonicalNav(activities) = %q", got)
	}
	if got := canonicalNav("suggestions"); got != "suggestions" {
		t.Fatalf("canonicalNav(suggestions) = %q", got)
	}
	cases := map[string]string{
		"admin_activities":      "activities",
		"admin_activity_edit":   "activities",
		"admin_activity_detail": "activities",
		"admin_reports":         "reports",
		"admin_roles":           "roles",
	}
	for page, want := range cases {
		if got := navForPage(page); got != want {
			t.Fatalf("navForPage(%q) = %q, want %q", page, got, want)
		}
	}
}

func TestAdminActivityTemplatesRender(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	legacy := "U-0006"
	user := &db.User{ID: 1, LegacyUserID: &legacy, Name: "管理员", Phone: "13800000006"}
	posterID := "123e4567-e89b-42d3-a456-426614174000"
	now := time.Now()
	activity := db.Activity{
		ID: 5, ActivityNo: "A-0005", Name: "模板测试活动", Status: "报名中",
		MaxSignup: 20, CurrentSignup: 1, SigninEnabled: true, SigninCode: "0123",
		PosterMediaID: &posterID, StartAt: &now,
	}
	roster := []db.AdminActivityRosterEntry{{
		UserID: 7, LegacyUserID: "U-0007", Name: "张三", Nickname: "小张",
		Gender: "女", PhoneMasked: "138****1234", SignupAt: now,
		Attendance: db.AttendanceOnTime,
	}}

	detail := adminActivityDetailPageData{
		Activity:              &activity,
		Roster:                roster,
		Stats:                 db.AdminActivitySigninStats{SignupCount: 1, CheckedInCount: 1},
		Statuses:              adminActivityStatuses,
		SignupAudienceCount:   1,
		ObserverAudienceCount: 1,
		AudienceTotal:         2,
		BroadcastKey:          posterID,
	}
	var buf bytes.Buffer
	if err := tpl.Render(&buf, "admin_activity_detail", PageData{
		Title: "活动工作台", User: user, CSRFToken: "csrf-value", Data: detail,
		ActiveNav: "activities", MediaMaxMB: 10,
	}); err != nil {
		t.Fatalf("render detail: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		"活动工作台", "报名/签到名单", "张三", "138****1234", "按时签到",
		"站内广播", "快速设置", "匹配控制台尚未在本期交付",
		`enctype="multipart/form-data"`, `name="broadcast_key"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("detail template missing %q", want)
		}
	}
	if strings.Contains(html, "13812341234") {
		t.Fatal("detail template leaked the raw phone")
	}

	buf.Reset()
	if err := tpl.Render(&buf, "admin_activities", PageData{
		Title: "活动管理", User: user, CSRFToken: "csrf-value",
		Data:      adminActivitiesPageData{Activities: []adminActivityRow{{Activity: activity, SignupCount: 1, CheckedInCount: 1}}},
		ActiveNav: "activities", ActiveSubnav: "list",
	}); err != nil {
		t.Fatalf("render list: %v", err)
	}
	listHTML := buf.String()
	for _, want := range []string{
		"活动列表", "新建活动", "工作台", "/admin/activities/5/poster?size=thumb",
		"已签到 1", "分组",
	} {
		if !strings.Contains(listHTML, want) {
			t.Fatalf("list template missing %q", want)
		}
	}
}
