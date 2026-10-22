// Package matching implements the legacy grouping algorithm.
//
// The implementation mirrors lib/grouping.py (satellite rolling) exactly:
// mutual-affinity scoring with fixed weights, stable score ordering, a greedy
// core pair per group, affinity-greedy expansion to the configured size, and
// even distribution of the remaining participants. Ordering only depends on
// the input slices, so the same input always produces the same groups.
package matching

import (
	"errors"
	"sort"
)

// Weights and score tables are copied from the legacy implementation. Changing
// them changes historical grouping results, so they are constants, not config.
var (
	PriorityScores = map[int]float64{
		1: 100, 2: 90, 3: 80, 4: 70, 5: 65,
		6: 60, 7: 55, 8: 50, 9: 45, 10: 40,
	}
	MatchmakerStarScores = map[int]float64{
		5: 100, 4: 90, 3: 80, 2: 75, 1: 70,
	}
	WeightUserSelection  = 0.7
	WeightMatchmakerPick = 0.3
)

// ErrInvalidGroupSize reports a non-positive per-group size.
var ErrInvalidGroupSize = errors.New("matching: per-group sizes must be greater than zero")

// Participant is one grouping candidate. Gender is "male" or "female"; other
// values are ignored, matching the legacy filter.
type Participant struct {
	ID     int64  `json:"id"`
	Gender string `json:"gender"`
}

// Selection is one ordered choice. Priority is 1 (best) through 7.
type Selection struct {
	ID       int64 `json:"id"`
	Priority int   `json:"priority"`
}

// MatchmakerPick is an optional matchmaker recommendation. It is kept for
// parity even though the migrated UI does not produce them yet.
type MatchmakerPick struct {
	Person1ID int64 `json:"person1_id"`
	Person2ID int64 `json:"person2_id"`
	Stars     int   `json:"stars"`
}

// Group is one generated group. Members are ordered by input order, males and
// females tracked separately, like the legacy algorithm.
type Group struct {
	GroupID   int     `json:"group_id"`
	MaleIDs   []int64 `json:"male_ids"`
	FemaleIDs []int64 `json:"female_ids"`
}

// Members returns males followed by females, matching the legacy write order.
func (g Group) Members() []int64 {
	out := make([]int64, 0, len(g.MaleIDs)+len(g.FemaleIDs))
	out = append(out, g.MaleIDs...)
	out = append(out, g.FemaleIDs...)
	return out
}

func userSelectionScore(priority int) float64 {
	return PriorityScores[priority]
}

// MutualAffinityScore sums both directions plus matchmaker picks, then applies
// the legacy weights.
func MutualAffinityScore(p1ID, p2ID int64, selections map[int64][]Selection, picks []MatchmakerPick) float64 {
	us1 := directionScore(p1ID, p2ID, selections)
	us2 := directionScore(p2ID, p1ID, selections)

	var ms1, ms2 float64
	for _, pick := range picks {
		if pick.Person1ID == p1ID && pick.Person2ID == p2ID {
			if score := MatchmakerStarScores[pick.Stars]; score > ms1 {
				ms1 = score
			}
		}
		if pick.Person1ID == p2ID && pick.Person2ID == p1ID {
			if score := MatchmakerStarScores[pick.Stars]; score > ms2 {
				ms2 = score
			}
		}
	}

	final1 := us1*WeightUserSelection + ms1*WeightMatchmakerPick
	final2 := us2*WeightUserSelection + ms2*WeightMatchmakerPick
	return final1 + final2
}

func directionScore(from, to int64, selections map[int64][]Selection) float64 {
	for _, choice := range selections[from] {
		if choice.ID == to {
			return userSelectionScore(choice.Priority)
		}
	}
	return 0
}

func groupAffinityScore(candidateID int64, memberIDs []int64,
	selections map[int64][]Selection, picks []MatchmakerPick) float64 {
	var total float64
	for _, memberID := range memberIDs {
		total += MutualAffinityScore(candidateID, memberID, selections, picks)
	}
	return total
}

type pairScore struct {
	maleID   int64
	femaleID int64
	score    float64
}

// pairScores creates the male×female score table in input order and sorts it
// stably by descending score. Stable ordering is the legacy tie-break.
func pairScores(males, females []int64, selections map[int64][]Selection,
	picks []MatchmakerPick) []pairScore {
	table := make([]pairScore, 0, len(males)*len(females))
	for _, maleID := range males {
		for _, femaleID := range females {
			table = append(table, pairScore{
				maleID:   maleID,
				femaleID: femaleID,
				score:    MutualAffinityScore(maleID, femaleID, selections, picks),
			})
		}
	}
	sort.SliceStable(table, func(i, j int) bool {
		return table[i].score > table[j].score
	})
	return table
}

func findCorePair(table []pairScore, assigned map[int64]bool) (pairScore, bool) {
	for _, pair := range table {
		if !assigned[pair.maleID] && !assigned[pair.femaleID] {
			return pair, true
		}
	}
	return pairScore{}, false
}

func addUnassignedToGroup(group *Group, males, females []int64, assigned map[int64]bool) {
	for _, maleID := range males {
		if !assigned[maleID] {
			group.MaleIDs = append(group.MaleIDs, maleID)
			assigned[maleID] = true
			break
		}
	}
	for _, femaleID := range females {
		if !assigned[femaleID] {
			group.FemaleIDs = append(group.FemaleIDs, femaleID)
			assigned[femaleID] = true
			break
		}
	}
}

func determineTargetGender(group *Group, targetMale, targetFemale int) string {
	if len(group.MaleIDs) < targetMale && len(group.FemaleIDs) < targetFemale {
		if len(group.MaleIDs) <= len(group.FemaleIDs) {
			return "male"
		}
		return "female"
	}
	if len(group.MaleIDs) < targetMale {
		return "male"
	}
	if len(group.FemaleIDs) < targetFemale {
		return "female"
	}
	return ""
}

func expandGroupToFull(group *Group, males, females []int64, assigned map[int64]bool,
	targetMale, targetFemale int, selections map[int64][]Selection, picks []MatchmakerPick) {
	maxIter := maxInt(targetMale, targetFemale) * 2
	for i := 0; i < maxIter; i++ {
		if len(group.MaleIDs) >= targetMale && len(group.FemaleIDs) >= targetFemale {
			break
		}
		gender := determineTargetGender(group, targetMale, targetFemale)
		if gender == "" {
			break
		}
		pool := males
		if gender == "female" {
			pool = females
		}
		var available []int64
		for _, id := range pool {
			if !assigned[id] {
				available = append(available, id)
			}
		}
		if len(available) == 0 {
			break
		}
		members := make([]int64, 0, len(group.MaleIDs)+len(group.FemaleIDs))
		members = append(members, group.MaleIDs...)
		members = append(members, group.FemaleIDs...)

		type candidate struct {
			id    int64
			score float64
		}
		scored := make([]candidate, 0, len(available))
		for _, id := range available {
			scored = append(scored, candidate{
				id:    id,
				score: groupAffinityScore(id, members, selections, picks),
			})
		}
		sort.SliceStable(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})
		best := scored[0].id
		if gender == "male" {
			group.MaleIDs = append(group.MaleIDs, best)
		} else {
			group.FemaleIDs = append(group.FemaleIDs, best)
		}
		assigned[best] = true
	}
}

func distributeRemaining(groups []Group, allRemaining []int64,
	malesSet, femalesSet map[int64]bool, fullCount int) {
	if len(allRemaining) == 0 || fullCount == 0 {
		return
	}
	var remMales, remFemales []int64
	for _, id := range allRemaining {
		switch {
		case malesSet[id]:
			remMales = append(remMales, id)
		case femalesSet[id]:
			remFemales = append(remFemales, id)
		}
	}
	mPer := len(remMales) / fullCount
	mExtra := len(remMales) % fullCount
	fPer := len(remFemales) / fullCount
	fExtra := len(remFemales) % fullCount
	mi, fi := 0, 0
	for gi := 0; gi < fullCount; gi++ {
		mc := mPer
		if gi < mExtra {
			mc++
		}
		fc := fPer
		if gi < fExtra {
			fc++
		}
		for j := 0; j < mc && mi < len(remMales); j++ {
			groups[gi].MaleIDs = append(groups[gi].MaleIDs, remMales[mi])
			mi++
		}
		for j := 0; j < fc && fi < len(remFemales); j++ {
			groups[gi].FemaleIDs = append(groups[gi].FemaleIDs, remFemales[fi])
			fi++
		}
	}
}

// RunGroupingAlgorithm is the direct port of the legacy satellite rolling
// algorithm. participants should be ordered deterministically by the caller;
// selections maps each participant to its ordered choices.
func RunGroupingAlgorithm(participants []Participant, selections map[int64][]Selection,
	malesPerGroup, femalesPerGroup int, picks []MatchmakerPick) ([]Group, error) {
	if malesPerGroup < 1 || femalesPerGroup < 1 {
		return nil, ErrInvalidGroupSize
	}

	var males, females []int64
	for _, participant := range participants {
		switch participant.Gender {
		case "male":
			males = append(males, participant.ID)
		case "female":
			females = append(females, participant.ID)
		}
	}
	malesSet := make(map[int64]bool, len(males))
	for _, id := range males {
		malesSet[id] = true
	}
	femalesSet := make(map[int64]bool, len(females))
	for _, id := range females {
		femalesSet[id] = true
	}

	fullCount := len(males) / malesPerGroup
	if byFemales := len(females) / femalesPerGroup; byFemales < fullCount {
		fullCount = byFemales
	}
	if fullCount == 0 {
		return []Group{}, nil
	}

	scores := pairScores(males, females, selections, picks)
	assigned := make(map[int64]bool, len(participants))
	groups := make([]Group, 0, fullCount)

	for i := 0; i < fullCount; i++ {
		group := Group{
			GroupID:   i + 1,
			MaleIDs:   []int64{},
			FemaleIDs: []int64{},
		}
		if core, ok := findCorePair(scores, assigned); ok {
			group.MaleIDs = append(group.MaleIDs, core.maleID)
			group.FemaleIDs = append(group.FemaleIDs, core.femaleID)
			assigned[core.maleID] = true
			assigned[core.femaleID] = true
		} else {
			addUnassignedToGroup(&group, males, females, assigned)
		}
		expandGroupToFull(&group, males, females, assigned, malesPerGroup, femalesPerGroup,
			selections, picks)
		if len(group.MaleIDs) >= malesPerGroup && len(group.FemaleIDs) >= femalesPerGroup {
			groups = append(groups, group)
			continue
		}
		// Not enough people to fill the group: release them and stop creating.
		for _, id := range group.Members() {
			delete(assigned, id)
		}
		break
	}

	remaining := make([]int64, 0, len(participants))
	for _, id := range males {
		if !assigned[id] {
			remaining = append(remaining, id)
		}
	}
	for _, id := range females {
		if !assigned[id] {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) > 0 && len(groups) > 0 {
		distributeRemaining(groups, remaining, malesSet, femalesSet, len(groups))
	}
	return groups, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
