package httpserver

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

func TestLoadTemplatesAndRenderPages(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	for _, page := range []string{"home", "login", "activate", "error"} {
		if !tpl.HasPage(page) {
			t.Fatalf("missing page %q", page)
		}
	}

	cases := []struct {
		name    string
		page    string
		data    PageData
		want    []string
		notWant []string
	}{
		{
			name: "login",
			page: "login",
			data: PageData{
				Title: "登录", CSRFToken: "csrf-value", Next: "/x", Phone: "138",
				FeishuURL: publicFeishuAppURL, ScanSafe: true, CopyPage: "login",
			},
			want: []string{
				"<!doctype html>", "<title>登录 · 一线牵</title>", `name="csrf_token" value="csrf-value"`,
				`name="next" value="/x"`, `value="138" required autofocus`,
				`href="/register"`, `class="button primary block"`,
				`data-copy-text="login.title"`, `data-copy-text="login.altCta"`,
				`data-copy-page="login"`, `/static/auth.css`, `/static/public-copy.js`,
				publicFeishuAppURL,
			},
			notWant: []string{
				`value="138" placeholder=`, "注册新账号", "已在飞书版使用过",
				`class="shell-header"`, `/static/app.css`, `/static/shell.css`,
			},
		},
		{
			name: "home",
			page: "home",
			data: PageData{
				Title:         publicLandingTitle,
				PageCSS:       "register.css",
				FeishuURL:     publicFeishuAppURL,
				PublicLanding: true,
			},
			want: []string{
				"<!doctype html>", `lang="zh-CN"`, "<title>" + publicLandingTitle + "</title>",
				`class="shell-body public-landing home-pending"`,
				`/static/home.css`, `/static/home.js`,
				`href="/register?from=home"`, `href="/login"`, `id="feishu"`,
				`data-home-text="hero.ctaRegister"`, `data-home-aria="features.label"`,
				publicFeishuAppURL, `target="_blank"`, `rel="noopener noreferrer"`,
			},
			notWant: []string{
				`id="app"`, `feishu-qr`, "跳至主要内容", "shell-header",
				"shell-account", "立即注册", "人工审核", "二维码",
				`/static/app.css`, `/static/shell.css`, `lang="en"`,
				"A trusted place", "Create account", "Used the Feishu version before?",
			},
		},
		{
			name: "activate",
			page: "activate",
			data: PageData{
				Title: "设定密码", CSRFToken: "csrf-value", Token: "tok", Phone: "138****06",
				ScanSafe: true, CopyPage: "activate",
				Data: activatePageData{NameHTML: asciiEntityText("测试用户")},
			},
			want: []string{
				`name="token" value="tok"`, "138****06",
				`data-copy-page="activate"`, `data-copy-text="activate.title"`,
				"&#27979;&#35797;&#29992;&#25143;",
			},
			notWant: []string{"测试用户", `/static/app.css`, `/static/shell.css`},
		},
		{
			name: "password",
			page: "password",
			data: PageData{
				Title: "修改密码", CSRFToken: "csrf-value", ScanSafe: true, CopyPage: "password",
				Data: passwordPageData{Required: true},
			},
			want: []string{
				`data-copy-page="password"`, `data-copy-text="password.required"`,
				`action="/password"`, `name="current_password"`,
			},
			notWant: []string{"账号安全", `/static/app.css`, `/static/shell.css`},
		},
		{
			name: "register_done",
			page: "register_done",
			data: PageData{
				Title: "资料审核中", CSRFToken: "csrf-value", PageCSS: "register.css",
				ScanSafe: true, CopyPage: "register_done",
				Data: registerDoneData{CopyKey: "register_done.pending", LoggedIn: true, PhoneMasked: "138****00"},
			},
			want: []string{
				`data-copy-page="register_done"`, `data-copy-text="register_done.pending.heading"`,
				`data-copy-text="register_done.pending.message"`, "138****00", `action="/logout"`,
			},
			notWant: []string{"查看我的资料", `/static/app.css`, `/static/shell.css`},
		},
		{
			name: "not_found",
			page: "not_found",
			data: PageData{Title: "找不到页面", ScanSafe: true, CopyPage: "not_found"},
			want: []string{
				`data-copy-page="not_found"`, `data-copy-text="not_found.title"`,
				`href="/"`, `/static/public-copy.js`,
			},
			notWant: []string{`/static/app.css`, `/static/shell.css`},
		},
		{
			name: "error",
			page: "error",
			data: PageData{Title: "找不到页面", Error: "页面不存在"},
			want: []string{"找不到页面", "页面不存在"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tpl.Render(&buf, tc.page, tc.data); err != nil {
				t.Fatalf("render: %v", err)
			}
			html := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Fatalf("missing %q in output", want)
				}
			}
			for _, unwanted := range tc.notWant {
				if strings.Contains(html, unwanted) {
					t.Fatalf("unexpected %q in output", unwanted)
				}
			}
		})
	}
}

// TestTemplatesVersionStaticAssets 防止静态资源引用漏掉 cache-busting 参数：
// 静态文件以 public, max-age=3600 下发，版本参数缺失会让部署后仍用旧样式。
func TestTemplatesVersionStaticAssets(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	data := PageData{Title: "登录", PageCSS: "admin.css", ActiveNav: "admin"}
	if err := tpl.Render(&buf, "login", data); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`/static/app.css?v=`,
		`/static/shell.css?v=`,
		`/static/admin.css?v=`,
		`/static/admin.js?v=`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("layout missing cache-busted %q", want)
		}
	}
	for _, unwanted := range []string{
		`/static/app.css"`,
		`/static/shell.css"`,
		`/static/admin.css"`,
		`/static/admin.js"`,
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("layout still references unversioned %q", unwanted)
		}
	}

	for name, want := range map[string]string{
		"templates/pages/admin_matching_group.tmpl":    `/static/admin_matching.js?v={{assetVersion}}`,
		"templates/pages/admin_matching_onetoone.tmpl": `/static/admin_matching.js?v={{assetVersion}}`,
		"static/admin_points.css":                      `/static/admin.css?v=` + assetVersion + `"`,
		"static/admin_user_detail.css":                 `/static/admin.css?v=` + assetVersion + `"`,
	} {
		body, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), want) {
			t.Fatalf("%s missing %q", name, want)
		}
	}
}

// TestStaticCSSImportVersionMatches 防止模块 CSS 的 @import 版本漂移：
// @import 里的版本与 httpserver.assetVersion 不一致时，部署后浏览器仍会命中
// 上一版 admin.css（静态资源 max-age=3600），样式改动看不到。
func TestStaticCSSImportVersionMatches(t *testing.T) {
	names, err := fs.Glob(webassets.FS, "static/*.css")
	if err != nil {
		t.Fatal(err)
	}
	importRE := regexp.MustCompile(`@import\s+url\("(/?static/[^"?]+)\?v=([^"]*)"\)`)
	seen := 0
	for _, name := range names {
		body, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range importRE.FindAllStringSubmatch(string(body), -1) {
			seen++
			if match[2] != assetVersion {
				t.Fatalf("%s imports %s?v=%s, want assetVersion=%s", name, match[1], match[2], assetVersion)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no versioned @import found; cache-busting guard is ineffective")
	}
}

func TestTemplatesEscapeUserData(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	phone := `<script>alert(1)</script>`
	if err := tpl.Render(&buf, "login", PageData{Title: "登录", Phone: phone}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, phone) {
		t.Fatal("user data was not escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatal("expected escaped phone")
	}
}
