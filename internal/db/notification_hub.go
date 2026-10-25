package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotificationChannel is the PostgreSQL NOTIFY channel used by the SSE stream.
// Payloads are decimal user ids; the channel is never queried, so an idle
// server performs no database work for connected listeners.
const NotificationChannel = "yxq_notify"

const (
	notificationReconnectMin = 500 * time.Millisecond
	notificationReconnectMax = 30 * time.Second
)

// NotificationHub owns one dedicated LISTEN connection and fans notifications
// out to per-user subscribers. It reconnects on its own and signals every
// subscriber after a reconnect so handlers can re-snapshot the missed window.
type NotificationHub struct {
	dsn    string
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.RWMutex
	subs map[int64]map[*notificationSub]struct{}

	connected  atomic.Bool
	generation atomic.Int64
}

type notificationSub struct {
	ch chan struct{}
}

// NewNotificationHub starts the listener loop immediately. Connection failures
// are retried in the background and never block process startup.
func NewNotificationHub(parent context.Context, dsn string, logger *slog.Logger) *NotificationHub {
	ctx, cancel := context.WithCancel(parent)
	h := &NotificationHub{
		dsn:    dsn,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		subs:   make(map[int64]map[*notificationSub]struct{}),
	}
	go h.run()
	return h
}

// Subscribe registers interest in one user's notifications. The returned
// channel receives at most one pending signal; a full buffer drops the extra
// signal because the handler re-reads the current snapshot anyway.
func (h *NotificationHub) Subscribe(userID int64) (<-chan struct{}, func()) {
	sub := &notificationSub{ch: make(chan struct{}, 1)}
	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = make(map[*notificationSub]struct{})
	}
	h.subs[userID][sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[userID], sub)
			if len(h.subs[userID]) == 0 {
				delete(h.subs, userID)
			}
			h.mu.Unlock()
		})
	}
	return sub.ch, cancel
}

// Connected reports whether the dedicated LISTEN connection is ready.
func (h *NotificationHub) Connected() bool { return h.connected.Load() }

// Generation increments on every successful (re)connect. Tests can use it to
// wait for recovery without depending on logs or timing heuristics.
func (h *NotificationHub) Generation() int64 { return h.generation.Load() }

// WaitReady blocks until the listener has connected at least once or ctx ends.
func (h *NotificationHub) WaitReady(ctx context.Context) error {
	if h.connected.Load() {
		return nil
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.done:
			return errors.New("db: notification hub closed")
		case <-ticker.C:
			if h.connected.Load() {
				return nil
			}
		}
	}
}

// Close stops the listener and releases its connection.
func (h *NotificationHub) Close() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
	}
}

func (h *NotificationHub) run() {
	defer close(h.done)
	backoff := notificationReconnectMin
	for {
		if h.ctx.Err() != nil {
			return
		}
		genBefore := h.generation.Load()
		err := h.listenOnce()
		if h.ctx.Err() != nil {
			return
		}
		if h.generation.Load() > genBefore {
			// A connection was established; start the next retry from the
			// minimum delay even if it later dropped.
			backoff = notificationReconnectMin
		}
		if err != nil {
			h.logger.Warn("notification listener disconnected", "error", err)
		}
		if !sleepContext(h.ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > notificationReconnectMax {
			backoff = notificationReconnectMax
		}
	}
}

func (h *NotificationHub) listenOnce() error {
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	conn, err := pgx.Connect(ctx, h.dsn)
	cancel()
	if err != nil {
		return fmt.Errorf("db: connect notification listener: %w", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()

	if _, err := conn.Exec(h.ctx, "SET application_name = 'yixianqian-notify'"); err != nil {
		return fmt.Errorf("db: name notification listener: %w", err)
	}
	if _, err := conn.Exec(h.ctx, "LISTEN "+NotificationChannel); err != nil {
		return fmt.Errorf("db: listen %s: %w", NotificationChannel, err)
	}
	h.connected.Store(true)
	h.generation.Add(1)
	// A reconnect may have missed events while the connection was down.
	h.broadcastAll()
	defer h.connected.Store(false)

	for {
		n, err := conn.WaitForNotification(h.ctx)
		if err != nil {
			return err
		}
		userID, err := strconv.ParseInt(n.Payload, 10, 64)
		if err != nil || userID <= 0 {
			continue
		}
		h.fanout(userID)
	}
}

// subSnapshot copies one user's subscribers while holding the read lock.
//
// fanout/broadcastAll must never iterate the live map after unlocking: the
// cancel func deletes from the same map under the write lock, and concurrent
// map iteration+write is a Go runtime fatal error (unrecoverable process exit).
// Copying is cheap (a handful of tabs per user) and keeps delivery lock-free.
func (h *NotificationHub) subSnapshot(userID int64) []*notificationSub {
	h.mu.RLock()
	defer h.mu.RUnlock()
	subs := h.subs[userID]
	if len(subs) == 0 {
		return nil
	}
	out := make([]*notificationSub, 0, len(subs))
	for sub := range subs {
		out = append(out, sub)
	}
	return out
}

// allSubscribers returns a snapshot of every subscriber across users.
func (h *NotificationHub) allSubscribers() []*notificationSub {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, subs := range h.subs {
		total += len(subs)
	}
	if total == 0 {
		return nil
	}
	out := make([]*notificationSub, 0, total)
	for _, subs := range h.subs {
		for sub := range subs {
			out = append(out, sub)
		}
	}
	return out
}

func (h *NotificationHub) fanout(userID int64) {
	for _, sub := range h.subSnapshot(userID) {
		select {
		case sub.ch <- struct{}{}:
		default:
		}
	}
}

func (h *NotificationHub) broadcastAll() {
	for _, sub := range h.allSubscribers() {
		select {
		case sub.ch <- struct{}{}:
		default:
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func notifyUserTx(ctx context.Context, tx pgx.Tx, userID int64) error {
	if _, err := tx.Exec(ctx, "SELECT pg_notify($1, $2)", NotificationChannel,
		strconv.FormatInt(userID, 10)); err != nil {
		return fmt.Errorf("db: notify notification: %w", err)
	}
	return nil
}

func (d *DB) notifyUser(ctx context.Context, userID int64) error {
	if _, err := d.pool.Exec(ctx, "SELECT pg_notify($1, $2)", NotificationChannel,
		strconv.FormatInt(userID, 10)); err != nil {
		return fmt.Errorf("db: notify notification: %w", err)
	}
	return nil
}
