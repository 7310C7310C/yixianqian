package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// likeStateJSON is the compact row shape used when likes are folded into a
// single round trip. Field names are internal to this package.
type likeStateJSON struct {
	TargetID int64  `json:"target_id"`
	Status   string `json:"status"`
	LikeType string `json:"like_type"`
	Month    string `json:"month"`
}

// LoadInteractionCounters loads the like quota and praise state in one
// statement. Both are per-viewer read-only snapshots and were previously two
// (like quota) plus one (praise) round trips.
func (d *DB) LoadInteractionCounters(ctx context.Context, userID int64, now time.Time) (LikeQuota, PraiseState, error) {
	var (
		rawLikes   []byte
		permanent  int
		praiseUsed int
		praised    []int64
		received   int
	)
	err := d.pool.QueryRow(ctx,
		`SELECT
		   COALESCE((SELECT json_agg(json_build_object(
		              'target_id', target_id, 'status', status,
		              'like_type', like_type, 'month', COALESCE(month, '')))
		             FROM likes WHERE initiator_id = $1), '[]'::json),
		   (SELECT count(*) FROM points_redemptions
		     WHERE user_id = $1 AND item = $2 AND status = $3),
		   (SELECT count(*) FROM praises
		     WHERE initiator_id = $1 AND day = $4 AND status = $5),
		   (SELECT COALESCE(array_agg(DISTINCT target_id ORDER BY target_id), '{}'::bigint[])
		      FROM praises WHERE initiator_id = $1 AND status = $5),
		   (SELECT count(*) FROM praises
		     WHERE target_id = $1 AND week = $6 AND status = $5)`,
		userID, RedeemItemRealLike, RedeemStatusActive, DayKey(now), PraiseStatusActive, WeekKey(now)).
		Scan(&rawLikes, &permanent, &praiseUsed, &praised, &received)
	if err != nil {
		return LikeQuota{}, PraiseState{}, fmt.Errorf("db: load interaction counters: %w", err)
	}
	records, err := decodeLikeRecords(rawLikes, userID)
	if err != nil {
		return LikeQuota{}, PraiseState{}, err
	}
	quota := ComputeLikeQuota(records, permanent, MonthKey(now))
	return quota, praiseStateFromCounts(praiseUsed, praised, received), nil
}

// cardDeckStateSelect is the four-column card overlay (likes out/in, praises,
// weekly recommendations). It is shared by the two callers below so the home
// and cards paths cannot drift.
const cardDeckStateSelect = `SELECT
		   COALESCE((SELECT json_agg(json_build_object(
		              'target_id', target_id, 'status', status,
		              'like_type', like_type, 'month', COALESCE(month, '')))
		             FROM likes
		            WHERE initiator_id = $1 AND status IN ($2, $3)), '[]'::json),
		   COALESCE((SELECT json_agg(json_build_object(
		              'target_id', initiator_id, 'status', status,
		              'like_type', like_type, 'month', COALESCE(month, '')))
		             FROM likes
		            WHERE target_id = $1 AND status IN ($2, $3)), '[]'::json),
		   (SELECT COALESCE(array_agg(target_id ORDER BY target_id), '{}'::bigint[])
		      FROM praises WHERE initiator_id = $1 AND status = $4),
		   (SELECT COALESCE(array_agg(target_user_id ORDER BY position), '{}'::bigint[])
		      FROM weekly_recommendations WHERE user_id = $1 AND week = $5)`

// interactionCounterSelect is the like-quota + praise-state part used when the
// cards endpoint has not loaded counters yet ($6 item, $7 status, $8 day).
const interactionCounterSelect = `,
		   COALESCE((SELECT json_agg(json_build_object(
		              'target_id', target_id, 'status', status,
		              'like_type', like_type, 'month', COALESCE(month, '')))
		             FROM likes WHERE initiator_id = $1), '[]'::json),
		   (SELECT count(*) FROM points_redemptions
		     WHERE user_id = $1 AND item = $6 AND status = $7),
		   (SELECT count(*) FROM praises
		     WHERE initiator_id = $1 AND day = $8 AND status = $4),
		   (SELECT COALESCE(array_agg(DISTINCT target_id ORDER BY target_id), '{}'::bigint[])
		      FROM praises WHERE initiator_id = $1 AND status = $4),
		   (SELECT count(*) FROM praises
		     WHERE target_id = $1 AND week = $5 AND status = $4)`

func decodeCardState(rawOutgoing, rawIncoming []byte, praised []int64, now time.Time) (CardSocialState, error) {
	state := CardSocialState{
		LikedTargets:   map[int64]bool{},
		LikedMe:        map[int64]bool{},
		PraisedTargets: map[int64]bool{},
		MessageCounts:  map[int64]int{},
	}
	currentMonth := MonthKey(now)
	apply := func(raw []byte, target map[int64]bool) error {
		var rows []likeStateJSON
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return fmt.Errorf("db: decode card likes: %w", err)
			}
		}
		for _, row := range rows {
			if IsLikeActive(row.Status, row.LikeType, row.Month, currentMonth) {
				target[row.TargetID] = true
			}
		}
		return nil
	}
	if err := apply(rawOutgoing, state.LikedTargets); err != nil {
		return state, err
	}
	if err := apply(rawIncoming, state.LikedMe); err != nil {
		return state, err
	}
	for _, id := range praised {
		state.PraisedTargets[id] = true
	}
	return state, nil
}

func decodeLikeRecords(raw []byte, userID int64) ([]LikeRecord, error) {
	var rows []likeStateJSON
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("db: decode like quota rows: %w", err)
		}
	}
	records := make([]LikeRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, LikeRecord{
			InitiatorID: userID,
			TargetID:    row.TargetID,
			Status:      row.Status,
			LikeType:    row.LikeType,
			Month:       row.Month,
		})
	}
	return records, nil
}

func praiseStateFromCounts(used int, praised []int64, received int) PraiseState {
	state := PraiseState{
		Limit:        DailyPraiseLimit,
		Left:         DailyPraiseLimit - used,
		Used:         used,
		Praised:      praised,
		ReceivedWeek: received,
	}
	if state.Praised == nil {
		state.Praised = []int64{}
	}
	if state.Left < 0 {
		state.Left = 0
	}
	return state
}

// LoadCardDeckState loads the card overlay and this week's pinned
// recommendations in one statement. WeeklyRecommendationTargets retains its
// position order, which OrderCardIDs depends on.
func (d *DB) LoadCardDeckState(ctx context.Context, viewerID int64, now time.Time, week string) (CardSocialState, []int64, error) {
	var (
		state       CardSocialState
		rawOutgoing []byte
		rawIncoming []byte
		praised     []int64
		recommended []int64
	)
	err := d.pool.QueryRow(ctx, cardDeckStateSelect,
		viewerID, LikeStatusSingle, LikeStatusMutual, PraiseStatusActive, week).
		Scan(&rawOutgoing, &rawIncoming, &praised, &recommended)
	if err != nil {
		return state, nil, fmt.Errorf("db: load card deck state: %w", err)
	}
	state, err = decodeCardState(rawOutgoing, rawIncoming, praised, now)
	if err != nil {
		return state, nil, err
	}
	return state, recommended, nil
}

// LoadCardPageState is LoadCardDeckState plus the like/praise counters in the
// same statement. /legacy/api/cards uses it when no caller-supplied counters
// exist, saving a round trip without changing freshness.
func (d *DB) LoadCardPageState(ctx context.Context, viewerID int64, now time.Time, week string) (CardSocialState, []int64, LikeQuota, PraiseState, error) {
	var (
		state          CardSocialState
		quota          LikeQuota
		praise         PraiseState
		rawOutgoing    []byte
		rawIncoming    []byte
		rawLikes       []byte
		praised        []int64
		praisedCounter []int64
		recommended    []int64
		permanent      int
		praiseUsed     int
		received       int
	)
	err := d.pool.QueryRow(ctx, cardDeckStateSelect+interactionCounterSelect,
		viewerID, LikeStatusSingle, LikeStatusMutual, PraiseStatusActive, week,
		RedeemItemRealLike, RedeemStatusActive, DayKey(now)).
		Scan(&rawOutgoing, &rawIncoming, &praised, &recommended,
			&rawLikes, &permanent, &praiseUsed, &praisedCounter, &received)
	if err != nil {
		return state, nil, quota, praise, fmt.Errorf("db: load card page state: %w", err)
	}
	state, err = decodeCardState(rawOutgoing, rawIncoming, praised, now)
	if err != nil {
		return state, nil, quota, praise, err
	}
	records, err := decodeLikeRecords(rawLikes, viewerID)
	if err != nil {
		return state, nil, quota, praise, err
	}
	quota = ComputeLikeQuota(records, permanent, MonthKey(now))
	praise = praiseStateFromCounts(praiseUsed, praised, received)
	return state, recommended, quota, praise, nil
}

// GetSocialProfilesWithMessageCounts is the card-page batch: public profiles
// plus the per-recipient message badge in one statement. It replaces
// GetSocialProfilesByIDs + MessageCountsFor on the deck path only.
func (d *DB) GetSocialProfilesWithMessageCounts(ctx context.Context, ids []int64) (map[int64]SocialProfile, map[int64]int, error) {
	profiles := make(map[int64]SocialProfile, len(ids))
	counts := make(map[int64]int, len(ids))
	if len(ids) == 0 {
		return profiles, counts, nil
	}
	rows, err := d.pool.Query(ctx,
		"SELECT "+socialProfileColumns+`,
		        COALESCE((SELECT count(*) FROM messages m
		                   WHERE m.target_user_id = u.id AND m.status <> '已删除'), 0)
		   FROM users u `+socialPhotoJoin+`
		  WHERE u.id = ANY($1) ORDER BY u.id`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("db: list social profiles with message counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			p              SocialProfile
			selfTraits     []byte
			selfHobbies    []byte
			selfSports     []byte
			mbti           []byte
			partnerTraits  []byte
			partnerHobbies []byte
			partnerSports  []byte
			messageCount   int
		)
		if err := rows.Scan(&p.ID, &p.LegacyID, &p.LegacyOpenID, &p.Name, &p.Nickname, &p.Gender, &p.Birthday,
			&p.HeightCM, &p.Education, &p.BaptismalName, &p.Church, &p.ChurchLocation,
			&p.ChurchGroup, &p.NativePlace, &p.City, &p.Industry, &p.Position, &p.Income,
			&p.House, &p.Driving, &p.HouseNoteHave, &p.HouseNoteNone,
			&p.DrivingNoteHave, &p.DrivingNoteNone, &p.Marriage, &p.Family,
			&p.LiveWithParents, &p.Personality, &selfTraits, &selfHobbies, &selfSports, &mbti,
			&p.PartnerCriteria, &partnerTraits, &partnerHobbies, &partnerSports,
			&p.Wechat, &p.ContactConsent, &p.AccountKind, &p.AccountStatus, &p.LoginStatus,
			&p.Photos, &messageCount); err != nil {
			return nil, nil, fmt.Errorf("db: scan social profile with message count: %w", err)
		}
		p.SelfTraits = decodeSocialStringList(selfTraits)
		p.SelfHobbies = decodeSocialStringList(selfHobbies)
		p.SelfSports = decodeSocialStringList(selfSports)
		p.MBTI = decodeSocialStringList(mbti)
		p.PartnerTraits = decodeSocialStringList(partnerTraits)
		p.PartnerHobbies = decodeSocialStringList(partnerHobbies)
		p.PartnerSports = decodeSocialStringList(partnerSports)
		profiles[p.ID] = p
		counts[p.ID] = messageCount
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("db: iterate social profiles with message counts: %w", err)
	}
	return profiles, counts, nil
}
