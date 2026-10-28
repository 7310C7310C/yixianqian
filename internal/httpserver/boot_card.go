package httpserver

import (
	"encoding/json"
	"fmt"
)

// bootCardData is the server-rendered first card used before the SPA boots.
// It intentionally mirrors the default card page (photo overlay + profile tab)
// so the first paint is the final card, not a bare image + shimmer.
type bootCardData struct {
	Counter    string
	ShowHearts bool
	AnonLeft   int
	RealLeft   int

	Photo            string
	PhotoCount       int
	Dots             []bool
	PhotoPlaceholder string

	BadgeText  string
	BadgeClass string

	Nickname   string
	GenderIcon string
	Simple     string
	FacesJSON  string
	MsgCount   int

	ShowFeedback bool
	ShowHeart    bool

	Sections []bootCardSection
}

type bootCardSection struct {
	Title  string
	Icon   string
	Fields []bootCardField
}

type bootCardField struct {
	Label string
	Value string
	Full  bool
}

// bootCardFromPayload builds the boot card view from the already-sanitized
// home bootstrap. It returns false when the viewer has no deck card (ID gate,
// empty deck, …) so the shell falls back to the existing Vue boot skeleton.
func bootCardFromPayload(payload map[string]any) (bootCardData, bool) {
	cards, ok := payload["cards"].([]map[string]any)
	if !ok || len(cards) == 0 {
		return bootCardData{}, false
	}
	first := cards[0]
	if first == nil {
		return bootCardData{}, false
	}

	card := bootCardData{
		Photo:    firstCardPhoto(first),
		Nickname: stringValue(first["nickname"]),
	}
	observer := false
	anonLeft, realLeft := 0, 0
	if user, ok := payload["user"].(map[string]any); ok {
		observer, _ = user["is_observer"].(bool)
		anonLeft = intValue(user["anon_left"])
		realLeft = intValue(user["real_left"])
	}
	if gender := stringValue(first["gender"]); gender != "" {
		if gender == "男性" {
			card.GenderIcon = "♂"
			card.PhotoPlaceholder = "👨"
		} else {
			card.GenderIcon = "♀"
			card.PhotoPlaceholder = "👩"
		}
	} else {
		card.PhotoPlaceholder = "👩"
	}
	card.MsgCount = intValue(first["msg_count"])
	if display, ok := first["display_fields"].(CardDisplayFields); ok {
		card.Simple = display.Simple
		for _, section := range display.Sections {
			view := bootCardSection{Title: section.Title, Icon: section.Icon}
			for _, field := range section.Fields {
				view.Fields = append(view.Fields, bootCardField{
					Label: field.Label,
					Value: field.Value,
					Full:  field.Label == "我是怎样的人" || field.Label == "理想中的TA",
				})
			}
			if len(view.Fields) > 0 {
				card.Sections = append(card.Sections, view)
			}
		}
	}

	photos, _ := first["photos"].([]string)
	card.PhotoCount = len(photos)
	if card.PhotoCount == 0 && card.Photo != "" {
		card.PhotoCount = 1
	}
	if card.PhotoCount > 0 {
		card.Dots = make([]bool, card.PhotoCount)
		card.Dots[0] = true
	}

	faces, _ := first["faces"].([]any)
	if len(faces) > 0 && faces[0] != nil {
		if raw, err := json.Marshal(faces[0]); err == nil {
			card.FacesJSON = string(raw)
		}
	}

	isSelf, _ := first["is_self"].(bool)
	if isSelf {
		card.BadgeText = "这是你 · 他人视角"
		card.BadgeClass = "boot-badge--self"
	} else if recommended, _ := first["recommended"].(bool); recommended {
		card.BadgeText = "推荐"
		card.BadgeClass = "boot-badge--recommend"
	}
	card.ShowFeedback = !isSelf
	card.ShowHeart = !isSelf && !observer

	total, _ := payload["cards_total"].(int)
	if total <= 0 {
		total = len(cards)
	}
	if total < 1 {
		total = 1
	}
	card.Counter = fmt.Sprintf("1 / %d", total)

	if !observer {
		card.ShowHearts = true
		card.AnonLeft = anonLeft
		card.RealLeft = realLeft
	}
	return card, true
}

func firstCardPhoto(card map[string]any) string {
	if photo := stringValue(card["photo"]); photo != "" {
		return photo
	}
	if thumbs, ok := card["photos_thumb"].([]string); ok && len(thumbs) > 0 {
		return thumbs[0]
	}
	if photos, ok := card["photos"].([]string); ok && len(photos) > 0 {
		return photos[0]
	}
	return ""
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func intValue(value any) int {
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
