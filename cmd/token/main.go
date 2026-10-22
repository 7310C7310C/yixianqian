// Command token issues a one-time activation URL for an existing user or for a
// new phone/name pair.
package main

import (
	"context"
	"errors"
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
		fmt.Fprintln(os.Stderr, "token:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)

	legacyID := flag.String("legacy-user-id", "", "existing user's legacy_user_id")
	phoneRaw := flag.String("phone", "", "phone number for an existing or new user")
	name := flag.String("name", "", "display name (required for a new phone)")
	ttl := flag.Duration("ttl", 0, "token lifetime (default ACTIVATION_TTL)")
	flag.Parse()

	if (*legacyID == "") == (*phoneRaw == "") {
		return errors.New("provide exactly one of -legacy-user-id or -phone")
	}
	if *ttl == 0 {
		*ttl = cfg.ActivationTTL
	}
	if *ttl <= 0 || *ttl > 30*24*time.Hour {
		return errors.New("-ttl must be between 1s and 720h")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(ctx, logger); err != nil {
		return err
	}

	var (
		userID   *int64
		phone    string
		userName string
		target   string
	)
	if *legacyID != "" {
		user, err := database.GetUserByLegacyID(ctx, *legacyID)
		if errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("no user with legacy_user_id %q", *legacyID)
		}
		if err != nil {
			return err
		}
		if user.Status != "active" {
			return fmt.Errorf("user %q is disabled", *legacyID)
		}
		has, err := database.HasCredential(ctx, user.ID)
		if err != nil {
			return err
		}
		if has {
			return fmt.Errorf("user %q already has a password; use the login flow", *legacyID)
		}
		userID = &user.ID
		target = fmt.Sprintf("%d", user.ID)
	} else {
		phone = auth.NormalizePhone(*phoneRaw)
		if phone == "" {
			return fmt.Errorf("invalid phone number %q", *phoneRaw)
		}
		user, err := database.GetUserByPhone(ctx, phone)
		switch {
		case err == nil:
			if user.Status != "active" {
				return fmt.Errorf("user with phone %q is disabled", phone)
			}
			has, err := database.HasCredential(ctx, user.ID)
			if err != nil {
				return err
			}
			if has {
				return fmt.Errorf("user with phone %q already has a password", phone)
			}
			userID = &user.ID
			userName = user.Name
			target = fmt.Sprintf("%d", user.ID)
		case errors.Is(err, db.ErrNotFound):
			if *name == "" {
				return errors.New("-name is required when the phone does not belong to an existing user")
			}
			userName = *name
			target = auth.MaskPhone(phone)
		default:
			return err
		}
	}

	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		return err
	}
	token, err := database.CreateActivationToken(ctx, userID, phone, userName, tokenHash, *ttl)
	if err != nil {
		return err
	}
	if err := database.InsertAudit(ctx, db.AuditEntry{
		Action:     "activation_token.issued",
		TargetType: "user",
		TargetID:   target,
		Metadata:   map[string]any{"via": "token_cli", "expires_at": token.ExpiresAt.UTC().Format(time.RFC3339)},
	}); err != nil {
		logger.Warn("token audit write failed", "error", err)
	}

	logger.Info("activation token issued", "target", target, "expires_at", token.ExpiresAt.UTC().Format(time.RFC3339))
	fmt.Println(cfg.BaseURL + "/activate?token=" + url.QueryEscape(raw))
	return nil
}
