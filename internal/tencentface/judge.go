package tencentface

import "strings"

// 判定阈值与关键字集合逐字照抄 v1 web/backend/tencent_face.py：两个条件
// 同时满足才拦（标签命中动物/卡通 + 活体接口判定无人脸）。不要放宽。
const (
	// AnimalConfidence 是动物标签的置信度阈值（v1 用 >= 30）。
	AnimalConfidence = 30
	// CartoonConfidence 是明确卡通名称的置信度阈值（v1 用 >= 80）。
	CartoonConfidence = 80
)

// animalSecondCategories 是 v1 的 ANIMAL_C2 集合（二级分类精确匹配）。
var animalSecondCategories = map[string]bool{
	"宠物": true, "哺乳动物": true, "鸟": true, "禽类": true, "鱼": true,
	"海洋动物": true, "昆虫": true, "爬行动物": true, "两栖动物": true,
	"动物": true, "家畜": true, "家禽": true, "野生动物": true,
}

// animalNameKeys 是 v1 的 ANIMAL_NAMES（标签名称包含即算）。
var animalNameKeys = []string{"猫", "狗", "宠物", "哺乳动物", "兔", "仓鼠", "动物"}

// cartoonNameKeys 是 v1 的 CARTOON_NAMES（只有明确卡通名才算；涂鸦等不算）。
var cartoonNameKeys = []string{"动漫", "卡通", "漫画", "插画", "手办", "玩偶", "表情包"}

// ReasonAnimal/ReasonCartoon 是写入结果与日志的固定原因码（不存厂商标签名）。
const (
	ReasonAnimal  = "animal"
	ReasonCartoon = "cartoon"
)

// labelConfidence 取动物/卡通标签的最高置信度，语义等同于 v1 _label_hit。
func labelConfidence(labels []Label) (animal, cartoon float64) {
	for _, label := range labels {
		name := label.Name
		conf := label.Confidence
		if (animalSecondCategories[label.SecondCategory] || containsAny(name, animalNameKeys)) && conf > animal {
			animal = conf
		}
		if containsAny(name, cartoonNameKeys) && conf > cartoon {
			cartoon = conf
		}
	}
	return animal, cartoon
}

func containsAny(name string, keys []string) bool {
	for _, key := range keys {
		if strings.Contains(name, key) {
			return true
		}
	}
	return false
}

// Judge 是纯判定：标签命中 且 活体接口判定无人脸 -> blocked。
// Verdict.Reason 用固定码 animal/cartoon，不写厂商标签名。
func Judge(labels []Label, noFace bool) Verdict {
	animal, cartoon := labelConfidence(labels)
	verdict := Verdict{Animal: animal, Cartoon: cartoon, NoFace: noFace}
	if !noFace {
		return verdict
	}
	switch {
	case animal >= AnimalConfidence:
		verdict.Blocked = true
		verdict.Reason = ReasonAnimal
	case cartoon >= CartoonConfidence:
		verdict.Blocked = true
		verdict.Reason = ReasonCartoon
	}
	return verdict
}

// labelHit 报告标签是否命中了需要调用活体接口的动物/卡通阈值。
func labelHit(animal, cartoon float64) bool {
	return animal >= AnimalConfidence || cartoon >= CartoonConfidence
}
