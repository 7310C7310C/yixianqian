#!/usr/bin/env python3
"""四 Tab / 子页滚动隔离回归（Playwright）。

背景：用户端主内容共用一个文档滚动容器，`<Transition mode="out-in">` 切 tab 时
浏览器沿用上一个 tab 的 `window.scrollY`——长页切短页被夹到底部、短页切长页整页
下移，表现为「活动页下滑，其他 tab 也被下滑」。本 smoke 固定以下行为：

1. 切换 tab 不复用上一个 tab 的滚动位置：新 tab 落在自己记忆的位置（首次为顶部）；
2. 回到之前滚过的 tab，位置还原（活动列表回到原处）；
3. 子页（活动详情 / 我的活动等）一律从顶部进入，返回列表时还原列表位置；
4. 全程底栏 `.van-tabbar` 尺寸与位置不变（不闪、不被顶走）；
5. 活动详情的返回栏 `.back-nav` 贴顶（sticky 正常）；
6. 弹层（意见反馈）与管理台 iframe overlay 打开时锁住外层滚动，
   关闭后回到打开前的位置；
7. 无 console error / pageerror。

需要已部署的 staging 与一个可登录账号的原始 session cookie；不放进 make ci，
手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    SHOT_DIR=/tmp/opencode/tab-scroll-smoke \
    python3 tools/browser/tab_scroll_smoke.py

部署前验证本地改动（用本地 index.html 顶替 staging 的 SPA 外壳，接口仍走 staging）：

    INDEX_HTML=web/static/legacy/index.html \
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<...> \
    python3 tools/browser/tab_scroll_smoke.py

默认 chromium（headless shell）；可用 BROWSER=firefox / webkit 换引擎（本地需已安装）。
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

sys.path.insert(0, str(Path(__file__).resolve().parent))

import local_assets  # noqa: E402

VIEWPORT = (390, 844)
TABS = {"cards": "牵线", "likes": "消息", "home": "活动", "profile": "我的"}
TAB_HEIGHT = 50
TOL = 2


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


STATE_JS = """
() => {
  const se = document.scrollingElement || document.documentElement;
  const rect = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return {top: Math.round(r.top), bottom: Math.round(r.bottom), height: Math.round(r.height)};
  };
  return {
    scrollY: Math.round(window.scrollY),
    maxScroll: se.scrollHeight - window.innerHeight,
    active: rect(document.querySelector('.tab-main > *')),
    tabbar: rect(document.querySelector('.van-tabbar')),
    backNav: rect(document.querySelector('.back-nav')),
    bodyPosition: getComputedStyle(document.body).position,
  };
}
"""


def state(page) -> dict:
    return page.evaluate(STATE_JS)


def assert_near(actual, expected, message, tol: int = TOL) -> None:
    if abs(actual - expected) > tol:
        raise SmokeFailure(f"{message}：期望 {expected}±{tol}，实际 {actual}")


def assert_restored(st: dict, expected: int, message: str) -> None:
    """目标页内容高度可能比离开时矮（异步列表/图片未加载完），此时浏览器会夹到底部；
    只要不超过该页自己的记录位置即可，绝不能是别的 tab 的位置。"""
    assert_near(st["scrollY"], min(expected, max(0, st["maxScroll"])), message, tol=8)


def scroll_to(page, y: int) -> None:
    page.evaluate("(y) => window.scrollTo(0, y)", y)
    page.wait_for_timeout(120)


# 批次6 起 likes/home/profile/detail/group 的 render 都在 lazy chunk 里：
# 切页后先出现 .page-chunk-loading，再由 chunk 渲染出真实页面。之前用固定
# 450ms 判断就绪，在慢网/低端机上会假红；这里改成显式等待「页面标记出现
# 且 .page-chunk-loading 消失且尺寸非零」。标记按页选区只用于判就绪，
# 不改变原有滚动断言。
PAGE_MARKERS = {
    "cards": ".cards-page",
    "likes": ".msg-card",
    "home": ".activity-card, .van-empty",
    "profile": ".profile-header",
    "my_activities": ".back-nav",
    "my_likes": ".back-nav",
    "my_messages": ".back-nav",
    "received_messages": ".back-nav",
    "reported_messages": ".back-nav",
    "liked_me": ".back-nav",
    "mutual": ".back-nav",
    "user_detail": ".back-nav",
    "activity_detail": ".back-nav",
    "group_admin": ".back-nav",
    "group_detail": ".back-nav",
    "group_roster": ".back-nav",
    "group_select": ".back-nav",
    "group_result": ".back-nav",
    "onetoone_result": ".back-nav",
    "checkin": ".back-nav",
    "points": ".back-nav",
    "points_admin": ".back-nav",
    "profile_detail": ".back-nav",
    "profile_edit": ".back-nav",
    "my_groups": ".back-nav",
    "password": ".back-nav",
}
READY_TIMEOUT_MS = 8000


def wait_page_ready(page, key: str, timeout: int = READY_TIMEOUT_MS) -> None:
    """等目标页真正渲染出来（lazy chunk 已加载、loading 占位已消失）。

    判据按「当前挂载页的根元素」范围查标记：Transition 是 out-in，
    只查根元素可避免把上一个页面（同样含 .page/.back-nav）当成新页就绪。
    """
    marker = PAGE_MARKERS.get(key)
    if not marker:
        raise SmokeFailure(f"未登记页面就绪标记：{key}")
    try:
        page.wait_for_function(
            """(sel) => {
                if (document.querySelector('.page-chunk-loading')) return false;
                const root = document.querySelector('.tab-main > *');
                if (!root) return false;
                if (!root.matches(sel) && !root.querySelector(sel)) return false;
                const r = root.getBoundingClientRect();
                return r.width > 0 && r.height > 0;
            }""",
            arg=marker,
            timeout=timeout,
        )
    except PlaywrightTimeoutError:
        diag = page.evaluate(
            """() => {
                const root = document.querySelector('.tab-main > *');
                return {
                    loading: !!document.querySelector('.page-chunk-loading'),
                    loadingText: (document.querySelector('.page-chunk-loading') || {}).innerText || '',
                    rootClass: root ? String(root.className) : '',
                    rootHtml: root ? root.outerHTML.slice(0, 120) : '',
                    boot: !!document.querySelector('.boot-fail'),
                    scrollY: Math.round(window.scrollY)
                };
            }"""
        )
        raise SmokeFailure(f"页面 {key} 在 {timeout}ms 内未就绪（lazy chunk 未渲染）：{diag}") from None


def click_tab(page, key: str, settle_ms: int = 0) -> None:
    """点底部 tab。用 DOM click 绕开一次性引导层，避免 pointer 拦截。"""
    page.evaluate(
        """(label) => {
            const mask = document.querySelector('.swipe-guide-mask');
            if (mask) mask.remove();
            const item = [...document.querySelectorAll('.van-tabbar-item')]
                .find((el) => el.innerText.includes(label));
            if (!item) throw new Error('找不到 tab: ' + label);
            item.click();
        }""",
        TABS[key],
    )
    wait_page_ready(page, key)
    if settle_ms:
        page.wait_for_timeout(settle_ms)


def dismiss_guides(page) -> None:
    """关掉首次进入的滑动引导 / 更新说明等一次性弹层。"""
    page.evaluate(
        """() => {
            const mask = document.querySelector('.swipe-guide-mask');
            if (mask) mask.remove();
            document.querySelectorAll('.van-overlay').forEach((el) => el.remove());
        }"""
    )


def wait_app(page) -> None:
    page.wait_for_selector(".van-tabbar", timeout=20000)
    try:
        page.locator("#boot-splash").wait_for(state="detached", timeout=15000)
    except PlaywrightTimeoutError:
        pass
    wait_page_ready(page, "cards")
    page.wait_for_timeout(300)   # 首卡图片/引导层的短暂收尾，不用于判就绪
    dismiss_guides(page)


def click_cell(page, text: str) -> bool:
    return bool(page.evaluate(
        """(text) => {
            const cell = [...document.querySelectorAll('.van-cell')]
                .find((el) => (el.innerText || '').includes(text));
            if (!cell) return false;
            cell.click();
            return true;
        }""",
        text,
    ))


def sample_tabbar(page, during: int = 0) -> set[tuple[int, int]]:
    """采样底栏位置/高度；during>0 时在切换动画期间连采。"""
    samples = set()
    for _ in range(max(1, during)):
        rect = page.evaluate(
            "() => { const r = document.querySelector('.van-tabbar').getBoundingClientRect();"
            " return [Math.round(r.top), Math.round(r.height)]; }"
        )
        samples.add((int(rect[0]), int(rect[1])))
        if during:
            page.wait_for_timeout(40)
    return samples


def run(page, out_dir: Path) -> list[str]:
    notes: list[str] = []
    width, height = VIEWPORT
    page.goto(os.environ["BASE_URL"].rstrip("/") + "/", wait_until="domcontentloaded")
    wait_app(page)
    page.screenshot(path=str(out_dir / f"tab-scroll-{width}-0-cards.png"))

    # 1. 活动页滚动 → 切「我的」：不得继承活动页滚动位置。
    click_tab(page, "home")
    page.wait_for_selector(".activity-card", timeout=15000)
    page.wait_for_timeout(400)
    home_state = state(page)
    if home_state["maxScroll"] < 120:
        raise SmokeFailure(f"活动列表太短（maxScroll={home_state['maxScroll']}），无法验证滚动隔离")
    home_scroll = min(500, home_state["maxScroll"])
    scroll_to(page, home_scroll)
    after = state(page)
    assert_near(after["scrollY"], home_scroll, "活动页未滚到目标位置", tol=8)

    click_tab(page, "profile")
    profile_state = state(page)
    assert_near(profile_state["scrollY"], 0, "切到「我的」后仍带着活动页滚动位置")
    assert profile_state["active"] and profile_state["active"]["top"] == 0, "「我的」页顶被裁掉"
    page.screenshot(path=str(out_dir / f"tab-scroll-{width}-1-profile.png"))

    # 2. 「我的」滚到底 → 回「活动」：还原活动页离开时的位置，不沿用「我的」的位置。
    scroll_to(page, max(0, profile_state["maxScroll"]))
    profile_bottom = state(page)
    if profile_bottom["maxScroll"] >= 60:
        assert_near(profile_bottom["scrollY"], profile_bottom["maxScroll"], "「我的」未滚到底", tol=4)
    click_tab(page, "home")
    back_home = state(page)
    assert_restored(back_home, home_scroll, "回到活动 tab 未还原离开时的位置")
    page.screenshot(path=str(out_dir / f"tab-scroll-{width}-2-home-restored.png"))

    # 3. 其余 tab 首次进入都在顶部。
    for key, label in (("likes", "消息"), ("cards", "牵线")):
        click_tab(page, key)
        st = state(page)
        assert_near(st["scrollY"], 0, f"切到「{label}」后滚动位置不为 0")
        assert st["active"] and st["active"]["top"] == 0, f"「{label}」页顶被裁掉"
        page.screenshot(path=str(out_dir / f"tab-scroll-{width}-3-{key}.png"))

    # 4. 切 tab 全程底栏不闪：位置/高度恒定，且贴视口底部。
    samples = set()
    for key in ("home", "profile", "cards", "likes", "home"):
        click_tab(page, key, settle_ms=0)
        samples |= sample_tabbar(page, during=6)
    expected = (height - TAB_HEIGHT, TAB_HEIGHT)
    if samples != {expected}:
        raise SmokeFailure(f"底栏在切换过程中位置/高度变化：{sorted(samples)}，期望 {expected}")

    # 5. 活动列表滚动态进详情：详情从顶部开始，返回栏贴顶，返回后列表位置还原。
    click_tab(page, "home")
    page.wait_for_selector(".activity-card", timeout=15000)
    page.wait_for_timeout(400)
    home_state = state(page)
    list_scroll = min(600, home_state["maxScroll"])
    scroll_to(page, list_scroll)
    page.evaluate("document.querySelector('.activity-card').click()")
    wait_page_ready(page, "activity_detail")
    detail = state(page)
    assert_near(detail["scrollY"], 0, "活动详情继承了列表的滚动位置")
    if detail["backNav"] is None:
        raise SmokeFailure("活动详情没有返回栏 .back-nav")
    assert_near(detail["backNav"]["top"], 0, "活动详情返回栏没有贴顶")
    page.screenshot(path=str(out_dir / f"tab-scroll-{width}-4-detail.png"))
    if detail["maxScroll"] > 120:
        scroll_to(page, 120)
        sticky = state(page)
        assert_near(sticky["backNav"]["top"], 0, "活动详情滚动后返回栏没有吸顶")
        assert_near(sticky["scrollY"], 120, "活动详情滚不动", tol=8)
    page.evaluate("document.querySelector('.back-nav').click()")
    wait_page_ready(page, "home")
    returned = state(page)
    assert_restored(returned, list_scroll, "从活动详情返回后列表位置丢失")

    # 6. 个人页子页（我的活动＝报名列表）：从顶部进入，返回还原个人页位置。
    click_tab(page, "profile")
    page.wait_for_timeout(300)
    profile_state = state(page)
    profile_scroll = min(200, profile_state["maxScroll"])
    scroll_to(page, profile_scroll)
    if not click_cell(page, "我的活动"):
        notes.append("个人页没有「我的活动」入口，跳过子页滚动检查")
    else:
        wait_page_ready(page, "my_activities")
        sub = state(page)
        assert_near(sub["scrollY"], 0, "「我的活动」子页继承了个人页滚动位置")
        if sub["maxScroll"] > 120:
            scroll_to(page, 120)
        page.screenshot(path=str(out_dir / f"tab-scroll-{width}-5-my-activities.png"))
        page.evaluate("document.querySelector('.back-nav').click()")
        wait_page_ready(page, "profile")
        restored = state(page)
        assert_restored(restored, profile_scroll, "从「我的活动」返回后个人页位置丢失")

    # 7. 弹层锁滚动：意见反馈打开时，滚轮不得带动页面。
    scroll_to(page, min(120, profile_state["maxScroll"]))
    before = state(page)["scrollY"]
    if not click_cell(page, "意见反馈"):
        notes.append("个人页没有「意见反馈」入口，跳过弹层锁滚动检查")
    else:
        page.wait_for_selector(".van-popup", timeout=8000)
        page.wait_for_timeout(300)
        page.mouse.move(width // 2, height // 2)
        page.mouse.wheel(0, 600)
        page.wait_for_timeout(400)
        assert_near(state(page)["scrollY"], before, "弹层打开时滚轮带动了外层页面")
        page.keyboard.press("Escape")
        page.wait_for_timeout(500)

    # 8. 管理台 iframe overlay：锁住外层滚动，iframe 内滚动不影响外层，关闭后位置还原。
    click_tab(page, "profile")
    page.wait_for_timeout(400)
    scroll_to(page, min(200, state(page)["maxScroll"]))
    locked_scroll = state(page)["scrollY"]
    if not click_cell(page, "管理后台"):
        notes.append("当前账号没有「管理后台」入口，跳过 iframe overlay 检查")
    else:
        page.wait_for_selector(".embed-overlay", timeout=15000)
        try:
            page.wait_for_selector(".embed-frame--ready", timeout=15000)
        except Exception:  # noqa: BLE001 - 管理台首屏慢时仍验证外层锁滚动
            notes.append("管理台 iframe 15s 内未就绪，仅验证外层锁滚动")
        page.wait_for_timeout(400)
        opened = state(page)
        if opened["bodyPosition"] != "fixed":
            raise SmokeFailure("管理台 overlay 打开后外层页面没有锁定滚动")
        page.mouse.move(width // 2, 60)
        page.mouse.wheel(0, 500)
        page.wait_for_timeout(400)
        wheeled = state(page)
        assert wheeled["active"] and abs(wheeled["active"]["top"] - opened["active"]["top"]) <= TOL, \
            "管理台 overlay 打开时外层页面被滚动了"
        frame = page.query_selector(".embed-frame")
        if frame and frame.content_frame():
            frame.content_frame().evaluate("window.scrollTo(0, 400)")
            page.wait_for_timeout(300)
            inner = state(page)
            assert inner["active"] and abs(inner["active"]["top"] - opened["active"]["top"]) <= TOL, \
                "管理台 iframe 内滚动带动了外层页面"
        page.screenshot(path=str(out_dir / f"tab-scroll-{width}-6-admin-overlay.png"))
        page.evaluate("document.querySelector('.embed-back').click()")
        page.wait_for_timeout(900)
        closed = state(page)
        assert_restored(closed, locked_scroll, "关闭管理台后没有回到打开前的位置")
        if closed["bodyPosition"] == "fixed":
            raise SmokeFailure("关闭管理台后外层滚动锁没有解除")

        # 浏览器返回同样只关 overlay，并解除滚动锁（防锁泄漏）。
        click_cell(page, "管理后台")
        page.wait_for_selector(".embed-overlay", timeout=15000)
        page.wait_for_timeout(600)
        page.go_back()
        page.wait_for_timeout(900)
        if page.evaluate("!!document.querySelector('.embed-overlay')"):
            raise SmokeFailure("浏览器返回没有关闭管理台 overlay")
        after_back = state(page)
        if after_back["bodyPosition"] == "fixed":
            raise SmokeFailure("浏览器返回关闭管理台后滚动锁没有解除")
        assert_restored(after_back, locked_scroll, "浏览器返回关闭管理台后位置丢失")

    return notes


def attach_errors(page) -> list[str]:
    errors: list[str] = []
    page.on("console", lambda msg: errors.append(
        f"console: {msg.text} @ {(msg.location or {}).get('url', '')}") if msg.type == "error" else None)
    page.on("pageerror", lambda exc: errors.append(f"pageerror: {exc}"))
    return errors


def main() -> int:
    base_url = require("BASE_URL")
    cookie = require("SESSION_COOKIE")
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/tab-scroll-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)
    index_html = os.environ.get("INDEX_HTML", "").strip()

    failures: list[str] = []
    notes: list[str] = []
    engine = os.environ.get("BROWSER", "chromium").strip() or "chromium"
    with sync_playwright() as pw:
        if engine not in ("chromium", "firefox", "webkit"):
            sys.exit(f"BROWSER 只支持 chromium/firefox/webkit，收到 {engine}")
        browser = getattr(pw, engine).launch()
        width, height = VIEWPORT
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
        context.add_cookies([{
            "name": "yxq_session",
            "value": cookie,
            "url": base_url,
            "httpOnly": True,
            "sameSite": "Lax",
        }])
        page = context.new_page()
        errors = attach_errors(page)
        if index_html:
            # 先真实加载一次：服务端在文档响应里下发 csrf cookie，本地外壳顶替文档时
            # 这一步不会被走到，缺 cookie 会让 /legacy/api/client-events 等写入 403。
            page.goto(base_url.rstrip("/") + "/", wait_until="domcontentloaded")
            page.wait_for_timeout(1500)
            # MAIN_SCRIPT 可指定本机主脚本（例如尚未部署的 app.min.js）；
            # 不指定时用同目录未压缩的 app.js。
            main_script = os.environ.get("MAIN_SCRIPT", "").strip()
            script = Path(main_script) if main_script else Path(index_html).with_name("app.js")
            if not script.exists():
                sys.exit(f"MAIN_SCRIPT 不存在：{script}")
            arm = local_assets.Arm.from_files("local", Path(index_html), script)
            local_assets.install(context, base_url, arm)
        try:
            notes = run(page, out_dir)
            if errors:
                raise SmokeFailure("出现 console error / pageerror：" + "；".join(errors[:5]))
        except Exception as exc:  # noqa: BLE001 - 单点失败继续汇总
            failures.append(str(exc))
            try:
                page.screenshot(path=str(out_dir / f"tab-scroll-{width}-FAIL.png"))
            except Exception:  # pragma: no cover - 截图失败不影响结论
                pass
        finally:
            context.close()
            browser.close()

    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print("PASS 四 tab / 子页 / 弹层 / 管理台 iframe overlay 滚动隔离")
    for note in notes:
        print(" note:", note)
    print(f"browser: {engine}")
    print(f"screenshots: {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
