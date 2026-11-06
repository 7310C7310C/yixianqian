package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

type cardOverviewItemPayload struct {
	OpenID      string `json:"openid"`
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	Gender      string `json:"gender"`
	Age         int    `json:"age"`
	Height      int    `json:"height"`
	City        string `json:"city"`
	Photo       string `json:"photo"`
	IsNew       bool   `json:"is_new"`
	Recommended bool   `json:"recommended"`
	Liked       bool   `json:"liked"`
}

type cardOverviewPayload struct {
	Items   []cardOverviewItemPayload `json:"items"`
	Total   int                       `json:"total"`
	Offset  int                       `json:"offset"`
	Limit   int                       `json:"limit"`
	HasMore bool                      `json:"has_more"`
}

func readCardOverview(t *testing.T, client *wsClient, query string) cardOverviewPayload {
	t.Helper()
	resp := client.get("/legacy/api/cards/overview" + query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var payload cardOverviewPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	return payload
}

// TestIntegrationCardsOverviewNewestFirst covers the 全览 contract end to end:
// auth, eligibility parity with the deck, newest-registration order, paging and
// the photo grant for the returned tiles.
func TestIntegrationCardsOverviewNewestFirst(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	viewer, viewerClient := authzUser(t, env, "U-OVW", "全览本人", "男性", "active", "user")
	oldest, _ := authzUser(t, env, "U-OVA", "全览最早", "女性", "active", "user")
	middle, _ := authzUser(t, env, "U-OVB", "全览中间", "女性", "active", "user")
	newest, _ := authzUser(t, env, "U-OVC", "全览最新", "女性", "active", "user")
	sameGender, _ := authzUser(t, env, "U-OVS", "全览同性", "男性", "active", "user")
	noPhoto, _ := authzUser(t, env, "U-OVN", "全览无照片", "女性", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, oldest.ID, middle.ID, newest.ID, sameGender.ID, noPhoto.ID)
	for _, u := range []int64{oldest.ID, middle.ID, newest.ID, sameGender.ID} {
		createAuthzPhoto(t, env, u)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET birthday = '1996-01-01', height_cm = 168 WHERE id = $1`, middle.ID); err != nil {
		t.Fatalf("set middle details: %v", err)
	}

	// 匿名必须 401。
	anonymous := newWSClient(t, env.server.URL)
	if resp := anonymous.get("/legacy/api/cards/overview"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", resp.StatusCode)
	}

	page := readCardOverview(t, viewerClient, "?limit=60")
	if page.Offset != 0 || page.Limit != 60 || page.Total < 3 {
		t.Fatalf("unexpected envelope: %+v", page)
	}
	if page.HasMore != (page.Total > len(page.Items)) {
		t.Fatalf("has_more/total mismatch: total=%d items=%d has_more=%v", page.Total, len(page.Items), page.HasMore)
	}

	index := make(map[string]int, len(page.Items))
	for i, item := range page.Items {
		index[item.UserID] = i
		if item.Gender != "女性" {
			t.Fatalf("overview leaked gender %q: %+v", item.Gender, item)
		}
		if item.UserID == legacyOf(viewer) {
			t.Fatalf("overview leaked self: %+v", item)
		}
		if item.Photo == "" {
			t.Fatalf("overview item without photo: %+v", item)
		}
	}
	oldestAt, okOldest := index[legacyOf(oldest)]
	middleAt, okMiddle := index[legacyOf(middle)]
	newestAt, okNewest := index[legacyOf(newest)]
	if !okOldest || !okMiddle || !okNewest {
		t.Fatalf("created users missing from overview: oldest=%v middle=%v newest=%v (%d items)",
			okOldest, okMiddle, okNewest, len(page.Items))
	}
	if !(newestAt < middleAt && middleAt < oldestAt) {
		t.Fatalf("not newest-first: newest=%d middle=%d oldest=%d", newestAt, middleAt, oldestAt)
	}
	if _, ok := index[legacyOf(sameGender)]; ok {
		t.Fatal("overview leaked a same-gender account")
	}
	if _, ok := index[legacyOf(noPhoto)]; ok {
		t.Fatal("overview leaked an account without a photo")
	}

	birthday := time.Date(1996, 1, 1, 0, 0, 0, 0, time.Local)
	middleItem := page.Items[middleAt]
	if middleItem.Age != socialAgeOn(&birthday, time.Now()) || middleItem.Height != 168 || middleItem.City == "" {
		t.Fatalf("tile fields incomplete: %+v", middleItem)
	}
	if !middleItem.IsNew {
		t.Fatalf("fresh account should carry the new badge: %+v", middleItem)
	}

	// 返回的缩略图必须已登记 grant，否则首屏就是一片 404。
	expectPhotoStatus(t, viewerClient, page.Items[0].Photo, http.StatusOK)

	// 分页：相邻两页不重叠；limit 超上限按 60 收口。
	firstPage := readCardOverview(t, viewerClient, "?limit=2&offset=0")
	secondPage := readCardOverview(t, viewerClient, "?limit=2&offset=2")
	if len(firstPage.Items) > 2 || len(secondPage.Items) > 2 {
		t.Fatalf("page size not honoured: %d/%d", len(firstPage.Items), len(secondPage.Items))
	}
	if firstPage.HasMore != (firstPage.Total > 2) {
		t.Fatalf("first page has_more = %v total=%d", firstPage.HasMore, firstPage.Total)
	}
	seen := make(map[string]bool, len(firstPage.Items))
	for _, item := range firstPage.Items {
		seen[item.OpenID] = true
	}
	for _, item := range secondPage.Items {
		if seen[item.OpenID] {
			t.Fatalf("pagination repeated %q", item.OpenID)
		}
	}
	// 第二页同样要发照片 grant（否则滚动到第二页就整页 404）。
	if len(secondPage.Items) > 0 {
		expectPhotoStatus(t, viewerClient, secondPage.Items[0].Photo, http.StatusOK)
	}
	// 越界页不能把 total 冲成 0（window count 没有行时不可用，DB 层要补 count）。
	beyond := readCardOverview(t, viewerClient, "?limit=2&offset="+strconv.Itoa(page.Total+1000))
	if len(beyond.Items) != 0 || beyond.Total != page.Total || beyond.HasMore {
		t.Fatalf("beyond-end page = %+v, want empty with total=%d", beyond, page.Total)
	}
	if clamped := readCardOverview(t, viewerClient, "?limit=999"); clamped.Limit != cardOverviewMaxPageSize {
		t.Fatalf("limit clamp = %d, want %d", clamped.Limit, cardOverviewMaxPageSize)
	}

	// 身份证未补填的账号必须被服务端门禁挡住（与 /cards 一致）。
	blocked, blockedClient := authzUser(t, env, "U-OVI", "全览未补证", "男性", "active", "user")
	cleanupHTTPUsers(t, env, blocked.ID)
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET id_card_valid = false WHERE id = $1`, blocked.ID); err != nil {
		t.Fatalf("unset id card: %v", err)
	}
	blockedResp := blockedClient.get("/legacy/api/cards/overview")
	if blockedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("id-card gate status = %d body=%s", blockedResp.StatusCode, readBody(t, blockedResp))
	}
	if body := readBody(t, blockedResp); !strings.Contains(body, "id_card_required") {
		t.Fatalf("id-card gate body = %s, want code id_card_required", body)
	}

	// 性别缺失的账号拿到 400，而不是空列表。
	noGender, noGenderClient := authzUser(t, env, "U-OVG", "全览无性别", "男性", "active", "user")
	cleanupHTTPUsers(t, env, noGender.ID)
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET gender = '' WHERE id = $1`, noGender.ID); err != nil {
		t.Fatalf("clear gender: %v", err)
	}
	resp := noGenderClient.get("/legacy/api/cards/overview")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("genderless status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// legacyOf is the legacy id the overview echoes as user_id (matches
// socialCardOpenID: legacy open id first, legacy user id otherwise).
func legacyOf(u db.User) string {
	if u.LegacyUserID != nil && *u.LegacyUserID != "" {
		return *u.LegacyUserID
	}
	return ""
}
