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
// 2026-10-28 (boot card island): 36 invalidated index.html / app.css / app.min.js
// after replacing the bare hero with the server-rendered complete first card.
// 2026-10-28 (boot card takeover fix): 37 invalidated index.html / app.min.js after
// scoping the real-card selector/scroll transfer to #app and skipping restore.
// 2026-10-28 (boot card staging fixes): 38 invalidated index.html / app.min.js after
// early face-crop retries and scroll handoff on every dismissal path.
// 2026-10-28 (boot card interaction handoff): 39 invalidated index.html / app.min.js
// after routing inline pointerdown through the app scroll-handoff hook.
// 2026-10-28 (boot badge height): 40 invalidated app.css / critical CSS after matching
// the static island badge line box to the real card badge.
// 2026-10-28 (boot card polish): 41 invalidated app.css/app.js after blocking click-through,
// card actions, conditional preload and scroll handoff guard.
// 2026-10-28 (boot card touch/deeplink): 42 invalidated index.html/app.css/app.js after
// touch tap-vs-scroll, data-src image and new-element scroll cancel.
// 2026-10-28 (boot card touch fallback): 43 invalidated app.js after touchmove movement
// fallback for mobile WebViews without pointermove.
// 2026-10-28 (boot card touchend): 44 invalidated app.js after touch-only touchend tap.
// 2026-10-28 (boot card hard cap): 45 invalidated app.js after only hard-cutting when
// loadHome is still pending (not while a complete static card is being read).
const assetVersion = "45"

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
	// PrefetchSPA warms the SPA shell assets after a login/activate/register
	// page loads, so the first authenticated `/` visit is not a cold bundle
	// download. The layout skips it on Save-Data/2G connections.
	PrefetchSPA bool
	// CopyPage selects the public-copy.js copy table (ASCII value). When set,
	// the layout loads public-copy.js and exposes the value as
	// <body data-copy-page>.
	CopyPage string
	// CopyJSON carries a per-request, ASCII-only (all non-ASCII escaped as
	// \uXXXX) JSON copy catalog for pages whose copy is generated on the server
	// (currently /register). The layout exposes it as <body data-copy-json> and
	// loads /static/register.js to inject it.
	CopyJSON string
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
	// base holds the shared layout + partials. It is used to render the
	// server-side first-card island (boot_card) outside the SPA root.
	base *template.Template
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

	out := &Templates{pages: make(map[string]*template.Template, len(pageFiles)), base: base}
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

// RenderBootCard renders the server-side first-card island into the SPA shell.
// The template lives in templates/partials/boot_card.tmpl and is parsed with the
// shared base set so it cannot drift into a second template engine.
func (t *Templates) RenderBootCard(w io.Writer, data any) error {
	if t == nil || t.base == nil {
		return fmt.Errorf("httpserver: templates not loaded")
	}
	if t.base.Lookup("boot_card") == nil {
		return fmt.Errorf("httpserver: boot_card template missing")
	}
	return t.base.ExecuteTemplate(w, "boot_card", data)
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
