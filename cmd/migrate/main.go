// Command migrate applies embedded migrations once, outside the server
// process. Production/staging run it as a release step with AUTO_MIGRATE=false;
// the server then refuses to start on a schema it does not own.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	database, err := db.OpenMaintenance(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	return database.Migrate(ctx, logger)
}
