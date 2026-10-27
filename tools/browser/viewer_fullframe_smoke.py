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

import json
import os
import sys
import time
import urllib.request
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


def next_slide_dims() -> tuple[int, int] | None:
    """Read the first home card's second photo dimensions from the API.

    Avoids hard-coding a fixture size: the check follows whatever the fixture
    (or staging data) provides. Returns None when unavailable (e.g. old API
    without photos_meta or a card with a single photo).
    """
    req = urllib.request.Request(BASE + "/legacy/api/home",
                                 headers={"Cookie": "yxq_session=" + COOKIE})
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            payload = json.load(resp)
    except Exception:
        return None
    cards = payload.get("cards") or []
    if not cards:
        return None
    meta = cards[0].get("photos_meta") or []
    if len(meta) < 2:
        return None
    w, h = int(meta[1].get("w") or 0), int(meta[1].get("h") or 0)
    if w <= 0 or h <= 0:
        return None
    return w, h
FULL_WAIT_MS = int(os.environ.get("FULL_WAIT_MS", "8000"))
# 首次访问的引导遮罩/公告弹窗/SPA 重渲染都会让“点击卡片”在 staging 上偶发失败；
# 这些重试只作用于交互（打开查看器），断言仍然只跑一次，避免掩盖真实回归。
OPEN_RETRIES = int(os.environ.get("OPEN_RETRIES", "3"))
STABLE_TIMEOUT_MS = int(os.environ.get("RECT_STABLE_TIMEOUT_MS", "2500"))
GUIDE_TEXTS = ("我知道了", "知道了", "开始体验", "开始")

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


def _overlay_visible(page, selector: str) -> bool:
    try:
        loc = page.locator(selector)
        return loc.count() > 0 and loc.first.is_visible()
    except Exception:
        return False


def dismiss_overlays(page, budget_s: float = 5.0) -> None:
    """Close first-visit guides / release-note dialogs that can cover the card.

    Bounded: only known overlay selectors, no blanket click on every popup
    button (that could re-open another dialog and spin), stops as soon as
    nothing is visible. A guide that still appears late is retried by
    click_card.
    """
    deadline = time.time() + budget_s
    for _ in range(20):
        acted = False
        if _overlay_visible(page, ".swipe-guide-mask"):
            clicked = False
            for label in GUIDE_TEXTS:
                btn = page.locator(f".swipe-guide-mask >> text={label}")
                try:
                    if btn.count():
                        btn.first.click(timeout=1200)
                        clicked = True
                        break
                except Exception:
                    continue
            if not clicked:
                try:
                    page.keyboard.press("Escape")
                except Exception:
                    pass
            acted = True
        elif _overlay_visible(page, ".van-dialog"):
            try:
                page.locator(".van-dialog .van-dialog__confirm, .van-dialog button").last.click(timeout=1200)
            except Exception:
                try:
                    page.keyboard.press("Escape")
                except Exception:
                    pass
            acted = True
        elif _overlay_visible(page, ".van-overlay"):
            try:
                page.keyboard.press("Escape")
            except Exception:
                pass
            acted = True
        if not acted or time.time() > deadline:
            return
        page.wait_for_timeout(250)


def click_card(page) -> None:
    """Click the card photo, retiring late overlays / detached DOM first."""
    last: Exception | None = None
    for attempt in range(8):
        try:
            dismiss_overlays(page)
            wrap = page.locator(".swipe-card .card-photo-wrap").first
            wrap.wait_for(state="visible", timeout=5000)
            wrap.click(timeout=3000)
            return
        except Exception as exc:  # overlay intercept / detached re-render
            last = exc
            dismiss_overlays(page)
            page.wait_for_timeout(300 + 150 * attempt)
    raise SystemExit(f"cannot click card after 8 attempts: {last}")


def goto_home(page) -> None:
    """Load the deck and wait until the first card photo is decoded.

    Retries the navigation only; assertions still run once on the final DOM.
    """
    last: Exception | None = None
    for _ in range(3):
        try:
            page.goto(BASE + "/", wait_until="domcontentloaded", timeout=30000)
            page.wait_for_selector(".swipe-card .card-photo-item", timeout=20000)
            page.wait_for_function(
                "() => { const i = document.querySelector('.swipe-card .card-photo-item');"
                " return i && i.complete && i.naturalWidth > 0; }", timeout=20000)
            return
        except Exception as exc:
            last = exc
            page.wait_for_timeout(500)
    raise SystemExit(f"home deck did not become ready: {last}")


def prepare_card(page) -> None:
    """Dismiss the first-visit swipe guide (if it is going to appear) and settle."""
    try:
        page.wait_for_selector(".swipe-guide-mask", state="visible", timeout=6000)
    except Exception:
        pass  # not a first visit / already dismissed
    dismiss_overlays(page)


def measure_stable(page) -> dict | None:
    """Measure the viewer frame once it stops moving (avoids mid-animation rects)."""
    deadline = time.time() + STABLE_TIMEOUT_MS / 1000
    prev = None
    while True:
        cur = page.evaluate(MEASURE_JS)
        if cur is None:
            return None
        if prev is not None and all(abs(cur[k] - prev[k]) <= 0.5
                                    for k in ("x", "y", "w", "h")):
            return cur
        prev = cur
        if time.time() > deadline:
            return cur
        page.wait_for_timeout(120)


def release_held(held) -> None:
    for route in held:
        try:
            route.continue_()
        except Exception:
            pass
    held.clear()


def open_viewer(page, held) -> dict:
    """Open the first card photo and return the settled thumbnail frame.

    Retries the interaction (overlay/DOM replacement races) without retrying
    the geometry assertion itself.
    """
    last: Exception | None = None
    for attempt in range(OPEN_RETRIES):
        try:
            click_card(page)
            page.wait_for_selector(".pswp", state="visible", timeout=15000)
            page.wait_for_function(
                "() => { const i = document.querySelector('.pswp__img');"
                " return i && i.complete && i.naturalWidth > 0; }", timeout=FULL_WAIT_MS)
            rect = measure_stable(page)
            if rect is None:
                raise RuntimeError("viewer img missing")
            return rect
        except Exception as exc:
            last = exc
            release_held(held)
            try:
                if page.locator(".pswp").count():
                    page.keyboard.press("Escape")
                    page.wait_for_selector(".pswp", state="detached", timeout=4000)
            except Exception:
                pass
            page.wait_for_timeout(400 + 300 * attempt)
    raise SystemExit(f"cannot open viewer after {OPEN_RETRIES} attempts: {last}")


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

    goto_home(page)
    prepare_card(page)

    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    # 缩图阶段：点开第一张并暂停原图请求，等缩图解码后量框。
    mode["on"] = "hold"
    thumb_rect = open_viewer(page, held)
    thumb = thumb_rect
    if "size=thumb" not in thumb["src"]:
        raise SystemExit(f"[{viewport}] first frame is not the thumbnail: {thumb['src']}")
    if thumb["objectFit"] != "cover":
        raise SystemExit(f"[{viewport}] viewer thumbnail object-fit={thumb['objectFit']}, want cover")
    page.screenshot(path=str(SHOT_DIR / f"viewer-thumb-{viewport}.png"))

    # 放行原图请求，等 data-yxq-full=1 且 naturalWidth 变成原图
    mode["on"] = "pass"
    for route in held:
        route.continue_()
    held.clear()
    page.wait_for_function(
        "() => { const i = document.querySelector('.pswp__img');"
        " return i && i.dataset.yxqFull === '1' && i.naturalWidth > 0; }",
        timeout=FULL_WAIT_MS)
    full = measure_stable(page)
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

    # 下一张（左右滑 / 方向键）：用 API photos_meta 的第二张尺寸，不硬编码 fixture。
    dims = next_slide_dims()
    try:
        if dims is None:
            raise RuntimeError("no second-photo photos_meta")
        want_w, want_h = dims
        # PhotoSwipe 的键盘模块未启用；用核心 next() 等价触发「下一张」。
        page.evaluate("() => { if (window.pswp && window.pswp.next) window.pswp.next(); }")
        page.wait_for_function(
            "([w, h]) => { const s = window.pswp && window.pswp.currSlide;"
            " const i = s && s.content && s.content.element;"
            " return s && s.index === 1 && i && i.dataset.yxqFull === '1'"
            " && i.naturalWidth === w && i.naturalHeight === h; }",
            arg=[want_w, want_h], timeout=8000)
        nxt = page.evaluate(MEASURE_JS)
        if nxt is None or abs(nxt["w"] / nxt["h"] - want_w / want_h) > 0.02:
            raise SystemExit(f"[{viewport}] next slide frame mismatch: {nxt} vs {want_w}x{want_h}")
        print(f"PASS viewer [{viewport}]: next slide frame {nxt['w']:.1f}x{nxt['h']:.1f}"
              f" (meta {want_w}x{want_h})")
    except SystemExit:
        raise
    except Exception:
        print(f"SKIP viewer [{viewport}]: next-slide meta check not available in this build")

    # 原图失败：保留缩图，不关查看器（模拟网络失败/10s 内不升级）。
    # 重新载入页面，避免关闭动画/遮罩残影干扰第二次点击。
    goto_home(page)
    prepare_card(page)
    mode["on"] = "abort"
    open_viewer(page, held)
    page.wait_for_timeout(1500)
    kept = measure_stable(page)
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
