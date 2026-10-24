package db

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/7310C7310C/yixianqian/internal/matching"
)

// weeklyRecommendationLockKey 是生成任务的事务级 advisory lock 键。启动补跑、
// 整点刷新与管理端手动重跑可能同时发生，锁住后只有一个事务在写名单。
const weeklyRecommendationLockKey = 72645301

// GenerateRecommendationsResult 是一次生成/刷新的结果，供日志与管理端展示。
type GenerateRecommendationsResult struct {
	Week    string
	Viewers int
	Entries int
	Rotated bool // 是否跨周（本周第一次生成）
	Changed bool // 是否真的重写了名单
}

// recommendationRow 是一行待写入/已存在的推荐位。
type recommendationRow struct {
	position int
	targetID int64
	pinned   bool
}

// recommendUser 是生成器需要的资料子集。
type recommendUser struct {
	id      int64
	gender  string
	profile matching.Profile
}

// incomingLike 是一条「匿名喜欢我」的记录，排序键与 v1 一致：
// (归属月份, 落库时间) 倒序，最近的在前。
type incomingLike struct {
	month     string
	createdAt time.Time
	initiator int64
}

// GenerateWeeklyRecommendations 按 v1 lib/recommend.py 的规则生成牵线页推荐位。
//
//   - 必显：匿名喜欢我的异性单身用户，最近优先、去重、排除自己；相互喜欢不算
//     （那已经不是秘密了），实名喜欢也不算。
//   - 候选：异性单身，排除已喜欢/已互相喜欢/必显/自己，按相似度降序、同分按 ID。
//   - 周一换批：保留 4 个算法位（先被必显占），其余换新；同周只做
//     「剔除失效 + 补空位」，不整批换。
//
// 幂等：同一周、同一数据重复调用不会改名单（也不刷 created_at）；名单只在
// 真正变化时重写。整个生成过程在一个事务里完成，跨进程用 advisory lock 串行化。
func (d *DB) GenerateWeeklyRecommendations(ctx context.Context, now time.Time) (GenerateRecommendationsResult, error) {
	now = InSocialTZ(now)
	week := WeekKey(now)
	currentMonth := MonthKey(now)
	result := GenerateRecommendationsResult{Week: week}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("db: begin weekly recommendations: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, weeklyRecommendationLockKey); err != nil {
		return result, fmt.Errorf("db: lock weekly recommendations: %w", err)
	}

	users, err := loadRecommendUsers(ctx, tx)
	if err != nil {
		return result, err
	}
	byID := make(map[int64]recommendUser, len(users))
	for _, u := range users {
		byID[u.id] = u
	}

	byMe, toMe, err := loadRecommendLikes(ctx, tx, currentMonth)
	if err != nil {
		return result, err
	}

	// 本周已有名单 → 同周刷新；否则视为跨周轮换，读上一周的算法位。
	currentRows, err := loadWeekRecommendationRows(ctx, tx, week)
	if err != nil {
		return result, err
	}
	rotated := len(currentRows) == 0
	prevAlgo := make(map[int64][]int64)
	if rotated {
		prevWeek, err := previousRecommendationWeek(ctx, tx, week)
		if err != nil {
			return result, err
		}
		if prevWeek != "" {
			prevRows, err := loadWeekRecommendationRows(ctx, tx, prevWeek)
			if err != nil {
				return result, err
			}
			prevAlgo = algoTargetsByViewer(prevRows)
		}
	} else {
		prevAlgo = algoTargetsByViewer(currentRows)
	}
	result.Rotated = rotated

	next := make(map[int64][]recommendationRow, len(users))
	for _, viewer := range users {
		opposite := oppositeGender(viewer.gender)
		if opposite == "" {
			continue
		}
		pinned, pinnedSet := pinnedTargets(viewer.id, opposite, byID, toMe[viewer.id])
		candidates, scores := recommendCandidates(viewer, opposite, pinnedSet, users, byMe[viewer.id])
		matching.SortCandidatesDesc(scores, candidates)
		algo := matching.NextAlgo(prevAlgo[viewer.id], candidates, pinned, rotated)
		list := matching.BuildList(pinned, algo)
		if len(list) == 0 {
			continue
		}
		_, pinnedSlots, _ := matching.BuildSlots(len(pinned))
		rows := make([]recommendationRow, 0, len(list))
		pinnedTake := len(pinned)
		if pinnedTake > pinnedSlots {
			pinnedTake = pinnedSlots
		}
		for i, id := range list {
			rows = append(rows, recommendationRow{position: i, targetID: id, pinned: i < pinnedTake})
		}
		next[viewer.id] = rows
	}

	result.Viewers = len(next)
	for _, rows := range next {
		result.Entries += len(rows)
	}
	if sameRecommendationRows(currentRows, next) {
		if err := tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("db: commit weekly recommendations: %w", err)
		}
		return result, nil
	}
	if err := writeWeekRecommendationRows(ctx, tx, week, next); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("db: commit weekly recommendations: %w", err)
	}
	result.Changed = true
	return result, nil
}

func loadRecommendUsers(ctx context.Context, tx pgx.Tx) ([]recommendUser, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id, u.gender, COALESCE(u.education, ''), COALESCE(u.city, ''),
		       COALESCE(u.church, ''), u.self_hobbies, u.self_sports, u.self_traits, u.mbti
		  FROM users u
		 WHERE u.status = 'active'
		   AND u.account_kind = 'user'
		   AND u.account_status = 'active'
		   AND u.gender IN ($1, $2)
		 ORDER BY u.id`, GenderMale, GenderFemale)
	if err != nil {
		return nil, fmt.Errorf("db: list recommend users: %w", err)
	}
	defer rows.Close()
	out := make([]recommendUser, 0)
	for rows.Next() {
		var (
			u                       recommendUser
			selfHobbies, selfSports []byte
			selfTraits, mbti        []byte
			education, city, church string
		)
		if err := rows.Scan(&u.id, &u.gender, &education, &city, &church,
			&selfHobbies, &selfSports, &selfTraits, &mbti); err != nil {
			return nil, fmt.Errorf("db: scan recommend user: %w", err)
		}
		u.profile = matching.Profile{
			Hobbies:   decodeSocialStringList(selfHobbies),
			Sports:    decodeSocialStringList(selfSports),
			Traits:    decodeSocialStringList(selfTraits),
			Education: education,
			MBTI:      decodeSocialStringList(mbti),
			City:      city,
			Church:    church,
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate recommend users: %w", err)
	}
	return out, nil
}

// loadRecommendLikes 返回 (我喜欢过谁, 谁匿名喜欢我)。
//
// 「谁喜欢我」只收仍然有效的匿名单向喜欢（3 个月到期的由 IsLikeActive 判定，
// 失效的静默出局、不需要清理任务）。相互喜欢不算必显——两个人早就收到通知
// 在聊了。byMe 收全部有效喜欢（含实名/相互），用于排除「已经喜欢过的人」。
func loadRecommendLikes(ctx context.Context, tx pgx.Tx, currentMonth string) (
	map[int64]map[int64]bool, map[int64][]incomingLike, error) {
	rows, err := tx.Query(ctx, `
		SELECT initiator_id, target_id, status, like_type, COALESCE(month, ''), created_at
		  FROM likes
		 WHERE status IN ($1, $2)
		 ORDER BY created_at, id`, LikeStatusSingle, LikeStatusMutual)
	if err != nil {
		return nil, nil, fmt.Errorf("db: list recommend likes: %w", err)
	}
	defer rows.Close()
	byMe := make(map[int64]map[int64]bool)
	toMe := make(map[int64][]incomingLike)
	for rows.Next() {
		var (
			initiatorID, targetID int64
			status, likeType      string
			month                 string
			createdAt             time.Time
		)
		if err := rows.Scan(&initiatorID, &targetID, &status, &likeType, &month, &createdAt); err != nil {
			return nil, nil, fmt.Errorf("db: scan recommend like: %w", err)
		}
		if !IsLikeActive(status, likeType, month, currentMonth) {
			continue
		}
		if byMe[initiatorID] == nil {
			byMe[initiatorID] = make(map[int64]bool)
		}
		byMe[initiatorID][targetID] = true
		if status != LikeStatusSingle || likeType != LikeTypeAnon {
			continue
		}
		toMe[targetID] = append(toMe[targetID], incomingLike{
			month: month, createdAt: createdAt, initiator: initiatorID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("db: iterate recommend likes: %w", err)
	}
	return byMe, toMe, nil
}

// pinnedTargets 按「最近优先」返回必显对象，并挡掉自己、重复记录、性别不符
// 与不在册的人：宁可少一个，也不能推出乱子。
func pinnedTargets(viewerID int64, opposite string, byID map[int64]recommendUser,
	incoming []incomingLike) ([]int64, map[int64]bool) {
	sorted := append([]incomingLike(nil), incoming...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].month != sorted[j].month {
			return sorted[i].month > sorted[j].month
		}
		if !sorted[i].createdAt.Equal(sorted[j].createdAt) {
			return sorted[i].createdAt.After(sorted[j].createdAt)
		}
		// 同一时刻落库的记录按 ID 定序，保证同一份数据每次生成顺序一致。
		return sorted[i].initiator > sorted[j].initiator
	})
	pinned := make([]int64, 0, len(sorted))
	seen := map[int64]bool{viewerID: true}
	for _, like := range sorted {
		src := like.initiator
		if seen[src] {
			continue
		}
		candidate, ok := byID[src]
		if !ok || candidate.gender != opposite {
			continue
		}
		seen[src] = true
		pinned = append(pinned, src)
	}
	return pinned, seen
}

// recommendCandidates 返回本轮算法候选（排除自己、必显、已喜欢过的人）与分数。
func recommendCandidates(viewer recommendUser, opposite string, skip map[int64]bool,
	users []recommendUser, liked map[int64]bool) ([]int64, map[int64]int) {
	candidates := make([]int64, 0, len(users))
	scores := make(map[int64]int, len(users))
	for _, candidate := range users {
		if candidate.gender != opposite {
			continue
		}
		if skip[candidate.id] || candidate.id == viewer.id || liked[candidate.id] {
			continue
		}
		candidates = append(candidates, candidate.id)
		scores[candidate.id] = matching.MatchScore(viewer.profile, candidate.profile)
	}
	return candidates, scores
}

func loadWeekRecommendationRows(ctx context.Context, tx pgx.Tx, week string) (
	map[int64][]recommendationRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT user_id, position, target_user_id, (pinned_by_user_id IS NOT NULL)
		  FROM weekly_recommendations
		 WHERE week = $1
		 ORDER BY user_id, position`, week)
	if err != nil {
		return nil, fmt.Errorf("db: list weekly recommendations: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]recommendationRow)
	for rows.Next() {
		var viewerID int64
		var row recommendationRow
		if err := rows.Scan(&viewerID, &row.position, &row.targetID, &row.pinned); err != nil {
			return nil, fmt.Errorf("db: scan weekly recommendation: %w", err)
		}
		out[viewerID] = append(out[viewerID], row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate weekly recommendations: %w", err)
	}
	return out, nil
}

// previousRecommendationWeek 返回比当前周更早的最近一周。week 形如
// "2026-W01"，定宽零填充，文本比较等价于时间先后。
func previousRecommendationWeek(ctx context.Context, tx pgx.Tx, week string) (string, error) {
	var prev string
	err := tx.QueryRow(ctx,
		`SELECT week FROM weekly_recommendations WHERE week < $1
		  ORDER BY week DESC LIMIT 1`, week).Scan(&prev)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("db: find previous recommendation week: %w", err)
	}
	return prev, nil
}

func algoTargetsByViewer(rows map[int64][]recommendationRow) map[int64][]int64 {
	out := make(map[int64][]int64, len(rows))
	for viewerID, list := range rows {
		algo := make([]int64, 0, len(list))
		for _, row := range list {
			if row.pinned {
				continue
			}
			algo = append(algo, row.targetID)
		}
		if len(algo) > 0 {
			out[viewerID] = algo
		}
	}
	return out
}

func sameRecommendationRows(current, next map[int64][]recommendationRow) bool {
	if len(current) != len(next) {
		return false
	}
	for viewerID, nextRows := range next {
		currentRows, ok := current[viewerID]
		if !ok || len(currentRows) != len(nextRows) {
			return false
		}
		for i := range nextRows {
			if currentRows[i].position != nextRows[i].position ||
				currentRows[i].targetID != nextRows[i].targetID ||
				currentRows[i].pinned != nextRows[i].pinned {
				return false
			}
		}
	}
	return true
}

func writeWeekRecommendationRows(ctx context.Context, tx pgx.Tx, week string,
	rows map[int64][]recommendationRow) error {
	if _, err := tx.Exec(ctx, `DELETE FROM weekly_recommendations WHERE week = $1`, week); err != nil {
		return fmt.Errorf("db: clear weekly recommendations: %w", err)
	}
	// 排序只为让写入顺序稳定（测试/审计可复现），主键已保证唯一。
	viewerIDs := make([]int64, 0, len(rows))
	for viewerID := range rows {
		viewerIDs = append(viewerIDs, viewerID)
	}
	sort.Slice(viewerIDs, func(i, j int) bool { return viewerIDs[i] < viewerIDs[j] })
	for _, viewerID := range viewerIDs {
		for _, row := range rows[viewerID] {
			var pinnedBy any
			if row.pinned {
				// 必显原因只存服务端；管理端与 API 都不读这一列。
				pinnedBy = row.targetID
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO weekly_recommendations (week, user_id, position, target_user_id, pinned_by_user_id)
				VALUES ($1, $2, $3, $4, $5)`,
				week, viewerID, row.position, row.targetID, pinnedBy); err != nil {
				return fmt.Errorf("db: insert weekly recommendation: %w", err)
			}
		}
	}
	return nil
}

// WeeklyRecommendationTargets 返回某个用户本周的推荐位（按位置升序）。
// 必显与算法位在对外投影里不做区分。
func (d *DB) WeeklyRecommendationTargets(ctx context.Context, viewerID int64, week string) ([]int64, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT target_user_id FROM weekly_recommendations
		 WHERE week = $1 AND user_id = $2
		 ORDER BY position`, week, viewerID)
	if err != nil {
		return nil, fmt.Errorf("db: list weekly recommendation targets: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0, 7)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: scan weekly recommendation target: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate weekly recommendation targets: %w", err)
	}
	return out, nil
}

// RecommendationViewerSummary 是管理端只读名单的一行。它刻意不含任何
// 「谁暗恋谁」信息：必显原因只留在服务端。
type RecommendationViewerSummary struct {
	UserID    int64
	Nickname  string
	Count     int
	UpdatedAt time.Time
}

// RecommendationWeekSummary 是管理端「本周推荐」区块的汇总。
type RecommendationWeekSummary struct {
	Week      string
	Viewers   int
	Entries   int
	Generated time.Time
	Sample    []RecommendationViewerSummary
}

// WeeklyRecommendationSummary 汇总某一周的名单。sampleLimit <= 0 时不取样。
func (d *DB) WeeklyRecommendationSummary(ctx context.Context, week string, sampleLimit int) (RecommendationWeekSummary, error) {
	out := RecommendationWeekSummary{Week: week, Sample: make([]RecommendationViewerSummary, 0)}
	err := d.pool.QueryRow(ctx, `
		SELECT count(DISTINCT user_id), count(*), COALESCE(max(created_at), 'epoch'::timestamptz)
		  FROM weekly_recommendations WHERE week = $1`, week).
		Scan(&out.Viewers, &out.Entries, &out.Generated)
	if err != nil {
		return out, fmt.Errorf("db: summarize weekly recommendations: %w", err)
	}
	if sampleLimit <= 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx, `
		SELECT w.user_id, COALESCE(u.nickname, ''), count(*), max(w.created_at)
		  FROM weekly_recommendations w
		  JOIN users u ON u.id = w.user_id
		 WHERE w.week = $1
		 GROUP BY w.user_id, u.nickname
		 ORDER BY max(w.created_at) DESC, w.user_id
		 LIMIT $2`, week, sampleLimit)
	if err != nil {
		return out, fmt.Errorf("db: list weekly recommendation viewers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row RecommendationViewerSummary
		if err := rows.Scan(&row.UserID, &row.Nickname, &row.Count, &row.UpdatedAt); err != nil {
			return out, fmt.Errorf("db: scan weekly recommendation viewer: %w", err)
		}
		out.Sample = append(out.Sample, row)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("db: iterate weekly recommendation viewers: %w", err)
	}
	return out, nil
}
