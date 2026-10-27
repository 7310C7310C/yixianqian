package tencentface

import "testing"

func labels(items ...[3]any) []Label {
	out := make([]Label, 0, len(items))
	for _, item := range items {
		name, _ := item[0].(string)
		conf, _ := item[1].(float64)
		second, _ := item[2].(string)
		out = append(out, Label{Name: name, Confidence: conf, SecondCategory: second})
	}
	return out
}

// 用例从 v1 tests/test_tencent_face.py 原样移植；阈值与双条件语义不得放宽。
func TestJudgePortedFromV1(t *testing.T) {
	cases := []struct {
		name   string
		labels []Label
		noFace bool
		block  bool
		reason string
	}{
		{
			name:   "cat photo blocked when no face",
			labels: labels([3]any{"宠物", 54.0, "宠物"}, [3]any{"猫", 39.0, "宠物"}),
			noFace: true, block: true, reason: ReasonAnimal,
		},
		{
			name:   "cat photo passes when human face detected",
			labels: labels([3]any{"宠物", 87.0, "宠物"}, [3]any{"猫", 83.0, "宠物"}),
			noFace: false, block: false,
		},
		{
			name:   "human photo without animal label not blocked",
			labels: labels([3]any{"人", 97.0, "人像"}, [3]any{"面部", 92.0, "人体部位"}),
			noFace: true, block: false,
		},
		{
			name:   "cartoon name blocked only when no face",
			labels: labels([3]any{"动漫", 90.0, "绘画卡通"}),
			noFace: true, block: true, reason: ReasonCartoon,
		},
		{
			name:   "graffiti label not in cartoon blocklist",
			labels: labels([3]any{"涂鸦", 79.0, "绘画卡通"}),
			noFace: true, block: false,
		},
		{
			name:   "low confidence animal not blocked",
			labels: labels([3]any{"宠物", 20.0, "宠物"}),
			noFace: true, block: false,
		},
		{
			name:   "plant label not blocked",
			labels: labels([3]any{"树", 53.0, "树"}),
			noFace: true, block: false,
		},
		{
			// v1 的阈值是 >=30：恰好 30 也算命中；29.9 不算。
			name:   "animal threshold is inclusive at 30",
			labels: labels([3]any{"宠物", 30.0, "宠物"}),
			noFace: true, block: true, reason: ReasonAnimal,
		},
		{
			name:   "animal just below threshold not blocked",
			labels: labels([3]any{"宠物", 29.9, "宠物"}),
			noFace: true, block: false,
		},
		{
			// 动物优先级高于卡通（v1 的 reason 取动物名）。
			name:   "animal wins over cartoon for reason",
			labels: labels([3]any{"宠物", 55.0, "宠物"}, [3]any{"动漫", 90.0, "绘画卡通"}),
			noFace: true, block: true, reason: ReasonAnimal,
		},
		{
			name:   "cartoon at exactly 80 blocks",
			labels: labels([3]any{"卡通", 80.0, "绘画卡通"}),
			noFace: true, block: true, reason: ReasonCartoon,
		},
		{
			// 二级分类命中即可，不要求名称含关键字。
			name:   "animal second category is enough",
			labels: labels([3]any{"金毛", 66.0, "宠物"}),
			noFace: true, block: true, reason: ReasonAnimal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.labels, tc.noFace)
			if got.Blocked != tc.block {
				t.Fatalf("blocked = %v, want %v (verdict=%+v)", got.Blocked, tc.block, got)
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestLabelHitOnlyWhenThresholdReached(t *testing.T) {
	if labelHit(29, 79) {
		t.Fatal("below both thresholds must not trigger liveness call")
	}
	if !labelHit(AnimalConfidence, 0) || !labelHit(0, CartoonConfidence) {
		t.Fatal("threshold values must trigger liveness call")
	}
}

func TestLabelConfidencePicksMaximum(t *testing.T) {
	animal, cartoon := labelConfidence(labels(
		[3]any{"宠物", 40.0, "宠物"},
		[3]any{"狗", 72.0, "宠物"},
		[3]any{"动漫", 85.0, "绘画卡通"},
	))
	if animal != 72 || cartoon != 85 {
		t.Fatalf("animal=%v cartoon=%v", animal, cartoon)
	}
}
