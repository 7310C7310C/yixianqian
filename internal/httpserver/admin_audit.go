package httpserver

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const (
	adminPermAudit        = "audit.view"
	adminPermPointsAdjust = "points.adjust"
	adminAuditPageSize    = 100
	adminAuditExportBatch = 1000
	adminAuditExportMax   = 10000
	adminAuditMetaRunes   = 200
)

// The console renders audit time in China Standard Time. Date filters use the
// same fixed zone so they do not depend on the host's tz database.
var adminAuditLocation = time.FixedZone("CST", 8*60*60)

func init() { registerModule(registerAdminAuditRoutes) }

func registerAdminAuditRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/audit", a.adminHTML(adminPermAudit, a.handleAdminAuditPage))
	mux.HandleFunc("GET /admin/audit.csv", a.adminHTML(adminPermAudit, a.handleAdminAuditCSV))
}

type adminAuditFilterForm struct {
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	From       string
	To         string
}

type adminAuditRow struct {
	ID            int64
	CreatedAt     string
	ActorName     string
	ActorLegacyID string
	ActorURL      string
	Action        string
	TargetType    string
	TargetID      string
	TargetURL     string
	IP            string
	Metadata      string
}

type adminAuditPageData struct {
	Rows      []adminAuditRow
	Total     int64
	Page      int
	PageCount int
	PrevURL   string
	NextURL   string
	ExportURL string
	Filter    adminAuditFilterForm
	LoadError string
}

func (a *App) handleAdminAuditPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	filter, form, warnings := parseAdminAuditFilter(r)
	page := adminAuditPageNumber(r.URL.Query().Get("page"))

	var (
		total     int64
		countErr  error
		pageCount int
	)
	total, countErr = a.db.CountAdminAudit(r.Context(), filter)
	if countErr != nil {
		a.logger(r).Error("count admin audit failed", "error", countErr)
		warnings = append(warnings, "暂时无法统计审计总数。")
	} else {
		pageCount = int((total + adminAuditPageSize - 1) / adminAuditPageSize)
		if pageCount > 0 && page > pageCount {
			page = pageCount
		}
	}

	filter.Limit = adminAuditPageSize
	filter.Offset = (page - 1) * adminAuditPageSize
	entries, err := a.db.ListAdminAudit(r.Context(), filter)
	data := adminAuditPageData{
		Rows:      make([]adminAuditRow, 0, len(entries)),
		Total:     total,
		Page:      page,
		PageCount: pageCount,
		Filter:    form,
		ExportURL: adminAuditExportURL(form),
	}
	if err != nil {
		a.logger(r).Error("list admin audit failed", "error", err)
		data.LoadError = "审计日志暂时无法读取，请稍后再试。"
	} else {
		for _, entry := range entries {
			data.Rows = append(data.Rows, newAdminAuditRow(entry))
		}
		if countErr != nil && len(entries) == adminAuditPageSize {
			data.PageCount = page + 1
		}
		data.PageCount = maxInt(data.PageCount, page)
	}
	if data.Page > 1 {
		data.PrevURL = adminAuditPageURL(form, data.Page-1)
	}
	if data.Page < data.PageCount {
		data.NextURL = adminAuditPageURL(form, data.Page+1)
	}

	a.render(w, r, http.StatusOK, "admin_audit", PageData{
		Title:     "审计日志",
		User:      &access.User,
		Data:      data,
		Notice:    strings.Join(warnings, " "),
		PageCSS:   "admin.css",
		ActiveNav: "audit",
	})
}

func (a *App) handleAdminAuditCSV(w http.ResponseWriter, r *http.Request, access adminAccess) {
	filter, _, _ := parseAdminAuditFilter(r)
	filter.Limit = adminAuditExportBatch

	entries := make([]db.AdminAuditEntry, 0, adminAuditPageSize)
	for len(entries) < adminAuditExportMax {
		filter.Offset = len(entries)
		batch, err := a.db.ListAdminAudit(r.Context(), filter)
		if err != nil {
			a.logger(r).Error("export admin audit failed", "error", err)
			http.Error(w, "审计日志暂时无法读取", http.StatusInternalServerError)
			return
		}
		if len(batch) == 0 {
			break
		}
		remaining := adminAuditExportMax - len(entries)
		if len(batch) > remaining {
			batch = batch[:remaining]
		}
		entries = append(entries, batch...)
		if len(batch) < filter.Limit {
			break
		}
	}

	now := a.clock.Now().In(adminAuditLocation)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="audit-%s.csv"`, now.Format("20060102-150405")))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// BOM keeps Excel from misreading UTF-8 Chinese text.
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return
	}

	cw := csv.NewWriter(w)
	header := []string{
		"id", "created_at", "actor_user_id", "actor_legacy_id", "actor_name",
		"action", "target_type", "target_id", "ip", "metadata",
	}
	if err := cw.Write(header); err != nil {
		a.logger(r).Debug("write audit csv header failed", "error", err)
		return
	}
	for _, entry := range entries {
		actorUserID := ""
		if entry.ActorUserID != nil {
			actorUserID = strconv.FormatInt(*entry.ActorUserID, 10)
		}
		record := []string{
			strconv.FormatInt(entry.ID, 10),
			entry.CreatedAt.In(adminAuditLocation).Format("2006-01-02 15:04:05"),
			actorUserID,
			entry.ActorLegacyID,
			entry.ActorName,
			entry.Action,
			entry.TargetType,
			entry.TargetID,
			entry.IP,
			adminAuditMetadataJSON(entry.Metadata),
		}
		if err := cw.Write(record); err != nil {
			a.logger(r).Debug("write audit csv row failed", "error", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		a.logger(r).Debug("flush audit csv failed", "error", err)
	}
}

func adminAuditPageNumber(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page <= 0 {
		return 1
	}
	return page
}

func parseAdminAuditFilter(r *http.Request) (db.AdminAuditFilter, adminAuditFilterForm, []string) {
	q := r.URL.Query()
	form := adminAuditFilterForm{
		Actor:      strings.TrimSpace(q.Get("actor")),
		Action:     strings.TrimSpace(q.Get("action")),
		TargetType: strings.TrimSpace(q.Get("target_type")),
		TargetID:   strings.TrimSpace(q.Get("target_id")),
		From:       strings.TrimSpace(q.Get("from")),
		To:         strings.TrimSpace(q.Get("to")),
	}
	filter := db.AdminAuditFilter{
		Actor:      form.Actor,
		Action:     form.Action,
		TargetType: form.TargetType,
		TargetID:   form.TargetID,
	}
	var warnings []string
	if form.From != "" {
		if from, err := time.ParseInLocation("2006-01-02", form.From, adminAuditLocation); err == nil {
			filter.From = &from
		} else {
			warnings = append(warnings, "开始日期格式不正确，已忽略（格式：YYYY-MM-DD）。")
		}
	}
	if form.To != "" {
		if to, err := time.ParseInLocation("2006-01-02", form.To, adminAuditLocation); err == nil {
			end := to.AddDate(0, 0, 1)
			filter.To = &end
		} else {
			warnings = append(warnings, "结束日期格式不正确，已忽略（格式：YYYY-MM-DD）。")
		}
	}
	return filter, form, warnings
}

func adminAuditFilterValues(form adminAuditFilterForm) url.Values {
	values := url.Values{}
	for key, value := range map[string]string{
		"actor":       form.Actor,
		"action":      form.Action,
		"target_type": form.TargetType,
		"target_id":   form.TargetID,
		"from":        form.From,
		"to":          form.To,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	return values
}

func adminAuditPageURL(form adminAuditFilterForm, page int) string {
	values := adminAuditFilterValues(form)
	values.Set("page", strconv.Itoa(page))
	return "/admin/audit?" + values.Encode()
}

func adminAuditExportURL(form adminAuditFilterForm) string {
	values := adminAuditFilterValues(form)
	if query := values.Encode(); query != "" {
		return "/admin/audit.csv?" + query
	}
	return "/admin/audit.csv"
}

func newAdminAuditRow(entry db.AdminAuditEntry) adminAuditRow {
	row := adminAuditRow{
		ID:            entry.ID,
		CreatedAt:     entry.CreatedAt.In(adminAuditLocation).Format("2006-01-02 15:04:05"),
		ActorName:     entry.ActorName,
		ActorLegacyID: entry.ActorLegacyID,
		Action:        entry.Action,
		TargetType:    entry.TargetType,
		TargetID:      entry.TargetID,
		IP:            entry.IP,
		Metadata:      adminAuditMetadataSummary(entry.Metadata),
	}
	if entry.ActorUserID != nil && *entry.ActorUserID > 0 {
		row.ActorURL = "/admin/users/" + strconv.FormatInt(*entry.ActorUserID, 10)
	}
	if entry.TargetType == "user" {
		if targetID, err := strconv.ParseInt(entry.TargetID, 10, 64); err == nil && targetID > 0 {
			row.TargetURL = "/admin/users/" + strconv.FormatInt(targetID, 10)
		}
	}
	return row
}

// adminAuditSensitiveKeyRE marks metadata keys whose values must never leave
// the server: passwords, tokens, cookies, keys and storage object paths.
var adminAuditSensitiveKeyRE = regexp.MustCompile(
	`password|passwd|secret|token|cookie|authorization|session|csrf|api_key|private_key|object_key|thumb_key|storage_key|r2_key`)

// sanitizeAdminAuditMetadata recursively replaces sensitive values. DB writes
// should already avoid secrets; this is the last line of defence for display
// and export.
func sanitizeAdminAuditMetadata(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if adminAuditSensitiveKeyRE.MatchString(strings.ToLower(key)) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = sanitizeAdminAuditMetadata(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeAdminAuditMetadata(item)
		}
		return out
	case []string:
		out := make([]string, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if adminAuditSensitiveKeyRE.MatchString(strings.ToLower(key)) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = item
		}
		return out
	default:
		return value
	}
}

func adminAuditMetadataJSON(metadata map[string]any) string {
	cleaned := sanitizeAdminAuditMetadata(metadata)
	raw, err := json.Marshal(cleaned)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func adminAuditMetadataSummary(metadata map[string]any) string {
	jsonText := adminAuditMetadataJSON(metadata)
	if jsonText == "{}" || jsonText == "null" {
		return ""
	}
	return truncateAdminAuditRunes(jsonText, adminAuditMetaRunes)
}

func truncateAdminAuditRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
