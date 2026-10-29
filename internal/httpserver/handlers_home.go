package httpserver

import (
	"net/http"
	"net/url"
)

// publicFeishuAppURL 是 v1 飞书版入口；公开首页与登录弹框共用，避免两处漂移。
// 公开首页只保留该入口链接，不再展示二维码图片。
const publicFeishuAppURL = "https://applink.feishu.cn/T996FhMkTERp"

// publicHomeTitle is shared by the anonymous home render and login-error
// re-renders so the two paths cannot drift.
const publicHomeTitle = "一线牵 · 祝你早日找到另一半"

// handleHome serves the public landing page to anonymous visitors and the v1
// Vue SPA to signed-in users. The landing page stays free of account data; the
// SPA remains the only logged-in experience. The old standalone /login page was
// merged into a dialog on this page; /login now 303s here with ?login=1.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r); ok {
		a.serveLegacyApp(w, r)
		return
	}
	q := r.URL.Query()
	// /admin 的顶层 303 落在 /?admin=1；匿名时先登录并保留 next，登录后
	// 由 SPA 依据 admin=1 自动打开管理台 overlay。
	if q.Get("admin") == "1" {
		http.Redirect(w, r, "/?login=1&next="+url.QueryEscape("/?admin=1"), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		a.trackPublicEvent(r, "page_view", "home")
	}
	data := PageData{
		Title:         publicHomeTitle,
		PageCSS:       "register.css",
		PublicLanding: true,
	}
	if q.Get("login") == "1" || q.Get("logged_out") != "" || q.Get("activated") != "" ||
		q.Get("error") != "" {
		data.LoginOpen = true
		data.LoginNotice = homeLoginNotice(q)
		data.LoginError = homeLoginError(q.Get("error"))
		data.Next = homeNext(q.Get("next"))
	}
	a.render(w, r, http.StatusOK, "home", data)
}

// homeLoginNotice maps fixed query flags to home.js notice keys. Arbitrary
// query values are never reflected into the page.
func homeLoginNotice(q url.Values) string {
	switch {
	case q.Get("logged_out") == "1":
		return "logged_out"
	case q.Get("activated") == "1":
		return "activated"
	default:
		return ""
	}
}

// homeLoginError maps fixed error codes to home.js copy keys. Unknown codes
// become a generic failure; the raw query value is never echoed.
func homeLoginError(code string) string {
	switch code {
	case "bad", "missing", "csrf", "rate", "locked", "server", "temp_expired", "bad_request":
		return code
	case "":
		return ""
	default:
		return "server"
	}
}

// homeNext sanitises the next path for the login dialog hidden field. "/" is
// dropped so the default post-login destination stays the SPA home.
func homeNext(raw string) string {
	next := safeNext(raw)
	if next == "/" {
		return ""
	}
	return next
}

// safePhoneEcho keeps only characters a phone input can legitimately contain.
// The landing page must stay Han-free, so a crafted Chinese "phone" value is
// dropped instead of being reflected into the form.
func safePhoneEcho(raw string) string {
	if raw == "" || len(raw) > 24 {
		return ""
	}
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9', r == '+', r == '-', r == ' ', r == '(', r == ')':
			continue
		default:
			return ""
		}
	}
	return raw
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
		errMessage = "iPhone HEIC 照片请先在相册转成 JPEG，或关闭「高效」格式后再上传。"
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
