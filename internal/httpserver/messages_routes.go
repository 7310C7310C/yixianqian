package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

const (
	maxMessageJSONBytes = 64 << 10
	contentModeratePerm = "content.moderate"
)

var (
	messageCreateLimiter = auth.NewLimiter(20, time.Minute)
	messageReportLimiter = auth.NewLimiter(5, time.Minute)
)

func init() { registerModule(registerMessageRoutes) }

func registerMessageRoutes(a *App, mux *http.ServeMux) {

}

func messageViewBlock(status string) (string, bool) {
	switch status {
	case "pending":
		return "资料审核中，通过后即可使用一线牵 App。", true
	case "rejected":
		return "很抱歉，你的资料未通过审核，如有疑问请联系管理员。", true
	case "left":
		return "你已暂时退出相亲市场，如需恢复请联系管理员。", true
	case "disabled":
		return "账号已停用，请联系管理员。", true
	default:
		return "", false
	}
}

func messagePostBlock(status string) (string, bool) {
	if status == "matched" {
		return "你目前处于已脱单状态，不能留言；请先在「我的」页恢复单身。", true
	}
	return messageViewBlock(status)
}

func (a *App) requireMessageViewHTML(w http.ResponseWriter, r *http.Request, user db.User) (string, bool) {
	status, err := a.db.GetAccountStatus(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusForbidden, "账号状态异常", "找不到账号状态，请联系管理员。")
		return "", false
	}
	if err != nil {
		a.logger(r).Error("load account status failed", "error", err, "user_id", user.ID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return "", false
	}
	if message, blocked := messageViewBlock(status); blocked {
		a.renderError(w, r, http.StatusForbidden, "账号限制", message)
		return "", false
	}
	return status, true
}

func (a *App) requireMessageViewAPI(w http.ResponseWriter, r *http.Request, user db.User) (string, bool) {
	status, err := a.db.GetAccountStatus(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusForbidden, "account_not_found", "找不到账号状态。")
		return "", false
	}
	if err != nil {
		a.logger(r).Error("load account status failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return "", false
	}
	if message, blocked := messageViewBlock(status); blocked {
		writeAPIError(w, http.StatusForbidden, "account_gated", message)
		return "", false
	}
	return status, true
}

func (a *App) requireMessagePostHTML(w http.ResponseWriter, r *http.Request, user db.User) (string, bool) {
	status, ok := a.requireMessageViewHTML(w, r, user)
	if !ok {
		return "", false
	}
	if message, blocked := messagePostBlock(status); blocked {
		a.renderError(w, r, http.StatusForbidden, "无法留言", message)
		return "", false
	}
	return status, true
}

func (a *App) requireMessagePostAPI(w http.ResponseWriter, r *http.Request, user db.User) (string, bool) {
	status, ok := a.requireMessageViewAPI(w, r, user)
	if !ok {
		return "", false
	}
	if message, blocked := messagePostBlock(status); blocked {
		writeAPIError(w, http.StatusForbidden, "account_gated", message)
		return "", false
	}
	return status, true
}

func (a *App) isModerator(ctx context.Context, user db.User) bool {
	ok, err := a.db.UserHasAdminPermission(ctx, user.ID, contentModeratePerm)
	if err != nil {
		a.log.Error("check moderator permission failed", "error", err, "user_id", user.ID)
		return false
	}
	if ok {
		return true
	}
	return false
}

func (a *App) canViewMessageThread(ctx context.Context, viewer, target db.User, admin bool) (bool, error) {
	if viewer.ID == target.ID || admin {
		return true, nil
	}
	return a.db.HasAuthoredMessage(ctx, viewer.ID, target.ID)
}

func takeRateLimit(limiter *auth.Limiter, key string) (bool, int) {
	ok, retry := limiter.Allow(key)
	return ok, int(retry.Seconds()) + 1
}

// handleMessageDelete is the JSON DELETE endpoint for non-JS clients using a
// header CSRF token. HTML forms use POST /messages/{id}/delete instead.
// --- JSON API ---------------------------------------------------------------

type createMessagePayload struct {
	Target       string          `json:"target"`
	TargetOpenID string          `json:"target_openid"`
	Content      string          `json:"content"`
	ParentID     json.RawMessage `json:"parent_id"`
	ReplyToID    json.RawMessage `json:"reply_to_id"`
}

type messageJSON struct {
	ID                 int64  `json:"id"`
	TargetUserID       int64  `json:"target_user_id,omitempty"`
	TargetOpenID       string `json:"target_openid,omitempty"`
	TargetNickname     string `json:"target_nickname,omitempty"`
	TargetAvatar       string `json:"target_avatar"`
	AuthorUserID       int64  `json:"author_user_id,omitempty"`
	AuthorOpenID       string `json:"author_openid,omitempty"`
	AuthorNickname     string `json:"author_nickname,omitempty"`
	AuthorUID          string `json:"author_uid,omitempty"`
	AuthorAvatar       string `json:"author_avatar"`
	Content            string `json:"content"`
	ParentID           *int64 `json:"parent_id"`
	ReplyToID          *int64 `json:"reply_to_id"`
	ReplyToNickname    string `json:"reply_to_nickname"`
	ReplyToUID         string `json:"reply_to_uid,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	CreatedAtFormatted string `json:"created_at_text"`
	IsMine             bool   `json:"is_mine"`
	CanDelete          bool   `json:"can_delete"`
	Status             string `json:"status,omitempty"`
}

func messageJSONFor(m db.Message, viewer db.User, admin bool) messageJSON {
	return messageJSON{
		ID:                 m.ID,
		TargetUserID:       m.TargetUserID,
		TargetOpenID:       firstNonEmpty(m.TargetLegacyOpenID, m.TargetLegacyID),
		TargetNickname:     m.TargetName,
		AuthorUserID:       m.AuthorID,
		AuthorOpenID:       firstNonEmpty(m.AuthorLegacyOpenID, m.AuthorLegacyID),
		AuthorNickname:     m.AuthorName,
		AuthorUID:          m.AuthorLegacyID,
		Content:            m.Content,
		ParentID:           m.ParentID,
		ReplyToID:          m.ReplyToID,
		ReplyToNickname:    m.ReplyToName,
		ReplyToUID:         m.ReplyToLegacyID,
		CreatedAt:          m.CreatedAt.UnixMilli(),
		CreatedAtFormatted: m.CreatedAt.UTC().Format(time.RFC3339),
		IsMine:             m.AuthorID == viewer.ID,
		CanDelete:          m.AuthorID == viewer.ID || m.TargetUserID == viewer.ID || admin,
		Status:             m.Status,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (a *App) apiListMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("target"))
	if ref == "" {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []messageJSON{}})
		return
	}
	target, err := a.db.FindUserByRef(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusBadRequest, "target_not_found", "目标用户不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("resolve message target failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	admin := a.isModerator(r.Context(), user)
	allowed, err := a.canViewMessageThread(r.Context(), user, target, admin)
	if err != nil {
		a.logger(r).Error("check thread access failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	if !allowed {
		writeAPIError(w, http.StatusForbidden, "forbidden", "这个留言串仅限卡片主人与留言者查看。")
		return
	}
	msgs, err := a.db.ListMessages(r.Context(), target.ID)
	if err != nil {
		a.logger(r).Error("list messages failed", "error", err, "target_user_id", target.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]messageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageJSONFor(m, user, admin))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func (a *App) apiCreateMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if _, ok := a.requireMessagePostAPI(w, r, user); !ok {
		return
	}
	if ok, retry := takeRateLimit(messageCreateLimiter, "message:create:"+strconv.FormatInt(user.ID, 10)); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(retry))
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "每分钟最多留言 20 次，请稍后再试。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
	var payload createMessagePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return
	}
	ref := strings.TrimSpace(firstNonEmpty(payload.Target, payload.TargetOpenID))
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_target", "缺少目标用户。")
		return
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		writeAPIError(w, http.StatusBadRequest, "empty_content", "内容不能为空。")
		return
	}
	if utf8.RuneCountInString(content) > db.MessageMaxRunes {
		writeAPIError(w, http.StatusBadRequest, "content_too_long", "留言最多 500 字。")
		return
	}
	parentID, err := parseOptionalIDJSON(payload.ParentID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_parent", "回复的留言格式不正确。")
		return
	}
	replyToID, err := parseOptionalIDJSON(payload.ReplyToID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_reply_to", "回复的留言格式不正确。")
		return
	}
	target, err := a.db.FindUserByRef(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusBadRequest, "target_not_found", "目标用户不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("resolve message target failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	msg, err := a.db.CreateMessage(r.Context(), db.CreateMessageParams{
		TargetUserID: target.ID,
		AuthorID:     user.ID,
		Content:      content,
		ParentID:     parentID,
		ReplyToID:    replyToID,
	})
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusBadRequest, "invalid_thread", "回复对象不存在或已删除。")
		return
	}
	if err != nil {
		a.logger(r).Error("create message failed", "error", err, "user_id", user.ID, "target_user_id", target.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法送出留言。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": msg.ID})
}

func (a *App) apiDeleteMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_message_id", "留言编号不正确。")
		return
	}
	msg, err := a.db.GetMessageByID(r.Context(), messageID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && msg.Status == db.MessageStatusDeleted) {
		writeAPIError(w, http.StatusNotFound, "message_not_found", "留言不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("load message failed", "error", err, "message_id", messageID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	admin := a.isModerator(r.Context(), user)
	if msg.AuthorID != user.ID && msg.TargetUserID != user.ID && !admin {
		writeAPIError(w, http.StatusForbidden, "forbidden", "只有留言作者、卡片主人或管理员可以删除。")
		return
	}
	if _, err := a.db.DeleteMessage(r.Context(), messageID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "message_not_found", "留言不存在。")
			return
		}
		a.logger(r).Error("delete message failed", "error", err, "message_id", messageID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	a.audit(r, &user.ID, "message.delete", "message", strconv.FormatInt(messageID, 10), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type reportPayload struct {
	Reason string `json:"reason"`
}

func (a *App) apiReportMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_message_id", "留言编号不正确。")
		return
	}
	if ok, retry := takeRateLimit(messageReportLimiter, "message:report:"+strconv.FormatInt(user.ID, 10)); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(retry))
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "每分钟最多举报 5 次，请稍后再试。")
		return
	}
	var payload reportPayload
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
			return
		}
	}
	reason := strings.TrimSpace(payload.Reason)
	if utf8.RuneCountInString(reason) > db.MessageMaxRunes {
		writeAPIError(w, http.StatusBadRequest, "reason_too_long", "举报原因最多 500 字。")
		return
	}
	msg, err := a.db.GetMessageByID(r.Context(), messageID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && msg.Status == db.MessageStatusDeleted) {
		writeAPIError(w, http.StatusNotFound, "message_not_found", "留言不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("load reported message failed", "error", err, "message_id", messageID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	reportID, already, err := a.db.CreateMessageReport(r.Context(), db.ReportMessageParams{
		MessageID:  messageID,
		ReporterID: user.ID,
		Reason:     reason,
	})
	switch {
	case errors.Is(err, db.ErrMessageDeleted):
		writeAPIError(w, http.StatusBadRequest, "message_deleted", "留言已删除，无法举报。")
		return
	case errors.Is(err, db.ErrCannotReportSelf):
		writeAPIError(w, http.StatusBadRequest, "cannot_report_self", "你不能举报自己的留言。")
		return
	case errors.Is(err, db.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "message_not_found", "留言不存在。")
		return
	case err != nil:
		a.logger(r).Error("report message failed", "error", err, "message_id", messageID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	if !already {
		a.audit(r, &user.ID, "message.report", "message", strconv.FormatInt(messageID, 10),
			map[string]any{"report_id": reportID})
		a.notifyMessageReport(r.Context(), user, msg, reason, reportID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "already": already})
}

func (a *App) apiMyMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	msgs, err := a.db.ListMessagesByAuthor(r.Context(), user.ID, 200)
	if err != nil {
		a.logger(r).Error("list my messages failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	admin := a.isModerator(r.Context(), user)
	out := make([]messageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageJSONFor(m, user, admin))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func (a *App) apiReceivedMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	msgs, err := a.db.ListMessagesReceived(r.Context(), user.ID, 200)
	if err != nil {
		a.logger(r).Error("list received messages failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	admin := a.isModerator(r.Context(), user)
	out := make([]messageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageJSONFor(m.Message, user, admin))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

type reportJSON struct {
	ID             int64  `json:"id"` // legacy clients handle by message id
	ReportID       int64  `json:"report_id"`
	MessageID      *int64 `json:"message_id"`
	AuthorOpenID   string `json:"author_openid"`
	AuthorNickname string `json:"author_nickname"`
	AuthorRealName string `json:"author_realname"`
	AuthorUID      string `json:"author_uid"`
	TargetOpenID   string `json:"target_openid"`
	TargetNickname string `json:"target_nickname"`
	TargetRealName string `json:"target_realname"`
	Content        string `json:"content"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	ReportTime     int64  `json:"report_time"`
	CreatedAt      int64  `json:"created_at"`
	HandledAt      *int64 `json:"handled_at"`
	Handler        *int64 `json:"handler"`
	HandlerName    string `json:"handler_name"`
}

func (a *App) apiReportedMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.isModerator(r.Context(), user) {
		writeAPIError(w, http.StatusForbidden, "forbidden", "需要内容管理权限。")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	reports, err := a.db.ListReports(r.Context(), status, 200)
	if err != nil {
		a.logger(r).Error("list reports failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]reportJSON, 0, len(reports))
	for _, rep := range reports {
		item := reportJSON{
			ReportID:    rep.ID,
			MessageID:   rep.TargetMessageID,
			Content:     rep.MessageContent,
			Status:      rep.Status,
			Reason:      rep.Reason,
			ReportTime:  rep.CreatedAt.UnixMilli(),
			Handler:     rep.HandledBy,
			HandlerName: rep.HandledByName,
		}
		if rep.TargetMessageID != nil {
			item.ID = *rep.TargetMessageID
		}
		if rep.MessageAuthorID != nil {
			item.AuthorOpenID = strconv.FormatInt(*rep.MessageAuthorID, 10)
		}
		item.AuthorNickname = rep.MessageAuthorName
		item.AuthorRealName = rep.MessageAuthorName
		item.TargetOpenID = ""
		if rep.TargetUserID != nil {
			item.TargetOpenID = strconv.FormatInt(*rep.TargetUserID, 10)
		}
		item.TargetNickname = rep.TargetName
		item.TargetRealName = rep.TargetName
		if rep.MessageCreatedAt != nil {
			item.CreatedAt = rep.MessageCreatedAt.UnixMilli()
		}
		if rep.HandledAt != nil {
			ms := rep.HandledAt.UnixMilli()
			item.HandledAt = &ms
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func (a *App) apiHandleReport(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if !a.isModerator(r.Context(), user) {
		writeAPIError(w, http.StatusForbidden, "forbidden", "需要内容管理权限。")
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_message_id", "留言编号不正确。")
		return
	}
	var payload struct {
		Action string `json:"action"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确。")
		return
	}
	deleteMessage := false
	switch strings.TrimSpace(payload.Action) {
	case "delete":
		deleteMessage = true
	case "approve", "reject", "restore":
		deleteMessage = false
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_action", "action 需为 approve 或 delete。")
		return
	}
	reportID, err := a.db.HandleReportForMessage(r.Context(), messageID, user.ID, deleteMessage)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "report_not_found", "找不到待处理的举报。")
		return
	}
	if err != nil {
		a.logger(r).Error("handle report failed", "error", err, "message_id", messageID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	a.audit(r, &user.ID, "message.report.handle", "message", strconv.FormatInt(messageID, 10),
		map[string]any{"report_id": reportID, "deleted": deleteMessage})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "report_id": reportID})
}

type notificationJSON struct {
	ID        int64          `json:"id"`
	Recipient int64          `json:"recipient"`
	Kind      string         `json:"kind"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Text      string         `json:"text"`
	Body      string         `json:"body"`
	Link      string         `json:"link"`
	Key       string         `json:"key,omitempty"`
	Payload   map[string]any `json:"payload"`
	Read      bool           `json:"read"`
	ReadAt    *time.Time     `json:"read_at"`
	CreatedAt time.Time      `json:"created_at"`
	Time      string         `json:"time"`
}

func notificationJSONFor(n db.Notification) notificationJSON {
	text := n.Title
	if n.Body != "" {
		text += " " + n.Body
	}
	key, _ := n.Payload["dedupe_key"].(string)
	return notificationJSON{
		ID:        n.ID,
		Recipient: n.UserID,
		Kind:      n.Kind,
		Type:      n.Kind,
		Title:     n.Title,
		Text:      text,
		Body:      n.Body,
		Link:      n.Link,
		Key:       key,
		Payload:   n.Payload,
		Read:      n.ReadAt != nil,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
		Time:      n.CreatedAt.Local().Format("2006-01-02 15:04:05"),
	}
}

func (a *App) apiListNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	rows, err := a.db.ListNotifications(r.Context(), user.ID, 100)
	if err != nil {
		a.logger(r).Error("list notifications failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	unread, err := a.db.UnreadNotificationCount(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("count unread notifications failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	out := make([]notificationJSON, 0, len(rows))
	for _, n := range rows {
		out = append(out, notificationJSONFor(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "unread": unread})
}

func (a *App) apiNotificationRead(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	id, ok := parsePathID(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_notification_id", "通知编号不正确。")
		return
	}
	found, err := a.db.MarkNotificationRead(r.Context(), user.ID, id)
	if err != nil {
		a.logger(r).Error("mark notification read failed", "error", err, "notification_id", id)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "notification_not_found", "通知不存在。")
		return
	}
	unread, err := a.db.UnreadNotificationCount(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("count unread notifications failed", "error", err, "user_id", user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unread": unread})
}

func (a *App) apiNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return
	}
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return
	}
	if _, ok := a.requireMessageViewAPI(w, r, user); !ok {
		return
	}
	updated, err := a.db.MarkAllNotificationsRead(r.Context(), user.ID)
	if err != nil {
		a.logger(r).Error("mark all notifications read failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "系统暂时无法处理。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": updated})
}

func (a *App) notifyMessageReport(ctx context.Context, reporter db.User, msg db.Message, reason string, reportID int64) {
	ids, err := a.db.ListAdminUserIDs(ctx, contentModeratePerm)
	if err != nil {
		a.log.Error("list moderators for report notification failed", "error", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	reporterName, err := a.db.DisplayName(ctx, reporter.ID)
	if err != nil || reporterName == "" {
		reporterName = reporter.Name
	}
	content := truncateRunes(msg.Content, 200)
	body := fmt.Sprintf("举报人：%s；被举报人：%s；卡片主人：%s；内容：%s",
		reporterName, msg.AuthorName, msg.TargetName, content)
	if reason != "" {
		body += "；原因：" + truncateRunes(reason, 200)
	}
	link := "/messages?target=" + strconv.FormatInt(msg.TargetUserID, 10)
	for _, adminID := range ids {
		if _, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
			UserID:    adminID,
			Kind:      "message_report",
			Title:     "收到留言举报",
			Body:      body,
			Link:      link,
			DedupeKey: fmt.Sprintf("message-report:%d:%d:%d", msg.ID, reportID, adminID),
		}); err != nil {
			a.log.Error("create report notification failed", "error", err, "admin_id", adminID, "message_id", msg.ID)
		}
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func parsePathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseOptionalIDString(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("invalid id")
	}
	return &id, nil
}

func parseOptionalIDJSON(raw json.RawMessage) (*int64, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return parseOptionalIDString(asString)
	}
	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		id, err := strconv.ParseInt(asNumber.String(), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid id")
		}
		return &id, nil
	}
	return nil, fmt.Errorf("invalid id")
}
