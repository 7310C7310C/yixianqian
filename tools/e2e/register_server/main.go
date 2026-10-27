// Command register_server is a local verification harness for the scan-safe
// /register page. It runs the real HTTP server against PostgreSQL with an
// in-memory object store so Playwright can exercise the form end to end
// without R2 credentials.
//
// Usage:
//
//	go run ./tools/e2e/register_server -dsn 'postgres:///db?host=/var/run/postgresql' -addr 127.0.0.1:18095
//
// It is verification tooling only; the production entrypoint stays
// cmd/server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/httpserver"
)

type memoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: map[string][]byte{}, types: map[string]string{}}
}

func (s *memoryStore) Put(_ context.Context, key string, data []byte, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
	s.types[key] = contentType
	return nil
}

func (s *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

func (s *memoryStore) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	delete(s.types, key)
	return nil
}

func main() {
	dsn := flag.String("dsn", "", "PostgreSQL DSN for the throwaway verification database")
	addr := flag.String("addr", "127.0.0.1:18095", "listen address")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "-dsn is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := db.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := database.Migrate(ctx, logger); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	cfg := config.Config{
		AppEnv:            "test",
		BaseURL:           "http://" + *addr,
		DatabaseURL:       *dsn,
		SessionSecret:     strings.Repeat("e", 32),
		SessionCookieName: "yxq_session",
		SessionTTL:        time.Hour,
		MediaMaxBytes:     10 << 20,
		MediaThumbSize:    128,
		ActivationTTL:     time.Hour,
		LogLevel:          slog.LevelWarn,
		LogFormat:         "text",
	}
	app, err := httpserver.New(cfg, database, newMemoryStore(), logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new app: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen %s: %v\n", *addr, err)
		os.Exit(1)
	}
	server := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "serve: %v\n", serveErr)
			os.Exit(1)
		}
	}()

	fmt.Printf("E2E_READY http://%s\n", listener.Addr().String())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}
