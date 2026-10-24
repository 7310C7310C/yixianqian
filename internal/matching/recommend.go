package matching

import (
	"fmt"
	"sort"
	"time"
)

// 牵线页「推荐位」的选人规则（纯逻辑）。
//
// 移植自 v1 lib/recommend.py，行为逐条对齐：
//
//  1. 暗恋我的人必须出现在推荐位里（「必显」）——这是这个功能存在的理由。
//  2. 不能让人从推荐位反推出「这些人喜欢我」——推荐位随必显人数扩展，
//     但永远留至少 RecommendMinAlgo 个算法位。必显 12 个人时推荐位有 12 个，
//     其中 9 个必显、3 个算法，外人分不出哪 3 个是陪跑的。
//
// 每周轮换：算法位每周换掉一批，但保留 RecommendKeep 个不动；保留数先让必显占，
// 必显已有 4 个以上时算法位一个都不留。必显位本身不参与轮换——它是事实
// （谁喜欢我），不是选出来的。

const (
	// RecommendMinTotal 是推荐位总数下限。必显更多时推荐位跟着变长。
	RecommendMinTotal = 7
	// RecommendMinAlgo 是算法位下限。永远留这么多，否则推荐位可能全是必显、
	// 匿名性被反推。
	RecommendMinAlgo = 3
	// RecommendKeep 是每周保留不动的名额（先被必显占）。
	RecommendKeep = 4
)

// BuildSlots 算出 (推荐位总数, 必显位, 算法位)。
//
// 必显比 7 个多时推荐位跟着变长，但算法位只有被压到下限这一种可能，
// 不会消失——下限就是匿名性的保护垫。
func BuildSlots(pinnedCount int) (total, pinnedSlots, algoSlots int) {
	if pinnedCount < 0 {
		pinnedCount = 0
	}
	total = RecommendMinTotal
	if pinnedCount > total {
		total = pinnedCount
	}
	algoSlots = total - pinnedCount
	if algoSlots < RecommendMinAlgo {
		algoSlots = RecommendMinAlgo
	}
	if algoSlots > total {
		algoSlots = total // 站很小、候选池空时算法位不该超过总数
	}
	return total, total - algoSlots, algoSlots
}

// NextAlgo 返回本轮的算法位名单。
//
//   - prevAlgo  上轮算法位（按分数降序）
//   - candidates 本轮算法候选（按分数降序，已排除必显对象）
//   - pinned     本轮必显（按最近优先）
//   - newWeek    是否到了轮换的周一
//
// 非轮换周只做「剔除失效 + 补齐空位」：某人临时脱单或已互相喜欢时，留着他是错的；
// 但不因为这种个别变动就把整批换掉——推荐位天天变的话，用户根本看不出
// 这是个「每周一批」的东西，也就不会觉得被认真推荐过。
func NextAlgo(prevAlgo, candidates, pinned []int64, newWeek bool) []int64 {
	_, pinnedSlots, algoSlots := BuildSlots(len(pinned))
	pinnedTake := pinned
	if len(pinnedTake) > pinnedSlots {
		pinnedTake = pinnedTake[:pinnedSlots]
	}
	forSlots := RecommendKeep - len(pinnedTake)
	if forSlots < 0 {
		forSlots = 0 // 本周保留几个算法位
	}

	candSet := make(map[int64]struct{}, len(candidates))
	for _, id := range candidates {
		candSet[id] = struct{}{}
	}
	pinnedSet := make(map[int64]struct{}, len(pinnedTake))
	for _, id := range pinnedTake {
		pinnedSet[id] = struct{}{}
	}

	validPrev := make([]int64, 0, len(prevAlgo))
	prevSet := make(map[int64]struct{}, len(prevAlgo))
	for _, id := range prevAlgo {
		prevSet[id] = struct{}{}
		if _, ok := candSet[id]; !ok {
			continue
		}
		if _, ok := pinnedSet[id]; ok {
			continue
		}
		validPrev = append(validPrev, id)
	}

	keepLimit := algoSlots
	if newWeek {
		keepLimit = forSlots
	}
	if keepLimit > len(validPrev) {
		keepLimit = len(validPrev)
	}
	picked := append([]int64(nil), validPrev[:keepLimit]...)
	// seen 必须连必显一起算：候选池里当然有必显那些人，漏掉就会把他们
	// 又塞进算法位，推荐位里同一个人出现两次。
	seen := make(map[int64]struct{}, len(picked)+len(pinnedTake))
	for _, id := range picked {
		seen[id] = struct{}{}
	}
	for _, id := range pinnedTake {
		seen[id] = struct{}{}
	}

	// 轮换周先捞圈子外的生面孔。候选池是按分数降序的全量合格异性，
	// 里面当然有上轮那批人——直接顺着填，分数高的旧人立刻把自己填回来，
	// 「换掉 3 个」就成了空话。生面孔不够时（小站）才回头用旧人补位，
	// 宁可旧面孔重复也不让推荐位空着。
	fill := make([]int64, 0, len(candidates))
	if newWeek {
		for _, id := range candidates {
			if _, ok := prevSet[id]; !ok {
				fill = append(fill, id)
			}
		}
	}
	for _, id := range append(fill, candidates...) {
		if len(picked) >= algoSlots {
			break
		}
		if _, ok := seen[id]; ok {
			continue
		}
		picked = append(picked, id)
		seen[id] = struct{}{}
	}
	if len(picked) > algoSlots {
		picked = picked[:algoSlots]
	}
	return picked
}

// BuildList 返回推荐位的最终顺序：必显在前，算法在后。
//
// 顺序本身就是产品语义（暗恋的人排在最前），且固定不变——不打乱，
// 否则「每周保留 4 个」在用户看来仍然是位置全变。
//
// 这里和 NextAlgo 一样按 pinnedSlots 截断：暗恋你的人多于名额时，
// 只有最近的那几个上推荐位（喜欢关系本身不受影响，配对照常）。
func BuildList(pinned, algo []int64) []int64 {
	_, pinnedSlots, _ := BuildSlots(len(pinned))
	if len(pinned) > pinnedSlots {
		pinned = pinned[:pinnedSlots]
	}
	out := make([]int64, 0, len(pinned)+len(algo))
	seen := make(map[int64]struct{}, len(pinned)+len(algo))
	for _, id := range pinned {
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, id)
		seen[id] = struct{}{}
	}
	for _, id := range algo {
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, id)
		seen[id] = struct{}{}
	}
	return out
}

// WeekKey 返回本周标识，形如 "2026-W38"（ISO 周，周一是第一天）。
//
// 用 ISO 周而不是「距某天的天数 / 7」：跨年那周 ISO 编号是连续的，
// 自己算容易在 12 月底跳回 W01 时触发一次假轮换。调用方应传入社会时区
// （Asia/Shanghai）的时间；与 db.WeekKey 的格式一致。
func WeekKey(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// SortCandidatesDesc 按分数降序、ID 升序排序（同分按 ID 定序，保证同一批人
// 不会因为 map 遍历顺序而无缘无故换位置）。分数直接用 MatchScore。
func SortCandidatesDesc(scores map[int64]int, ids []int64) {
	sort.Slice(ids, func(i, j int) bool {
		si, sj := scores[ids[i]], scores[ids[j]]
		if si != sj {
			return si > sj
		}
		return ids[i] < ids[j]
	})
}
