package httpserver

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
	webassets "github.com/7310C7310C/yixianqian/web"
)

// assetVersion is the cache-busting query value for static assets referenced by
// templates. Static files are served with public, max-age=3600, so a stylesheet
// change would otherwise stay stale in browsers for an hour after deploy. Bump
// this whenever web/static content referenced from templates changes; module
// CSS @import lines carry the same value and must be bumped together.
// Batch10: 15 invalidates edge/browser immutable caches for the shell assets.
// Batch11 (landing scan-guard + viewer overlay removal): 28 invalidated the new
// home.css/home.js pair and the regenerated critical CSS / root render.
// Batch12 (welcome merge + scan-safe login/activate/password/register-done +
// viewer photos_meta): 29 invalidated the auth.css/public-copy.js pair and the
// regenerated app.min.js/app.css.
// Batch12b (scan-safe public GET error pages + app.js /welcome cleanup): 30.
// Batch13 (scan-safe /register form: server-generated ASCII copy catalog +
// register.js): 31.
// Batch14 (viewer arrows + loop): 32 invalidated app.css / app.min.js.
// 2026-10-28 (brand favicon): 34 invalidated the placeholder favicon.svg and
// added the v1 heart icon set (svg/png/ico) to the template links.
// 2026-10-28 (remove self-card hint): 35 invalidated app.css / app.min.js /
// app.render.js after dropping the redundant first-card bubble.
// 2026-10-29 (brand splash): 50 invalidated index/app.css/app.min.js after reverting
// the static island and adding the pure heart + 一线牵 splash.
// 2026-10-29 (old hero restore): 51 invalidated index/app.css/app.min.js after
// removing the brand splash and returning to the pre-static-island hero.
// 2026-10-29 (login dialog + register regions/QR): 52 invalidated home.css/
// home.js/register.css/register.js/admin.css and added the register.js region
// loader. 53 covered the review fixes to home.js/register.js; 54 covers the
// house/car notes, QR long-press gate and unified image-format changes. 55
// covered the SPA compression white-fill fix. 56 covers the photo preview,
// simplified hints, church province/city and QR compression changes. 57
// covered the CSP/preview/feedback fixes. 58 covers the region focus/church
// revert changes. 65 covers the self-card photo sync. 66 dropped the matching
// group/one-to-one status tags from the activity list cards; 67 also covers the
// activity detail card and the empty meta row on my-groups cards. 68 blocked
// the volunteer-selection entry until check-in and hid self sign-in when the
// activity has no valid code; 69 keeps the ended-activity defense and requires
// a real 4-digit code. Keep admin @import values in sync.
const assetVersion = "73"

// MediaView is the minimal media representation exposed to templates.
type MediaView struct {
	ID     string
	Width  int
	Height int
}

// PageData is the common template payload. Handlers fill only what they need.
type PageData struct {
	Title      string
	User       *db.User
	LegacyID   string
	CSRFToken  string
	Error      string
	Notice     string
	Next       string
	Token      string
	Phone      string
	Name       string
	Media      []MediaView
	MediaMaxMB int
	// Data is an optional page-specific payload. Feature modules should use
	// this instead of growing PageData for every page, which keeps parallel
	// work from conflicting in this shared file.
	Data         any
	ActiveNav    string
	ActiveSubnav string
	PageCSS      string
	// PublicLanding marks the anonymous English-only landing page. The layout
	// renders it without the Chinese shell header / skip link and drops the
	// 「· 一线牵」 title suffix.
	PublicLanding bool
	// ScanSafe marks an anonymous (or scan-sensitive) page that must not ship
	// Chinese copy in its HTML, CSS or JS. The layout drops the Chinese shell
	// chrome and loads the ASCII-only home.css/auth.css pair; Chinese copy is
	// injected by /static/public-copy.js.
	ScanSafe bool
	// CopyPage selects the public-copy.js copy table (ASCII value). When set,
	// the layout loads public-copy.js and exposes the value as
	// <body data-copy-page>.
	CopyPage string
	// CopyJSON carries a per-request, ASCII-only (all non-ASCII escaped as
	// \uXXXX) JSON copy catalog for pages whose copy is generated on the server
	// (currently /register). The layout exposes it as <body data-copy-json> and
	// loads /static/register.js to inject it.
	CopyJSON string
	// LoginOpen opens the landing-page login dialog. The login page was merged
	// into the anonymous home page; old /login links 303 to /?login=1.
	LoginOpen bool
	// LoginError is a fixed error code rendered by home.js (never free text,
	// so the landing page stays Han-free).
	LoginError string
	// LoginNotice is a fixed notice code (logged_out / activated).
	LoginNotice string
	// NoticeCopyKey renders a JS-injected notice instead of the server-side
	// Chinese Notice string on scan-safe pages (e.g. /login?logged_out=1).
	NoticeCopyKey string
	// ErrorCode selects the public-copy.js error table on the scan-safe public
	// error page; publicErrorData repeats it for template assertions.
	ErrorCode string
	// FeishuURL 是飞书版入口；render 会为所有页面填入默认值，模板无需区分
	// GET/POST 渲染路径。
	FeishuURL string
	// CanPoints / CanAudit control admin navigation visibility. The renderer
	// fills them from the admin access stored on the request context.
	CanPoints bool
	CanAudit  bool
}

// Templates holds one cloned template set per page so each page can define its
// own "content" block while sharing the layout.
type Templates struct {
	pages map[string]*template.Template
}

// LoadTemplates parses embedded templates once at startup.
func LoadTemplates() (*Templates, error) {
	base, err := template.New("layout").Funcs(template.FuncMap{
		"assetVersion": func() string { return assetVersion },
	}).ParseFS(webassets.FS,
		"templates/layout.tmpl",
		"templates/partials/*.tmpl",
	)
	if err != nil {
		return nil, fmt.Errorf("httpserver: parse base templates: %w", err)
	}

	pageFiles, err := fs.Glob(webassets.FS, "templates/pages/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("httpserver: list page templates: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, fmt.Errorf("httpserver: no page templates found")
	}

	out := &Templates{pages: make(map[string]*template.Template, len(pageFiles))}
	for _, file := range pageFiles {
		name := strings.TrimSuffix(path.Base(file), ".tmpl")
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("httpserver: clone templates for %s: %w", name, err)
		}
		if _, err := clone.ParseFS(webassets.FS, file); err != nil {
			return nil, fmt.Errorf("httpserver: parse page %s: %w", name, err)
		}
		out.pages[name] = clone
	}
	return out, nil
}

// Render executes the layout with the page's content block.
func (t *Templates) Render(w io.Writer, page string, data PageData) error {
	tpl, ok := t.pages[page]
	if !ok {
		return fmt.Errorf("httpserver: unknown page %q", page)
	}
	return tpl.ExecuteTemplate(w, "layout", data)
}

// HasPage reports whether a named page template was loaded.
func (t *Templates) HasPage(page string) bool {
	_, ok := t.pages[page]
	return ok
}

// renderPage buffers output so a template failure cannot emit a half page.
func (a *App) renderPage(w io.Writer, page string, data PageData) []byte {
	var buf bytes.Buffer
	if err := a.tpl.Render(&buf, page, data); err != nil {
		a.log.Error("render template failed", "page", page, "error", err)
		return nil
	}
	return buf.Bytes()
}
