package httpserver

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

// stripTitle removes the <title> element. The scan contract allows Chinese
// only there; every other byte of the response must be ASCII-safe (no Han).
func stripTitle(body string) (string, bool) {
	start := strings.Index(body, "<title>")
	if start < 0 {
		return body, false
	}
	end := strings.Index(body[start:], "</title>")
	if end < 0 {
		return body, false
	}
	end += start + len("</title>")
	return body[:start] + body[end:], true
}

// assertHanFreePublicBody enforces the public-page scan contract: exactly one
// title tag (which may carry the Chinese page name) and zero Han elsewhere.
func assertHanFreePublicBody(t *testing.T, body string) {
	t.Helper()
	rest, ok := stripTitle(body)
	if !ok {
		t.Fatalf("page has no <title>: %s", body)
	}
	if match := hanPattern.FindString(rest); match != "" {
		i := strings.Index(rest, match)
		from := i - 40
		if from < 0 {
			from = 0
		}
		to := i + 40
		if to > len(rest) {
			to = len(rest)
		}
		t.Fatalf("public page contains Han outside <title>: %q near %q", match, rest[from:to])
	}
}

var staticRefPattern = regexp.MustCompile(`(?:src|href)="(/static/[^"?#]+)`)
var copyKeyPattern = regexp.MustCompile(`data-copy-(?:text|aria|html|placeholder)="([^"]+)"`)

// TestPublicScanPagesAreHanFree renders every scan-safe page and checks both
// halves of the contract: the HTML (outside <title>) and every referenced
// same-origin CSS/JS are Han-free.
func TestPublicScanPagesAreHanFree(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	pages := []struct {
		name string
		page string
		data PageData
	}{
		{
			name: "login",
			page: "login",
			data: PageData{
				Title: "登录", CSRFToken: "csrf", Next: "/x", FeishuURL: publicFeishuAppURL,
				ScanSafe: true, CopyPage: "login",
			},
		},
		{
			name: "login notice",
			page: "login",
			data: PageData{
				Title: "登录", CSRFToken: "csrf", FeishuURL: publicFeishuAppURL,
				ScanSafe: true, CopyPage: "login", NoticeCopyKey: "login.notice.logged_out",
			},
		},
		{
			name: "activate",
			page: "activate",
			data: PageData{
				Title: "设定密码", CSRFToken: "csrf", Token: "tok", Phone: "138****06",
				ScanSafe: true, CopyPage: "activate",
				Data: activatePageData{NameHTML: asciiEntityText("测试用户")},
			},
		},
		{
			name: "password",
			page: "password",
			data: PageData{
				Title: "修改密码", CSRFToken: "csrf", ScanSafe: true, CopyPage: "password",
				Data: passwordPageData{Required: true},
			},
		},
		{
			name: "register_done pending",
			page: "register_done",
			data: PageData{
				Title: "资料审核中", CSRFToken: "csrf", PageCSS: "register.css",
				ScanSafe: true, CopyPage: "register_done",
				Data: registerDoneData{CopyKey: "register_done.pending", LoggedIn: true, PhoneMasked: "138****00"},
			},
		},
		{
			name: "register_done anonymous",
			page: "register_done",
			data: PageData{
				Title: "注册与审核", CSRFToken: "csrf", PageCSS: "register.css",
				ScanSafe: true, CopyPage: "register_done",
				Data: registerDoneData{CopyKey: "register_done.default"},
			},
		},
		{
			name: "public_error",
			page: "public_error",
			data: PageData{
				Title: "启用链接已过期", ScanSafe: true, CopyPage: "public_error",
				ErrorCode: publicErrorActivateExpired,
				Data:      publicErrorData{Code: publicErrorActivateExpired},
			},
		},
		{
			name: "not_found",
			page: "not_found",
			data: PageData{Title: "找不到页面", ScanSafe: true, CopyPage: "not_found"},
		},
	}

	jsCopy, err := fs.ReadFile(webassets.FS, "static/public-copy.js")
	if err != nil {
		t.Fatalf("read public-copy.js: %v", err)
	}
	js := string(jsCopy)

	assets := map[string]bool{}
	for _, tc := range pages {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tpl.Render(&buf, tc.page, tc.data); err != nil {
				t.Fatalf("render: %v", err)
			}
			body := buf.String()
			assertHanFreePublicBody(t, body)
			if !strings.Contains(body, `lang="zh-CN"`) {
				t.Fatal("scan-safe pages must declare an ASCII html lang")
			}
			if !strings.Contains(body, `data-copy-page="`+tc.data.CopyPage+`"`) {
				t.Fatalf("missing data-copy-page=%q", tc.data.CopyPage)
			}
			if strings.Contains(body, "/static/app.css") || strings.Contains(body, "/static/shell.css") {
				t.Fatal("scan-safe pages must not reference CSS with Chinese comments")
			}
			keys := copyKeyPattern.FindAllStringSubmatch(body, -1)
			if len(keys) == 0 {
				t.Fatal("page has no data-copy keys")
			}
			for _, match := range keys {
				if !strings.Contains(js, "'"+match[1]+"'") {
					t.Fatalf("public-copy.js has no copy for key %q", match[1])
				}
			}
			for _, match := range staticRefPattern.FindAllStringSubmatch(body, -1) {
				assets[match[1]] = true
			}
		})
	}

	for asset := range assets {
		name := strings.TrimPrefix(asset, "/")
		data, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("scan-safe page references missing asset %q: %v", asset, err)
		}
		if match := hanPattern.FindString(string(data)); match != "" {
			t.Fatalf("%s contains Han characters (%q); referenced assets must stay ASCII-only", name, match)
		}
	}
	if !assets["/static/public-copy.js"] || !assets["/static/auth.css"] {
		t.Fatalf("scan-safe pages must reference public-copy.js + auth.css, got %v", assets)
	}
}

// TestPublicCopyCoversEveryNoticeKey keeps the Go notice keys and the JS copy
// table in sync.
func TestPublicCopyCoversEveryNoticeKey(t *testing.T) {
	js, err := fs.ReadFile(webassets.FS, "static/public-copy.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"login.notice.logged_out", "login.notice.activated"} {
		if !strings.Contains(string(js), "'"+key+"'") {
			t.Fatalf("public-copy.js missing %q", key)
		}
	}
}

// TestASCIITextEscapesMarkupAndNonASCII guards the activation-page name path:
// user data must never inject markup and must stay ASCII in the response body.
func TestASCIITextEscapesMarkupAndNonASCII(t *testing.T) {
	got := string(asciiEntityText(`<script>"x"&' 张三`))
	want := `&lt;script&gt;&#34;x&#34;&amp;&#39; &#24352;&#19977;`
	if got != want {
		t.Fatalf("asciiEntityText = %q, want %q", got, want)
	}
	if hanPattern.MatchString(got) {
		t.Fatalf("asciiEntityText output contains Han: %q", got)
	}
}
