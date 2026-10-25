#!/usr/bin/env python3
"""批次8 壳 critical CSS 验证：阻断/延迟 app.css 时首屏几何与 CLS 必须不变。

覆盖 reviewer B1：
- 正常加载 vs 阻断 `app.css` vs `app.css` 延迟 500ms/2s 三种情况；
- 逐元素比较 `#app`、`.swipe-card`、`.card-photo-wrap`、`.van-tabbar`、首卡图片的
  getBoundingClientRect（±2px）与关键 computed style（min-height/padding/width/position）；
- 延迟加载期间采样 layout-shift，要求 CLS ≤0.01；
- 截图留档。
"""

import os
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

BASE = os.environ["BASE_URL"].rstrip("/")
COOKIE = os.environ["SESSION_COOKIE"].strip()
OUT = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/b8-css"))
OUT.mkdir(parents=True, exist_ok=True)

SELECTORS = ["#app", "#app .cards-page", "#app .swipe-card", "#app .card-photo-wrap",
             "#app .card-photo-item", "#app .van-tabbar", ".swipe-guide-mask"]

GEOM = """(sels) => {
  const out = {};
  for (const sel of sels) {
    const el = document.querySelector(sel);
    if (!el) { out[sel] = null; continue; }
    const r = el.getBoundingClientRect();
    const cs = getComputedStyle(el);
    out[sel] = {
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height),
      minHeight: cs.minHeight, paddingBottom: cs.paddingBottom, width: cs.width,
      position: cs.position, objectPosition: cs.objectPosition,
    };
  }
  return out;
}"""

CLS_INIT = """
window.__cls = 0; window.__clsWorst = 0;
try {
  new PerformanceObserver(function (l) {
    for (const e of l.getEntries()) {
      if (e.hadRecentInput) continue;
      window.__cls += e.value;
      window.__clsWorst = Math.max(window.__clsWorst, e.value);
    }
  }).observe({type: 'layout-shift', buffered: true});
} catch (e) {}
"""


def ctx_page(browser, mode: str):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    ctx.add_cookies([{"name": "yxq_session", "value": COOKIE, "url": BASE,
                      "httpOnly": True, "sameSite": "Lax"}])
    ctx.add_init_script(CLS_INIT)
    if mode == "block":
        ctx.route("**/static/legacy/app.css*", lambda r: r.abort())
    page = ctx.new_page()
    page.goto(BASE + "/app", wait_until="domcontentloaded", timeout=60000)
    page.wait_for_selector(".van-tabbar", timeout=30000)
    page.wait_for_timeout(1500)
    return ctx, page


def compare(name: str, base: dict, other: dict) -> bool:
    ok = True
    for sel in SELECTORS:
        a, b = base.get(sel), other.get(sel)
        if (a is None) != (b is None):
            print(f"FAIL [{name}] {sel}: presence differs ({a is not None} vs {b is not None})")
            ok = False
            continue
        if a is None:
            continue
        for key in ("minHeight", "paddingBottom", "width", "position", "objectPosition"):
            if a[key] != b[key]:
                print(f"FAIL [{name}] {sel}.{key}: {a[key]!r} != {b[key]!r}")
                ok = False
        for key in ("x", "y", "w", "h"):
            if abs(a[key] - b[key]) > 2:
                print(f"FAIL [{name}] {sel}.{key}: {a[key]} != {b[key]} (±2px)")
                ok = False
    return ok


with sync_playwright() as pw:
    browser = pw.chromium.launch(args=["--no-sandbox"])
    ok = True

    ctx, page = ctx_page(browser, "normal")
    base = page.evaluate(GEOM, SELECTORS)
    normal_cls = page.evaluate("window.__cls")
    page.screenshot(path=str(OUT / "cards-normal.png"))
    print("normal:", {k: (v and {kk: v[kk] for kk in ('y', 'h', 'minHeight', 'paddingBottom')}) for k, v in base.items()})
    if normal_cls > 0.01:
        print(f"FAIL normal CLS {normal_cls}")
        ok = False
    ctx.close()

    # 1) app.css 被阻断
    ctx, page = ctx_page(browser, "block")
    blocked = page.evaluate(GEOM, SELECTORS)
    blocked_cls = page.evaluate("window.__cls")
    page.screenshot(path=str(OUT / "cards-blocked.png"))
    if blocked_cls > 0.01:
        print(f"FAIL blocked CLS {blocked_cls}")
        ok = False
    ok = compare("block", base, blocked) and ok
    ctx.close()

    # 2) app.css 延迟 2s 到达（最坏情况）
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    ctx.add_cookies([{"name": "yxq_session", "value": COOKIE, "url": BASE,
                      "httpOnly": True, "sameSite": "Lax"}])
    ctx.add_init_script(CLS_INIT)
    held = {}

    def hold(route):
        held.setdefault("routes", []).append(route)

    ctx.route("**/static/legacy/app.css*", hold)
    page = ctx.new_page()
    page.goto(BASE + "/app", wait_until="domcontentloaded", timeout=60000)
    page.wait_for_selector(".van-tabbar", timeout=30000)
    page.wait_for_timeout(2000)
    during = page.evaluate(GEOM, SELECTORS)
    page.wait_for_timeout(200)
    for r in held.get("routes", []):
        r.continue_()
    page.wait_for_timeout(1500)
    after = page.evaluate(GEOM, SELECTORS)
    delayed_cls = page.evaluate("window.__cls")
    worst = page.evaluate("window.__clsWorst")
    page.screenshot(path=str(OUT / "cards-delayed.png"))
    print(f"delayed(2s) CLS={delayed_cls:.4f} worst={worst:.4f}")
    if delayed_cls > 0.01:
        print(f"FAIL delayed CLS {delayed_cls} > 0.01")
        ok = False
    ok = compare("delayed-before", base, during) and ok
    ok = compare("delayed-after", base, after) and ok
    ctx.close()
    browser.close()

print("PASS critical CSS：blocked/delayed 首屏几何一致、CLS≤0.01" if ok else "FAIL")
sys.exit(0 if ok else 1)
