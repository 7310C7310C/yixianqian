package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// recommendInsertLike 直接写 likes 行，便于控制状态、月份与落库时间
// （CreateLike 会做配额与配对校验，测试需要构造「已失效」「相互喜欢」等边角）。
func recommendInsertLike(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	initiator, target int64, status, likeType, month string, createdAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO likes (initiator_id, target_id, status, like_type, month, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		initiator, target, status, likeType, month, createdAt); err != nil {
		t.Fatalf("insert recommend like: %v", err)
	}
}

func recommendPinnedIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	week string, viewerID int64) []int64 {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT target_user_id FROM weekly_recommendations
		 WHERE week = $1 AND user_id = $2 AND pinned_by_user_id IS NOT NULL
		 ORDER BY position`, week, viewerID)
	if err != nil {
		t.Fatalf("query pinned ids: %v", err)
	}
	defer rows.Close()
	out := make([]int64, 0, 4)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan pinned id: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pinned ids: %v", err)
	}
	return out
}

func recommendContains(list []int64, id int64) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func recommendSet(list []int64) map[int64]bool {
	out := make(map[int64]bool, len(list))
	for _, id := range list {
		out[id] = true
	}
	return out
}

// TestIntegrationWeeklyRecommendations 用 16 个测试用户覆盖 v1 规则：
// 必显（匿名/最近优先/去重/异性/失效过滤）、候选排除、每周保留 4 个换 3 个、
// 同周幂等与失效剔除、必显≥4 时算法位仍 3 个。
func TestIntegrationWeeklyRecommendations(t *testing.T) {
	database, pool, ctx := setupGroupingDB(t)
	// 本表没有其他测试使用；清空可避免上一次运行残留的周影响「上一周」判断。
	if _, err := pool.Exec(ctx, `DELETE FROM weekly_recommendations`); err != nil {
		t.Fatalf("clear weekly recommendations: %v", err)
	}

	now1 := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC) // 周一 18:00（Asia/Shanghai）
	week1 := WeekKey(now1)
	week2 := WeekKey(now1.AddDate(0, 0, 7))
	if week1 != "2026-W10" || week2 != "2026-W11" {
		t.Fatalf("unexpected ISO weeks: %s %s", week1, week2)
	}

	males := make([]User, 6)
	for i := range males {
		males[i] = createGroupUser(t, ctx, database, pool, fmt.Sprintf("U-REC-M%d", i), GenderMale)
	}
	// 12 个女生：M5 排除已实名喜欢的 F5 后仍有 11 个候选，才能验证「换 3 个生面孔」；
	// 候选不够时 v1 允许旧人回填，那不算轮换失败。
	females := make([]User, 12)
	for i := range females {
		females[i] = createGroupUser(t, ctx, database, pool, fmt.Sprintf("U-REC-F%d", i), GenderFemale)
	}
	male := func(i int) int64 { return males[i].ID }
	female := func(i int) int64 { return females[i].ID }

	// F0、F1 匿名喜欢 M0：F1 更近，必须排最前（即便 F0 更早）。
	recommendInsertLike(t, ctx, pool, female(0), male(0), LikeStatusSingle, LikeTypeAnon,
		"2026-02", time.Date(2026, 2, 15, 9, 0, 0, 0, time.UTC))
	recommendInsertLike(t, ctx, pool, female(1), male(0), LikeStatusSingle, LikeTypeAnon,
		"2026-03", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC))
	// F2..F6 喜欢 M1：必显 5 个，只上最近 4 个，算法位仍保底 3 个。
	for i := 2; i <= 6; i++ {
		recommendInsertLike(t, ctx, pool, female(i), male(1), LikeStatusSingle, LikeTypeAnon,
			"2026-03", time.Date(2026, 3, 1, 10+i, 0, 0, 0, time.UTC))
	}
	// M2 已喜欢 F7 → F7 不能再进 M2 的候选。
	recommendInsertLike(t, ctx, pool, male(2), female(7), LikeStatusSingle, LikeTypeAnon,
		"2026-03", time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC))
	// M3 与 F8 已相互喜欢 → 既不算必显，也不再是候选。
	recommendInsertLike(t, ctx, pool, male(3), female(8), LikeStatusMutual, LikeTypeAnon,
		"2026-03", time.Date(2026, 2, 20, 8, 0, 0, 0, time.UTC))
	recommendInsertLike(t, ctx, pool, female(8), male(3), LikeStatusMutual, LikeTypeAnon,
		"2026-03", time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC))
	// F9 喜欢 M4 但匿名喜欢已过期（超过 3 个月）→ 不是必显。
	recommendInsertLike(t, ctx, pool, female(9), male(4), LikeStatusSingle, LikeTypeAnon,
		"2025-10", time.Date(2025, 10, 1, 9, 0, 0, 0, time.UTC))
	// M5 已实名喜欢 F5 → F5 不能再进 M5 的候选（实名喜欢也不算必显）。
	recommendInsertLike(t, ctx, pool, male(5), female(5), LikeStatusSingle, LikeTypeReal,
		"2026-03", time.Date(2026, 3, 1, 8, 30, 0, 0, time.UTC))
	// 同性喜欢（脏数据）不能变成必显。
	recommendInsertLike(t, ctx, pool, female(0), female(1), LikeStatusSingle, LikeTypeAnon,
		"2026-03", time.Date(2026, 3, 1, 7, 0, 0, 0, time.UTC))

	first, err := database.GenerateWeeklyRecommendations(ctx, now1)
	if err != nil {
		t.Fatalf("generate week1: %v", err)
	}
	if !first.Rotated || !first.Changed {
		t.Fatalf("first generation rotated=%v changed=%v, want true/true", first.Rotated, first.Changed)
	}
	if first.Week != week1 {
		t.Fatalf("result week = %s, want %s", first.Week, week1)
	}

	list := func(viewerID int64) []int64 {
		t.Helper()
		ids, err := database.WeeklyRecommendationTargets(ctx, viewerID, week1)
		if err != nil {
			t.Fatalf("list recommendations: %v", err)
		}
		return ids
	}

	// 必显 2 个：最近的 F1 在前，F0 在后；总数 7。
	m0 := list(male(0))
	if len(m0) != 7 {
		t.Fatalf("M0 list = %v, want 7 entries", m0)
	}
	if m0[0] != female(1) || m0[1] != female(0) {
		t.Fatalf("M0 pinned head = %v, want [F1 F0]", m0[:2])
	}
	if pinned := recommendPinnedIDs(t, ctx, pool, week1, male(0)); len(pinned) != 2 {
		t.Fatalf("M0 pinned rows = %v, want 2", pinned)
	}

	// 必显 ≥4：M1 有 5 个暗恋者，只上最近 4 个；算法位仍是 3 个。
	m1 := list(male(1))
	if len(m1) != 7 {
		t.Fatalf("M1 list = %v, want 7 entries", m1)
	}
	wantPinned := []int64{female(6), female(5), female(4), female(3)}
	for i, id := range wantPinned {
		if m1[i] != id {
			t.Fatalf("M1 pinned[%d] = %d, want %d (%v)", i, m1[i], id, m1)
		}
	}
	if recommendContains(m1, female(2)) {
		t.Fatalf("5th pinned F2 must not take a slot: %v", m1)
	}
	pinnedM1 := recommendSet(recommendPinnedIDs(t, ctx, pool, week1, male(1)))
	if len(pinnedM1) != 4 {
		t.Fatalf("M1 pinned rows = %d, want 4", len(pinnedM1))
	}
	for _, id := range m1[4:] {
		if pinnedM1[id] {
			t.Fatalf("M1 algo slot contains pinned id %d: %v", id, m1)
		}
	}
	// 算法位确实有 3 个（不是被必显挤掉）。
	if len(m1)-len(pinnedM1) != 3 {
		t.Fatalf("M1 algo entries = %d, want 3", len(m1)-len(pinnedM1))
	}

	// 候选排除：已喜欢、已相互喜欢、实名喜欢、同性、自己。
	if recommendContains(list(male(2)), female(7)) {
		t.Fatal("M2 list contains an already-liked user")
	}
	if recommendContains(list(male(3)), female(8)) {
		t.Fatal("M3 list contains a mutual match")
	}
	if recommendContains(list(male(5)), female(5)) {
		t.Fatal("M5 list contains a user he already liked")
	}
	if pinned := recommendPinnedIDs(t, ctx, pool, week1, male(4)); len(pinned) != 0 {
		t.Fatalf("expired anonymous like leaked into pinned: %v", pinned)
	}
	if pinned := recommendPinnedIDs(t, ctx, pool, week1, female(1)); len(pinned) != 0 {
		t.Fatalf("same-gender like leaked into pinned: %v", pinned)
	}
	// 候选池是 users 全表的 active 用户，共享测试库里还有其它用例留下的
	// 账号（`make ci` 连续跑两次时会命中）。因此这里只断言规则本身：
	// 不推荐自己、只推荐异性；不要求每个候选都是本用例的夹具。
	byID := make(map[int64]string, len(males)+len(females))
	viewers := append(append([]User{}, males...), females...)
	viewerIDs := make([]int64, 0, len(viewers))
	for _, viewer := range viewers {
		viewerIDs = append(viewerIDs, viewer.ID)
	}
	fixtureNicknames := make(map[int64]string, len(viewers))
	nicknameRows, err := pool.Query(ctx,
		`SELECT id, nickname FROM users WHERE id = ANY($1)`, viewerIDs)
	if err != nil {
		t.Fatalf("load fixture nicknames: %v", err)
	}
	for nicknameRows.Next() {
		var (
			id       int64
			nickname string
		)
		if err := nicknameRows.Scan(&id, &nickname); err != nil {
			nicknameRows.Close()
			t.Fatalf("scan fixture nickname: %v", err)
		}
		fixtureNicknames[id] = nickname
	}
	nicknameRows.Close()
	if err := nicknameRows.Err(); err != nil {
		t.Fatalf("iterate fixture nicknames: %v", err)
	}
	for _, u := range males {
		byID[u.ID] = GenderMale
	}
	for _, u := range females {
		byID[u.ID] = GenderFemale
	}
	recommended := make(map[int64]bool)
	for _, viewer := range viewers {
		for _, id := range list(viewer.ID) {
			recommended[id] = true
		}
	}
	recommendedIDs := make([]int64, 0, len(recommended))
	for id := range recommended {
		recommendedIDs = append(recommendedIDs, id)
	}
	if len(recommendedIDs) > 0 {
		genderRows, err := pool.Query(ctx,
			`SELECT id, COALESCE(gender, '') FROM users WHERE id = ANY($1)`, recommendedIDs)
		if err != nil {
			t.Fatalf("load recommended genders: %v", err)
		}
		for genderRows.Next() {
			var (
				id     int64
				gender string
			)
			if err := genderRows.Scan(&id, &gender); err != nil {
				genderRows.Close()
				t.Fatalf("scan recommended gender: %v", err)
			}
			byID[id] = gender
		}
		genderRows.Close()
		if err := genderRows.Err(); err != nil {
			t.Fatalf("iterate recommended genders: %v", err)
		}
	}
	for _, viewer := range viewers {
		for _, id := range list(viewer.ID) {
			if id == viewer.ID {
				t.Fatalf("viewer %d recommended self", viewer.ID)
			}
			if byID[id] == "" || byID[id] == byID[viewer.ID] {
				t.Fatalf("viewer %d (%s) recommended %d (%s)", viewer.ID, byID[viewer.ID], id, byID[id])
			}
		}
	}

	// 幂等：同一周重跑不改名单，也不刷新 created_at。
	var createdBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT max(created_at) FROM weekly_recommendations WHERE week = $1`,
		week1).Scan(&createdBefore); err != nil {
		t.Fatalf("read generated time: %v", err)
	}
	second, err := database.GenerateWeeklyRecommendations(ctx, now1)
	if err != nil {
		t.Fatalf("regenerate week1: %v", err)
	}
	if second.Changed {
		t.Fatal("same-week rerun rewrote an unchanged list")
	}
	var createdAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT max(created_at) FROM weekly_recommendations WHERE week = $1`,
		week1).Scan(&createdAfter); err != nil {
		t.Fatalf("read generated time after rerun: %v", err)
	}
	if !createdAfter.Equal(createdBefore) {
		t.Fatalf("idempotent rerun refreshed created_at: %v -> %v", createdBefore, createdAfter)
	}
	m0BeforeRotation := list(male(0))
	if again := list(male(0)); !equalIDs(again, m0BeforeRotation) {
		t.Fatalf("idempotent rerun changed M0 list: %v -> %v", m0BeforeRotation, again)
	}
	m5BeforeRotation := list(male(5))
	if len(m5BeforeRotation) != 7 {
		t.Fatalf("M5 week1 list = %v, want 7 entries", m5BeforeRotation)
	}

	// 跨周轮换（必须在同周刷新之前跑，轮换基线才是干净的周一名单）：
	// 无必显的 M5 保留 4 个、换 3 个；有必显的 M0 只保留 2 个算法位。
	next, err := database.GenerateWeeklyRecommendations(ctx, now1.AddDate(0, 0, 7))
	if err != nil {
		t.Fatalf("generate week2: %v", err)
	}
	if !next.Rotated || !next.Changed {
		t.Fatalf("week2 rotated=%v changed=%v, want true/true", next.Rotated, next.Changed)
	}
	if next.Week != week2 {
		t.Fatalf("week2 = %s, want %s", next.Week, week2)
	}
	m5Next, err := database.WeeklyRecommendationTargets(ctx, male(5), week2)
	if err != nil {
		t.Fatalf("list week2 recommendations: %v", err)
	}
	if len(m5Next) != 7 {
		t.Fatalf("M5 week2 list = %v, want 7 entries", m5Next)
	}
	kept := 0
	for _, id := range m5BeforeRotation {
		if recommendContains(m5Next, id) {
			kept++
		}
	}
	if kept != 4 {
		t.Fatalf("M5 kept %d of previous algo slots, want 4 (%v -> %v)",
			kept, m5BeforeRotation, m5Next)
	}
	for i := 0; i < 4; i++ {
		if m5Next[i] != m5BeforeRotation[i] {
			t.Fatalf("M5 kept slots changed order: %v -> %v", m5BeforeRotation, m5Next)
		}
	}
	for _, id := range m5Next[4:] {
		if recommendContains(m5BeforeRotation, id) {
			t.Fatalf("M5 rotation kept an old face in the replaced slots: %d (%v -> %v)",
				id, m5BeforeRotation, m5Next)
		}
	}

	m0Next, err := database.WeeklyRecommendationTargets(ctx, male(0), week2)
	if err != nil {
		t.Fatalf("list M0 week2: %v", err)
	}
	if len(m0Next) < 4 || m0Next[0] != female(1) || m0Next[1] != female(0) {
		t.Fatalf("M0 week2 pinned head = %v, want [F1 F0]", m0Next)
	}
	m0AlgoPrev := m0BeforeRotation[2:]
	keptM0 := 0
	for _, id := range m0AlgoPrev {
		if recommendContains(m0Next[2:], id) {
			keptM0++
		}
	}
	if keptM0 != 2 {
		t.Fatalf("M0 kept %d algo slots, want 2 (必显占 2 个保留名额): %v -> %v",
			keptM0, m0BeforeRotation, m0Next)
	}

	// 同周刷新：某人脱单后必须从推荐位剔除并补空位，但不整批换、不动必显头。
	drop := m0BeforeRotation[len(m0BeforeRotation)-1]
	if _, err := pool.Exec(ctx, `UPDATE users SET account_status = 'matched' WHERE id = $1`, drop); err != nil {
		t.Fatalf("mark user matched: %v", err)
	}
	third, err := database.GenerateWeeklyRecommendations(ctx, now1)
	if err != nil {
		t.Fatalf("refresh week1: %v", err)
	}
	if !third.Changed {
		t.Fatal("same-week refresh did not rewrite after a candidate left")
	}
	refreshed := list(male(0))
	if recommendContains(refreshed, drop) {
		t.Fatalf("matched user %d still recommended: %v", drop, refreshed)
	}
	if len(refreshed) != 7 {
		t.Fatalf("refreshed M0 list = %v, want 7 entries", refreshed)
	}
	if refreshed[0] != female(1) || refreshed[1] != female(0) {
		t.Fatalf("same-week refresh moved pinned head: %v", refreshed[:2])
	}

	// 管理端汇总只有人数/条目/时间，不带必显原因。
	summary, err := database.WeeklyRecommendationSummary(ctx, week2, 20)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Viewers < len(males)+len(females) {
		t.Fatalf("summary viewers = %d, want >= %d", summary.Viewers, len(males)+len(females))
	}
	if summary.Entries <= 0 || summary.Generated.IsZero() {
		t.Fatalf("summary entries=%d generated=%v", summary.Entries, summary.Generated)
	}
	// 样本来自全表，可能包含其它用例残留的账号（昵称可为空），只校验
	// 管理端真正依赖的字段；本用例夹具的昵称另行确认。
	for _, row := range summary.Sample {
		if row.UserID == 0 || row.Count <= 0 || row.UpdatedAt.IsZero() {
			t.Fatalf("summary row = %+v", row)
		}
		if name, ok := fixtureNicknames[row.UserID]; ok && row.Nickname != name {
			t.Fatalf("fixture summary row = %+v, want nickname %q", row, name)
		}
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
