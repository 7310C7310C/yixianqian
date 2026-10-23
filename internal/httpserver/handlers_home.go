package httpserver

import (
	"net/http"
	"net/url"
)

// publicFeishuAppURL 是 v1 飞书版入口；公开首页与登录页共用，避免两处漂移。
const publicFeishuAppURL = "https://applink.feishu.cn/T996FhMkTERp"

// handleHome serves the public landing page to anonymous visitors and the v1
// Vue SPA to signed-in users. The landing page stays free of account data; the
// SPA remains the only logged-in experience.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r); ok {
		a.serveLegacyApp(w, r)
		return
	}
	// /admin 的顶层 303 落在 /?admin=1；匿名时先登录并保留 next，登录后
	// 由 SPA 依据 admin=1 自动打开管理台 overlay。
	if r.URL.Query().Get("admin") == "1" {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/?admin=1"), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		a.trackPublicEvent(r, "page_view", "home")
	}
	a.render(w, r, http.StatusOK, "home", PageData{
		Title:   "祝你早日找到另一半",
		PageCSS: "register.css",
	})
}

// handleLegacyApp serves the same SPA at /app, which keeps deep links from the
// bot working while / remains the canonical entrypoint.
func (a *App) handleLegacyApp(w http.ResponseWriter, r *http.Request) {
	a.serveLegacyApp(w, r)
}

// homeFlash maps fixed query codes to messages. Arbitrary query text is never
// reflected into the page.
func homeFlash(r *http.Request) (notice, errMessage string) {
	q := r.URL.Query()
	if q.Get("activated") == "1" {
		notice = "账号已启用，欢迎使用。"
	}
	if q.Get("uploaded") == "1" {
		notice = "照片已上传。"
	}
	switch q.Get("upload_error") {
	case "":
	case "heic":
		errMessage = "HEIC/HEIF 格式尚未支持，请改用 JPEG 或 PNG。"
	case "too_large":
		errMessage = "文件超过大小上限。"
	case "unsupported":
		errMessage = "只支持 JPEG 与 PNG 图片。"
	case "dimensions":
		errMessage = "图片尺寸过大。"
	case "no_file":
		errMessage = "请选择要上传的文件。"
	case "csrf":
		errMessage = "表单已过期，请重新整理后再试。"
	default:
		errMessage = "上传失败，请稍后再试。"
	}
	return notice, errMessage
}
