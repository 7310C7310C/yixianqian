package db

import (
	"context"
	"fmt"
	"math/bits"
	"time"
)

// CardFilter is the parsed card filter set. Zero values mean "not set"; text
// filters are literal case-insensitive substring matches (v1 parity).
type CardFilter struct {
	Nickname    string
	UserID      string
	City        string
	Church      string
	NativePlace string
	Industry    string
	Education   []string
	Income      []string
	House       []string
	Driving     []string
	AgeMin      int
	AgeMax      int
	HeightMin   int
	HeightMax   int
}

// ListCardCandidates returns the eligible card pool ordered by user id (a
// stable input order for OrderCards). targetGender "" means both genders
// (observer browsing).
func (d *DB) ListCardCandidates(ctx context.Context, viewerID int64, targetGender string,
	f CardFilter, now time.Time) ([]SocialProfile, error) {
	ageMinAt, ageMaxAt := socialAgeThresholds(f.AgeMin, f.AgeMax, now)
	var heightMin, heightMax *int
	if f.HeightMin > 0 {
		v := f.HeightMin
		heightMin = &v
	}
	if f.HeightMax > 0 {
		v := f.HeightMax
		heightMax = &v
	}
	rows, err := d.pool.Query(ctx,
		`SELECT `+socialProfileColumns+`
		 FROM users u `+socialPhotoJoin+`
		 WHERE u.status = 'active'
		   AND u.account_kind = 'user'
		   AND u.account_status = 'active'
		   AND u.id <> $1
		   AND ($2 = '' OR u.gender = $2)
		   AND ph.photos IS NOT NULL AND cardinality(ph.photos) > 0
		   AND ($3 = '' OR position(lower($3) in lower(COALESCE(u.nickname, ''))) > 0)
		   AND ($4 = '' OR position(lower($4) in lower(COALESCE(u.legacy_user_id, ''))) > 0)
		   AND ($5 = '' OR position(lower($5) in lower(COALESCE(u.city, ''))) > 0)
		   AND ($6 = '' OR position(lower($6) in lower(COALESCE(u.church, ''))) > 0)
		   AND ($7 = '' OR position(lower($7) in lower(COALESCE(u.native_place, ''))) > 0)
		   AND ($8 = '' OR position(lower($8) in lower(COALESCE(u.industry, ''))) > 0)
		   AND (cardinality($9::text[]) = 0 OR u.education = ANY($9::text[]))
		   AND (cardinality($10::text[]) = 0 OR COALESCE(NULLIF(u.income, ''), '未填') = ANY($10::text[]))
		   AND (cardinality($11::text[]) = 0 OR COALESCE(NULLIF(u.house, ''), '未填') = ANY($11::text[]))
		   AND (cardinality($12::text[]) = 0 OR COALESCE(NULLIF(u.driving, ''), '未填') = ANY($12::text[]))
		   AND ($13::date IS NULL OR (u.birthday IS NOT NULL AND u.birthday <= $13::date))
		   AND ($14::date IS NULL OR (u.birthday IS NOT NULL AND u.birthday > $14::date))
		   AND ($15::int IS NULL OR COALESCE(u.height_cm, 0) >= $15)
		   AND ($16::int IS NULL OR (u.height_cm IS NOT NULL AND u.height_cm <= $16))
		 ORDER BY u.id`,
		viewerID, targetGender, f.Nickname, f.UserID, f.City, f.Church,
		f.NativePlace, f.Industry, socialNonNilStrings(f.Education), socialNonNilStrings(f.Income),
		socialNonNilStrings(f.House), socialNonNilStrings(f.Driving),
		ageMinAt, ageMaxAt, heightMin, heightMax)
	if err != nil {
		return nil, fmt.Errorf("db: list card candidates: %w", err)
	}
	defer rows.Close()
	var out []SocialProfile
	for rows.Next() {
		p, err := scanSocialProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan card candidate: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate card candidates: %w", err)
	}
	return out, nil
}

func socialNonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func socialAgeThresholds(ageMin, ageMax int, now time.Time) (*time.Time, *time.Time) {
	t := now.In(shanghai)
	today := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, shanghai)
	var minAt, maxAt *time.Time
	if ageMin > 0 {
		v := today.AddDate(-ageMin, 0, 0)
		minAt = &v
	}
	if ageMax > 0 {
		// Age > ageMax means born on or before this date; invert to ">".
		v := today.AddDate(-(ageMax + 1), 0, 0)
		maxAt = &v
	}
	return minAt, maxAt
}

// CardSocialState is the viewer-specific overlay for the card deck.
type CardSocialState struct {
	LikedTargets   map[int64]bool
	LikedMe        map[int64]bool
	PraisedTargets map[int64]bool
	MessageCounts  map[int64]int
}

// LoadCardSocialState loads likes/praises relevant to deck rendering.
func (d *DB) LoadCardSocialState(ctx context.Context, viewerID int64, now time.Time) (CardSocialState, error) {
	state := CardSocialState{
		LikedTargets:   map[int64]bool{},
		LikedMe:        map[int64]bool{},
		PraisedTargets: map[int64]bool{},
		MessageCounts:  map[int64]int{},
	}
	currentMonth := MonthKey(now)
	outgoing, err := d.listLikesByDirection(ctx, "initiator_id", viewerID)
	if err != nil {
		return state, err
	}
	for _, l := range outgoing {
		if IsLikeActive(l.Status, l.LikeType, l.Month, currentMonth) {
			state.LikedTargets[l.TargetID] = true
		}
	}
	incoming, err := d.listLikesByDirection(ctx, "target_id", viewerID)
	if err != nil {
		return state, err
	}
	for _, l := range incoming {
		if IsLikeActive(l.Status, l.LikeType, l.Month, currentMonth) {
			state.LikedMe[l.InitiatorID] = true
		}
	}
	praisedRows, err := d.pool.Query(ctx,
		`SELECT target_id FROM praises WHERE initiator_id = $1 AND status = $2`,
		viewerID, PraiseStatusActive)
	if err != nil {
		return state, fmt.Errorf("db: list praise targets: %w", err)
	}
	for praisedRows.Next() {
		var id int64
		if err := praisedRows.Scan(&id); err != nil {
			praisedRows.Close()
			return state, fmt.Errorf("db: scan praise target: %w", err)
		}
		state.PraisedTargets[id] = true
	}
	if err := praisedRows.Err(); err != nil {
		praisedRows.Close()
		return state, fmt.Errorf("db: iterate praise targets: %w", err)
	}
	praisedRows.Close()

	countRows, err := d.pool.Query(ctx,
		`SELECT target_user_id, count(*) FROM messages
		 WHERE status <> '已删除' GROUP BY target_user_id`)
	if err != nil {
		return state, fmt.Errorf("db: count messages per target: %w", err)
	}
	for countRows.Next() {
		var id int64
		var n int
		if err := countRows.Scan(&id, &n); err != nil {
			countRows.Close()
			return state, fmt.Errorf("db: scan message count: %w", err)
		}
		state.MessageCounts[id] = n
	}
	if err := countRows.Err(); err != nil {
		countRows.Close()
		return state, fmt.Errorf("db: iterate message counts: %w", err)
	}
	countRows.Close()
	return state, nil
}

func (d *DB) listLikesByDirection(ctx context.Context, column string, viewerID int64) ([]Like, error) {
	// column is an internal constant chosen by the caller, never user input.
	query := `SELECT id, initiator_id, target_id, status, like_type, COALESCE(message, ''),
	                 COALESCE(month, ''), created_at
	          FROM likes WHERE ` + column + ` = $1 AND status IN ($2, $3)`
	rows, err := d.pool.Query(ctx, query, viewerID, LikeStatusSingle, LikeStatusMutual)
	if err != nil {
		return nil, fmt.Errorf("db: list likes by %s: %w", column, err)
	}
	defer rows.Close()
	var out []Like
	for rows.Next() {
		var l Like
		if err := rows.Scan(&l.ID, &l.InitiatorID, &l.TargetID, &l.Status,
			&l.LikeType, &l.Message, &l.Month, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan like by %s: %w", column, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate likes by %s: %w", column, err)
	}
	return out, nil
}

// OrderCards applies v1 deck ordering: pinned cards first (in the given order),
// everyone else shuffled with a stable seed, and people who liked the viewer
// scattered randomly inside the first 30%.
func OrderCards(cards []SocialProfile, likedMeIDs map[int64]bool, pinnedIDs []int64, seed string) []SocialProfile {
	if len(cards) == 0 {
		return nil
	}
	byID := make(map[int64]SocialProfile, len(cards))
	for _, c := range cards {
		if _, exists := byID[c.ID]; !exists {
			byID[c.ID] = c
		}
	}
	head := make([]SocialProfile, 0, len(pinnedIDs))
	used := make(map[int64]bool, len(pinnedIDs))
	for _, id := range pinnedIDs {
		if used[id] {
			continue
		}
		if c, ok := byID[id]; ok {
			head = append(head, c)
			used[id] = true
		}
	}
	rest := make([]SocialProfile, 0, len(cards))
	for _, c := range cards {
		if !used[c.ID] {
			rest = append(rest, c)
			used[c.ID] = true
		}
	}
	if len(rest) == 0 {
		return head
	}
	rng := newSocialStableRNG(seed)
	if len(likedMeIDs) == 0 {
		shuffleSocialCards(rest, rng)
		return append(head, rest...)
	}
	liked := make([]SocialProfile, 0, len(rest))
	others := make([]SocialProfile, 0, len(rest))
	for _, c := range rest {
		if likedMeIDs[c.ID] {
			liked = append(liked, c)
		} else {
			others = append(others, c)
		}
	}
	shuffleSocialCards(liked, rng)
	shuffleSocialCards(others, rng)
	frontN := len(rest) * 3 / 10
	if frontN < 1 {
		frontN = 1
	}
	if frontN > len(rest) {
		frontN = len(rest)
	}
	takeLiked := len(liked)
	if takeLiked > frontN {
		takeLiked = frontN
	}
	positions := rng.samplePositions(frontN, takeLiked)
	front := make([]SocialProfile, 0, frontN)
	li, oi := 0, 0
	for i := 0; i < frontN; i++ {
		if positions[i] {
			if li < len(liked) {
				front = append(front, liked[li])
				li++
				continue
			}
		}
		if oi < len(others) {
			front = append(front, others[oi])
			oi++
			continue
		}
		if li < len(liked) {
			front = append(front, liked[li])
			li++
		}
	}
	tail := make([]SocialProfile, 0, len(liked)-li+len(others)-oi)
	tail = append(tail, liked[li:]...)
	tail = append(tail, others[oi:]...)
	shuffleSocialCards(tail, rng)
	out := make([]SocialProfile, 0, len(head)+len(front)+len(tail))
	out = append(out, head...)
	out = append(out, front...)
	out = append(out, tail...)
	return out
}

type socialStableRNG struct{ state uint64 }

func newSocialStableRNG(seed string) *socialStableRNG {
	// FNV-1a is stable across processes and Go versions.
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(seed); i++ {
		h ^= uint64(seed[i])
		h *= prime
	}
	if h == 0 {
		h = 0x9e3779b97f4a7c15
	}
	return &socialStableRNG{state: h}
}

func (r *socialStableRNG) next() uint64 {
	// SplitMix64.
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *socialStableRNG) intn(n int) int {
	if n <= 1 {
		return 0
	}
	// 64-bit multiply high gives an unbiased-enough mapping for our sizes.
	hi, _ := bits.Mul64(r.next(), uint64(n))
	return int(hi)
}

func (r *socialStableRNG) samplePositions(n, k int) map[int]bool {
	out := make(map[int]bool, k)
	if k <= 0 || n <= 0 {
		return out
	}
	if k > n {
		k = n
	}
	for len(out) < k {
		out[r.intn(n)] = true
	}
	return out
}

func shuffleSocialCards[T any](items []T, rng *socialStableRNG) {
	for i := len(items) - 1; i > 0; i-- {
		j := rng.intn(i + 1)
		items[i], items[j] = items[j], items[i]
	}
}
