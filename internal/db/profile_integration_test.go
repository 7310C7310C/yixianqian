package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

func profileStringPtr(v string) *string { return &v }
func profileIntPtr(v int) *int          { return &v }
func profileListPtr(v ...string) *[]string {
	out := append([]string(nil), v...)
	return &out
}

func profileTestUser(t *testing.T, ctx context.Context, database *DB, prefix string) User {
	t.Helper()
	legacy := prefix + "-" + randomDigits(t, 10)
	phone := "1" + randomDigits(t, 10)
	user, err := database.UpsertLegacyUser(ctx, legacy, phone, "测试用户")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET account_status = 'active' WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	return user
}

func profileTestMedia(t *testing.T, ctx context.Context, database *DB, userID int64) Media {
	t.Helper()
	id, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	media, err := database.CreateMedia(ctx, Media{
		ID:           id,
		UserID:       userID,
		OriginalName: "photo.jpg",
		ObjectKey:    fmt.Sprintf("media/%d/%s.jpg", userID, id),
		ThumbKey:     fmt.Sprintf("media/%d/%s_thumb.jpg", userID, id),
		ContentType:  "image/jpeg",
		ByteSize:     1024,
		ThumbSize:    256,
		Width:        800,
		Height:       600,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	return media
}

func TestIntegrationProfileUpdateAndPhotos(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	database, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	owner := profileTestUser(t, ctx, database, "U-PROFILE")
	other := profileTestUser(t, ctx, database, "U-OTHER")

	profile, err := database.GetProfile(ctx, owner.ID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.ID != owner.ID || profile.LegacyUserID != *owner.LegacyUserID {
		t.Fatalf("unexpected profile identity: %+v", profile)
	}
	if profile.AccountStatus != "active" {
		t.Fatalf("account status = %q", profile.AccountStatus)
	}

	phone := "1" + randomDigits(t, 10)
	formattedPhone := "+86 " + phone[:3] + "-" + phone[3:7] + "-" + phone[7:]
	upd := ProfileUpdate{
		Nickname:        profileStringPtr("小美"),
		HeightCM:        profileIntPtr(165),
		Education:       profileStringPtr("本科"),
		City:            profileStringPtr("上海"),
		Church:          profileStringPtr("某教堂"),
		ChurchGroup:     profileStringPtr("青年团"),
		ChurchLocation:  profileStringPtr("上海-浦东"),
		NativePlace:     profileStringPtr("福建"),
		Industry:        profileStringPtr("教育"),
		Position:        profileStringPtr("老师"),
		Income:          profileStringPtr("20W - 30W"),
		House:           profileStringPtr("有"),
		Driving:         profileStringPtr("无"),
		Personality:     profileStringPtr("喜欢安静"),
		Family:          profileStringPtr("三口之家"),
		LiveWithParents: profileStringPtr("独立生活"),
		PartnerCriteria: profileStringPtr("善良"),
		SelfTraits:      profileListPtr("喜欢安静", "正直"),
		SelfHobbies:     profileListPtr("看书"),
		SelfSports:      profileListPtr("跑步"),
		MBTI:            profileListPtr("I", "N"),
		PartnerTraits:   profileListPtr("正直"),
		PartnerHobbies:  profileListPtr("看电影"),
		PartnerSports:   profileListPtr("游泳"),
		Wechat:          profileStringPtr("wx-profile-test"),
		Phone:           profileStringPtr(formattedPhone),
	}
	updated, err := database.UpdateProfile(ctx, owner.ID, upd)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.Nickname != "小美" || updated.HeightCM == nil || *updated.HeightCM != 165 {
		t.Fatalf("basic update not applied: %+v", updated)
	}
	if updated.Education != "本科" || updated.City != "上海" || updated.NativePlace != "福建" {
		t.Fatalf("text update not applied: %+v", updated)
	}
	if updated.Phone != phone {
		t.Fatalf("phone = %q, want normalized %q", updated.Phone, phone)
	}
	if len(updated.SelfTraits) != 2 || updated.SelfTraits[1] != "正直" {
		t.Fatalf("self traits = %v", updated.SelfTraits)
	}
	if updated.Name != "测试用户" {
		t.Fatalf("real name must not change, got %q", updated.Name)
	}

	// Invalid values are rejected before SQL runs and leave the row intact.
	if _, err := database.UpdateProfile(ctx, owner.ID, ProfileUpdate{
		HeightCM: profileIntPtr(999),
	}); err == nil {
		t.Fatal("height 999 was accepted")
	} else {
		var invalid *ValidationError
		if !errors.As(err, &invalid) || invalid.Field != "height_cm" {
			t.Fatalf("height error = %v", err)
		}
	}
	if _, err := database.UpdateProfile(ctx, owner.ID, ProfileUpdate{
		Education: profileStringPtr("火星"),
	}); err == nil {
		t.Fatal("unknown education option was accepted")
	}
	if _, err := database.UpdateProfile(ctx, owner.ID, ProfileUpdate{
		Nickname: profileStringPtr("   "),
	}); err == nil {
		t.Fatal("blank nickname was accepted")
	}
	if _, err := database.UpdateProfile(ctx, owner.ID, ProfileUpdate{
		SelfTraits: profileListPtr("不存在的选项"),
	}); err == nil {
		t.Fatal("unknown multi option was accepted")
	}
	reloaded, err := database.GetProfile(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Nickname != "小美" || reloaded.Education != "本科" {
		t.Fatalf("invalid updates changed the row: %+v", reloaded)
	}

	if _, err := database.UpdateProfile(ctx, owner.ID, ProfileUpdate{
		Phone: profileStringPtr(other.Phone),
	}); !errors.Is(err, ErrPhoneConflict) {
		t.Fatalf("phone conflict error = %v", err)
	}

	// Photo ownership and ordering.
	var photos []ProfilePhoto
	var mediaRows []Media
	for i := 0; i < MaxProfilePhotos; i++ {
		media := profileTestMedia(t, ctx, database, owner.ID)
		mediaRows = append(mediaRows, media)
		photo, err := database.AddProfilePhoto(ctx, owner.ID, media.ID)
		if err != nil {
			t.Fatalf("add photo %d: %v", i, err)
		}
		if photo.IsCover != (i == 0) {
			t.Fatalf("photo %d cover = %v", i, photo.IsCover)
		}
		if photo.SortOrder != i {
			t.Fatalf("photo %d order = %d", i, photo.SortOrder)
		}
		photos = append(photos, photo)
	}
	if _, err := database.AddProfilePhoto(ctx, owner.ID, profileTestMedia(t, ctx, database, owner.ID).ID); !errors.Is(err, ErrPhotoLimit) {
		t.Fatalf("tenth photo error = %v", err)
	}
	if _, err := database.AddProfilePhoto(ctx, other.ID, mediaRows[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attaching another user's media error = %v", err)
	}

	reordered, err := database.SetProfilePhotoCover(ctx, owner.ID, photos[2].MediaID)
	if err != nil {
		t.Fatalf("set cover: %v", err)
	}
	if len(reordered) != MaxProfilePhotos || reordered[0].MediaID != photos[2].MediaID || !reordered[0].IsCover {
		t.Fatalf("cover was not moved first: %+v", reordered)
	}
	covers := 0
	for _, photo := range reordered {
		if photo.IsCover {
			covers++
		}
	}
	if covers != 1 {
		t.Fatalf("cover count = %d", covers)
	}

	if _, err := database.DeleteProfilePhoto(ctx, other.ID, photos[0].MediaID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner delete error = %v", err)
	}
	deleted, err := database.DeleteProfilePhoto(ctx, owner.ID, photos[0].MediaID)
	if err != nil {
		t.Fatalf("delete photo: %v", err)
	}
	if deleted.ID != mediaRows[0].ID {
		t.Fatalf("deleted media = %q", deleted.ID)
	}
	if _, err := database.GetProfilePhotoFile(ctx, photos[0].MediaID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted photo still resolvable: %v", err)
	}
	remaining, err := database.ProfilePhotos(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != MaxProfilePhotos-1 || !remaining[0].IsCover {
		t.Fatalf("remaining photos = %+v", remaining)
	}

	// Account status rules.
	status, err := database.SetAccountStatus(ctx, owner.ID, "matched")
	if err != nil || status != "matched" {
		t.Fatalf("switch to matched: %q %v", status, err)
	}
	status, err = database.SetAccountStatus(ctx, owner.ID, "active")
	if err != nil || status != "active" {
		t.Fatalf("switch to active: %q %v", status, err)
	}

	activityNo := "A-PROFILE-" + randomDigits(t, 10)
	var activityID int64
	if err := database.pool.QueryRow(ctx,
		`INSERT INTO activities (activity_no, name) VALUES ($1, '测试活动') RETURNING id`,
		activityNo).Scan(&activityID); err != nil {
		t.Fatalf("create activity: %v", err)
	}
	if _, err := database.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, status) VALUES ($1, $2, '已报名')`,
		activityID, owner.ID); err != nil {
		t.Fatalf("create signup: %v", err)
	}
	if _, err := database.SetAccountStatus(ctx, owner.ID, "matched"); !errors.Is(err, ErrActiveSignupBlocked) {
		t.Fatalf("active signup error = %v", err)
	}
	if _, err := database.pool.Exec(ctx,
		`UPDATE signups SET status = '已取消' WHERE activity_id = $1 AND user_id = $2`,
		activityID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if status, err := database.SetAccountStatus(ctx, owner.ID, "matched"); err != nil || status != "matched" {
		t.Fatalf("switch after cancel: %q %v", status, err)
	}

	observer := profileTestUser(t, ctx, database, "U-OBSERVER")
	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET account_kind = 'observer', account_status = 'observer' WHERE id = $1`,
		observer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetAccountStatus(ctx, observer.ID, "active"); !errors.Is(err, ErrObserverNoSwitch) {
		t.Fatalf("observer switch error = %v", err)
	}
	if _, err := database.SetAccountStatus(ctx, owner.ID, "pending"); err == nil {
		t.Fatal("invalid status target was accepted")
	}
}
