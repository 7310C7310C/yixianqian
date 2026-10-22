package db

import (
	"testing"
)

func socialTestProfiles(n int) []SocialProfile {
	out := make([]SocialProfile, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, SocialProfile{ID: int64(i), Nickname: "u"})
	}
	return out
}

func socialIDsOf(cards []SocialProfile) []int64 {
	out := make([]int64, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

func TestOrderCardsStableAndPinned(t *testing.T) {
	cards := socialTestProfiles(100)
	likedMe := map[int64]bool{5: true, 10: true, 20: true, 30: true}
	pinned := []int64{100, 99, 100}

	first := socialIDsOf(OrderCards(cards, likedMe, pinned, "seed-abc"))
	second := socialIDsOf(OrderCards(cards, likedMe, pinned, "seed-abc"))
	if len(first) != 100 || len(second) != 100 {
		t.Fatalf("lengths = %d, %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("order changed at %d: %v vs %v", i, first[i], second[i])
		}
	}
	if first[0] != 100 || first[1] != 99 {
		t.Fatalf("pinned head = %v", first[:2])
	}
	// All liked-me cards fit in the 30-card front region.
	front := make(map[int64]bool)
	for _, id := range first[2:32] {
		front[id] = true
	}
	for id := range likedMe {
		if id == 100 || id == 99 {
			continue
		}
		if !front[id] {
			t.Fatalf("liked-me card %d not in front 30: %v", id, first[:32])
		}
	}
	other := socialIDsOf(OrderCards(cards, likedMe, pinned, "seed-xyz"))
	same := true
	for i := range first {
		if first[i] != other[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seed produced identical order")
	}
}

func TestOrderCardsPaginationNoDupNoGap(t *testing.T) {
	cards := socialTestProfiles(77)
	likedMe := map[int64]bool{3: true, 17: true, 41: true, 77: true}
	ordered := OrderCards(cards, likedMe, nil, "page-seed")

	seen := make(map[int64]bool, len(ordered))
	var paged []int64
	for offset := 0; offset < len(ordered); offset += 10 {
		end := offset + 10
		if end > len(ordered) {
			end = len(ordered)
		}
		for _, c := range ordered[offset:end] {
			if seen[c.ID] {
				t.Fatalf("duplicate card %d across pages", c.ID)
			}
			seen[c.ID] = true
			paged = append(paged, c.ID)
		}
	}
	if len(paged) != len(cards) {
		t.Fatalf("paged %d cards, want %d", len(paged), len(cards))
	}
	for _, c := range cards {
		if !seen[c.ID] {
			t.Fatalf("card %d missing from pages", c.ID)
		}
	}
}

func TestOrderCardsEmptyAndSingle(t *testing.T) {
	if got := OrderCards(nil, nil, nil, "x"); got != nil {
		t.Fatalf("nil cards -> %v", got)
	}
	one := []SocialProfile{{ID: 1}}
	got := OrderCards(one, map[int64]bool{1: true}, nil, "x")
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("single card = %+v", got)
	}
}
