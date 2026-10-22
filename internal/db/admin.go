package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Admin report decisions. Message reports can delete or dismiss; user reports
// can be accepted or dismissed.
const (
	AdminReportDelete  = "delete"
	AdminReportDismiss = "dismiss"
	AdminReportResolve = "resolve"

	AdminSuggestionHandled  = "已处理"
	AdminSuggestionIgnored  = "已忽略"
	adminSuperAdminRoleCode = "super_admin"
)

// AdminUser is the moderation projection of a user account.
type AdminUser struct {
	ID            int64
	LegacyUserID  string
	Name          string
	Phone         string
	Nickname      string
	AccountKind   string
	AccountStatus string
	LoginStatus   string
	ReviewedAt    *time.Time
	LastActiveAt  *time.Time
	CreatedAt     time.Time
	Roles         []string
}

// AdminUserFilter narrows the admin user list.
type AdminUserFilter struct {
	Status string
	Query  string
	Limit  int
}

// AdminUserCounts summarises account statuses for the admin dashboard.
type AdminUserCounts struct {
	Pending  int64
	Active   int64
	Rejected int64
	Disabled int64
	Total    int64
}

// AdminStats is the dashboard summary. Pending counts exclude resolved rows.
type AdminStats struct {
	Users              AdminUserCounts
	PendingReports     int64
	PendingSuggestions int64
	SuperAdmins        int64
}

// AdminRole is one role with its permission catalogue and member count.
type AdminRole struct {
	ID          int64
	Code        string
	Name        string
	Permissions []string
	UserCount   int64
}

// AdminAssignment is one user holding one or more admin roles.
type AdminAssignment struct {
	UserID       int64
	LegacyUserID string
	Name         string
	Phone        string
	Roles        []string
}

// AdminSuggestion is one product feedback row.
type AdminSuggestion struct {
	ID         int64
	AuthorID   *int64
	AuthorName string
	Type       string
	Content    string
	Status     string
	CreatedAt  time.Time
}

const adminUserSelect = `SELECT u.id, COALESCE(u.legacy_user_id, ''), u.name, u.phone,
	COALESCE(NULLIF(u.nickname, ''), ''), u.account_kind, u.account_status, u.status,
	u.reviewed_at, u.last_active_at, u.created_at,
	COALESCE((SELECT array_agg(r.code ORDER BY r.code)
	          FROM admin_user_roles ur
	          JOIN admin_roles r ON r.id = ur.role_id
	          WHERE ur.user_id = u.id), '{}'::text[])
	FROM users u`

func scanAdminUser(row rowScanner) (AdminUser, error) {
	var u AdminUser
	err := row.Scan(&u.ID, &u.LegacyUserID, &u.Name, &u.Phone, &u.Nickname,
		&u.AccountKind, &u.AccountStatus, &u.LoginStatus, &u.ReviewedAt,
		&u.LastActiveAt, &u.CreatedAt, &u.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, err
	}
	return u, nil
}

// GetAdminUser loads one user with admin role codes.
func (d *DB) GetAdminUser(ctx context.Context, userID int64) (AdminUser, error) {
	u, err := scanAdminUser(d.pool.QueryRow(ctx, adminUserSelect+" WHERE u.id = $1", userID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return AdminUser{}, fmt.Errorf("db: get admin user: %w", err)
	}
	return u, err
}

// ListAdminUsers returns a filtered, newest-first user page.
func (d *DB) ListAdminUsers(ctx context.Context, f AdminUserFilter) ([]AdminUser, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := strings.TrimSpace(f.Query)
	rows, err := d.pool.Query(ctx,
		adminUserSelect+`
		 WHERE ($1 = '' OR u.account_status = $1)
		   AND ($2 = ''
		        OR position(lower($2) in lower(u.name)) > 0
		        OR position(lower($2) in lower(u.nickname)) > 0
		        OR position(lower($2) in lower(u.phone)) > 0
		        OR position(lower($2) in lower(COALESCE(u.legacy_user_id, ''))) > 0)
		 ORDER BY CASE WHEN u.account_status = 'pending' THEN 0 ELSE 1 END,
		          u.created_at DESC, u.id DESC
		 LIMIT $3`, f.Status, query, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list admin users: %w", err)
	}
	defer rows.Close()
	out := make([]AdminUser, 0)
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountAdminUsers groups accounts by the business status used by the console.
func (d *DB) CountAdminUsers(ctx context.Context) (AdminUserCounts, error) {
	var c AdminUserCounts
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE account_status = 'pending'),
		        count(*) FILTER (WHERE account_status = 'active' AND status = 'active'),
		        count(*) FILTER (WHERE account_status = 'rejected'),
		        count(*) FILTER (WHERE status = 'disabled'),
		        count(*)
		 FROM users`).Scan(&c.Pending, &c.Active, &c.Rejected, &c.Disabled, &c.Total)
	if err != nil {
		return AdminUserCounts{}, fmt.Errorf("db: count admin users: %w", err)
	}
	return c, nil
}

// AdminStats returns the dashboard counters in one round trip.
func (d *DB) AdminStats(ctx context.Context) (AdminStats, error) {
	var s AdminStats
	err := d.pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM users WHERE account_status = 'pending'),
		   (SELECT count(*) FROM users WHERE account_status = 'active' AND status = 'active'),
		   (SELECT count(*) FROM users WHERE account_status = 'rejected'),
		   (SELECT count(*) FROM users WHERE status = 'disabled'),
		   (SELECT count(*) FROM users),
		   (SELECT count(*) FROM reports WHERE status = $1),
		   (SELECT count(*) FROM suggestions WHERE status = $1),
		   (SELECT count(*) FROM admin_user_roles ur
		     JOIN admin_roles r ON r.id = ur.role_id
		     WHERE r.code = $2)`,
		ReportStatusPending, adminSuperAdminRoleCode).
		Scan(&s.Users.Pending, &s.Users.Active, &s.Users.Rejected, &s.Users.Disabled,
			&s.Users.Total, &s.PendingReports, &s.PendingSuggestions, &s.SuperAdmins)
	if err != nil {
		return AdminStats{}, fmt.Errorf("db: load admin stats: %w", err)
	}
	return s, nil
}

// UserAdminRoles returns the role codes assigned to a user.
func (d *DB) UserAdminRoles(ctx context.Context, userID int64) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT r.code
		 FROM admin_user_roles ur
		 JOIN admin_roles r ON r.id = ur.role_id
		 WHERE ur.user_id = $1
		 ORDER BY r.code`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list user admin roles: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// UserPermissions returns the effective permission codes. super_admin always
// receives the full catalogue, including permissions added after the role.
func (d *DB) UserPermissions(ctx context.Context, userID int64) ([]string, error) {
	var super bool
	if err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM admin_user_roles ur
		   JOIN admin_roles r ON r.id = ur.role_id
		   WHERE ur.user_id = $1 AND r.code = $2
		 )`, userID, adminSuperAdminRoleCode).Scan(&super); err != nil {
		return nil, fmt.Errorf("db: check super admin: %w", err)
	}
	if super {
		rows, err := d.pool.Query(ctx, `SELECT code FROM admin_permissions ORDER BY code`)
		if err != nil {
			return nil, fmt.Errorf("db: list permissions: %w", err)
		}
		defer rows.Close()
		out := make([]string, 0, 16)
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				return nil, err
			}
			out = append(out, code)
		}
		return out, rows.Err()
	}
	rows, err := d.pool.Query(ctx,
		`SELECT DISTINCT rp.permission
		 FROM admin_user_roles ur
		 JOIN admin_role_permissions rp ON rp.role_id = ur.role_id
		 WHERE ur.user_id = $1
		 ORDER BY rp.permission`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list user permissions: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// IsSuperAdmin reports whether the user holds the protected role.
func (d *DB) IsSuperAdmin(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM admin_user_roles ur
		   JOIN admin_roles r ON r.id = ur.role_id
		   WHERE ur.user_id = $1 AND r.code = $2
		 )`, userID, adminSuperAdminRoleCode).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("db: check super admin: %w", err)
	}
	return ok, nil
}

// FindAdminUserByRef resolves a user by numeric id, legacy id, legacy open id
// or phone. Admin role assignment uses the same refs as the legacy bot.
func (d *DB) FindAdminUserByRef(ctx context.Context, ref string) (User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return User{}, ErrNotFound
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return d.GetUserByID(ctx, id)
	}
	return scanUser(d.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE legacy_user_id = $1 OR legacy_open_id = $1 OR phone = $1
		 LIMIT 1`, ref))
}

// ApproveAdminUser activates a pending/rejected account and records who
// reviewed it.
func (d *DB) ApproveAdminUser(ctx context.Context, userID, actorID int64, note string) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("db: begin approve user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var previous string
	err = tx.QueryRow(ctx,
		`SELECT account_status FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load user for approval: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users
		 SET status = 'active', account_status = 'active',
		     id_card_valid = CASE WHEN id_card_ciphertext IS NOT NULL THEN true ELSE id_card_valid END,
		     reviewed_by = $2, reviewed_at = now(), review_note = $3,
		     failed_login_count = 0, last_failed_at = NULL, locked_until = NULL,
		     updated_at = now()
		 WHERE id = $1`, userID, actorID, sanitizeText(note, 500)); err != nil {
		return "", fmt.Errorf("db: approve user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("db: commit approve user: %w", err)
	}
	return previous, nil
}

// RejectAdminUser marks the account rejected and revokes its sessions so a
// logged-in user cannot keep browsing after a rejection.
func (d *DB) RejectAdminUser(ctx context.Context, userID, actorID int64, note string) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("db: begin reject user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var previous string
	err = tx.QueryRow(ctx,
		`SELECT account_status FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load user for rejection: %w", err)
	}
	var isSuper bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM admin_user_roles ur
		   JOIN admin_roles r ON r.id = ur.role_id
		   WHERE ur.user_id = $1 AND r.code = $2
		 )`, userID, adminSuperAdminRoleCode).Scan(&isSuper); err != nil {
		return "", fmt.Errorf("db: check rejected super admin: %w", err)
	}
	if isSuper {
		var roleID int64
		if err := tx.QueryRow(ctx,
			`SELECT id FROM admin_roles WHERE code = $1 FOR UPDATE`,
			adminSuperAdminRoleCode).Scan(&roleID); err != nil {
			return "", fmt.Errorf("db: lock super admin role: %w", err)
		}
		var others int64
		if err := tx.QueryRow(ctx,
			`SELECT count(DISTINCT u.id)
			 FROM users u
			 JOIN admin_user_roles ur ON ur.user_id = u.id
			 JOIN admin_roles r ON r.id = ur.role_id
			 WHERE r.code = $1 AND u.status = 'active' AND u.id <> $2`,
			adminSuperAdminRoleCode, userID).Scan(&others); err != nil {
			return "", fmt.Errorf("db: count active super admins: %w", err)
		}
		if others == 0 {
			return "", ErrLastSuperAdmin
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users
		 SET status = 'disabled', account_status = 'rejected', reviewed_by = $2,
		     reviewed_at = now(), review_note = $3, updated_at = now()
		 WHERE id = $1`, userID, actorID, sanitizeText(note, 500)); err != nil {
		return "", fmt.Errorf("db: reject user: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return "", fmt.Errorf("db: revoke rejected user sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("db: commit reject user: %w", err)
	}
	return previous, nil
}

// SetAdminUserEnabled disables or re-enables an account. Disabling the last
// active super_admin is refused so the console cannot lock everyone out.
func (d *DB) SetAdminUserEnabled(ctx context.Context, userID, actorID int64, enabled bool) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("db: begin toggle user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountStatus, loginStatus string
	err = tx.QueryRow(ctx,
		`SELECT account_status, status FROM users WHERE id = $1 FOR UPDATE`,
		userID).Scan(&accountStatus, &loginStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load user for toggle: %w", err)
	}

	if enabled && accountStatus == "pending" {
		return "", ErrAdminUserPending
	}
	if enabled && accountStatus == "rejected" {
		return "", ErrAdminUserRejected
	}

	if !enabled {
		var isSuper bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM admin_user_roles ur
			   JOIN admin_roles r ON r.id = ur.role_id
			   WHERE ur.user_id = $1 AND r.code = $2
			 )`, userID, adminSuperAdminRoleCode).Scan(&isSuper); err != nil {
			return "", fmt.Errorf("db: check target super admin: %w", err)
		}
		if isSuper && loginStatus == "active" {
			// Serialise with role removal so two concurrent requests cannot
			// leave the system without an active super_admin.
			var roleID int64
			if err := tx.QueryRow(ctx,
				`SELECT id FROM admin_roles WHERE code = $1 FOR UPDATE`,
				adminSuperAdminRoleCode).Scan(&roleID); err != nil {
				return "", fmt.Errorf("db: lock super admin role: %w", err)
			}
			var others int64
			if err := tx.QueryRow(ctx,
				`SELECT count(DISTINCT u.id)
				 FROM users u
				 JOIN admin_user_roles ur ON ur.user_id = u.id
				 JOIN admin_roles r ON r.id = ur.role_id
				 WHERE r.code = $1 AND u.status = 'active' AND u.id <> $2`,
				adminSuperAdminRoleCode, userID).Scan(&others); err != nil {
				return "", fmt.Errorf("db: count active super admins: %w", err)
			}
			if others == 0 {
				return "", ErrLastSuperAdmin
			}
		}
	}

	newLogin := "active"
	if !enabled {
		newLogin = "disabled"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET status = $2, updated_at = now()
		 WHERE id = $1`, userID, newLogin); err != nil {
		return "", fmt.Errorf("db: toggle user: %w", err)
	}
	if !enabled {
		if _, err := tx.Exec(ctx,
			`UPDATE sessions SET revoked_at = now()
			 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return "", fmt.Errorf("db: revoke disabled user sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("db: commit toggle user: %w", err)
	}
	return loginStatus, nil
}

// AdminSetTempPassword stores a hashed temporary password and forces the user
// to change it on next login. Existing sessions are revoked.
func (d *DB) AdminSetTempPassword(ctx context.Context, userID int64, passwordHash string, expiresAt time.Time, actorID int64) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin temp password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("db: check user for temp password: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO auth_credentials
		   (user_id, password_hash, must_change_password, temp_expires_at, created_by)
		 VALUES ($1, $2, true, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE
		   SET password_hash = EXCLUDED.password_hash,
		       must_change_password = true,
		       temp_expires_at = EXCLUDED.temp_expires_at,
		       session_version = auth_credentials.session_version + 1,
		       created_by = EXCLUDED.created_by,
		       updated_at = now()`,
		userID, passwordHash, expiresAt, actorID); err != nil {
		return fmt.Errorf("db: set temp password: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("db: revoke temp password sessions: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users
		 SET failed_login_count = 0, last_failed_at = NULL, locked_until = NULL,
		     updated_at = now()
		 WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("db: reset temp password locks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit temp password: %w", err)
	}
	return nil
}

// GetCredentialState reports the forced password-change flag. ErrNotFound means
// the account has no credential yet.
func (d *DB) GetCredentialState(ctx context.Context, userID int64) (bool, *time.Time, error) {
	var mustChange bool
	var expiresAt *time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT must_change_password, temp_expires_at
		 FROM auth_credentials WHERE user_id = $1`, userID).Scan(&mustChange, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, ErrNotFound
	}
	if err != nil {
		return false, nil, fmt.Errorf("db: get credential state: %w", err)
	}
	return mustChange, expiresAt, nil
}

// ChangePassword replaces the credential and clears the forced-change flag.
// Other sessions are revoked; the caller's current session stays valid.
func (d *DB) ChangePassword(ctx context.Context, userID int64, passwordHash string, currentSessionID int64) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin change password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE auth_credentials
		 SET password_hash = $2, must_change_password = false,
		     temp_expires_at = NULL, session_version = session_version + 1,
		     updated_at = now()
		 WHERE user_id = $1`, userID, passwordHash)
	if err != nil {
		return fmt.Errorf("db: update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL AND ($2 = 0 OR id <> $2)`,
		userID, currentSessionID); err != nil {
		return fmt.Errorf("db: revoke old password sessions: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET failed_login_count = 0, last_failed_at = NULL,
		     locked_until = NULL, updated_at = now()
		 WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("db: reset password locks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit change password: %w", err)
	}
	return nil
}

// ListAdminRoles returns every role with permissions and member counts.
func (d *DB) ListAdminRoles(ctx context.Context) ([]AdminRole, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT r.id, r.code, r.name,
		        COALESCE((SELECT array_agg(rp.permission ORDER BY rp.permission)
		                  FROM admin_role_permissions rp
		                  WHERE rp.role_id = r.id), '{}'::text[]),
		        (SELECT count(*) FROM admin_user_roles ur WHERE ur.role_id = r.id)
		 FROM admin_roles r
		 ORDER BY CASE r.code WHEN 'super_admin' THEN 0 ELSE 1 END, r.code`)
	if err != nil {
		return nil, fmt.Errorf("db: list admin roles: %w", err)
	}
	defer rows.Close()
	out := make([]AdminRole, 0)
	for rows.Next() {
		var r AdminRole
		if err := rows.Scan(&r.ID, &r.Code, &r.Name, &r.Permissions, &r.UserCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAdminAssignments returns users currently holding admin roles.
func (d *DB) ListAdminAssignments(ctx context.Context) ([]AdminAssignment, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT u.id, COALESCE(u.legacy_user_id, ''), u.name, u.phone,
		        array_agg(r.code ORDER BY r.code)
		 FROM admin_user_roles ur
		 JOIN users u ON u.id = ur.user_id
		 JOIN admin_roles r ON r.id = ur.role_id
		 GROUP BY u.id, u.legacy_user_id, u.name, u.phone
		 ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("db: list admin assignments: %w", err)
	}
	defer rows.Close()
	out := make([]AdminAssignment, 0)
	for rows.Next() {
		var a AdminAssignment
		if err := rows.Scan(&a.UserID, &a.LegacyUserID, &a.Name, &a.Phone, &a.Roles); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssignAdminRole grants or removes one role. Removing super_admin from the
// last holder is rejected inside a transaction that locks the role row.
func (d *DB) AssignAdminRole(ctx context.Context, userID int64, roleCode string, assign bool) (bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("db: begin assign role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var roleID int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM admin_roles WHERE code = $1 FOR UPDATE`, roleCode).Scan(&roleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("db: load admin role: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("db: check role user: %w", err)
	}
	if !exists {
		return false, ErrNotFound
	}

	changed := false
	if assign {
		tag, err := tx.Exec(ctx,
			`INSERT INTO admin_user_roles (user_id, role_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, userID, roleID)
		if err != nil {
			return false, fmt.Errorf("db: assign admin role: %w", err)
		}
		changed = tag.RowsAffected() > 0
	} else {
		var assigned bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM admin_user_roles WHERE user_id = $1 AND role_id = $2
			 )`, userID, roleID).Scan(&assigned); err != nil {
			return false, fmt.Errorf("db: check role assignment: %w", err)
		}
		if assigned && roleCode == adminSuperAdminRoleCode {
			var activeOthers int64
			if err := tx.QueryRow(ctx,
				`SELECT count(*)
				 FROM admin_user_roles ur
				 JOIN users u ON u.id = ur.user_id
				 WHERE ur.role_id = $1 AND u.status = 'active' AND u.id <> $2`,
				roleID, userID).Scan(&activeOthers); err != nil {
				return false, fmt.Errorf("db: count active super admins: %w", err)
			}
			if activeOthers == 0 {
				return false, ErrLastSuperAdmin
			}
		}
		if assigned {
			if _, err := tx.Exec(ctx,
				`DELETE FROM admin_user_roles WHERE user_id = $1 AND role_id = $2`,
				userID, roleID); err != nil {
				return false, fmt.Errorf("db: remove admin role: %w", err)
			}
			changed = true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("db: commit assign role: %w", err)
	}
	return changed, nil
}

// HandleAdminReport resolves one report. action is one of AdminReportDelete,
// AdminReportDismiss or AdminReportResolve.
func (d *DB) HandleAdminReport(ctx context.Context, reportID, handlerID int64, action string) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("db: begin handle admin report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var targetMessageID *int64
	var status string
	err = tx.QueryRow(ctx,
		`SELECT target_message_id, status FROM reports WHERE id = $1 FOR UPDATE`,
		reportID).Scan(&targetMessageID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load admin report: %w", err)
	}
	if status != ReportStatusPending {
		return "", ErrConflict
	}

	next := ReportStatusPending
	switch action {
	case AdminReportDelete:
		if targetMessageID == nil {
			return "", ErrAdminReportAction
		}
		next = ReportStatusApproved
		if _, err := tx.Exec(ctx,
			`UPDATE messages SET status = $2 WHERE id = $1`,
			*targetMessageID, MessageStatusDeleted); err != nil {
			return "", fmt.Errorf("db: delete reported message: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`WITH RECURSIVE doomed AS (
			   SELECT id FROM messages WHERE parent_id = $1 AND status <> $2
			   UNION ALL
			   SELECT m.id FROM messages m JOIN doomed d ON m.parent_id = d.id WHERE m.status <> $2
			 )
			 UPDATE messages SET status = $2 WHERE id IN (SELECT id FROM doomed)`,
			*targetMessageID, MessageStatusDeleted); err != nil {
			return "", fmt.Errorf("db: cascade reported replies: %w", err)
		}
	case AdminReportDismiss:
		next = ReportStatusRejected
		if targetMessageID != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE messages SET status = $2 WHERE id = $1 AND status = $3`,
				*targetMessageID, MessageStatusNormal, MessageStatusReported); err != nil {
				return "", fmt.Errorf("db: restore dismissed message: %w", err)
			}
		}
	case AdminReportResolve:
		if targetMessageID != nil {
			return "", ErrAdminReportAction
		}
		next = ReportStatusApproved
	default:
		return "", ErrAdminReportAction
	}

	if _, err := tx.Exec(ctx,
		`UPDATE reports
		 SET status = $2, handled_by = $3, handled_at = now()
		 WHERE id = $1`, reportID, next, handlerID); err != nil {
		return "", fmt.Errorf("db: update admin report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("db: commit admin report: %w", err)
	}
	return next, nil
}

// ListAdminSuggestions returns feedback rows, optionally filtered by status.
func (d *DB) ListAdminSuggestions(ctx context.Context, status string, limit int) ([]AdminSuggestion, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.pool.Query(ctx,
		`SELECT id, author_id, author_name, type, content, status, created_at
		 FROM suggestions
		 WHERE ($1 = '' OR status = $1)
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list admin suggestions: %w", err)
	}
	defer rows.Close()
	out := make([]AdminSuggestion, 0)
	for rows.Next() {
		var s AdminSuggestion
		if err := rows.Scan(&s.ID, &s.AuthorID, &s.AuthorName, &s.Type,
			&s.Content, &s.Status, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// HandleAdminSuggestion marks feedback handled or ignored.
func (d *DB) HandleAdminSuggestion(ctx context.Context, suggestionID int64, status string) error {
	if status != AdminSuggestionHandled && status != AdminSuggestionIgnored {
		return ErrAdminSuggestionStatus
	}
	tag, err := d.pool.Exec(ctx,
		`UPDATE suggestions SET status = $2 WHERE id = $1`, suggestionID, status)
	if err != nil {
		return fmt.Errorf("db: handle suggestion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
