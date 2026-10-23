package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// The reported-messages page in the bundled v1 SPA posts
// {action: "delete"|"approve"} to /legacy/api/messages/{id}/handle. The list
// endpoint already existed; this route completes the write path with the same
// RBAC, CSRF and audit rules as the SSR console.
func init() { registerModule(registerLegacyReportHandle) }

func registerLegacyReportHandle(a *App, mux *http.ServeMux) {
	mux.HandleFunc("POST /legacy/api/messages/{id}/handle",
		a.legacyAdminAPI([]string{adminPermContent}, a.handleLegacyMessageHandleReport))
}

func (a *App) handleLegacyMessageHandleReport(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if !a.legacyWriteCSRF(w, r) {
		return
	}
	messageID, ok := parsePathID(r)
	if !ok {
		legacyError(w, http.StatusBadRequest, "留言编号不正确")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageJSONBytes)
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		legacyError(w, http.StatusBadRequest, "请求内容格式不正确")
		return
	}
	deleteMessage := false
	switch strings.TrimSpace(payload.Action) {
	case "delete":
		deleteMessage = true
	case "approve", "reject", "restore":
		deleteMessage = false
	default:
		legacyError(w, http.StatusBadRequest, "action 需为 approve 或 delete")
		return
	}
	reportID, err := a.db.HandleReportForMessage(r.Context(), messageID, access.User.ID, deleteMessage)
	if errors.Is(err, db.ErrNotFound) {
		legacyError(w, http.StatusNotFound, "找不到待处理的举报")
		return
	}
	if err != nil {
		legacyServerError(a, w, r, err, "legacy handle report failed")
		return
	}
	a.audit(r, &access.User.ID, "message.report.handle", "message",
		strconv.FormatInt(messageID, 10), map[string]any{
			"report_id": reportID,
			"deleted":   deleteMessage,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"report_id": reportID,
		"deleted":   deleteMessage,
	})
}
