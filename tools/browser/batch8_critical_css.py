#!/usr/bin/env python3
"""批次8 壳 critical CSS 验证：阻断 app.css（模拟异步样式未到时）首屏是否可用。

- 正常加载：记录 styleSheets 数、.van-tabbar 计算样式、CLS；
- 阻断 /static/legacy/app.css：首卡几何/底栏固定/背景色必须仍由内联 critical 提供，
  截图留档；子页（消息）允许暂未完全样式化（异步 CSS 随后到达）。
"""

import os
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

BASE = os.environ["BASE_URL"].rstrip("/")
COOKIE = os.environ["SESSION_COOKIE"].strip()
OUT = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/b8-css"))
OUT.mkdir(parents=True, exist_ok=True)

CHECK = """() => {
  const tab = document.querySelector('.van-tabbar');
  const card = document.querySelector('.swipe-card');
  const page = document.querySelector('.cards-page');
  const tree = document.getElementById('app');
  return {
    stylesheets: document.styleSheets.length,
    tabbarPosition: tab ? getComputedStyle(tab).position : null,
    tabbarHeight: tab ? Math.round(tab.getBoundingClientRect().height) : null,
    cardVisible: card ? (card.getBoundingClientRect().width > 100) : false,
    cardRadius: card ? getComputedStyle(card).borderRadius : null,
    pageBg: page ? getComputedStyle(page).backgroundColor : null,
    rootBg: getComputedStyle(document.body).backgroundColor,
    cloaked: tree ? tree.hasAttribute('v-cloak') : null,
  };
}"""

with sync_playwright() as pw:
    browser = pw.chromium.launch(args=["--no-sandbox"])
    ok = True

    def new_ctx(block_rest: bool):
        ctx = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
        ctx.add_cookies([{"name": "yxq_session", "value": COOKIE, "url": BASE,
                          "httpOnly": True, "sameSite": "Lax"}])
        if block_rest:
            ctx.route("**/static/legacy/app.css*", lambda r: r.abort())
        page = ctx.new_page()
        page.goto(BASE + "/app", wait_until="domcontentloaded", timeout=60000)
        page.wait_for_selector(".van-tabbar", timeout=30000)
        page.wait_for_timeout(1200)
        return ctx, page

    ctx, page = new_ctx(False)
    normal = page.evaluate(CHECK)
    print("normal:", normal)
    page.screenshot(path=str(OUT / "cards-with-app-css.png"))
    if normal["tabbarPosition"] != "fixed" or not normal["cardVisible"]:
        print("FAIL: 正常加载首屏样式缺失")
        ok = False
    ctx.close()

    ctx, page = new_ctx(True)
    blocked = page.evaluate(CHECK)
    print("blocked app.css:", blocked)
    page.screenshot(path=str(OUT / "cards-critical-only.png"))
    if blocked["tabbarPosition"] != "fixed":
        print("FAIL: 无 app.css 时底栏不再是 fixed（critical 未覆盖）")
        ok = False
    if not blocked["cardVisible"]:
        print("FAIL: 无 app.css 时首卡不可见（critical 未覆盖）")
        ok = False
    if blocked["cardRadius"] in (None, "0px"):
        print("FAIL: 无 app.css 时首卡圆角丢失（critical 未覆盖 .swipe-card）")
        ok = False
    if blocked["cloaked"]:
        print("FAIL: #app 仍带 v-cloak")
        ok = False
    ctx.close()
    browser.close()

print("PASS critical CSS 覆盖首屏" if ok else "FAIL")
sys.exit(0 if ok else 1)
