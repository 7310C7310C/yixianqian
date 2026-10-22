package httpserver

import "net/http"

// handleHome serves the v1 Vue SPA for signed-in users. Anonymous visitors are
// sent to the Go login page.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	a.serveLegacyApp(w, r)
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
