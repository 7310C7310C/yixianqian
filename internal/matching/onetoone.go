// Package matching implements the legacy one-to-one matching algorithms.
//
// The online path produces each participant's independent top-N list. The
// production path is the offline "women host, men visit" balanced assignment:
// every woman gets exactly K chats, every man gets a base or base+1 quota, and
// a min-cost flow maximises the summed symmetric score.
//
// The arithmetic, graph construction, SPFA relaxation order and tie-breaking
// mirror lib/onetoone.py from the v1 system so the golden parity fixtures keep
// producing identical allocations.
package matching

import (
	"fmt"
	"math"
	"sort"
)

// Weights and scores are product rules, not configuration.
const (
	WeightOnlineMyPriority    = 0.5
	WeightOnlineTheirPriority = 0.3
	WeightOnlineSimilarity    = 0.2
	WeightOfflinePriority     = 0.4
	WeightOfflineSimilarity   = 0.2

	UnselectedScore = 30
)

// OtoPriorityScores maps a volunteer rank (1-based) to its score. Ranks outside
// the table receive UnselectedScore, exactly like the legacy implementation.
var OtoPriorityScores = map[int]int{
	1: 100, 2: 90, 3: 80, 4: 70, 5: 65,
	6: 60, 7: 55, 8: 50, 9: 45, 10: 40,
}

// OtoParticipant is one eligible signup. Key is the stable identifier used for
// output and deterministic tie-breaking (the legacy open id when available).
type OtoParticipant struct {
	Key    string
	Gender string // "male" or "female"
}

// Choice is one volunteer entry; Priority starts at 1.
type Choice struct {
	Target   string
	Priority int
}

// Profile is the subset of user fields used by the similarity score. The key
// names mirror lib/recommend.py.
type Profile struct {
	Hobbies   []string
	Sports    []string
	Traits    []string
	Education string
	MBTI      []string
	City      string
	Church    string
}

// RankedTarget is one online top-N entry.
type RankedTarget struct {
	Target string
	Rank   int
}

// OfflineMatch is one male-list entry, sorted by score descending then key.
type OfflineMatch struct {
	Target string
	Rank   int
	Score  float64
}

// OfflinePair is one female-list entry.
type OfflinePair struct {
	Target string
	Score  float64
}

// OfflineResult is the balanced allocation. Both list directions describe the
// same edges; MaleQuota records each man's total number of chats.
type OfflineResult struct {
	MaleLists   map[string][]OfflineMatch
	FemaleLists map[string][]OfflinePair
	MaleQuota   map[string]int
}

// Similarity weights, mirroring lib/recommend.py.
const (
	wHobbies   = 25
	wSports    = 15
	wTraits    = 15
	wEducation = 15
	wMBTI      = 15
	wCity      = 10
	wChurch    = 5

	neutralRatio = 0.5
)

var educationOrder = map[string]int{
	"高中及以下": 1,
	"大专":    2,
	"本科":    3,
	"硕士":    4,
	"博士":    5,
}

var educationByDiff = map[int]int{0: 15, 1: 11, 2: 6}

// MatchScore returns the 0..100 similarity score. An unfilled field earns the
// neutral half-score instead of a penalty or a bonus.
func MatchScore(a, b Profile) int {
	score := 0
	score += scaledJaccard(a.Hobbies, b.Hobbies, wHobbies)
	score += scaledJaccard(a.Sports, b.Sports, wSports)
	score += scaledJaccard(a.Traits, b.Traits, wTraits)

	eduA := educationOrder[a.Education]
	eduB := educationOrder[b.Education]
	if eduA == 0 || eduB == 0 {
		score += scale(neutralRatio, wEducation)
	} else {
		if v, ok := educationByDiff[absInt(eduA-eduB)]; ok {
			score += v
		} else {
			score += 2
		}
	}

	mbtiA, mbtiB := stringSet(a.MBTI), stringSet(b.MBTI)
	if len(mbtiA) == 0 || len(mbtiB) == 0 {
		score += scale(neutralRatio, wMBTI)
	} else {
		common := 0
		for v := range mbtiA {
			if _, ok := mbtiB[v]; ok {
				common++
			}
		}
		if common > 4 {
			common = 4
		}
		score += scale(float64(common)/4, wMBTI)
	}

	for _, item := range []struct {
		va, vb string
		weight int
	}{
		{a.City, b.City, wCity},
		{a.Church, b.Church, wChurch},
	} {
		if item.va == "" || item.vb == "" {
			score += scale(neutralRatio, item.weight)
		} else if item.va == item.vb {
			score += item.weight
		}
	}
	if score > 100 {
		score = 100
	}
	return score
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func scale(ratio float64, weight int) int {
	// Python's round() rounds halves to even; math.RoundToEven matches it.
	return int(math.RoundToEven(ratio * float64(weight)))
}

func scaledJaccard(a, b []string, weight int) int {
	as, bs := stringSet(a), stringSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return scale(neutralRatio, weight)
	}
	common := 0
	for v := range as {
		if _, ok := bs[v]; ok {
			common++
		}
	}
	union := len(as) + len(bs) - common
	if union == 0 {
		return scale(neutralRatio, weight)
	}
	return scale(float64(common)/float64(union), weight)
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

// RunOnline builds each participant's independent top-N opposite-gender list.
// Ties keep participant order (stable sort), matching the legacy code.
func RunOnline(participants []OtoParticipant, selections map[string][]Choice,
	profiles map[string]Profile, topN int) map[string][]RankedTarget {
	males := participantKeys(participants, "male")
	females := participantKeys(participants, "female")

	chosenBy := map[string]map[string]int{}
	for selector, choices := range selections {
		for _, c := range choices {
			if chosenBy[c.Target] == nil {
				chosenBy[c.Target] = map[string]int{}
			}
			chosenBy[c.Target][selector] = c.Priority
		}
	}

	result := make(map[string][]RankedTarget, len(participants))
	for _, p := range participants {
		pid := p.Key
		var opposite []string
		switch p.Gender {
		case "male":
			opposite = females
		case "female":
			opposite = males
		default:
			result[pid] = []RankedTarget{}
			continue
		}

		myChoice := map[string]int{}
		for _, c := range selections[pid] {
			if old, ok := myChoice[c.Target]; !ok || c.Priority < old {
				myChoice[c.Target] = c.Priority
			}
		}
		profA := profiles[pid]
		type scored struct {
			score  float64
			target string
		}
		items := make([]scored, 0, len(opposite))
		for _, qid := range opposite {
			if qid == pid {
				continue
			}
			myScore := UnselectedScore
			if p, ok := myChoice[qid]; ok {
				if v, ok := OtoPriorityScores[p]; ok {
					myScore = v
				}
			}
			theirScore := UnselectedScore
			if p, ok := chosenBy[pid][qid]; ok {
				if v, ok := OtoPriorityScores[p]; ok {
					theirScore = v
				}
			}
			match := MatchScore(profA, profiles[qid])
			total := WeightOnlineMyPriority*float64(myScore) +
				WeightOnlineTheirPriority*float64(theirScore) +
				WeightOnlineSimilarity*float64(match)
			items = append(items, scored{score: total, target: qid})
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
		limit := topN
		if limit > len(items) {
			limit = len(items)
		}
		if limit < 0 {
			limit = 0
		}
		list := make([]RankedTarget, 0, limit)
		for i := 0; i < limit; i++ {
			list = append(list, RankedTarget{Target: items[i].target, Rank: i + 1})
		}
		result[pid] = list
	}
	return result
}

func participantKeys(participants []OtoParticipant, gender string) []string {
	out := make([]string, 0, len(participants))
	for _, p := range participants {
		if p.Gender == gender {
			out = append(out, p.Key)
		}
	}
	return out
}

// RunOffline solves the balanced transport problem. The returned error text
// mirrors the legacy ValueErrors so admin previews can surface it verbatim.
func RunOffline(participants []OtoParticipant, selections map[string][]Choice,
	profiles map[string]Profile, femaleChats int) (OfflineResult, error) {
	males := participantKeys(participants, "male")
	females := participantKeys(participants, "female")
	m, f, k := len(males), len(females), femaleChats
	if m == 0 || f == 0 {
		return OfflineResult{
			MaleLists:   map[string][]OfflineMatch{},
			FemaleLists: map[string][]OfflinePair{},
			MaleQuota:   map[string]int{},
		}, nil
	}
	if k < 1 {
		return OfflineResult{}, fmt.Errorf("女生场次 female_chats 必须 ≥ 1，当前 %d", k)
	}
	if k > m {
		return OfflineResult{}, fmt.Errorf(
			"女生场次 female_chats=%d 不能超过男生人数 %d（每个女生最多和 %d 位男生各聊一次）",
			k, m, m)
	}

	myChoice := map[string]map[string]int{}
	for selector, choices := range selections {
		mc := map[string]int{}
		for _, c := range choices {
			if old, ok := mc[c.Target]; !ok || c.Priority < old {
				mc[c.Target] = c.Priority
			}
		}
		myChoice[selector] = mc
	}
	prio := func(a, b string) int {
		if p, ok := myChoice[a][b]; ok {
			if v, ok := OtoPriorityScores[p]; ok {
				return v
			}
		}
		return UnselectedScore
	}

	scores := make([][]float64, m)
	for i, male := range males {
		scores[i] = make([]float64, f)
		pm := profiles[male]
		for j, female := range females {
			match := MatchScore(pm, profiles[female])
			scores[i][j] = WeightOfflinePriority*float64(prio(male, female)+prio(female, male)) +
				WeightOfflineSimilarity*float64(match)
		}
	}

	edges := f * k
	base, rem := edges/m, edges%m
	quota := make([]int, m)
	for i := range quota {
		quota[i] = base
		if i < rem {
			quota[i]++
		}
	}

	source := 0
	maleBase := 1
	femaleBase := 1 + m
	sink := 1 + m + f
	flow := newMinCostFlow(sink + 1)
	for i := 0; i < m; i++ {
		flow.addEdge(source, maleBase+i, quota[i], 0)
	}
	used := make([][]edgeRef, m)
	for i := 0; i < m; i++ {
		used[i] = make([]edgeRef, f)
		for j := 0; j < f; j++ {
			cost := -int(math.RoundToEven(scores[i][j] * 1000))
			flow.addEdge(maleBase+i, femaleBase+j, 1, cost)
			used[i][j] = edgeRef{u: maleBase + i, idx: len(flow.g[maleBase+i]) - 1}
		}
	}
	for j := 0; j < f; j++ {
		flow.addEdge(femaleBase+j, sink, k, 0)
	}

	got, _ := flow.run(source, sink, edges)
	if got != edges {
		return OfflineResult{}, fmt.Errorf("线下均衡分配未收敛：实际配对 %d/%d 条", got, edges)
	}

	maleLists := make(map[string][]OfflineMatch, m)
	femaleLists := make(map[string][]OfflinePair, f)
	maleQuota := make(map[string]int, m)
	for i, male := range males {
		type entry struct {
			target string
			score  float64
		}
		list := make([]entry, 0, f)
		for j, female := range females {
			if flow.g[used[i][j].u][used[i][j].idx].cap == 0 {
				list = append(list, entry{target: female, score: scores[i][j]})
			}
		}
		sort.Slice(list, func(a, b int) bool {
			if list[a].score != list[b].score {
				return list[a].score > list[b].score
			}
			return list[a].target < list[b].target
		})
		ranked := make([]OfflineMatch, 0, len(list))
		for rank, item := range list {
			ranked = append(ranked, OfflineMatch{Target: item.target, Rank: rank + 1, Score: item.score})
		}
		maleLists[male] = ranked
		maleQuota[male] = quota[i]
	}
	for j, female := range females {
		type entry struct {
			target string
			score  float64
		}
		list := make([]entry, 0, m)
		for i, male := range males {
			if flow.g[used[i][j].u][used[i][j].idx].cap == 0 {
				list = append(list, entry{target: male, score: scores[i][j]})
			}
		}
		sort.Slice(list, func(a, b int) bool {
			if list[a].score != list[b].score {
				return list[a].score > list[b].score
			}
			return list[a].target < list[b].target
		})
		pairs := make([]OfflinePair, 0, len(list))
		for _, item := range list {
			pairs = append(pairs, OfflinePair{Target: item.target, Score: item.score})
		}
		femaleLists[female] = pairs
	}
	return OfflineResult{MaleLists: maleLists, FemaleLists: femaleLists, MaleQuota: maleQuota}, nil
}

type edge struct {
	to   int
	rev  int
	cap  int
	cost int
}

type edgeRef struct {
	u   int
	idx int
}

// minCostFlow is the legacy SPFA successive-shortest-path implementation. Edge
// insertion order and queue order are part of the deterministic result.
type minCostFlow struct {
	g [][]edge
}

func newMinCostFlow(n int) *minCostFlow {
	return &minCostFlow{g: make([][]edge, n)}
}

func (m *minCostFlow) addEdge(u, v, capacity, cost int) {
	m.g[u] = append(m.g[u], edge{to: v, rev: len(m.g[v]), cap: capacity, cost: cost})
	m.g[v] = append(m.g[v], edge{to: u, rev: len(m.g[u]) - 1, cap: 0, cost: -cost})
}

func (m *minCostFlow) run(source, sink, maxFlow int) (int, int) {
	const inf = int(^uint(0) >> 2)
	flow, totalCost := 0, 0
	for flow < maxFlow {
		dist := make([]int, len(m.g))
		inQueue := make([]bool, len(m.g))
		prevV := make([]int, len(m.g))
		prevE := make([]int, len(m.g))
		for i := range dist {
			dist[i] = inf
			prevV[i] = -1
			prevE[i] = -1
		}
		dist[source] = 0
		queue := []int{source}
		inQueue[source] = true
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			inQueue[u] = false
			for i := range m.g[u] {
				e := m.g[u][i]
				if e.cap > 0 && dist[e.to] > dist[u]+e.cost {
					dist[e.to] = dist[u] + e.cost
					prevV[e.to] = u
					prevE[e.to] = i
					if !inQueue[e.to] {
						queue = append(queue, e.to)
						inQueue[e.to] = true
					}
				}
			}
		}
		if prevV[sink] == -1 {
			break
		}
		push := maxFlow - flow
		for v := sink; v != source; v = prevV[v] {
			if e := m.g[prevV[v]][prevE[v]]; e.cap < push {
				push = e.cap
			}
		}
		flow += push
		totalCost += push * dist[sink]
		for v := sink; v != source; v = prevV[v] {
			u, idx := prevV[v], prevE[v]
			rev := m.g[u][idx].rev
			m.g[u][idx].cap -= push
			m.g[v][rev].cap += push
		}
	}
	return flow, totalCost
}
