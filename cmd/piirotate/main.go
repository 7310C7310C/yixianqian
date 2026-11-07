// Command piirotate re-encrypts and re-hashes ID-card rows written with the
// pre-split key. Run it once with both SESSION_SECRET (legacy) and PII_SECRET
// (new) set, after a database backup and before starting the new server.
//
// It is fail-closed: any row that cannot be decrypted, or any hash-only row
// that cannot be recomputed, aborts the whole rotation. Updates run in one
// transaction, so a unique-index conflict leaves the table unchanged.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/pii"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "piirotate:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.PIISecret == "" {
		return fmt.Errorf("PII_SECRET is required")
	}
	if cfg.PIISecret == cfg.SessionSecret {
		return fmt.Errorf("PII_SECRET must differ from SESSION_SECRET")
	}
	dryRun := flag.Bool("dry-run", false, "verify and count without writing")
	flag.Parse()

	oldSecret := cfg.SessionSecret
	newSecret := cfg.PIIKey()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	// Hash-only rows (no ciphertext) cannot be recomputed safely. Refuse to
	// continue instead of leaving a hash that bypasses the new duplicate check.
	var hashOnly int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM users
		  WHERE id_card_hash IS NOT NULL AND id_card_ciphertext IS NULL`).Scan(&hashOnly); err != nil {
		return fmt.Errorf("count hash-only rows: %w", err)
	}
	if hashOnly > 0 {
		return fmt.Errorf("%d rows have id_card_hash but no ciphertext; resolve manually before rotating", hashOnly)
	}

	rows, err := conn.Query(ctx,
		`SELECT id, id_card_ciphertext FROM users
		  WHERE id_card_ciphertext IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	type row struct {
		id     int64
		cipher []byte
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.cipher); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Decrypt every row before writing anything: one bad row must not leave a
	// half-rotated table.
	type rotated struct {
		id     int64
		sealed []byte
		hash   string
	}
	prepared := make([]rotated, 0, len(list))
	already := 0
	for _, r := range list {
		plain, usedLegacy, err := pii.Open(newSecret, r.cipher)
		if err == nil {
			if !usedLegacy {
				already++
			}
		} else {
			// open with the old root also covers rows written by the brief
			// 026ea40 build when PII_SECRET was empty (new enc domain, old root).
			plain, _, err = pii.Open(oldSecret, r.cipher)
			if err != nil {
				return fmt.Errorf("user %d: cannot decrypt with either key: %w", r.id, err)
			}
		}
		normalized := strings.ToUpper(strings.TrimSpace(plain))
		if len(normalized) != 18 {
			return fmt.Errorf("user %d: unexpected id-card length %d; resolve manually", r.id, len(normalized))
		}
		sealed, err := pii.Seal(newSecret, plain)
		if err != nil {
			return fmt.Errorf("user %d: seal: %w", r.id, err)
		}
		prepared = append(prepared, rotated{
			id: r.id, sealed: sealed, hash: pii.Hash(newSecret, normalized),
		})
	}

	if *dryRun {
		fmt.Fprintf(os.Stderr, "piirotate: dry-run rotate=%d already=%d (no writes)\n", len(prepared), already)
		return nil
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, r := range prepared {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET id_card_ciphertext = $2, id_card_hash = $3 WHERE id = $1`,
			r.id, r.sealed, r.hash); err != nil {
			return fmt.Errorf("user %d: update (unique conflict?): %w", r.id, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO pii_rotation (id, rotated_at, key_version)
		 VALUES (true, now(), 'v2')
		 ON CONFLICT (id) DO UPDATE
		   SET rotated_at = excluded.rotated_at, key_version = excluded.key_version`); err != nil {
		return fmt.Errorf("write rotation marker: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		// A commit error can be ambiguous (server committed, connection died).
		// Reconnect before checking the marker; the original connection may be
		// the reason the query fails. A visible marker means success.
		if conn2, connErr := pgx.Connect(ctx, cfg.DatabaseURL); connErr == nil {
			var marked bool
			_ = conn2.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pii_rotation)`).Scan(&marked)
			_ = conn2.Close(context.Background())
			if marked {
				fmt.Fprintln(os.Stderr, "piirotate: commit returned an error, but rotation marker is present; treating as committed")
				return nil
			}
		}
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintf(os.Stderr, "piirotate: rotated=%d already=%d\n", len(prepared), already)
	return nil
}
