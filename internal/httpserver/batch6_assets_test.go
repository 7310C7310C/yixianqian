package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

type pagesManifest struct {
	Root struct {
		File          string `json:"file"`
		TemplateSHA   string `json:"templateSha256"`
		TemplateBytes int    `json:"templateSize"`
	} `json:"root"`
	Initial []string `json:"initial"`
	Groups  map[string]struct {
		File  string   `json:"file"`
		Pages []string `json:"pages"`
	} `json:"groups"`
	Pages map[string]struct {
		Group         string `json:"group"`
		File          string `json:"file"`
		TemplateSHA   string `json:"templateSha256"`
		TemplateBytes int    `json:"templateSize"`
	} `json:"pages"`
}

// The lazy page chunks must be regenerated whenever a page template changes.
// This is the CI gate for tools/spa_precompile/precompile.mjs: edit
// web/static/legacy/pages/*.html without `npm run build` -> red.
func TestPageChunksMatchTemplates(t *testing.T) {
	raw, err := fs.ReadFile(webassets.FS, "static/legacy/pages/pages.manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m pagesManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Pages) != 26 {
		t.Fatalf("page manifest has %d pages, want 26", len(m.Pages))
	}
	if len(m.Initial) != 1 || m.Initial[0] != "cards" {
		t.Fatalf("initial chunk = %v, want [cards]", m.Initial)
	}
	for name, page := range m.Pages {
		body, err := fs.ReadFile(webassets.FS, "static/legacy/pages/"+name+".html")
		if err != nil {
			t.Fatalf("page %s missing: %v", name, err)
		}
		sum := sha256.Sum256([]byte(strings.TrimSpace(string(body))))
		if got := hex.EncodeToString(sum[:]); got != page.TemplateSHA {
			t.Fatalf("page %s stale: sha=%s generated=%s; run `cd tools/spa_precompile && npm run build`", name, got, page.TemplateSHA)
		}
		if len(strings.TrimSpace(string(body))) != page.TemplateBytes {
			t.Fatalf("page %s stale: size=%d generated=%d", name, len(strings.TrimSpace(string(body))), page.TemplateBytes)
		}
		if name == "cards" {
			continue
		}
		chunk, err := fs.ReadFile(webassets.FS, "static/legacy/"+page.File)
		if err != nil {
			t.Fatalf("chunk %s missing: %v", page.File, err)
		}
		if !strings.Contains(string(chunk), "__YXQ_PAGES__['"+name+"']") {
			t.Fatalf("chunk %s does not register page %s", page.File, name)
		}
	}
	for group, info := range m.Groups {
		for _, name := range info.Pages {
			if m.Pages[name].Group != group {
				t.Fatalf("page %s group = %s, want %s", name, m.Pages[name].Group, group)
			}
		}
	}
	html := readEmbedded(t, "static/legacy/index.html")
	if !strings.Contains(html, "<page-host") {
		t.Fatal("index.html has no <page-host>")
	}
	if strings.Contains(html, "v-else-if=\"page===") || strings.Contains(html, "v-if=\"page===") {
		t.Fatal("index.html still contains page branches; run tools/spa_precompile/split_pages.mjs")
	}
	render := readEmbedded(t, "static/legacy/app.render.js")
	if !strings.Contains(render, "window.__YXQ_RENDER__ = render") ||
		!strings.Contains(render, "__YXQ_PAGES__['cards'] = cardsRender") {
		t.Fatal("app.render.js is missing the root render or the initial cards chunk")
	}
}

// The Vant subset bundle must cover every component referenced by the shell
// and by app.js. Missing components would resolve to nothing at runtime, so
// the build is gated here and app.js also shows a visible boot error.
func TestVantSubsetCoversTemplate(t *testing.T) {
	raw, err := fs.ReadFile(webassets.FS, "static/legacy/lib/vant.subset.manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Vant       string   `json:"vant"`
		Components []string `json:"components"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	scan := func(text string) {
		for _, match := range regexp.MustCompile(`<van-([a-z-]+)`).FindAllStringSubmatch(text, -1) {
			want["van-"+match[1]] = true
		}
		for _, match := range regexp.MustCompile(`resolveComponent\('(van-[a-z-]+)'\)`).FindAllStringSubmatch(text, -1) {
			want[match[1]] = true
		}
	}
	scan(readEmbedded(t, "static/legacy/index.html"))
	pages, err := fs.Glob(webassets.FS, "static/legacy/pages/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range pages {
		scan(string(mustRead(t, name)))
	}
	appJS := readEmbedded(t, "static/legacy/app.js")
	scan(appJS)

	have := map[string]bool{}
	for _, name := range m.Components {
		have[name] = true
	}
	var missing []string
	for name := range want {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("Vant subset is missing components used by the SPA: %v", missing)
	}
	required := regexp.MustCompile(`var YXQ_VANT_REQUIRED = \[([^\]]+)\]`).FindStringSubmatch(appJS)
	if required == nil {
		t.Fatal("app.js has no YXQ_VANT_REQUIRED list")
	}
	var listed []string
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(required[1], -1) {
		listed = append(listed, q[1])
	}
	sort.Strings(listed)
	sortedHave := append([]string(nil), m.Components...)
	sort.Strings(sortedHave)
	if strings.Join(listed, ",") != strings.Join(sortedHave, ",") {
		t.Fatalf("app.js YXQ_VANT_REQUIRED=%v, subset manifest=%v", listed, sortedHave)
	}
	if len(have) != 17 {
		t.Fatalf("Vant subset has %d components, want 17", len(have))
	}
	if m.Vant != "4.8.7" {
		t.Fatalf("Vant subset version = %s, want 4.8.7", m.Vant)
	}
	for _, file := range []string{"static/legacy/lib/vant.min.js", "static/legacy/lib/vant.min.css"} {
		if len(mustRead(t, file)) == 0 {
			t.Fatalf("%s is empty", file)
		}
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := fs.ReadFile(webassets.FS, name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
