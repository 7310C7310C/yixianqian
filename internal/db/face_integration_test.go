package db

import (
	"context"
	"errors"
	"fmt"
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

// staging 的批量压测账号可能共用同一 media。回填候选必须按 media_id 去重，
// 否则同一张照片会被下载/probe 20 次；SetProfilePhotoFace 则应一次更新所有
// 引用该 media 的 profile_photos 行。
func TestIntegrationFaceBackfillSharedMediaDedupe(t *testing.T) {
	ctx, database, owner, _ := faceTestSetup(t)

	const clones = 20
	shared := profileTestMedia(t, ctx, database, owner.ID)
	if _, err := database.AddProfilePhoto(ctx, owner.ID, shared.ID); err != nil {
		t.Fatalf("add shared photo to owner: %v", err)
	}
	cloneUsers := make([]int64, 0, clones-1)
	for i := 0; i < clones-1; i++ {
		clone := profileTestUser(t, ctx, database, fmt.Sprintf("U-FACECLONE-%02d", i))
		cloneUsers = append(cloneUsers, clone.ID)
		// 媒体属于 owner；这里直接插入 profile_photos 模拟共享 media 的历史数据。
		if _, err := database.pool.Exec(ctx,
			`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
			 VALUES ($1, $2, 0, true)`, clone.ID, shared.ID); err != nil {
			t.Fatalf("attach shared photo to clone %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		for _, id := range cloneUsers {
			_, _ = database.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
		}
	})

	countShared := func(candidates []FaceBackfillCandidate) int {
		n := 0
		for _, c := range candidates {
			if c.MediaID == shared.ID {
				n++
			}
		}
		return n
	}

	// unchecked 模式：20 行未检测，但候选只能有 1 条。
	unchecked, err := database.ListFaceBackfillCandidates(ctx, 0, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := countShared(unchecked); got != 1 {
		t.Fatalf("shared media unchecked candidates = %d, want 1", got)
	}
	// -all 模式同样去重。
	allCandidates, err := database.ListFaceBackfillCandidates(ctx, 0, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := countShared(allCandidates); got != 1 {
		t.Fatalf("shared media all candidates = %d, want 1", got)
	}

	// 一次检测结果必须落到全部 20 行。
	if err := database.SetProfilePhotoFace(ctx, shared.ID, ProfilePhotoFace{
		Checked: true, HasFace: true,
		CenterX: floatPtr(0.5), CenterY: floatPtr(0.4),
		BoxW: floatPtr(0.2), BoxH: floatPtr(0.3),
	}); err != nil {
		t.Fatal(err)
	}
	var total, checked, withFace int
	if err := database.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE face_checked),
		        count(*) FILTER (WHERE has_face)
		   FROM profile_photos WHERE media_id = $1`, shared.ID).
		Scan(&total, &checked, &withFace); err != nil {
		t.Fatal(err)
	}
	if total != clones || checked != clones || withFace != clones {
		t.Fatalf("shared rows total=%d checked=%d has_face=%d, want %d/%d/%d",
			total, checked, withFace, clones, clones, clones)
	}
	if has, err := database.ProfileHasFace(ctx, cloneUsers[0]); err != nil || !has {
		t.Fatalf("clone ProfileHasFace = %v err=%v, want true", has, err)
	}

	// 全部 checked 后，未检测候选不再包含该 media。
	unchecked, err = database.ListFaceBackfillCandidates(ctx, 0, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := countShared(unchecked); got != 0 {
		t.Fatalf("checked shared media candidates = %d, want 0", got)
	}
}

func TestIntegrationFaceBackfillAdvisoryLock(t *testing.T) {
	ctx, database, _, _ := faceTestSetup(t)

	first, acquired, err := database.TryFaceBackfillLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || first == nil {
		t.Fatal("first backfill lock should be acquired")
	}
	defer first.Release(ctx)

	second, acquired, err := database.TryFaceBackfillLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if acquired || second != nil {
		if second != nil {
			second.Release(ctx)
		}
		t.Fatal("second backfill lock must not be acquired concurrently")
	}

	first.Release(ctx)
	third, acquired, err := database.TryFaceBackfillLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || third == nil {
		t.Fatal("lock should be acquirable after release")
	}
	third.Release(ctx)
}

func TestIntegrationSetProfilePhotoFaceNotFound(t *testing.T) {
	ctx, database, _, _ := faceTestSetup(t)
	err := database.SetProfilePhotoFace(ctx, "missing-media", ProfilePhotoFace{Checked: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
