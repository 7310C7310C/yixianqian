package face

import "testing"

func TestApplyComplianceBlocksNonHumanPhoto(t *testing.T) {
	local := Result{
		HasFace: true,
		Center:  []float64{0.4, 0.3},
		Box:     []float64{0.2, 0.25},
		Width:   800,
		Height:  600,
	}
	got := ApplyCompliance(local, ComplianceResult{Blocked: true, Reason: "animal"})
	if got.HasFace {
		t.Fatal("has_face must be false when compliance blocks the photo")
	}
	if !got.Blocked || got.Reason != "animal" {
		t.Fatalf("blocked/reason = %v/%q", got.Blocked, got.Reason)
	}
	// 几何仍来自本地检测，不得被第三方结果改写或清空。
	if len(got.Center) != 2 || got.Center[0] != 0.4 || len(got.Box) != 2 || got.Box[1] != 0.25 {
		t.Fatalf("geometry changed: %+v", got)
	}
}

func TestApplyComplianceKeepsLocalFaceWhenPassed(t *testing.T) {
	local := Result{HasFace: true, Center: []float64{0.4, 0.3}, Box: []float64{0.2, 0.25}, Width: 800, Height: 600}
	got := ApplyCompliance(local, ComplianceResult{})
	if !got.HasFace || got.Blocked || got.Reason != "" {
		t.Fatalf("result = %+v", got)
	}
	if got.Center[0] != 0.4 || got.Box[0] != 0.2 {
		t.Fatalf("geometry changed: %+v", got)
	}
}

func TestApplyComplianceDoesNotInventFace(t *testing.T) {
	local := Result{HasFace: false, Width: 800, Height: 600}
	for _, blocked := range []bool{false, true} {
		got := ApplyCompliance(local, ComplianceResult{Blocked: blocked, Reason: "cartoon"})
		if got.HasFace {
			t.Fatalf("blocked=%v: compliance must never set has_face=true", blocked)
		}
		if blocked && (!got.Blocked || got.Reason != "cartoon") {
			t.Fatalf("blocked verdict lost: %+v", got)
		}
	}
}

func TestComplianceResultZeroValueIsPass(t *testing.T) {
	// 调用方把 nil/零值视为放行；这是 fail-open 的类型级保证。
	var zero ComplianceResult
	if zero.Blocked {
		t.Fatal("zero ComplianceResult must not block")
	}
}
