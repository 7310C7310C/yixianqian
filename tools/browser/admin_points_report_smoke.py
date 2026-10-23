#!/usr/bin/env python3
"""麦穗管理 + 举报管理（v1 SPA 原生入口）Playwright smoke。

覆盖 390×844 与 1280×900 两个视口，真实 Chromium：

1. 管理员：「我的→管理」同时出现「🌾 麦穗管理」「🚨 举报管理」
   （与「👥 活动管理」「🛠️ 管理后台」并存）；
2. 麦穗管理：标题、四个统计、累计行、四个 tab；
   「流水 / 兑换 / 邀请」都有数据行，点首个用户打开详情弹层（余额/邀请进度/流水）并关闭；
3. 举报管理：待处理卡片可见，带「同意」「驳回」按钮；
4. 普通用户：我的页没有「管理」区与两个入口；
   `/legacy/api/admin/points/overview` 与 `/legacy/api/messages/reported` 均 403 且返回 v1 error；
5. 全程 console error / pageerror 为 0（忽略通知 SSE 断线）。

需要两个已登录 session（管理员 + 普通用户）；不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<管理员 yxq_session> \
    USER_SESSION_COOKIE=<普通用户 yxq_session> \
    SHOT_DIR=/tmp/opencode/admin-points-report \
    python3 tools/browser/admin_points_report_smoke.py

只读：只打开页面与 tab，不提交调整/退穗/举报处理。
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

VIEWPORTS = ((390, 844), (1280, 900))
POINTS_TABS = ("流水", "兑换", "邀请")
FORBIDDEN_PATHS = (
    "/legacy/api/admin/points/overview",
    "/legacy/api/messages/reported",
)


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


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


def wait_profile(page, tag: str) -> None:
    page.locator("#app").wait_for(state="visible", timeout=20000)
    page.wait_for_function("() => window.__YXQ_AUTHENTICATED__ === true", timeout=20000)
    tap_tab(page, "我的")
    page.locator(".profile-header, .profile-cells").first.wait_for(state="visible", timeout=15000)


def admin_cell(page, label: str):
    return page.locator(".van-cell").filter(has_text=label).first


def check_admin_entries(page, shot_dir: Path, tag: str) -> None:
    for label in ("活动管理", "麦穗管理", "举报管理", "管理后台"):
        cell = admin_cell(page, label)
        try:
            cell.wait_for(state="visible", timeout=10000)
        except PlaywrightTimeoutError as exc:
            raise SmokeFailure(f"{tag}: 我的页缺少「{label}」入口：{exc}") from exc
        title = cell.locator(".van-cell__title")
        text = title.inner_text() if title.count() else cell.inner_text()
        if label not in text:
            raise SmokeFailure(f"{tag}: 「{label}」入口文案不符（{text!r}）")
    admin_cell(page, "举报管理").scroll_into_view_if_needed()
    page.wait_for_timeout(300)
    page.screenshot(path=str(shot_dir / f"{tag}-profile-admin.png"))


def open_points_admin(page, shot_dir: Path, tag: str) -> None:
    admin_cell(page, "麦穗管理").click()
    try:
        page.locator(".pa-title", has_text="麦穗管理").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 麦穗管理页未打开（权限或接口失败）：{exc}") from exc
    if page.get_by_text("麦穗没载出来").count():
        raise SmokeFailure(f"{tag}: 麦穗管理页出现错误态")
    stats = page.locator(".pa-stats .pa-stat")
    if stats.count() != 4:
        raise SmokeFailure(f"{tag}: 统计卡片数量 = {stats.count()}，want 4")
    page.locator(".pa-total-line").first.wait_for(state="visible", timeout=10000)
    rows = page.locator(".pa-row")
    try:
        rows.first.wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 麦穗用户列表为空（smoke 夹具缺失）：{exc}") from exc
    page.screenshot(path=str(shot_dir / f"{tag}-points-overview.png"))


def open_points_detail(page, shot_dir: Path, tag: str) -> None:
    page.locator(".pa-row").first.click()
    try:
        page.locator(".pa-detail").wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 用户详情弹层未打开：{exc}") from exc
    page.locator(".pa-detail-actions").first.wait_for(state="visible", timeout=10000)
    page.get_by_role("button", name="关闭").click()
    page.locator(".pa-detail").wait_for(state="hidden", timeout=10000)
    page.screenshot(path=str(shot_dir / f"{tag}-points-overview-back.png"))


def check_points_tabs(page, shot_dir: Path, tag: str) -> None:
    for label in POINTS_TABS:
        tab = page.locator(".van-tab").filter(has_text=label).first
        try:
            tab.click(timeout=5000)
            page.locator(".van-tab__panel:visible .pa-row").first.wait_for(
                state="visible", timeout=15000)
        except PlaywrightTimeoutError as exc:
            raise SmokeFailure(f"{tag}: 麦穗「{label}」tab 无数据行：{exc}") from exc
        page.screenshot(path=str(shot_dir / f"{tag}-points-{label}.png"))


def back_to_profile(page, tag: str) -> None:
    page.locator(".back-nav").first.click()
    try:
        admin_cell(page, "麦穗管理").wait_for(state="visible", timeout=10000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 从麦穗管理返回我的失败：{exc}") from exc


def open_reported(page, shot_dir: Path, tag: str) -> None:
    admin_cell(page, "举报管理").click()
    try:
        page.locator(".reported-msg-content, .van-empty").first.wait_for(state="visible", timeout=15000)
    except PlaywrightTimeoutError as exc:
        raise SmokeFailure(f"{tag}: 举报管理页未载入：{exc}") from exc
    cards = page.locator(".msg-card")
    if cards.count() == 0:
        raise SmokeFailure(f"{tag}: 举报列表为空（smoke 夹具缺少待处理举报）")
    if page.get_by_text("待处理").count() == 0:
        raise SmokeFailure(f"{tag}: 举报列表没有待处理项")
    for label in ("同意", "驳回"):
        button = page.get_by_role("button", name=label)
        if button.count() == 0 or not button.first.is_visible():
            raise SmokeFailure(f"{tag}: 待处理举报缺少「{label}」按钮")
    page.screenshot(path=str(shot_dir / f"{tag}-reported-messages.png"))


def check_plain_user(page, base: str, shot_dir: Path, tag: str) -> None:
    wait_profile(page, tag)
    if page.locator(".section-title").filter(has_text="管理").count() != 0:
        raise SmokeFailure(f"{tag}: 普通用户我的页出现了「管理」区")
    for label in ("麦穗管理", "举报管理", "活动管理"):
        if page.locator(".van-cell").filter(has_text=label).count() != 0:
            raise SmokeFailure(f"{tag}: 普通用户看到了「{label}」入口")
    page.screenshot(path=str(shot_dir / f"{tag}-profile-plain.png"))

    # 用 context.request 而不是页面内 fetch：同样携带 session cookie，
    # 但故意的 403 不会被 Chrome 记成页面 console error。
    request = page.context.request
    for path in FORBIDDEN_PATHS:
        resp = request.get(base + path)
        status = resp.status
        body = {}
        try:
            body = resp.json()
        except Exception:  # noqa: BLE001 - 非 JSON 也会在下面判失败
            body = {}
        if status != 403:
            raise SmokeFailure(f"{tag}: 普通用户 {path} status = {status}，want 403")
        if not body.get("error"):
            raise SmokeFailure(f"{tag}: 普通用户 {path} 403 响应缺少 error 字段")


def run_admin_viewport(browser, base: str, width: int, height: int, cookie: str,
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
        wait_profile(page, tag)
        check_admin_entries(page, shot_dir, tag)
        open_points_admin(page, shot_dir, tag)
        open_points_detail(page, shot_dir, tag)
        check_points_tabs(page, shot_dir, tag)
        back_to_profile(page, tag)
        open_reported(page, shot_dir, tag)
        guard.assert_clean(f"{tag} 麦穗/举报管理")
        print(f"[ok] {width}x{height} 我的→麦穗管理（总览/详情/流水/兑换/邀请）→举报管理")
    finally:
        context.close()


def run_plain_viewport(browser, base: str, width: int, height: int, cookie: str,
                       cookie_name: str, shot_dir: Path) -> None:
    tag = f"{width}-plain"
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    add_session_cookie(context, base, cookie, cookie_name)
    page = context.new_page()
    guard = ConsoleGuard(page)
    try:
        resp = page.goto(base + "/", wait_until="domcontentloaded", timeout=30000)
        if resp is None or resp.status != 200:
            raise SmokeFailure(f"{tag}: / status={getattr(resp, 'status', None)}")
        check_plain_user(page, base, shot_dir, tag)
        guard.assert_clean(f"{tag} 普通用户")
        print(f"[ok] {width}x{height} 普通用户无管理入口且接口 403")
    finally:
        context.close()


def main() -> int:
    base = require("BASE_URL").rstrip("/")
    admin_cookie = require("SESSION_COOKIE")
    user_cookie = require("USER_SESSION_COOKIE")
    cookie_name = os.environ.get("SESSION_COOKIE_NAME", "yxq_session").strip() or "yxq_session"
    shot_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/admin-points-report"))
    shot_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for width, height in VIEWPORTS:
                for runner, cookie, kind in (
                    (run_admin_viewport, admin_cookie, "管理员"),
                    (run_plain_viewport, user_cookie, "普通用户"),
                ):
                    try:
                        runner(browser, base, width, height, cookie, cookie_name, shot_dir)
                    except SmokeFailure as exc:
                        failures.append(f"[{kind}] {exc}")
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
