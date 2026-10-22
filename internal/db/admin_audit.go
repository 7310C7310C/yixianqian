package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// adminAuditMaxLimit caps one query so a crafted page/export request cannot
// pull the whole audit table into memory.
const adminAuditMaxLimit = 1000

// AdminAuditEntry is one audit row joined with the actor's display data.
type AdminAuditEntry struct {
	ID            int64
	CreatedAt     time.Time
	ActorUserID   *int64
	ActorLegacyID string
	ActorName     string
	Action        string
	TargetType    string
	TargetID      string
	IP            string
	Metadata      map[string]any
}

// AdminAuditFilter narrows the audit list and count queries. To is exclusive;
// the HTTP layer turns the inclusive "to" date into the next day's midnight.
type AdminAuditFilter struct {
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	From       *time.Time
	To         *time.Time
	Limit      int
	Offset     int
}

func (f AdminAuditFilter) limit() int {
	if f.Limit <= 0 {
		return 100
	}
	if f.Limit > adminAuditMaxLimit {
		return adminAuditMaxLimit
	}
	return f.Limit
}

func (f AdminAuditFilter) offset() int {
	if f.Offset < 0 {
		return 0
	}
	return f.Offset
}

// where builds the shared filter clause and arguments for list and count.
func (f AdminAuditFilter) where() (string, []any) {
	var clauses []string
	args := make([]any, 0, 8)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if actor := strings.TrimSpace(f.Actor); actor != "" {
		p := arg(actor)
		clauses = append(clauses, fmt.Sprintf(`(position(lower(%s) in lower(COALESCE(u.name, ''))) > 0
			OR position(lower(%s) in lower(COALESCE(u.legacy_user_id, ''))) > 0
			OR position(lower(%s) in lower(u.phone)) > 0)`, p, p, p))
	}
	if action := strings.TrimSpace(f.Action); action != "" {
		p := arg(action)
		clauses = append(clauses, fmt.Sprintf("position(lower(%s) in lower(a.action)) > 0", p))
	}
	if targetType := strings.TrimSpace(f.TargetType); targetType != "" {
		clauses = append(clauses, "a.target_type = "+arg(targetType))
	}
	if targetID := strings.TrimSpace(f.TargetID); targetID != "" {
		clauses = append(clauses, "a.target_id = "+arg(targetID))
	}
	if f.From != nil {
		clauses = append(clauses, "a.created_at >= "+arg(*f.From))
	}
	if f.To != nil {
		clauses = append(clauses, "a.created_at < "+arg(*f.To))
	}
	return strings.Join(clauses, " AND "), args
}

func adminAuditQuery(where string) string {
	q := `SELECT a.id, a.created_at, a.actor_user_id,
		COALESCE(u.legacy_user_id, ''), COALESCE(u.name, ''),
		a.action, a.target_type, a.target_id, COALESCE(host(a.ip), ''), a.metadata
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_user_id`
	if where != "" {
		q += " WHERE " + where
	}
	return q
}

// ListAdminAudit returns audit rows newest-first, joined with actor details.
func (d *DB) ListAdminAudit(ctx context.Context, f AdminAuditFilter) ([]AdminAuditEntry, error) {
	where, args := f.where()
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	query := adminAuditQuery(where) +
		fmt.Sprintf(" ORDER BY a.created_at DESC, a.id DESC LIMIT $%d OFFSET $%d", limitPos, offsetPos)
	args = append(args, f.limit(), f.offset())

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list admin audit: %w", err)
	}
	defer rows.Close()

	out := make([]AdminAuditEntry, 0)
	for rows.Next() {
		var (
			e       AdminAuditEntry
			rawMeta []byte
		)
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.ActorUserID, &e.ActorLegacyID,
			&e.ActorName, &e.Action, &e.TargetType, &e.TargetID, &e.IP, &rawMeta); err != nil {
			return nil, fmt.Errorf("db: scan admin audit: %w", err)
		}
		e.Metadata = decodeAdminAuditMetadata(rawMeta)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: list admin audit: %w", err)
	}
	return out, nil
}

// CountAdminAudit counts rows matching the same filter as ListAdminAudit.
func (d *DB) CountAdminAudit(ctx context.Context, f AdminAuditFilter) (int64, error) {
	where, args := f.where()
	query := `SELECT count(*) FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_user_id`
	if where != "" {
		query += " WHERE " + where
	}
	var total int64
	if err := d.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("db: count admin audit: %w", err)
	}
	return total, nil
}

func decodeAdminAuditMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil || meta == nil {
		return map[string]any{}
	}
	return meta
}
