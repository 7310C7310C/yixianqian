#!/usr/bin/env python3
"""壳内首卡静态岛 + 牌堆单卡窗口回归（批次5）。

覆盖：
1. 390/1280：`#boot-card` 的首卡 hero 与 Vue 真实 `.swipe-card`/`.card-photo-wrap`
   几何一致（±1px），首图 URL 使用 `?size=thumb`；Vue 首卡图绘制后 静态岛移除；
2. 静态岛期间/交接无 layout-shift（CLS < 0.01），LCP 元素是首卡缩图（静态岛或真实卡）；
3. 滑动、切 tab、浏览器回退、`#/likes` 深链、restore 后 静态岛不残留、不重现；
4. 牌堆只挂载当前 1 张卡（DOM 节点数受控），0/1/2/末张/回退/restore 用例；
5. 无卡（bootstrap cards 为空）与首图请求失败时降级：静态岛显示占位/不出现破图，
   数据就绪后移除；
6. 暗色主题与 375/390/768/1280 视口。

需要已登录 staging/本地实例的原始 session cookie：

    BASE_URL=http://127.0.0.1:18296 SESSION_COOKIE=<yxq_session> \
    SHOT_DIR=/tmp/opencode/b5-hero python3 tools/browser/app_hero_deck_smoke.py

依赖 playwright；不放进 make ci。
"""

from __future__ import annotations

import base64
import json
import os
import re
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

FILTER_DEFAULT = {
    "gender": "", "nickname": "", "user_id": "",
    "height_min": "", "height_max": "", "age_min": "", "age_max": "",
    "education": [], "income": [], "house": [], "driving": [],
}
TINY_PNG = base64.b64decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2n1cAAAAASUVORK5CYII='
)
HERO_INIT = """
window.__hero_snapshot = null;
(function () {
  function snap() {
    const h = document.getElementById('boot-card');
    if (!h) return;
    const c = h.querySelector('.swipe-card');
    const p = h.querySelector('.card-photo-wrap');
    const i = h.querySelector('.boot-photo');
    const cr = c && c.getBoundingClientRect();
    const pr = p && p.getBoundingClientRect();
    window.__hero_snapshot = {
      present: true,
      src: (i && i.getAttribute('src')) || '',
      card: cr && cr.toJSON(), photo: pr && pr.toJSON(),
      bg: getComputedStyle(h).backgroundColor,
      cardBg: c && getComputedStyle(c).backgroundColor,
      imgDisplay: i && getComputedStyle(i).display,
      objectPosition: i && getComputedStyle(i).objectPosition,
      tabbarHeight: (h.querySelector('.boot-tabbar') || {getBoundingClientRect: () => null}).getBoundingClientRect()?.height || null,
      ready: !!(i && i.complete && i.naturalWidth),
      failed: !!(p && p.classList.contains('boot-photo-failed'))
    };
  }
  new MutationObserver(snap).observe(document.documentElement || document, {
    childList: true, subtree: true, attributes: true, attributeFilter: ['src', 'class', 'style']
  });
  window.__heroRemovedAt = null;
  window.__heroSeen = false;
  new MutationObserver(function () {
    if (document.getElementById('boot-card')) { window.__heroSeen = true; return; }
    if (window.__heroSeen && window.__heroRemovedAt === null) {
      window.__heroRemovedAt = Math.round(performance.now());
    }
  }).observe(document, {childList: true, subtree: true});
  document.addEventListener('readystatechange', snap);
  document.addEventListener('DOMContentLoaded', snap);
  setTimeout(snap, 0);
})();
"""

PERF_INIT = """
window.__b5 = {lcp: 0, lcpEl: '', lcpClass: '', cls: 0, longtasks: []};
try { new PerformanceObserver(function (l) {
  for (const e of l.getEntries()) {
    window.__b5.lcp = e.startTime;
    window.__b5.lcpEl = (e.element && (e.element.currentSrc || e.element.src)) || e.url || '';
    window.__b5.lcpClass = (e.element && typeof e.element.className === 'string') ? e.element.className : '';
  }
}).observe({type: 'largest-contentful-paint', buffered: true}); } catch (e) {}
try { new PerformanceObserver(function (l) {
  for (const e of l.getEntries()) { if (!e.hadRecentInput) window.__b5.cls += e.value; }
}).observe({type: 'layout-shift', buffered: true}); } catch (e) {}
try { new PerformanceObserver(function (l) {
  for (const e of l.getEntries()) window.__b5.longtasks.push({start: e.startTime, dur: e.duration});
}).observe({type: 'longtask', buffered: true}); } catch (e) {}
"""


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def make_context(browser, base_url: str, cookie: str, width: int, height: int, dark: bool = False):
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.add_cookies([{
        "name": "yxq_session", "value": cookie,
        "url": base_url, "httpOnly": True, "sameSite": "Lax",
    }])
    context.add_init_script(
        "try{localStorage.setItem('yxq_swipe_guide_v1','1');"
        "localStorage.setItem('yxq_update_notes_v1','1');"
        + ("localStorage.setItem('yxq_dark','1');" if dark else "")
        + "}catch(e){}"
    )
    context.add_init_script(PERF_INIT + HERO_INIT)
    return context


def page_errors(page) -> list[str]:
    errors: list[str] = []
    page.on("pageerror", lambda exc: errors.append(str(exc)))
    return errors


def throttle(context, page, cpu: int = 4, latency: float = 150, down_mbps: float = 1.6):
    cdp = context.new_cdp_session(page)
    cdp.send("Network.enable")
    cdp.send("Emulation.setCPUThrottlingRate", {"rate": cpu})
    cdp.send("Network.emulateNetworkConditions", {
        "offline": False, "latency": latency,
        "downloadThroughput": int(down_mbps * 1024 * 1024 / 8),
        "uploadThroughput": int(down_mbps * 1024 * 1024 / 16),
    })
    return cdp


def wait_hero_gone(page, timeout: int = 20000) -> None:
    try:
        page.locator("#boot-card").wait_for(state="detached", timeout=timeout)
    except PlaywrightTimeoutError:
        raise SmokeFailure("壳内 hero 未在超时内移除") from None


def wait_cards_ready(page, timeout: int = 30000) -> None:
    page.locator(".van-tabbar").wait_for(state="visible", timeout=timeout)
    page.locator(".swipe-card, .van-empty").first.wait_for(state="visible", timeout=timeout)


def rect(locator) -> dict:
    box = locator.bounding_box()
    if box is None:
        raise SmokeFailure("元素没有 bounding box")
    return box


def assert_close(a: dict, b: dict, label: str, tol: float = 1.5) -> None:
    for key in ("x", "y", "width", "height"):
        if abs(float(a[key]) - float(b[key])) > tol:
            raise SmokeFailure(f"{label} 几何不一致 {key}: hero={a[key]} card={b[key]}")


def counter_text(page) -> str:
    return page.locator(".card-counter").inner_text().strip()


def parse_counter(page) -> tuple[int, int]:
    text = counter_text(page)
    try:
        left, right = text.split("/")
        return int(left.strip()), int(right.strip())
    except ValueError:
        raise SmokeFailure(f"无法解析卡片计数器 {text!r}") from None


def wait_counter(page, want: str, timeout: int = 15000) -> None:
    try:
        page.wait_for_function(
            "(want) => { const el = document.querySelector('.card-counter'); return !!el && el.textContent.trim() === want; }",
            arg=want, timeout=timeout,
        )
    except PlaywrightTimeoutError:
        raise SmokeFailure(f"计数器等待 {want!r} 超时，当前 {counter_text(page)!r}") from None


def wait_counter_left(page, want_left: int, timeout: int = 15000) -> None:
    """Wait for numerator only; bootstrap vs /api/cards total may differ by one."""
    page.wait_for_function(
        r"""(left) => {
          const el = document.querySelector('.card-counter');
          if (!el) return false;
          const m = el.textContent.trim().match(/^([0-9]+)\s*\/\s*([0-9]+)$/);
          return !!m && Number(m[1]) === left && Number(m[2]) > 0;
        }""",
        arg=want_left, timeout=timeout,
    )

def wait_counter_exact(page, want: int, timeout: int = 20000) -> None:
    """Wait until the deck counter shows want / want (app total, incl. self card)."""
    try:
        page.wait_for_function(
            r"""(target) => {
              const el = document.querySelector('.card-counter');
              if (!el) return false;
              const m = el.textContent.trim().match(/^([0-9]+)\s*\/\s*([0-9]+)$/);
              return !!m && Number(m[1]) === target && Number(m[2]) === target;
            }""",
            arg=want, timeout=timeout,
        )
    except PlaywrightTimeoutError:
        raise SmokeFailure(f"counter 未到 {want} / {want}") from None


def drag_card(page, dx: int) -> None:
    box = rect(page.locator(".card-main"))
    x = box["x"] + box["width"] / 2
    y = box["y"] + box["height"] / 2
    page.mouse.move(x, y)
    page.mouse.down()
    page.mouse.move(x + dx, y, steps=10)
    page.mouse.up()
    page.wait_for_timeout(350)


def restore_pos(page, pos: int) -> None:
    page.evaluate(
        """(payload) => sessionStorage.setItem('yxq_deck_restore', JSON.stringify(payload))""",
        {"pos": pos, "filters": FILTER_DEFAULT, "ts": int(time.time() * 1000)},
    )
    page.reload(wait_until="domcontentloaded")
    # 首帧断言：DOMContentLoaded 时静态岛必须已被 head script 跳过。
    # 不能只等 detached——旧实现会先显示第 1 张、约 1s 后才被 app.js 移除。
    if page.locator("#boot-card").count():
        raise SmokeFailure("restore 刷新时静态岛在 DOMContentLoaded 仍存在（首帧会显示第 1 张）")
    if not page.evaluate("() => document.documentElement.classList.contains('boot-skip')"):
        raise SmokeFailure("restore 刷新时未设置 boot-skip")
    page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)


def run_viewport(browser, base_url: str, cookie: str, width: int, height: int,
                 out_dir: Path, throttle_net: bool) -> list[str]:
    tag = f"{width}x{height}"
    context = None
    try:
        context = make_context(browser, base_url, cookie, width, height)
        page = context.new_page()
        errors = page_errors(page)
        if throttle_net:
            throttle(context, page)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=60000)
        page.wait_for_function("() => !!window.__hero_snapshot", timeout=10000)
        if not page.evaluate("() => !!document.querySelector('link[rel=preload][as=image]')"):
            raise SmokeFailure(f"{tag}: 正常首页缺少首卡图 preload")
        page.screenshot(path=str(out_dir / f"boot-card-{tag}.png"))
        wait_hero_gone(page)
        # 读取移除前的最后一次快照：裁切可能由 app.js 在交接前才同步，必须比最终真实卡。
        hero = page.evaluate("window.__hero_snapshot")
        if not hero or "/cards/photo/" not in hero["src"] or "size=thumb" not in hero["src"]:
            raise SmokeFailure(f"{tag}: 静态岛首图不是卡片缩图 {hero and hero.get('src')!r}")
        wait_cards_ready(page)
        real = page.evaluate("""() => {
          const card = document.querySelector('.swipe-card');
          const photo = card && card.querySelector('.card-photo-wrap');
          const cr = card && card.getBoundingClientRect();
          const pr = photo && photo.getBoundingClientRect();
          return {card: cr && cr.toJSON(), photo: pr && pr.toJSON()};
        }""")
        if real["card"] is None:
            raise SmokeFailure(f"{tag}: 没有渲染真实卡片")
        assert_close(hero["card"], real["card"], f"{tag} 静态岛/真实卡")
        assert_close(hero["photo"], real["photo"], f"{tag} 静态岛/真实卡照片区")
        detail = page.evaluate("""() => {
          const img = document.querySelector('.swipe-card .card-photo-item');
          const extra = document.querySelectorAll('.swipe-card .card-photo-item')[1];
          const tab = document.querySelector('.van-tabbar');
          return {
            objectPosition: img ? getComputedStyle(img).objectPosition : '',
            nextObjectPosition: extra ? getComputedStyle(extra).objectPosition : '',
            tabbarHeight: tab ? tab.getBoundingClientRect().height : null,
          };
        }""")
        if hero.get("objectPosition") and detail["objectPosition"]:
            # 真实卡可能按人脸写 object-position；hero 默认/复制值必须一致。
            if hero["objectPosition"] != detail["objectPosition"]:
                raise SmokeFailure(
                    f"{tag}: 静态岛裁切 {hero['objectPosition']!r} != 真实卡 {detail['objectPosition']!r}"
                )
        if hero.get("tabbarHeight") and detail["tabbarHeight"] is not None:
            if abs(float(hero["tabbarHeight"]) - float(detail["tabbarHeight"])) > 1.5:
                raise SmokeFailure(
                    f"{tag}: 静态岛假 tabbar {hero['tabbarHeight']}px != 真 tabbar {detail['tabbarHeight']}px"
                )

        timing = page.evaluate("""() => {
          const removed = window.__YXQ_BOOT_CARD__ && window.__YXQ_BOOT_CARD__.removedAt();
          const home = window.__YXQ_LOAD_HOME_DONE_AT__;
          return {
            removed: removed == null ? null : Math.round(removed),
            home: home == null ? null : Math.round(home),
            gap: (removed != null && home != null) ? Math.round(removed - home) : null,
            interactive: window.__YXQ_CARD_INTERACTIVE_AT__ || null,
          };
        }""")
        if timing["gap"] is not None and timing["gap"] > 1200:
            raise SmokeFailure(f"{tag}: 静态岛交接 gap {timing['gap']}ms > 1200ms")
        if timing["interactive"] is None:
            raise SmokeFailure(f"{tag}: 缺少 time-to-interactive 标记")
        stats = page.evaluate("""() => ({
          cls: window.__b5.cls, lcp: Math.round(window.__b5.lcp), lcpEl: window.__b5.lcpEl,
          lcpClass: window.__b5.lcpClass,
          longtasks: window.__b5.longtasks.length,
          cards: document.querySelectorAll('.swipe-card').length,
          photos: [...document.querySelectorAll('.card-photo-item')]
                    .filter((el) => el.getAttribute('src')).length,
          photoNodes: document.querySelectorAll('.card-photo-item').length,
          dom: document.querySelectorAll('*').length,
          mount: (performance.getEntriesByName('yxq-mount')[0] || {}).duration || null,
          boot: (performance.getEntriesByName('yxq-boot')[0] || {}).duration || null,
          hero: !!document.getElementById('boot-card'),
        })""")
        if stats["hero"]:
            raise SmokeFailure(f"{tag}: 静态岛仍在 DOM")
        if stats["cls"] > 0.01:
            raise SmokeFailure(f"{tag}: CLS {stats['cls']} 超过 0.01")
        # LCP 可以是首卡照片，也可以是首卡文案（小图/mock 图时文字更大）；
        # 关键是元素属于首卡，且时间在预算内。
        if not any(name in stats["lcpClass"] for name in
                   ("boot-photo", "card-photo-item", "card-subtitle", "card-name")) and "/cards/photo/" not in stats["lcpEl"]:
            raise SmokeFailure(
                f"{tag}: LCP 不是首卡内容（class={stats['lcpClass']!r}, url={stats['lcpEl']!r}）"
            )
        if stats["cards"] != 1:
            raise SmokeFailure(f"{tag}: 牌堆应只挂当前 1 张，实际 {stats['cards']}")
        # 卡片模板按 v-for 渲染全部照片节点（滑动切换用），但只有当前与下一张应当带 src
        # （批次4/5 的解码预算）。旧断言数的是节点总数，卡有 >2 张照片时必然假红
        # （批次7 集成排查：staging 账号首卡 9 张照片，节点 9 / 带 src 2）。
        if stats["photos"] > 2:
            raise SmokeFailure(
                f"{tag}: 当前卡带 src 的照片超过 2 张（{stats['photos']}，节点共 {stats.get('photoNodes')}）"
            )
        # 仍保留对 v-for 渲染膨胀的约束（reviewer N7）：节点数不应随牌堆/窗口无限增长。
        if stats["photoNodes"] > 12:
            raise SmokeFailure(f"{tag}: 当前卡照片节点过多（{stats['photoNodes']} > 12）")
        if stats["dom"] > 260:
            raise SmokeFailure(f"{tag}: DOM 节点 {stats['dom']} 超过 260")
        if stats["mount"] is None:
            raise SmokeFailure(f"{tag}: 缺少 yxq-mount 测量")
        if stats["boot"] is None:
            raise SmokeFailure(f"{tag}: 缺少 yxq-boot 测量")
        page.screenshot(path=str(out_dir / f"hero-{tag}.png"))
        if errors:
            raise SmokeFailure(f"{tag}: pageerror {errors[:2]}")
        return []
    except Exception as exc:  # noqa: BLE001 - report all viewport failures together
        return [f"{tag}: {exc}"]
    finally:
        if context is not None:
            context.close()


def run_interactions(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=60000)
        wait_hero_gone(page)
        wait_cards_ready(page)
        # 首屏 bootstrap 的 cards_total 与随后 /api/cards 可能差 1（推荐位/候选变动）；
        # 统一以一次刷新后的 API 总数为准。
        page.reload(wait_until="domcontentloaded")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
        wait_hero_gone(page)
        wait_cards_ready(page)
        # bootstrap 的 cards_total 可能比 /api/cards 多 1；以 API 总数为准。
        api_resp = page.request.get(base_url + "/legacy/api/cards?limit=1")
        if not api_resp.ok:
            raise SmokeFailure(f"/legacy/api/cards status={api_resp.status}")
        total = int(api_resp.json().get("cards_total") or 0)
        if total < 3:
            raise SmokeFailure(f"测试账号牌堆不足 3 张（total={total}）")
        wait_counter_left(page, 1)

        # 1) 切 tab：hero 已消失，不因回切而重现。
        page.locator(".van-tabbar-item").filter(has_text="消息").click()
        page.wait_for_timeout(500)
        if page.locator("#boot-card").count():
            raise SmokeFailure("切消息后 静态岛重现")
        page.locator(".van-tabbar-item").filter(has_text="牵线").click()
        page.wait_for_timeout(500)
        if page.locator("#boot-card").count():
            raise SmokeFailure("回牵线后 静态岛重现")

        # 2) 滑动：1→2→1；始终只有一张卡。
        drag_card(page, -90)
        wait_counter_left(page, 2)
        if page.locator(".swipe-card").count() != 1:
            raise SmokeFailure("滑动后牌堆 DOM 不是 1 张")
        page.screenshot(path=str(out_dir / "deck-2-390.png"))
        drag_card(page, 90)
        wait_counter_left(page, 1)

        # 3) 浏览器回退：我的 → 我的麦穗（pushState）→ back → 我的；静态岛不重现。
        page.locator(".van-tabbar-item").filter(has_text="我的").click()
        page.wait_for_timeout(500)
        page.locator(".profile-card-row").filter(has_text="我的麦穗").click()
        page.wait_for_timeout(600)
        page.get_by_text("我的麦穗", exact=False).first.wait_for(state="visible", timeout=10000)
        page.go_back()
        page.wait_for_timeout(600)
        page.locator(".van-tabbar").wait_for(state="visible", timeout=10000)
        if page.locator("#boot-card").count():
            raise SmokeFailure("浏览器回退后 静态岛重现")
        page.locator(".van-tabbar-item").filter(has_text="牵线").click()
        page.wait_for_timeout(500)

        # 4) restore：pos 2 / 0 / 末张。总人数以落地后计数器为准（可能因候选变动 ±1）。
        restore_pos(page, 2)
        wait_cards_ready(page)
        wait_hero_gone(page)
        wait_counter_left(page, 3)
        num, den = parse_counter(page)
        if num != 3:
            raise SmokeFailure(f"restore pos=2 落点不是第 3 张：{num} / {den}")
        restore_pos(page, 0)
        wait_cards_ready(page)
        wait_hero_gone(page)
        wait_counter_left(page, 1)
        num, den = parse_counter(page)
        if num != 1:
            raise SmokeFailure(f"restore pos=0 落点不是第 1 张：{num} / {den}")
        last_num = None
        last_den = None
        app_total = 0
        # restoreDeck 的 pos 以 app/bootstrap 总数为准（含本人卡），不是 /api/cards 的 total。
        # bootstrap total 可能比 API total 大 1；用 counter 分母作为可复现的 app total。
        for _ in range(3):
            _, app_total = parse_counter(page)
            if app_total < 3:
                raise SmokeFailure(f"restore 末张前牌堆不足 3 张（app_total={app_total}）")
            restore_pos(page, app_total - 1)
            wait_cards_ready(page)
            wait_hero_gone(page)
            try:
                wait_counter_exact(page, app_total, timeout=20000)
            except SmokeFailure:
                pass
            last_num, last_den = parse_counter(page)
            if last_num == app_total and last_den == app_total:
                break
        if last_num != last_den:
            raise SmokeFailure(f"restore 末张失败：{last_num} / {last_den}（app_total={app_total}）")
        drag_card(page, -90)
        page.get_by_text("已是最后一张", exact=False).first.wait_for(state="visible", timeout=5000)
        drag_card(page, 90)
        num, den = parse_counter(page)
        if num != den - 1:
            raise SmokeFailure(f"末张回退落点 {num} / {den}")

        # 5) #/likes 深链：hero 不应在非牵线页出现；也不应 preload 首卡缩图。
        boot_photo = page.evaluate("() => window.__YXQ_PRELOAD__ || ''")
        preload_hits = []
        page.on("request", lambda req: preload_hits.append(req.url) if boot_photo and boot_photo in req.url else None)
        page.goto(base_url + "/app#/likes", wait_until="domcontentloaded", timeout=40000)
        page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
        page.wait_for_timeout(500)
        if page.evaluate("() => !!document.querySelector('link[rel=preload][as=image]')"):
            raise SmokeFailure("深链 #/likes 仍创建首卡图 preload")
        if preload_hits:
            raise SmokeFailure(f"深链 #/likes 仍请求首卡缩图：{preload_hits[:2]}")
        if page.locator("#boot-card").count():
            raise SmokeFailure("深链 #/likes 时 静态岛残留")
        page.locator(".van-tabbar-item").filter(has_text="牵线").click()
        page.wait_for_timeout(500)
        if page.locator("#boot-card").count():
            raise SmokeFailure("深链回牵线后 静态岛重现")
        if errors:
            raise SmokeFailure(f"交互用例 pageerror {errors[:2]}")
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"390 交互/restore: {exc}"]
    finally:
        if context is not None:
            context.close()


def run_fallbacks(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    failures: list[str] = []

    # 无卡：bootstrap cards 置空，hero 只有 shimmer；数据就绪后移除并显示空态。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        throttle(context, page)

        def strip_cards(route) -> None:
            response = route.fetch()
            body = re.sub(r'"cards":\[.*?\],"cards_total"', '"cards":[],"cards_total"', response.text(), count=1, flags=re.S)
            # 无卡时 Go 不会渲染静态岛；测试里把整块（含 inline script）去掉，模拟服务端无卡。
            body = re.sub(r'<div id="boot-card".*?(?=<div id="app" v-cloak>)', '', body, count=1, flags=re.S)
            route.fulfill(response=response, body=body)

        context.route(base_url.rstrip("/") + "/app", strip_cards)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.locator("#boot-card").wait_for(state="detached", timeout=5000)
        page.locator(".van-empty").wait_for(state="visible", timeout=20000)
        page.screenshot(path=str(out_dir / "boot-card-no-cards-390.png"))
        if errors:
            raise SmokeFailure(f"无卡用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"无卡降级: {exc}")
    finally:
        if context is not None:
            context.close()

    # 首图请求失败：hero 不出现破图，最终移除；卡片走自身占位。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        throttle(context, page)
        context.route("**/cards/photo/**", lambda route: route.abort())
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function("() => !!window.__hero_snapshot && window.__hero_snapshot.failed", timeout=15000)
        if page.evaluate("window.__hero_snapshot.imgDisplay") != "none":
            raise SmokeFailure("首图失败后 hero 仍显示破图")
        wait_hero_gone(page, timeout=25000)
        page.screenshot(path=str(out_dir / "hero-image-fail-390.png"))
        if errors:
            raise SmokeFailure(f"首图失败用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"首图失败降级: {exc}")
    finally:
        if context is not None:
            context.close()

    return failures


def run_takeover_regressions(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    """B1/B2/I1 回归：真实卡图片等待与滚动位置必须命中 #app，而不是静态岛。"""
    failures: list[str] = []

    # 1) 静态岛首图坏掉、真实卡首图正常：交接必须等真实卡 decode，不能等静态岛。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)

        def bad_boot_photo(route) -> None:
            response = route.fetch()
            body = re.sub(
                r'(class="card-photo-item boot-photo" src=")[^"]*(")',
                r'\1/cards/photo/00000000-0000-0000-0000-000000000000?size=thumb\2',
                response.text(), count=1, flags=re.S,
            )
            route.fulfill(response=response, body=body)

        context.route(base_url.rstrip("/") + "/app", bad_boot_photo)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function(
            "() => window.__YXQ_BOOT_CARD__ && window.__YXQ_BOOT_CARD__.removedAt()",
            timeout=6000,
        )
        removed = page.evaluate("window.__YXQ_BOOT_CARD__ ? {at: window.__YXQ_BOOT_CARD__.removedAt(), reason: window.__YXQ_BOOT_CARD__.reason()} : null")
        if not removed or removed["reason"] != "painted":
            raise SmokeFailure(f"坏静态岛首图时交接未等真实卡：{removed}")
        page.locator("#boot-card").wait_for(state="detached", timeout=5000)
        page.screenshot(path=str(out_dir / "boot-card-bad-island-photo-390.png"))
        if errors:
            raise SmokeFailure(f"坏静态岛首图用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"坏静态岛首图: {exc}")
    finally:
        if context is not None:
            context.close()

    # 2) 静态岛滚动后交接：真实卡必须继承 scrollTop（选择器必须限定 #app）。
    #    用 bootstrap 里首卡的 photos_thumb 指向一个受控 URL，等 app 已挂载、
    #    loadHome 已返回、真实卡图仍被挂起时再滚动；这样“settle 时快照”的旧实现
    #    会取到 0，而“done 前读取”的新实现取到 180。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        held_img = {}

        def patch_bootstrap(route) -> None:
            response = route.fetch()
            body = re.sub(
                r'("photos_thumb":\["?)[^"]*("?)',
                r'\1/cards/photo/11111111-1111-1111-1111-111111111111?size=thumb\2',
                response.text(), count=1, flags=re.S,
            )
            route.fulfill(response=response, body=body)

        def hold_real_image(route) -> None:
            if "route" not in held_img:
                held_img["route"] = route
                return
            route.continue_()

        context.route(base_url.rstrip("/") + "/app", patch_bootstrap)
        context.route("**/cards/photo/11111111-1111-1111-1111-111111111111**", hold_real_image)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function(
            "() => window.__YXQ_APP_MOUNTED__ && window.__YXQ_LOAD_HOME_DONE__ === true && !!document.getElementById('boot-card')",
            timeout=15000,
        )
        page.evaluate("() => { const el = document.querySelector('#boot-card .card-info'); if (el) el.scrollTop = 180; }")
        page.wait_for_timeout(100)
        boot_scroll = page.evaluate("() => { const el = document.querySelector('#boot-card .card-info'); return el ? el.scrollTop : -1; }")
        if boot_scroll < 150:
            raise SmokeFailure(f"静态岛资料区不可滚动：scrollTop={boot_scroll}")
        if held_img.get("route"):
            held_img["route"].fulfill(status=200, content_type="image/png", body=TINY_PNG)
        page.locator("#boot-card").wait_for(state="detached", timeout=15000)
        # 交接瞬间读取：只验证 handoff 本身，避免随后 /api/cards 刷新/重渲染把 scroll 归零。
        real_scroll = page.evaluate("() => { const el = document.querySelector('#app .swipe-card .card-info'); return el ? el.scrollTop : -1; }")
        if real_scroll < 150:
            raise SmokeFailure(f"交接后真实卡未继承滚动位置：real scrollTop={real_scroll}")
        # 首屏后切走再回牵线，deck 重渲染也不能把资料区拉回顶部（1.8s 守卫内）。
        page.locator(".van-tabbar-item").filter(has_text="消息").click()
        page.wait_for_timeout(200)
        page.locator(".van-tabbar-item").filter(has_text="牵线").click()
        page.wait_for_timeout(250)
        scroll_after_tab = page.evaluate("() => { const el = document.querySelector('#app .swipe-card .card-info'); return el ? el.scrollTop : -1; }")
        if scroll_after_tab < 150:
            raise SmokeFailure(f"切页返回后滚动位置丢失：real scrollTop={scroll_after_tab}")
        page.screenshot(path=str(out_dir / "boot-card-scroll-transfer-390.png"))
        if errors:
            raise SmokeFailure(f"滚动交接用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"滚动交接: {exc}")
    finally:
        if context is not None:
            try:
                if held_img.get("route"):
                    held_img["route"].abort()
            except Exception:  # noqa: BLE001
                pass
            context.close()
    # 3) 滚动后点一下（inline pointerdown）也必须走 app 的 dismiss hook，保留 scrollTop。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        held_img = {}

        def patch_bootstrap_click(route) -> None:
            response = route.fetch()
            body = re.sub(
                r'("photos_thumb":\["?)[^"]*("?)',
                r'\1/cards/photo/22222222-2222-2222-2222-222222222222?size=thumb\2',
                response.text(), count=1, flags=re.S,
            )
            route.fulfill(response=response, body=body)

        def hold_real_image_click(route) -> None:
            if "route" not in held_img:
                held_img["route"] = route
                return
            route.continue_()

        context.route(base_url.rstrip("/") + "/app", patch_bootstrap_click)
        context.route("**/cards/photo/22222222-2222-2222-2222-222222222222**", hold_real_image_click)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function(
            "() => window.__YXQ_APP_MOUNTED__ && window.__YXQ_LOAD_HOME_DONE__ === true && !!document.getElementById('boot-card')",
            timeout=15000,
        )
        page.evaluate("() => { const el = document.querySelector('#boot-card .card-info'); if (el) el.scrollTop = 180; }")
        page.wait_for_timeout(120)
        page.locator("#boot-card .card-info").click(position={"x": 12, "y": 12})
        page.locator("#boot-card").wait_for(state="detached", timeout=5000)
        removed = page.evaluate("window.__YXQ_BOOT_CARD__ ? {reason: window.__YXQ_BOOT_CARD__.reason()} : null")
        real_scroll = page.evaluate("() => { const el = document.querySelector('#app .swipe-card .card-info'); return el ? el.scrollTop : -1; }")
        if not removed or removed.get("reason") != "interact":
            raise SmokeFailure(f"点击交接 reason 异常：{removed}")
        if real_scroll < 150:
            raise SmokeFailure(f"点击交接后真实卡未继承滚动位置：real scrollTop={real_scroll}")
        if errors:
            raise SmokeFailure(f"点击交接用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"点击交接: {exc}")
    finally:
        if context is not None:
            try:
                if held_img.get("route"):
                    held_img["route"].abort()
            except Exception:  # noqa: BLE001
                pass
            context.close()
    # 4) 点击穿透：点静态岛 tabbar「消息」不能切到真实消息页，应只 dismiss。
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        held_img = {}

        def patch_bootstrap_clickthrough(route) -> None:
            response = route.fetch()
            body = re.sub(
                r'("photos_thumb":\["?)[^"]*("?)',
                r'\1/cards/photo/33333333-3333-3333-3333-333333333333?size=thumb\2',
                response.text(), count=1, flags=re.S,
            )
            route.fulfill(response=response, body=body)

        def hold_real_image_clickthrough(route) -> None:
            if "route" not in held_img:
                held_img["route"] = route
                return
            route.continue_()

        context.route(base_url.rstrip("/") + "/app", patch_bootstrap_clickthrough)
        context.route("**/cards/photo/33333333-3333-3333-3333-333333333333**", hold_real_image_clickthrough)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function(
            "() => window.__YXQ_APP_MOUNTED__ && window.__YXQ_LOAD_HOME_DONE__ === true && !!document.getElementById('boot-card')",
            timeout=15000,
        )
        page.locator("#boot-card .boot-tab").filter(has_text="消息").click()
        page.locator("#boot-card").wait_for(state="detached", timeout=5000)
        active = page.locator(".van-tabbar-item--active").first.inner_text(timeout=5000)
        if "牵线" not in active:
            raise SmokeFailure(f"点击穿透切到了其他 tab：active={active!r}")
        if page.locator("#app .swipe-card").count() != 1:
            raise SmokeFailure("点击穿透后牵线卡不在")
        if errors:
            raise SmokeFailure(f"点击穿透用例 pageerror {errors[:2]}")
    except Exception as exc:  # noqa: BLE001
        failures.append(f"点击穿透: {exc}")
    finally:
        if context is not None:
            try:
                if held_img.get("route"):
                    held_img["route"].abort()
            except Exception:  # noqa: BLE001
                pass
            context.close()
    return failures


def run_load_home_hang(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    """loadHome 挂起时，壳脚本/app.js 的 4s 硬上限必须移除静态岛并露出启动骨架。"""
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844)
        page = context.new_page()
        errors = page_errors(page)
        # 不节流：本用例验证「已挂载后 loadHome 挂起」的 4s 硬上限，
        # 避免 CPU×4 下 mount 本身超过 4s 把场景变成「尚未开始 loadHome」。
        held = {}

        def hold_home(route) -> None:
            if "route" not in held:
                held["route"] = route
                return
            route.continue_()

        def strip_bootstrap(route) -> None:
            response = route.fetch()
            body = re.sub(
                r"window\.__YXQ_BOOTSTRAP__=\{.*?\};",
                "window.__YXQ_BOOTSTRAP__=null;",
                response.text(), count=1, flags=re.S,
            )
            route.fulfill(response=response, body=body)

        context.route(base_url.rstrip("/") + "/app", strip_bootstrap)
        context.route("**/legacy/api/home", hold_home)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.locator(".app-boot-skeleton").wait_for(state="visible", timeout=15000)
        page.locator("#boot-card").wait_for(state="detached", timeout=8000)
        removed = page.evaluate("window.__YXQ_BOOT_CARD__ ? {at: window.__YXQ_BOOT_CARD__.removedAt(), reason: window.__YXQ_BOOT_CARD__.reason()} : null")
        if not removed or removed["at"] is None or removed["at"] > 6500:
            raise SmokeFailure(f"loadHome 挂起时 静态岛移除过晚：{removed}")
        if removed["reason"] not in ("load-timeout", "hard-timeout"):
            raise SmokeFailure(f"loadHome 挂起时 静态岛移除原因异常：{removed}")
        page.locator(".app-boot-skeleton").wait_for(state="visible", timeout=5000)
        page.screenshot(path=str(out_dir / "hero-load-home-hang-390.png"))
        if errors:
            raise SmokeFailure(f"loadHome 挂起用例 pageerror {errors[:2]}")
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"loadHome 挂起硬上限: {exc}"]
    finally:
        if context is not None:
            try:
                if held.get("route"):
                    held["route"].abort()
            except Exception:  # noqa: BLE001 - best effort cleanup
                pass
            context.close()


def run_dark(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    context = None
    try:
        context = make_context(browser, base_url, cookie, 390, 844, dark=True)
        page = context.new_page()
        throttle(context, page)
        page.goto(base_url + "/app", wait_until="domcontentloaded", timeout=40000)
        page.wait_for_function("() => !!window.__hero_snapshot", timeout=10000)
        snap = page.evaluate("window.__hero_snapshot")
        if snap["bg"] != "rgb(17, 17, 17)":
            raise SmokeFailure(f"暗色 hero 背景 {snap['bg']!r}")
        if snap["cardBg"] not in ("rgb(34, 34, 38)", "rgba(0, 0, 0, 0)"):
            raise SmokeFailure(f"暗色卡片背景 {snap['cardBg']!r}")
        page.screenshot(path=str(out_dir / "hero-dark-390.png"))
        wait_hero_gone(page)
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"暗色: {exc}"]
    finally:
        if context is not None:
            context.close()


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/b5-hero"))
    out_dir.mkdir(parents=True, exist_ok=True)
    failures: list[str] = []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(args=["--no-sandbox"])
            for width, height in ((390, 844), (1280, 900), (375, 667), (768, 1024)):
                failures.extend(run_viewport(browser, base_url, cookie, width, height, out_dir, throttle_net=True))
            failures.extend(run_interactions(browser, base_url, cookie, out_dir))
            failures.extend(run_fallbacks(browser, base_url, cookie, out_dir))
            failures.extend(run_takeover_regressions(browser, base_url, cookie, out_dir))
            failures.extend(run_load_home_hang(browser, base_url, cookie, out_dir))
            failures.extend(run_dark(browser, base_url, cookie, out_dir))
            browser.close()
    except Exception as exc:  # noqa: BLE001
        failures.append(f"fatal: {exc}")
    if failures:
        for item in failures:
            print("FAIL", item)
        return 1
    print("PASS boot-card/deck smoke (390/1280/375/768, dark, interactions, fallbacks)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
