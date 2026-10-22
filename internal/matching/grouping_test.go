package matching

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func withGender(ids []int64, gender string) []Participant {
	out := make([]Participant, 0, len(ids))
	for _, id := range ids {
		out = append(out, Participant{ID: id, Gender: gender})
	}
	return out
}

func allMemberIDs(groups []Group) []int64 {
	var out []int64
	for _, group := range groups {
		out = append(out, group.Members()...)
	}
	return out
}

func TestAffinityWeights(t *testing.T) {
	oneWay := map[int64][]Selection{
		1: {{ID: 2, Priority: 1}},
	}
	if got := MutualAffinityScore(1, 2, oneWay, nil); got != 70 {
		t.Fatalf("one-way score = %v, want 70", got)
	}
	mutual := map[int64][]Selection{
		1: {{ID: 2, Priority: 1}},
		2: {{ID: 1, Priority: 1}},
	}
	if got := MutualAffinityScore(1, 2, mutual, nil); got != 140 {
		t.Fatalf("mutual score = %v, want 140", got)
	}
	picks := []MatchmakerPick{{Person1ID: 1, Person2ID: 2, Stars: 5}}
	if got := MutualAffinityScore(1, 2, nil, picks); got != 30 {
		t.Fatalf("matchmaker score = %v, want 30", got)
	}
	picks = []MatchmakerPick{{Person1ID: 1, Person2ID: 2, Stars: 5},
		{Person1ID: 2, Person2ID: 1, Stars: 5}}
	if got := MutualAffinityScore(1, 2, nil, picks); got != 60 {
		t.Fatalf("bidirectional matchmaker score = %v, want 60", got)
	}
}

func TestInvalidGroupSize(t *testing.T) {
	for _, sizes := range [][2]int{{0, 1}, {1, 0}, {-1, 2}} {
		if _, err := RunGroupingAlgorithm(nil, nil, sizes[0], sizes[1], nil); !errors.Is(err, ErrInvalidGroupSize) {
			t.Fatalf("sizes %v error = %v", sizes, err)
		}
	}
}

func TestExactGroups(t *testing.T) {
	participants := append(withGender([]int64{1, 2, 3, 4}, "male"),
		withGender([]int64{101, 102, 103, 104}, "female")...)
	groups, err := RunGroupingAlgorithm(participants, nil, 2, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	for _, group := range groups {
		if len(group.MaleIDs) != 2 || len(group.FemaleIDs) != 2 {
			t.Fatalf("group not full: %+v", group)
		}
	}
}

func TestRemainderDistributedWithoutLoss(t *testing.T) {
	participants := append(withGender([]int64{1, 2, 3, 4, 5}, "male"),
		withGender([]int64{101, 102, 103, 104, 105}, "female")...)
	groups, err := RunGroupingAlgorithm(participants, nil, 2, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	all := allMemberIDs(groups)
	if len(all) != 10 || len(uniqueIDs(all)) != 10 {
		t.Fatalf("members = %v", all)
	}
}

func TestInsufficientReturnsEmpty(t *testing.T) {
	cases := [][]Participant{
		{{ID: 1, Gender: "male"}, {ID: 2, Gender: "female"}},
		nil,
		withGender([]int64{1, 2}, "male"),
	}
	for _, participants := range cases {
		groups, err := RunGroupingAlgorithm(participants, nil, 2, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 0 {
			t.Fatalf("groups = %+v, want empty", groups)
		}
	}
}

func TestMutualTopPickGroupedTogether(t *testing.T) {
	participants := []Participant{
		{ID: 1, Gender: "male"}, {ID: 2, Gender: "male"},
		{ID: 101, Gender: "female"}, {ID: 102, Gender: "female"},
	}
	selections := map[int64][]Selection{
		1:   {{ID: 101, Priority: 1}},
		101: {{ID: 1, Priority: 1}},
	}
	groups, err := RunGroupingAlgorithm(participants, selections, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.MaleIDs[0] == 1 && group.FemaleIDs[0] != 101 {
			t.Fatalf("mutual pick split: %+v", groups)
		}
	}
}

func TestLargeScaleDeterministic(t *testing.T) {
	participants := append(withGender(sequentialIDs(60, 1), "male"),
		withGender(sequentialIDs(60, 101), "female")...)
	first, err := RunGroupingAlgorithm(participants, nil, 3, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunGroupingAlgorithm(participants, nil, 3, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 {
		t.Fatalf("groups = %d, want 20", len(first))
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("algorithm is not deterministic")
	}
	all := allMemberIDs(first)
	if len(all) != 120 || len(uniqueIDs(all)) != 120 {
		t.Fatalf("members = %d unique = %d", len(all), len(uniqueIDs(all)))
	}
}

func TestGoldenParityCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/grouping_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name            string                 `json:"name"`
			MalesPerGroup   int                    `json:"males_per_group"`
			FemalesPerGroup int                    `json:"females_per_group"`
			Participants    []Participant          `json:"participants"`
			Selections      map[string][]Selection `json:"selections"`
			MatchmakerPicks []MatchmakerPick       `json:"matchmaker_picks"`
			Groups          []Group                `json:"groups"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 40 {
		t.Fatalf("parity fixture only has %d cases", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			selections := make(map[int64][]Selection, len(tc.Selections))
			for key, choices := range tc.Selections {
				id, err := strconv.ParseInt(key, 10, 64)
				if err != nil {
					t.Fatalf("bad selector id %q: %v", key, err)
				}
				selections[id] = choices
			}
			got, err := RunGroupingAlgorithm(tc.Participants, selections,
				tc.MalesPerGroup, tc.FemalesPerGroup, tc.MatchmakerPicks)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.Groups
			if want == nil {
				want = []Group{}
			}
			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				t.Fatalf("groups differ\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func sequentialIDs(n, start int) []int64 {
	out := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, int64(start+i))
	}
	return out
}

func uniqueIDs(ids []int64) map[int64]bool {
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	return seen
}
