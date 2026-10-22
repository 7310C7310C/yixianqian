package db

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// AuditEntry is one append-only audit record. Metadata must never contain
// passwords, raw tokens or cookie values.
type AuditEntry struct {
	ActorUserID *int64
	Action      string
	TargetType  string
	TargetID    string
	IP          string
	UserAgent   string
	Metadata    map[string]any
}

type sqlExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// InsertAudit appends an audit row outside a transaction.
func (d *DB) InsertAudit(ctx context.Context, e AuditEntry) error {
	return insertAudit(ctx, d.pool, e)
}

func insertAudit(ctx context.Context, ex sqlExecutor, e AuditEntry) error {
	if e.Action == "" {
		return fmt.Errorf("db: audit action is required")
	}
	_, err := ex.Exec(ctx,
		`INSERT INTO audit_log
		   (actor_user_id, action, target_type, target_id, ip, user_agent, metadata)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet, NULLIF($6, ''), $7::text::jsonb)`,
		anyOrNil(e.ActorUserID), e.Action, e.TargetType, e.TargetID,
		e.IP, sanitizeText(e.UserAgent, 512), marshalMetadata(e.Metadata))
	if err != nil {
		return fmt.Errorf("db: insert audit: %w", err)
	}
	return nil
}

// sanitizeText replaces invalid UTF-8 and truncates on a rune boundary so
// PostgreSQL text columns never receive malformed input.
func sanitizeText(s string, max int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
