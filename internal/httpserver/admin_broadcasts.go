package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func init() { registerModule(registerAdminBroadcastRoutes) }

func registerAdminBroadcastRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/broadcasts", a.adminHTML(adminPermContent, a.handleAdminBroadcastsPage))
	mux.HandleFunc("POST /admin/broadcasts", a.adminHTML(adminPermContent, a.handleAdminBroadcastSend))
}

type adminBroadcastView struct {
	ID             int64
	Title          string
	Body           string
	CreatorName    string
	RecipientCount int
	MediaID        string
	CreatedAt      string
}

type adminBroadcastPageData struct {
	Broadcasts []adminBroadcastView
	FormTitle  string
	FormBody   string
}

func (a *App) handleAdminBroadcastsPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.renderAdminBroadcasts(w, r, access, http.StatusOK, "", "", "")
}

func (a *App) renderAdminBroadcasts(w http.ResponseWriter, r *http.Request, access adminAccess,
	status int, message, formTitle, formBody string) {
	rows, err := a.db.ListBroadcasts(r.Context(), 50)
	if err != nil {
		a.logger(r).Error("list broadcasts failed", "error", err)
		message = "读取广播记录失败，请稍后再试。"
	}
	views := make([]adminBroadcastView, 0, len(rows))
	for _, b := range rows {
		views = append(views, adminBroadcastView{
			ID:             b.ID,
			Title:          b.Title,
			Body:           b.Body,
			CreatorName:    b.CreatorName,
			RecipientCount: b.RecipientCount,
			MediaID:        b.MediaID,
			CreatedAt:      b.CreatedAt.Local().Format("2006-01-02 15:04"),
		})
	}
	notice := ""
	if sent := strings.TrimSpace(r.URL.Query().Get("sent")); sent != "" {
		notice = "广播已发送给 " + sent + " 位用户。"
	}
	mediaMaxMB := int(a.cfg.MediaMaxBytes / (1 << 20))
	if mediaMaxMB < 1 {
		mediaMaxMB = 1
	}
	user := access.User
	a.render(w, r, status, "admin_broadcast", PageData{
		Title:      "全员消息",
		User:       &user,
		Error:      message,
		Notice:     notice,
		Data:       adminBroadcastPageData{Broadcasts: views, FormTitle: formTitle, FormBody: formBody},
		PageCSS:    "admin.css",
		ActiveNav:  "admin-broadcasts",
		MediaMaxMB: mediaMaxMB,
	})
}

func (a *App) handleAdminBroadcastSend(w http.ResponseWriter, r *http.Request, access adminAccess) {
	// Site-wide sends can carry one image; the shared media pipeline enforces
	// the same size/format/HEIC rules as profile uploads.
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest,
			"表单格式不正确，请刷新页面后重试。", "", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MediaMaxBytes+(2<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		a.renderAdminBroadcasts(w, r, access, http.StatusRequestEntityTooLarge,
			"表单过大或格式不正确，请压缩图片后重试。", "", "")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !a.formCSRFValid(r) {
		a.renderAdminBroadcasts(w, r, access, http.StatusForbidden, "表单已过期，请重新整理后再试。", "", "")
		return
	}
	title := strings.TrimSpace(r.PostFormValue("title"))
	body := strings.TrimSpace(r.PostFormValue("body"))
	if title == "" {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest, "请填写消息标题。", title, body)
		return
	}
	if utf8.RuneCountInString(title) > adminBroadcastTitleMax {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest, "标题最多 100 字。", title, body)
		return
	}
	if utf8.RuneCountInString(body) > adminBroadcastBodyMax {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest, "正文最多 2000 字。", title, body)
		return
	}
	if r.PostFormValue("confirm") != "1" {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest,
			"请勾选「确认发送给全部用户」后再提交。", title, body)
		return
	}

	mediaID := ""
	file, _, fileErr := r.FormFile("image")
	if fileErr == nil {
		_ = file.Close()
		if !a.acquireMedia(w, r, false) {
			return
		}
		defer a.releaseMedia()
		saved, err := a.receiveUploadField(r, access.User, "image")
		if err != nil {
			status, _, msg := uploadErrorDetails(err)
			a.renderAdminBroadcasts(w, r, access, status, msg, title, body)
			return
		}
		mediaID = saved.ID
	} else if !errors.Is(fileErr, http.ErrMissingFile) {
		a.renderAdminBroadcasts(w, r, access, http.StatusBadRequest, "读取图片失败，请重试。", title, body)
		return
	}

	broadcast, sent, err := a.db.CreateBroadcast(r.Context(), access.User.ID, title, body, mediaID)
	if err != nil {
		a.logger(r).Error("create broadcast failed", "error", err, "media_id", mediaID)
		if mediaID != "" {
			a.discardMedia(r, access.User.ID, mediaID)
		}
		a.renderAdminBroadcasts(w, r, access, http.StatusInternalServerError,
			"广播发送失败，未写入任何通知；请稍后重试。", title, body)
		return
	}
	a.audit(r, &access.User.ID, "admin.broadcast.send", "broadcast",
		strconv.FormatInt(broadcast.ID, 10), map[string]any{
			"title":           title,
			"recipient_count": sent,
			"media_id":        mediaID,
			"audience":        "all_active",
		})
	http.Redirect(w, r, "/admin/broadcasts?sent="+strconv.Itoa(sent), http.StatusSeeOther)
}
