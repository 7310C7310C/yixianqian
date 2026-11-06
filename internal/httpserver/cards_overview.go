package httpserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// CardOverviewItem is the light card rendered by the 全览 grid. It deliberately
// carries only what a tile needs: the full profile stays behind /users/{openid}/public.
type CardOverviewItem struct {
	OpenID      string `json:"openid"`
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	Gender      string `json:"gender"`
	Age         int    `json:"age,omitempty"`
	Height      int    `json:"height,omitempty"`
	City        string `json:"city"`
	Photo       string `json:"photo"`
	IsNew       bool   `json:"is_new"`
	Recommended bool   `json:"recommended"`
	Liked       bool   `json:"liked"`
}

const (
	cardOverviewPageSize    = 30
	cardOverviewMaxPageSize = 60
	// cardOverviewNewWindow drives the "新" badge. Registration time is the
	// anchor because the overview order itself is registration order.
	cardOverviewNewWindow = 14 * 24 * time.Hour
)

// buildCardOverviewItem projects a profile into the overview tile. Pure so the
// badge/age/photo rules are unit-testable without HTTP or a database.
func buildCardOverviewItem(p db.SocialProfile, createdAt, now time.Time) CardOverviewItem {
	item := CardOverviewItem{
		OpenID:   socialCardOpenID(p),
		UserID:   p.LegacyID,
		Nickname: p.DisplayName(),
		Gender:   p.Gender,
		Age:      socialAgeOn(p.Birthday, now),
		City:     p.City,
	}
	if p.HeightCM != nil {
		item.Height = *p.HeightCM
	}
	if len(p.Photos) > 0 {
		item.Photo = socialPhotoView(p.Photos[0]).ThumbURL
	}
	if !createdAt.IsZero() {
		item.IsNew = now.Sub(createdAt) <= cardOverviewNewWindow
	}
	return item
}

// handleLegacyCardsOverview backs 全览模式: the same eligible pool as the deck,
// newest registration first, paged. Filters do not apply here on purpose —
// filtering belongs to the deck, and the overview is the "everyone, newest
// first" surface.
func (a *App) handleLegacyCardsOverview(w http.ResponseWriter, r *http.Request) {
	user, ok := a.legacyActor(w, r)
	if !ok {
		return
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusForbidden, "账号目前无法使用，请联络管理员")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards overview viewer failed")
		return
	}
	if a.legacyViewRole(r) == "observer" {
		viewer.AccountKind = "observer"
		viewer.AccountStatus = "observer"
	}
	targetGender, err := socialTargetGender(viewer, "")
	if errors.Is(err, errGenderMissing) {
		legacyError(w, http.StatusBadRequest, "你的性别资料尚未完善，请先到「我的资料」填写")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards overview gender failed")
		return
	}

	limit := socialNonNegative(r.URL.Query().Get("limit"), cardOverviewPageSize)
	if limit <= 0 {
		limit = cardOverviewPageSize
	}
	if limit > cardOverviewMaxPageSize {
		limit = cardOverviewMaxPageSize
	}
	offset := socialNonNegative(r.URL.Query().Get("offset"), 0)

	now := a.clock.Now()
	entries, total, err := a.db.ListCardOverviewPage(r.Context(), viewer.ID, targetGender, limit, offset)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards overview failed")
		return
	}

	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	profiles, err := a.db.GetSocialProfilesByIDs(r.Context(), ids)
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards overview profiles failed")
		return
	}
	state, recommendedIDs, err := a.db.LoadCardDeckState(r.Context(), viewer.ID, now, db.WeekKey(now))
	if err != nil {
		legacyServerError(a, w, r, err, "legacy cards overview state failed")
		return
	}
	recommended := make(map[int64]bool, len(recommendedIDs))
	for _, id := range recommendedIDs {
		recommended[id] = true
	}

	items := make([]CardOverviewItem, 0, len(entries))
	owners := make([]int64, 0, len(entries))
	for _, entry := range entries {
		p, ok := profiles[entry.ID]
		if !ok {
			continue
		}
		item := buildCardOverviewItem(p, entry.CreatedAt, now)
		item.Recommended = recommended[entry.ID]
		item.Liked = state.LikedTargets[entry.ID]
		items = append(items, item)
		owners = append(owners, entry.ID)
	}
	// The grid renders /cards/photo/{id}; without a grant the first scroll would
	// burn a page of 404s.
	a.grantPhotoOwners(user.ID, owners)

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"offset":   offset,
		"limit":    limit,
		"has_more": offset+len(entries) < total,
	})
}
