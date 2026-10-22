package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func newLegacySocialTestApp(t *testing.T) *App {
	t.Helper()
	app, err := New(testConfig(), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func legacySignedInRequest(method, path, body string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(withUser(req.Context(), &db.User{ID: 42, Name: "测试用户"}))
	return req
}

func TestLegacySocialRoutesRequireLogin(t *testing.T) {
	app := newLegacySocialTestApp(t)
	handler := app.Handler()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/legacy/api/like"},
		{http.MethodPost, "/legacy/api/praise"},
		{http.MethodDelete, "/legacy/api/praise/U-0001"},
		{http.MethodGet, "/legacy/api/messages"},
		{http.MethodPost, "/legacy/api/messages"},
		{http.MethodDelete, "/legacy/api/messages/1"},
		{http.MethodPost, "/legacy/api/messages/1/report"},
		{http.MethodGet, "/legacy/api/messages/mine"},
		{http.MethodGet, "/legacy/api/messages/received"},
		{http.MethodGet, "/legacy/api/messages/reported"},
		{http.MethodGet, "/legacy/api/notifications"},
		{http.MethodPost, "/legacy/api/notifications/1/read"},
		{http.MethodPost, "/legacy/api/notifications/read-all"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
			continue
		}
		var payload map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Errorf("%s %s body = %q", tc.method, tc.path, rec.Body.String())
			continue
		}
		if payload["error"] != "未登录" {
			t.Errorf("%s %s error = %q", tc.method, tc.path, payload["error"])
		}
	}
}

func TestLegacySocialWritesRejectMissingCSRF(t *testing.T) {
	app := newLegacySocialTestApp(t)
	handler := app.Handler()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/legacy/api/like"},
		{http.MethodPost, "/legacy/api/praise"},
		{http.MethodDelete, "/legacy/api/praise/U-0001"},
		{http.MethodPost, "/legacy/api/messages"},
		{http.MethodDelete, "/legacy/api/messages/1"},
		{http.MethodPost, "/legacy/api/messages/1/report"},
		{http.MethodPost, "/legacy/api/notifications/1/read"},
		{http.MethodPost, "/legacy/api/notifications/read-all"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, legacySignedInRequest(tc.method, tc.path, "{}"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", tc.method, tc.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "表单已过期") {
			t.Errorf("%s %s body = %q", tc.method, tc.path, rec.Body.String())
		}
	}
}

func legacyCSRFRequest(app *App, method, path, body string) *http.Request {
	req := legacySignedInRequest(method, path, body)
	token, err := app.csrf.Issue()
	if err != nil {
		panic(err)
	}
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	req.Header.Set(auth.CSRFHeader, token)
	return req
}

func TestLegacySocialValidatesInputBeforeDatabase(t *testing.T) {
	app := newLegacySocialTestApp(t)
	handler := app.Handler()
	cases := []struct {
		name string
		path string
		body string
		want string
	}{
		{"like invalid json", "/legacy/api/like", "{", "请求内容格式不正确"},
		{"like missing target", "/legacy/api/like", "{}", "缺少目标用户"},
		{"like invalid type", "/legacy/api/like", `{"target_openid":"U-1","like_type":"随便"}`, "like_type 不合法"},
		{"praise missing target", "/legacy/api/praise", "{}", "缺少目标用户"},
		{"message missing target", "/legacy/api/messages", "{}", "缺少目标用户"},
		{"message empty content", "/legacy/api/messages", `{"target_openid":"U-1"}`, "内容不能为空"},
		{"message too long", "/legacy/api/messages", `{"target_openid":"U-1","content":"` + strings.Repeat("字", 501) + `"}`, "留言最多500字"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, legacyCSRFRequest(app, http.MethodPost, tc.path, tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestLegacyLikeErrors(t *testing.T) {
	app := newLegacySocialTestApp(t)
	cases := []struct {
		err     error
		like    string
		status  int
		message string
	}{
		{db.ErrSelfAction, db.LikeTypeAnon, http.StatusBadRequest, "不能喜欢自己"},
		{db.ErrSameGender, db.LikeTypeAnon, http.StatusBadRequest, "仅限异性之间喜欢"},
		{db.ErrTargetUnavailable, db.LikeTypeAnon, http.StatusBadRequest, "对方当前不在牵线名单中"},
		{db.ErrAlreadyLiked, db.LikeTypeAnon, http.StatusBadRequest, "你已经喜欢过TA了"},
		{db.ErrInvalidLikeType, db.LikeTypeAnon, http.StatusBadRequest, "like_type 不合法"},
		{db.ErrLikeMessageShort, db.LikeTypeAnon, http.StatusBadRequest, "附言需 10 字以上"},
		{db.ErrLikeMessageLong, db.LikeTypeAnon, http.StatusBadRequest, "附言最多 500 字"},
		{db.ErrLikeQuota, db.LikeTypeAnon, http.StatusBadRequest, "本月匿名喜欢额度已用完"},
		{db.ErrLikeQuota, db.LikeTypeReal, http.StatusBadRequest, "本月实名喜欢机会已用完"},
		{db.ErrNotFound, db.LikeTypeAnon, http.StatusNotFound, "用户不存在"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/legacy/api/like", nil)
		writeLegacyLikeError(app, rec, req, tc.err, tc.like)
		if rec.Code != tc.status {
			t.Errorf("%v status = %d, want %d", tc.err, rec.Code, tc.status)
		}
		if !strings.Contains(rec.Body.String(), tc.message) {
			t.Errorf("%v body = %q, want %q", tc.err, rec.Body.String(), tc.message)
		}
	}
}

func TestLegacyThreadMessagesShape(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	rootID := int64(11)
	msgs := []db.Message{
		{
			ID: rootID, TargetUserID: 1, AuthorID: 2,
			AuthorName: "作者甲", AuthorLegacyID: "U-0002", AuthorLegacyOpenID: "ou_author",
			Content: "首条留言", CreatedAt: now,
		},
		{
			ID: 12, TargetUserID: 1, AuthorID: 1, ParentID: &rootID, ReplyToID: &rootID,
			AuthorName: "卡片主人", AuthorLegacyID: "U-0001", AuthorLegacyOpenID: "ou_owner",
			ReplyToName: "作者甲", ReplyToLegacyID: "U-0002",
			Content: "回复内容", CreatedAt: now.Add(time.Minute),
		},
	}
	avatars := map[int64]string{2: "/cards/photo/m1?size=thumb"}
	out := legacyThreadMessages(msgs, 1, false, avatars)
	if len(out) != 2 {
		t.Fatalf("items = %d, want 2 flat rows", len(out))
	}
	if out[0].ID != "11" || out[0].AuthorOpenID != "ou_author" || out[0].AuthorUID != "U-0002" {
		t.Fatalf("root = %+v", out[0])
	}
	if out[0].AuthorAvatar != "/cards/photo/m1?size=thumb" {
		t.Fatalf("root avatar = %q", out[0].AuthorAvatar)
	}
	if out[0].IsMine || !out[0].CanDelete {
		t.Fatalf("root flags = mine:%v delete:%v", out[0].IsMine, out[0].CanDelete)
	}
	if len(out[0].Replies) != 1 || out[0].Replies[0].Content != "回复内容" {
		t.Fatalf("root replies = %+v", out[0].Replies)
	}
	if out[1].ParentID != "11" || out[1].ReplyToNickname != "作者甲" || !out[1].IsMine {
		t.Fatalf("reply = %+v", out[1])
	}
}

func TestLegacyNotificationsShape(t *testing.T) {
	readAt := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	rows := []db.Notification{
		{
			ID: 7, Kind: "system", Title: "活动提醒", Body: "记得报名",
			Link: "/activities/A-100", ReadAt: &readAt,
			CreatedAt: time.Date(2026, 10, 22, 8, 30, 0, 0, time.UTC),
		},
		{
			ID: 8, Kind: "like", Title: "有人喜欢了你", Payload: map[string]any{"activity_id": "A-200"},
			CreatedAt: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
		},
	}
	out := legacyNotifications(rows)
	if len(out) != 2 {
		t.Fatalf("notifications = %d", len(out))
	}
	if out[0].Text != "活动提醒 记得报名" || out[0].ActivityID != "A-100" || !out[0].Read {
		t.Fatalf("first = %+v", out[0])
	}
	if out[1].ActivityID != "A-200" || out[1].Read {
		t.Fatalf("second = %+v", out[1])
	}
	if out[1].Text != "有人喜欢了你" {
		t.Fatalf("second text = %q", out[1].Text)
	}
}

func TestLegacyActivityIDFromLink(t *testing.T) {
	cases := map[string]string{
		"/activities/A-100":                       "A-100",
		"/activities/A-100?from=notifications":    "A-100",
		"https://next.nantou.love/activities/A-9": "A-9",
		"/legacy/api/activities/A-7":              "A-7",
		"/activity/x y":                           "x y",
		"/cards":                                  "",
		"":                                        "",
	}
	for link, want := range cases {
		if got := legacyActivityIDFromLink(link); got != want {
			t.Errorf("legacyActivityIDFromLink(%q) = %q, want %q", link, got, want)
		}
	}
}
