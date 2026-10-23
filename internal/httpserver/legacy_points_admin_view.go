package httpserver

import (
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The v1 SPA renders these payloads directly. Field names and types are part
// of the contract; keep them aligned with index.html's points_admin page.

type legacyPointsOverviewStats struct {
	UserCount       int   `json:"user_count"`
	EntryCount      int   `json:"entry_count"`
	Earned          int64 `json:"earned"`
	Spent           int64 `json:"spent"`
	Outstanding     int64 `json:"outstanding"`
	RedemptionCount int   `json:"redemption_count"`
	InviteCount     int   `json:"invite_count"`
}

type legacyPointsUserJSON struct {
	UserOID    string `json:"user_oid"`
	UserID     string `json:"user_id"`
	Nickname   string `json:"nickname"`
	Earned     int    `json:"earned"`
	Spent      int    `json:"spent"`
	EntryCount int    `json:"entry_count"`
	LastAt     string `json:"last_at"`
	Balance    int    `json:"balance"`
}

type legacyPointsLedgerJSON struct {
	ID        int64  `json:"id"`
	Nickname  string `json:"nickname"`
	UserID    string `json:"user_id"`
	KindLabel string `json:"kind_label"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
	Delta     int    `json:"delta"`
}

type legacyPointsRedemptionJSON struct {
	ID          int64  `json:"id"`
	Nickname    string `json:"nickname"`
	UserID      string `json:"user_id"`
	ItemLabel   string `json:"item_label"`
	StatusLabel string `json:"status_label"`
	CreatedAt   string `json:"created_at"`
	Cost        int    `json:"cost"`
}

type legacyPointsInviteJSON struct {
	ID          int64  `json:"id"`
	Nickname    string `json:"nickname"`
	UserID      string `json:"user_id"`
	PhoneMasked string `json:"phone_masked"`
	StatusLabel string `json:"status_label"`
	CreatedAt   string `json:"created_at"`
}

type legacyPointsUserRef struct {
	Nickname string `json:"nickname"`
	UserID   string `json:"user_id"`
}

type legacyPointsSummary struct {
	Balance int `json:"balance"`
	Earned  int `json:"earned"`
	Spent   int `json:"spent"`
}

type legacyPointsKindTotal struct {
	Kind      string `json:"kind"`
	KindLabel string `json:"kind_label"`
	Total     int    `json:"total"`
	Count     int    `json:"count"`
}

type legacyPointsItemTotal struct {
	Item      string `json:"item"`
	ItemLabel string `json:"item_label"`
	Total     int    `json:"total"`
	Count     int    `json:"count"`
}

type legacyPointsInviteProgress struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Confirmed  int `json:"confirmed"`
	Finalized  int `json:"finalized"`
	ClawedBack int `json:"clawed_back"`
	Rejected   int `json:"rejected"`
}

type legacyPointsInviteItem struct {
	ID          int64  `json:"id"`
	PhoneMasked string `json:"phone_masked"`
	StatusLabel string `json:"status_label"`
}

type legacyPointsUserRedemption struct {
	ID          int64  `json:"id"`
	ItemLabel   string `json:"item_label"`
	StatusLabel string `json:"status_label"`
	Cost        int    `json:"cost"`
	CreatedAt   string `json:"created_at"`
}

type legacyPointsUserEntry struct {
	ID        int64  `json:"id"`
	KindLabel string `json:"kind_label"`
	Reason    string `json:"reason"`
	Delta     int    `json:"delta"`
	CreatedAt string `json:"created_at"`
}

type legacyPointsUserDetailJSON struct {
	User           legacyPointsUserRef          `json:"user"`
	Summary        legacyPointsSummary          `json:"summary"`
	ExtraRealLike  int                          `json:"extra_real_like"`
	EarnedByKind   []legacyPointsKindTotal      `json:"earned_by_kind"`
	RedeemedByItem []legacyPointsItemTotal      `json:"redeemed_by_item"`
	Invites        legacyPointsInviteProgress   `json:"invites"`
	InviteList     []legacyPointsInviteItem     `json:"invite_list"`
	Redemptions    []legacyPointsUserRedemption `json:"redemptions"`
	Entries        []legacyPointsUserEntry      `json:"entries"`
}

func legacyPointsUserID(legacyID string, id int64) string {
	if trimmed := strings.TrimSpace(legacyID); trimmed != "" {
		return trimmed
	}
	return strconv.FormatInt(id, 10)
}

func legacyPointsNickname(nickname, name string) string {
	if trimmed := strings.TrimSpace(nickname); trimmed != "" {
		return trimmed
	}
	return name
}

func legacyPointsTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return formatSocialTime(*value)
}

func legacyPointsOverviewUsers(rows []db.LegacyPointsUserRow) []legacyPointsUserJSON {
	out := make([]legacyPointsUserJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsUserJSON{
			UserOID:    strconv.FormatInt(row.ID, 10),
			UserID:     legacyPointsUserID(row.LegacyUserID, row.ID),
			Nickname:   legacyPointsNickname(row.Nickname, row.Name),
			Earned:     row.Earned,
			Spent:      row.Spent,
			EntryCount: row.EntryCount,
			LastAt:     legacyPointsTime(row.LastAt),
			Balance:    row.Balance,
		})
	}
	return out
}

func legacyPointsLedgerRows(rows []db.AdminLedgerRow) []legacyPointsLedgerJSON {
	out := make([]legacyPointsLedgerJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsLedgerJSON{
			ID:        row.ID,
			Nickname:  row.UserName,
			UserID:    legacyPointsUserID(row.LegacyUserID, row.UserID),
			KindLabel: row.KindLabel,
			Reason:    row.Reason,
			CreatedAt: formatSocialTime(row.CreatedAt),
			Delta:     row.Delta,
		})
	}
	return out
}

func legacyPointsRedemptionRows(rows []db.AdminRedemptionRow) []legacyPointsRedemptionJSON {
	out := make([]legacyPointsRedemptionJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsRedemptionJSON{
			ID:          row.ID,
			Nickname:    row.UserName,
			UserID:      legacyPointsUserID(row.UserLegacyID, row.UserID),
			ItemLabel:   row.ItemLabel,
			StatusLabel: row.StatusLabel,
			CreatedAt:   formatSocialTime(row.CreatedAt),
			Cost:        row.Cost,
		})
	}
	return out
}

func legacyPointsInviteRows(rows []db.AdminInviteRow) []legacyPointsInviteJSON {
	out := make([]legacyPointsInviteJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsInviteJSON{
			ID:          row.ID,
			Nickname:    row.InviterName,
			UserID:      legacyPointsUserID(row.InviterLegacyID, row.InviterID),
			PhoneMasked: row.PhoneMasked,
			StatusLabel: row.StatusLabel,
			CreatedAt:   formatSocialTime(row.CreatedAt),
		})
	}
	return out
}

func legacyPointsKindTotals(rows []db.LegacyPointsKindTotal) []legacyPointsKindTotal {
	out := make([]legacyPointsKindTotal, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsKindTotal{
			Kind:      row.Kind,
			KindLabel: db.PointsKindLabel(row.Kind),
			Total:     row.Total,
			Count:     row.Count,
		})
	}
	return out
}

func legacyPointsItemTotals(rows []db.LegacyPointsItemTotal) []legacyPointsItemTotal {
	out := make([]legacyPointsItemTotal, 0, len(rows))
	for _, row := range rows {
		label := db.RedeemItemLabels[row.Item]
		if label == "" {
			label = row.Item
		}
		out = append(out, legacyPointsItemTotal{
			Item:      row.Item,
			ItemLabel: label,
			Total:     row.Total,
			Count:     row.Count,
		})
	}
	return out
}

func legacyPointsInviteList(rows []db.InviteView) []legacyPointsInviteItem {
	out := make([]legacyPointsInviteItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsInviteItem{
			ID:          row.ID,
			PhoneMasked: row.PhoneMasked,
			StatusLabel: row.StatusLabel,
		})
	}
	return out
}

func legacyPointsUserRedemptions(rows []db.Redemption) []legacyPointsUserRedemption {
	out := make([]legacyPointsUserRedemption, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsUserRedemption{
			ID:          row.ID,
			ItemLabel:   row.ItemLabel,
			StatusLabel: row.StatusLabel,
			Cost:        row.Cost,
			CreatedAt:   formatSocialTime(row.CreatedAt),
		})
	}
	return out
}

func legacyPointsUserEntries(rows []db.PointsEntry) []legacyPointsUserEntry {
	out := make([]legacyPointsUserEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyPointsUserEntry{
			ID:        row.ID,
			KindLabel: row.KindLabel,
			Reason:    row.Reason,
			Delta:     row.Delta,
			CreatedAt: formatSocialTime(row.CreatedAt),
		})
	}
	return out
}
