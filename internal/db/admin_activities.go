package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminActivityInput is the editable subset of an activity. CurrentSignup is
// intentionally not editable; it is maintained by signup/cancel transactions.
type AdminActivityInput struct {
	Name            string
	Description     string
	Location        string
	Condition       string
	MaxSignup       int
	Fee             string
	Food            string
	Status          string
	GroupEnabled    bool
	MalePerGroup    int
	FemalePerGroup  int
	OnetooneEnabled bool
	SigninEnabled   bool
	SigninCode      string
	PublishAt       *time.Time
	StartAt         *time.Time
	EndAt           *time.Time
}

func (in AdminActivityInput) clean() AdminActivityInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Location = strings.TrimSpace(in.Location)
	in.Condition = strings.TrimSpace(in.Condition)
	in.Fee = strings.TrimSpace(in.Fee)
	in.Food = strings.TrimSpace(in.Food)
	in.Status = strings.TrimSpace(in.Status)
	in.SigninCode = strings.TrimSpace(in.SigninCode)
	return in
}

func (d *DB) nextActivityNo(ctx context.Context, tx pgx.Tx) (string, error) {
	var next int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX((regexp_replace(activity_no, '\D', '', 'g'))::int), 0) + 1
		FROM activities
		WHERE activity_no ~ '^A-[0-9]+$'`).Scan(&next); err != nil {
		return "", fmt.Errorf("db: next activity no: %w", err)
	}
	return fmt.Sprintf("A-%04d", next), nil
}

func scanAdminActivity(row rowScanner) (Activity, error) { return scanActivity(row) }

// CreateActivity inserts an activity and returns the persisted row.
func (d *DB) CreateActivity(ctx context.Context, in AdminActivityInput) (Activity, error) {
	in = in.clean()
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Activity{}, fmt.Errorf("db: begin activity: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	no, err := d.nextActivityNo(ctx, tx)
	if err != nil {
		return Activity{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO activities (
			activity_no, name, description, location, condition, max_signup,
			fee, food, status, group_enabled, male_per_group, female_per_group,
			onetoone_enabled, signin_enabled, signin_code, publish_at, start_at, end_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+qualifiedActivityColumns("activities"),
		no, in.Name, in.Description, in.Location, in.Condition, in.MaxSignup,
		in.Fee, in.Food, in.Status, in.GroupEnabled, in.MalePerGroup, in.FemalePerGroup,
		in.OnetooneEnabled, in.SigninEnabled, in.SigninCode, in.PublishAt, in.StartAt, in.EndAt)
	activity, err := scanAdminActivity(row)
	if err != nil {
		return Activity{}, fmt.Errorf("db: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Activity{}, fmt.Errorf("db: commit activity: %w", err)
	}
	return activity, nil
}

// UpdateActivity updates the editable fields; current_signup is preserved.
func (d *DB) UpdateActivity(ctx context.Context, id int64, in AdminActivityInput) (Activity, error) {
	in = in.clean()
	row := d.pool.QueryRow(ctx, `
		UPDATE activities SET
			name=$2, description=$3, location=$4, condition=$5, max_signup=$6,
			fee=$7, food=$8, status=$9, group_enabled=$10, male_per_group=$11,
			female_per_group=$12, onetoone_enabled=$13, signin_enabled=$14,
			signin_code=$15, publish_at=$16, start_at=$17, end_at=$18,
			updated_at=now()
		WHERE id=$1
		RETURNING `+qualifiedActivityColumns("activities"),
		id, in.Name, in.Description, in.Location, in.Condition, in.MaxSignup,
		in.Fee, in.Food, in.Status, in.GroupEnabled, in.MalePerGroup, in.FemalePerGroup,
		in.OnetooneEnabled, in.SigninEnabled, in.SigninCode, in.PublishAt, in.StartAt, in.EndAt)
	activity, err := scanAdminActivity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("db: update activity: %w", err)
	}
	return activity, nil
}
