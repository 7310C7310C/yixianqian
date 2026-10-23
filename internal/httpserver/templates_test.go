package httpserver

import (
	"bytes"
	"strings"
	"testing"
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
				FeishuURL: publicFeishuAppURL,
			},
			want: []string{
				"<!doctype html>", "登录", `name="csrf_token" value="csrf-value"`,
				`name="next" value="/x"`, `value="138" required autofocus`,
				`href="/register"`, "注册新账号", `class="button primary block"`,
				"已在飞书版使用过", publicFeishuAppURL,
			},
			notWant: []string{`value="138" placeholder=`},
		},
		{
			name: "home",
			page: "home",
			data: PageData{
				Title:     "祝你早日找到另一半",
				FeishuURL: publicFeishuAppURL,
			},
			want: []string{
				"<!doctype html>", "一线牵", "立即注册", `href="/register?from=home"`,
				`href="/login"`, "人工审核", "匿名喜欢", "动态通知",
				"已在飞书版使用过", publicFeishuAppURL,
			},
			notWant: []string{`id="app"`, `feishu-qr`, `二维码`},
		},
		{
			name: "activate",
			page: "activate",
			data: PageData{Title: "设定密码", CSRFToken: "csrf-value", Token: "tok", Name: "测试用户", Phone: "138****06"},
			want: []string{"设定密码", `name="token" value="tok"`, "138****06", "测试用户"},
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
