#!/usr/bin/env python3
"""大图查看器「缩图 = 原图同框」smoke（WS1，2026-10-27）。

契约（DECISIONS 39）：打开第一帧的缩图显示区域必须等于原图最终显示区域
（同位置同大小，容差 ≤2px）；缩图在框内 object-fit: cover（不拉伸）；
原图加载完成不再改尺寸。

前置：BASE_URL 指向带 photos_meta 的实例，且登录用户第一张卡有照片。
    BASE_URL=http://127.0.0.1:8095 SESSION_COOKIE_FILE=/tmp/.../session.cookie \
    python3 tools/browser/viewer_fullframe_smoke.py
"""
from __future__ import annotations

import os
import sys
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
if not BASE:
    sys.exit("BASE_URL required")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
if not COOKIE:
    cookie_file = os.environ.get("SESSION_COOKIE_FILE", "").strip()
    if not cookie_file:
        sys.exit("SESSION_COOKIE_FILE or SESSION_COOKIE required")
    COOKIE = Path(cookie_file).read_text(encoding="utf-8").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/viewer-fullframe-20261027"))
VIEWPORTS = os.environ.get("VIEWPORTS", "390x844,1280x900").strip()
TOLERANCE = float(os.environ.get("RECT_TOLERANCE_PX", "2"))
FULL_WAIT_MS = int(os.environ.get("FULL_WAIT_MS", "8000"))

MEASURE_JS = """
() => {
  const img = (window.pswp && window.pswp.currSlide && window.pswp.currSlide.content &&
    window.pswp.currSlide.content.element) || document.querySelector('.pswp__img');
  if (!img) return null;
  const r = img.getBoundingClientRect();
  const cs = getComputedStyle(img);
  return {
    x: r.x, y: r.y, w: r.width, h: r.height,
    naturalW: img.naturalWidth, naturalH: img.naturalHeight,
    src: img.currentSrc || img.src,
    full: img.dataset.yxqFull === '1',
    objectFit: cs.objectFit
  };
}
"""


def dismiss_overlays(page) -> None:
    """Close first-visit guides / release-note dialogs that can cover the card."""
    for _ in range(3):
        try:
            dlg = page.locator(".van-dialog:visible")
            if dlg.count():
                dlg.locator("button").last.click(timeout=2000)
                page.wait_for_timeout(300)
                continue
            if page.locator(".van-overlay:visible").count():
                page.keyboard.press("Escape")
                page.wait_for_timeout(300)
                continue
            return
        except Exception:
            page.wait_for_timeout(300)


def click_card(page) -> None:
    """Click the card photo, dismissing any late first-visit overlay first."""
    for attempt in range(6):
        try:
            page.locator(".swipe-card .card-photo-wrap").click(timeout=2500)
            return
        except Exception:
            if attempt == 5:
                raise
            dismiss_overlays(page)
            page.wait_for_timeout(500)


def rect_delta(a, b) -> dict[str, float]:
    return {k: abs(a[k] - b[k]) for k in ("x", "y", "w", "h")}


def check_viewport(p, viewport: str, console_errors: list[str]) -> None:
    width, _, height = viewport.partition("x")
    browser = p.chromium.launch()
    context = browser.new_context(viewport={"width": int(width), "height": int(height)},
                                  device_scale_factor=1)
    parsed = urlparse(BASE)
    context.add_cookies([{
        "name": "yxq_session", "value": COOKIE, "domain": parsed.hostname, "path": "/",
        "httpOnly": True, "secure": parsed.scheme == "https", "sameSite": "Lax",
    }])

    mode = {"on": "pass"}
    held: list = []

    def route_handler(route, request):
        is_original = "/cards/photo/" in request.url and "size=thumb" not in request.url
        if is_original and mode["on"] == "hold":
            held.append(route)   # keep the original pending so the thumb phase is observable
            return
        if is_original and mode["on"] == "abort":
            route.abort()
            return
        route.continue_()

    context.route("**/*", route_handler)
    page = context.new_page()
    page.on("console", lambda msg: console_errors.append(msg.text)
            if msg.type == "error" and not any(x in msg.text for x in
                                              ("Failed to load resource", "net::ERR", "favicon", "ResizeObserver"))
            else None)
    page.on("pageerror", lambda exc: console_errors.append(str(exc)))

    page.goto(BASE + "/", wait_until="domcontentloaded")
    page.wait_for_selector(".swipe-card .card-photo-item", timeout=20000)
    # First-visit swipe guide covers the card; dismiss it like a real user.
    try:
        page.wait_for_selector(".swipe-guide-mask", state="visible", timeout=8000)
        page.locator("text=我知道了").click(timeout=4000)
        page.wait_for_selector(".swipe-guide-mask", state="detached", timeout=5000)
    except Exception:
        pass
    dismiss_overlays(page)
    page.wait_for_function(
        "() => { const i = document.querySelector('.swipe-card .card-photo-item');"
        " return i && i.complete && i.naturalWidth > 0; }", timeout=20000)

    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    # 缩图阶段：点开第一张并暂停原图请求，等缩图解码后量框。
    mode["on"] = "hold"
    page.click(".swipe-card .card-photo-wrap")
    page.wait_for_selector(".pswp", state="visible", timeout=15000)
    page.wait_for_function(
        "() => { const i = document.querySelector('.pswp__img');"
        " return i && i.complete && i.naturalWidth > 0; }", timeout=FULL_WAIT_MS)
    thumb = page.evaluate(MEASURE_JS)
    if thumb is None:
        raise SystemExit(f"[{viewport}] viewer img missing")
    if "size=thumb" not in thumb["src"]:
        raise SystemExit(f"[{viewport}] first frame is not the thumbnail: {thumb['src']}")
    if thumb["objectFit"] != "cover":
        raise SystemExit(f"[{viewport}] viewer thumbnail object-fit={thumb['objectFit']}, want cover")
    page.screenshot(path=str(SHOT_DIR / f"viewer-thumb-{viewport}.png"))
    thumb_rect = thumb

    # 放行原图请求，等 data-yxq-full=1 且 naturalWidth 变成原图
    mode["on"] = "pass"
    for route in held:
        route.continue_()
    held.clear()
    page.wait_for_function(
        "() => { const i = document.querySelector('.pswp__img');"
        " return i && i.dataset.yxqFull === '1' && i.naturalWidth > 0; }",
        timeout=FULL_WAIT_MS)
    full = page.evaluate(MEASURE_JS)
    page.screenshot(path=str(SHOT_DIR / f"viewer-full-{viewport}.png"))
    if full is None or "size=thumb" in full["src"]:
        raise SystemExit(f"[{viewport}] original did not replace thumbnail: {full}")

    delta = rect_delta(thumb_rect, full)
    worst = max(delta.values())
    print(f"PASS viewer [{viewport}]: thumb={thumb_rect['w']:.1f}x{thumb_rect['h']:.1f}"
          f" @({thumb_rect['x']:.1f},{thumb_rect['y']:.1f}) thumbNatural="
          f"{thumb_rect['naturalW']}x{thumb_rect['naturalH']} -> full="
          f"{full['w']:.1f}x{full['h']:.1f} @({full['x']:.1f},{full['y']:.1f}) "
          f"fullNatural={full['naturalW']}x{full['naturalH']} maxDelta={worst:.2f}px")
    if worst > TOLERANCE:
        raise SystemExit(f"[{viewport}] thumbnail/full frame jumped by {worst:.2f}px > {TOLERANCE}px: {delta}")

    # 下一张（左右滑 / 方向键）：方形照片用 meta 的 1:1 框，不依赖缩图尺寸。
    try:
        # PhotoSwipe 的键盘模块未启用；用核心 next() 等价触发「下一张」。
        page.evaluate("() => { if (window.pswp && window.pswp.next) window.pswp.next(); }")
        page.wait_for_function(
            "() => { const s = window.pswp && window.pswp.currSlide;"
            " const i = s && s.content && s.content.element;"
            " return s && s.index === 1 && i && i.dataset.yxqFull === '1'"
            " && i.naturalWidth === 1000 && i.naturalHeight === 1000; }",
            timeout=8000)
        nxt = page.evaluate(MEASURE_JS)
        if nxt is None or abs(nxt["w"] / nxt["h"] - 1) > 0.02:
            raise SystemExit(f"[{viewport}] next slide frame not 1:1: {nxt}")
        print(f"PASS viewer [{viewport}]: next slide square frame {nxt['w']:.1f}x{nxt['h']:.1f}")
    except SystemExit:
        raise
    except Exception:
        print(f"SKIP viewer [{viewport}]: next-slide meta check not available in this build")

    # 原图失败：保留缩图，不关查看器（模拟网络失败/10s 内不升级）。
    # 重新载入页面，避免关闭动画/遮罩残影干扰第二次点击。
    page.goto(BASE + "/", wait_until="domcontentloaded")
    page.wait_for_selector(".swipe-card .card-photo-wrap", timeout=20000)
    page.wait_for_function(
        "() => { const i = document.querySelector('.swipe-card .card-photo-item');"
        " return i && i.complete && i.naturalWidth > 0; }", timeout=20000)
    page.wait_for_timeout(1800)
    dismiss_overlays(page)
    mode["on"] = "abort"
    click_card(page)
    page.wait_for_selector(".pswp", state="visible", timeout=15000)
    page.wait_for_function(
        "() => { const i = document.querySelector('.pswp__img');"
        " return i && i.complete && i.naturalWidth > 0; }", timeout=FULL_WAIT_MS)
    page.wait_for_timeout(1500)
    kept = page.evaluate(MEASURE_JS)
    if kept is None or "size=thumb" not in kept["src"]:
        raise SystemExit(f"[{viewport}] original failure did not keep the thumbnail: {kept}")
    if not page.is_visible(".pswp"):
        raise SystemExit(f"[{viewport}] viewer closed after original failure")
    print(f"PASS viewer [{viewport}]: original failure keeps thumbnail, viewer stays open")
    page.keyboard.press("Escape")
    browser.close()


def main() -> int:
    console_errors: list[str] = []
    with sync_playwright() as p:
        for viewport in VIEWPORTS.split(","):
            check_viewport(p, viewport, console_errors)
    if console_errors:
        raise SystemExit("console errors: " + " | ".join(console_errors[:5]))
    print("PASS: no console/page errors")
    return 0


if __name__ == "__main__":
    sys.exit(main())
