#!/usr/bin/env python3
"""卡上多图横滑：过渡期旧图不应变 ghost，切卡不应出现 loading 闪烁。

用法：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/card_loading_smoke.py
"""

from __future__ import annotations

import os
import sys

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").strip().rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
if not BASE or not COOKIE:
    sys.exit("缺少 BASE_URL 或 SESSION_COOKIE")


def main() -> int:
    with sync_playwright() as pw:
        browser = pw.chromium.launch(args=["--no-sandbox"])
        ctx = browser.new_context(
            viewport={"width": 390, "height": 844}, device_scale_factor=2,
            locale="zh-CN", has_touch=True, is_mobile=True,
        )
        ctx.add_cookies([{
            "name": "yxq_session", "value": COOKIE, "url": BASE,
            "httpOnly": True, "sameSite": "Lax",
        }])
        page = ctx.new_page()
        page.goto(BASE + "/app?card-loading=" + str(os.getpid()), wait_until="domcontentloaded", timeout=60000)
        page.wait_for_selector(".van-tabbar", timeout=30000)
        page.wait_for_selector(".swipe-card .card-photo-item", timeout=30000)
        try:
            if page.is_visible(".sg-btn"):
                page.click(".sg-btn")
        except Exception:
            pass
        page.wait_for_timeout(1200)
        for _ in range(3):
            if page.locator(".van-overlay:visible").count():
                page.mouse.click(5, 5)
                page.wait_for_timeout(300)

        cards = page.evaluate("""async () => (await (await fetch('/legacy/api/home')).json()).cards || []""")
        multi = next((c for c in cards if len(c.get("photos") or []) > 1), None)
        if not multi:
            raise AssertionError("没有多图卡可测")
        target = (multi.get("photos_thumb") or [""])[0].split("/cards/photo/")[-1].split("?")[0]
        for _ in range(20):
            cur = page.evaluate("""() => { const i=document.querySelector('.swipe-card .card-photo-item');
              return i ? (i.currentSrc || i.src) : ''; }""")
            if target in cur:
                break
            page.keyboard.press("ArrowRight")
            page.wait_for_timeout(350)
        else:
            raise AssertionError("未能滑到多图卡")
        page.wait_for_timeout(1500)

        box = page.locator(".card-photo-wrap").bounding_box()
        page.mouse.move(box["x"] + box["width"] * 0.8, box["y"] + box["height"] * 0.5)
        page.mouse.down()
        for i in range(1, 6):
            page.mouse.move(box["x"] + box["width"] * (0.8 - 0.12 * i), box["y"] + box["height"] * 0.5)
            page.wait_for_timeout(16)
        page.mouse.up()
        page.wait_for_timeout(120)
        state = page.evaluate("""() => ({
          imgs: document.querySelectorAll('.card-photo-track img.card-photo-item').length,
          ghosts: document.querySelectorAll('.card-photo-track .card-photo-ghost').length,
          loading: !!document.querySelector('.photo-loading')
        })""")
        if state["ghosts"]:
            raise AssertionError(f"多图过渡出现 ghost：{state}")
        if state["loading"]:
            raise AssertionError(f"多图过渡出现 loading 占位：{state}")
        if state["imgs"] < 2:
            raise AssertionError(f"多图过渡旧图丢失：{state}")

        page.wait_for_timeout(100)
        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(120)
        card_state = page.evaluate("""() => {
          const i=document.querySelector('.swipe-card .card-photo-item');
          return { complete: !!(i && i.complete && i.naturalWidth), loading: !!document.querySelector('.photo-loading') };
        }""")
        if card_state["loading"] or not card_state["complete"]:
            raise AssertionError(f"切卡出现 loading/空白：{card_state}")
        print("PASS card-loading", state, card_state)
        ctx.close()
        browser.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
