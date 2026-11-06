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
        for width, height in VIEWPORTS:
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
            try:
                if local_assets:
                    # 真实加载一次：服务端下发 csrf cookie，后续写请求才不会被 403。
                    page.goto(base_url + "/", wait_until="domcontentloaded")
                    page.wait_for_timeout(800)
                    install_local_assets(context, base_url)
                page.goto(base_url + "/app", wait_until="domcontentloaded")
                page.wait_for_selector(".cards-page", timeout=20000)
                dismiss_guides(page)

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
                page.wait_for_selector(".cards-overview-page", timeout=10000)
                if page.locator(".van-tabbar").count():
                    raise SmokeFailure("全览子页不应显示底栏")
                title = page.locator(".ov-title").inner_text()
                if "全览" not in title:
                    raise SmokeFailure(f"全览页头异常：{title}")
                page.wait_for_selector(".ov-tile[data-uid]", timeout=15000)
                page.wait_for_function(
                    """() => {
                        const imgs = [...document.querySelectorAll('.ov-photo')].slice(0, 2);
                        return imgs.length > 0 && imgs.every((img) => img.complete && img.naturalWidth > 0);
                    }""",
                    timeout=15000,
                )
                page.screenshot(path=str(out_dir / f"overview-{label}.png"))

                # ---- 5. 顺序与接口一致（最新注册在前）----
                api = page.evaluate(
                    """async () => {
                        const resp = await fetch('/legacy/api/cards/overview?limit=60', {credentials: 'same-origin'});
                        return await resp.json();
                    }"""
                )
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

                # ---- 7. 进资料页 → 返回保留列表与滚动位置 ----
                page.evaluate(
                    "() => { const el = document.querySelector('.cards-overview-scroll'); el.scrollTop = 420; }"
                )
                page.wait_for_timeout(350)
                scrolled = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                kept = len(tile_uids(page))
                # 用 JS 点当前视口内完整可见的 tile：Playwright 的 click 会把 tile
                # scrollIntoView（可能把列表拉回顶部），导致滚动位置断言失真。
                page.evaluate(
                    """() => {
                        const sc = document.querySelector('.cards-overview-scroll');
                        const box = sc.getBoundingClientRect();
                        const tiles = [...document.querySelectorAll('.ov-tile[data-uid]')];
                        const target = tiles.find((el) => {
                            const r = el.getBoundingClientRect();
                            return r.top >= box.top + 4 && r.bottom <= box.bottom - 4;
                        }) || tiles[0];
                        target.click();
                    }"""
                )
                page.wait_for_selector(".user-detail-card, .back-nav", timeout=15000)
                page.wait_for_timeout(600)
                page.locator(".back-nav").first.click()
                page.wait_for_selector(".cards-overview-page", timeout=10000)
                page.wait_for_timeout(800)
                restored = page.evaluate("() => document.querySelector('.cards-overview-scroll').scrollTop")
                if len(tile_uids(page)) < min(kept, 3):
                    raise SmokeFailure(f"返回后全览列表丢失：{kept} → {len(tile_uids(page))}")
                if scrolled > 100 and restored < min(scrolled, 120):
                    diag = page.evaluate(
                        """() => {
                            const el = document.querySelector('.cards-overview-scroll');
                            return {
                                top: el ? el.scrollTop : null,
                                scrollHeight: el ? el.scrollHeight : null,
                                clientHeight: el ? el.clientHeight : null,
                                items: document.querySelectorAll('.ov-tile[data-uid]').length,
                                sub: (document.querySelector('.ov-sub') || {}).innerText || '',
                                hist: window.__YXQ_HIST_DEBUG__ ? window.__YXQ_HIST_DEBUG__() : null,
                            };
                        }"""
                    )
                    raise SmokeFailure(f"返回后滚动位置未恢复：{scrolled} → {restored} diag={diag}")
                notes.append(f"{label}: 返回资料页保留 {len(tile_uids(page))} 条，scrollTop={restored}")

                # ---- 8. 返回牵线 ----
                page.locator(".ov-back").click()
                page.wait_for_selector(".cards-page", timeout=10000)
                if page.locator(".cards-overview-page").count():
                    raise SmokeFailure("返回后全览没有关闭")
                page.screenshot(path=str(out_dir / f"back-{label}.png"))

                if errors:
                    raise SmokeFailure("出现 console error / pageerror：" + "；".join(errors[:5]))
            except Exception as exc:  # noqa: BLE001 - 单视口失败继续另一档
                failures.append(f"{label}: {exc}")
                try:
                    page.screenshot(path=str(out_dir / f"FAIL-{label}.png"))
                except Exception:  # pragma: no cover
                    pass
            finally:
                context.close()
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
