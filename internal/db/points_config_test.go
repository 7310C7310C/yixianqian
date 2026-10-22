package db

import "testing"

func TestNormalizePointsPhone(t *testing.T) {
	cases := map[string]string{
		"13800001234":       "13800001234",
		"138 0000 1234":     "13800001234",
		"+86 138-0000-1234": "13800001234",
		"008613800001234":   "13800001234",
		"8613800001234":     "13800001234",
		"1380000123":        "",
		"23800001234":       "",
		"":                  "",
		"010-12345678":      "",
	}
	for raw, want := range cases {
		if got := NormalizePointsPhone(raw); got != want {
			t.Errorf("NormalizePointsPhone(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPointsConfigDefaultsMatchLegacy(t *testing.T) {
	cfg := DefaultPointsConfig()
	if cfg.InviteRewardFemale != 20 || cfg.InviteRewardMale != 15 {
		t.Fatalf("invite rewards = %d/%d, want 20/15", cfg.InviteRewardFemale, cfg.InviteRewardMale)
	}
	if cfg.InviteConfirmDays != 7 {
		t.Fatalf("confirm days = %d, want 7", cfg.InviteConfirmDays)
	}
	if cfg.RedeemRealLike != 20 || cfg.RedeemPrioritySignup != 30 ||
		cfg.RedeemWish != 30 || cfg.RedeemMatchmaker != 50 {
		t.Fatalf("redeem prices = %+v", cfg)
	}
	if cfg.PriorityRatio != 0.3 || cfg.PriorityCancelHours != 48 {
		t.Fatalf("priority config = %+v", cfg)
	}
	if cfg.MatchmakerDeadline != 14 || cfg.MatchmakerConditionMax != 200 {
		t.Fatalf("matchmaker config = %+v", cfg)
	}
	if got := InviteRewardAmount(cfg, "女性"); got != 20 {
		t.Fatalf("female reward = %d, want 20", got)
	}
	if got := InviteRewardAmount(cfg, "男性"); got != 15 {
		t.Fatalf("male reward = %d, want 15", got)
	}
	if got := InviteRewardAmount(cfg, ""); got != 15 {
		t.Fatalf("unknown gender reward = %d, want lower 15", got)
	}
}

func TestPointsConfigOverrideParsing(t *testing.T) {
	cfg := DefaultPointsConfig()
	cfg, ok := applyPointsConfigOverride("invite_confirm_days", "3", cfg)
	if !ok || cfg.InviteConfirmDays != 3 {
		t.Fatalf("confirm override = %+v ok=%v", cfg, ok)
	}
	cfg, _ = applyPointsConfigOverride("priority_ratio", "0.45", cfg)
	if cfg.PriorityRatio != 0.45 {
		t.Fatalf("ratio = %v", cfg.PriorityRatio)
	}
	// Old config accepted "20.0" for integer fields.
	cfg, _ = applyPointsConfigOverride("redeem_wish", "35.0", cfg)
	if cfg.RedeemWish != 35 {
		t.Fatalf("wish = %d", cfg.RedeemWish)
	}
	// Broken overrides fall back to defaults rather than crashing the request.
	cfg, _ = applyPointsConfigOverride("redeem_wish", "not-a-number", cfg)
	if cfg.RedeemWish != 30 {
		t.Fatalf("broken override = %d, want default 30", cfg.RedeemWish)
	}
}

func TestCanCancelPriority(t *testing.T) {
	cfg := DefaultPointsConfig()
	if CanCancelPriority(cfg, nil) {
		t.Fatal("nil hours must not refund")
	}
	h := 47.9
	if CanCancelPriority(cfg, &h) {
		t.Fatal("47.9h must not refund")
	}
	h = 48
	if !CanCancelPriority(cfg, &h) {
		t.Fatal("exactly 48h must refund")
	}
	h = 72
	if !CanCancelPriority(cfg, &h) {
		t.Fatal("72h must refund")
	}
}

func TestPointsTargetGender(t *testing.T) {
	if got := pointsTargetGender("男性"); got != "女性" {
		t.Fatalf("male viewer target = %q", got)
	}
	if got := pointsTargetGender("女性"); got != "男性" {
		t.Fatalf("female viewer target = %q", got)
	}
	if got := pointsTargetGender(""); got != "男性" {
		t.Fatalf("empty gender target = %q", got)
	}
}
