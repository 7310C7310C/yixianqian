#!/usr/bin/env python3
"""活动管理（v1 SPA 原生入口）Playwright smoke。

覆盖 390×844 与 1280×900：

1. 登录态进入 `/`，底部「我的」出现「管理」区与「👥 活动管理」入口；
2. 进入活动管理总览：标题、待执行/已完成统计、至少一条活动；
3. 进入活动控制台：模式卡、活动状态按钮、现场签到码输入都在；
4. 从控制台进入「报名名单」再返回；
5. 从控制台进入「现场签到」再返回；
6. 控制台页头「✏️ 编辑」打开同源 iframe（`/admin/activities/{id}/edit`），
   iframe 内 SSR 表单可见；关闭 overlay 后回到控制台；
7. 全程 console error / pageerror 为 0（忽略通知 SSE 断线）。

需要已部署的实例与一个可管理活动的管理员 session；不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    SHOT_DIR=/tmp/opencode/admin-activity-native \
    python3 tools/browser/admin_activity_native_smoke.py

只读：不保存签到码、不改活动状态、不执行分组/一对一。
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

VIEWPORTS = ((390, 844), (1280, 900))
MAX_ROWS = 5
# GaLoading 组件根节点：只有整块异步内容还在加载时才会挂载，是「数据未到」的统一标记。
LOADING = ".ga-slow-loading"


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def wait_block_loaded(page, tag: str, what: str, ready_js: str, timeout: int = 15000) -> None:
    """等整块异步内容渲染完成：加载指示消失且 ready_js 为真。

    `.ga-title` 在切页瞬间就挂载，但 `gaLoading` 仍为 true（只渲染 ga-loading），
    统计、列表、名册都还没进 DOM；只看标题就断言会把加载态当成空数据。
    """
    script = f"() => !document.querySelector('{LOADING}') && ({ready_js})"
    try:
        page.wait_for_function(script, timeout=timeout)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(
            f"{tag}: {what} 未在 {timeout // 1000}s 内渲染完成"
            f"（加载指示未消失或数据未到）：{exc}") from exc


class ConsoleGuard:
    """收集 console error / pageerror；通知 SSE 断线不计（既有 smoke 同款豁免）。"""

    def __init__(self, page) -> None:
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


def add_session_cookie(context, base: str, raw: str, name: str) -> None:
    parsed = urlparse(base)
    context.add_cookies([{
        "name": name,
        "value": raw,
        "domain": parsed.hostname,
        "path": "/",
        "secure": parsed.scheme == "https",
        "httpOnly": True,
        "sameSite": "Lax",
    }])


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


def wait_profile(page, shot_dir: Path, tag: str) -> None:
    page.locator("#app").wait_for(state="visible", timeout=20000)
    page.wait_for_function("() => window.__YXQ_AUTHENTICATED__ === true", timeout=20000)
    tap_tab(page, "我的")
    cell = page.locator(".van-cell").filter(has_text="活动管理").first
    try:
        cell.wait_for(state="visible", timeout=10000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 我的页没有「👥 活动管理」入口（admin 账号？）：{exc}") from exc
    title = cell.locator(".van-cell__title")
    text = title.inner_text() if title.count() else cell.inner_text()
    if "活动管理" not in text:
        raise SmokeFailure(f"{tag}: 管理入口文案不符（{text!r}）")
    cell.scroll_into_view_if_needed()
    page.wait_for_timeout(300)
    page.screenshot(path=str(shot_dir / f"{tag}-profile-admin.png"))


def open_admin_list(page, shot_dir: Path, tag: str) -> None:
    page.locator(".van-cell").filter(has_text="活动管理").first.click()
    try:
        page.locator(".ga-title", has_text="活动管理").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 活动管理页未打开（权限或接口失败）：{exc}") from exc
    # gaLoading 期间统计和 .ga-act-row 都不存在；等整块结束，避免把加载态当空列表。
    wait_block_loaded(page, tag, "活动管理列表",
                      "!!document.querySelector('.ga-act-row, .van-empty')")
    if page.get_by_text("活动没载出来").count():
        raise SmokeFailure(f"{tag}: 活动管理列表出现错误态")
    try:
        page.locator(".ga-act-row").first.wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 活动管理列表为空（smoke 夹具缺少活动）：{exc}") from exc
    page.screenshot(path=str(shot_dir / f"{tag}-group-admin.png"))


def open_console(page, row_index: int, shot_dir: Path, tag: str) -> str | None:
    """打开第 row_index 条活动；若该活动没有报名名单入口，返回列表并返回 None。"""
    rows = page.locator(".ga-act-row")
    activity_no = rows.nth(row_index).locator(".ga-row-id").inner_text().strip()
    rows.nth(row_index).click()
    if not wait_console(page, tag, activity_no):
        return None
    if row_index == 0:
        page.screenshot(path=str(shot_dir / f"{tag}-group-detail.png"))
    return activity_no


def wait_console(page, tag: str, activity_no: str) -> bool:
    try:
        page.locator(".ga-signin-code-field").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 活动控制台未载入（{activity_no}）：{exc}") from exc
    if page.get_by_text("活动没载出来").count():
        raise SmokeFailure(f"{tag}: 活动控制台出现错误态（{activity_no}）")
    if page.locator(".ga-status-btns .van-button").count() < 5:
        raise SmokeFailure(f"{tag}: 控制台缺少活动状态按钮（{activity_no}）")
    if not page.locator(".ga-detail-tabs").count():
        raise SmokeFailure(f"{tag}: 控制台缺少模式 tabs（{activity_no}）")
    roster = page.get_by_role("button", name=re.compile("查看未填写名单|查看未提交名单"))
    if roster.count() and roster.first.is_visible():
        return True
    page.locator(".ga-page .back-nav").first.click()
    page.locator(".ga-title", has_text="活动管理").wait_for(state="visible", timeout=10000)
    return False


def open_console_by_no(page, activity_no: str, tag: str) -> None:
    row = page.locator(".ga-act-row", has_text=activity_no).first
    try:
        row.wait_for(state="visible", timeout=10000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 列表里找不到活动 {activity_no}：{exc}") from exc
    row.click()
    wait_console(page, tag, activity_no)


def check_roster(page, shot_dir: Path, tag: str) -> None:
    roster = page.get_by_role("button", name=re.compile("查看未填写名单|查看未提交名单"))
    roster.first.click()
    try:
        page.locator(".ga-title", has_text="报名名单").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 报名名单页未打开：{exc}") from exc
    # .ga-roster-tabs 只在 gaRosterLoading 结束后渲染；只等标题会把加载态当名单页。
    wait_block_loaded(page, tag, "报名名单", "!!document.querySelector('.ga-roster-tabs')")
    if page.get_by_text("名单没载出来").count():
        raise SmokeFailure(f"{tag}: 报名名单出现错误态")
    page.locator(".ga-roster-tabs").first.wait_for(state="visible", timeout=10000)
    page.screenshot(path=str(shot_dir / f"{tag}-group-roster.png"))
    page.locator(".ga-page .back-nav").first.click()
    page.locator(".ga-signin-code-field").wait_for(state="visible", timeout=10000)


def check_checkin(page, shot_dir: Path, tag: str) -> None:
    button = page.get_by_role("button", name=re.compile("现场签到"))
    button.first.click()
    try:
        page.locator(".ga-title", has_text="现场签到").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 现场签到页未打开：{exc}") from exc
    # 加载态本身就是一个 .ga-empty 包着 ga-loading；只等 .ga-empty 会把转圈误当「空名册」。
    wait_block_loaded(page, tag, "现场签到名册", "!!document.querySelector('.ga-roster-tabs')")
    if page.get_by_text("签到名册没载出来").count():
        raise SmokeFailure(f"{tag}: 现场签到名册出现错误态")
    page.locator(".ga-signin-row, .van-empty").first.wait_for(state="visible", timeout=15000)
    page.screenshot(path=str(shot_dir / f"{tag}-checkin.png"))
    page.locator(".ga-page .back-nav").first.click()
    # 控制台→签到会把 group_detail 的历史条目就地改写为 checkin，返回直接到总览；
    # 若从列表入口进入（历史是 push），则需再返回一次。
    try:
        page.locator(".ga-title", has_text="活动管理").wait_for(state="visible", timeout=3000)
    except PlaywrightTimeoutError:
        page.locator(".ga-page .back-nav").first.click()
        page.locator(".ga-title", has_text="活动管理").wait_for(state="visible", timeout=10000)


def check_edit_iframe(page, shot_dir: Path, tag: str) -> None:
    button = page.get_by_role("button", name=re.compile("编辑"))
    try:
        button.first.wait_for(state="visible", timeout=5000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 控制台没有「✏️ 编辑」按钮：{exc}") from exc
    button.first.click()
    page.locator(".embed-overlay").wait_for(state="visible", timeout=10000)
    page.locator(".embed-frame").wait_for(state="attached", timeout=10000)
    src = page.locator(".embed-frame").get_attribute("src") or ""
    if not re.search(r"^/admin/activities/\d+/edit$", src):
        raise SmokeFailure(f"{tag}: 编辑 iframe src 不是 /admin/activities/<id>/edit（{src}）")
    try:
        page.frame_locator(".embed-frame").locator('input[name="name"]').wait_for(timeout=20000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 编辑 iframe 未载入 SSR 表单：{exc}") from exc
    page.locator(".embed-loading").wait_for(state="hidden", timeout=10000)
    page.screenshot(path=str(shot_dir / f"{tag}-activity-edit.png"))
    page.locator(".embed-back").click()
    page.locator(".embed-overlay").wait_for(state="detached", timeout=5000)
    page.locator(".ga-signin-code-field").wait_for(state="visible", timeout=10000)
    final = urlparse(page.url)
    if final.query:
        raise SmokeFailure(f"{tag}: 关闭编辑 iframe 后顶层 URL 不干净（{page.url}）")


def run_viewport(browser, base: str, width: int, height: int, cookie: str,
                 cookie_name: str, shot_dir: Path) -> None:
    tag = str(width)
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    add_session_cookie(context, base, cookie, cookie_name)
    page = context.new_page()
    guard = ConsoleGuard(page)
    try:
        resp = page.goto(base + "/", wait_until="domcontentloaded", timeout=30000)
        if resp is None or resp.status != 200:
            raise SmokeFailure(f"{tag}: / status={getattr(resp, 'status', None)}")
        wait_profile(page, shot_dir, tag)
        open_admin_list(page, shot_dir, tag)

        rows = page.locator(".ga-act-row")
        activity_no = None
        for index in range(min(rows.count(), MAX_ROWS)):
            activity_no = open_console(page, index, shot_dir, tag)
            if activity_no:
                break
        if not activity_no:
            raise SmokeFailure(f"{tag}: 前 {MAX_ROWS} 条活动都没有报名名单入口（模式为未选择？）")

        check_roster(page, shot_dir, tag)
        check_checkin(page, shot_dir, tag)
        open_console_by_no(page, activity_no, tag)
        check_edit_iframe(page, shot_dir, tag)
        page.locator(".ga-page .back-nav").first.click()
        page.locator(".ga-title", has_text="活动管理").wait_for(state="visible", timeout=10000)
        guard.assert_clean(f"{tag} 活动管理")
        print(f"[ok] {width}x{height} 我的→活动管理→控制台→名单→签到→编辑 iframe→返回")
    finally:
        context.close()


def main() -> int:
    base = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    cookie_name = os.environ.get("SESSION_COOKIE_NAME", "yxq_session").strip() or "yxq_session"
    shot_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/admin-activity-native"))
    shot_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for width, height in VIEWPORTS:
                try:
                    run_viewport(browser, base, width, height, cookie, cookie_name, shot_dir)
                except SmokeFailure as exc:
                    failures.append(str(exc))
        finally:
            browser.close()

    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print("PASS")
    print(f"screenshots: {shot_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
