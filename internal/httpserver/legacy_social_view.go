package httpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

type legacyThreadMessage struct {
	ID              string                `json:"id"`
	AuthorOpenID    string                `json:"author_openid"`
	AuthorNickname  string                `json:"author_nickname"`
	AuthorUID       string                `json:"author_uid"`
	AuthorAvatar    string                `json:"author_avatar"`
	Content         string                `json:"content"`
	ParentID        string                `json:"parent_id"`
	ReplyToID       string                `json:"reply_to_id"`
	ReplyToNickname string                `json:"reply_to_nickname"`
	ReplyToUID      string                `json:"reply_to_uid"`
	TargetOpenID    string                `json:"target_openid,omitempty"`
	TargetNickname  string                `json:"target_nickname,omitempty"`
	TargetAvatar    string                `json:"target_avatar,omitempty"`
	OnMyCard        bool                  `json:"on_my_card,omitempty"`
	Unread          bool                  `json:"unread,omitempty"`
	Replied         bool                  `json:"replied,omitempty"`
	CreatedAt       int64                 `json:"created_at"`
	IsMine          bool                  `json:"is_mine"`
	CanDelete       bool                  `json:"can_delete"`
	Replies         []legacyThreadMessage `json:"replies"`
}

type legacySentMessage struct {
	ID              string `json:"id"`
	TargetOpenID    string `json:"target_openid"`
	TargetNickname  string `json:"target_nickname"`
	TargetAvatar    string `json:"target_avatar"`
	Content         string `json:"content"`
	ParentID        string `json:"parent_id"`
	ReplyToID       string `json:"reply_to_id"`
	ReplyToNickname string `json:"reply_to_nickname"`
	ReplyToUID      string `json:"reply_to_uid"`
	CreatedAt       int64  `json:"created_at"`
}

type legacyReportedMessage struct {
	ID             string `json:"id"`
	ReportID       int64  `json:"report_id"`
	AuthorOpenID   string `json:"author_openid"`
	AuthorNickname string `json:"author_nickname"`
	AuthorRealName string `json:"author_realname"`
	AuthorUID      string `json:"author_uid"`
	AuthorAvatar   string `json:"author_avatar"`
	TargetOpenID   string `json:"target_openid"`
	TargetNickname string `json:"target_nickname"`
	TargetRealName string `json:"target_realname"`
	TargetUID      string `json:"target_uid"`
	Content        string `json:"content"`
	Reason         string `json:"reason"`
	Status         string `json:"status"`
	ReportTime     int64  `json:"report_time"`
	CreatedAt      int64  `json:"created_at"`
	HandledAt      *int64 `json:"handled_at"`
	HandlerName    string `json:"handler_name"`
}

type legacyNotificationJSON struct {
	ID         int64          `json:"id"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Text       string         `json:"text"`
	Time       string         `json:"time"`
	ActivityID string         `json:"activity_id"`
	Link       string         `json:"link"`
	ImageURL   string         `json:"image_url,omitempty"`
	ThumbURL   string         `json:"thumb_url,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Read       bool           `json:"read"`
}

func legacyMessageRef(openID, legacyID string, id int64) string {
	if strings.TrimSpace(openID) != "" {
		return openID
	}
	if strings.TrimSpace(legacyID) != "" {
		return legacyID
	}
	return strconv.FormatInt(id, 10)
}

func legacyOptionalID(id *int64) string {
	if id == nil {
		return ""
	}
	return strconv.FormatInt(*id, 10)
}

func legacyMessageAvatar(p db.SocialProfile, ok bool) string {
	if !ok || len(p.Photos) == 0 {
		return ""
	}
	return socialPhotoView(p.Photos[0]).ThumbURL
}

func (a *App) legacyMessageAvatars(ctx context.Context, msgs []db.Message) (map[int64]string, error) {
	ids := make([]int64, 0, len(msgs))
	seen := make(map[int64]struct{}, len(msgs))
	for _, m := range msgs {
		if _, ok := seen[m.AuthorID]; ok {
			continue
		}
		seen[m.AuthorID] = struct{}{}
		ids = append(ids, m.AuthorID)
	}
	profiles, err := a.db.GetSocialProfilesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	avatars := make(map[int64]string, len(profiles))
	for id, p := range profiles {
		avatars[id] = legacyMessageAvatar(p, true)
	}
	return avatars, nil
}

func (a *App) legacyMessageTargetAvatars(ctx context.Context, msgs []db.Message) (map[int64]string, error) {
	ids := make([]int64, 0, len(msgs))
	seen := make(map[int64]struct{}, len(msgs))
	for _, m := range msgs {
		if _, ok := seen[m.TargetUserID]; ok {
			continue
		}
		seen[m.TargetUserID] = struct{}{}
		ids = append(ids, m.TargetUserID)
	}
	profiles, err := a.db.GetSocialProfilesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	avatars := make(map[int64]string, len(profiles))
	for id, p := range profiles {
		avatars[id] = legacyMessageAvatar(p, true)
	}
	return avatars, nil
}

func legacyThreadMessages(msgs []db.Message, viewerID int64, admin bool, avatars map[int64]string) []legacyThreadMessage {
	out := make([]legacyThreadMessage, 0, len(msgs))
	index := make(map[string]int, len(msgs))
	for _, m := range msgs {
		item := legacyThreadMessage{
			ID:              strconv.FormatInt(m.ID, 10),
			AuthorOpenID:    legacyMessageRef(m.AuthorLegacyOpenID, m.AuthorLegacyID, m.AuthorID),
			AuthorNickname:  m.AuthorName,
			AuthorUID:       m.AuthorLegacyID,
			AuthorAvatar:    avatars[m.AuthorID],
			Content:         m.Content,
			ParentID:        legacyOptionalID(m.ParentID),
			ReplyToID:       legacyOptionalID(m.ReplyToID),
			ReplyToNickname: m.ReplyToName,
			ReplyToUID:      m.ReplyToLegacyID,
			CreatedAt:       m.CreatedAt.UnixMilli(),
			IsMine:          m.AuthorID == viewerID,
			CanDelete:       m.AuthorID == viewerID || m.TargetUserID == viewerID || admin,
			Replies:         []legacyThreadMessage{},
		}
		index[item.ID] = len(out)
		out = append(out, item)
	}
	// The v1 SPA renders a flat list and recomputes threads client-side, so the
	// top-level response stays flat. Replies are also attached to their root
	// for clients that consume the documented thread shape.
	roots := out
	for i := range out {
		parent := out[i].ParentID
		if parent == "" {
			continue
		}
		rootIdx, ok := index[parent]
		if !ok {
			continue
		}
		child := out[i]
		child.Replies = []legacyThreadMessage{}
		roots[rootIdx].Replies = append(roots[rootIdx].Replies, child)
	}
	return out
}

func legacySentMessages(msgs []db.Message, avatars map[int64]string) []legacySentMessage {
	out := make([]legacySentMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, legacySentMessage{
			ID:              strconv.FormatInt(m.ID, 10),
			TargetOpenID:    legacyMessageRef(m.TargetLegacyOpenID, m.TargetLegacyID, m.TargetUserID),
			TargetNickname:  m.TargetName,
			TargetAvatar:    avatars[m.TargetUserID],
			Content:         m.Content,
			ParentID:        legacyOptionalID(m.ParentID),
			ReplyToID:       legacyOptionalID(m.ReplyToID),
			ReplyToNickname: m.ReplyToName,
			ReplyToUID:      m.ReplyToLegacyID,
			CreatedAt:       m.CreatedAt.UnixMilli(),
		})
	}
	return out
}

func (a *App) legacyReceivedAvatars(ctx context.Context, msgs []db.ReceivedMessage) (map[int64]string, error) {
	ids := make([]int64, 0, len(msgs)*2)
	seen := make(map[int64]struct{}, len(msgs)*2)
	add := func(id int64) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, m := range msgs {
		add(m.AuthorID)
		add(m.TargetUserID)
	}
	profiles, err := a.db.GetSocialProfilesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	avatars := make(map[int64]string, len(profiles))
	for id, p := range profiles {
		avatars[id] = legacyMessageAvatar(p, true)
	}
	return avatars, nil
}

func legacyReceivedMessages(msgs []db.ReceivedMessage, viewerID int64, avatars map[int64]string) []legacyThreadMessage {
	out := make([]legacyThreadMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, legacyThreadMessage{
			ID:              strconv.FormatInt(m.ID, 10),
			AuthorOpenID:    legacyMessageRef(m.AuthorLegacyOpenID, m.AuthorLegacyID, m.AuthorID),
			AuthorNickname:  m.AuthorName,
			AuthorUID:       m.AuthorLegacyID,
			AuthorAvatar:    avatars[m.AuthorID],
			TargetOpenID:    legacyMessageRef(m.TargetLegacyOpenID, m.TargetLegacyID, m.TargetUserID),
			TargetNickname:  m.TargetName,
			TargetAvatar:    avatars[m.TargetUserID],
			OnMyCard:        m.TargetUserID == viewerID,
			Unread:          m.Unread,
			Replied:         m.Replied,
			Content:         m.Content,
			ParentID:        legacyOptionalID(m.ParentID),
			ReplyToID:       legacyOptionalID(m.ReplyToID),
			ReplyToNickname: m.ReplyToName,
			ReplyToUID:      m.ReplyToLegacyID,
			CreatedAt:       m.CreatedAt.UnixMilli(),
			IsMine:          m.AuthorID == viewerID,
			CanDelete:       m.AuthorID == viewerID || m.TargetUserID == viewerID,
			Replies:         []legacyThreadMessage{},
		})
	}
	return out
}

func (a *App) legacyReportProfiles(ctx context.Context, reports []db.Report) (map[int64]db.SocialProfile, error) {
	ids := make([]int64, 0, len(reports)*2)
	seen := make(map[int64]struct{}, len(reports)*2)
	add := func(id *int64) {
		if id == nil || *id <= 0 {
			return
		}
		if _, ok := seen[*id]; ok {
			return
		}
		seen[*id] = struct{}{}
		ids = append(ids, *id)
	}
	for _, rep := range reports {
		add(rep.MessageAuthorID)
		add(rep.TargetUserID)
	}
	return a.db.GetSocialProfilesByIDs(ctx, ids)
}

func legacyReportedMessages(reports []db.Report, profiles map[int64]db.SocialProfile) []legacyReportedMessage {
	out := make([]legacyReportedMessage, 0, len(reports))
	for _, rep := range reports {
		item := legacyReportedMessage{
			ReportID:       rep.ID,
			AuthorNickname: rep.MessageAuthorName,
			AuthorRealName: rep.MessageAuthorName,
			TargetNickname: rep.TargetName,
			TargetRealName: rep.TargetName,
			Content:        rep.MessageContent,
			Reason:         rep.Reason,
			Status:         rep.Status,
			ReportTime:     rep.CreatedAt.UnixMilli(),
			HandlerName:    rep.HandledByName,
		}
		if rep.TargetMessageID != nil {
			item.ID = strconv.FormatInt(*rep.TargetMessageID, 10)
		}
		if rep.MessageCreatedAt != nil {
			item.CreatedAt = rep.MessageCreatedAt.UnixMilli()
		}
		if rep.HandledAt != nil {
			ms := rep.HandledAt.UnixMilli()
			item.HandledAt = &ms
		}
		if rep.MessageAuthorID != nil {
			if p, ok := profiles[*rep.MessageAuthorID]; ok {
				item.AuthorOpenID = socialCardOpenID(p)
				item.AuthorUID = p.LegacyID
				item.AuthorAvatar = legacyMessageAvatar(p, true)
				if name := p.DisplayName(); name != "" {
					item.AuthorNickname = name
					item.AuthorRealName = name
				}
			}
		}
		if rep.TargetUserID != nil {
			if p, ok := profiles[*rep.TargetUserID]; ok {
				item.TargetOpenID = socialCardOpenID(p)
				item.TargetUID = p.LegacyID
				if name := p.DisplayName(); name != "" {
					item.TargetNickname = name
					item.TargetRealName = name
				}
			}
		}
		out = append(out, item)
	}
	return out
}

func (a *App) legacyPraiseStateMap(ctx context.Context, state db.PraiseState) (map[string]any, error) {
	praised := make([]string, 0, len(state.Praised))
	if len(state.Praised) > 0 {
		profiles, err := a.db.GetSocialProfilesByIDs(ctx, state.Praised)
		if err != nil {
			return nil, err
		}
		for _, id := range state.Praised {
			if p, ok := profiles[id]; ok {
				praised = append(praised, socialCardOpenID(p))
			}
		}
	}
	return map[string]any{
		"limit":         state.Limit,
		"left":          state.Left,
		"used":          state.Used,
		"praised":       praised,
		"received_week": state.ReceivedWeek,
	}, nil
}

func legacyNotifications(rows []db.Notification) []legacyNotificationJSON {
	out := make([]legacyNotificationJSON, 0, len(rows))
	for _, n := range rows {
		text := n.Title
		if n.Body != "" {
			text += " " + n.Body
		}
		out = append(out, legacyNotificationJSON{
			ID:         n.ID,
			Kind:       n.Kind,
			Title:      n.Title,
			Body:       n.Body,
			Text:       text,
			Time:       formatSocialTime(n.CreatedAt),
			ActivityID: legacyNotificationActivityID(n),
			Link:       n.Link,
			ImageURL:   legacyNotificationImageURL(n),
			ThumbURL:   legacyNotificationThumbURL(n),
			Payload:    n.Payload,
			Read:       n.ReadAt != nil,
		})
	}
	return out
}

func legacyNotificationImageURL(n db.Notification) string {
	if raw, ok := n.Payload["image_url"].(string); ok && strings.TrimSpace(raw) != "" {
		return raw
	}
	if raw, ok := n.Payload["media_id"].(string); ok && strings.TrimSpace(raw) != "" {
		return "/media/" + raw
	}
	return ""
}

func legacyNotificationThumbURL(n db.Notification) string {
	url := legacyNotificationImageURL(n)
	if url == "" {
		return ""
	}
	if strings.Contains(url, "?") {
		return url
	}
	return url + "?size=thumb"
}

func legacyNotificationActivityID(n db.Notification) string {
	if id := legacyPayloadActivityID(n.Payload["activity_id"]); id != "" {
		return id
	}
	return legacyActivityIDFromLink(n.Link)
}

func legacyPayloadActivityID(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v != float64(int64(v)) {
			return ""
		}
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func legacyActivityIDFromLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	path := parsed.Path
	for _, prefix := range []string{"/legacy/api/activities/", "/activities/", "/activity/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		id := strings.TrimPrefix(path, prefix)
		if i := strings.IndexByte(id, '/'); i >= 0 {
			id = id[:i]
		}
		if id == "" {
			return ""
		}
		if unescaped, err := url.PathUnescape(id); err == nil {
			return unescaped
		}
		return id
	}
	return ""
}
