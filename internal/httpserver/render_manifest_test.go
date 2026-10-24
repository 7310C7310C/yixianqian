package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// extractAppTemplate returns the exact inner template of #app as compiled by
// tools/spa_precompile: from the first child to the last child, trimmed of the
// surrounding indentation that the Vue parser drops.
func extractAppTemplate(t *testing.T, html string) []byte {
	t.Helper()
	start := regexp.MustCompile(`<div\s+[^>]*\bid="app"[^>]*>`).FindStringIndex(html)
	if start == nil {
		t.Fatal("index.html has no #app container")
	}
	depth := 1
	innerStart := start[1]
	for i := start[1]; i < len(html); {
		rel := strings.IndexByte(html[i:], '<')
		if rel < 0 {
			break
		}
		j := i + rel
		switch {
		case strings.HasPrefix(html[j:], "<!--"):
			end := strings.Index(html[j:], "-->")
			if end < 0 {
				t.Fatal("unterminated comment inside #app")
			}
			i = j + end + 3
			continue
		case strings.HasPrefix(html[j:], "</div"):
			depth--
			if depth == 0 {
				return []byte(strings.TrimSpace(html[innerStart:j]))
			}
			i = j + len("</div")
			continue
		case strings.HasPrefix(html[j:], "<div"):
			next := byte(0)
			if j+4 < len(html) {
				next = html[j+4]
			}
			if next == '>' || next == ' ' || next == '\n' || next == '\t' {
				depth++
			}
		}
		i = j + 1
	}
	t.Fatal("unterminated #app container")
	return nil
}

// TestRenderManifestMatchesTemplate is the CI gate for app.render.js: changing
// the #app template without rebuilding must fail make ci.
func TestRenderManifestMatchesTemplate(t *testing.T) {
	html := readEmbedded(t, "static/legacy/index.html")
	render := readEmbedded(t, "static/legacy/app.render.js")

	header, _, _ := strings.Cut(render, "\n")
	shaMatch := regexp.MustCompile(`template-sha256=([0-9a-f]{64})`).FindStringSubmatch(header)
	sizeMatch := regexp.MustCompile(`template-size=([0-9]+)`).FindStringSubmatch(header)
	versionMatch := regexp.MustCompile(`vue=([0-9]+\.[0-9]+\.[0-9]+)`).FindStringSubmatch(header)
	if shaMatch == nil || sizeMatch == nil || versionMatch == nil {
		t.Fatalf("app.render.js header is missing vue/template metadata: %q", header)
	}

	template := extractAppTemplate(t, html)
	sum := sha256.Sum256(template)
	gotSHA := hex.EncodeToString(sum[:])
	if gotSHA != shaMatch[1] {
		t.Fatalf("app.render.js is stale: template sha=%s, generated=%s; run `cd tools/spa_precompile && npm run build`", gotSHA, shaMatch[1])
	}
	wantSize, err := strconv.Atoi(sizeMatch[1])
	if err != nil || wantSize != len(template) {
		t.Fatalf("app.render.js template-size=%s, actual=%d", sizeMatch[1], len(template))
	}
	if versionMatch[1] != "3.4.27" {
		t.Fatalf("compiler version %s does not match the pinned Vue runtime 3.4.27", versionMatch[1])
	}
}
