#!/usr/bin/env python3
"""牵线 tab 爱心光晕 + 全览模式浏览器回归（Playwright）。

固定以下行为：

1. 停在「牵线」tab 时，底栏爱心带 `.tab-heart-live`，图标/光晕分别跑
   `yxqHeartBeat` / `yxqHeartHalo` 2.4s 动画；切到其他 tab 后 class 与动画消失。
2. 从其他 tab 点「牵线」只切 tab，不打开全览；已在牵线时再点一次才进入全览。
3. 全览是独立子页：底栏隐藏、页头「全览」、按注册时间倒序（与接口顺序逐项一致）。
4. 滚动触底自动加载下一页；点卡片进资料页，返回后列表与滚动位置保留。
5. 全览页 0 console error / pageerror。

需要已登录 staging 与 yxq_session：

    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/cards_overview_smoke.py

部署前可用本机未部署的前端资源验证（接口仍走 staging）：

    LOCAL_ASSETS=1 BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/cards_overview_smoke.py

可选：SHOT_DIR 截图目录（默认 /tmp/opencode/cards-overview-smoke）。
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

ROOT = Path(__file__).resolve().parents[2]
# LEGACY_DIR 允许用上传到临时目录的未部署生成物做部署前验证。
LEGACY = Path(os.environ.get("LEGACY_DIR") or (ROOT / "web" / "static" / "legacy"))
VIEWPORTS = ((390, 844), (1280, 900))


def viewports_from_env():
    raw = os.environ.get("VIEWPORTS", "").strip()
    if not raw:
        return VIEWPORTS
    out = []
    for part in raw.split(","):
        w, h = part.strip().lower().split("x", 1)
        out.append((int(w), int(h)))
    return tuple(out)
IGNORED_CONSOLE = ("notifications/stream", "net::ERR_ABORTED")


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def attach_errors(page) -> list[str]:
    errors: list[str] = []
    page.on(
        "console",
        lambda msg: errors.append(f"console: {msg.text}")
        if msg.type == "error" and not any(x in msg.text for x in IGNORED_CONSOLE)
        else None,
    )
    page.on("pageerror", lambda exc: errors.append(f"pageerror: {exc}"))
    return errors


def dismiss_guides(page) -> None:
    page.wait_for_timeout(500)
    try:
        page.locator("#boot-skeleton").wait_for(state="detached", timeout=15000)
    except PlaywrightTimeoutError:
        pass
    for _ in range(6):
        acted = False
        guide = page.locator(".swipe-guide-mask .sg-btn")
        if guide.count() and guide.first.is_visible():
            try:
                guide.first.click(timeout=2000)
                acted = True
            except PlaywrightTimeoutError:
                pass
        for text in ("知道了", "关闭"):
            locator = page.get_by_text(text, exact=True)
            for i in range(locator.count()):
                if locator.nth(i).is_visible():
                    try:
                        locator.nth(i).click(timeout=1500)
                        acted = True
                    except PlaywrightTimeoutError:
                        pass
                    break
        if not acted:
            return
        page.wait_for_timeout(250)


def tab_item(page, label: str):
    return page.locator(".van-tabbar-item").filter(has_text=label).first


def touch_swipe(page, selector: str, dx: int) -> bool:
    """在元素上派发一次水平触摸滑动（Chromium 支持 TouchEvent 构造）。"""
    return page.evaluate(
        """({sel, dx}) => {
            const el = document.querySelector(sel);
            if (!el) return false;
            const r = el.getBoundingClientRect();
            const x = r.left + r.width * 0.7;
            const y = r.top + r.height * 0.4;
            const mk = (cx) => new Touch({identifier: 1, target: el, clientX: cx, clientY: y});
            const fire = (type, cx, active) => el.dispatchEvent(new TouchEvent(type, {
                bubbles: true, cancelable: true,
                touches: active ? [mk(cx)] : [],
                changedTouches: [mk(cx)],
            }));
            fire('touchstart', x, true);
            fire('touchmove', x + dx / 2, true);
            fire('touchend', x + dx, false);
            return true;
        }""",
        {"sel": selector, "dx": dx},
    )


def touch_drag(page, selector: str, dy: int) -> bool:
    """在元素上派发一次垂直触摸拖拽（用于下滑收卡/滚动守卫）。"""
    return page.evaluate(
        """({sel, dy}) => {
            const el = document.querySelector(sel);
            if (!el) return false;
            const r = el.getBoundingClientRect();
            const x = r.left + r.width * 0.5;
            const y = r.top + Math.min(r.height * 0.4, 120);
            const mk = (cy) => new Touch({identifier: 3, target: el, clientX: x, clientY: cy});
            const fire = (type, cy, active) => el.dispatchEvent(new TouchEvent(type, {
                bubbles: true, cancelable: true,
                touches: active ? [mk(cy)] : [],
                changedTouches: [mk(cy)],
            }));
            fire('touchstart', y, true);
            fire('touchmove', y + dy / 2, true);
            fire('touchmove', y + dy, true);
            fire('touchend', y + dy, false);
            return true;
        }""",
        {"sel": selector, "dy": dy},
    )


def touch_drag_start(page, selector: str, dy: int) -> bool:
    """按下并拖到中途；保留起始坐标与元素，供 touch_drag_move/finish 继续同一手势。"""
    return page.evaluate(
        """({sel, dy}) => {
            const el = document.querySelector(sel);
            if (!el) return false;
            const r = el.getBoundingClientRect();
            const x = r.left + r.width * 0.5;
            const y = r.top + Math.min(r.height * 0.4, 120);
            window.__ovDragStart = {x: x, y: y, el: el};
            const mk = (cy) => new Touch({identifier: 5, target: el, clientX: x, clientY: cy});
            const fire = (type, cy, active) => el.dispatchEvent(new TouchEvent(type, {
                bubbles: true, cancelable: true,
                touches: active ? [mk(cy)] : [],
                changedTouches: [mk(cy)],
            }));
            fire('touchstart', y, true);
            fire('touchmove', y + dy, true);
            return true;
        }""",
        {"sel": selector, "dy": dy},
    )


def touch_drag_move(page, dy: int) -> bool:
    """在手势中途再发一次 move（坐标基于 touch_drag_start 保存的起点）。"""
    return page.evaluate(
        """({dy}) => {
            const st = window.__ovDragStart;
            if (!st || !st.el) return false;
            const mk = (cy) => new Touch({identifier: 5, target: st.el, clientX: st.x, clientY: cy});
            st.el.dispatchEvent(new TouchEvent('touchmove', {
                bubbles: true, cancelable: true,
                touches: [mk(st.y + dy)],
                changedTouches: [mk(st.y + dy)],
            }));
            return true;
        }""",
        {"dy": dy},
    )


def touch_drag_finish(page, selector: str, dy: int) -> bool:
    """完成 touch_drag_start 的手势（同一元素与起始坐标，元素滚动/位移也不影响）。"""
    return page.evaluate(
        """({dy}) => {
            const st = window.__ovDragStart;
            if (!st || !st.el) return false;
            const mk = (cy) => new Touch({identifier: 5, target: st.el, clientX: st.x, clientY: cy});
            const fire = (type, cy, active) => st.el.dispatchEvent(new TouchEvent(type, {
                bubbles: true, cancelable: true,
                touches: active ? [mk(cy)] : [],
                changedTouches: [mk(cy)],
            }));
            fire('touchmove', st.y + dy, true);
            fire('touchend', st.y + dy, false);
            return true;
        }""",
        {"dy": dy},
    )


def parse_motion(style: str) -> tuple[float, float]:
    """从 inline transform 里取 translateY / scale。"""
    my = re.search(r"translate3d\(\s*-?0(?:px)?\s*,\s*(-?[\d.]+)px\s*,\s*-?0(?:px)?\s*\)", style or "")
    ms = re.search(r"scale\(([\d.]+)\)", style or "")
    return (float(my.group(1)) if my else 0.0, float(ms.group(1)) if ms else 1.0)


def wait_real_page(page, selector: str, label: str) -> None:
    """等真正的页面挂上，并等跟手过场预览层撤掉（预览层里也有 page-host，不能只看类名）。"""
    page.wait_for_selector(".tab-main > " + selector, timeout=15000)
    try:
        page.wait_for_selector(".ov-peek-clip", state="detached", timeout=5000)
    except PlaywrightTimeoutError:
        raise SmokeFailure(f"{label}: 跟手过场预览层未撤掉") from None


def assert_scrollbar_theme(page, label: str, require_webkit: bool = True) -> bool:
    """桌面滚动条要跟随日夜主题：暗色变量必须是亮色，且与日间不同。"""
    state = page.evaluate(
        """() => {
            const root = document.documentElement;
            if (!matchMedia('(hover: hover) and (pointer: fine)').matches) return {skip: true};
            const el = document.querySelector('.tab-main > .cards-page .card-info, .tab-main > .cards-page .msg-list');
            if (!el) return null;
            const had = root.classList.contains('van-theme-dark');
            const thumb = (node) => {
                try { return getComputedStyle(node, '::-webkit-scrollbar-thumb').backgroundColor; }
                catch (e) { return ''; }
            };
            const lc = getComputedStyle(el);
            const light = {
                v: lc.getPropertyValue('--yxq-scroll-thumb').trim(),
                c: lc.scrollbarColor,
                t: thumb(el),
            };
            root.classList.add('van-theme-dark');
            const dc = getComputedStyle(el);
            const dark = {
                v: dc.getPropertyValue('--yxq-scroll-thumb').trim(),
                c: dc.scrollbarColor,
                t: thumb(el),
            };
            if (!had) root.classList.remove('van-theme-dark');
            return {light: light, dark: dark};
        }"""
    )
    if not state:
        raise SmokeFailure(f"{label}: 找不到卡片滚动区")
    if state.get("skip"):
        return False
    if "255" not in state["dark"]["v"] or state["dark"]["v"] == state["light"]["v"]:
        raise SmokeFailure(f"{label}: 暗色滚动条变量未切换：{state}")
    if "auto" in state["dark"]["c"] or "255" not in state["dark"]["c"]:
        raise SmokeFailure(f"{label}: 暗色 scrollbar-color 未生效/未用亮色：{state}")
    if require_webkit and "255" not in state["dark"]["t"]:
        raise SmokeFailure(f"{label}: 暗色 ::-webkit-scrollbar-thumb 未用亮色：{state}")
    return True


def assert_no_card_enter_anim(page, label: str) -> None:
    """回到牵线后，卡片应直接在最终位置出现；不能重播上一次切卡的 slide-in 动画。"""
    state = page.evaluate(
        """() => {
            const el = document.querySelector('.tab-main > .cards-page .swipe-card');
            return el ? {cls: el.className, anim: getComputedStyle(el).animationName} : null;
        }"""
    )
    if not state:
        raise SmokeFailure(f"{label}: 回到牵线后找不到卡片")
    if "slide-in-" in state["cls"] or state["anim"] != "none":
        raise SmokeFailure(f"{label}: 回到牵线后卡片仍在播进入动画：{state}")


def apply_overview_filter(page, placeholder: str, value: str) -> None:
    page.locator(".ov-filter").click()
    page.wait_for_selector(".filter-title", timeout=5000)
    page.get_by_placeholder(placeholder).fill(value)
    page.get_by_text("应用筛选", exact=True).click()


def clear_overview_filter(page) -> None:
    page.locator(".ov-filter").click()
    page.wait_for_selector(".filter-title", timeout=5000)
    page.locator(".filter-title").locator("..").get_by_text("清除筛选", exact=True).click()
    page.wait_for_function(
        "() => document.querySelectorAll('.ov-tile[data-uid]').length > 0",
        timeout=15000,
    )


def click_tab(page, label: str, marker: str | None = None) -> None:
    # 已在目标页就不要点：牵线 tab 在激活态再点一次会进全览（产品行为）。
    if marker and page.locator(marker).count():
        return
    last_error = None
    for _ in range(6):
        dismiss_guides(page)
        try:
            tab_item(page, label).click(timeout=4000)
            if marker:
                page.wait_for_selector(marker, timeout=10000)
            return
        except PlaywrightTimeoutError as exc:
            last_error = exc
            # 第一次点击可能已切页、只是 marker 等待超时；此时不能再点牵线（会进全览）。
            if marker and page.locator(marker).count():
                return
            # 晚到的一次性弹层（不同视口/慢网下出现时机不同）会盖住底栏：关掉再试。
            dismiss_guides(page)
            page.keyboard.press("Escape")
            page.wait_for_timeout(350)
    raise SmokeFailure(f"无法点击 tab {label}（6 次尝试）：{last_error}")


def tap_cards_again(page) -> None:
    """已激活的牵线 tab 再点一次：晚到弹层可能盖住底栏，关掉后重试。"""
    last_error = None
    for _ in range(3):
        dismiss_guides(page)
        try:
            tab_item(page, "牵线").click(timeout=4000)
            return
        except PlaywrightTimeoutError as exc:
            last_error = exc
            dismiss_guides(page)
            page.keyboard.press("Escape")
            page.wait_for_timeout(300)
    raise SmokeFailure(f"无法再次点击牵线 tab：{last_error}")


def install_local_assets(context, base_url: str) -> None:
    """把本机未部署的壳/主脚本/render chunk/CSS 顶替进 staging 页面。"""
    shell = (LEGACY / "index.html").read_text(encoding="utf-8")
    shell = shell.replace("window.__YXQ_AUTHENTICATED__=false;", "window.__YXQ_AUTHENTICATED__=true;")
    files = {
        "/static/legacy/app.min.js": ("application/javascript; charset=utf-8", LEGACY / "app.min.js"),
        "/static/legacy/app.render.js": ("application/javascript; charset=utf-8", LEGACY / "app.render.js"),
        "/static/legacy/pages/detail.render.js": (
            "application/javascript; charset=utf-8",
            LEGACY / "pages" / "detail.render.js",
        ),
        "/static/legacy/app.css": ("text/css; charset=utf-8", LEGACY / "app.css"),
        "/static/legacy/app.critical.css": ("text/css; charset=utf-8", LEGACY / "app.critical.css"),
    }
    base = urlparse(base_url)

    def handler(route, request):
        parsed = urlparse(request.url)
        if parsed.netloc != base.netloc:
            route.fallback()
            return
        if request.resource_type == "document" and parsed.path.rstrip("/") in ("", "/app"):
            route.fulfill(status=200, content_type="text/html; charset=utf-8", body=shell)
            return
        target = files.get(parsed.path)
        if target:
            content_type, path = target
            route.fulfill(status=200, content_type=content_type, body=path.read_bytes())
            return
        route.fallback()

    context.route("**/*", handler)


def glow_state(page) -> dict:
    return page.evaluate(
        """() => {
            const item = [...document.querySelectorAll('.van-tabbar-item')]
                .find((el) => (el.innerText || '').includes('牵线'));
            if (!item) return {missing: true};
            const wrapper = item.querySelector('.van-tabbar-item__icon');
            const icon = wrapper && wrapper.querySelector('.van-icon');
            const iconStyle = icon ? getComputedStyle(icon) : null;
            const haloStyle = wrapper ? getComputedStyle(wrapper, '::before') : null;
            const tabbar = document.querySelector('.van-tabbar');
            const wrapRect = wrapper ? wrapper.getBoundingClientRect() : null;
            const barRect = tabbar ? tabbar.getBoundingClientRect() : null;
            return {
                missing: false,
                cls: String(item.className || ''),
                iconAnim: iconStyle ? iconStyle.animationName : '',
                iconDuration: iconStyle ? iconStyle.animationDuration : '',
                haloAnim: haloStyle ? haloStyle.animationName : '',
                haloContent: haloStyle ? haloStyle.content : '',
                wrapperPosition: wrapper ? getComputedStyle(wrapper).position : '',
                haloCenterX: wrapRect ? Math.round(wrapRect.left + wrapRect.width / 2) : -1,
                tabbarWidth: barRect ? Math.round(barRect.width) : 0,
            };
        }"""
    )


def tile_uids(page) -> list[str]:
    return page.evaluate(
        "() => [...document.querySelectorAll('.ov-tile[data-uid]')].map((el) => el.dataset.uid)"
    )


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    local_assets = os.environ.get("LOCAL_ASSETS", "").strip() in ("1", "true", "yes")
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/cards-overview-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    notes: list[str] = []
    engine = os.environ.get("BROWSER", "chromium").strip() or "chromium"
    with sync_playwright() as pw:
        if engine not in ("chromium", "firefox", "webkit"):
            sys.exit(f"BROWSER 只支持 chromium/firefox/webkit，收到 {engine}")
        browser = getattr(pw, engine).launch()
        for width, height in viewports_from_env():
            label = f"{width}x{height}"
            context_args = {
                "viewport": {"width": width, "height": height},
                "device_scale_factor": 2,
                "locale": "zh-CN",
                "user_agent": (
                    "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) "
                    "AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 "
                    "Mobile/15E148 Safari/604.1"
                ),
            }
            if engine != "firefox":  # Firefox 不支持 is_mobile
                context_args["is_mobile"] = True
                context_args["has_touch"] = True
            context = browser.new_context(**context_args)
            context.add_cookies(
                [{
                    "name": "yxq_session",
                    "value": cookie,
                    "url": base_url,
                    "httpOnly": True,
                    "sameSite": "Lax",
                }]
            )
            # 一次性引导（更新说明/活动引导/滑动提示）会在首屏后弹层并拦截 tab 点击；
            # smoke 只验证本次功能，进入前把它们标记为已读。
            context.add_init_script(
                """try {
                    localStorage.setItem('yxq_update_notes_v1', '1');
                    localStorage.setItem('yxq_swipe_guide_v1', '1');
                    sessionStorage.setItem('yxq_activity_promo_v1', '1');
                } catch (e) {}"""
            )
            page = context.new_page()
            errors = attach_errors(page)
            public_reqs = {}

            def count_public(request):
                url = request.url
                if "/legacy/api/users/" in url and url.endswith("/public"):
                    oid = url.split("/users/")[-1].rsplit("/public", 1)[0]
                    public_reqs[oid] = public_reqs.get(oid, 0) + 1

            page.on("request", count_public)
            try:
                if local_assets:
                    # 真实加载一次：服务端下发 csrf cookie，后续写请求才不会被 403。
                    page.goto(base_url + "/", wait_until="domcontentloaded")
                    page.wait_for_timeout(800)
                    install_local_assets(context, base_url)
                page.goto(base_url + "/app", wait_until="domcontentloaded")
                page.wait_for_selector(".cards-page", timeout=20000)
                dismiss_guides(page)
                # Vant CSS 是异步加载的；等底栏图标按 22px 布局后再量光晕几何。
                page.wait_for_function(
                    """() => {
                        const w = document.querySelector('.van-tabbar-item .van-tabbar-item__icon');
                        if (!w) return false;
                        const r = w.getBoundingClientRect();
                        return r.width > 0 && r.width < 40;
                    }""",
                    timeout=8000,
                )

                # ---- 1. 牵线 tab 爱心光晕 ----
                state = glow_state(page)
                if state.get("missing"):
                    raise SmokeFailure("找不到牵线 tab")
                if "tab-heart-live" not in state["cls"]:
                    raise SmokeFailure(f"牵线 tab 缺少 tab-heart-live：{state['cls']}")
                if state["iconAnim"] != "yxqHeartBeat" or state["iconDuration"] != "2.4s":
                    raise SmokeFailure(f"爱心动画未生效：{state}")
                if state["haloAnim"] != "yxqHeartHalo" or state["haloContent"] in ("", "none"):
                    raise SmokeFailure(f"光晕伪元素未生效：{state}")
                # 几何断言：光晕必须挂在爱心 wrapper 上（position:relative），
                # 中心落在第一个 tab（宽度 1/4 内），不能跑到整条底栏中央。
                if state["wrapperPosition"] != "relative":
                    raise SmokeFailure(f"光晕包含块错误，wrapper position={state['wrapperPosition']}：{state}")
                if state["tabbarWidth"] and state["haloCenterX"] > state["tabbarWidth"] * 0.30:
                    raise SmokeFailure(f"光晕不在牵线 tab 上：centerX={state['haloCenterX']} / {state['tabbarWidth']}")
                page.screenshot(path=str(out_dir / f"glow-{label}.png"))

                # ---- 2. 切走后动画停止 ----
                click_tab(page, "消息", ".msg-page")
                state = glow_state(page)
                if "tab-heart-live" in state["cls"] or state["iconAnim"] != "none":
                    raise SmokeFailure(f"离开牵线后光晕未停止：{state}")

                # ---- 3. 首次点牵线只切 tab，不打开全览 ----
                click_tab(page, "牵线", ".cards-page")
                page.wait_for_timeout(300)
                if page.locator(".cards-overview-page").count():
                    raise SmokeFailure("从其他 tab 点牵线就打开了全览")
                state = glow_state(page)
                if "tab-heart-live" not in state["cls"] or state["iconAnim"] != "yxqHeartBeat":
                    raise SmokeFailure(f"回到牵线后光晕未恢复：{state}")

                # ---- 4. 再点一次进入全览 ----
                tap_cards_again(page)
                wait_real_page(page, ".cards-overview-page", label)
                if page.locator(".van-tabbar").count():
                    raise SmokeFailure("全览子页不应显示底栏")
                title = page.locator(".ov-title").inner_text()
                if "全览" not in title:
                    raise SmokeFailure(f"全览页头异常：{title}")
                page.wait_for_selector(".ov-tile[data-uid]", timeout=15000)
                page.wait_for_function(
                    """() => {
                        const img = document.querySelector('.ov-tile[data-uid] .ov-photo');
                        return !!img && img.complete && img.naturalWidth > 0;
                    }""",
                    timeout=15000,
                )
                page.screenshot(path=str(out_dir / f"overview-{label}.png"))

                # ---- 5. 顺序与接口一致（最新注册在前）----
                api = page.evaluate(
                    """async () => {
                        const load = async () => {
                            const resp = await fetch('/legacy/api/cards/overview?limit=60', {credentials: 'same-origin'});
                            const text = await resp.text();
                            try { return JSON.parse(text); }
                            catch (e) { return {__nonJson: true, status: resp.status, body: text.slice(0, 120)}; }
                        };
                        let data = await load();
                        if (data && data.__nonJson) {
                            await new Promise((r) => setTimeout(r, 600));
                            data = await load();
                        }
                        return data;
                    }"""
                )
                if api.get("__nonJson"):
                    raise SmokeFailure(f"overview 接口返回非 JSON：{api}")
                api_uids = [item.get("user_id") for item in (api.get("items") or [])]
                if not api_uids:
                    raise SmokeFailure("全览接口没有返回任何卡片")
                if any(not item.get("photo") for item in api.get("items") or []):
                    raise SmokeFailure("全览接口返回了没有照片的卡片")
                uids = tile_uids(page)
                if uids != api_uids[: len(uids)]:
                    raise SmokeFailure(f"DOM 顺序与接口不一致：{uids[:6]} vs {api_uids[:6]}")
                notes.append(f"{label}: API total={api.get('total')} 首屏={len(uids)} 顺序一致")

                # ---- 6. 触底加载下一页 ----
                before = len(uids)
                page.evaluate(
                    "() => { const el = document.querySelector('.cards-overview-scroll'); el.scrollTop = el.scrollHeight; }"
                )
                try:
                    page.wait_for_function(
                        "(n) => document.querySelectorAll('.ov-tile[data-uid]').length > n",
                        arg=before,
                        timeout=10000,
                    )
                    notes.append(f"{label}: 触底加载 {before} → {len(tile_uids(page))}")
                except PlaywrightTimeoutError:
                    foot = page.locator(".ov-foot").inner_text()
                    if "已经到底" not in foot:
                        raise SmokeFailure(f"触底未加载更多，底部文案：{foot}") from None
                    notes.append(f"{label}: 首屏已到底（{before} 条）")

                broken = page.evaluate(
                    """() => [...document.querySelectorAll('.ov-photo')]
                        .filter((img) => img.complete && img.naturalWidth === 0).length"""
                )
                if broken:
                    raise SmokeFailure(f"全览有 {broken} 张图片加载失败（404/损坏）")

                # ---- 7. 底部空白：子页要铺满视口（#app 底栏预留不能留白带）----
                gap = page.evaluate(
                    """() => {
                        const el = document.querySelector('.cards-overview-page');
                        const r = el.getBoundingClientRect();
                        return {
                            gap: Math.round(window.innerHeight - r.bottom),
                            pageHeight: Math.round(r.height),
                            viewport: window.innerHeight,
                        };
                    }"""
                )
                if abs(gap["gap"]) > 2:
                    raise SmokeFailure(f"全览页底部有空白带：{gap}")
                notes.append(f"{label}: 全览页铺满视口 {gap['pageHeight']}px（留白 {gap['gap']}px）")

                # ---- 8. 网格卡片多图滑动（优先找真实多图用户，用昵称筛选置顶）----
                multi_target = None
                recommended_target = None
                offset = 0
                while offset < 600 and not (multi_target and recommended_target):
                    page_data = page.evaluate(
                        """async (off) => {
                            const r = await fetch('/legacy/api/cards/overview?limit=60&offset=' + off, {credentials: 'same-origin'});
                            return await r.json();
                        }""",
                        offset,
                    )
                    for item in page_data.get("items") or []:
                        if not recommended_target and item.get("recommended"):
                            recommended_target = item
                        if not multi_target and len(item.get("photos") or []) > 1 and not item.get("liked"):
                            multi_target = item
                    offset += 60
                filter_applied = False
                if multi_target:
                    page.locator(".ov-filter").click()
                    page.wait_for_selector(".filter-title", timeout=5000)
                    page.get_by_placeholder("如：张三").fill(multi_target["nickname"])
                    # 两处筛选都支持 Enter 应用；这里走 Enter（不点按钮）。
                    page.keyboard.press("Enter")
                    page.wait_for_selector(
                        f'.ov-tile[data-uid="{multi_target["user_id"]}"]',
                        timeout=15000,
                    )
                    indep = page.evaluate(
                        """() => {
                            const p = window.yxqRootProxy;
                            return {
                                deck: p ? p.filter && p.filter.nickname : null,
                                deckActive: p ? !!p.hasActiveFilter : null,
                                ov: p ? p.overviewFilter && p.overviewFilter.nickname : null,
                                ovActive: p ? !!p.ovHasActiveFilter : null,
                            };
                        }"""
                    )
                    if indep["deck"] or indep["deckActive"]:
                        raise SmokeFailure(f"全览筛选污染了牵线筛选：{indep}")
                    if indep["ov"] != multi_target["nickname"] or not indep["ovActive"]:
                        raise SmokeFailure(f"全览筛选未写入独立 store：{indep}")
                    notes.append(f"{label}: 全览筛选独立于牵线（Enter 应用）{indep}")
                    filter_applied = True
                    notes.append(f"{label}: 昵称筛选置顶多图用户 {multi_target['nickname']}（{len(multi_target['photos'])} 图）")
                page.evaluate("() => { const el = document.querySelector('.cards-overview-scroll'); el.scrollTop = 0; }")
                page.wait_for_timeout(350)
                swipe_uid = (multi_target or {}).get("user_id") or page.evaluate(
                    """() => {
                        const tiles = [...document.querySelectorAll('.ov-tile[data-uid]')];
                        const el = tiles.find((t) => t.querySelector('.ov-photo-count'));
                        return el ? el.dataset.uid : '';
                    }"""
                )
                if not swipe_uid:
                    notes.append(f"{label}: 首屏无多图卡片，跳过多图滑动")
                else:
                    # 先把目标 tile 滚到视口内，再取手势坐标（否则鼠标坐标落在视口外）。
                    page.evaluate(
                        """(uid) => {
                            const sc = document.querySelector('.cards-overview-scroll');
                            const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                            if (!sc || !el) return;
                            const top = el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop;
                            sc.scrollTop = Math.max(0, top - 120);
                        }""",
                        swipe_uid,
                    )
                    page.wait_for_timeout(300)
                    swipe = page.evaluate(
                        """(uid) => {
                            const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                            if (!el) return null;
                            const r = el.getBoundingClientRect();
                            return {
                                uid,
                                counter: (el.querySelector('.ov-photo-count') || {}).textContent || '',
                                x: r.left + r.width * 0.7,
                                y: r.top + r.height * 0.35,
                            };
                        }""",
                        swipe_uid,
                    )
                    if not swipe:
                        raise SmokeFailure(f"找不到多图 tile：{swipe_uid}")
                    page.mouse.move(swipe["x"], swipe["y"])
                    page.mouse.down()
                    page.mouse.move(swipe["x"] - 100, swipe["y"], steps=6)
                    page.mouse.up()
                    page.wait_for_timeout(400)
                    if page.locator(".ov-sheet .swipe-card").count() and page.locator(".ov-sheet .swipe-card").first.is_visible():
                        raise SmokeFailure("网格滑动被误判为点击，弹出了卡片浮层")
                    after_counter = page.evaluate(
                        """(uid) => {
                            const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                            return el ? ((el.querySelector('.ov-photo-count') || {}).textContent || '') : '';
                        }""",
                        swipe["uid"],
                    )
                    if after_counter == swipe["counter"]:
                        raise SmokeFailure(f"网格多图滑动未生效：{swipe['counter']} → {after_counter}")
                    notes.append(f"{label}: 网格多图滑动 {swipe['counter']} → {after_counter}")
                    # 触摸路径：同一 tile 反向滑回（网格的 ovGesture* 与浮层是两套 handler）。
                    if touch_swipe(page, f'.ov-tile[data-uid="{swipe["uid"]}"]', 90):
                        page.wait_for_timeout(400)
                        touch_counter = page.evaluate(
                            """(uid) => {
                                const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                                return el ? ((el.querySelector('.ov-photo-count') || {}).textContent || '') : '';
                            }""",
                            swipe["uid"],
                        )
                        if touch_counter == after_counter:
                            raise SmokeFailure(f"网格触摸滑动未生效：{after_counter} → {touch_counter}")
                        if page.locator(".ov-sheet .swipe-card").count() and page.locator(".ov-sheet .swipe-card").first.is_visible():
                            raise SmokeFailure("网格触摸滑动被误判为点击")
                        notes.append(f"{label}: 网格触摸滑动 {after_counter} → {touch_counter}")

                if swipe_uid and swipe:
                    # PM 反馈：滑图后要能立即点卡；不能等 450ms 时间窗。
                    page.mouse.click(swipe["x"], swipe["y"])
                    page.wait_for_selector(".ov-sheet .swipe-card", timeout=15000)
                    page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                    notes.append(f"{label}: 滑图后立即点卡弹出浮层")
                    # 卡外空白区域：点击卡上方留白应收卡。
                    page.mouse.click(width // 2, 16)
                    try:
                        page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                    except PlaywrightTimeoutError:
                        raise SmokeFailure("点击卡片外空白未关闭浮层") from None
                    notes.append(f"{label}: 点卡外空白关闭浮层")
                    # 桌面鼠标纵向拖拽不应被当成点击弹出浮层。
                    page.mouse.move(swipe["x"], swipe["y"])
                    page.mouse.down()
                    page.mouse.move(swipe["x"], swipe["y"] + 100, steps=6)
                    page.mouse.up()
                    page.wait_for_timeout(350)
                    if page.locator(".ov-sheet .swipe-card").count() and page.locator(".ov-sheet .swipe-card").first.is_visible():
                        raise SmokeFailure("鼠标纵向拖拽被误判为点击，弹出了浮层")
                    notes.append(f"{label}: 鼠标纵向拖拽不误开浮层")

                # ---- 9. 卡片浮层：大图 / 多图 / 留言 / 反馈 / 点赞 / 爱心 ----
                items = api.get("items") or []
                target = multi_target
                multi = [it for it in items if len(it.get("photos") or []) > 1]
                for item in ([] if target else multi):
                    if not item.get("liked"):
                        target = item
                        break
                if target is None and multi:
                    target = multi[0]
                if target is None:
                    for item in items:
                        if not item.get("liked"):
                            target = item
                            break
                if target is None:
                    target = items[0] if items else {}
                target_uid = target.get("user_id")
                page.evaluate(
                    """(uid) => {
                        const sc = document.querySelector('.cards-overview-scroll');
                        const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                        if (!sc || !el) return;
                        const top = el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop;
                        sc.scrollTop = Math.max(0, top - 80);
                    }""",
                    target_uid,
                )
                page.wait_for_timeout(250)
                scroll_before_sheet = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                page.evaluate(
                    """(uid) => {
                        const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                        if (el) el.click();
                    }""",
                    target_uid,
                )
                page.wait_for_selector(".ov-sheet .swipe-card", timeout=15000)
                # 列表已带全量卡面；这里只等渲染，不再有 public profile 请求。
                page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                page.wait_for_timeout(300)
                oid = target.get("openid") or ""
                if oid and public_reqs.get(oid, 0) != 0:
                    raise SmokeFailure(f"浮层不应请求 public profile：{public_reqs.get(oid)} 次")
                if oid:
                    notes.append(f"{label}: 浮层直接使用列表完整资料（public 请求 0 次）")
                sheet = page.evaluate(
                    """() => {
                        const card = document.querySelector('.ov-sheet .swipe-card');
                        const track = card && card.querySelector('.card-photo-track');
                        const acts = card ? [...card.querySelectorAll('.card-actions button')] : [];
                        const tabs = card ? [...card.querySelectorAll('.card-tab')].map((el) => (el.innerText || '').trim()) : [];
                        return {
                            name: (card && card.querySelector('.card-name') ? card.querySelector('.card-name').innerText : ''),
                            actions: acts.map((el) => (el.innerText || '').trim()),
                            tabs,
                            likeVisible: !!(card && card.querySelector('.heart-btn')),
                            praiseVisible: !!(card && card.querySelector('.praise-btn')),
                            feedbackVisible: !!(card && card.querySelector('.feedback-btn')),
                            photos: card ? card.querySelectorAll('.card-photo-slide img').length : 0,
                            transform: track ? track.style.transform : '',
                            sections: card ? card.querySelectorAll('.card-info .card-section').length : 0,
                            visible: !!card && card.getBoundingClientRect().height > 0,
                        };
                    }"""
                )
                if target.get("nickname") and target["nickname"] not in sheet["name"]:
                    raise SmokeFailure(f"浮层姓名不符：{sheet['name']} vs {target['nickname']}")
                if not sheet["visible"] or sheet["sections"] <= 0:
                    raise SmokeFailure(f"浮层卡片资料未渲染：{sheet}")
                if "资料" not in sheet["tabs"] or "留言" not in sheet["tabs"]:
                    raise SmokeFailure(f"浮层缺少资料/留言 tab：{sheet['tabs']}")
                if not sheet["feedbackVisible"]:
                    raise SmokeFailure(f"浮层缺少悬浮反馈按钮：{sheet}")
                if not target.get("liked") and not sheet["likeVisible"]:
                    raise SmokeFailure(f"未喜欢对象缺少悬浮爱心：{sheet}")
                if not target.get("liked") and not sheet["praiseVisible"]:
                    raise SmokeFailure(f"未喜欢对象缺少悬浮点赞：{sheet}")
                if sheet["photos"] > 1:
                    before_transform = sheet["transform"]
                    box = page.locator(".ov-sheet .card-photo-wrap").bounding_box()
                    if box:
                        page.mouse.move(box["x"] + box["width"] * 0.75, box["y"] + box["height"] * 0.5)
                        page.mouse.down()
                        page.mouse.move(box["x"] + box["width"] * 0.25, box["y"] + box["height"] * 0.5, steps=6)
                        page.mouse.up()
                        page.wait_for_timeout(400)
                        after_transform = page.evaluate(
                            "() => { const t = document.querySelector('.ov-sheet .card-photo-track'); return t ? t.style.transform : ''; }"
                        )
                        if after_transform == before_transform:
                            raise SmokeFailure(f"浮层卡面鼠标滑动未生效：{before_transform} → {after_transform}")
                        if touch_swipe(page, ".ov-sheet .card-photo-wrap", -80):
                            page.wait_for_timeout(400)
                            touch_transform = page.evaluate(
                                "() => { const t = document.querySelector('.ov-sheet .card-photo-track'); return t ? t.style.transform : ''; }"
                            )
                            if touch_transform == after_transform:
                                raise SmokeFailure(f"浮层卡面触摸滑动未生效：{after_transform} → {touch_transform}")
                            notes.append(f"{label}: 浮层卡面触摸滑动 {after_transform} → {touch_transform}")
                        notes.append(f"{label}: 浮层卡面鼠标滑动 {before_transform} → {after_transform}")
                notes.append(f"{label}: 浮层 tab {sheet['tabs']}、悬浮动作 {sheet['actions']}、资料分区 {sheet['sections']}")

                # 留言 tab：打开后输入（不真正发送，避免污染 staging 数据），再切回资料
                page.locator(".ov-sheet .card-tab").filter(has_text="留言").first.click()
                page.wait_for_selector(".ov-sheet .msg-input", timeout=8000)
                page.fill(".ov-sheet .msg-input", "smoke 测试留言")
                if page.locator(".ov-sheet .msg-send").is_disabled():
                    raise SmokeFailure("留言输入后发送按钮仍禁用")
                page.fill(".ov-sheet .msg-input", "")
                # 留言列表（.msg-list）滚动后下滑不应收卡（滚动容器不是 .card-info）。
                msg_scrollable = page.evaluate(
                    """() => {
                        const el = document.querySelector('.ov-sheet .msg-list');
                        return !!el && el.scrollHeight > el.clientHeight + 4;
                    }"""
                )
                if msg_scrollable:
                    page.evaluate("() => { const el = document.querySelector('.ov-sheet .msg-list'); el.scrollTop = 120; }")
                    page.wait_for_timeout(200)
                    touch_drag(page, ".ov-sheet .msg-list", 150)
                    page.wait_for_timeout(400)
                    if not page.locator(".ov-sheet .swipe-card").first.is_visible():
                        raise SmokeFailure("留言列表滚动中下滑被误判为收卡")
                    page.evaluate("() => { const el = document.querySelector('.ov-sheet .msg-list'); el.scrollTop = 0; }")
                    page.wait_for_timeout(150)
                    notes.append(f"{label}: 留言列表滚动中下滑不误收")
                # 作者头像 → 资料页 → 返回：浮层与全览页应恢复（不是关掉浮层停在资料页）。
                if page.locator(".ov-sheet .msg-item .msg-avatar-sm").count():
                    page.locator(".ov-sheet .msg-item .msg-avatar-sm").first.click()
                    page.wait_for_selector(".user-detail-card", timeout=15000)
                    page.wait_for_timeout(400)
                    page.locator(".back-nav").first.click()
                    page.wait_for_selector(".cards-overview-page", timeout=10000)
                    page.wait_for_selector(".ov-sheet .swipe-card", timeout=10000)
                    if not page.locator(".ov-sheet .swipe-card").first.is_visible():
                        raise SmokeFailure("从作者资料页返回后浮层未恢复")
                    notes.append(f"{label}: 作者资料页返回后浮层恢复")
                    # 返回后页面仍有过渡；关闭并重新打开（资料随身携带，重开即完整、
                    # 默认资料 tab），避免在过渡窗口里切 tab 的时序竞态。
                    page.wait_for_timeout(500)
                    page.locator(".ov-card-close").click()
                    try:
                        page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                    except PlaywrightTimeoutError:
                        pass
                    page.evaluate(
                        """(uid) => {
                            const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                            if (el) el.click();
                        }""",
                        target["user_id"],
                    )
                    page.wait_for_selector(".ov-sheet .swipe-card", timeout=10000)
                else:
                    notes.append(f"{label}: 该卡暂无留言，跳过作者资料页往返")
                page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                page.wait_for_timeout(300)
                # 打开时会把当前卡滚到视口中心；关闭保位以打开后的位置为基线。
                scroll_before_sheet = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")

                # 悬浮反馈：打开后取消（不提交）
                page.locator(".ov-sheet .feedback-btn").first.click()
                page.wait_for_selector(".van-dialog", timeout=8000)
                if not page.get_by_text("反馈该用户", exact=True).count():
                    raise SmokeFailure("反馈弹窗未出现")
                page.locator(".van-dialog").last.get_by_text("取消", exact=True).click()
                page.wait_for_timeout(400)

                # 关闭浮层：列表与滚动位置保持不变
                page.locator(".ov-card-close").click()
                try:
                    page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                except PlaywrightTimeoutError:
                    pass
                page.wait_for_timeout(300)
                scroll_after_sheet = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                if abs(scroll_after_sheet - scroll_before_sheet) > 3:
                    raise SmokeFailure(f"关闭浮层后滚动位置变化：{scroll_before_sheet} → {scroll_after_sheet}")
                if len(tile_uids(page)) < 1:
                    raise SmokeFailure("关闭浮层后列表丢失")
                notes.append(f"{label}: 关闭浮层后列表与滚动位置保持（{scroll_after_sheet}px）")
                page.screenshot(path=str(out_dir / f"sheet-{label}.png"))

                # 下滑收卡：内容滚动中不误收，滑至顶后下滑才收。
                page.evaluate(
                    """(uid) => {
                        const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                        if (el) el.click();
                    }""",
                    target_uid,
                )
                page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                page.wait_for_timeout(300)
                scrollable = page.evaluate(
                    """() => {
                        const el = document.querySelector('.ov-sheet .card-info');
                        return !!el && el.scrollHeight > el.clientHeight + 4;
                    }"""
                )
                if scrollable:
                    page.evaluate("() => { const el = document.querySelector('.ov-sheet .card-info'); el.scrollTop = 200; }")
                    page.wait_for_timeout(200)
                    touch_drag(page, ".ov-sheet .card-info", 150)
                    page.wait_for_timeout(400)
                    if not page.locator(".ov-sheet .swipe-card").first.is_visible():
                        raise SmokeFailure("资料滚动中下滑被误判为收卡")
                    page.evaluate("() => { const el = document.querySelector('.ov-sheet .card-info'); el.scrollTop = 0; }")
                    page.wait_for_timeout(200)
                    notes.append(f"{label}: 内容滚动中下滑不误收")
                touch_drag(page, ".ov-sheet .card-info", 160)
                page.wait_for_timeout(500)
                try:
                    page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                except PlaywrightTimeoutError:
                    raise SmokeFailure("滑至顶后下滑未收卡") from None
                notes.append(f"{label}: 滑至顶后下滑收卡")

                # 内容滑到最底部后继续上滑，也要能收卡。
                page.evaluate(
                    """(uid) => {
                        const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                        if (el) el.click();
                    }""",
                    target_uid,
                )
                page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                page.evaluate("() => { const el = document.querySelector('.ov-sheet .card-info'); if (el) el.scrollTop = el.scrollHeight; }")
                page.wait_for_timeout(250)
                touch_drag(page, ".ov-sheet .card-info", -160)
                try:
                    page.wait_for_function(
                        "() => { const el = document.querySelector('.ov-sheet'); return !!el && el.classList.contains('ov-sheet-up'); }",
                        timeout=2000,
                    )
                except PlaywrightTimeoutError:
                    raise SmokeFailure("上滑收卡时 popup 未使用向上离场") from None
                try:
                    page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                except PlaywrightTimeoutError:
                    raise SmokeFailure("滑至底后上滑未收卡") from None
                notes.append(f"{label}: 滑至底后上滑收卡（向上离场）")

                # 筛选取消/放弃不能写回牵线的 filter store（用户强调两处互不影响）。
                page.evaluate("() => { const p = window.yxqRootProxy; if (p && p.filter) p.filter.nickname = 'DECK-KEEP'; }")
                page.locator(".ov-filter").click()
                page.wait_for_selector(".filter-title", timeout=5000)
                page.get_by_placeholder("如：张三").fill("X-CANCEL")
                page.locator(".van-popup").filter(has=page.locator(".filter-title")).first.get_by_text(
                    "取消", exact=True
                ).click()
                page.get_by_text("放弃", exact=True).click()
                page.wait_for_timeout(300)
                cancel_state = page.evaluate(
                    """() => {
                        const p = window.yxqRootProxy;
                        return { deck: p ? (p.filter && p.filter.nickname) : null, ov: p ? (p.overviewFilter && p.overviewFilter.nickname) : null };
                    }"""
                )
                if cancel_state["deck"] != "DECK-KEEP":
                    raise SmokeFailure(f"全览筛选取消写回了牵线筛选：{cancel_state}")
                if cancel_state["ov"] != (multi_target or {}).get("nickname"):
                    raise SmokeFailure(f"全览筛选取消未恢复自身基线：{cancel_state}")
                page.evaluate("() => { const p = window.yxqRootProxy; if (p && p.filter) p.filter.nickname = ''; }")
                notes.append(f"{label}: 全览筛选取消/放弃不影响牵线筛选 {cancel_state}")

                if filter_applied:
                    clear_overview_filter(page)
                    # 推荐徽标：列表 item 自带 recommended，打开浮层后仍应显示「推荐」。
                    if recommended_target:
                        page.locator(".ov-filter").click()
                        page.wait_for_selector(".filter-title", timeout=5000)
                        page.get_by_placeholder("如：张三").fill(recommended_target["nickname"])
                        page.get_by_text("应用筛选", exact=True).click()
                        page.wait_for_selector(
                            f'.ov-tile[data-uid="{recommended_target["user_id"]}"]', timeout=15000
                        )
                        page.evaluate(
                            """(uid) => {
                                const el = document.querySelector('.ov-tile[data-uid="' + uid + '"]');
                                if (el) el.click();
                            }""",
                            recommended_target["user_id"],
                        )
                        page.wait_for_selector(".ov-sheet .swipe-card", timeout=15000)
                        page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                        page.wait_for_timeout(300)
                        overlay_text = page.evaluate(
                            "() => (document.querySelector('.ov-sheet .card-photo-overlay') || {}).innerText || ''"
                        )
                        if "推荐" not in overlay_text:
                            raise SmokeFailure(f"推荐用户浮层丢失推荐徽标：{overlay_text!r}")
                        notes.append(f"{label}: 浮层保留推荐徽标（{recommended_target['nickname']}）")
                        page.locator(".ov-card-close").click()
                        try:
                            page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                        except PlaywrightTimeoutError:
                            pass
                        clear_overview_filter(page)
                    # 长列表上验证「打开/关闭浮层不改变列表滚动位置」。
                    # 清筛选的 reset 会异步滚顶，先落 0 再设置 420，避免测成 0→0 假绿。
                    page.evaluate("() => { const el = document.querySelector('.cards-overview-scroll'); el.scrollTop = 0; }")
                    page.wait_for_timeout(250)
                    page.evaluate("() => { const el = document.querySelector('.cards-overview-scroll'); el.scrollTop = 420; }")
                    page.wait_for_timeout(350)
                    scroll_before = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                    page.evaluate(
                        """() => {
                            const sc = document.querySelector('.cards-overview-scroll');
                            const box = sc.getBoundingClientRect();
                            const tiles = [...document.querySelectorAll('.ov-tile[data-uid]')];
                            const target = tiles.find((el) => {
                                const r = el.getBoundingClientRect();
                                return r.top >= box.top + 4 && r.bottom <= box.bottom - 4;
                            }) || tiles[0];
                            if (target) target.click();
                        }"""
                    )
                    page.wait_for_selector(".ov-sheet .swipe-card", timeout=10000)
                    page.wait_for_timeout(600)
                    # 打开时会把当前卡滚到视口中心（PM 要求）；以打开后的位置为基线验证关闭保位。
                    scroll_after_open = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                    # 先只开/关一次，验证长列表滚动保位（不含切卡自动定位）。
                    page.locator(".ov-card-close").click()
                    try:
                        page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                    except PlaywrightTimeoutError:
                        pass
                    page.wait_for_timeout(300)
                    scroll_after_plain = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                    if abs(scroll_after_plain - scroll_after_open) > 3:
                        raise SmokeFailure(f"长列表关闭浮层后滚动位置变化：{scroll_after_open} → {scroll_after_plain}")
                    notes.append(f"{label}: 长列表关闭浮层保位 {scroll_after_open}px → {scroll_after_plain}px（打开时自动定位）")
                    # 再次打开做左右切卡 + 网格自动定位。
                    page.evaluate(
                        """() => {
                            const sc = document.querySelector('.cards-overview-scroll');
                            const box = sc.getBoundingClientRect();
                            const tiles = [...document.querySelectorAll('.ov-tile[data-uid]')];
                            const target = tiles.find((el) => {
                                const r = el.getBoundingClientRect();
                                return r.top >= box.top + 4 && r.bottom <= box.bottom - 4;
                            }) || tiles[0];
                            if (target) target.click();
                        }"""
                    )
                    page.wait_for_selector(".ov-sheet .swipe-card", timeout=10000)
                    page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                    page.wait_for_timeout(400)
                    # 浮层卡左右滑切卡 + 全览网格自动滚到当前卡。
                    before_switch = page.evaluate(
                        """() => {
                            const p = window.yxqRootProxy;
                            const i = p.ovCardIndex;
                            return { i, name: p.overviewSheet.nickname, uid: p.overviewItems[i] && p.overviewItems[i].user_id };
                        }"""
                    )
                    box = page.locator(".ov-sheet .card-main").bounding_box()
                    if box:
                        # 把网格滚到离目标最远的一端，切卡时的自动定位必须平滑滚动（不能瞬跳）。
                        page.evaluate("() => { const p = window.yxqRootProxy; if (p) p.overviewHasMore = false; }")
                        scroll_info = page.evaluate(
                            """() => {
                                const p = window.yxqRootProxy;
                                const idx = p.ovCardIndex + 1;
                                const it = p.overviewItems[idx];
                                const sc = document.querySelector('.cards-overview-scroll');
                                if (!it || !sc) return null;
                                const el = document.querySelector('.ov-tile[data-uid="' + it.user_id + '"]');
                                if (!el) return null;
                                const top = el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop;
                                const target = Math.max(0, top - (sc.clientHeight - el.clientHeight) / 2);
                                const max = Math.max(0, sc.scrollHeight - sc.clientHeight);
                                const start = target > max / 2 ? 0 : max;
                                sc.scrollTop = start;
                                return { start, target, max };
                            }"""
                        )
                        page.wait_for_timeout(150)
                        page.mouse.move(box["x"] + box["width"] * 0.78, box["y"] + box["height"] * 0.4)
                        page.mouse.down()
                        page.mouse.move(box["x"] + box["width"] * 0.2, box["y"] + box["height"] * 0.4, steps=8)
                        page.mouse.up()
                        page.wait_for_timeout(150)
                        scroll_mid = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                        try:
                            page.wait_for_function(
                                "() => { const el = document.querySelector('.ov-sheet .swipe-card'); return !!el && el.classList.contains('slide-in-right'); }",
                                timeout=2000,
                            )
                        except PlaywrightTimeoutError:
                            raise SmokeFailure("浮层卡左滑没有 slide-in-right 过场") from None
                        page.wait_for_timeout(700)
                        scroll_final = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                        if scroll_info and abs(scroll_final - scroll_info["start"]) >= 40:
                            lo, hi = sorted((scroll_info["start"], scroll_final))
                            if not (lo < scroll_mid < hi):
                                raise SmokeFailure(f"网格自动定位不是平滑滚动（mid={scroll_mid} final={scroll_final} start={scroll_info['start']}）")
                            notes.append(f"{label}: 网格自动定位平滑滚动 {scroll_info['start']}px → {scroll_mid}px → {scroll_final}px")
                        else:
                            notes.append(f"{label}: 切卡目标距离过小，跳过平滑滚动采样")
                        after_switch = page.evaluate(
                            """() => {
                                const p = window.yxqRootProxy;
                                const i = p.ovCardIndex;
                                const cur = p.overviewItems[i];
                                const sc = document.querySelector('.cards-overview-scroll');
                                const tile = cur ? document.querySelector('.ov-tile[data-uid="' + cur.user_id + '"]') : null;
                                const visible = !!(sc && tile) && tile.getBoundingClientRect().top < sc.getBoundingClientRect().bottom && tile.getBoundingClientRect().bottom > sc.getBoundingClientRect().top;
                                return { i, name: p.overviewSheet.nickname, visible };
                            }"""
                        )
                        if after_switch["i"] != before_switch["i"] + 1:
                            raise SmokeFailure(f"浮层卡左滑未切到下一张：{before_switch} → {after_switch}")
                        if after_switch["name"] == before_switch["name"]:
                            raise SmokeFailure(f"浮层卡左滑姓名未变：{before_switch} → {after_switch}")
                        if not after_switch["visible"]:
                            raise SmokeFailure(f"切卡后全览未自动滚动到当前卡：{after_switch}")
                        notes.append(f"{label}: 浮层卡左滑切卡并自动滚动到当前卡 {before_switch['name']} → {after_switch['name']}")
                        # 右滑回上一张
                        page.mouse.move(box["x"] + box["width"] * 0.2, box["y"] + box["height"] * 0.4)
                        page.mouse.down()
                        page.mouse.move(box["x"] + box["width"] * 0.78, box["y"] + box["height"] * 0.4, steps=8)
                        page.mouse.up()
                        try:
                            page.wait_for_function(
                                "() => { const el = document.querySelector('.ov-sheet .swipe-card'); return !!el && el.classList.contains('slide-in-left'); }",
                                timeout=2000,
                            )
                        except PlaywrightTimeoutError:
                            raise SmokeFailure("浮层卡右滑没有 slide-in-left 过场") from None
                        page.wait_for_timeout(700)
                        back_switch = page.evaluate(
                            "() => { const p = window.yxqRootProxy; return { i: p.ovCardIndex, name: p.overviewSheet.nickname }; }"
                        )
                        if back_switch["i"] != before_switch["i"]:
                            raise SmokeFailure(f"浮层卡右滑未回到上一张：{after_switch} → {back_switch}")
                        notes.append(f"{label}: 浮层卡右滑回上一张 {after_switch['name']} → {back_switch['name']}")
                    page.locator(".ov-card-close").click()
                    try:
                        page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                    except PlaywrightTimeoutError:
                        pass
                    page.wait_for_timeout(300)
                    if len(tile_uids(page)) < 1:
                        raise SmokeFailure("切卡后关闭浮层列表丢失")
                    notes.append(f"{label}: 切卡后关闭浮层列表仍在")

                # ---- 10. 筛选：应用无结果 → 清除 ----
                page.locator(".ov-filter").click()
                page.wait_for_selector(".filter-title", timeout=5000)
                page.get_by_placeholder("如：张三").fill("zzzz-no-such-user")
                page.get_by_text("应用筛选", exact=True).click()
                page.wait_for_function(
                    "() => (document.querySelector('.ov-state-text') || {}).innerText?.includes('没有符合筛选条件的人')",
                    timeout=10000,
                )
                if not page.locator(".ov-filter.active").count():
                    raise SmokeFailure("应用筛选后筛选按钮未高亮")
                if page.locator(".ov-tile[data-uid]").count():
                    raise SmokeFailure("无结果筛选仍显示卡片")
                if page.locator(".ov-filter-note").count():
                    raise SmokeFailure("全览页头不应再有「已按牵线筛选展示」提示行")
                page.locator(".ov-state").get_by_text("清除筛选", exact=True).click()
                page.wait_for_function(
                    "() => document.querySelectorAll('.ov-tile[data-uid]').length > 0",
                    timeout=15000,
                )
                if page.locator(".ov-filter.active").count():
                    raise SmokeFailure("清除筛选后按钮仍高亮")
                notes.append(f"{label}: 筛选无结果 → 清除筛选恢复列表")

                # ---- 10.5 人脸裁切：与牵线页同一套 computeFaceCrop（U-0079 有已检测人脸）----
                apply_overview_filter(page, "如：0003", "0079")
                page.wait_for_selector('.ov-tile[data-uid="U-0079"]', timeout=15000)
                page.evaluate("() => { const el = document.querySelector('.ov-tile[data-uid=\"U-0079\"]'); if (el) el.click(); }")
                page.wait_for_selector(".ov-sheet .card-info .card-section", timeout=15000)
                page.wait_for_function(
                    """() => {
                        const img = document.querySelector('.ov-sheet .card-photo-base');
                        return !!img && img.complete && img.naturalWidth > 0;
                    }""",
                    timeout=15000,
                )
                page.wait_for_timeout(400)
                face_pos = page.evaluate(
                    """() => {
                        const imgs = [...document.querySelectorAll('.ov-sheet .card-photo-base')];
                        return imgs.slice(0, 2).map((img) => img.style.objectPosition || '');
                    }"""
                )
                if not any(face_pos):
                    raise SmokeFailure(f"U-0079 浮层卡面未应用人脸 objectPosition：{face_pos}")
                notes.append(f"{label}: 浮层人脸裁切生效（U-0079 objectPosition={face_pos}）")
                page.locator(".ov-card-close").click()
                try:
                    page.locator(".ov-sheet").first.wait_for(state="hidden", timeout=8000)
                except PlaywrightTimeoutError:
                    pass
                clear_overview_filter(page)

                # ---- 8. 全览到顶继续下拉 / 到底继续上滑 → 回牵线 ----
                page.evaluate("() => { const el = document.querySelector('.cards-overview-scroll'); if (el) el.scrollTop = 0; }")
                page.wait_for_timeout(200)
                touch_drag_start(page, ".cards-overview-scroll", 60)
                page.wait_for_timeout(50)
                early = page.evaluate(
                    """() => {
                        const el = document.querySelector('.tab-main > .cards-overview-page');
                        return el ? el.style.transform : '';
                    }"""
                )
                ey, _ = parse_motion(early)
                if abs(ey) < 5:
                    raise SmokeFailure(f"第一次拖拽后全览页未跟手（被初始状态覆盖）：{early}")
                touch_drag_move(page, 100)
                page.wait_for_timeout(50)
                peek = page.evaluate(
                    """() => ({
                        cards: document.querySelectorAll('.ov-peek-clip--back .cards-page').length,
                        ov: document.querySelectorAll('.ov-peek-clip--back .cards-overview-page').length,
                    })"""
                )
                if peek["cards"] != 1 or peek["ov"] != 0:
                    raise SmokeFailure(f"返回牵线的垫层页面错误：{peek}")
                motion = page.evaluate(
                    """() => {
                        const page = document.querySelector('.ov-peek-clip--back .cards-page');
                        const bar = document.querySelector('.ov-peek-clip--back .van-tabbar');
                        return {page: page ? page.style.transform : '', bar: bar ? bar.style.transform : ''};
                    }"""
                )
                py, ps = parse_motion(motion["page"])
                by, _ = parse_motion(motion["bar"])
                if abs(py) < 5 or ps >= 0.999 or abs(by) < 5:
                    raise SmokeFailure(f"返回时牵线页/底栏未真正跟手：{motion}")
                touch_drag_finish(page, ".cards-overview-scroll", 120)
                wait_real_page(page, ".cards-page", label)
                assert_no_card_enter_anim(page, label)
                assert_scrollbar_theme(page, label, engine == "chromium")
                if page.locator(".tab-main > .cards-overview-page").count():
                    raise SmokeFailure("全览到顶下拉未回到牵线")
                notes.append(f"{label}: 全览到顶继续下拉回牵线")
                # 页内切卡动画仍保留；320ms 后方向类清掉，避免下次回到牵线重播。
                page.keyboard.press("ArrowRight")
                page.wait_for_timeout(80)
                inpage = page.evaluate(
                    """() => {
                        const el = document.querySelector('.tab-main > .cards-page .swipe-card');
                        return el ? {cls: el.className, anim: getComputedStyle(el).animationName} : null;
                    }"""
                )
                if not inpage or "slide-in-" not in inpage["cls"]:
                    raise SmokeFailure(f"页内切卡动画丢失：{inpage}")
                page.wait_for_timeout(400)
                assert_no_card_enter_anim(page, label + " 切卡后")
                notes.append(f"{label}: 页内切卡动画保留，回到牵线不重播")

                # 重新进入全览，模拟已加载到底后上滑回牵线。
                page.locator(".van-tabbar-item").filter(has_text="牵线").first.click()
                wait_real_page(page, ".cards-overview-page", label)
                page.wait_for_selector(".ov-tile[data-uid]", timeout=15000)
                page.evaluate("() => { const p = window.yxqRootProxy; if (p) p.overviewHasMore = false; }")
                page.evaluate("() => { const el = document.querySelector('.cards-overview-scroll'); if (el) el.scrollTop = el.scrollHeight; }")
                page.wait_for_timeout(300)
                touch_drag(page, ".cards-overview-scroll", -120)
                wait_real_page(page, ".cards-page", label)
                if page.locator(".tab-main > .cards-overview-page").count():
                    raise SmokeFailure("全览到底上滑未回到牵线")
                notes.append(f"{label}: 全览到底继续上滑回牵线")
                page.screenshot(path=str(out_dir / f"back-{label}.png"))

                # ---- 10.8 牵线页纵向滑动（上/下）进入全览 ----
                page.wait_for_selector(".swipe-card", timeout=10000)
                touch_drag_start(page, ".swipe-card", 70)
                page.wait_for_timeout(50)
                early = page.evaluate(
                    """() => {
                        const el = document.querySelector('.tab-main > .cards-page');
                        return el ? el.style.transform : '';
                    }"""
                )
                ey, _ = parse_motion(early)
                if abs(ey) < 5:
                    raise SmokeFailure(f"第一次拖拽后牵线页未跟手（被初始状态覆盖）：{early}")
                touch_drag_move(page, 110)
                page.wait_for_timeout(50)
                peek = page.evaluate(
                    """() => ({
                        ov: document.querySelectorAll('.ov-peek-clip:not(.ov-peek-clip--back) .cards-overview-page').length,
                        cards: document.querySelectorAll('.ov-peek-clip:not(.ov-peek-clip--back) .cards-page').length,
                    })"""
                )
                if peek["ov"] != 1 or peek["cards"] != 0:
                    raise SmokeFailure(f"进入全览的预览页错误：{peek}")
                motion = page.evaluate(
                    """() => {
                        const page = document.querySelector('.tab-main > .cards-page');
                        const bar = document.querySelector('.tab-main ~ .van-tabbar');
                        return {page: page ? page.style.transform : '', bar: bar ? bar.style.transform : ''};
                    }"""
                )
                py, ps = parse_motion(motion["page"])
                by, _ = parse_motion(motion["bar"])
                if abs(py) < 5 or ps >= 0.999 or abs(by) < 5:
                    raise SmokeFailure(f"进入时牵线页/底栏未真正跟手：{motion}")
                touch_drag_finish(page, ".swipe-card", 140)
                wait_real_page(page, ".cards-overview-page", label)
                if not page.locator(".tab-main > .cards-overview-page .ov-title").count():
                    raise SmokeFailure("牵线页下滑未进入全览")
                notes.append(f"{label}: 牵线页下滑进入全览")
                page.locator(".ov-back").click()
                wait_real_page(page, ".cards-page", label)
                assert_no_card_enter_anim(page, label)
                touch_drag(page, ".swipe-card", -140)
                wait_real_page(page, ".cards-overview-page", label)
                notes.append(f"{label}: 牵线页上滑进入全览")
                page.locator(".ov-back").click()
                wait_real_page(page, ".cards-page", label)

                # ---- 11. 回归：牌堆「反馈」仍可打开（openFeedbackDialog 改签名）----
                # 牌堆首位通常是本人卡（没有反馈按钮），先切到下一张。
                page.keyboard.press("ArrowRight")
                page.wait_for_timeout(600)
                feedback = page.locator(".feedback-btn")
                if not feedback.count():
                    raise SmokeFailure("牌堆卡片缺少反馈按钮")
                feedback.first.click()
                page.wait_for_selector(".van-dialog", timeout=8000)
                if not page.get_by_text("反馈该用户", exact=True).count():
                    raise SmokeFailure("牌堆反馈弹窗未出现")
                page.locator(".van-dialog").last.get_by_text("取消", exact=True).click()
                page.wait_for_timeout(300)
                notes.append(f"{label}: 牌堆反馈按钮回归 PASS")

                if errors:
                    raise SmokeFailure("出现 console error / pageerror：" + "；".join(errors[:5]))
            except Exception as exc:  # noqa: BLE001 - 单视口失败继续另一档
                diag = None
                try:
                    diag = page.evaluate(
                        """() => {
                            const p = window.yxqRootProxy || {};
                            const s = p.overviewSheet;
                            return {
                                page: p.page,
                                tab: p.ovCardTab,
                                hasSheet: !!s,
                                sections: s && s.display_fields ? (s.display_fields.sections || []).length : null,
                                domSections: document.querySelectorAll('.ov-sheet .card-info .card-section').length,
                                msgInput: !!document.querySelector('.ov-sheet .msg-input'),
                                popup: (() => { const el = document.querySelector('.ov-sheet'); return el ? getComputedStyle(el).display : null; })(),
                            };
                        }"""
                    )
                except Exception:
                    diag = None
                failures.append(f"{label}: {exc} diag={diag} | url={page.url} | errors={errors[:2]}")
                try:
                    page.screenshot(path=str(out_dir / f"FAIL-{label}.png"))
                except Exception:  # pragma: no cover
                    pass
            finally:
                context.close()
        # 滚动条主题只在 hover/fine 命中时检查；主流程两个视口都开了 touch，
        # 单独用非 touch 的桌面 context 验证一次，避免断言被静默跳过。
        if engine == "chromium":
            desktop = None
            try:
                desktop = browser.new_context(
                    viewport={"width": 1280, "height": 900},
                    device_scale_factor=1,
                    locale="zh-CN",
                    is_mobile=False,
                    has_touch=False,
                    user_agent=(
                        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
                        "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
                    ),
                )
                desktop.add_cookies(
                    [{
                        "name": "yxq_session",
                        "value": cookie,
                        "url": base_url,
                        "httpOnly": True,
                        "sameSite": "Lax",
                    }]
                )
                desktop.add_init_script(
                    """try {
                        localStorage.setItem('yxq_update_notes_v1', '1');
                        localStorage.setItem('yxq_swipe_guide_v1', '1');
                        sessionStorage.setItem('yxq_activity_promo_v1', '1');
                    } catch (e) {}"""
                )
                if local_assets:
                    install_local_assets(desktop, base_url)
                dp = desktop.new_page()
                dp.goto(base_url + "/app", wait_until="domcontentloaded", timeout=30000)
                dp.wait_for_selector(
                    ".tab-main > .cards-page .card-info, .tab-main > .cards-page .msg-list",
                    timeout=20000,
                )
                if not assert_scrollbar_theme(dp, "1280x900 desktop"):
                    raise SmokeFailure("1280x900 desktop: 滚动条断言未执行（hover/fine 未命中）")
                notes.append("1280x900 desktop: 滚动条明暗切换 PASS")
            except Exception as exc:  # noqa: BLE001 - 单列失败不掩盖主流程结果
                failures.append(f"1280x900 desktop 滚动条: {exc}")
            finally:
                if desktop is not None:
                    desktop.close()
        browser.close()

    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print("PASS 牵线爱心光晕 + 全览模式（排序 / 分页 / 资料页往返 / 返回）")
    for note in notes:
        print(" note:", note)
    print(f"browser: {engine}, local_assets={local_assets}")
    print(f"screenshots: {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
