package db

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound          = errors.New("db: not found")
	ErrConflict          = errors.New("db: conflict")
	ErrInvalidActivation = errors.New("db: activation token is invalid, expired or already used")
	ErrAlreadyActivated  = errors.New("db: account already has a password")
	ErrPhoneConflict     = errors.New("db: phone already belongs to another account")
	ErrIDCardConflict    = errors.New("db: id card already belongs to another account")
	ErrAccountDisabled   = errors.New("db: account is disabled")

	ErrLastSuperAdmin         = errors.New("db: cannot remove or disable the last super admin")
	ErrAdminPrivileged        = errors.New("db: actor may not manage this admin account")
	ErrAdminUserNotReviewable = errors.New("db: user is not awaiting review")
	ErrAdminUserPending       = errors.New("db: pending user must be reviewed before enabling")
	ErrAdminUserRejected      = errors.New("db: rejected user must be approved before enabling")
	ErrAdminReportAction      = errors.New("db: invalid report action")
	ErrAdminSuggestionStatus  = errors.New("db: invalid suggestion status")
)

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_phone_key":
			return fmt.Errorf("%w: %s", ErrPhoneConflict, pgErr.ConstraintName)
		case "users_id_card_hash_uidx":
			return fmt.Errorf("%w: %s", ErrIDCardConflict, pgErr.ConstraintName)
		case "auth_credentials_pkey":
			return fmt.Errorf("%w: %s", ErrAlreadyActivated, pgErr.ConstraintName)
		default:
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.ConstraintName)
		}
	}
	return err
}
