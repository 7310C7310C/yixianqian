package db

import "testing"

func orderFixtureProfiles(n int) []SocialProfile {
	out := make([]SocialProfile, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, SocialProfile{ID: int64(i)})
	}
	return out
}

func TestOrderCardIDsMatchesOrderCards(t *testing.T) {
	profiles := orderFixtureProfiles(200)
	ids := make([]int64, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.ID)
	}

	cases := []struct {
		name   string
		liked  map[int64]bool
		pinned []int64
		seed   string
	}{
		{name: "empty extas", seed: "seed-a"},
		{name: "liked subset", liked: map[int64]bool{3: true, 17: true, 120: true, 199: true}, seed: "seed-b"},
		{name: "pinned present and absent", liked: map[int64]bool{2: true, 88: true}, pinned: []int64{5, 5, 9999, 1, 200}, seed: "seed-c"},
		{name: "all pinned", pinned: []int64{1, 2, 3, 4, 5}, seed: "seed-d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := socialIDsOf(OrderCards(profiles, tc.liked, tc.pinned, tc.seed))
			got := OrderCardIDs(ids, tc.liked, tc.pinned, tc.seed)
			if len(want) != len(got) {
				t.Fatalf("length = %d, want %d", len(got), len(want))
			}
			for i := range want {
				if want[i] != got[i] {
					t.Fatalf("position %d = %d, want %d", i, got[i], want[i])
				}
			}
		})
	}
}

func TestOrderCardIDsEdgeCases(t *testing.T) {
	if got := OrderCardIDs(nil, nil, nil, "x"); got != nil {
		t.Fatalf("nil ids = %v, want nil", got)
	}
	one := []int64{7}
	got := OrderCardIDs(one, map[int64]bool{7: true}, nil, "x")
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("one id = %v", got)
	}
}
