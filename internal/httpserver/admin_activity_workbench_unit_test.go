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

func TestAdminBroadcastPreviewToken(t *testing.T) {
	secret := strings.Repeat("s", 32)
	claims := adminBroadcastPreviewClaims{
		Version:          adminBroadcastPreviewVersion,
		ActivityID:       5,
		Key:              "123e4567-e89b-42d3-a456-426614174000",
		IncludeSignups:   true,
		IncludeObservers: false,
		Count:            2,
		Fingerprint:      adminBroadcastFingerprint([]int64{7, 9}),
		IssuedAt:         time.Now().Unix(),
	}
	token, err := signAdminBroadcastPreview(secret, claims)
	if err != nil {
		t.Fatalf("sign preview: %v", err)
	}
	got, err := verifyAdminBroadcastPreview(secret, token, time.Now())
	if err != nil {
		t.Fatalf("verify preview: %v", err)
	}
	if got.ActivityID != claims.ActivityID || got.Key != claims.Key ||
		got.IncludeSignups != claims.IncludeSignups || got.IncludeObservers != claims.IncludeObservers ||
		got.Count != claims.Count || got.Fingerprint != claims.Fingerprint {
		t.Fatalf("verified claims = %+v, want %+v", got, claims)
	}
	if _, err := verifyAdminBroadcastPreview(strings.Repeat("t", 32), token, time.Now()); err == nil {
		t.Fatal("token verified with a different secret")
	}
	tampered := "A" + token
	if _, err := verifyAdminBroadcastPreview(secret, tampered, time.Now()); err == nil {
		t.Fatal("tampered token verified")
	}
	expired := claims
	expired.IssuedAt = time.Now().Add(-adminBroadcastPreviewTTL - time.Minute).Unix()
	expiredToken, err := signAdminBroadcastPreview(secret, expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAdminBroadcastPreview(secret, expiredToken, time.Now()); err == nil {
		t.Fatal("expired token verified")
	}
	if adminBroadcastFingerprint([]int64{7, 9}) == adminBroadcastFingerprint([]int64{7, 10}) {
		t.Fatal("fingerprint did not change with the recipient set")
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
	if got := canonicalNav("matching"); got != "matching" {
		t.Fatalf("canonicalNav(matching) = %q", got)
	}
	cases := map[string]string{
		"admin_activities":        "activities",
		"admin_activity_edit":     "activities",
		"admin_activity_detail":   "activities",
		"admin_matching":          "matching",
		"admin_matching_group":    "matching",
		"admin_matching_onetoone": "matching",
		"admin_reports":           "reports",
		"admin_roles":             "roles",
	}
	for page, want := range cases {
		if got := navForPage(page); got != want {
			t.Fatalf("navForPage(%q) = %q, want %q", page, got, want)
		}
	}
}

func TestAdminActivityStatusesCoverDatabaseStatuses(t *testing.T) {
	for _, status := range []string{
		db.ActivityStatusOpen, db.ActivityStatusNotStarted, db.ActivityStatusFull,
		db.ActivityStatusFinished, db.ActivityStatusCancelled, adminActivityStatusDraft,
	} {
		if !adminActivityStatusKnown(status) {
			t.Fatalf("status %q missing from adminActivityStatuses", status)
		}
	}
	if adminActivityStatusKnown("历史状态") {
		t.Fatal("unknown status should not be treated as known")
	}
}

func TestFilterAdminRoster(t *testing.T) {
	roster := []db.AdminActivityRosterEntry{
		{UserID: 1, Name: "张三", Nickname: "小张", LegacyUserID: "U-0001", PhoneMasked: "138****1111", Attendance: db.AttendanceOnTime},
		{UserID: 2, Name: "李四", Nickname: "阿四", LegacyUserID: "U-0002", PhoneMasked: "139****2222", Attendance: db.AttendanceLate},
		{UserID: 3, Name: "王五", LegacyUserID: "U-0003", PhoneMasked: "137****3333", Attendance: db.AttendanceAbsent},
		{UserID: 4, Name: "赵六", LegacyUserID: "U-0004", PhoneMasked: "136****4444"},
	}
	cases := []struct {
		name       string
		query      string
		attendance string
		wantIDs    []int64
	}{
		{"all", "", "", []int64{1, 2, 3, 4}},
		{"name", "张", "", []int64{1}},
		{"nickname", "阿四", "", []int64{2}},
		{"legacy id", "u-0004", "", []int64{4}},
		{"masked phone", "3333", "", []int64{3}},
		{"checked", "", "checked", []int64{1, 2}},
		{"unchecked", "", "unchecked", []int64{4}},
		{"late", "", "late", []int64{2}},
		{"absent", "", "absent", []int64{3}},
		{"query and filter", "张", "checked", []int64{1}},
		{"no match", "不存在", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAdminRoster(roster, tc.query, tc.attendance)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("filter(%q, %q) len = %d, want %d", tc.query, tc.attendance, len(got), len(tc.wantIDs))
			}
			for i, want := range tc.wantIDs {
				if got[i].UserID != want {
					t.Fatalf("filter(%q, %q)[%d] = %d, want %d", tc.query, tc.attendance, i, got[i].UserID, want)
				}
			}
		})
	}
}

func TestAdminActivityRosterPaginationURLs(t *testing.T) {
	href := adminActivityRosterHref(5, "张三", "checked", 2)
	want := "/admin/activities/5?attendance=checked&page=2&q=%E5%BC%A0%E4%B8%89#roster"
	if href != want {
		t.Fatalf("roster href = %q, want %q", href, want)
	}
	if got := adminActivityRosterHref(5, "", "", 1); got != "/admin/activities/5#roster" {
		t.Fatalf("clear href = %q", got)
	}
	member := adminActivityMemberHref(5, "签到状态已更新。", "张三", "unchecked", 2, 7)
	for _, want := range []string{"#member-7", "page=2", "attendance=unchecked", "q=%E5%BC%A0%E4%B8%89", "updated=7"} {
		if !strings.Contains(member, want) {
			t.Fatalf("member href = %q, missing %q", member, want)
		}
	}
	noticeHref := adminActivityRosterNoticeHref(5, "已清除筛选。", "张三", "", 1)
	if !strings.HasSuffix(noticeHref, "#roster") || strings.Contains(noticeHref, "member-") ||
		!strings.Contains(noticeHref, "q=%E5%BC%A0%E4%B8%89") || strings.Contains(noticeHref, "attendance=") {
		t.Fatalf("notice href = %q", noticeHref)
	}
	if got := normalizeAdminRosterAttendance("all"); got != "" {
		t.Fatalf("normalize(all) = %q", got)
	}
	if got := normalizeAdminRosterAttendance("bogus"); got != "" {
		t.Fatalf("normalize(bogus) = %q", got)
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
		GroupEnabled: true, PosterMediaID: &posterID, StartAt: &now,
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
		RosterAllCount:        1,
		RosterFilteredCount:   1,
		RosterPage:            1,
		RosterPageCount:       1,
		RosterClearURL:        "/admin/activities/5#roster",
		RosterReturnTo:        "/admin/activities/5#roster",
		SignupAudienceCount:   1,
		ObserverAudienceCount: 1,
		AudienceTotal:         2,
		BroadcastKey:          posterID,
		BroadcastPreview:      true,
		BroadcastPreviewToken: "signed-preview-token",
		BroadcastPreviewCount: 2,
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
		"站内广播", "快速设置", "分组 / 一对一", "匹配总览",
		`href="/admin/matching/group/5"`,
		`enctype="multipart/form-data"`, `name="broadcast_key"`,
		`id="member-7"`, `type="text" name="signin_code"`, `formnovalidate`,
		`观察员（全站）`, `href="#broadcast"`, `action="/admin/activities/5#roster"`,
		`action="/admin/activities/5/broadcast#broadcast"`,
		`<option value="已满员"`, `name="broadcast_action" value="preview"`,
		`name="broadcast_preview_token" value="signed-preview-token"`,
		`name="attendance_state" value="按时"`, `name="attendance" value=""`,
		`name="q" value=""`, `name="page" value="1"`, `name="return_to" value="/admin/activities/5#roster"`,
		`确认发送？`, `回到顶部`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("detail template missing %q", want)
		}
	}
	if strings.Contains(html, "13812341234") {
		t.Fatal("detail template leaked the raw phone")
	}

	// An unknown stored status must render an explicit keep-current option
	// instead of silently falling back to the first select option.
	unknown := activity
	unknown.Status = "历史状态"
	unknownDetail := detail
	unknownDetail.Activity = &unknown
	unknownDetail.StatusKeep = true
	unknownDetail.StatusUnknown = "历史状态"
	buf.Reset()
	if err := tpl.Render(&buf, "admin_activity_detail", PageData{
		Title: "活动工作台", User: user, CSRFToken: "csrf-value", Data: unknownDetail,
		ActiveNav: "activities", MediaMaxMB: 10,
	}); err != nil {
		t.Fatalf("render unknown status: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, `<option value="历史状态" selected>保持目前状态（历史状态）</option>`) {
		t.Fatalf("unknown status lost its keep-current option")
	}

	// Edit page: text inputs carry a type, datetime-local fields exist, and the
	// current status is selected from the complete list.
	editActivity := activity
	editActivity.Status = db.ActivityStatusFull
	buf.Reset()
	if err := tpl.Render(&buf, "admin_activity_edit", PageData{
		Title: "活动编辑", User: user, CSRFToken: "csrf-value",
		Data:      adminActivityEditPageData{Activity: &editActivity, Statuses: adminActivityStatuses},
		ActiveNav: "activities",
	}); err != nil {
		t.Fatalf("render edit: %v", err)
	}
	editHTML := buf.String()
	for _, want := range []string{
		`type="text" name="name"`, `type="text" name="fee"`,
		`type="text" name="signin_code"`, `type="datetime-local" name="publish_at"`,
		`<option value="已满员" selected>已满员</option>`,
	} {
		if !strings.Contains(editHTML, want) {
			t.Fatalf("edit template missing %q", want)
		}
	}

	// An empty stored status is treated like an unknown one: the form offers
	// keep-current instead of defaulting to 报名中.
	empty := activity
	empty.Status = ""
	buf.Reset()
	if err := tpl.Render(&buf, "admin_activity_edit", PageData{
		Title: "活动编辑", User: user, CSRFToken: "csrf-value",
		Data: adminActivityEditPageData{
			Activity: &empty, Statuses: adminActivityStatuses, StatusKeep: true,
		},
		ActiveNav: "activities",
	}); err != nil {
		t.Fatalf("render empty status edit: %v", err)
	}
	emptyHTML := buf.String()
	if !strings.Contains(emptyHTML, `<option value="" selected>保持目前状态（未设置）</option>`) {
		t.Fatalf("empty status lost keep-current option")
	}
	if strings.Contains(emptyHTML, `<option value="报名中" selected>`) {
		t.Fatalf("empty status silently defaulted to 报名中")
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
