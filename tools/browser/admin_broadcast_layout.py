#!/usr/bin/env python3
"""管理端广播预览/错误的视口回归（Playwright）。

后台广播的“预览收件人”和错误提示都是 POST 直接渲染的页面。表单 action 必须带
#broadcast，否则浏览器把结果页停在顶部，确认卡/送出按钮/错误提示都在视口外。
本脚本在 390×844 与 1280×900 两个视口验证：

1. 预览后 URL 保留 #broadcast，scrollY > 0；
2. 「确认发送」卡与送出按钮完整落在视口内；
3. 发送前改动收件人群组触发 400 错误，错误提示也在同一视口内。

需要已启动的服务、已登录的活动管理员 session，以及至少一个报名者（或观察员）。
它依赖真实环境，不放进 make ci；手动运行：

    BASE_URL=http://127.0.0.1:18095 \
    SESSION_COOKIE=<yxq_session 原始值> \
    ACTIVITY_ID=1 \
    python3 tools/browser/admin_broadcast_layout.py

依赖：pip install playwright && python3 -m playwright install chromium
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
TITLE = "预览滚动回归"
BODY = "预览后确认卡应停留在视口内。"


def visible_in_viewport(page, selector: str) -> dict:
    return page.evaluate(
        """(selector) => {
          const el = document.querySelector(selector);
          if (!el) return {visible: false, missing: true};
          const r = el.getBoundingClientRect();
          return {
            visible: r.top >= 0 && r.bottom <= innerHeight,
            top: Math.round(r.top),
            bottom: Math.round(r.bottom),
            viewport: innerHeight,
          };
        }""",
        selector,
    )


def check(condition: bool, message: str, failures: list[str]) -> None:
    if not condition:
        failures.append(message)


def run_viewport(browser, viewport: tuple[int, int], base_url: str, cookie: str,
                 activity_id: str, out_dir: Path) -> list[str]:
    width, height = viewport
    ctx = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    ctx.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    page = ctx.new_page()
    failures: list[str] = []
    workbench = f"{base_url}/admin/activities/{activity_id}"
    tag = f"{width}"

    page.goto(workbench + "#broadcast", wait_until="networkidle")
    page.check("input[name=include_signups]")
    page.fill("input[name=broadcast_title]", TITLE)
    page.fill("textarea[name=broadcast_body]", BODY)
    page.locator("button[name=broadcast_action][value=preview]").scroll_into_view_if_needed()
    page.click("button[name=broadcast_action][value=preview]")
    page.wait_for_load_state("networkidle")
    page.wait_for_timeout(300)

    after_preview = page.evaluate(
        """() => ({hash: location.hash, scrollY: Math.round(window.scrollY),
                   url: location.href})"""
    )
    confirm = visible_in_viewport(page, ".admin-confirm")
    send = visible_in_viewport(page, 'button[name=broadcast_action][value=send]')
    page.screenshot(path=str(out_dir / f"preview-{tag}.png"), full_page=False)

    check(after_preview["hash"] == "#broadcast", f"{tag}: 预览后 URL 缺 #broadcast: {after_preview['url']}", failures)
    check(after_preview["scrollY"] > 0, f"{tag}: 预览后仍停在页首 scrollY=0", failures)
    check(confirm.get("visible", False), f"{tag}: 确认卡不在视口内: {confirm}", failures)
    check(send.get("visible", False), f"{tag}: 送出按钮不在视口内: {send}", failures)

    # 预览后改动收件人群组再发送，服务端 400 重新渲染；错误提示必须仍在视口内。
    # 一定翻转观察员勾选状态，保证与预览 token 的群组不一致，绝不真的发出广播。
    observer = page.locator("input[name=include_observers]")
    with page.expect_navigation():
        if observer.is_checked():
            observer.uncheck()
        else:
            observer.check()
        page.click('button[name=broadcast_action][value=send]')
    page.wait_for_load_state("networkidle")
    page.wait_for_timeout(300)
    after_error = page.evaluate(
        """() => ({hash: location.hash, scrollY: Math.round(window.scrollY),
                   url: location.href})"""
    )
    alert = visible_in_viewport(page, "#broadcast [role=alert]")
    page.screenshot(path=str(out_dir / f"error-{tag}.png"), full_page=False)

    check(after_error["hash"] == "#broadcast", f"{tag}: 错误页 URL 缺 #broadcast: {after_error['url']}", failures)
    check(after_error["scrollY"] > 0, f"{tag}: 错误页仍停在页首 scrollY=0", failures)
    check(alert.get("visible", False), f"{tag}: 错误提示不在视口内: {alert}", failures)

    print(json.dumps({
        "viewport": [width, height],
        "preview": {"url": after_preview["url"], "scrollY": after_preview["scrollY"],
                    "confirm": confirm, "send": send},
        "error": {"url": after_error["url"], "scrollY": after_error["scrollY"], "alert": alert},
        "failures": failures,
    }, ensure_ascii=False))
    ctx.close()
    return failures


def main() -> int:
    base_url = os.environ.get("BASE_URL", "").rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "")
    activity_id = os.environ.get("ACTIVITY_ID", "1")
    if not base_url or not cookie:
        sys.exit("BASE_URL 与 SESSION_COOKIE 必填（见脚本 docstring）")
    out_dir = Path(os.environ.get("SCREENSHOT_DIR") or tempfile.mkdtemp(prefix="yxq-broadcast-layout-"))
    out_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for viewport in VIEWPORTS:
                failures.extend(run_viewport(browser, viewport, base_url, cookie, activity_id, out_dir))
        finally:
            browser.close()
    if failures:
        print("\n".join("FAIL " + f for f in failures), file=sys.stderr)
        return 1
    print(f"OK 截图目录: {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
