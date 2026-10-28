package httpserver

import "encoding/json"

// bootCardData is the server-rendered mini deck used before the SPA boots.
// The first card is fully rendered; the next bootstrap cards are compact
// (photo/name/simple + profile skeleton) so the user can swipe immediately.
type bootCardData struct {
	Total      int
	Cards      []bootCardView
	ShowHearts bool
	AnonLeft   int
	RealLeft   int
}

type bootCardView struct {
	OpenID  string
	Compact bool

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
	Liked        bool

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

// bootCardFromPayload builds the static mini deck from the already-sanitized
// home bootstrap. It returns false when the viewer has no deck card (ID gate,
// empty deck, …) so the shell falls back to the Vue boot skeleton.
func bootCardFromPayload(payload map[string]any) (bootCardData, bool) {
	cards, ok := payload["cards"].([]map[string]any)
	if !ok || len(cards) == 0 {
		return bootCardData{}, false
	}

	observer := false
	anonLeft, realLeft := 0, 0
	if user, ok := payload["user"].(map[string]any); ok {
		observer, _ = user["is_observer"].(bool)
		anonLeft = intValue(user["anon_left"])
		realLeft = intValue(user["real_left"])
	}

	views := make([]bootCardView, 0, len(cards))
	for i, first := range cards {
		if first == nil {
			continue
		}
		view := bootCardView{
			OpenID:   stringValue(first["openid"]),
			Compact:  i > 0,
			Photo:    firstCardPhoto(first),
			Nickname: stringValue(first["nickname"]),
			MsgCount: intValue(first["msg_count"]),
		}
		if gender := stringValue(first["gender"]); gender != "" {
			if gender == "男性" {
				view.GenderIcon = "♂"
				view.PhotoPlaceholder = "👨"
			} else {
				view.GenderIcon = "♀"
				view.PhotoPlaceholder = "👩"
			}
		} else {
			view.PhotoPlaceholder = "👩"
		}
		if display, ok := first["display_fields"].(CardDisplayFields); ok {
			view.Simple = display.Simple
			if !view.Compact {
				for _, section := range display.Sections {
					sec := bootCardSection{Title: section.Title, Icon: section.Icon}
					for _, field := range section.Fields {
						sec.Fields = append(sec.Fields, bootCardField{
							Label: field.Label,
							Value: field.Value,
							Full:  field.Label == "我是怎样的人" || field.Label == "理想中的TA",
						})
					}
					if len(sec.Fields) > 0 {
						view.Sections = append(view.Sections, sec)
					}
				}
			}
		}

		photos, _ := first["photos"].([]string)
		view.PhotoCount = len(photos)
		if view.PhotoCount == 0 && view.Photo != "" {
			view.PhotoCount = 1
		}
		if view.PhotoCount > 0 {
			view.Dots = make([]bool, view.PhotoCount)
			view.Dots[0] = true
		}

		faces, _ := first["faces"].([]any)
		if len(faces) > 0 && faces[0] != nil {
			if raw, err := json.Marshal(faces[0]); err == nil {
				view.FacesJSON = string(raw)
			}
		}

		isSelf, _ := first["is_self"].(bool)
		if isSelf {
			view.BadgeText = "这是你 · 他人视角"
			view.BadgeClass = "boot-badge--self"
		} else if recommended, _ := first["recommended"].(bool); recommended {
			view.BadgeText = "推荐"
			view.BadgeClass = "boot-badge--recommend"
		}
		if !view.Compact {
			view.ShowFeedback = !isSelf
			view.ShowHeart = !isSelf && !observer
			if liked, _ := first["liked"].(bool); liked {
				view.Liked = true
			}
		}

		views = append(views, view)
	}
	if len(views) == 0 {
		return bootCardData{}, false
	}

	total, _ := payload["cards_total"].(int)
	if total <= 0 {
		total = len(views)
	}
	if total < 1 {
		total = 1
	}
	return bootCardData{
		Total:      total,
		Cards:      views,
		ShowHearts: !observer,
		AnonLeft:   anonLeft,
		RealLeft:   realLeft,
	}, true
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
