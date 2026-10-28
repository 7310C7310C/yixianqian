package httpserver

import (
	"io/fs"
	"net/http"
	"strconv"

	webassets "github.com/7310C7310C/yixianqian/web"
)

// faviconHandler serves /favicon.ico from the embedded static tree. Browsers
// and older clients request the root path directly; v1 has always answered it,
// and the same file is already whitelisted by the session, password and
// registration gates. Without this route the request fell through to the HTML
// 404 page, so Safari/home-screen shortcuts showed a generic icon.
func faviconHandler() http.Handler {
	data, err := fs.ReadFile(webassets.FS, "static/favicon.ico")
	if err != nil {
		panic("embedded static/favicon.ico missing: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/vnd.microsoft.icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	})
}
