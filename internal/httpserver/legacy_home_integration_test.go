package httpserver

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func attachTestPhoto(t *testing.T, env *profileHTTPEnv, userID int64) string {
	t.Helper()
	suffix := randomHTTPDigits(t, 12)
	mediaID := "m-home-" + suffix
	if _, err := env.database.CreateMedia(env.ctx, db.Media{
		ID:           mediaID,
		UserID:       userID,
		OriginalName: "photo.jpg",
		ObjectKey:    fmt.Sprintf("media/%d/%s.jpg", userID, mediaID),
		ThumbKey:     fmt.Sprintf("media/%d/%s_thumb.jpg", userID, mediaID),
		ContentType:  "image/jpeg",
		ByteSize:     1000,
		ThumbSize:    100,
		Width:        600,
		Height:       800,
	}); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, 0, true)`, userID, mediaID); err != nil {
		t.Fatalf("create profile photo: %v", err)
	}
	return mediaID
}

type legacyHomeCard struct {
	OpenID   string            `json:"openid"`
	Nickname string            `json:"nickname"`
	IsSelf   bool              `json:"is_self"`
	Liked    bool              `json:"liked"`
	Praised  bool              `json:"praised"`
	MsgCount int               `json:"msg_count"`
	Faces    []*legacyFaceView `json:"faces"`
}

type legacyHomePayload struct {
	Cards      []legacyHomeCard `json:"cards"`
	CardsTotal int              `json:"cards_total"`
}

// TestIntegrationLegacyHomeSelfCard pins the v1 /api/home behaviour: the
// viewer's own card is the first card, cards_total counts it, while
// /legacy/api/cards keeps exposing only the candidate pool.
func TestIntegrationLegacyHomeSelfCard(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	viewer, viewerClient := env.createUser(t, "U-HOMESELF", "本人视角", "active", "user")
	candidate, _ := env.createUser(t, "U-HOMECAND", "候选用户", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, candidate.ID)

	prefix := "首页自卡" + randomHTTPDigits(t, 8)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '男性', nickname = $2, city = '上海' WHERE id = $1`,
		viewer.ID, prefix+"-本人"); err != nil {
		t.Fatalf("update viewer: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = '女性', nickname = $2, city = '杭州' WHERE id = $1`,
		candidate.ID, prefix+"-候选"); err != nil {
		t.Fatalf("update candidate: %v", err)
	}
	mediaID := attachTestPhoto(t, env, candidate.ID)
	centerX, centerY := 0.45, 0.38
	boxW, boxH := 0.2, 0.24
	if err := env.database.SetProfilePhotoFace(env.ctx, mediaID, db.ProfilePhotoFace{
		Checked: true, HasFace: true,
		CenterX: &centerX, CenterY: &centerY, BoxW: &boxW, BoxH: &boxH,
	}); err != nil {
		t.Fatalf("set face: %v", err)
	}

	query := "?nickname=" + url.QueryEscape(prefix)

	resp := viewerClient.get("/legacy/api/home" + query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy home status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home legacyHomePayload
	legacyJSONDecode(t, resp, &home)
	if home.CardsTotal != 2 {
		t.Fatalf("home cards_total = %d, want 2", home.CardsTotal)
	}
	if len(home.Cards) != 2 {
		t.Fatalf("home cards = %d, want 2", len(home.Cards))
	}
	self := home.Cards[0]
	if !self.IsSelf || self.Nickname != prefix+"-本人" {
		t.Fatalf("first home card = %+v, want self", self)
	}
	if self.Liked || self.Praised {
		t.Fatalf("self card interactions leaked: liked=%v praised=%v", self.Liked, self.Praised)
	}
	if self.MsgCount != 0 {
		t.Fatalf("self card msg_count = %d, want 0", self.MsgCount)
	}
	if home.Cards[1].IsSelf || home.Cards[1].Nickname != prefix+"-候选" {
		t.Fatalf("second home card = %+v, want candidate", home.Cards[1])
	}
	if len(self.Faces) != 0 {
		t.Fatalf("self card faces = %+v, want empty", self.Faces)
	}
	if len(home.Cards[1].Faces) != 1 || home.Cards[1].Faces[0] == nil {
		t.Fatalf("candidate card faces = %+v", home.Cards[1].Faces)
	}
	if math.Abs(home.Cards[1].Faces[0].C[0]-0.45) > 0.001 ||
		math.Abs(home.Cards[1].Faces[0].Box[1]-0.24) > 0.001 {
		t.Fatalf("candidate face = %+v", home.Cards[1].Faces[0])
	}

	resp = viewerClient.get("/legacy/api/cards" + query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy cards status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var pool struct {
		Cards      []legacyHomeCard `json:"cards"`
		CardsTotal int              `json:"cards_total"`
		Total      int              `json:"total"`
	}
	legacyJSONDecode(t, resp, &pool)
	if pool.CardsTotal != 1 || pool.Total != 1 || len(pool.Cards) != 1 {
		t.Fatalf("cards endpoint total=%d cards_total=%d len=%d, want 1", pool.Total, pool.CardsTotal, len(pool.Cards))
	}
	if pool.Cards[0].IsSelf {
		t.Fatal("/legacy/api/cards must not include the viewer's own card")
	}
}
