package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/face"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type profileHTTPEnv struct {
	ctx      context.Context
	cfg      config.Config
	database *db.DB
	pool     *pgxpool.Pool
	server   *httptest.Server
	store    *fakeStore
	app      *App
}

func setupProfileHTTPEnv(t *testing.T) *profileHTTPEnv {
	t.Helper()
	return setupActivitiesHTTPEnv(t, nil)
}

func setupActivitiesHTTPEnv(t *testing.T, mutate func(*config.Config)) *profileHTTPEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	store := newFakeStore()
	app, err := New(cfg, database, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	// 默认不启动 Python 子进程；需要验证人脸链路的测试自行替换 faceProbe。
	app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{}, errors.New("face probe disabled in test env")
	})
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	return &profileHTTPEnv{
		ctx:      ctx,
		cfg:      cfg,
		database: database,
		pool:     pool,
		server:   server,
		store:    store,
		app:      app,
	}
}

func (e *profileHTTPEnv) createUser(t *testing.T, legacyPrefix, name, accountStatus, accountKind string) (db.User, *wsClient) {
	t.Helper()
	legacy := legacyPrefix + "-" + randomHTTPDigits(t, 10)
	phone := "1" + randomHTTPDigits(t, 10)
	user, err := e.database.UpsertLegacyUser(e.ctx, legacy, phone, name)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx,
		`UPDATE users SET account_status = $2, account_kind = $3 WHERE id = $1`,
		user.ID, accountStatus, accountKind); err != nil {
		t.Fatalf("set account status: %v", err)
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.database.CreateSession(e.ctx, user.ID, hash, time.Hour, "", "http-test"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	client := newWSClient(t, e.server.URL)
	base, err := url.Parse(e.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.jar.SetCookies(base, []*http.Cookie{{
		Name:  e.cfg.SessionCookieName,
		Value: raw,
		Path:  "/",
	}})
	resp := client.get("/welcome")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prime csrf: status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if client.csrfCookie() == "" {
		t.Fatal("csrf cookie missing after page render")
	}
	return user, client
}

func createHTTPActivity(t *testing.T, e *profileHTTPEnv, maxSignup int, signin bool, code string) (int64, string) {
	t.Helper()
	activityNo := "A-HTTP-ACT-" + randomHTTPDigits(t, 10)
	var activityID int64
	// 默认不排期：报名/签到的时间窗规则由 activities_window_integration_test
	// 专门覆盖，其余用例只验证业务动作本身。
	err := e.pool.QueryRow(e.ctx,
		`INSERT INTO activities (activity_no, name, description, location, condition,
		                        max_signup, current_signup, fee, food, status,
		                        signin_enabled, signin_code)
		 VALUES ($1, $2, '活动描述', '活动地点', '参与条件', $3, 0, '100', '自理',
		         '报名中', $4, $5)
		 RETURNING id`,
		activityNo, "测试活动 "+activityNo, maxSignup, signin, code).Scan(&activityID)
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.pool.Exec(cleanupCtx, `DELETE FROM activities WHERE id = $1`, activityID)
	})
	return activityID, activityNo
}

func cleanupHTTPUsers(t *testing.T, e *profileHTTPEnv, ids ...int64) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, id := range ids {
			_, _ = e.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, id)
		}
	})
}

func activityAPIErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return payload.Error.Code
}

func profileRequest(t *testing.T, client *wsClient, method, path, csrf string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, client.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	return client.do(req)
}

func profileJSONRequest(t *testing.T, client *wsClient, method, path, csrf string, payload any) *http.Response {
	t.Helper()
	if payload == nil {
		return profileRequest(t, client, method, path, csrf, nil, "application/json")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return profileRequest(t, client, method, path, csrf, bytes.NewReader(body), "application/json")
}

func profileDecode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func profileAPIErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var payload struct {
		Error apiError `json:"error"`
	}
	profileDecode(t, resp, &payload)
	return payload.Error.Code
}

func newMessagesHTTPEnv(t *testing.T) (*db.DB, *pgxpool.Pool, *httptest.Server, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open raw pool: %v", err)
	}
	t.Cleanup(pool.Close)
	app, err := New(testConfig(), database, newFakeStore(), logger)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	return database, pool, server, ctx
}

func createMessageUser(t *testing.T, ctx context.Context, database *db.DB, pool *pgxpool.Pool, name string) db.User {
	t.Helper()
	u, err := database.UpsertLegacyUser(ctx, "U-MSGHTTP-"+randomHTTPDigits(t, 10), "1"+randomHTTPDigits(t, 10), name)
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET account_status = 'active' WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("activate user %s: %v", name, err)
	}
	return u
}

func loginMessageClient(t *testing.T, database *db.DB, server *httptest.Server, user db.User) *wsClient {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(context.Background(), user.ID, hash, time.Hour, "", "messages-http-test"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	client := newWSClient(t, server.URL)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.jar.SetCookies(u, []*http.Cookie{{
		Name:  testConfig().SessionCookieName,
		Value: raw,
		Path:  "/",
	}})
	resp := client.get("/welcome")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("welcome for %s status = %d", user.Name, resp.StatusCode)
	}
	if client.csrfCookie() == "" {
		t.Fatalf("csrf cookie missing for %s", user.Name)
	}
	return client
}

func (c *wsClient) postJSON(path, csrf string, payload any) *http.Response {
	c.t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	return c.do(req)
}

func (c *wsClient) deleteWithCSRF(path, csrf string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodDelete, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	if csrf != "" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	return c.do(req)
}

type pointsHTTPEnv struct {
	ctx      context.Context
	database *db.DB
	conn     *pgx.Conn
	cfg      config.Config
	server   *httptest.Server
}

func openPointsHTTPEnv(t *testing.T) *pointsHTTPEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect setup: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	})
	cfg := testConfig()
	app, err := New(cfg, database, newFakeStore(), logger)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	return &pointsHTTPEnv{ctx: ctx, database: database, conn: conn, cfg: cfg, server: server}
}

func (e *pointsHTTPEnv) user(t *testing.T, gender, accountStatus, accountKind string) (db.User, *wsClient) {
	t.Helper()
	legacy := "U-PTS-HTTP-" + randomHTTPDigits(t, 10)
	phone := "1" + randomHTTPDigits(t, 10)
	u, err := e.database.UpsertLegacyUser(e.ctx, legacy, phone, "麦穗测试")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = e.conn.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	if _, err := e.conn.Exec(e.ctx,
		`UPDATE users SET gender = $2, account_status = $3, account_kind = $4,
		                  status = 'active', nickname = $5 WHERE id = $1`,
		u.ID, gender, accountStatus, accountKind, legacy); err != nil {
		t.Fatalf("update fixture user: %v", err)
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.database.CreateSession(e.ctx, u.ID, hash, time.Hour, "", "points-http"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	client := newWSClient(t, e.server.URL)
	base, err := url.Parse(e.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.jar.SetCookies(base, []*http.Cookie{{
		Name: e.cfg.SessionCookieName, Value: raw, Path: "/",
	}})
	return u, client
}
