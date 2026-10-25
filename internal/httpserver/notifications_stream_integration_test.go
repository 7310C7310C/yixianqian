package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/jackc/pgx/v5"
)

// countingTracer counts statements issued through the business pool. The raw
// pgxpool used by the test for fixtures is deliberately not traced.
type countingTracer struct {
	queries atomic.Int64
}

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	return ctx
}

func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type notificationStream struct {
	resp   *http.Response
	reader *bufio.Reader
}

func openNotificationStream(t *testing.T, client *wsClient, ctx context.Context) *notificationStream {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		client.base+"/legacy/api/notifications/stream", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	// The shared wsClient has a 15s whole-request timeout, which would kill an
	// SSE body; use the same cookie jar without that timeout.
	streamClient := &http.Client{Jar: client.jar}
	resp, err := streamClient.Do(req)
	if err != nil {
		t.Fatalf("open notification stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("notification stream status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("notification stream content-type=%q", ct)
	}
	return &notificationStream{resp: resp, reader: bufio.NewReader(resp.Body)}
}

func (s *notificationStream) close() { _ = s.resp.Body.Close() }

func (s *notificationStream) next(t *testing.T) legacyNotificationStreamPayload {
	t.Helper()
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read notification stream: %v", err)
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload legacyNotificationStreamPayload
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("decode notification stream: %v", err)
		}
		return payload
	}
}

func insertNotification(t *testing.T, env *profileHTTPEnv, userID int64, title string) int64 {
	t.Helper()
	var id int64
	if err := env.pool.QueryRow(env.ctx,
		`INSERT INTO notifications (user_id, kind, title, body) VALUES ($1, 'test', $2, '') RETURNING id`,
		userID, title).Scan(&id); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	// Mirror the production write path: the raw fixture pool must also notify,
	// otherwise this test would only exercise the fallback ticker.
	if _, err := env.pool.Exec(env.ctx, `SELECT pg_notify($1, $2)`,
		db.NotificationChannel, strconv.FormatInt(userID, 10)); err != nil {
		t.Fatalf("notify fixture: %v", err)
	}
	return id
}

// TestIntegrationNotificationStreamLISTENNotify covers: an idle listener costs
// no business queries, a committed notification reaches every tab of the owner
// immediately, and another user's stream stays silent.
func TestIntegrationNotificationStreamLISTENNotify(t *testing.T) {
	tracer := &countingTracer{}
	env := setupActivitiesHTTPEnvWithOptions(t, nil, db.WithQueryTracer(tracer))

	// No SSE subscriber yet: the dedicated LISTEN connection must not query.
	time.Sleep(2 * time.Second)
	if got := tracer.queries.Load(); got != 0 {
		t.Fatalf("idle listener issued %d queries, want 0", got)
	}

	userA, clientA := env.createUser(t, "U-SSE-A", "通知甲", "active", "user")
	_, clientB := env.createUser(t, "U-SSE-B", "通知乙", "active", "user")
	if err := env.app.notifier.WaitReady(env.ctx); err != nil {
		t.Fatalf("notification hub not ready: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	streamA1 := openNotificationStream(t, clientA, ctx)
	defer streamA1.close()
	streamA2 := openNotificationStream(t, clientA, ctx)
	defer streamA2.close()
	streamB := openNotificationStream(t, clientB, ctx)
	defer streamB.close()

	for name, stream := range map[string]*notificationStream{"A1": streamA1, "A2": streamA2, "B": streamB} {
		if initial := stream.next(t); initial.Unread != 0 {
			t.Fatalf("%s initial unread=%d, want 0", name, initial.Unread)
		}
	}

	// No events for five seconds: the old 4s poller would have issued queries.
	tracer.queries.Store(0)
	time.Sleep(5 * time.Second)
	if got := tracer.queries.Load(); got != 0 {
		t.Fatalf("idle SSE issued %d queries in 5s, want 0", got)
	}

	id := insertNotification(t, env, userA.ID, "即时通知")
	for name, stream := range map[string]*notificationStream{"A1": streamA1, "A2": streamA2} {
		pushed := stream.next(t)
		if pushed.Unread != 1 || pushed.LatestID != id || pushed.LatestTitle != "即时通知" {
			t.Fatalf("%s pushed=%+v, want unread=1 latest=%d", name, pushed, id)
		}
	}
	// Two snapshots (unread + latest) for each of A1/A2; allow a little slack
	// for driver-side statements while rejecting any 4s polling.
	if got := tracer.queries.Load(); got < 2 || got > 8 {
		t.Fatalf("queries after notification = %d, want 2..8", got)
	}

	// User B must not see user A's notification.
	bEvents := make(chan legacyNotificationStreamPayload, 1)
	go func() {
		for {
			line, err := streamB.reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var payload legacyNotificationStreamPayload
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload) == nil {
				bEvents <- payload
				return
			}
		}
	}()
	select {
	case got := <-bEvents:
		t.Fatalf("user B received user A's notification: %+v", got)
	case <-time.After(2 * time.Second):
	}

	// Reading in one tab wakes the other tab as well.
	if _, err := env.database.MarkAllNotificationsRead(env.ctx, userA.ID); err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	for name, stream := range map[string]*notificationStream{"A1": streamA1, "A2": streamA2} {
		read := stream.next(t)
		if read.Unread != 0 {
			t.Fatalf("%s unread after read-all = %d, want 0", name, read.Unread)
		}
	}
}

// TestIntegrationNotificationStreamListenerReconnect kills the dedicated LISTEN
// backend and verifies the hub reconnects and delivers the next notification.
func TestIntegrationNotificationStreamListenerReconnect(t *testing.T) {
	env := setupActivitiesHTTPEnvWithOptions(t, nil)
	user, client := env.createUser(t, "U-SSE-R", "通知重连", "active", "user")
	if err := env.app.notifier.WaitReady(env.ctx); err != nil {
		t.Fatalf("notification hub not ready: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream := openNotificationStream(t, client, ctx)
	defer stream.close()
	if initial := stream.next(t); initial.Unread != 0 {
		t.Fatalf("initial unread=%d, want 0", initial.Unread)
	}

	before := env.app.notifier.Generation()
	if _, err := env.pool.Exec(env.ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		  WHERE application_name = 'yixianqian-notify' AND datname = current_database()`); err != nil {
		t.Fatalf("terminate listener backend: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for env.app.notifier.Generation() <= before {
		if time.Now().After(deadline) {
			t.Fatal("notification hub did not reconnect within 10s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	id := insertNotification(t, env, user.ID, "重连后通知")
	pushed := stream.next(t)
	if pushed.Unread != 1 || pushed.LatestID != id {
		t.Fatalf("pushed after reconnect=%+v, want unread=1 latest=%d", pushed, id)
	}
}
