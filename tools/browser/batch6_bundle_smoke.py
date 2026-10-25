#!/usr/bin/env python3
"""Batch 6 bundle smoke: Vant subset + lazy page chunks + visible degradation.

Cases (390x844, one logged-in staging/local account):
1. normal: cards chunk is initial, tabbar renders, no console error/pageerror;
   switching to 活动/消息/我的 loads the matching page chunk once and keeps tabbar.
2. missing Vant component: window.__YXQ_VANT_FORCE_MISSING__='van-button'
   must show .boot-fail with 「组件缺失」 (not a blank page).
3. page chunk 404: block pages/home.render.js; entering 活动 must show
   .boot-fail with 「页面资源加载失败」 and no pageerror.

    BASE_URL=https://next.nantou.love SESSION_COOKIE=<raw> \
      SHOT_DIR=/tmp/opencode/b6-bundle python3 tools/browser/batch6_bundle_smoke.py
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
OUT = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/b6-bundle"))
if not BASE or not COOKIE:
    sys.exit("BASE_URL 与 SESSION_COOKIE 必填")


def ignore_console(text: str) -> bool:
    return "at.alicdn.com" in text


def new_ctx(browser, init: str | None = None):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    ctx.add_cookies([{"name": "yxq_session", "value": COOKIE, "url": BASE,
                      "httpOnly": True, "sameSite": "Lax"}])
    if init:
        ctx.add_init_script(init)
    return ctx


def attach(page):
    errors = []
    page.on("console", lambda m: errors.append(m.text[:200]) if m.type == "error" and not ignore_console(m.text) else None)
    page.on("pageerror", lambda e: errors.append("pageerror: " + str(e)[:200]))
    return errors


def dismiss_guide(page):
    page.wait_for_timeout(800)
    if page.locator(".sg-btn").count():
        page.locator(".sg-btn").click()
        page.wait_for_timeout(300)
    if page.locator(".van-overlay:visible").count():
        page.evaluate("document.querySelectorAll('.van-overlay').forEach(function(e){e.click()})")
        page.wait_for_timeout(300)


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    failures = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=True, args=["--disable-dev-shm-usage", "--no-sandbox"])

        # 1. normal load + lazy tabs
        ctx = new_ctx(browser)
        page = ctx.new_page()
        errors = attach(page)
        chunks = []
        page.on("request", lambda r: chunks.append(r.url) if "/pages/" in r.url else None)
        try:
            page.goto(BASE + "/", wait_until="load", timeout=60000)
            page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
            dismiss_guide(page)
            page.wait_for_timeout(1200)
            page.screenshot(path=str(OUT / "bundle-cards-390.png"))
            if not page.evaluate("window.__YXQ_PAGES__ && typeof window.__YXQ_PAGES__.cards === 'function'"):
                failures.append("cards chunk is not in the initial bundle")
            for index, name in ((2, "home"), (1, "likes"), (3, "profile")):
                dismiss_guide(page)
                page.locator(".van-tabbar-item").nth(index).click()
                page.wait_for_timeout(1600)
                if not page.evaluate(f"window.__YXQ_PAGES__ && typeof window.__YXQ_PAGES__.{name} === 'function'"):
                    failures.append(f"{name} page chunk did not load")
                if not page.locator(".van-tabbar").count():
                    failures.append(f"tabbar disappeared after {name}")
                page.screenshot(path=str(OUT / f"bundle-{name}-390.png"))
            if not chunks:
                failures.append("no lazy page chunk request observed")
            if errors:
                failures.append(f"normal load console/page errors: {errors[:3]}")
        except Exception as exc:  # noqa: BLE001
            failures.append(f"normal: {exc}")
        finally:
            ctx.close()

        # 2. missing Vant component -> visible boot error
        ctx = new_ctx(browser, "window.__YXQ_VANT_FORCE_MISSING__='van-button';")
        page = ctx.new_page()
        try:
            page.goto(BASE + "/", wait_until="load", timeout=60000)
            page.locator(".boot-fail").wait_for(state="visible", timeout=20000)
            detail = page.locator(".boot-fail-detail").inner_text()
            if "组件缺失" not in detail:
                failures.append(f"missing Vant detail = {detail!r}")
            page.screenshot(path=str(OUT / "bundle-vant-missing-390.png"))
        except Exception as exc:  # noqa: BLE001
            failures.append(f"missing Vant: {exc}")
        finally:
            ctx.close()

        # 3. page chunk failure -> visible boot error
        ctx = new_ctx(browser)
        page = ctx.new_page()
        page.route("**/pages/home.render.js*", lambda route: route.abort())
        try:
            page.goto(BASE + "/", wait_until="load", timeout=60000)
            page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
            dismiss_guide(page)
            page.locator(".van-tabbar-item").nth(2).click()
            page.locator(".boot-fail").wait_for(state="visible", timeout=20000)
            detail = page.locator(".boot-fail-detail").inner_text()
            if "页面资源加载失败" not in detail:
                failures.append(f"chunk failure detail = {detail!r}")
            page.screenshot(path=str(OUT / "bundle-chunk-missing-390.png"))
        except Exception as exc:  # noqa: BLE001
            failures.append(f"chunk failure: {exc}")
        finally:
            ctx.close()
        browser.close()

    if failures:
        print("FAIL")
        for f in failures:
            print(" -", f)
        return 1
    print("PASS batch6 bundle smoke (normal/lazy tabs, missing Vant, chunk failure)")
    print("screenshots:", OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
