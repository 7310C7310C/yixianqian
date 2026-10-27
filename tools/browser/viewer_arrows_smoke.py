#!/usr/bin/env python3
"""大图左右按键 + 循环查看 smoke（2026-10-28）。

契约：
- 大图查看器在 >=2 张时显示左右按键（触摸端也可见），单张不显示；
- 左右按键可从第一张跳到末张、从末张跳回第一张（首尾循环）；
- 点击按键不关闭查看器。

首页牌堆没有单图卡时只跳过单图断言并打印 SKIP（多图契约仍强制）。

前置：BASE_URL 指向登录态可用的实例，且首页牌堆里至少有一张多图卡片。
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
    python3 tools/browser/viewer_arrows_smoke.py
"""
from __future__ import annotations

import json
import os
import sys
import urllib.request
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

sys.path.insert(0, str(Path(__file__).resolve().parent))
import guide_guard  # noqa: E402

BASE = os.environ.get("BASE_URL", "").rstrip("/")
if not BASE:
    sys.exit("BASE_URL required")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
if not COOKIE:
    cookie_file = os.environ.get("SESSION_COOKIE_FILE", "").strip()
    if not cookie_file:
        sys.exit("SESSION_COOKIE_FILE or SESSION_COOKIE required")
    COOKIE = Path(cookie_file).read_text(encoding="utf-8").strip()

VIEWPORTS = os.environ.get("VIEWPORTS", "390x844,1280x900").strip()
# 截图含真实会员照片：默认不落盘，只在显式 SHOT_DIR 时留档。
SHOT_DIR = os.environ.get("SHOT_DIR", "").strip()
OPEN_RETRIES = int(os.environ.get("OPEN_RETRIES", "3"))

ARROW_STATE_JS = """
() => {
  const p = window.pswp;
  const root = document.querySelector('.pswp');
  const arrows = Array.from(document.querySelectorAll('.yxq-viewer-arrow'));
  return {
    open: !!(p && p.opener && p.opener.isOpen),
    n: p ? p.getNumItems() : 0,
    index: p ? p.currIndex : -1,
    oneSlide: !!(root && root.classList.contains('pswp--one-slide')),
    arrows: arrows.map((a) => {
      const r = a.getBoundingClientRect();
      const cs = getComputedStyle(a);
      return {
        cls: a.className,
        label: a.getAttribute('aria-label') || '',
        x: r.x, y: r.y, w: r.width, h: r.height,
        display: cs.display, visibility: cs.visibility, opacity: cs.opacity,
        pointerEvents: cs.pointerEvents,
        bg: cs.backgroundColor, shadow: cs.boxShadow,
      };
    }),
    builtinArrows: document.querySelectorAll('.pswp__button--arrow').length,
  };
}
"""


def api_cards() -> list[dict]:
    req = urllib.request.Request(BASE + "/legacy/api/home",
                                 headers={"Cookie": "yxq_session=" + COOKIE})
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            payload = json.load(resp)
    except Exception as exc:  # pragma: no cover - 环境问题
        raise SystemExit(f"cannot read /legacy/api/home: {exc}")
    return payload.get("cards") or []


def card_photo_count(card: dict) -> int:
    photos = card.get("photos") or []
    if not photos and card.get("photo"):
        photos = [card.get("photo")]
    return len(photos)


def target_card_offset(cards: list[dict]) -> int | None:
    for i, card in enumerate(cards):
        if card_photo_count(card) >= 2:
            return i
    return None


def single_card_offset(cards: list[dict]) -> int | None:
    for i, card in enumerate(cards):
        if card_photo_count(card) == 1:
            return i
    return None


def goto_home(page) -> None:
    last: Exception | None = None
    for _ in range(3):
        try:
            page.goto(BASE + "/", wait_until="domcontentloaded", timeout=30000)
            page.wait_for_selector(".swipe-card .card-photo-item", timeout=20000)
            page.wait_for_function(
                "() => { const i = document.querySelector('.swipe-card .card-photo-item');"
                " return i && i.complete && i.naturalWidth > 0; }", timeout=20000)
            guide_guard.wait_boot_hero_gone(page)
            guide_guard.dismiss_guides(page)
            return
        except Exception as exc:  # overlay / late re-render
            last = exc
            page.wait_for_timeout(500)
    raise SystemExit(f"home deck did not become ready: {last}")


def drag_card_next(page) -> None:
    """鼠标/触摸横滑牌堆到下一张（走 SPA 自己的 switchCard 手势）。"""
    box = page.locator(".swipe-card .card-main").first.bounding_box()
    if not box:
        raise SystemExit("card-main not visible")
    y = box["y"] + box["height"] * 0.45
    x0 = box["x"] + box["width"] * 0.78
    x1 = box["x"] + box["width"] * 0.22
    page.mouse.move(x0, y)
    page.mouse.down()
    for i in range(1, 5):
        page.mouse.move(x0 + (x1 - x0) * i / 4, y, steps=3)
        page.wait_for_timeout(30)
    page.mouse.up()
    page.wait_for_timeout(500)


def open_viewer(page) -> None:
    last: Exception | None = None
    for attempt in range(OPEN_RETRIES):
        try:
            guide_guard.dismiss_guides(page)
            page.locator(".swipe-card .card-photo-wrap").first.click(timeout=4000)
            page.wait_for_selector(".pswp.pswp--open", state="visible", timeout=15000)
            page.wait_for_function(
                "() => { const i = document.querySelector('.pswp__img');"
                " return i && i.complete && i.naturalWidth > 0; }", timeout=10000)
            page.wait_for_selector(".pswp.pswp--ui-visible", timeout=4000)
            page.wait_for_function(
                "() => window.pswp && window.pswp.opener && window.pswp.opener.isOpen",
                timeout=8000)
            return
        except Exception as exc:
            last = exc
            try:
                page.keyboard.press("Escape")
                page.wait_for_selector(".pswp", state="detached", timeout=4000)
            except Exception:
                pass
            page.wait_for_timeout(300 + 250 * attempt)
    raise SystemExit(f"cannot open viewer after {OPEN_RETRIES} attempts: {last}")


def wait_index(page, want: int, timeout_ms: int = 6000) -> None:
    page.wait_for_function("(want) => window.pswp && window.pswp.currIndex === want",
                           arg=want, timeout=timeout_ms)


def press_arrow(page, side: str, touch: bool) -> None:
    loc = page.locator(f".yxq-viewer-arrow--{side}")
    if touch:
        loc.tap(timeout=4000)
    else:
        loc.click(timeout=4000)


def check_viewport(p, viewport: str, target_offset: int, single_offset: int | None,
                   console_errors: list[str]) -> None:
    width, _, height = viewport.partition("x")
    width, height = int(width), int(height)
    touch = width < 600
    browser = p.chromium.launch()
    context = browser.new_context(
        viewport={"width": width, "height": height},
        device_scale_factor=1,
        has_touch=touch,
        is_mobile=touch,
    )
    parsed = urlparse(BASE)
    context.add_cookies([{
        "name": "yxq_session", "value": COOKIE, "domain": parsed.hostname, "path": "/",
        "httpOnly": True, "secure": parsed.scheme == "https", "sameSite": "Lax",
    }])
    page = context.new_page()
    page.on("console", lambda msg: console_errors.append(msg.text)
            if msg.type == "error" and not any(x in msg.text for x in
                                              ("Failed to load resource", "net::ERR", "favicon", "ResizeObserver"))
            else None)
    page.on("pageerror", lambda exc: console_errors.append(str(exc)))

    goto_home(page)
    for _ in range(target_offset):
        drag_card_next(page)
        guide_guard.dismiss_guides(page, timeout_ms=2000)

    open_viewer(page)
    state = page.evaluate(ARROW_STATE_JS)
    n = int(state["n"])
    if n < 2:
        raise SystemExit(f"[{viewport}] opened a card with {n} photo(s), want >=2: {state}")
    if state["oneSlide"]:
        raise SystemExit(f"[{viewport}] viewer still marked one-slide with {n} photos")
    if len(state["arrows"]) != 2:
        raise SystemExit(f"[{viewport}] expected 2 custom arrows, got {len(state['arrows'])}: {state}")
    if state["builtinArrows"] != 0:
        raise SystemExit(f"[{viewport}] built-in PhotoSwipe arrows not disabled: {state}")

    prev_btn = next(a for a in state["arrows"] if "yxq-viewer-arrow--prev" in a["cls"])
    next_btn = next(a for a in state["arrows"] if "yxq-viewer-arrow--next" in a["cls"])
    for name, btn in (("prev", prev_btn), ("next", next_btn)):
        if btn["display"] == "none" or btn["visibility"] == "hidden":
            raise SystemExit(f"[{viewport}] {name} arrow hidden: {btn}")
        if btn["pointerEvents"] == "none":
            raise SystemExit(f"[{viewport}] {name} arrow not clickable: {btn}")
        if btn["w"] < 40 or btn["h"] < 40:
            raise SystemExit(f"[{viewport}] {name} arrow touch target too small: {btn}")
        if btn["x"] < 0 or btn["x"] + btn["w"] > width + 0.5:
            raise SystemExit(f"[{viewport}] {name} arrow outside viewport: {btn}")
    if not (prev_btn["x"] < width / 2 < next_btn["x"]):
        raise SystemExit(f"[{viewport}] arrows are not on left/right sides: {prev_btn} {next_btn}")

    wait_index(page, 0)
    press_arrow(page, "prev", touch)
    wait_index(page, n - 1)
    if not page.evaluate("() => !!(window.pswp && window.pswp.opener && window.pswp.opener.isOpen)"):
        raise SystemExit(f"[{viewport}] viewer closed after prev arrow")

    press_arrow(page, "next", touch)
    wait_index(page, 0)
    # 正向整圈：0 -> 1 -> ... -> n-1 -> 0
    for want in list(range(1, n)) + [0]:
        press_arrow(page, "next", touch)
        wait_index(page, want)
    if not page.evaluate("() => !!(window.pswp && window.pswp.opener && window.pswp.opener.isOpen)"):
        raise SystemExit(f"[{viewport}] viewer closed during next cycle")

    # 点/悬停后 photoswipe.css 的 .pswp__button:hover/:focus 会把背景重置为 none；
    # 这里锁住按钮底色与描边，防止 CSS 级联回归（浅色照片上白箭头不可见）。
    after = page.evaluate(ARROW_STATE_JS)
    for btn in after["arrows"]:
        if btn["bg"] in ("rgba(0, 0, 0, 0)", "transparent"):
            raise SystemExit(f"[{viewport}] arrow background became transparent after tap/focus: {btn}")
        if btn["shadow"] == "none":
            raise SystemExit(f"[{viewport}] arrow outline shadow missing: {btn}")

    if SHOT_DIR:
        shots = Path(SHOT_DIR)
        shots.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(shots / f"viewer-arrows-{viewport}.png"))
    print(f"PASS viewer arrows [{viewport}]: n={n}, loop prev/next wrap ok, "
          f"prev={prev_btn['w']:.0f}x{prev_btn['h']:.0f} next={next_btn['w']:.0f}x{next_btn['h']:.0f}")
    page.keyboard.press("Escape")
    page.wait_for_selector(".pswp", state="detached", timeout=4000)

    if single_offset is not None:
        # 单张照片：按钮仍注入但必须隐藏（.pswp--one-slide）。
        goto_home(page)
        for _ in range(single_offset):
            drag_card_next(page)
            guide_guard.dismiss_guides(page, timeout_ms=2000)
        open_viewer(page)
        single = page.evaluate(ARROW_STATE_JS)
        if int(single["n"]) != 1:
            raise SystemExit(f"[{viewport}] single-photo card opened {single['n']} photos: {single}")
        if not single["oneSlide"]:
            raise SystemExit(f"[{viewport}] single-photo viewer missing pswp--one-slide: {single}")
        if len(single["arrows"]) != 2:
            raise SystemExit(f"[{viewport}] single-photo viewer arrows not injected: {single}")
        for btn in single["arrows"]:
            if btn["display"] != "none":
                raise SystemExit(f"[{viewport}] single-photo arrow not hidden: {btn}")
        print(f"PASS viewer arrows [{viewport}]: single photo -> arrows hidden")
        page.keyboard.press("Escape")

    browser.close()


def main() -> int:
    cards = api_cards()
    offset = target_card_offset(cards)
    if offset is None:
        raise SystemExit("no card with >=2 photos in the home deck; cannot verify arrows")
    single = single_card_offset(cards)
    print(f"target deck offset with >=2 photos: {offset}")
    if single is None:
        print("SKIP single-photo check: no single-photo card in the home deck")
    else:
        print(f"single-photo deck offset: {single}")

    console_errors: list[str] = []
    with sync_playwright() as p:
        for viewport in VIEWPORTS.split(","):
            check_viewport(p, viewport, offset, single, console_errors)
    if console_errors:
        raise SystemExit("console errors: " + " | ".join(console_errors[:5]))
    print("PASS: no console/page errors")
    return 0


if __name__ == "__main__":
    sys.exit(main())
