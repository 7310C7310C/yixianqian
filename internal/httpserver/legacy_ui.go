package httpserver

import (
	"bytes"
	"io/fs"
	"net/http"
	"sync"

	"github.com/7310C7310C/yixianqian/internal/platform"
	webassets "github.com/7310C7310C/yixianqian/web"
)

// legacyIndexPath is the v1 Vue SPA entrypoint bundled with the binary.
const legacyIndexPath = "static/legacy/index.html"

// SPA 外壳带一个固定的启动旗标，供内嵌的 Vue 应用判断页面是否由已登录
// 用户请求。这里只替换该字面量，不写入任何账号资料。
var (
	legacyAuthFlagOff = []byte("window.__YXQ_AUTHENTICATED__=false;")
	legacyAuthFlagOn  = []byte("window.__YXQ_AUTHENTICATED__=true;")
)

// legacyVersionLiteral is the shell placeholder replaced with the real build
// version. app.js reads it through window.__YXQ_VERSION__.
var legacyVersionLiteral = []byte("__YXQ_VERSION_VALUE__")

// legacyAssetVersionLiteral is replaced in the shell so app.css/app.js and the
// bundled libraries always carry the current asset version in their URL.
var legacyAssetVersionLiteral = []byte("__ASSET_VERSION_VALUE__")

var (
	legacyIndexOnce sync.Once
	legacyIndexHTML []byte
	legacyIndexErr  error
)

func loadLegacyIndex() ([]byte, error) {
	legacyIndexOnce.Do(func() {
		legacyIndexHTML, legacyIndexErr = fs.ReadFile(webassets.FS, legacyIndexPath)
	})
	return legacyIndexHTML, legacyIndexErr
}

// serveLegacyApp returns the v1 SPA shell. It is served with no-store because
// the asset carries the API prefix and compatibility shims, not user data.
func (a *App) serveLegacyApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r); !ok {
		http.Redirect(w, r, "/login?next=/", http.StatusSeeOther)
		return
	}
	body, err := loadLegacyIndex()
	if err != nil {
		a.logger(r).Error("load legacy index failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	// The SPA reads yxq_csrf for write requests. Sessions created before the
	// legacy UI existed may not have the cookie yet.
	a.ensureCSRF(w, r)
	body = bytes.Replace(body, legacyAuthFlagOff, legacyAuthFlagOn, 1)
	// 注入本次构建的版本号：前端 checkVersion 比对 /legacy/api/version 后
	// 只在 App 内提示「有新版本」，刷新由用户点击触发。
	body = bytes.Replace(body, legacyVersionLiteral, []byte(platform.Version), 1)
	// 静态资源版本占位（app.css/app.js/库）由服务端注入，避免壳每次部署换 URL。
	body = bytes.ReplaceAll(body, []byte(legacyAssetVersionLiteral), []byte(assetVersion))
	body = bytes.ReplaceAll(body, []byte("__ASSET_URL_VERSION__"), []byte(assetVersion))
	variant := "anon"
	if _, ok := currentUser(r); ok {
		variant = "auth"
	}
	etag := `"shell-` + platform.Version + `-` + variant + `"`
	w.Header().Set("ETag", etag)
	if mediaETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-cache")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
