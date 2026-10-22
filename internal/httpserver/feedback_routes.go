package httpserver

import (
	"net/http"
	"strings"
)

func init() { registerModule(registerFeedback) }

func registerFeedback(a *App, mux *http.ServeMux) {
	mux.HandleFunc("POST /feedback", a.handleFeedbackSubmit)
}

// handleFeedbackSubmit backs the legacy feedback sheet on cards and profile.
// It writes to the existing suggestions table consumed by the admin queue.
func (a *App) handleFeedbackSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUserHTML(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "表单格式错误", "请重新整理后再试。")
		return
	}
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请重新整理后再试。")
		return
	}
	kind := strings.TrimSpace(r.PostFormValue("kind"))
	if kind != "卡片反馈" {
		kind = "意见反馈"
	}
	reason := socialClipText(r.PostFormValue("reason"), 50)
	content := socialClipText(r.PostFormValue("content"), 1000)
	target := socialClipText(r.PostFormValue("target"), 50)
	if content == "" {
		content = "（未填写补充说明）"
	}
	if target != "" {
		content = "目标 " + target + "；" + reason + "：" + content
	} else {
		content = reason + "：" + content
	}
	if err := a.db.CreateSuggestion(r.Context(), user.ID, user.Name, kind, content); err != nil {
		a.logger(r).Error("create suggestion failed", "error", err, "user_id", user.ID)
		a.renderError(w, r, http.StatusInternalServerError, "暂时无法提交", "请稍后再试。")
		return
	}
	back := "/?feedback=1"
	if r.PostFormValue("from") == "cards" {
		back = cardsReturnURL(r, "feedback", "1")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
