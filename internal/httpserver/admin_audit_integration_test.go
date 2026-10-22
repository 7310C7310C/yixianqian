package httpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestIntegrationAdminAuditHTTP(t *testing.T) {
	env := setupActivitiesHTTPEnv(t, nil)
	marker := "AUDITHTTP-" + randomHTTPDigits(t, 8)

	reviewer, reviewerClient := env.createUser(t, "U-AUDITREV", "审计审核员", "active", "user")
	manager, managerClient := env.createUser(t, "U-AUDITMGR", "活动管理员", "active", "user")
	plain, plainClient := env.createUser(t, "U-AUDITPLAIN", "普通用户", "active", "user")
	adminUser, adminClient := env.createUser(t, "U-AUDITADM", "审计管理员", "active", "user")
	actor, _ := env.createUser(t, "U-AUDITACTOR", "审计操作人"+marker, "active", "user")
	cleanupHTTPUsers(t, env, reviewer.ID, manager.ID, plain.ID, adminUser.ID, actor.ID)
	t.Cleanup(func() {
		_, _ = env.pool.Exec(env.ctx,
			`DELETE FROM audit_log WHERE action LIKE $1`, marker+"%")
	})

	assignRole := func(user db.User, role string) {
		t.Helper()
		if changed, err := env.database.AssignAdminRole(env.ctx, user.ID, role, true); err != nil || !changed {
			t.Fatalf("assign %s to %d: changed=%v err=%v", role, user.ID, changed, err)
		}
	}
	assignRole(reviewer, "reviewer")
	assignRole(manager, "activity_manager")
	assignRole(adminUser, "admin")

	secretValue := "S3CRET-" + marker
	tokenValue := "TOKEN-" + marker
	objectKey := "media/1/" + marker + ".jpg"
	csrfValue := "CSRF-" + marker
	noteOne := "first-" + marker
	noteTwo := "second-" + marker
	noteThree := "third-" + marker

	insertAudit := func(actorID int64, action, targetType, targetID string,
		meta map[string]any, createdAt time.Time) int64 {
		t.Helper()
		raw, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := env.pool.QueryRow(env.ctx,
			`INSERT INTO audit_log
			   (actor_user_id, action, target_type, target_id, ip, metadata, created_at)
			 VALUES ($1, $2, $3, $4, '203.0.113.9'::inet, $5::text::jsonb, $6)
			 RETURNING id`,
			actorID, action, targetType, targetID, string(raw), createdAt).Scan(&id); err != nil {
			t.Fatalf("insert audit fixture: %v", err)
		}
		return id
	}

	now := time.Now().UTC()
	targetUserID := strconv.FormatInt(reviewer.ID, 10)
	firstID := insertAudit(actor.ID, marker+".user.approve", "user", targetUserID,
		map[string]any{
			"password":   secretValue,
			"token":      tokenValue,
			"object_key": objectKey,
			"note":       noteOne,
		}, now.Add(-3*time.Second))
	secondID := insertAudit(reviewer.ID, marker+".report.handle", "report", "R-"+marker,
		map[string]any{"note": noteTwo}, now.Add(-2*time.Second))
	thirdID := insertAudit(manager.ID, marker+".session.login", "session", "",
		map[string]any{"csrf": csrfValue, "note": noteThree}, now.Add(-time.Second))
	if firstID == 0 || secondID == 0 || thirdID == 0 {
		t.Fatal("audit fixtures missing ids")
	}

	// Permission gate: audit.view is required for both the page and the CSV.
	for _, path := range []string{"/admin/audit", "/admin/audit.csv"} {
		resp := managerClient.get(path)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("activity_manager %s status = %d", path, resp.StatusCode)
		}
		resp = plainClient.get(path)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("plain user %s status = %d", path, resp.StatusCode)
		}
	}

	resp := reviewerClient.get("/admin/audit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reviewer audit page status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if page := readBody(t, resp); !strings.Contains(page, "审计日志") {
		t.Fatalf("audit page missing heading: %s", page)
	}

	// Use the fuzzy action filter so all three fixtures stay visible even when
	// earlier suite runs left more than one page of audit rows in the test DB.
	resp = reviewerClient.get("/admin/audit?action=" + url.QueryEscape(marker))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filtered audit page status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{
		"审计日志", marker + ".user.approve", noteOne, noteTwo, noteThree,
		`href="/admin/users/` + targetUserID + `"`,
		`href="/admin/users/` + strconv.FormatInt(actor.ID, 10) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("audit page missing %q", want)
		}
	}
	for _, forbidden := range []string{secretValue, tokenValue, objectKey, csrfValue} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit page leaked sensitive value %q", forbidden)
		}
	}

	// Each filter narrows the list to the matching fixture row.
	filterCases := []struct {
		query   string
		want    string
		notWant []string
	}{
		{
			query:   "action=" + url.QueryEscape(marker+".user.approve"),
			want:    noteOne,
			notWant: []string{noteTwo, noteThree},
		},
		{
			query:   "actor=" + url.QueryEscape(actor.Name),
			want:    noteOne,
			notWant: []string{noteTwo, noteThree},
		},
		{
			query: "target_type=report&target_id=" + url.QueryEscape("R-"+marker),
			want:  noteTwo,
			notWant: []string{
				noteOne, noteThree,
			},
		},
	}
	for _, tc := range filterCases {
		resp := reviewerClient.get("/admin/audit?" + tc.query)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("filter %s status = %d", tc.query, resp.StatusCode)
		}
		filtered := readBody(t, resp)
		if !strings.Contains(filtered, tc.want) {
			t.Fatalf("filter %s missing %q", tc.query, tc.want)
		}
		for _, unwanted := range tc.notWant {
			if strings.Contains(filtered, unwanted) {
				t.Fatalf("filter %s unexpectedly contains %q", tc.query, unwanted)
			}
		}
	}

	// Invalid dates are ignored with a notice instead of a 500.
	resp = reviewerClient.get("/admin/audit?from=not-a-date")
	invalidBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(invalidBody, "开始日期格式不正确") {
		t.Fatalf("invalid date status=%d body=%s", resp.StatusCode, invalidBody)
	}

	// CSV export: headers, filter support and sanitised metadata.
	resp = reviewerClient.get("/admin/audit.csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("csv content-type = %q", got)
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.Contains(disposition, `attachment; filename="audit-`) ||
		!strings.HasSuffix(disposition, `.csv"`) {
		t.Fatalf("csv content-disposition = %q", disposition)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("csv cache-control = %q", got)
	}
	csvBody := readBody(t, resp)
	csvHeader := "id,created_at,actor_user_id,actor_legacy_id,actor_name,action,target_type,target_id,ip,metadata"
	if !strings.Contains(csvBody, csvHeader) {
		t.Fatalf("csv header missing: %s", csvBody)
	}
	for _, want := range []string{noteOne, noteTwo, noteThree, marker + ".user.approve"} {
		if !strings.Contains(csvBody, want) {
			t.Fatalf("csv missing %q", want)
		}
	}
	for _, forbidden := range []string{secretValue, tokenValue, objectKey, csrfValue} {
		if strings.Contains(csvBody, forbidden) {
			t.Fatalf("csv leaked sensitive value %q", forbidden)
		}
	}

	resp = reviewerClient.get("/admin/audit.csv?action=" + url.QueryEscape(marker+".user.approve"))
	filteredCSV := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(filteredCSV, noteOne) ||
		strings.Contains(filteredCSV, noteTwo) || strings.Contains(filteredCSV, noteThree) {
		t.Fatalf("filtered csv status=%d body=%s", resp.StatusCode, filteredCSV)
	}

	// Navigation: reviewer sees audit but not points; admin sees both, in order.
	resp = reviewerClient.get("/admin")
	reviewerHome := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(reviewerHome, `href="/admin/audit"`) {
		t.Fatalf("reviewer nav missing audit: status=%d", resp.StatusCode)
	}
	if strings.Contains(reviewerHome, `href="/admin/points"`) {
		t.Fatal("reviewer nav should not show points without points.adjust")
	}

	resp = adminClient.get("/admin")
	adminHome := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin home status = %d", resp.StatusCode)
	}
	for _, want := range []string{`href="/admin/points"`, `href="/admin/audit"`} {
		if !strings.Contains(adminHome, want) {
			t.Fatalf("admin nav missing %q", want)
		}
	}
	navOrder := []string{
		`href="/admin"`, `href="/admin/users"`, `href="/admin/activities"`,
		`href="/admin/matching"`, `href="/admin/points"`, `href="/admin/reports"`,
		`href="/admin/suggestions"`, `href="/admin/roles"`, `href="/admin/audit"`,
	}
	last := -1
	for _, item := range navOrder {
		index := strings.Index(adminHome, item)
		if index < 0 {
			t.Fatalf("admin nav missing %q", item)
		}
		if index <= last {
			t.Fatalf("admin nav out of order at %q", item)
		}
		last = index
	}

	resp = reviewerClient.get("/admin/audit")
	auditPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(auditPage, `href="/admin/audit" aria-current="page"`) {
		t.Fatalf("audit nav not marked current: status=%d", resp.StatusCode)
	}
}

func TestAdminAuditMetadataSanitizer(t *testing.T) {
	raw := map[string]any{
		"note":     "keep-me",
		"Password": "plain-password",
		"nested": map[string]any{
			"access_TOKEN": "raw-token",
			"Object_Key":   "media/1/secret.jpg",
			"items": []any{
				map[string]any{"csrf": "raw-csrf", "value": "visible"},
			},
		},
	}
	cleaned := adminAuditMetadataJSON(raw)
	for _, forbidden := range []string{
		"plain-password", "raw-token", "media/1/secret.jpg", "raw-csrf",
	} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("sanitised metadata leaked %q: %s", forbidden, cleaned)
		}
	}
	if !strings.Contains(cleaned, "keep-me") || !strings.Contains(cleaned, "visible") {
		t.Fatalf("sanitised metadata dropped safe values: %s", cleaned)
	}
	if !strings.Contains(cleaned, `"[redacted]"`) {
		t.Fatalf("sanitised metadata missing redaction marker: %s", cleaned)
	}

	long := map[string]any{"note": strings.Repeat("长", 400)}
	if summary := adminAuditMetadataSummary(long); len([]rune(summary)) > adminAuditMetaRunes+1 {
		t.Fatalf("metadata summary not truncated: %d runes", len([]rune(summary)))
	}
}
