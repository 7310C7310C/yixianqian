#!/usr/bin/env python3
"""废弃独立管理台入口 / 内嵌管理台过场 / 改密页全宽回归（Playwright）。

覆盖（390×844 与 1280×900 两个视口）：

1. 浏览器顶层打开 /admin：服务端 303 到 /?admin=1，SPA 载入后自动开内嵌管理台，
   顶层 URL 清回 /；无 Sec-Fetch-Dest 的请求仍 200（测试/API 兼容）；
2. 内嵌 iframe 首载 /admin?embed=1，骨架先出、iframe 后载；iframe 内部导航到
   /admin/users（无 query）仍能加载，顶层 URL 不变；
3. 登录 next=/admin：登录页隐藏 next 被改写为 /?admin=1，登录后落地 SPA 并自动开管理台；
4. 非 admin 访问 /?admin=1：不打开管理台，query 清掉；
5. 修改密码页与「我的资料」同宽（视口宽 - 24），返回列 full width 且不遮内容；
   page-slide 过场中改密页背景不透明、relative z-index，返回后还原「我的」滚动位置；
6. 管理台 overlay 以 embed-slide 滑入（20px + opacity，180ms），尊重 reduced-motion；
7. 全程 console error / pageerror 为 0，并输出关键截图。

需要已部署的 staging 与两个可登录账号的原始 session cookie；登录流程另需
一个已知密码的 admin 账号。不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    ADMIN_SESSION=<yxq_session 原始值> \
    ADMIN_PHONE=138... ADMIN_PASSWORD=<密码> \
    NONADMIN_SESSION=<另一个普通账号 yxq_session> \
    SHOT_DIR=/tmp/opencode/admin-embed-entry \
    python3 tools/browser/admin_embed_entry.py

ADMIN_PHONE/ADMIN_PASSWORD 缺失时只跳过登录 next=/admin 一项；NONADMIN_SESSION
缺失时只跳过非 admin 一项，其余照跑。密码只从环境读取，不写入日志。
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
ADMIN_NAV_TEXT = "用户"


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def wait(ms: int = 250) -> None:
    time.sleep(ms / 1000)


class ConsoleGuard:
    """收集 console error / pageerror；任何一条都算失败。"""

    def __init__(self, page) -> None:
        self.page = page
        self.errors: list[str] = []
        page.on("console", self._on_console)
        page.on("pageerror", lambda exc: self.errors.append(f"pageerror: {exc}"))

    def _on_console(self, msg) -> None:
        if msg.type != "error":
            return
        location = (getattr(msg, "location", None) or {}).get("url", "")
        if "legacy/api/notifications/stream" in location or "notifications/stream" in msg.text:
            return
        self.errors.append(f"console: {msg.text} ({location})")

    def assert_clean(self, context: str) -> None:
        if self.errors:
            raise SmokeFailure(f"{context}: console/pageerror 非空:\n  " + "\n  ".join(self.errors))


def add_session_cookie(context, base: str, raw: str) -> None:
    parsed = urlparse(base)
    context.add_cookies([{
        "name": "yxq_session",
        "value": raw,
        "domain": parsed.hostname,
        "path": "/",
        "secure": parsed.scheme == "https",
        "sameSite": "Lax",
    }])


OBSERVER_JS = """
window.__embedClassLog = [];
window.__passwordClassLog = [];
(() => {
  const record = (node, log) => {
    if (!node || node.nodeType !== 1 || !node.classList) return;
    const cs = Array.from(node.classList);
    if (cs.length) log.push(cs);
  };
  const start = () => {
    const obs = new MutationObserver(muts => {
      for (const m of muts) {
        for (const node of m.addedNodes) {
          record(node, window.__embedClassLog);
          if (node.querySelectorAll) {
            node.querySelectorAll('.embed-overlay').forEach(el => record(el, window.__embedClassLog));
            node.querySelectorAll('.password-page').forEach(el => record(el, window.__passwordClassLog));
          }
          if (node.classList && node.classList.contains('password-page')) {
            record(node, window.__passwordClassLog);
          }
        }
      }
    });
    obs.observe(document.body, {childList: true, subtree: true});
  };
  if (document.body) start();
  else document.addEventListener('DOMContentLoaded', start, {once: true});
})();
"""


def install_overlay_observer(page) -> None:
    page.add_init_script(OBSERVER_JS)


def reset_class_logs(page) -> None:
    page.evaluate("""() => {
      window.__embedClassLog = [];
      window.__passwordClassLog = [];
    }""")


def overlay_classes(page) -> list[list[str]]:
    return page.evaluate("() => window.__embedClassLog || []")


def password_classes(page) -> list[list[str]]:
    return page.evaluate("() => window.__passwordClassLog || []")


def stylesheet_has_embed_slide(page) -> dict:
    return page.evaluate("""() => {
      const out = {};
      const scan = (rules, reduced) => {
        for (const r of rules) {
          if (r.cssRules && r.media && r.media.mediaText &&
              r.media.mediaText.includes('prefers-reduced-motion')) {
            scan(Array.from(r.cssRules || []), true);
            continue;
          }
          const sel = r.selectorText || '';
          if (sel.includes('.embed-slide-enter-from')) {
            out.enterFrom = {transform: r.style.transform, opacity: r.style.opacity};
          }
          if (sel.includes('.embed-slide-enter-active')) {
            if (reduced) out.reducedMotion = true;
            else out.enterActive = {transition: r.style.transition};
          }
        }
      };
      for (const sheet of Array.from(document.styleSheets)) {
        try { scan(Array.from(sheet.cssRules || []), false); } catch (e) { /* cross-origin */ }
      }
      return out;
    }""")


def dismiss_guides(page) -> None:
    """关掉首次进入的滑动引导/活动/更新说明等一次性弹层。"""
    page.wait_for_timeout(800)
    for _ in range(6):
        acted = False
        guide = page.locator(".swipe-guide-mask .sg-btn")
        if guide.count() and guide.first.is_visible():
            try:
                guide.first.click(timeout=2000)
                acted = True
            except PlaywrightTimeoutError:
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
                except PlaywrightTimeoutError:
                    pass
                break
        if not acted:
            return
        page.wait_for_timeout(400)


def tap_tab(page, label: str) -> None:
    item = page.locator(".van-tabbar-item").filter(has_text=label)
    for _ in range(4):
        dismiss_guides(page)
        try:
            item.click(timeout=4000)
            return
        except PlaywrightTimeoutError:
            page.wait_for_timeout(500)
    raise SmokeFailure(f"无法点击底部 tab「{label}」（被引导/弹层遮挡？）")


def box(locator) -> dict:
    b = locator.bounding_box()
    if not b:
        raise SmokeFailure("元素没有 bounding box")
    return b


def goto_admin_top_level(page, base: str, shot_dir: Path, tag: str) -> None:
    """顶层 /admin → 303 → SPA 自动 overlay；拦截 iframe 请求证明骨架先出。"""
    held = {}

    def hold_admin_route(url):
        parsed = urlparse(url)
        return parsed.path == "/admin" and "embed=1" in parsed.query

    def hold(route):
        held["route"] = route

    reset_class_logs(page)
    page.route(hold_admin_route, hold)
    page.goto(base + "/admin", wait_until="domcontentloaded", timeout=30000)
    try:
        page.locator(".embed-overlay").wait_for(state="visible", timeout=20000)
        page.locator(".embed-skeleton").wait_for(state="visible", timeout=5000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: /admin 未自动打开内嵌管理台：{exc}") from exc

    final = urlparse(page.url)
    if final.path != "/" or final.query:
        raise SmokeFailure(f"{tag}: 顶层 URL 未清回 /（实际 {page.url}）")

    if "route" not in held:
        raise SmokeFailure(f"{tag}: iframe 请求未被挂起，无法验证骨架先出")
    if page.locator(".embed-frame--ready").count():
        raise SmokeFailure(f"{tag}: 骨架阶段 iframe 已就绪（应等请求放行）")
    page.screenshot(path=str(shot_dir / f"{tag}-admin-skeleton.png"))

    held["route"].continue_()
    page.unroute(hold_admin_route)
    try:
        page.frame_locator(".embed-frame").locator(".admin-nav").wait_for(timeout=20000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 内嵌管理台 iframe 未载入：{exc}") from exc

    page.locator(".embed-loading").wait_for(state="hidden", timeout=10000)
    src = page.locator(".embed-frame").get_attribute("src") or ""
    if "/admin?embed=1" not in src:
        raise SmokeFailure(f"{tag}: iframe src 未带 embed=1（{src}）")

    classes = overlay_classes(page)
    flat = [c for group in classes for c in group]
    if "embed-slide-enter-active" not in flat or "embed-slide-enter-from" not in flat:
        raise SmokeFailure(f"{tag}: overlay 未走 embed-slide 过场（捕获 {classes}）")
    page.wait_for_timeout(400)
    state = page.evaluate("""() => {
      const el = document.querySelector('.embed-overlay');
      if (!el) return null;
      const s = getComputedStyle(el);
      return {opacity: s.opacity, transform: s.transform};
    }""")
    if not state or state["opacity"] != "1" or state["transform"] not in ("none", "matrix(1, 0, 0, 1, 0, 0)"):
        raise SmokeFailure(f"{tag}: 管理台滑入后未回到稳定态：{state}")
    page.screenshot(path=str(shot_dir / f"{tag}-admin-overlay.png"))

    # iframe 内部导航无 query，仍应渲染 SSR（Sec-Fetch-Dest: iframe）。
    frame = page.frame_locator(".embed-frame")
    frame.locator(".admin-nav a", has_text=ADMIN_NAV_TEXT).click()
    try:
        page.frame_locator(".embed-frame").locator(".admin-filter").wait_for(timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: iframe 内部导航 /admin/users 失败：{exc}") from exc
    inner_urls = [urlparse(f.url) for f in page.frames if "/admin" in f.url]
    if not inner_urls or inner_urls[0].path != "/admin/users" or inner_urls[0].query:
        raise SmokeFailure(f"{tag}: iframe 内部导航 URL 异常：{[u.geturl() for u in inner_urls]}")
    top = urlparse(page.url)
    if top.path != "/" or top.query:
        raise SmokeFailure(f"{tag}: iframe 内部导航改变了顶层 URL（{page.url}）")

    page.locator(".embed-back").click()
    page.locator(".embed-overlay").wait_for(state="detached", timeout=5000)


def check_direct_requests(page, base: str, tag: str) -> None:
    """服务端契约：document 303；iframe / 无 header 仍 200（登录态下）。"""
    admin_url = base + "/admin"
    resp = page.request.get(admin_url, headers={"Sec-Fetch-Dest": "document"}, max_redirects=0)
    if resp.status != 303 or resp.headers.get("location") != "/?admin=1":
        raise SmokeFailure(
            f"{tag}: document /admin = {resp.status} location={resp.headers.get('location')!r}，"
            "want 303 /?admin=1")
    resp = page.request.get(admin_url, headers={"Sec-Fetch-Dest": "iframe"})
    if resp.status != 200:
        raise SmokeFailure(f"{tag}: iframe /admin = {resp.status}，want 200")
    resp = page.request.get(admin_url)
    if resp.status != 200:
        raise SmokeFailure(f"{tag}: 无 header /admin = {resp.status}，want 200（测试/API 兼容）")

    slide = stylesheet_has_embed_slide(page)
    enter_from = slide.get("enterFrom") or {}
    enter_active = slide.get("enterActive") or {}
    if enter_from.get("transform") != "translateX(20px)" or enter_from.get("opacity") != "0":
        raise SmokeFailure(f"{tag}: embed-slide-enter-from 不符（{slide}）")
    if "0.18s" not in (enter_active.get("transition") or ""):
        raise SmokeFailure(f"{tag}: embed-slide 过场时长不是 180ms（{slide}）")
    if not slide.get("reducedMotion"):
        raise SmokeFailure(f"{tag}: embed-slide 缺少 prefers-reduced-motion 关闭规则（{slide}）")


def check_login_next_admin(browser, base: str, shot_dir: Path, tag: str) -> None:
    phone = os.environ.get("ADMIN_PHONE", "").strip()
    password = os.environ.get("ADMIN_PASSWORD", "").strip()
    if not phone or not password:
        print(f"[skip] {tag}: 未提供 ADMIN_PHONE/ADMIN_PASSWORD，跳过登录 next=/admin")
        return
    context = browser.new_context(viewport={"width": 390, "height": 844})
    page = context.new_page()
    guard = ConsoleGuard(page)
    try:
        page.goto(base + "/login?next=%2Fadmin", wait_until="domcontentloaded", timeout=30000)
        hidden = page.locator('input[name="next"]').input_value()
        if hidden != "/?admin=1":
            raise SmokeFailure(f"{tag}: 登录页 next 未改写为 /?admin=1（实际 {hidden!r}）")
        page.fill('input[name="phone"]', phone)
        page.fill('input[name="password"]', password)
        page.click('button[type="submit"]')
        try:
            page.locator(".embed-overlay").wait_for(state="visible", timeout=20000)
        except PlaywrightTimeoutError as exc:
            raise SmokeFailure(f"{tag}: 登录 next=/admin 后未自动开管理台：{exc}") from exc
        final = urlparse(page.url)
        if final.path != "/" or final.query:
            raise SmokeFailure(f"{tag}: 登录后 URL 未清回 /（{page.url}）")
        page.frame_locator(".embed-frame").locator(".admin-nav").wait_for(timeout=20000)
        page.screenshot(path=str(shot_dir / f"{tag}-login-next-admin.png"))
        guard.assert_clean(f"{tag} 登录 next=/admin")
    finally:
        context.close()


def check_non_admin(browser, base: str, shot_dir: Path, tag: str) -> None:
    raw = os.environ.get("NONADMIN_SESSION", "").strip()
    if not raw:
        print(f"[skip] {tag}: 未提供 NONADMIN_SESSION，跳过非 admin ?admin=1")
        return
    context = browser.new_context(viewport={"width": 390, "height": 844})
    add_session_cookie(context, base, raw)
    page = context.new_page()
    guard = ConsoleGuard(page)
    try:
        page.goto(base + "/?admin=1", wait_until="domcontentloaded", timeout=30000)
        try:
            page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        except PlaywrightTimeoutError as exc:
            raise SmokeFailure(f"{tag}: 非 admin 首页未载入：{exc}") from exc
        page.wait_for_timeout(800)
        if page.locator(".embed-overlay").count():
            raise SmokeFailure(f"{tag}: 非 admin 的 ?admin=1 打开了管理台")
        final = urlparse(page.url)
        if final.query:
            raise SmokeFailure(f"{tag}: 非 admin 的 ?admin=1 未清 query（{page.url}）")
        page.screenshot(path=str(shot_dir / f"{tag}-nonadmin-ignored.png"))
        guard.assert_clean(f"{tag} 非 admin")
    finally:
        context.close()


def check_password_page(page, base: str, shot_dir: Path, tag: str, viewport_width: int) -> None:
    dismiss_guides(page)
    tap_tab(page, "我的")
    cell = page.locator(".van-cell", has_text="修改密码").first
    cell.wait_for(state="visible", timeout=10000)
    page.evaluate("() => window.scrollTo(0, document.body.scrollHeight)")
    page.wait_for_timeout(300)
    cell.scroll_into_view_if_needed()
    page.wait_for_timeout(200)
    before_scroll = page.evaluate("() => window.scrollY")

    reset_class_logs(page)
    cell.click()
    try:
        page.locator(".password-page").wait_for(state="visible", timeout=5000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 修改密码页未出现：{exc}") from exc
    page.wait_for_timeout(400)

    classes = password_classes(page)
    flat = [c for group in classes for c in group]
    if "page-slide-enter-active" not in flat:
        raise SmokeFailure(f"{tag}: 修改密码页未沿用 page-slide（捕获 {classes}）")

    nav = page.locator(".password-page .back-nav")
    card = page.locator(".password-page .pw-card")
    nav_box, card_box = box(nav), box(card)
    if abs(nav_box["width"] - viewport_width) > 1:
        raise SmokeFailure(f"{tag}: 返回列不是 full width（{nav_box['width']} vs {viewport_width}）")
    want_card = viewport_width - 24
    if abs(card_box["width"] - want_card) > 2:
        raise SmokeFailure(f"{tag}: 改密卡片宽度 {card_box['width']}，want {want_card}（与我的资料同宽）")
    if card_box["y"] < nav_box["y"] + nav_box["height"] - 1:
        raise SmokeFailure(f"{tag}: 改密卡片被返回列遮住（{card_box} vs {nav_box}）")

    page_style = page.evaluate("""() => {
      const el = document.querySelector('.password-page');
      const s = getComputedStyle(el);
      return {position: s.position, zIndex: s.zIndex, background: s.backgroundColor};
    }""")
    if page_style["position"] != "relative" or page_style["zIndex"] == "auto":
        raise SmokeFailure(f"{tag}: 改密页过场层级不符（{page_style}）")
    if page_style["background"] in ("rgba(0, 0, 0, 0)", "transparent"):
        raise SmokeFailure(f"{tag}: 改密页背景透明，过场会闪出我的页（{page_style}）")
    page.screenshot(path=str(shot_dir / f"{tag}-password-page.png"))

    page.locator(".password-page .back-nav").click()
    page.locator(".password-page").wait_for(state="detached", timeout=5000)
    page.wait_for_timeout(400)
    after_scroll = page.evaluate("() => window.scrollY")
    if abs(after_scroll - before_scroll) > 2:
        raise SmokeFailure(f"{tag}: 返回后滚动位置未还原（{after_scroll} vs {before_scroll}）")


def check_admin_from_profile(page, base: str, shot_dir: Path, tag: str) -> None:
    dismiss_guides(page)
    tap_tab(page, "我的")
    cell = page.locator(".van-cell", has_text="管理后台").first
    try:
        cell.wait_for(state="visible", timeout=10000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 我的页没有管理后台入口（admin 账号？）：{exc}") from exc
    held = {}

    def admin_route(url):
        parsed = urlparse(url)
        return parsed.path == "/admin" and "embed=1" in parsed.query

    reset_class_logs(page)
    page.route(admin_route, lambda route: held.setdefault("route", route))
    cell.click()
    page.locator(".embed-overlay").wait_for(state="visible", timeout=5000)
    page.locator(".embed-skeleton").wait_for(state="visible", timeout=5000)
    if "route" not in held:
        raise SmokeFailure(f"{tag}: 我的→管理后台 iframe 请求未被挂起")
    held["route"].continue_()
    page.unroute(admin_route)
    classes = overlay_classes(page)
    flat = [c for group in classes for c in group]
    if "embed-slide-enter-active" not in flat or "embed-slide-enter-from" not in flat:
        raise SmokeFailure(f"{tag}: 我的→管理后台未走 embed-slide（捕获 {classes}）")
    page.frame_locator(".embed-frame").locator(".admin-nav").wait_for(timeout=20000)
    page.locator(".embed-loading").wait_for(state="hidden", timeout=10000)
    page.screenshot(path=str(shot_dir / f"{tag}-admin-from-profile.png"))
    page.locator(".embed-back").click()
    page.locator(".embed-overlay").wait_for(state="detached", timeout=5000)
    final = urlparse(page.url)
    if final.path != "/" or final.query:
        raise SmokeFailure(f"{tag}: 关闭管理台后顶层 URL 不干净（{page.url}）")


def run_viewport(browser, base: str, width: int, height: int, shot_dir: Path) -> None:
    tag = f"{width}"
    context = browser.new_context(viewport={"width": width, "height": height})
    add_session_cookie(context, base, require("ADMIN_SESSION"))
    page = context.new_page()
    install_overlay_observer(page)
    guard = ConsoleGuard(page)
    try:
        goto_admin_top_level(page, base, shot_dir, tag)
        check_direct_requests(page, base, tag)
        check_password_page(page, base, shot_dir, tag, width)
        check_admin_from_profile(page, base, shot_dir, tag)
        guard.assert_clean(f"{tag} admin")
        print(f"[ok] {tag}x{height} 顶层重定向 / iframe / 骨架 / 过场 / 改密页 / 返回还原")
    finally:
        context.close()

    check_login_next_admin(browser, base, shot_dir, tag)
    check_non_admin(browser, base, shot_dir, tag)


def main() -> int:
    base = require("BASE_URL").rstrip("/")
    shot_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/admin-embed-entry"))
    shot_dir.mkdir(parents=True, exist_ok=True)
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch()
            try:
                for width, height in VIEWPORTS:
                    run_viewport(browser, base, width, height, shot_dir)
            finally:
                browser.close()
    except SmokeFailure as exc:
        print(f"[FAIL] {exc}", file=sys.stderr)
        return 1
    print(f"[PASS] 全部通过；截图目录 {shot_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
