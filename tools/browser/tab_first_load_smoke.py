#!/usr/bin/env python3
"""首次点击四 tab：无「加载中...」文字、内容就绪、CLS、快速连点/回退/深链/管理台 iframe。

慢机口径：CPU×4、1.6Mbps / 150ms，390×844。只用于已部署 staging 或本地实例，不放进 make ci。

    BASE_URL=http://127.0.0.1:8095 SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/tab_first_load_smoke.py
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

TEXT = "加载中"
TABS = [("消息", "likes"), ("活动", "home"), ("我的", "profile"), ("牵线", "cards")]
SELECTORS = {
    "likes": ".page .msg-card",
    "home": ".page",
    "profile": ".profile-header",
    "cards": ".swipe-card",
}
INIT = """
window.__tabSmoke = {hits: 0, samples: [], cls: 0, clsNoInput: 0};
try {
  setInterval(function(){
    try {
      var t = document.body ? document.body.innerText : '';
      if (t && t.indexOf('加载中') >= 0) {
        window.__tabSmoke.hits++;
        if (window.__tabSmoke.samples.length < 5) window.__tabSmoke.samples.push(t.slice(0, 160));
      }
    } catch (e) {}
  }, 25);
} catch (e) {}
try {
  new PerformanceObserver(function(l){
    for (const e of l.getEntries()) {
      window.__tabSmoke.cls += e.value;
      if (!e.hadRecentInput) window.__tabSmoke.clsNoInput += e.value;
    }
  }).observe({type: 'layout-shift', buffered: true});
} catch (e) {}
"""


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def make_context(browser, base_url: str, cookie: str, throttle: bool):
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    context.add_cookies([{
        "name": "yxq_session", "value": cookie, "url": base_url,
        "httpOnly": True, "sameSite": "Lax",
    }])
    context.add_init_script(INIT)
    if throttle:
        page = context.new_page()
        cdp = context.new_cdp_session(page)
        cdp.send("Network.enable")
        cdp.send("Network.setCacheDisabled", {"cacheDisabled": True})
        cdp.send("Emulation.setCPUThrottlingRate", {"rate": 4})
        down = int(1.6 * 1024 * 1024 / 8)
        cdp.send("Network.emulateNetworkConditions", {
            "offline": False, "latency": 150,
            "downloadThroughput": down, "uploadThroughput": int(down / 2),
        })
        page.close()
    return context


def assert_card_photo(page) -> None:
    page.wait_for_function(
        """() => {
          const img = document.querySelector('.swipe-card .card-photo-item');
          return !!(img && img.complete && img.naturalWidth > 0);
        }""",
        timeout=20000,
    )


def reset_hits(page) -> None:
    page.evaluate("() => { window.__tabSmoke.hits = 0; window.__tabSmoke.samples = []; }")


def assert_no_text(page, label: str) -> None:
    data = page.evaluate("() => ({hits: window.__tabSmoke.hits, samples: window.__tabSmoke.samples})")
    if data["hits"]:
        raise AssertionError(f"{label}: 出现「{TEXT}」{data['hits']} 次 samples={data['samples']}")


def active_tab(page) -> str:
    return page.evaluate("""() => {
      const el = document.querySelector('.van-tabbar-item--active');
      return (el && el.innerText || '').trim();
    }""")


def dismiss_guides(page) -> None:
    page.wait_for_timeout(300)
    for _ in range(6):
        acted = False
        guide = page.locator(".swipe-guide-mask .sg-btn")
        if guide.count() and guide.first.is_visible():
            try:
                guide.first.click(timeout=2000)
                acted = True
            except Exception:
                pass
        overlay = page.locator(".van-overlay")
        if overlay.count() and overlay.first.is_visible():
            page.mouse.click(5, 5)
            acted = True
        know = page.get_by_text("知道了", exact=True)
        for i in range(know.count()):
            if know.nth(i).is_visible():
                try:
                    know.nth(i).click(timeout=2000)
                    acted = True
                except Exception:
                    pass
                break
        if not acted:
            return
        page.wait_for_timeout(300)


def click_tab(page, label: str) -> None:
    dismiss_guides(page)
    item = page.locator(".van-tabbar-item").filter(has_text=label)
    item.first.click(timeout=10000)
    if label == "消息":
        page.wait_for_selector(SELECTORS["likes"], timeout=20000)
    elif label == "活动":
        page.wait_for_selector(SELECTORS["home"], timeout=20000)
    elif label == "我的":
        page.wait_for_selector(SELECTORS["profile"], timeout=20000)
    else:
        page.wait_for_selector(SELECTORS["cards"], timeout=20000)
    page.wait_for_timeout(350)


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/tab-first-load"))
    out_dir.mkdir(parents=True, exist_ok=True)
    throttle = os.environ.get("NO_THROTTLE", "") != "1"
    failures: list[str] = []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(args=["--no-sandbox"])
            context = make_context(browser, base_url, cookie, throttle)
            page = context.new_page()
            page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=60000)
            page.wait_for_selector(".van-tabbar", timeout=30000)
            page.wait_for_selector("#boot-splash", state="detached", timeout=20000)
            page.wait_for_timeout(500)
            dismiss_guides(page)
            assert_card_photo(page)

            # 1) 首次依次点击四 tab（从 cards 出发，最后回 cards）。
            for label, key in TABS:
                reset_hits(page)
                started = time.time()
                click_tab(page, label)
                assert_no_text(page, f"首次点击 {label}")
                if active_tab(page) != label:
                    failures.append(f"首次点击 {label}: active={active_tab(page)!r}")
                if page.locator(SELECTORS[key]).count() == 0:
                    failures.append(f"首次点击 {label}: 内容未就绪")
                if key == "cards":
                    assert_card_photo(page)
                page.screenshot(path=str(out_dir / f"tab-{key}.png"))
                print(f"PASS 首次点击 {label} ({int((time.time()-started)*1000)}ms)")

            # 2) 快速连点：消息→活动→我的，不等内容；最终应落在我的。
            reset_hits(page)
            for label in ("消息", "活动", "我的"):
                page.locator(".van-tabbar-item").filter(has_text=label).first.click(timeout=10000)
            page.wait_for_timeout(1200)
            assert_no_text(page, "快速连点")
            page.wait_for_selector(SELECTORS["profile"], timeout=20000)
            if active_tab(page) != "我的":
                failures.append(f"快速连点最终 active={active_tab(page)!r}")
            print("PASS 快速连点")

            # 3) 回退：我的 -> 活动（pushState）后 back，不出现文字。
            reset_hits(page)
            page.locator(".van-tabbar-item").filter(has_text="活动").first.click(timeout=10000)
            page.wait_for_timeout(400)
            page.go_back()
            page.wait_for_timeout(800)
            assert_no_text(page, "回退")
            print("PASS 回退")

            # 4) 深链 #/likes：直接落在 likes，chunk 未就绪也不显示文字。
            reset_hits(page)
            page.goto(base_url + "/app#/likes", wait_until="domcontentloaded", timeout=60000)
            page.wait_for_selector(".van-tabbar", timeout=30000)
            page.wait_for_selector(SELECTORS["likes"], timeout=20000)
            page.wait_for_timeout(300)
            assert_no_text(page, "深链 #/likes")
            print("PASS 深链")

            cls = page.evaluate("() => ({cls: window.__tabSmoke.cls, noInput: window.__tabSmoke.clsNoInput})")
            print(f"CLS total={cls['cls']:.4f} noInput={cls['noInput']:.4f}")
            if cls["noInput"] > 0.02:
                failures.append(f"CLS noInput={cls['noInput']} 超过 0.02")

            # 5) 管理台 iframe：入口仍可用（顶层 tabbar 隐藏，iframe 管理台出现）。
            page.goto(base_url + "/app?admin=1", wait_until="domcontentloaded", timeout=60000)
            page.wait_for_selector(".embed-frame--ready", timeout=30000)
            page.wait_for_timeout(800)
            frame = page.frame_locator(".embed-frame--ready")
            frame.locator(".admin-topbar").first.wait_for(state="visible", timeout=20000)
            # 管理台 overlay 覆盖顶层；tabbar 仍在 DOM 但被遮挡不影响使用。
            # 这里只断言 iframe 管理台可用、无 boot 错误。
            if page.locator(".boot-fail").count():
                failures.append("管理台 iframe 打开后出现 boot-fail")
            page.screenshot(path=str(out_dir / "admin-iframe.png"))
            print("PASS 管理台 iframe")

            context.close()
            browser.close()
    except Exception as exc:  # noqa: BLE001
        failures.append(str(exc))
    if failures:
        for f in failures:
            print(f"FAIL {f}")
        return 1
    print("PASS tab first load")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
