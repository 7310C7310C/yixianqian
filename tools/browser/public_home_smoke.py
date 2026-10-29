#!/usr/bin/env python3
"""公开首页 / 登录页 Playwright 回归（扫描防护契约）。

匿名（必跑）：
1. GET / 返回 200 且不跳转，渲染中文公开首页；中文文案由 /static/home.js 注入；
2. 原始 HTTP body 的 <title> 精确为「一线牵 · 祝你早日找到另一半」，移除 title 后整页无 CJK；
   页面引用的同源 CSS/JS 响应体也必须 0 汉字；
3. 无中文 shell header / skip link；注册是 primary block 主按钮，登录入口与飞书链接（target/rel）都在，
   页面不再展示二维码图片；390/1280 双视口无横向滚动、无 console error；
4. 点登录打开首页弹框（不离开首页），手机号/密码/注册入口/飞书说明可见。
已登录（设置 SESSION_COOKIE 时）：
5. GET / 仍是 v1 Vue SPA，__YXQ_AUTHENTICATED__=true，不渲染公开首页。

用法（staging 或本地预览服务）：
    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    SHOT_DIR=/tmp/opencode/public-home-smoke \
    python3 tools/browser/public_home_smoke.py
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
FEISHU_URL = "https://applink.feishu.cn/T996FhMkTERp"
PUBLIC_TITLE = "一线牵 · 祝你早日找到另一半"
HAN_RE = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]")
STATIC_ASSET_RE = re.compile(r'(?:href|src)="(/static/[^"]+\.(?:css|js)(?:\?[^"]*)?)"')
# Rendered copy that must appear once home.js has injected the TEXT table.
RENDERED_COPY = (
    "立即注册", "已有账号，登录", "免费注册 · 人工审核 · 手机号码即可登录",
    "人工审核", "匿名喜欢", "动态通知",
    "已在飞书版使用过", "打开飞书版一线牵",
)
REQUIRED_ASSETS = ("/static/home.css", "/static/home.js", "/static/register.css")


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def attach_error_capture(page) -> list[dict[str, str]]:
    errors: list[dict[str, str]] = []

    def on_console(msg):
        if msg.type != "error":
            return
        loc = msg.location or {}
        errors.append({"kind": "console", "text": msg.text, "url": loc.get("url", "")})

    def on_pageerror(exc):
        errors.append({"kind": "pageerror", "text": str(exc), "url": ""})

    page.on("console", on_console)
    page.on("pageerror", on_pageerror)
    return errors


def assert_no_console_errors(errors: list[dict[str, str]], context: str) -> None:
    real: list[str] = []
    for item in errors:
        url = item.get("url", "")
        text = item.get("text", "")
        if "legacy/api/notifications/stream" in url or "legacy/api/notifications/stream" in text:
            continue
        real.append(f"{item.get('kind')}: {text}" + (f" ({url})" if url else ""))
    if real:
        raise SmokeFailure(f"{context} 存在 console error：\n" + "\n".join(real))


def assert_primary_block(locator, context: str) -> None:
    classes = locator.get_attribute("class") or ""
    if "primary" not in classes or "block" not in classes:
        raise SmokeFailure(f"{context}: 不是 primary block（{classes}）")


def assert_han_free(text: str, context: str) -> None:
    match = HAN_RE.search(text)
    if match:
        start = max(0, match.start() - 40)
        snippet = text[start:match.start() + 40]
        raise SmokeFailure(f"{context} 出现汉字：{snippet!r}")


def assert_landing_source(raw_html: str, width: int) -> None:
    """PM 要求：源码除 <title> 外不得出现任何汉字（避免被扫描/墙）。"""
    title_tag = f"<title>{PUBLIC_TITLE}</title>"
    if raw_html.count(title_tag) != 1:
        raise SmokeFailure(f"{width}px: 源码 title 必须精确且唯一：{PUBLIC_TITLE!r}")
    rest = raw_html.replace(title_tag, "", 1)
    assert_han_free(rest, f"{width}px 首页源码（title 之外）")
    for wanted in REQUIRED_ASSETS:
        if wanted not in raw_html:
            raise SmokeFailure(f"{width}px: 首页源码缺少 {wanted}")
    for unwanted in ("/static/app.css", "/static/shell.css", "feishu-qr", "home-qr"):
        if unwanted in raw_html:
            raise SmokeFailure(f"{width}px: 首页源码不应引用 {unwanted}")


def assert_assets_han_free(context, base_url: str, raw_html: str, width: int) -> list[str]:
    """首页实际引用的同源 CSS/JS 响应体必须 0 汉字。"""
    checked: list[str] = []
    for ref in sorted(set(STATIC_ASSET_RE.findall(raw_html))):
        if urlparse(ref).netloc:
            continue
        resp = context.request.get(base_url + ref)
        if resp.status != 200:
            raise SmokeFailure(f"{width}px: 静态资源 {ref} status={resp.status}")
        assert_han_free(resp.text(), f"{width}px 静态资源 {ref}")
        checked.append(ref.split("?")[0])
    if not checked:
        raise SmokeFailure(f"{width}px: 首页没有引用任何同源 CSS/JS")
    for required in REQUIRED_ASSETS:
        if required not in checked:
            raise SmokeFailure(f"{width}px: 首页未实际引用 {required}（checked={checked}）")
    return checked


def assert_rendered_copy(page, width: int) -> None:
    for copy in RENDERED_COPY:
        locator = page.get_by_text(copy, exact=False).first
        locator.wait_for(state="visible", timeout=10000)
    if page.locator("html").get_attribute("lang") != "zh-CN":
        raise SmokeFailure(f"{width}px: 公开首页 lang 必须为 zh-CN")
    if "public-landing" not in (page.locator("body").get_attribute("class") or ""):
        raise SmokeFailure(f"{width}px: 公开首页缺少 public-landing class")
    pending = page.evaluate("() => document.body.classList.contains('home-pending')")
    if pending:
        raise SmokeFailure(f"{width}px: home.js 未清除 home-pending，文案可能仍未注入")
    unfilled = page.evaluate(
        """() => Array.from(document.querySelectorAll('[data-home-text]'))
             .filter(el => !(el.textContent || '').trim())
             .map(el => el.getAttribute('data-home-text'))"""
    )
    if unfilled:
        raise SmokeFailure(f"{width}px: 以下文案节点为空：{unfilled}")
    if page.locator(".shell-header").count():
        raise SmokeFailure(f"{width}px: 公开首页不应渲染 shell header")
    if page.locator(".shell-skip").count():
        raise SmokeFailure(f"{width}px: 公开首页不应渲染 skip link")


def assert_no_horizontal_scroll(page, width: int, context: str) -> None:
    metrics = page.evaluate(
        """() => ({scrollWidth: document.documentElement.scrollWidth,
                   innerWidth: window.innerWidth})"""
    )
    if metrics["scrollWidth"] > metrics["innerWidth"] + 1:
        raise SmokeFailure(f"{width}px {context}: 出现横向滚动 {metrics}")


def run_anonymous(browser, width: int, height: int, base_url: str, out_dir: Path) -> list[str]:
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    page = context.new_page()
    errors = attach_error_capture(page)
    tag = str(width)
    try:
        resp = page.goto(base_url + "/", wait_until="domcontentloaded")
        if resp is None or resp.status != 200:
            raise SmokeFailure(f"{width}px: / status={getattr(resp, 'status', None)}")
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}px: / 被重定向到 {page.url}")
        raw_html = resp.text()

        page.locator(".home-hero").wait_for(state="visible", timeout=10000)
        if page.locator("#app").count():
            raise SmokeFailure(f"{width}px: 匿名首页不应出现 SPA #app")
        assert_landing_source(raw_html, width)
        checked = assert_assets_han_free(context, base_url, raw_html, width)
        assert_rendered_copy(page, width)

        register = page.locator('.home-hero a[href="/register?from=home"]')
        register.wait_for(state="visible", timeout=10000)
        assert_primary_block(register, f"{width}px 首页注册按钮")
        if register.inner_text().strip() != "立即注册":
            raise SmokeFailure(f"{width}px: 注册按钮文案 = {register.inner_text()!r}")

        login_button = page.locator('.home-hero [data-login-open]')
        login_button.wait_for(state="visible", timeout=10000)
        if login_button.inner_text().strip() != "已有账号，登录":
            raise SmokeFailure(f"{width}px: 登录按钮文案 = {login_button.inner_text()!r}")

        feishu = page.locator(f'.home-feishu a[href="{FEISHU_URL}"]')
        feishu.wait_for(state="visible", timeout=10000)
        if feishu.get_attribute("target") != "_blank":
            raise SmokeFailure(f"{width}px: 飞书链接缺少 target=_blank")
        if "noopener" not in (feishu.get_attribute("rel") or ""):
            raise SmokeFailure(f"{width}px: 飞书链接缺少 rel=noopener")

        qr = page.locator(".home-qr img")
        if qr.count():
            raise SmokeFailure(f"{width}px: 公开首页不应再有二维码图片")
        assert_no_horizontal_scroll(page, width, "匿名首页")
        page.screenshot(path=str(out_dir / f"home-anon-{tag}.png"), full_page=True)
        print(f"PASS {width}px 匿名首页：中文注入、标题唯一、静态资源 0 汉字（{len(checked)} 个）")

        page.locator('.home-hero [data-login-open]').click()
        modal = page.locator('[data-login-modal]')
        modal.wait_for(state="visible", timeout=10000)
        for selector, label in [
            ("#home_login_phone", "手机号输入框"),
            ("#home_login_password", "密码输入框"),
            ('button[type="submit"]', "登录提交按钮"),
            ('a[href="/register"]', "注册链接"),
        ]:
            if not modal.locator(selector).count():
                raise SmokeFailure(f"{width}px: 登录弹框缺少{label}（{selector}）")
        modal_text = modal.inner_text()
        for copy in ("登录", "手机号码", "密码", "注册新账号", "已在飞书版使用过", "打开飞书版"):
            if copy not in modal_text:
                raise SmokeFailure(f"{width}px: 登录弹框缺少文案 {copy!r}")
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}px: 登录弹框不应离开首页（url={page.url}）")
        assert_no_horizontal_scroll(page, width, "登录弹框")
        page.screenshot(path=str(out_dir / f"login-dialog-anon-{tag}.png"), full_page=True)

        assert_no_console_errors(errors, f"{width}px 匿名")
        return []
    except Exception as exc:  # noqa: BLE001 - report all viewport failures together
        return [f"{width}px 匿名: {exc}"]
    finally:
        context.close()


def run_signed_in(browser, width: int, height: int, base_url: str, cookie: str,
                  cookie_name: str, out_dir: Path) -> list[str]:
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.add_cookies([{
        "name": cookie_name, "value": cookie, "url": base_url,
        "httpOnly": True, "sameSite": "Lax",
    }])
    page = context.new_page()
    tag = str(width)
    try:
        resp = page.goto(base_url + "/", wait_until="domcontentloaded")
        if resp is None or resp.status != 200:
            raise SmokeFailure(f"{width}px 已登录: / status={getattr(resp, 'status', None)}")
        page.locator("#app").wait_for(state="visible", timeout=15000)
        page.wait_for_function("() => window.__YXQ_AUTHENTICATED__ === true", timeout=15000)
        if page.locator(".home-hero").count():
            raise SmokeFailure(f"{width}px 已登录: / 仍是公开首页，没有切到 SPA")
        page.screenshot(path=str(out_dir / f"home-signed-in-{tag}.png"))
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"{width}px 已登录: {exc}"]
    finally:
        context.close()


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "").strip()
    cookie_name = os.environ.get("SESSION_COOKIE_NAME", "yxq_session").strip() or "yxq_session"
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/public-home-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for width, height in VIEWPORTS:
                failures.extend(run_anonymous(browser, width, height, base_url, out_dir))
                if cookie:
                    failures.extend(run_signed_in(
                        browser, width, height, base_url, cookie, cookie_name, out_dir,
                    ))
        finally:
            browser.close()

    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print("PASS")
    print(f"screenshots: {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
