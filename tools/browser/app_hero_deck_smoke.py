#!/usr/bin/env python3
"""纯品牌启动页 smoke：💗 + 一线牵，真实卡就绪后立即撤走。

    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \\
      SHOT_DIR=/tmp/opencode/splash python3 tools/browser/app_hero_deck_smoke.py
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

FAILS: list[str] = []


def cookie_context(browser, base: str, cookie: str, width: int, height: int, dark: bool = False):
    ctx = browser.new_context(viewport={"width": width, "height": height})
    ctx.add_cookies([{"name": "yxq_session", "value": cookie, "url": base,
                      "httpOnly": True, "sameSite": "Lax"}])
    if dark:
        ctx.add_init_script("try{localStorage.setItem('yxq_dark','1')}catch(e){}")
    return ctx


def one(browser, base: str, cookie: str, out: Path, width: int, height: int, dark: bool = False) -> None:
    tag = f"{width}x{height}" + ("-dark" if dark else "")
    ctx = cookie_context(browser, base, cookie, width, height, dark)
    page = ctx.new_page()
    errors: list[str] = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    try:
        page.goto(base + "/app", wait_until="domcontentloaded", timeout=40000)
        page.locator("#boot-splash").wait_for(state="visible", timeout=5000)
        text = page.locator("#boot-splash").inner_text()
        if "💗" not in text or "一线牵" not in text:
            raise RuntimeError(f"splash 文案异常：{text!r}")
        page.screenshot(path=str(out / f"splash-{tag}.png"))
        page.locator("#boot-splash").wait_for(state="detached", timeout=20000)
        page.locator("#app .cards-page .card-counter").wait_for(state="visible", timeout=20000)
        timing = page.evaluate("""() => ({
          ready: window.__YXQ_CARD_READY_AT__ || null,
          removed: window.__YXQ_HERO__ ? window.__YXQ_HERO__.removedAt() : null,
          reason: window.__YXQ_HERO__ ? window.__YXQ_HERO__.reason() : '',
        })""")
        if not timing["ready"] or not timing["removed"]:
            raise RuntimeError(f"缺少 ready/removed 时序：{timing}")
        gap = timing["removed"] - timing["ready"]
        if gap > 300:
            raise RuntimeError(f"splash 撤走过慢：removed-ready={gap}ms")
        if errors:
            raise RuntimeError(f"pageerror: {errors[:2]}")
        print(f"PASS {tag} ready/removed gap={gap}ms reason={timing['reason']}")
    except Exception as exc:
        FAILS.append(f"{tag}: {exc}")
        print(f"FAIL {tag}: {exc}")
    finally:
        ctx.close()


def no_splash(browser, base: str, cookie: str, path: str) -> None:
    ctx = cookie_context(browser, base, cookie, 390, 844)
    page = ctx.new_page()
    try:
        page.goto(base + path, wait_until="domcontentloaded", timeout=40000)
        page.wait_for_timeout(800)
        if page.locator("#boot-splash").count():
            FAILS.append(f"{path}: splash 不应显示")
        else:
            print(f"PASS no-splash {path}")
    except Exception as exc:
        FAILS.append(f"{path}: {exc}")
    finally:
        ctx.close()


def main() -> int:
    base = os.environ.get("BASE_URL", "").rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "")
    if not base or not cookie:
        sys.exit("BASE_URL/SESSION_COOKIE required")
    out = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/splash"))
    out.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as pw:
        browser = pw.chromium.launch(args=["--no-sandbox"])
        for w, h in ((390, 844), (1280, 900)):
            one(browser, base, cookie, out, w, h)
        one(browser, base, cookie, out, 390, 844, dark=True)
        no_splash(browser, base, cookie, "/app#/likes")
        no_splash(browser, base, cookie, "/app#/activities")
        no_splash(browser, base, cookie, "/app?native_admin=1&p=group_admin")
        browser.close()
    if FAILS:
        for f in FAILS:
            print("FAIL", f)
        return 1
    print("PASS splash smoke (390/1280, dark, deep-link, native_admin)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
