package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminPosterPart struct {
	Filename    string
	ContentType string
	Data        []byte
}

func adminActivityFormValues(csrf, name string) url.Values {
	return url.Values{
		"csrf_token":       {csrf},
		"name":             {name},
		"description":      {"海报表单测试描述"},
		"location":         {"测试地点"},
		"condition":        {"测试条件"},
		"max_signup":       {"20"},
		"fee":              {"免费"},
		"food":             {"自理"},
		"status":           {db.ActivityStatusOpen},
		"publish_at":       {""},
		"start_at":         {""},
		"end_at":           {""},
		"male_per_group":   {"0"},
		"female_per_group": {"0"},
	}
}

func adminActivityUpdateValues(csrf string, activity db.Activity) url.Values {
	values := url.Values{
		"csrf_token":       {csrf},
		"name":             {activity.Name},
		"description":      {activity.Description},
		"location":         {activity.Location},
		"condition":        {activity.Condition},
		"max_signup":       {strconv.Itoa(activity.MaxSignup)},
		"fee":              {activity.Fee},
		"food":             {activity.Food},
		"status":           {activity.Status},
		"publish_at":       {""},
		"start_at":         {""},
		"end_at":           {""},
		"male_per_group":   {strconv.Itoa(activity.MalePerGroup)},
		"female_per_group": {strconv.Itoa(activity.FemalePerGroup)},
	}
	for key, at := range map[string]*time.Time{
		"publish_at": activity.PublishAt,
		"start_at":   activity.StartAt,
		"end_at":     activity.EndAt,
	} {
		if at != nil {
			values.Set(key, at.Format("2006-01-02T15:04"))
		}
	}
	if activity.GroupEnabled {
		values.Set("group_enabled", "1")
	}
	if activity.OnetooneEnabled {
		values.Set("onetoone_enabled", "1")
	}
	if activity.SigninEnabled {
		values.Set("signin_enabled", "1")
	}
	if activity.SigninCode != "" {
		values.Set("signin_code", activity.SigninCode)
	}
	return values
}

func postAdminActivityMultipart(t *testing.T, client *wsClient, path string,
	fields url.Values, poster *adminPosterPart) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if poster != nil {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="poster"; filename="%s"`, poster.Filename))
		header.Set("Content-Type", poster.ContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(poster.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, client.base+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return client.do(req)
}

func posterAuditMeta(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	actorID int64, activityNo string) (string, map[string]any) {
	t.Helper()
	var raw string
	if err := pool.QueryRow(ctx,
		`SELECT metadata::text FROM audit_log
		 WHERE action = 'admin.activity.poster' AND actor_user_id = $1 AND target_id = $2
		 ORDER BY id DESC LIMIT 1`, actorID, activityNo).Scan(&raw); err != nil {
		t.Fatalf("load poster audit: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("decode poster audit metadata %q: %v", raw, err)
	}
	return raw, meta
}

func posterAuditCount(ctx context.Context, pool *pgxpool.Pool, actorID int64, activityNo string) (int, error) {
	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE action = 'admin.activity.poster' AND actor_user_id = $1 AND target_id = $2`,
		actorID, activityNo).Scan(&count)
	return count, err
}

func registerAdminPosterCleanup(t *testing.T, pool *pgxpool.Pool, activityIDs *[]int64, userIDs []int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if len(*activityIDs) > 0 {
			_, _ = pool.Exec(ctx, `DELETE FROM activities WHERE id = ANY($1)`, *activityIDs)
		}
		if len(userIDs) > 0 {
			_, _ = pool.Exec(ctx, `DELETE FROM media WHERE user_id = ANY($1)`, userIDs)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, userIDs)
		}
	})
}

func TestIntegrationAdminActivityCreateWithPoster(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "海报创建管理员")
	activityIDs := []int64{}
	registerAdminPosterCleanup(t, pool, &activityIDs, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	csrf := client.pageCSRF("/admin/activities/new")

	name := "海报创建测试 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, name),
		&adminPosterPart{Filename: "活动海报.png", ContentType: "image/png", Data: testPNG(t)})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create with poster status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location := resp.Header.Get("Location")
	if !strings.HasSuffix(location, "#poster") || !strings.Contains(location, "notice=") {
		t.Fatalf("create with poster redirect = %q", location)
	}
	activityID := adminActivityIDFromLocation(t, location)
	activityIDs = append(activityIDs, activityID)

	reloaded, err := database.GetActivity(ctx, strconv.FormatInt(activityID, 10))
	if err != nil {
		t.Fatalf("reload activity: %v", err)
	}
	if reloaded.PosterMediaID == nil {
		t.Fatalf("created activity has no poster: %+v", reloaded)
	}
	mediaID := *reloaded.PosterMediaID

	var ownerID int64
	var originalName, objectKey string
	if err := pool.QueryRow(ctx,
		`SELECT user_id, original_name, object_key FROM media WHERE id = $1`, mediaID).
		Scan(&ownerID, &originalName, &objectKey); err != nil {
		t.Fatalf("load media: %v", err)
	}
	if ownerID != manager.ID {
		t.Fatalf("media owner = %d, want %d", ownerID, manager.ID)
	}
	if originalName != "活动海报.png" {
		t.Fatalf("media original name = %q", originalName)
	}
	if objectKey == "" {
		t.Fatal("media object key is empty")
	}

	rawMeta, meta := posterAuditMeta(t, ctx, pool, manager.ID, reloaded.ActivityNo)
	if strings.Contains(rawMeta, objectKey) {
		t.Fatalf("poster audit leaked the object key: %s", rawMeta)
	}
	if meta["source"] != "create" {
		t.Fatalf("poster audit source = %v", meta["source"])
	}
	if _, ok := meta["media_id"]; !ok {
		t.Fatalf("poster audit missing media_id: %s", rawMeta)
	}
	if len(meta) != 2 {
		t.Fatalf("poster audit metadata has extra fields: %s", rawMeta)
	}

	editPath := "/admin/activities/" + strconv.FormatInt(activityID, 10) + "/edit"
	resp = client.get(editPath)
	editHTML := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit page status = %d body=%s", resp.StatusCode, editHTML)
	}
	thumbURL := "/admin/activities/" + strconv.FormatInt(activityID, 10) + "/poster?size=thumb"
	for _, want := range []string{
		`enctype="multipart/form-data"`, `name="poster"`, `accept="image/jpeg,image/png"`,
		`src="` + thumbURL + `"`, "活动海报.png", "最大 10 MB",
	} {
		if !strings.Contains(editHTML, want) {
			t.Fatalf("edit page missing %q", want)
		}
	}
	if strings.Contains(editHTML, objectKey) {
		t.Fatalf("edit page leaked the object key %q", objectKey)
	}

	posterPath := "/admin/activities/" + strconv.FormatInt(activityID, 10) + "/poster"
	resp = client.get(posterPath)
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("admin poster status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// 海报原图也是媒体串流：顶层 document 打开（新窗口/直接导航）仍返回图片，不做页面 303。
	resp = client.getWithHeaders(posterPath, map[string]string{"Sec-Fetch-Dest": "document"})
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("admin poster document status=%d type=%q",
			resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp = client.get(posterPath + "?size=thumb")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("admin poster thumb status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp = client.get("/activities/" + reloaded.ActivityNo + "/poster")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("public poster status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestIntegrationAdminActivityCreateWithoutPoster(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "无海报创建管理员")
	activityIDs := []int64{}
	registerAdminPosterCleanup(t, pool, &activityIDs, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	csrf := client.pageCSRF("/admin/activities/new")

	name := "无海报创建测试 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, name), nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create without poster status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	location := resp.Header.Get("Location")
	if strings.Contains(location, "#poster") {
		t.Fatalf("create without poster redirect = %q", location)
	}
	activityID := adminActivityIDFromLocation(t, location)
	activityIDs = append(activityIDs, activityID)
	reloaded, err := database.GetActivity(ctx, strconv.FormatInt(activityID, 10))
	if err != nil {
		t.Fatalf("reload activity: %v", err)
	}
	if reloaded.PosterMediaID != nil {
		t.Fatalf("poster unexpectedly set: %v", *reloaded.PosterMediaID)
	}
	if count, err := posterAuditCount(ctx, pool, manager.ID, reloaded.ActivityNo); err != nil || count != 0 {
		t.Fatalf("poster audit count = %d err=%v", count, err)
	}

	// An explicitly empty filename is "no poster", not an upload error.
	emptyName := "空文件名创建测试 " + randomHTTPDigits(t, 8)
	resp = postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, emptyName),
		&adminPosterPart{Filename: "", ContentType: "image/png", Data: nil})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create with empty filename status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	emptyLocation := resp.Header.Get("Location")
	if strings.Contains(emptyLocation, "#poster") {
		t.Fatalf("empty filename redirect = %q", emptyLocation)
	}
	emptyID := adminActivityIDFromLocation(t, emptyLocation)
	activityIDs = append(activityIDs, emptyID)
	emptyActivity, err := database.GetActivity(ctx, strconv.FormatInt(emptyID, 10))
	if err != nil {
		t.Fatalf("reload empty filename activity: %v", err)
	}
	if emptyActivity.PosterMediaID != nil {
		t.Fatalf("empty filename created a poster: %v", *emptyActivity.PosterMediaID)
	}
}

func TestIntegrationAdminActivityUpdatePosterReplace(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "海报替换管理员")
	registerAdminPosterCleanup(t, pool, &[]int64{}, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	activity := createWorkbenchActivity(t, ctx, database, pool, "海报替换测试")
	client := loginMessageClient(t, database, server, manager)
	editPath := "/admin/activities/" + strconv.FormatInt(activity.ID, 10) + "/edit"
	csrf := client.pageCSRF(editPath)
	updatePath := "/admin/activities/" + strconv.FormatInt(activity.ID, 10)

	resp := postAdminActivityMultipart(t, client, updatePath,
		adminActivityUpdateValues(csrf, activity),
		&adminPosterPart{Filename: "第一张.png", ContentType: "image/png", Data: testPNG(t)})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first poster update status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if location := resp.Header.Get("Location"); !strings.HasSuffix(location, "#poster") ||
		!strings.Contains(location, "notice=") {
		t.Fatalf("first poster update redirect = %q", location)
	}
	first, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10))
	if err != nil || first.PosterMediaID == nil {
		t.Fatalf("first poster not linked: %+v err=%v", first, err)
	}
	firstMediaID := *first.PosterMediaID

	// Reload the stored row so the second update posts the persisted fields
	// (including any formatting changes made by the first update).
	resp = postAdminActivityMultipart(t, client, updatePath,
		adminActivityUpdateValues(csrf, first),
		&adminPosterPart{Filename: "第二张.png", ContentType: "image/png", Data: testPNG(t)})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second poster update status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if location := resp.Header.Get("Location"); !strings.HasSuffix(location, "#poster") {
		t.Fatalf("second poster update redirect = %q", location)
	}
	second, err := database.GetActivity(ctx, strconv.FormatInt(activity.ID, 10))
	if err != nil || second.PosterMediaID == nil {
		t.Fatalf("second poster not linked: %+v err=%v", second, err)
	}
	if *second.PosterMediaID == firstMediaID {
		t.Fatalf("poster media id did not change: %s", firstMediaID)
	}
	rawMeta, meta := posterAuditMeta(t, ctx, pool, manager.ID, second.ActivityNo)
	if meta["source"] != "edit" {
		t.Fatalf("poster audit source = %v (%s)", meta["source"], rawMeta)
	}
}

func TestIntegrationAdminActivityPosterRejectsNonImage(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "非图片拒绝管理员")
	registerAdminPosterCleanup(t, pool, &[]int64{}, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	csrf := client.pageCSRF("/admin/activities/new")
	var mediaBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE user_id = $1`, manager.ID).Scan(&mediaBefore); err != nil {
		t.Fatal(err)
	}
	name := "非图片活动 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, name),
		&adminPosterPart{Filename: "伪装.png", ContentType: "image/png", Data: []byte("this is not an image")})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-image status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "只支持 JPEG 与 PNG 图片") || !strings.Contains(body, `name="poster"`) {
		t.Fatalf("non-image response was not the edit form: %s", body)
	}
	var activityCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE name = $1`, name).Scan(&activityCount); err != nil {
		t.Fatal(err)
	}
	if activityCount != 0 {
		t.Fatalf("non-image upload created %d activities", activityCount)
	}
	var mediaAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE user_id = $1`, manager.ID).Scan(&mediaAfter); err != nil {
		t.Fatal(err)
	}
	if mediaAfter != mediaBefore {
		t.Fatalf("media rows changed from %d to %d", mediaBefore, mediaAfter)
	}
}

func TestIntegrationAdminActivityPosterRejectsBadCSRF(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "海报CSRF管理员")
	registerAdminPosterCleanup(t, pool, &[]int64{}, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	name := "CSRF活动 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues("wrong-csrf", name),
		&adminPosterPart{Filename: "海豹.png", ContentType: "image/png", Data: testPNG(t)})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "表单已过期") || !strings.Contains(body, `name="poster"`) {
		t.Fatalf("bad csrf response was not the edit form: %s", body)
	}
	var activityCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE name = $1`, name).Scan(&activityCount); err != nil {
		t.Fatal(err)
	}
	if activityCount != 0 {
		t.Fatalf("bad csrf created %d activities", activityCount)
	}
	var mediaCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE user_id = $1`, manager.ID).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 0 {
		t.Fatalf("bad csrf created %d media rows", mediaCount)
	}
}

func TestIntegrationAdminActivityPosterRejectsOversize(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "海报超限管理员")
	registerAdminPosterCleanup(t, pool, &[]int64{}, []int64{manager.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	csrf := client.pageCSRF("/admin/activities/new")
	oversize := append(testPNG(t), make([]byte, int(testConfig().MediaMaxBytes))...)
	name := "超限活动 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, name),
		&adminPosterPart{Filename: "过大.png", ContentType: "image/png", Data: oversize})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `name="poster"`) || !strings.Contains(body, "文件超过大小上限") {
		t.Fatalf("oversize response was not the edit form: %s", body)
	}
	var activityCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE name = $1`, name).Scan(&activityCount); err != nil {
		t.Fatal(err)
	}
	if activityCount != 0 {
		t.Fatalf("oversize upload created %d activities", activityCount)
	}
	var mediaCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE user_id = $1`, manager.ID).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 0 {
		t.Fatalf("oversize upload created %d media rows", mediaCount)
	}
}

func TestIntegrationAdminActivityPosterAccess(t *testing.T) {
	database, pool, server, ctx := newMessagesHTTPEnv(t)
	manager := createMessageUser(t, ctx, database, pool, "海报权限管理员")
	reviewer := createMessageUser(t, ctx, database, pool, "海报权限审核员")
	activityIDs := []int64{}
	registerAdminPosterCleanup(t, pool, &activityIDs, []int64{manager.ID, reviewer.ID})
	if changed, err := database.AssignAdminRole(ctx, manager.ID, "activity_manager", true); err != nil || !changed {
		t.Fatalf("assign activity_manager: changed=%v err=%v", changed, err)
	}
	if changed, err := database.AssignAdminRole(ctx, reviewer.ID, "reviewer", true); err != nil || !changed {
		t.Fatalf("assign reviewer: changed=%v err=%v", changed, err)
	}
	client := loginMessageClient(t, database, server, manager)
	csrf := client.pageCSRF("/admin/activities/new")
	name := "海报权限测试 " + randomHTTPDigits(t, 8)
	resp := postAdminActivityMultipart(t, client, "/admin/activities",
		adminActivityFormValues(csrf, name),
		&adminPosterPart{Filename: "权限.png", ContentType: "image/png", Data: testPNG(t)})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	activityID := adminActivityIDFromLocation(t, resp.Header.Get("Location"))
	activityIDs = append(activityIDs, activityID)
	activity, err := database.GetActivity(ctx, strconv.FormatInt(activityID, 10))
	if err != nil {
		t.Fatalf("reload activity: %v", err)
	}
	posterPath := "/admin/activities/" + strconv.FormatInt(activityID, 10) + "/poster"

	anonymous := newWSClient(t, server.URL)
	resp = anonymous.get(posterPath)
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
		t.Fatalf("anonymous admin poster status=%d location=%q",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	reviewerClient := loginMessageClient(t, database, server, reviewer)
	resp = reviewerClient.get(posterPath)
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer admin poster status = %d", resp.StatusCode)
	}

	resp = anonymous.get("/activities/" + activity.ActivityNo + "/poster")
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous public poster status = %d", resp.StatusCode)
	}
}

func TestAdminActivityEditTemplatePosterForm(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	user := &db.User{ID: 1, Name: "管理员"}
	posterID := "123e4567-e89b-42d3-a456-426614174000"
	activity := db.Activity{
		ID: 5, Name: "海报活动", Status: db.ActivityStatusOpen, PosterMediaID: &posterID,
	}
	var buf bytes.Buffer
	if err := tpl.Render(&buf, "admin_activity_edit", PageData{
		Title: "活动编辑", User: user, CSRFToken: "csrf-value",
		Data: adminActivityEditPageData{
			Activity: &activity, Statuses: adminActivityStatuses, PosterName: "活动海报.png",
		},
		ActiveNav: "activities", MediaMaxMB: 10,
	}); err != nil {
		t.Fatalf("render edit: %v", err)
	}
	editHTML := buf.String()
	for _, want := range []string{
		`enctype="multipart/form-data"`, `name="poster"`, `accept="image/jpeg,image/png"`,
		`src="/admin/activities/5/poster?size=thumb"`, "活动海报.png", "最大 10 MB",
	} {
		if !strings.Contains(editHTML, want) {
			t.Fatalf("edit template missing %q", want)
		}
	}

	buf.Reset()
	if err := tpl.Render(&buf, "admin_activity_edit", PageData{
		Title: "活动编辑", User: user, CSRFToken: "csrf-value",
		Data: adminActivityEditPageData{
			Activity: &db.Activity{Status: db.ActivityStatusOpen}, IsNew: true,
			Statuses: adminActivityStatuses,
		},
		ActiveNav: "activities", MediaMaxMB: 10,
	}); err != nil {
		t.Fatalf("render new: %v", err)
	}
	newHTML := buf.String()
	for _, want := range []string{
		`enctype="multipart/form-data"`, `name="poster"`, `accept="image/jpeg,image/png"`,
	} {
		if !strings.Contains(newHTML, want) {
			t.Fatalf("new template missing %q", want)
		}
	}
	if strings.Contains(newHTML, "poster?size=thumb") {
		t.Fatal("new activity template rendered a poster preview")
	}
}
