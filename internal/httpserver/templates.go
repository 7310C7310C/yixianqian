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
	base, err := template.New("layout").ParseFS(webassets.FS,
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
