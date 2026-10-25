// Command seed creates the P0 test account and prints a one-time activation URL.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)

	legacyID := flag.String("legacy-id", "U-0006", "legacy user id")
	phoneRaw := flag.String("phone", "13800000006", "phone number")
	name := flag.String("name", "测试用户", "display name")
	flag.Parse()
	phone := auth.NormalizePhone(*phoneRaw)
	if phone == "" {
		return fmt.Errorf("invalid phone number %q", *phoneRaw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.OpenMaintenance(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(ctx, logger); err != nil {
		return err
	}

	user, err := database.UpsertLegacyUser(ctx, *legacyID, phone, *name)
	if err != nil {
		return err
	}
	hasCredential, err := database.HasCredential(ctx, user.ID)
	if err != nil {
		return err
	}
	if hasCredential {
		logger.Info("seed user already has a password; no activation token issued", "user_id", user.ID)
		fmt.Fprintln(os.Stderr, "seed user already activated; use a fresh database or remove the credential to re-issue")
		return nil
	}

	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if _, err := database.CreateActivationToken(ctx, &user.ID, "", "", tokenHash, cfg.ActivationTTL); err != nil {
		return err
	}
	if err := database.InsertAudit(ctx, db.AuditEntry{
		Action:     "activation_token.seeded",
		TargetType: "user",
		TargetID:   fmt.Sprintf("%d", user.ID),
		Metadata:   map[string]any{"legacy_user_id": *legacyID, "via": "seed_cli"},
	}); err != nil {
		logger.Warn("seed audit write failed", "error", err)
	}

	logger.Info("seed user ready", "user_id", user.ID, "legacy_user_id", *legacyID,
		"activation_expires_in", cfg.ActivationTTL.String())
	fmt.Println(cfg.BaseURL + "/activate?token=" + url.QueryEscape(raw))
	return nil
}
