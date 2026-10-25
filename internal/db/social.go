package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Social-target errors. Handlers map these to stable API codes/messages.
var (
	ErrSelfAction        = errors.New("db: cannot perform this action on yourself")
	ErrSameGender        = errors.New("db: social actions are limited to opposite genders")
	ErrTargetUnavailable = errors.New("db: target is not in the active card pool")
	ErrAlreadyLiked      = errors.New("db: already liked this user")
	ErrLikeQuota         = errors.New("db: like quota exhausted")
	ErrInvalidLikeType   = errors.New("db: invalid like type")
	ErrLikeMessageShort  = errors.New("db: like message must be at least 10 characters")
	ErrLikeMessageLong   = errors.New("db: like message exceeds 500 characters")
	ErrPraiseQuota       = errors.New("db: praise quota exhausted")
)

// SocialProfile is the card/public subset of a user row (no phone, ID card or
// other private fields).
type SocialProfile struct {
	ID              int64
	LegacyID        string
	LegacyOpenID    string
	Name            string
	Nickname        string
	Gender          string
	Birthday        *time.Time
	HeightCM        *int
	Education       string
	BaptismalName   string
	Church          string
	ChurchLocation  string
	ChurchGroup     string
	NativePlace     string
	City            string
	Industry        string
	Position        string
	Income          string
	House           string
	Driving         string
	HouseNoteHave   string
	HouseNoteNone   string
	DrivingNoteHave string
	DrivingNoteNone string
	Marriage        string
	Family          string
	LiveWithParents string
	Personality     string
	SelfTraits      []string
	SelfHobbies     []string
	SelfSports      []string
	MBTI            []string
	PartnerCriteria string
	PartnerTraits   []string
	PartnerHobbies  []string
	PartnerSports   []string

	Wechat         string
	ContactConsent bool
	AccountKind    string
	AccountStatus  string
	LoginStatus    string
	Photos         []string
}

// DisplayName is the safe public name: nickname, or a neutral placeholder. The
// real name is never used as a card nickname.
func (p SocialProfile) DisplayName() string {
	if strings.TrimSpace(p.Nickname) != "" {
		return p.Nickname
	}
	return "未命名"
}

// Like is one row of the likes table.
type Like struct {
	ID          int64
	InitiatorID int64
	TargetID    int64
	Status      string
	LikeType    string
	Message     string
	Month       string
	CreatedAt   time.Time
}

// LikePair adds the other party and derived state to a like.
type LikePair struct {
	Like  Like
	Other SocialProfile
	// Mutual means a reciprocal active like exists (or either row is already
	// marked mutual). Active is false only for expired anonymous likes.
	Mutual bool
	Active bool
}

// SocialOverview is the data behind /api/likes/me, /my_likes, /liked_me and
// /mutual. It contains no anonymous identities.
type SocialOverview struct {
	Me      SocialProfile
	LikesMe []LikePair
	MyLikes []LikePair
}

// GetSocialProfile loads the public card profile for one user.
func (d *DB) GetSocialProfile(ctx context.Context, id int64) (SocialProfile, error) {
	p, err := scanSocialProfile(d.pool.QueryRow(ctx,
		"SELECT "+socialProfileColumns+" FROM users u "+socialPhotoJoin+" WHERE u.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SocialProfile{}, ErrNotFound
	}
	if err != nil {
		return SocialProfile{}, fmt.Errorf("db: get social profile: %w", err)
	}
	return p, nil
}

// GetSocialProfileByLegacyOpenID resolves the legacy open id used by v1 API
// clients.
func (d *DB) GetSocialProfileByLegacyOpenID(ctx context.Context, openID string) (SocialProfile, error) {
	if strings.TrimSpace(openID) == "" {
		return SocialProfile{}, ErrNotFound
	}
	p, err := scanSocialProfile(d.pool.QueryRow(ctx,
		"SELECT "+socialProfileColumns+" FROM users u "+socialPhotoJoin+
			" WHERE u.legacy_open_id = $1 ORDER BY u.id LIMIT 1", openID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SocialProfile{}, ErrNotFound
	}
	if err != nil {
		return SocialProfile{}, fmt.Errorf("db: get social profile by legacy open id: %w", err)
	}
	return p, nil
}

// GetSocialProfilesByIDs batch-loads public profiles.
func (d *DB) GetSocialProfilesByIDs(ctx context.Context, ids []int64) (map[int64]SocialProfile, error) {
	out := make(map[int64]SocialProfile, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx,
		"SELECT "+socialProfileColumns+" FROM users u "+socialPhotoJoin+
			" WHERE u.id = ANY($1) ORDER BY u.id", ids)
	if err != nil {
		return nil, fmt.Errorf("db: list social profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanSocialProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan social profile: %w", err)
		}
		out[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate social profiles: %w", err)
	}
	return out, nil
}

// SocialOverview loads likes to/from the viewer and derives mutual state.
func (d *DB) SocialOverview(ctx context.Context, viewerID int64, now time.Time) (SocialOverview, error) {
	me, err := d.GetSocialProfile(ctx, viewerID)
	if err != nil {
		return SocialOverview{}, err
	}
	rows, err := d.pool.Query(ctx,
		`SELECT id, initiator_id, target_id, status, like_type, message, month, created_at
		 FROM likes
		 WHERE (initiator_id = $1 OR target_id = $1)
		   AND status IN ($2, $3)
		 ORDER BY created_at DESC, id DESC`,
		viewerID, LikeStatusSingle, LikeStatusMutual)
	if err != nil {
		return SocialOverview{}, fmt.Errorf("db: list likes involving user: %w", err)
	}
	defer rows.Close()

	var likes []Like
	otherSet := make(map[int64]struct{})
	for rows.Next() {
		var l Like
		if err := rows.Scan(&l.ID, &l.InitiatorID, &l.TargetID, &l.Status,
			&l.LikeType, &l.Message, &l.Month, &l.CreatedAt); err != nil {
			return SocialOverview{}, fmt.Errorf("db: scan like: %w", err)
		}
		likes = append(likes, l)
		if l.InitiatorID == viewerID {
			otherSet[l.TargetID] = struct{}{}
		} else {
			otherSet[l.InitiatorID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return SocialOverview{}, fmt.Errorf("db: iterate likes: %w", err)
	}

	othersIDs := make([]int64, 0, len(otherSet))
	for id := range otherSet {
		othersIDs = append(othersIDs, id)
	}
	others, err := d.GetSocialProfilesByIDs(ctx, othersIDs)
	if err != nil {
		return SocialOverview{}, err
	}

	currentMonth := MonthKey(now)
	type pairKey struct{ a, b int64 }
	activeByPair := make(map[pairKey]bool, len(likes))
	mutualByPair := make(map[pairKey]bool, len(likes))
	for _, l := range likes {
		k := pairKey{l.InitiatorID, l.TargetID}
		if IsLikeActive(l.Status, l.LikeType, l.Month, currentMonth) {
			activeByPair[k] = true
		}
		if l.Status == LikeStatusMutual {
			mutualByPair[k] = true
		}
	}
	isMutual := func(l Like) bool {
		if l.Status == LikeStatusMutual || mutualByPair[pairKey{l.InitiatorID, l.TargetID}] {
			return true
		}
		return activeByPair[pairKey{l.InitiatorID, l.TargetID}] &&
			activeByPair[pairKey{l.TargetID, l.InitiatorID}]
	}

	out := SocialOverview{Me: me}
	for _, l := range likes {
		pair := LikePair{
			Like:   l,
			Other:  others[l.TargetID],
			Mutual: isMutual(l),
			Active: IsLikeActive(l.Status, l.LikeType, l.Month, currentMonth),
		}
		if l.InitiatorID == viewerID {
			pair.Other = others[l.TargetID]
			out.MyLikes = append(out.MyLikes, pair)
		} else {
			pair.Other = others[l.InitiatorID]
			if pair.Active {
				out.LikesMe = append(out.LikesMe, pair)
			}
		}
	}
	return out, nil
}

// CreateLikeParams describes a new like request.
type CreateLikeParams struct {
	InitiatorID int64
	TargetID    int64
	LikeType    string
	Message     string
	Now         time.Time
}

// CreateLikeResult reports the written like and the post-write quota.
type CreateLikeResult struct {
	Like   Like
	Mutual bool
	Quota  LikeQuota
}

// CreateLike validates, consumes quota, writes the like and flips both rows to
// mutual when a reciprocal active like already exists. Everything happens in
// one transaction. A per-initiator row lock serializes quota/duplicate checks;
// a pair advisory lock makes two crossing likes race-safe.
func (d *DB) CreateLike(ctx context.Context, p CreateLikeParams) (CreateLikeResult, error) {
	if p.LikeType != LikeTypeAnon && p.LikeType != LikeTypeReal {
		return CreateLikeResult{}, ErrInvalidLikeType
	}
	p.Message = strings.TrimSpace(p.Message)
	messageRunes := utf8.RuneCountInString(p.Message)
	if messageRunes < 10 {
		return CreateLikeResult{}, ErrLikeMessageShort
	}
	if messageRunes > 500 {
		return CreateLikeResult{}, ErrLikeMessageLong
	}
	if p.InitiatorID == p.TargetID {
		return CreateLikeResult{}, ErrSelfAction
	}
	if p.Now.IsZero() {
		p.Now = SocialNow()
	}
	currentMonth := MonthKey(p.Now)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return CreateLikeResult{}, fmt.Errorf("db: begin like: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	me, err := lockSocialActor(ctx, tx, p.InitiatorID)
	if err != nil {
		return CreateLikeResult{}, err
	}
	target, err := socialUserState(ctx, tx, p.TargetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateLikeResult{}, ErrNotFound
	}
	if err != nil {
		return CreateLikeResult{}, fmt.Errorf("db: load like target: %w", err)
	}
	if err := validateSocialPair(me, target); err != nil {
		return CreateLikeResult{}, err
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairLockKey(p.InitiatorID, p.TargetID)); err != nil {
		return CreateLikeResult{}, fmt.Errorf("db: lock like pair: %w", err)
	}

	records, err := listLikesTx(ctx, tx, p.InitiatorID)
	if err != nil {
		return CreateLikeResult{}, err
	}
	for _, r := range records {
		if r.TargetID == p.TargetID && IsLikeActive(r.Status, r.LikeType, r.Month, currentMonth) {
			return CreateLikeResult{}, ErrAlreadyLiked
		}
	}
	// Redeemed extra real-name slots are permanent monthly additions. Read them
	// in the same transaction as the like row so the check matches what the
	// user paid for on /points.
	permanent, err := extraRealLikeQuotaTx(ctx, tx, p.InitiatorID)
	if err != nil {
		return CreateLikeResult{}, err
	}
	quota := ComputeLikeQuota(records, permanent, currentMonth)
	if p.LikeType == LikeTypeReal {
		if quota.RealLeft <= 0 {
			return CreateLikeResult{}, ErrLikeQuota
		}
	} else if quota.AnonLeft <= 0 {
		return CreateLikeResult{}, ErrLikeQuota
	}

	var liked Like
	err = tx.QueryRow(ctx,
		`INSERT INTO likes (initiator_id, target_id, initiator_name, target_name,
		                    status, like_type, message, month, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, initiator_id, target_id, status, like_type, message, month, created_at`,
		p.InitiatorID, p.TargetID, me.Nickname, target.Nickname,
		LikeStatusSingle, p.LikeType, p.Message, currentMonth, p.Now).
		Scan(&liked.ID, &liked.InitiatorID, &liked.TargetID, &liked.Status,
			&liked.LikeType, &liked.Message, &liked.Month, &liked.CreatedAt)
	if err != nil {
		return CreateLikeResult{}, mapWriteError(err)
	}

	reverse, err := listLikesTxBetween(ctx, tx, p.TargetID, p.InitiatorID)
	if err != nil {
		return CreateLikeResult{}, err
	}
	var mutualIDs []int64
	for _, r := range reverse {
		if IsLikeActive(r.Status, r.LikeType, r.Month, currentMonth) {
			mutualIDs = append(mutualIDs, r.ID)
		}
	}
	mutual := len(mutualIDs) > 0
	if mutual {
		mutualIDs = append(mutualIDs, liked.ID)
		if _, err := tx.Exec(ctx,
			`UPDATE likes SET status = $1 WHERE id = ANY($2)`, LikeStatusMutual, mutualIDs); err != nil {
			return CreateLikeResult{}, fmt.Errorf("db: mark mutual likes: %w", err)
		}
		liked.Status = LikeStatusMutual
	}

	// Notifications. Anonymous likes never carry the initiator's identity.
	if mutual {
		eventKey := fmt.Sprintf("mutual:%d:%d", socialMinInt64(p.InitiatorID, p.TargetID), socialMaxInt64(p.InitiatorID, p.TargetID))
		for _, pair := range [][2]int64{
			{p.TargetID, p.InitiatorID},
			{p.InitiatorID, p.TargetID},
		} {
			recipient := pair[0]
			otherID := pair[1]
			otherName := me.Nickname
			if otherID == target.ID {
				otherName = target.Nickname
			}
			if err := insertNotificationTx(ctx, tx, recipient, "mutual",
				"你和"+socialDisplayName(otherName)+"相互喜欢了",
				"你们可以到「相互喜欢」查看对方的微信号。",
				"/mutual", map[string]any{
					"event_key": eventKey,
					"other_id":  otherID,
				}, eventKey); err != nil {
				return CreateLikeResult{}, err
			}
		}
	} else {
		eventKey := fmt.Sprintf("like:%d", liked.ID)
		title := "有人匿名喜欢了你"
		payload := map[string]any{"event_key": eventKey, "anonymous": true}
		if p.LikeType == LikeTypeReal {
			title = socialDisplayName(me.Nickname) + " 实名喜欢了你"
			payload["anonymous"] = false
			payload["initiator_id"] = p.InitiatorID
		}
		if err := insertNotificationTx(ctx, tx, p.TargetID, "like", title,
			"去「谁喜欢我」看看吧。", "/liked_me", payload, eventKey); err != nil {
			return CreateLikeResult{}, err
		}
	}

	records, err = listLikesTx(ctx, tx, p.InitiatorID)
	if err != nil {
		return CreateLikeResult{}, err
	}
	quota = ComputeLikeQuota(records, permanent, currentMonth)
	if err := tx.Commit(ctx); err != nil {
		return CreateLikeResult{}, fmt.Errorf("db: commit like: %w", err)
	}
	return CreateLikeResult{Like: liked, Mutual: mutual, Quota: quota}, nil
}

type socialUserRow struct {
	ID            int64
	Name          string
	Nickname      string
	Gender        string
	AccountKind   string
	AccountStatus string
	LoginStatus   string
}

func lockSocialActor(ctx context.Context, tx pgx.Tx, id int64) (socialUserRow, error) {
	var u socialUserRow
	err := tx.QueryRow(ctx,
		`SELECT id, name, COALESCE(NULLIF(nickname, ''), ''), gender, account_kind, account_status, status
		 FROM users WHERE id = $1 FOR UPDATE`, id).
		Scan(&u.ID, &u.Name, &u.Nickname, &u.Gender, &u.AccountKind, &u.AccountStatus, &u.LoginStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return socialUserRow{}, ErrNotFound
	}
	if err != nil {
		return socialUserRow{}, fmt.Errorf("db: lock social actor: %w", err)
	}
	return u, nil
}

func socialUserState(ctx context.Context, tx pgx.Tx, id int64) (socialUserRow, error) {
	var u socialUserRow
	err := tx.QueryRow(ctx,
		`SELECT id, name, COALESCE(NULLIF(nickname, ''), ''), gender, account_kind, account_status, status
		 FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Name, &u.Nickname, &u.Gender, &u.AccountKind, &u.AccountStatus, &u.LoginStatus)
	return u, err
}

func validateSocialPair(me, target socialUserRow) error {
	if me.LoginStatus != "active" || me.AccountKind != "user" || me.AccountStatus != "active" {
		return ErrTargetUnavailable
	}
	if target.LoginStatus != "active" || target.AccountKind != "user" || target.AccountStatus != "active" {
		return ErrTargetUnavailable
	}
	if me.Gender == "" || target.Gender == "" {
		return ErrTargetUnavailable
	}
	if me.Gender == target.Gender {
		return ErrSameGender
	}
	if me.Gender != GenderMale && me.Gender != GenderFemale {
		return ErrTargetUnavailable
	}
	return nil
}

func listLikesTx(ctx context.Context, tx pgx.Tx, initiatorID int64) ([]LikeRecord, error) {
	rows, err := tx.Query(ctx,
		`SELECT initiator_id, target_id, status, like_type, COALESCE(month, '')
		 FROM likes WHERE initiator_id = $1`, initiatorID)
	if err != nil {
		return nil, fmt.Errorf("db: list likes for quota: %w", err)
	}
	defer rows.Close()
	var out []LikeRecord
	for rows.Next() {
		var r LikeRecord
		if err := rows.Scan(&r.InitiatorID, &r.TargetID, &r.Status, &r.LikeType, &r.Month); err != nil {
			return nil, fmt.Errorf("db: scan like quota row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate like quota rows: %w", err)
	}
	return out, nil
}

func listLikesTxBetween(ctx context.Context, tx pgx.Tx, initiatorID, targetID int64) ([]Like, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, initiator_id, target_id, status, like_type, COALESCE(message, ''),
		        COALESCE(month, ''), created_at
		 FROM likes WHERE initiator_id = $1 AND target_id = $2`, initiatorID, targetID)
	if err != nil {
		return nil, fmt.Errorf("db: list reciprocal likes: %w", err)
	}
	defer rows.Close()
	var out []Like
	for rows.Next() {
		var l Like
		if err := rows.Scan(&l.ID, &l.InitiatorID, &l.TargetID, &l.Status,
			&l.LikeType, &l.Message, &l.Month, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan reciprocal like: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate reciprocal likes: %w", err)
	}
	return out, nil
}

// LikeQuotaFor computes the viewer's current quota without writing. Redeemed
// extra real-name slots are included so the API matches CreateLike and /points.
func (d *DB) LikeQuotaFor(ctx context.Context, userID int64, now time.Time) (LikeQuota, error) {
	records, err := d.listLikeQuotaRecords(ctx, userID)
	if err != nil {
		return LikeQuota{}, err
	}
	permanent, err := d.ExtraRealLikeQuota(ctx, userID)
	if err != nil {
		return LikeQuota{}, err
	}
	return ComputeLikeQuota(records, permanent, MonthKey(now)), nil
}

func (d *DB) listLikeQuotaRecords(ctx context.Context, userID int64) ([]LikeRecord, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT initiator_id, target_id, status, like_type, COALESCE(month, '')
		 FROM likes WHERE initiator_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list like quota: %w", err)
	}
	defer rows.Close()
	var out []LikeRecord
	for rows.Next() {
		var r LikeRecord
		if err := rows.Scan(&r.InitiatorID, &r.TargetID, &r.Status, &r.LikeType, &r.Month); err != nil {
			return nil, fmt.Errorf("db: scan like quota: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate like quota: %w", err)
	}
	return out, nil
}

func insertNotificationTx(ctx context.Context, tx pgx.Tx, userID int64,
	kind, title, body, link string, payload map[string]any, eventKey string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("db: marshal notification payload: %w", err)
	}
	_, err = tx.Exec(ctx,
		`WITH inserted AS (
		   INSERT INTO notifications (user_id, kind, title, body, link, payload)
		   SELECT $1, $2, $3, $4, $5, $6::jsonb
		   WHERE NOT EXISTS (
		     SELECT 1 FROM notifications
		     WHERE user_id = $1 AND payload->>'event_key' = $7
		   )
		   RETURNING user_id
		 )
		 SELECT pg_notify($8, user_id::text) FROM inserted`,
		userID, kind, title, body, link, string(raw), eventKey, NotificationChannel)
	if err != nil {
		return fmt.Errorf("db: create notification: %w", err)
	}
	return nil
}

func pairLockKey(a, b int64) int64 {
	if a > b {
		a, b = b, a
	}
	// FNV-1a over the ordered pair. Only equality of the pair matters; rare
	// collisions merely serialize unrelated like writes.
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, v := range []int64{a, b} {
		x := uint64(v)
		for i := 0; i < 8; i++ {
			h ^= x & 0xff
			h *= prime
			x >>= 8
		}
		h ^= 0xff
		h *= prime
	}
	return int64(h)
}

func socialMinInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func socialMaxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func socialDisplayName(nickname string) string {
	if strings.TrimSpace(nickname) == "" {
		return "一位会员"
	}
	return nickname
}

// SocialNotification is kept as an alias for the shared notification model.
type SocialNotification = Notification

const socialProfileColumns = `u.id, COALESCE(u.legacy_user_id, ''), COALESCE(u.legacy_open_id, ''), u.name,
	COALESCE(u.nickname, ''), u.gender, u.birthday, u.height_cm,
	COALESCE(u.education, ''), COALESCE(u.baptismal_name, ''),
	COALESCE(u.church, ''), COALESCE(u.church_location, ''), COALESCE(u.church_group, ''),
	COALESCE(u.native_place, ''), COALESCE(u.city, ''), COALESCE(u.industry, ''),
	COALESCE(u.position, ''), COALESCE(u.income, ''), COALESCE(u.house, ''),
	COALESCE(u.driving, ''), COALESCE(u.house_note_have, ''), COALESCE(u.house_note_none, ''),
	COALESCE(u.driving_note_have, ''), COALESCE(u.driving_note_none, ''),
	COALESCE(u.marriage, ''), COALESCE(u.family, ''), COALESCE(u.live_with_parents, ''),
	COALESCE(u.personality, ''), u.self_traits, u.self_hobbies, u.self_sports, u.mbti,
	COALESCE(u.partner_criteria, ''), u.partner_traits, u.partner_hobbies, u.partner_sports,
	COALESCE(u.wechat, ''), u.contact_consent,
	u.account_kind, u.account_status, u.status,
	COALESCE(ph.photos, '{}'::text[])`

const socialPhotoJoin = `LEFT JOIN LATERAL (
	SELECT array_agg(pp.media_id ORDER BY pp.is_cover DESC, pp.sort_order) AS photos
	FROM profile_photos pp WHERE pp.user_id = u.id
) ph ON true`

func scanSocialProfile(row rowScanner) (SocialProfile, error) {
	var (
		p              SocialProfile
		selfTraits     []byte
		selfHobbies    []byte
		selfSports     []byte
		mbti           []byte
		partnerTraits  []byte
		partnerHobbies []byte
		partnerSports  []byte
	)
	err := row.Scan(&p.ID, &p.LegacyID, &p.LegacyOpenID, &p.Name, &p.Nickname, &p.Gender, &p.Birthday,
		&p.HeightCM, &p.Education, &p.BaptismalName, &p.Church, &p.ChurchLocation,
		&p.ChurchGroup, &p.NativePlace, &p.City, &p.Industry, &p.Position, &p.Income,
		&p.House, &p.Driving, &p.HouseNoteHave, &p.HouseNoteNone,
		&p.DrivingNoteHave, &p.DrivingNoteNone, &p.Marriage, &p.Family,
		&p.LiveWithParents, &p.Personality, &selfTraits, &selfHobbies, &selfSports, &mbti,
		&p.PartnerCriteria, &partnerTraits, &partnerHobbies, &partnerSports,
		&p.Wechat, &p.ContactConsent, &p.AccountKind, &p.AccountStatus, &p.LoginStatus,
		&p.Photos)
	if err != nil {
		return SocialProfile{}, err
	}
	p.SelfTraits = decodeSocialStringList(selfTraits)
	p.SelfHobbies = decodeSocialStringList(selfHobbies)
	p.SelfSports = decodeSocialStringList(selfSports)
	p.MBTI = decodeSocialStringList(mbti)
	p.PartnerTraits = decodeSocialStringList(partnerTraits)
	p.PartnerHobbies = decodeSocialStringList(partnerHobbies)
	p.PartnerSports = decodeSocialStringList(partnerSports)
	return p, nil
}

func decodeSocialStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// ViewerBundle is the per-request account context used by the home handler.
// It replaces four separate lookups (viewer row, observer flag, admin role,
// has_face) with one indexed query so the aggregated home payload does not pay
// four round trips.
type ViewerBundle struct {
	Viewer          SocialViewer
	ObserverEnabled bool
	HasFace         bool
	IsAdmin         bool
}

// LoadViewerBundle loads the gate flags for one signed-in user.
func (d *DB) LoadViewerBundle(ctx context.Context, userID int64) (ViewerBundle, error) {
	var b ViewerBundle
	err := d.pool.QueryRow(ctx,
		`SELECT u.id, u.name, COALESCE(NULLIF(u.nickname, ''), ''), u.gender,
		        u.account_kind, u.account_status, u.status, u.id_card_valid,
		        COALESCE(u.observer_enabled, false),
		        EXISTS (SELECT 1 FROM admin_user_roles ur WHERE ur.user_id = u.id),
		        EXISTS (SELECT 1 FROM profile_photos pp
		                 WHERE pp.user_id = u.id AND pp.face_checked AND pp.has_face)
		 FROM users u WHERE u.id = $1`, userID).
		Scan(&b.Viewer.ID, &b.Viewer.Name, &b.Viewer.Nickname, &b.Viewer.Gender,
			&b.Viewer.AccountKind, &b.Viewer.AccountStatus, &b.Viewer.Status, &b.Viewer.IDCardValid,
			&b.ObserverEnabled, &b.IsAdmin, &b.HasFace)
	if errors.Is(err, pgx.ErrNoRows) {
		return ViewerBundle{}, ErrNotFound
	}
	if err != nil {
		return ViewerBundle{}, fmt.Errorf("db: load viewer bundle: %w", err)
	}
	return b, nil
}

// SocialViewer is the gate-relevant account state for the current session user.
// The P0 User model intentionally does not grow profile columns; feature
// modules load what they need instead.
type SocialViewer struct {
	ID            int64
	Name          string
	Nickname      string
	Gender        string
	AccountKind   string
	AccountStatus string
	Status        string
	IDCardValid   bool
}

// GetSocialViewer loads the gate-relevant account row.
func (d *DB) GetSocialViewer(ctx context.Context, id int64) (SocialViewer, error) {
	var v SocialViewer
	err := d.pool.QueryRow(ctx,
		`SELECT id, name, COALESCE(NULLIF(nickname, ''), ''), gender,
		        account_kind, account_status, status, id_card_valid
		 FROM users WHERE id = $1`, id).
		Scan(&v.ID, &v.Name, &v.Nickname, &v.Gender, &v.AccountKind, &v.AccountStatus, &v.Status, &v.IDCardValid)
	if errors.Is(err, pgx.ErrNoRows) {
		return SocialViewer{}, ErrNotFound
	}
	if err != nil {
		return SocialViewer{}, fmt.Errorf("db: get social viewer: %w", err)
	}
	return v, nil
}
