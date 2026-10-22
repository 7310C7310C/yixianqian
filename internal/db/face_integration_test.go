package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

func faceTestSetup(t *testing.T) (context.Context, *DB, User, []Media) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	user := profileTestUser(t, ctx, database, "U-FACE")
	first := profileTestMedia(t, ctx, database, user.ID)
	second := profileTestMedia(t, ctx, database, user.ID)
	if _, err := database.AddProfilePhoto(ctx, user.ID, first.ID); err != nil {
		t.Fatalf("add first photo: %v", err)
	}
	if _, err := database.AddProfilePhoto(ctx, user.ID, second.ID); err != nil {
		t.Fatalf("add second photo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return ctx, database, user, []Media{first, second}
}

func TestIntegrationProfilePhotoFace(t *testing.T) {
	ctx, database, user, _ := faceTestSetup(t)

	has, err := database.ProfileHasFace(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("profile with no checked photos must not report has_face")
	}

	faces, err := database.FaceCentersForProfile(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 2 || faces[0].Checked {
		t.Fatalf("initial faces = %+v", faces)
	}

	photos, err := database.ProfilePhotos(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	coverID := photos[0].MediaID
	otherID := photos[1].MediaID

	if err := database.SetProfilePhotoFace(ctx, coverID, ProfilePhotoFace{
		Checked: true, HasFace: true,
		CenterX: floatPtr(0.5), CenterY: floatPtr(0.4),
		BoxW: floatPtr(0.2), BoxH: floatPtr(0.3),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetProfilePhotoFace(ctx, otherID, ProfilePhotoFace{
		Checked: true, HasFace: false,
	}); err != nil {
		t.Fatal(err)
	}

	has, err = database.ProfileHasFace(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("profile should have a usable face")
	}

	faces, err = database.FaceCentersForProfile(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 2 {
		t.Fatalf("faces = %+v", faces)
	}
	var detected *ProfilePhotoFace
	for i := range faces {
		if faces[i].MediaID == coverID {
			detected = &faces[i]
		}
	}
	if detected == nil || !detected.Checked || !detected.HasFace {
		t.Fatalf("detected face = %+v", detected)
	}
	if detected.CenterX == nil || math.Abs(*detected.CenterX-0.5) > 0.001 ||
		detected.BoxH == nil || math.Abs(*detected.BoxH-0.3) > 0.001 {
		t.Fatalf("face geometry = %+v", detected)
	}

	batch, err := database.FaceCentersForProfiles(ctx, []int64{user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch[user.ID]) != 2 {
		t.Fatalf("batch faces = %+v", batch)
	}

	candidates, err := database.ListFaceBackfillCandidates(ctx, 0, false, *user.LegacyUserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("checked photos should not be backfilled: %+v", candidates)
	}
	candidates, err = database.ListFaceBackfillCandidates(ctx, 0, true, *user.LegacyUserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("include checked candidates = %d, want 2", len(candidates))
	}
	if candidates[0].ObjectKey == "" || candidates[0].ContentType != "image/jpeg" {
		t.Fatalf("candidate = %+v", candidates[0])
	}

}

func TestIntegrationSetProfilePhotoFaceNotFound(t *testing.T) {
	ctx, database, _, _ := faceTestSetup(t)
	err := database.SetProfilePhotoFace(ctx, "missing-media", ProfilePhotoFace{Checked: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
