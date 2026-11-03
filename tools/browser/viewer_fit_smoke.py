#!/usr/bin/env python3
"""查看大图：小图在大屏上应等比放大到至少占满宽/高的一边，且不裁切。

用法：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/viewer_fit_smoke.py
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


def uuid_of(url: str) -> str:
    try:
        return url.split("/cards/photo/")[1].split("?")[0]
    except Exception:
        return ""


def measure(page):
    return page.evaluate(
        """() => {
          const pswp = window.pswp, s = pswp && pswp.currSlide, c = s && s.content, el = c && c.element;
          const r = el && el.getBoundingClientRect();
          return {
            vw: innerWidth, vh: innerHeight,
            rect: r && { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) },
            nw: el && el.naturalWidth, nh: el && el.naturalHeight,
            full: !!(r && r.x >= -1 && r.y >= -1 && r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1)
          };
        }"""
    )


def assert_fit(tag: str, m):
    if not m["full"]:
        raise AssertionError(f"{tag}: image is cropped/outside viewport: {m}")
    if not (abs(m["rect"]["w"] - m["vw"]) <= 2 or abs(m["rect"]["h"] - m["vh"]) <= 2):
        raise AssertionError(f"{tag}: image does not fill width or height: {m}")
    if m["nw"] and m["nh"]:
        ratio = m["rect"]["w"] / m["rect"]["h"]
        want = m["nw"] / m["nh"]
        if abs(ratio - want) > 0.01 * max(ratio, want):
            raise AssertionError(f"{tag}: image aspect changed: {ratio} vs {want}")


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
        page.goto(BASE + "/app?viewer-fit=" + str(os.getpid()), wait_until="domcontentloaded", timeout=60000)
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
        target = None
        for c in cards:
            m = (c.get("photos_meta") or [{}])[0]
            thumbs = c.get("photos_thumb") or []
            if m.get("w") and m.get("h") and m["w"] <= 600 and m["h"] > m["w"] and thumbs:
                target = uuid_of(thumbs[0])
                break
        if not target:
            raise AssertionError("没有找到可测的小尺寸竖图（w<=600）")

        for _ in range(20):
            cur = page.evaluate("""() => { const i=document.querySelector('.swipe-card .card-photo-item');
              return i ? (i.currentSrc || i.src) : ''; }""")
            if target in cur:
                break
            page.keyboard.press("ArrowRight")
            page.wait_for_timeout(350)
        else:
            raise AssertionError("未能滑到目标小图卡")

        page.wait_for_timeout(700)
        for _ in range(4):
            if page.locator(".van-overlay:visible").count():
                page.mouse.click(5, 5)
                page.wait_for_timeout(350)
        page.locator(".card-photo-wrap").click(position={"x": 40, "y": 40})
        page.wait_for_selector(".pswp", timeout=15000)
        page.wait_for_timeout(1800)
        small = measure(page)
        assert_fit("390-open", small)

        page.set_viewport_size({"width": 768, "height": 824})
        page.wait_for_timeout(900)
        big = measure(page)
        assert_fit("unfold-768", big)
        if abs(big["rect"]["w"] - small["rect"]["w"]) < 100 and abs(big["rect"]["h"] - small["rect"]["h"]) < 100:
            raise AssertionError(f"大屏没有放大：390={small['rect']} 768={big['rect']}")

        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(900)
        back = measure(page)
        assert_fit("fold-390", back)

        # 双击应放大到 2x，再双击回 1x（不依赖捏合）
        page.touchscreen.tap(195, 420)
        page.wait_for_timeout(80)
        page.touchscreen.tap(195, 420)
        page.wait_for_timeout(500)
        z1 = page.evaluate("() => window.pswp.currSlide.currZoomLevel")
        page.touchscreen.tap(195, 420)
        page.wait_for_timeout(80)
        page.touchscreen.tap(195, 420)
        page.wait_for_timeout(500)
        z2 = page.evaluate("() => window.pswp.currSlide.currZoomLevel")
        if abs(z1 - 2) > 0.1 or abs(z2 - 1) > 0.1:
            raise AssertionError(f"double tap zoom failed: {z1} -> {z2}")

        print("PASS viewer-fit", {"390": small["rect"], "768": big["rect"], "back": back["rect"], "doubleTap": [z1, z2]})
        ctx.close()
        browser.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
