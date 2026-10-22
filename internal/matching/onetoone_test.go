package matching

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func ids(n int, prefix, gender string) []OtoParticipant {
	out := make([]OtoParticipant, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, OtoParticipant{Key: prefix + strconv.Itoa(i), Gender: gender})
	}
	return out
}

func addSel(dst map[string][]Choice, selector string, targets ...string) {
	choices := make([]Choice, 0, len(targets))
	for i, target := range targets {
		choices = append(choices, Choice{Target: target, Priority: i + 1})
	}
	dst[selector] = choices
}

func ranked(result map[string][]RankedTarget, key string) []string {
	out := make([]string, 0, len(result[key]))
	for _, item := range result[key] {
		out = append(out, item.Target)
	}
	return out
}

func maleTargets(result OfflineResult, key string) []string {
	out := make([]string, 0, len(result.MaleLists[key]))
	for _, item := range result.MaleLists[key] {
		out = append(out, item.Target)
	}
	return out
}

func femaleTargets(result OfflineResult, key string) []string {
	out := make([]string, 0, len(result.FemaleLists[key]))
	for _, item := range result.FemaleLists[key] {
		out = append(out, item.Target)
	}
	return out
}

func assertEdgesConsistent(t *testing.T, result OfflineResult) {
	t.Helper()
	maleEdges := map[[2]string]bool{}
	for male, list := range result.MaleLists {
		for _, item := range list {
			maleEdges[[2]string{male, item.Target}] = true
		}
	}
	femaleEdges := map[[2]string]bool{}
	for female, list := range result.FemaleLists {
		for _, item := range list {
			femaleEdges[[2]string{item.Target, female}] = true
		}
	}
	if !reflect.DeepEqual(maleEdges, femaleEdges) {
		t.Fatalf("male/female lists disagree: %v vs %v", maleEdges, femaleEdges)
	}
}

// ---------------------------------------------------------------------------
// Online top-N, ported from tests/test_onetoone_matching.py
// ---------------------------------------------------------------------------

func TestOnlineMyChoicesRankedByPriorityWhenNoMutual(t *testing.T) {
	participants := append([]OtoParticipant{{Key: "m0", Gender: "male"}}, ids(10, "f", "female")...)
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0", "f1", "f2", "f3", "f4", "f5", "f6")
	result := RunOnline(participants, selections, nil, 10)
	want := []string{"f0", "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9"}
	if got := ranked(result, "m0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ranked = %v, want %v", got, want)
	}
}

func TestOnlineUnselectedWhoChoseMeGetsSurpriseSlot(t *testing.T) {
	participants := append([]OtoParticipant{{Key: "m0", Gender: "male"}}, ids(10, "f", "female")...)
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0", "f1", "f2", "f3", "f4", "f5", "f6")
	addSel(selections, "f7", "m0")
	result := RunOnline(participants, selections, nil, 10)
	want := []string{"f0", "f1", "f2", "f7", "f3", "f4", "f5", "f6", "f8", "f9"}
	if got := ranked(result, "m0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ranked = %v, want %v", got, want)
	}
}

func TestOnlineZeroChoicesRankedByTheirWill(t *testing.T) {
	participants := []OtoParticipant{
		{Key: "m0", Gender: "male"},
		{Key: "f0", Gender: "female"},
		{Key: "f1", Gender: "female"},
		{Key: "f2", Gender: "female"},
	}
	selections := map[string][]Choice{}
	addSel(selections, "f0", "m0")
	selections["f1"] = []Choice{{Target: "m0", Priority: 3}}
	result := RunOnline(participants, selections, nil, 10)
	if got, want := ranked(result, "m0"), []string{"f0", "f1", "f2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranked = %v, want %v", got, want)
	}
}

func TestOnlineInsufficientAndMissingGender(t *testing.T) {
	participants := append([]OtoParticipant{{Key: "m0", Gender: "male"}}, ids(3, "f", "female")...)
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0", "f1", "f2")
	if got := RunOnline(participants, selections, nil, 10)["m0"]; len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	solo := RunOnline([]OtoParticipant{{Key: "m0", Gender: "male"}}, nil, nil, 10)
	if len(solo["m0"]) != 0 {
		t.Fatalf("solo = %v", solo["m0"])
	}
	mixed := []OtoParticipant{
		{Key: "m0", Gender: "male"},
		{Key: "f0", Gender: "female"},
		{Key: "x0", Gender: ""},
	}
	got := RunOnline(mixed, nil, nil, 10)
	if len(got["x0"]) != 0 {
		t.Fatalf("unknown gender = %v", got["x0"])
	}
	if !reflect.DeepEqual(ranked(got, "m0"), []string{"f0"}) ||
		!reflect.DeepEqual(ranked(got, "f0"), []string{"m0"}) {
		t.Fatalf("mixed result = %v", got)
	}
}

func TestOnlineEachPersonIndependentAndSimilarityTieBreak(t *testing.T) {
	participants := []OtoParticipant{
		{Key: "m0", Gender: "male"},
		{Key: "m1", Gender: "male"},
		{Key: "f0", Gender: "female"},
		{Key: "f1", Gender: "female"},
	}
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0")
	addSel(selections, "f0", "m1")
	result := RunOnline(participants, selections, nil, 10)
	if ranked(result, "m0")[0] != "f0" || ranked(result, "f0")[0] != "m1" ||
		ranked(result, "m1")[0] != "f0" {
		t.Fatalf("result = %v", result)
	}

	profiles := map[string]Profile{
		"m0": {Hobbies: []string{"看书"}},
		"f0": {Hobbies: []string{"看书", "跑步"}},
		"f1": {Hobbies: []string{"游泳"}},
	}
	similar := RunOnline(participants, nil, profiles, 10)
	if got, want := ranked(similar, "m0"), []string{"f0", "f1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("similarity ranked = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Offline balanced allocation, ported from tests/test_onetoone_offline.py
// ---------------------------------------------------------------------------

func TestOfflineEqualDivision(t *testing.T) {
	participants := append(ids(2, "m", "male"), ids(2, "f", "female")...)
	result, err := RunOffline(participants, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.MaleQuota, map[string]int{"m0": 2, "m1": 2}) {
		t.Fatalf("quota = %v", result.MaleQuota)
	}
	for key, list := range result.MaleLists {
		if len(list) != 2 {
			t.Fatalf("%s got %d", key, len(list))
		}
	}
	for key, list := range result.FemaleLists {
		if len(list) != 2 {
			t.Fatalf("%s got %d", key, len(list))
		}
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineUnbalanced25x15(t *testing.T) {
	participants := append(ids(25, "m", "male"), ids(15, "f", "female")...)
	result, err := RunOffline(participants, nil, nil, 15)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, list := range result.MaleLists {
		total += len(list)
		if len(list) != 9 {
			t.Fatalf("male list len = %d", len(list))
		}
	}
	if total != 225 {
		t.Fatalf("total = %d", total)
	}
	for _, list := range result.FemaleLists {
		if len(list) != 15 {
			t.Fatalf("female list len = %d", len(list))
		}
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineNonDivisibleQuota(t *testing.T) {
	participants := append(ids(3, "m", "male"), ids(2, "f", "female")...)
	result, err := RunOffline(participants, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	quotas := []int{result.MaleQuota["m0"], result.MaleQuota["m1"], result.MaleQuota["m2"]}
	sort.Ints(quotas)
	if !reflect.DeepEqual(quotas, []int{1, 1, 2}) {
		t.Fatalf("quotas = %v", quotas)
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineMutualPreference(t *testing.T) {
	participants := append(ids(2, "m", "male"), ids(2, "f", "female")...)
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0")
	addSel(selections, "f0", "m0")
	addSel(selections, "m1", "f1")
	addSel(selections, "f1", "m1")
	result, err := RunOffline(participants, selections, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(maleTargets(result, "m0"), []string{"f0"}) ||
		!reflect.DeepEqual(maleTargets(result, "m1"), []string{"f1"}) {
		t.Fatalf("result = %v", result)
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineFemaleGetsTopKVisitors(t *testing.T) {
	participants := append(ids(3, "m", "male"), OtoParticipant{Key: "f0", Gender: "female"})
	selections := map[string][]Choice{}
	addSel(selections, "f0", "m0", "m1")
	addSel(selections, "m0", "f0")
	addSel(selections, "m1", "f0")
	addSel(selections, "m2", "f0")
	result, err := RunOffline(participants, selections, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := femaleTargets(result, "f0"), []string{"m0", "m1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visitors = %v, want %v", got, want)
	}
	if result.MaleQuota["m2"] != 0 || len(result.MaleLists["m2"]) != 0 {
		t.Fatalf("m2 = %v", result.MaleLists["m2"])
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineEveryFemaleHasVisitor(t *testing.T) {
	participants := append(ids(2, "m", "male"), ids(5, "f", "female")...)
	result, err := RunOffline(participants, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for key, list := range result.FemaleLists {
		if len(list) != 1 {
			t.Fatalf("%s got %d visitors", key, len(list))
		}
	}
	quotas := []int{result.MaleQuota["m0"], result.MaleQuota["m1"]}
	sort.Ints(quotas)
	if !reflect.DeepEqual(quotas, []int{2, 3}) {
		t.Fatalf("quotas = %v", quotas)
	}
	assertEdgesConsistent(t, result)
}

func TestOfflineInvalidFemaleChats(t *testing.T) {
	participants := append(ids(3, "m", "male"), ids(2, "f", "female")...)
	for _, k := range []int{0, -1} {
		if _, err := RunOffline(participants, nil, nil, k); err == nil {
			t.Fatalf("female_chats=%d should fail", k)
		}
	}
	tooMany := append(ids(2, "m", "male"), ids(3, "f", "female")...)
	if _, err := RunOffline(tooMany, nil, nil, 3); err == nil {
		t.Fatal("female_chats > male count should fail")
	}
}

func TestOfflineSingleGenderReturnsEmpty(t *testing.T) {
	result, err := RunOffline(ids(4, "m", "male"), nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MaleLists) != 0 || len(result.FemaleLists) != 0 || len(result.MaleQuota) != 0 {
		t.Fatalf("result = %v", result)
	}
}

func TestOfflineDeterministic(t *testing.T) {
	participants := append(ids(5, "m", "male"), ids(3, "f", "female")...)
	selections := map[string][]Choice{}
	addSel(selections, "m0", "f0", "f1")
	a, err := RunOffline(participants, selections, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RunOffline(participants, selections, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same input produced different allocations")
	}
}

// ---------------------------------------------------------------------------
// Golden parity fixtures generated by the frozen Python implementation.
// ---------------------------------------------------------------------------

type goldenCase struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Participants []struct {
		ID     string `json:"id"`
		Gender string `json:"gender"`
	} `json:"participants"`
	Selections map[string][]struct {
		ID       string `json:"id"`
		Priority int    `json:"priority"`
	} `json:"selections"`
	Profiles map[string]struct {
		Hobbies   []string `json:"hobbies"`
		Sports    []string `json:"sports"`
		Traits    []string `json:"traits"`
		Education string   `json:"education"`
		MBTI      []string `json:"mbti"`
		City      string   `json:"city"`
		Church    string   `json:"church"`
	} `json:"profiles"`
	FemaleChats int             `json:"female_chats"`
	Expected    json.RawMessage `json:"expected"`
}

type goldenFile struct {
	Cases []goldenCase `json:"cases"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/onetoone_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var f goldenFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Cases
}

func goldenProfiles(tc goldenCase) map[string]Profile {
	profiles := map[string]Profile{}
	for key, p := range tc.Profiles {
		profiles[key] = Profile{
			Hobbies: p.Hobbies, Sports: p.Sports, Traits: p.Traits,
			Education: p.Education, MBTI: p.MBTI, City: p.City, Church: p.Church,
		}
	}
	return profiles
}

func TestGoldenParityWithLegacyPython(t *testing.T) {
	for _, tc := range loadGolden(t) {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			var participants []OtoParticipant
			for _, p := range tc.Participants {
				participants = append(participants, OtoParticipant{Key: p.ID, Gender: p.Gender})
			}
			selections := map[string][]Choice{}
			for selector, choices := range tc.Selections {
				for _, c := range choices {
					selections[selector] = append(selections[selector], Choice{Target: c.ID, Priority: c.Priority})
				}
			}
			profiles := goldenProfiles(tc)

			if tc.Kind == "online" {
				var want map[string][][2]any
				if err := json.Unmarshal(tc.Expected, &want); err != nil {
					t.Fatal(err)
				}
				got := RunOnline(participants, selections, profiles, tc.FemaleChats)
				if len(got) != len(want) {
					t.Fatalf("keys = %d, want %d", len(got), len(want))
				}
				for key, rows := range want {
					gotRows := got[key]
					if len(gotRows) != len(rows) {
						t.Fatalf("%s len = %d, want %d", key, len(gotRows), len(rows))
					}
					for i, row := range rows {
						if gotRows[i].Target != row[0].(string) ||
							gotRows[i].Rank != int(row[1].(float64)) {
							t.Fatalf("%s[%d] = %+v, want %v", key, i, gotRows[i], row)
						}
					}
				}
				return
			}

			var want struct {
				MaleLists map[string][][3]any `json:"male_lists"`
				Female    map[string][][2]any `json:"female_lists"`
				Quota     map[string]int      `json:"male_quota"`
			}
			if err := json.Unmarshal(tc.Expected, &want); err != nil {
				t.Fatal(err)
			}
			got, err := RunOffline(participants, selections, profiles, tc.FemaleChats)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.MaleQuota, want.Quota) {
				t.Fatalf("quota = %v, want %v", got.MaleQuota, want.Quota)
			}
			if len(got.MaleLists) != len(want.MaleLists) {
				t.Fatalf("male list count = %d, want %d", len(got.MaleLists), len(want.MaleLists))
			}
			for key, rows := range want.MaleLists {
				gotRows := got.MaleLists[key]
				if len(gotRows) != len(rows) {
					t.Fatalf("%s len = %d, want %d", key, len(gotRows), len(rows))
				}
				for i, row := range rows {
					if gotRows[i].Target != row[0].(string) ||
						gotRows[i].Rank != int(row[1].(float64)) ||
						math.Abs(gotRows[i].Score-row[2].(float64)) > 1e-9 {
						t.Fatalf("%s[%d] = %+v, want %v", key, i, gotRows[i], row)
					}
				}
			}
			for key, rows := range want.Female {
				gotRows := got.FemaleLists[key]
				if len(gotRows) != len(rows) {
					t.Fatalf("%s len = %d, want %d", key, len(gotRows), len(rows))
				}
				for i, row := range rows {
					if gotRows[i].Target != row[0].(string) ||
						math.Abs(gotRows[i].Score-row[1].(float64)) > 1e-9 {
						t.Fatalf("%s[%d] = %+v, want %v", key, i, gotRows[i], row)
					}
				}
			}
		})
	}
}
