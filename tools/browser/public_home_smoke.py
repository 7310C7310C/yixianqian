#!/usr/bin/env python3
"""公开首页 / 登录页 Playwright 回归。

匿名（必跑）：
1. GET / 返回 200 且不跳转，渲染公开首页；
2. 注册是 primary block 主按钮，登录入口、飞书链接（target/rel）与二维码都在；
3. 点登录进入 /login，注册按钮仍是 primary block，飞书老用户说明存在。
已登录（设置 SESSION_COOKIE 时）：
4. GET / 仍是 v1 Vue SPA，__YXQ_AUTHENTICATED__=true，不渲染公开首页。

用法（staging）：
    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    SHOT_DIR=/tmp/opencode/public-home-smoke \
    python3 tools/browser/public_home_smoke.py
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
FEISHU_URL = "https://applink.feishu.cn/T996FhMkTERp"


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

        page.locator(".home-hero").wait_for(state="visible", timeout=10000)
        if page.locator("#app").count():
            raise SmokeFailure(f"{width}px: 匿名首页不应出现 SPA #app")

        register = page.locator('.home-hero a[href="/register?from=home"]')
        register.wait_for(state="visible", timeout=10000)
        assert_primary_block(register, f"{width}px 首页注册按钮")

        page.locator('.home-hero a[href="/login"]').wait_for(state="visible", timeout=10000)

        feishu = page.locator(f'.home-feishu a[href="{FEISHU_URL}"]')
        feishu.wait_for(state="visible", timeout=10000)
        if feishu.get_attribute("target") != "_blank":
            raise SmokeFailure(f"{width}px: 飞书链接缺少 target=_blank")
        if "noopener" not in (feishu.get_attribute("rel") or ""):
            raise SmokeFailure(f"{width}px: 飞书链接缺少 rel=noopener")

        qr = page.locator(".home-qr img")
        qr.wait_for(state="visible", timeout=10000)
        page.wait_for_function(
            "() => { const img = document.querySelector('.home-qr img');"
            " return !!img && img.complete && img.naturalWidth > 0; }",
            timeout=10000,
        )
        qr_src = qr.get_attribute("src") or ""
        if not qr_src.endswith("/static/feishu-qr.png"):
            raise SmokeFailure(f"{width}px: 二维码 src 不是固定资产（{qr_src}）")
        page.get_by_text("已在飞书版使用过", exact=False).first.wait_for(state="visible", timeout=10000)
        page.screenshot(path=str(out_dir / f"home-anon-{tag}.png"), full_page=True)

        page.locator('.home-hero a[href="/login"]').click()
        page.wait_for_url(lambda url: "/login" in url, timeout=10000)
        login_register = page.locator('a[href="/register"]')
        login_register.wait_for(state="visible", timeout=10000)
        assert_primary_block(login_register, f"{width}px 登录页注册按钮")
        page.get_by_text("已在飞书版使用过", exact=False).first.wait_for(state="visible", timeout=10000)
        page.screenshot(path=str(out_dir / f"login-anon-{tag}.png"), full_page=True)

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
