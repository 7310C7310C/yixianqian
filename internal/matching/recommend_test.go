package matching

import (
	"reflect"
	"testing"
	"time"
)

func TestRecommendBuildSlots(t *testing.T) {
	cases := []struct {
		pinned           int
		total, pin, algo int
	}{
		{0, 7, 0, 7},
		{1, 7, 1, 6},
		{3, 7, 3, 4},
		{4, 7, 4, 3},
		{7, 7, 4, 3},   // 必显把 7 个名额占满时，算法位仍保底 3 个
		{12, 12, 9, 3}, // 必显更多时推荐位跟着变长
		{-3, 7, 0, 7},  // 防御：负数按 0 处理
	}
	for _, tc := range cases {
		total, pinned, algo := BuildSlots(tc.pinned)
		if total != tc.total || pinned != tc.pin || algo != tc.algo {
			t.Fatalf("BuildSlots(%d) = (%d,%d,%d), want (%d,%d,%d)",
				tc.pinned, total, pinned, algo, tc.total, tc.pin, tc.algo)
		}
	}
}

func TestRecommendNextAlgoRotatesKeepFour(t *testing.T) {
	prev := []int64{101, 102, 103, 104}
	// 候选按分数降序：新人分更高，所以排在最前。
	candidates := []int64{201, 202, 203, 101, 102, 103, 104}
	got := NextAlgo(prev, candidates, nil, true)
	want := []int64{101, 102, 103, 104, 201, 202, 203}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rotation = %v, want %v", got, want)
	}
}

func TestRecommendNextAlgoPinnedFourKeepsNoAlgo(t *testing.T) {
	pinned := []int64{1, 2, 3, 4}
	prev := []int64{101, 102, 103, 104}
	candidates := []int64{101, 102, 103, 104, 201}
	got := NextAlgo(prev, candidates, pinned, true)
	// 必显 4 个先把保留名额占满，算法位整批换新；生面孔只有 1 个时
	// 才回头用旧人补位，宁可旧面孔重复也不让推荐位空着。
	want := []int64{201, 101, 102}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned=4 algo = %v, want %v", got, want)
	}
	for _, id := range got {
		for _, p := range pinned {
			if id == p {
				t.Fatalf("algo contains pinned id %d: %v", p, got)
			}
		}
	}
}

func TestRecommendNextAlgoPinnedOneKeepsThree(t *testing.T) {
	pinned := []int64{9}
	prev := []int64{101, 102, 103, 104}
	candidates := []int64{101, 102, 103, 104, 201, 202, 203}
	got := NextAlgo(prev, candidates, pinned, true)
	// 必显 1 个 → 保留 3 个算法位（101..103）；轮换周先捞生面孔，
	// 生面孔不够时（这里 104 是旧人）才回头用旧人补位。
	want := []int64{101, 102, 103, 201, 202, 203}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned=1 algo = %v, want %v", got, want)
	}
}

func TestRecommendNextAlgoSameWeekKeepsWholeBatch(t *testing.T) {
	prev := []int64{101, 102, 103, 104, 201, 202, 203}
	candidates := []int64{101, 102, 103, 104, 201, 202, 203, 204}
	got := NextAlgo(prev, candidates, nil, false)
	want := []int64{101, 102, 103, 104, 201, 202, 203}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("same week algo = %v, want %v", got, want)
	}
	// 幂等：同一输入再跑一次结果不变。
	if again := NextAlgo(prev, candidates, nil, false); !reflect.DeepEqual(again, got) {
		t.Fatalf("NextAlgo not idempotent: %v vs %v", again, got)
	}
}

func TestRecommendNextAlgoDropsInvalidAndFillsGap(t *testing.T) {
	prev := []int64{101, 102, 103, 104}
	// 103 已脱单/已互相喜欢，不再出现在候选里；999 是新候选。
	candidates := []int64{101, 102, 104, 999}
	got := NextAlgo(prev, candidates, nil, false)
	want := []int64{101, 102, 104, 999}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("same-week refresh = %v, want %v", got, want)
	}
}

func TestRecommendNextAlgoDeduplicatesPinnedFromCandidates(t *testing.T) {
	pinned := []int64{7}
	candidates := []int64{7, 8, 9, 10, 11, 12, 13}
	got := NextAlgo(nil, candidates, pinned, true)
	if len(got) != 6 {
		t.Fatalf("algo length = %d, want 6 (%v)", len(got), got)
	}
	for _, id := range got {
		if id == 7 {
			t.Fatalf("pinned id leaked into algo: %v", got)
		}
	}
	seen := map[int64]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate id %d in %v", id, got)
		}
		seen[id] = true
	}
}

func TestRecommendNextAlgoEmptyPools(t *testing.T) {
	if got := NextAlgo(nil, nil, nil, true); len(got) != 0 {
		t.Fatalf("empty pool = %v, want empty", got)
	}
	if got := NextAlgo([]int64{1, 2, 3, 4}, nil, nil, false); len(got) != 0 {
		t.Fatalf("no candidates = %v, want empty", got)
	}
	// 候选不足时有多少给多少，不补未知的人。
	got := NextAlgo(nil, []int64{1, 2}, nil, true)
	if !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("short pool = %v, want [1 2]", got)
	}
}

func TestRecommendBuildListPinnedFirst(t *testing.T) {
	got := BuildList([]int64{3, 1}, []int64{2, 4, 1})
	want := []int64{3, 1, 2, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildList = %v, want %v", got, want)
	}
	if got := BuildList(nil, nil); len(got) != 0 {
		t.Fatalf("empty BuildList = %v", got)
	}
	// 必显 8 个时推荐位扩到 8、算法位保底 3 → 只有最近 5 个必显上榜。
	got = BuildList([]int64{1, 2, 3, 4, 5, 6, 7, 8}, nil)
	if !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("pinned truncation = %v, want [1 2 3 4 5]", got)
	}
}

func TestRecommendWeekKeyISOAcrossYear(t *testing.T) {
	cases := []struct {
		day  string
		want string
	}{
		{"2025-12-28", "2025-W52"}, // 周日
		{"2025-12-29", "2026-W01"}, // 周一，ISO 周跨年
		{"2026-01-01", "2026-W01"},
		{"2026-12-31", "2026-W53"},
		{"2027-01-01", "2026-W53"},
		{"2026-10-24", "2026-W43"},
	}
	for _, tc := range cases {
		tm, err := time.Parse("2006-01-02", tc.day)
		if err != nil {
			t.Fatal(err)
		}
		if got := WeekKey(tm); got != tc.want {
			t.Fatalf("WeekKey(%s) = %s, want %s", tc.day, got, tc.want)
		}
	}
	// 跨年那周前后必须连续，不能跳回 W01 触发假轮换。
	before, _ := time.Parse("2006-01-02", "2026-01-04")
	after, _ := time.Parse("2006-01-02", "2026-01-05")
	if WeekKey(before) == WeekKey(after) {
		t.Fatalf("week key did not change on Monday: %s", WeekKey(before))
	}
}

func TestRecommendMatchScoreFullProfile(t *testing.T) {
	p := Profile{
		Hobbies:   []string{"看书", "爬山"},
		Sports:    []string{"羽毛球"},
		Traits:    []string{"喜欢安静"},
		Education: "本科",
		MBTI:      []string{"I", "N", "F", "P"},
		City:      "上海",
		Church:    "某堂",
	}
	if got := MatchScore(p, p); got != 100 {
		t.Fatalf("identical score = %d, want 100", got)
	}
}

func TestRecommendMatchScoreNeutralWhenEmpty(t *testing.T) {
	// 两侧都空：25*0.5=12.5→12（.5 取偶）、15*0.5=7.5→8、10*0.5=5、5*0.5=2.5→2。
	if got := MatchScore(Profile{}, Profile{}); got != 51 {
		t.Fatalf("empty score = %d, want 51", got)
	}
	// 一侧有、一侧没有也走中性分，不奖励空白也不惩罚。
	a := Profile{Hobbies: []string{"看书"}}
	b := Profile{Sports: []string{"跑步"}}
	got := MatchScore(a, b)
	want := MatchScore(Profile{}, Profile{})
	if got != want {
		t.Fatalf("one-side-empty score = %d, want neutral %d", got, want)
	}
}

func TestRecommendMatchScoreEducationAndTags(t *testing.T) {
	base := Profile{Education: "本科"}
	same := Profile{Education: "本科"}
	oneUp := Profile{Education: "硕士"}
	twoUp := Profile{Education: "博士"}
	if d := MatchScore(base, same) - MatchScore(base, oneUp); d != 4 {
		t.Fatalf("edu diff 1 penalty = %d, want 4", d)
	}
	if d := MatchScore(base, same) - MatchScore(base, twoUp); d != 9 {
		t.Fatalf("edu diff 2 penalty = %d, want 9", d)
	}
	// v1 的「高中及以下」与 v2 的「大专以下」是同一档。
	if MatchScore(Profile{Education: "高中及以下"}, Profile{Education: "大专以下"}) !=
		MatchScore(Profile{Education: "本科"}, Profile{Education: "本科"}) {
		t.Fatal("legacy and v2 education grade names must map to the same rank")
	}
	// Jaccard：1/3 * 25 = 8.33 → 8；其余项中性（51）里爱好那 12 分换成 8。
	if got := MatchScore(Profile{Hobbies: []string{"a", "b"}}, Profile{Hobbies: []string{"a", "c"}}); got != 47 {
		t.Fatalf("jaccard score = %d, want 47", got)
	}
	// MBTI 4 个字母全同：15 分（中性是 8）→ 51 - 8 + 15 = 58。
	if got := MatchScore(Profile{MBTI: []string{"I", "N", "F", "P"}},
		Profile{MBTI: []string{"I", "N", "F", "P"}}); got != 58 {
		t.Fatalf("mbti score = %d, want 58", got)
	}
	// 同城 + 同教堂拿满 15 分；中性时这两项本来就有 7 分 → 51 - 7 + 15 = 59。
	if got := MatchScore(Profile{City: "上海", Church: "某堂"}, Profile{City: "上海", Church: "某堂"}); got != 59 {
		t.Fatalf("city+church score = %d, want 59", got)
	}
}

func TestRecommendSortCandidatesDesc(t *testing.T) {
	scores := map[int64]int{5: 70, 3: 90, 1: 90, 9: 50}
	ids := []int64{5, 9, 3, 1}
	SortCandidatesDesc(scores, ids)
	if want := []int64{1, 3, 5, 9}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("sorted ids = %v, want %v", ids, want)
	}
}
