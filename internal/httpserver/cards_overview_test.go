package httpserver

import (
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestBuildCardOverviewItem(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	height := 168
	birthday := time.Date(1996, 1, 1, 0, 0, 0, 0, time.UTC)
	profile := db.SocialProfile{
		ID:       42,
		LegacyID: "U-0042",
		Nickname: "阿明",
		Gender:   "女性",
		Birthday: &birthday,
		HeightCM: &height,
		City:     "广东深圳",
		Photos:   []string{"media-1", "media-2"},
	}
	item := buildCardOverviewItem(profile, now.Add(-3*24*time.Hour), now)
	if item.OpenID != "U-0042" || item.UserID != "U-0042" || item.Nickname != "阿明" {
		t.Fatalf("identity mismatch: %+v", item)
	}
	if item.Age != 30 || item.Height != 168 || item.City != "广东深圳" {
		t.Fatalf("profile fields mismatch: %+v", item)
	}
	if want := "/cards/photo/media-1?size=thumb"; item.Photo != want {
		t.Fatalf("photo = %q, want %q (cover is first)", item.Photo, want)
	}
	if len(item.Photos) != 2 || item.Photos[0] != item.Photo || item.Photos[1] != "/cards/photo/media-2?size=thumb" {
		t.Fatalf("photos = %v, want both thumbs with cover first", item.Photos)
	}
	if !item.IsNew {
		t.Fatal("3 天前注册应标新")
	}
	if got := buildCardOverviewItem(profile, now.Add(-cardOverviewNewWindow-time.Second), now); got.IsNew {
		t.Fatal("超过 14 天不应标新")
	}
	if got := buildCardOverviewItem(profile, time.Time{}, now); got.IsNew {
		t.Fatal("未知注册时间不应标新")
	}
	if got := buildCardOverviewItem(db.SocialProfile{}, now, now); got.Photo != "" || got.Nickname != "未命名" {
		t.Fatalf("空资料兜底错误: %+v", got)
	}
}
