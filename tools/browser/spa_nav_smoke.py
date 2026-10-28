#!/usr/bin/env python3
"""用户端 SPA 整页导航回归（Playwright）。

覆盖 PM 的验收口径：登录后的常规操作不得触发主框架（顶层 document）导航，
避免浏览器顶部进度条；只有登录/注册/退出这类服务端页面才允许整页跳转。

步骤与断言（390x844 / 1280x900 双视口）：

1. 切四个 tab（牵线/消息/活动/我的）来回多次：0 次整页导航；
2. 打开活动详情、返回：0 次；
3. 我的 → 我的活动、麦穗：0 次；
4. 消息 tab（通知列表、进入即已读）：0 次；
5. 意见反馈提交（POST 被 mock，不落库）：0 次；
6. 活动报名（POST 被 mock，不落库）：0 次；
7. 版本变更：/legacy/api/version 被 mock 成新版本，只出现 .update-bar 提示条，
   0 次整页导航；点提示条后才有 1 次导航（URL 带 yxqv=）；
8. 401：/legacy/api/points/me 被 mock 成 401，先弹「登录已过期」对话框，
   0 次整页导航；点「重新登录」后才导航到 /login。

主框架导航用 CDP `Page.frameNavigated` 里 `type=Navigation`（跨文档）判定，
同文档的 history.pushState 不计入；同时记录文档请求与 Playwright
`framenavigated` 作为诊断。mock 的版本号一定与构建版本不同，因此不需要真的换版本。

需要已登录环境（staging 或本地实例）与 yxq_session：

    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/spa_nav_smoke.py

可选：SHOT_DIR 截图目录（默认 /tmp/opencode/spa-nav-smoke）。
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

VIEWPORTS = ((390, 844), (1280, 900))
IGNORED_CONSOLE = ("notifications/stream", "401", "Failed to load resource")


class NavSmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def dismiss_guides(page) -> None:
    """关掉首次进入的滑动引导/活动/更新说明等一次性弹层。"""
    page.wait_for_timeout(600)
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
        page.wait_for_timeout(300)


def tap_tab(page, label: str) -> None:
    """点击底部 tab；晚到的一次性引导可能拦截点击，带重试。"""
    item = page.locator(".van-tabbar-item").filter(has_text=label)
    last_error = None
    for _ in range(4):
        dismiss_guides(page)
        try:
            item.click(timeout=4000)
            return
        except PlaywrightTimeoutError as exc:
            last_error = exc
            page.keyboard.press("Escape")
            page.wait_for_timeout(300)
    raise NavSmokeFailure(f"无法点击 tab {label}（4 次尝试）：{last_error}")


def wait_profile(page) -> None:
    """等「我的」页真正进场（过渡 out-in 期间旧页仍在，直接找入口会落空）。"""
    page.locator(".van-cell").filter(has_text="修改密码").first.wait_for(state="visible", timeout=15000)


def tap_cell(page, text: str) -> None:
    cell = page.locator(".van-cell").filter(has_text=text).first
    cell.wait_for(state="visible", timeout=15000)
    cell.click()


class NavProbe:
    """主框架整页导航探针。

    CDP `Page.frameNavigated` 的 type 区分跨文档（Navigation）与同文档
    （SameDocument，history.pushState 走这里），只有前者算「整页导航」。
    """

    def __init__(self, context, page):
        self.page = page
        self.armed = False
        self.navigations: list[str] = []      # 跨文档导航 URL（自上个 checkpoint）
        self.doc_requests: list[str] = []     # 主框架文档请求（自上个 checkpoint）
        self.frame_events: list[str] = []     # Playwright framenavigated（含同文档，诊断）
        self.same_documents: list[str] = []   # 同文档 history 导航（pushState）
        self.total = {"nav": 0, "same": 0, "docs": 0}   # 整轮累计（用于报告）
        self.cdp = context.new_cdp_session(page)
        self.cdp.send("Page.enable")
        self.cdp.on("Page.frameNavigated", self._on_frame_navigated)
        page.on("request", self._on_request)
        page.on("framenavigated", self._on_pw_navigated)

    def _on_frame_navigated(self, params) -> None:
        frame = params.get("frame") or {}
        if frame.get("parentId"):
            return
        url = frame.get("url") or ""
        kind = params.get("type")
        if kind == "SameDocument":
            if self.armed and url:
                self.same_documents.append(url)
                self.total["same"] += 1
            return
        if kind != "Navigation":
            return
        if self.armed and url and url != "about:blank":
            self.navigations.append(url)
            self.total["nav"] += 1

    def _on_request(self, request) -> None:
        try:
            if not request.is_navigation_request():
                return
            if request.frame != self.page.main_frame:
                return
        except Exception:  # noqa: BLE001 - 帧已销毁时忽略
            return
        if self.armed:
            self.doc_requests.append(request.url)
            self.total["docs"] += 1

    def _on_pw_navigated(self, frame) -> None:
        if frame != self.page.main_frame:
            return
        if self.armed:
            self.frame_events.append(frame.url)

    def arm(self) -> None:
        self.armed = True
        self.reset()

    def reset(self) -> None:
        self.navigations.clear()
        self.doc_requests.clear()
        self.frame_events.clear()
        self.same_documents.clear()

    def checkpoint(self, name: str) -> None:
        if self.navigations:
            raise NavSmokeFailure(f"{name}: 出现整页导航 {self.navigations}")
        if self.doc_requests:
            raise NavSmokeFailure(f"{name}: 主框架发起了文档请求 {self.doc_requests}")


def attach_errors(page) -> list[dict[str, str]]:
    errors: list[dict[str, str]] = []

    def on_pageerror(exc):
        errors.append({"kind": "pageerror", "text": str(exc), "url": ""})

    def on_console(msg):
        if msg.type != "error":
            return
        text = msg.text
        if any(skip in text for skip in IGNORED_CONSOLE):
            return
        loc = msg.location or {}
        errors.append({"kind": "console", "text": text, "url": loc.get("url", "")})

    page.on("console", on_console)
    page.on("pageerror", on_pageerror)
    return errors


def open_activity_detail(page, probe: NavProbe, step: str) -> None:
    card = page.locator(".activity-card").first
    card.wait_for(state="visible", timeout=20000)
    dismiss_guides(page)
    card.locator("h3").first.click()
    page.locator(".back-nav").first.wait_for(state="visible", timeout=15000)
    page.wait_for_timeout(400)
    probe.checkpoint(step)


def close_subpage(page, probe: NavProbe, step: str) -> None:
    page.locator(".back-nav").first.click()
    page.wait_for_timeout(500)
    probe.checkpoint(step)


def run_viewport(browser, width: int, height: int, base_url: str, cookie: str,
                 out_dir: Path) -> list[str]:
    failures: list[str] = []
    tag = str(width)
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.route("**/legacy/api/notifications/stream", lambda route: route.abort())
    # 关掉一次性引导/更新说明/活动引导：它们只影响首次进入，与本用例无关且会遮挡点击
    context.add_init_script(
        "try{localStorage.setItem('yxq_swipe_guide_v1','1');"
        "localStorage.setItem('yxq_self_card_guide_v1','1');"
        "localStorage.setItem('yxq_update_notes_v1','1');"
        "sessionStorage.setItem('yxq_activity_promo_v1','1');}catch(e){}"
    )
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    page = context.new_page()
    errors = attach_errors(page)
    probe = NavProbe(context, page)
    stage = "启动"

    try:
        notes: list[str] = []
        page.goto(base_url + "/", wait_until="domcontentloaded", timeout=30000)
        page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
        dismiss_guides(page)
        if not page.evaluate("window.__YXQ_AUTHENTICATED__ === true"):
            raise NavSmokeFailure("未登录：window.__YXQ_AUTHENTICATED__ 不是 true")
        probe.arm()

        # 1. 四个 tab 来回切
        stage = "切四个 tab"
        for label in ["活动", "我的", "消息", "牵线", "活动", "我的", "消息", "牵线"]:
            tap_tab(page, label)
            page.wait_for_timeout(450)
            probe.checkpoint(f"切 tab「{label}」")
        page.screenshot(path=str(out_dir / f"tabs-{tag}.png"))

        # 2. 活动详情 → 返回
        stage = "活动详情"
        tap_tab(page, "活动")
        open_activity_detail(page, probe, "打开活动详情")
        page.screenshot(path=str(out_dir / f"activity-detail-{tag}.png"))
        close_subpage(page, probe, "活动详情返回")

        # 3. 我的活动 → 返回（观察员没有该入口，跳过）
        stage = "我的活动"
        tap_tab(page, "我的")
        wait_profile(page)
        if page.locator(".van-cell").filter(has_text="我的活动").count():
            tap_cell(page, "我的活动")
            page.wait_for_timeout(700)
            probe.checkpoint("进入我的活动")
            close_subpage(page, probe, "我的活动返回")
        else:
            notes.append("当前账号无「我的活动」入口（观察员），跳过")

        # 4. 麦穗 → 返回
        stage = "麦穗"
        wait_profile(page)
        if page.locator(".profile-card-row").filter(has_text="我的麦穗").count():
            page.locator(".profile-card-row").filter(has_text="我的麦穗").first.click()
            page.locator(".points-balance-label").wait_for(state="visible", timeout=15000)
            page.wait_for_timeout(400)
            probe.checkpoint("进入麦穗")
            close_subpage(page, probe, "麦穗返回")
        else:
            notes.append("当前账号无「麦穗」入口（观察员），跳过")

        # 5. 通知（消息 tab 即通知列表，进入会自动已读）
        stage = "通知"
        tap_tab(page, "消息")
        page.wait_for_timeout(900)
        probe.checkpoint("进入通知")

        # 6. 提交反馈（POST 用 mock，避免往库里写测试数据）
        stage = "提交反馈"
        context.route(
            "**/legacy/api/suggestions",
            lambda route: route.fulfill(status=200, content_type="application/json",
                                        body='{"ok":true}'),
        )
        tap_tab(page, "我的")
        wait_profile(page)
        tap_cell(page, "意见反馈")
        dialog = page.locator(".van-dialog").filter(has_text="意见反馈")
        dialog.wait_for(state="visible", timeout=10000)
        dialog.locator("textarea").fill("导航回归测试，不落库")
        dialog.locator(".van-dialog__confirm").click()
        dialog.wait_for(state="hidden", timeout=10000)
        page.wait_for_timeout(400)
        probe.checkpoint("提交反馈")

        # 7. 报名（POST/DELETE 用 mock；详情仍走真实接口）
        stage = "报名"
        context.route(
            "**/legacy/api/activities/*/signup",
            lambda route: route.fulfill(status=200, content_type="application/json",
                                        body='{"message":"报名成功"}'),
        )
        tap_tab(page, "活动")
        # 活动列表是异步拉的：等首张卡或空态出现后再数
        try:
            page.locator(".activity-card").first.wait_for(state="visible", timeout=10000)
        except PlaywrightTimeoutError:
            pass
        signed = False
        for idx in range(min(page.locator(".activity-card").count(), 4)):
            page.locator(".activity-card").nth(idx).locator("h3").first.click()
            page.locator(".back-nav").first.wait_for(state="visible", timeout=15000)
            page.wait_for_timeout(350)
            probe.checkpoint("打开活动详情")
            # 详情数据是异步拉的：先等报名条渲染，避免在 loading 态误判「没有报名按钮」
            try:
                page.locator(".signup-bar button").first.wait_for(state="visible", timeout=8000)
            except PlaywrightTimeoutError:
                pass
            # 未报名用「立即报名」，已报名用「取消报名」；两者都是报名相关操作
            btn = page.get_by_role("button", name="立即报名")
            if not btn.count():
                btn = page.get_by_role("button", name="取消报名")
            if os.environ.get("NAV_DEBUG") and not btn.count():
                print(f"  [{width}px] card{idx} buttons={[t.strip() for t in page.locator('button').all_inner_texts()]}")
                print(f"  [{width}px] card{idx} text={page.locator('#app').inner_text()[:160]!r}")
            if btn.count():
                btn.first.click()
                page.wait_for_timeout(900)
                probe.checkpoint("提交报名")
                page.screenshot(path=str(out_dir / f"signup-{tag}.png"))
                signed = True
                close_subpage(page, probe, "报名后返回")
                break
            close_subpage(page, probe, "活动详情返回")
        if not signed:
            # 没有「报名中」的活动不算失败：本用例只验证报名操作不触发整页导航
            notes.append("无「报名中」活动，跳过报名点击")
        context.unroute("**/legacy/api/activities/*/signup")

        # 8. 新版本：只出提示条，不自动刷新
        stage = "新版本提示"
        context.route(
            "**/legacy/api/version",
            lambda route: route.fulfill(status=200, content_type="application/json",
                                        body='{"version":"nav-smoke-new-build"}'),
        )
        page.evaluate("() => window.dispatchEvent(new Event('focus'))")
        bar = page.locator(".update-bar")
        bar.wait_for(state="visible", timeout=15000)
        probe.checkpoint("发现新版本只出提示")
        if "有新版本" not in bar.inner_text():
            raise NavSmokeFailure(f"提示条文案异常：{bar.inner_text()!r}")
        page.screenshot(path=str(out_dir / f"update-bar-{tag}.png"))
        probe.reset()
        bar.locator(".update-bar-text").click()
        page.wait_for_url("**yxqv=**", timeout=20000)
        page.locator(".van-tabbar").wait_for(state="visible", timeout=30000)
        if len(probe.navigations) != 1:
            raise NavSmokeFailure(f"点「更新」后应恰好 1 次导航，实际 {probe.navigations}")
        dismiss_guides(page)
        if page.locator(".update-bar").count() and page.locator(".update-bar-close").is_visible():
            page.locator(".update-bar-close").click()

        # 9. 401：先提示，用户点「重新登录」才导航
        stage = "401 提示"
        probe.reset()
        context.route(
            "**/legacy/api/points/me",
            lambda route: route.fulfill(status=401, content_type="application/json",
                                        body='{"error":"未登录"}'),
        )
        tap_tab(page, "我的")
        wait_profile(page)
        page.locator(".profile-card-row").filter(has_text="我的麦穗").first.click()
        expired = page.locator(".van-dialog").filter(has_text="登录已过期")
        expired.wait_for(state="visible", timeout=15000)
        page.wait_for_timeout(400)
        probe.checkpoint("401 先出 App 内提示")
        page.screenshot(path=str(out_dir / f"session-expired-{tag}.png"))
        expired.locator(".van-dialog__confirm").click()
        for _ in range(100):
            if probe.navigations:
                break
            page.wait_for_timeout(200)
        # 真实过期会话会停在 /login；本用例服务端 session 仍有效，/login 会再 303 回 /，
        # CDP 只报告最终落地 URL，所以这里断言「点击后恰好 1 次导航」。
        if len(probe.navigations) != 1:
            raise NavSmokeFailure(f"点「重新登录」后应恰好 1 次导航，实际 {probe.navigations}")
        if not probe.navigations[0].startswith(base_url):
            raise NavSmokeFailure(f"跳转目标异常：{probe.navigations[0]}")

        if errors:
            detail = "; ".join(f"{e['kind']}: {e['text']} ({e['url']})" for e in errors[:5])
            raise NavSmokeFailure(f"出现脚本错误：{detail}")
        print(f"  [{width}px] 常规操作整页导航 0 次；主框架文档请求 0 次；"
              f"同文档 pushState {probe.total['same']} 次；用户点击触发整页导航 {probe.total['nav']} 次（更新/重新登录）")
        for note in notes:
            print(f"  [{width}px] {note}")
    except Exception as exc:  # noqa: BLE001 - 汇总两档视口结果
        failures.append(f"{width}px [{stage}]: {exc}")
    finally:
        context.close()
    return failures


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/spa-nav-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        for width, height in VIEWPORTS:
            failures.extend(run_viewport(browser, width, height, base_url, cookie, out_dir))
        browser.close()

    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print(f"PASS 视口 {', '.join(f'{w}x{h}' for w, h in VIEWPORTS)}；截图 {out_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
