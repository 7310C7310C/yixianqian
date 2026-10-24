#!/usr/bin/env python3
"""管理台 → 原版活动管理（native_admin）真实浏览器验收（Chromium）。

覆盖 390×844 / 768×1024 / 1280×900 三视口：
总览活动入口 → 原版活动列表 → 控制台（模式/签到码/名单/现场签到/编辑）
→ 返回列表 → 返回管理台总览。
另验普通用户访问 native 地址会被退回首页。

数据依赖步骤（「查看未填写名单 / 查看未提交名单」等只在活动模式/数据满足时出现）
缺失时记 NOTE skip 并继续；功能级入口（模式卡、签到码、现场签到、编辑、名单筛选）
仍为硬断言，并区分「加载失败」与「数据为空」。

可对 staging 只读复跑（需自备临时 session cookie）；不部署：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<管理员 yxq_session 原始值> \
    USER_SESSION_COOKIE=<普通用户 yxq_session 原始值> \
    SCREENSHOT_DIR=/tmp/opencode/native-activity-live \
    python3 tools/browser/admin_native_activity_smoke.py
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:18097").rstrip("/")
ADMIN_COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
USER_COOKIE = os.environ.get("USER_SESSION_COOKIE", "").strip()
OUT_DIR = Path(os.environ.get("SCREENSHOT_DIR", ".team/audits/admin-native-activity"))
VIEWPORTS = ((390, 844), (768, 1024), (1280, 900))
NATIVE_PATH = "/app?native_admin=1&p=group_admin"

IGNORED_CONSOLE = ("notifications/stream",)


class Checker:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.notes: list[str] = []

    def check(self, ok: bool, message: str) -> None:
        if not ok:
            self.failures.append(message)

    def note(self, message: str) -> None:
        self.notes.append(message)


def shot(page, tag: str, name: str) -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(OUT_DIR / f"{name}-{tag}.png"), full_page=False)


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


def collect_console(page) -> list[str]:
    errors: list[str] = []

    def on_console(msg) -> None:
        if msg.type != "error":
            return
        if any(token in msg.text for token in IGNORED_CONSOLE):
            return
        errors.append(f"console: {msg.text}")

    page.on("console", on_console)
    page.on("pageerror", lambda exc: errors.append(f"pageerror: {exc}"))
    return errors


def wait_frame_url(page, frame, needle: str, timeout_ms: int = 15000) -> None:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        if needle in frame.url:
            return
        page.wait_for_timeout(100)
    raise AssertionError(f"等待 iframe URL 含 {needle!r} 超时，当前 {frame.url}")


def ga_title(frame) -> str:
    try:
        return frame.evaluate("() => (document.querySelector('.ga-title')?.innerText || '').trim()")
    except Exception:
        return ""


def wait_ga_title(page, frame, needle: str, timeout_ms: int = 15000) -> None:
    deadline = time.time() + timeout_ms / 1000
    last = ""
    while time.time() < deadline:
        last = ga_title(frame)
        if needle in last:
            return
        page.wait_for_timeout(120)
    raise AssertionError(f"等待原版页标题含 {needle!r} 超时，当前 {last!r}")


def no_horizontal_scroll(chk: Checker, page, frame, label: str, tag: str) -> None:
    try:
        metrics = frame.evaluate(
            "() => ({scrollWidth: document.documentElement.scrollWidth, innerWidth: window.innerWidth})"
        )
    except Exception as exc:  # noqa: BLE001 - 报告实际失败即可
        chk.check(False, f"{tag}:{label} 读取 iframe 宽度失败 {exc}")
        return
    chk.check(
        metrics["scrollWidth"] <= metrics["innerWidth"] + 1,
        f"{tag}:{label} iframe 横向滚动 {metrics['scrollWidth']}>{metrics['innerWidth']}",
    )
    outer = page.evaluate("() => [document.documentElement.scrollWidth, window.innerWidth]")
    chk.check(outer[0] <= outer[1] + 1, f"{tag}:{label} 外层横向滚动 {outer[0]}>{outer[1]}")


def wait_detail(frame, timeout_ms: int = 15000) -> None:
    frame.wait_for_selector(".ga-signin-code-field", timeout=timeout_ms)


def wait_roster_ready(frame, timeout_ms: int = 15000) -> None:
    """等待名单页结束 loading：标签页与错误重试二选一出现。

    数据为空时标签页仍会渲染，因此「有标签页」代表功能正常，
    「只有重试按钮」才代表加载失败；超时留给调用方断言报错。
    """
    try:
        frame.wait_for_selector(".ga-roster-tabs, .ga-empty-retry", timeout=timeout_ms)
    except Exception:  # noqa: BLE001 - 由调用方区分功能缺失/数据为空
        pass


def wait_activity_list(page, frame, timeout_ms: int = 25000) -> int:
    """等待活动列表结束 loading，返回渲染出的活动行数。

    结束态可能是行、空态或加载失败；调用方据此区分数据为空与功能/服务异常。
    """
    deadline = time.time() + timeout_ms / 1000
    last = 0
    while time.time() < deadline:
        last = frame.locator(".ga-act-row").count()
        if last:
            return last
        if frame.query_selector(".ga-empty-retry") is not None:
            return 0
        if frame.query_selector(".van-empty") is not None:
            return 0
        page.wait_for_timeout(200)
    return last


def find_admin_frame(page):
    for candidate in page.frames:
        if candidate.url.startswith(BASE_URL + "/admin"):
            return candidate
    return None


def open_admin(page) -> object:
    page.goto(BASE_URL + "/?admin=1", wait_until="load")
    page.wait_for_selector(".embed-overlay", timeout=20000)
    page.wait_for_selector(".embed-frame", timeout=20000)
    page.wait_for_timeout(800)
    frame = find_admin_frame(page)
    if frame is None:
        raise AssertionError("未找到 /admin iframe")
    return frame


def click_frame_text(page, frame, selector: str, text: str, timeout_ms: int = 10000) -> None:
    deadline = time.time() + timeout_ms / 1000
    locator = frame.locator(selector).filter(has_text=text)
    while time.time() < deadline:
        if locator.count():
            try:
                locator.first.click(timeout=3000)
                return
            except Exception:  # noqa: BLE001 - Vant 过场期间可重试
                pass
        page.wait_for_timeout(150)
    raise AssertionError(f"找不到可点击的 {selector}（{text}）")


def click_optional_button(page, frame, labels: tuple[str, ...], timeout_ms: int = 4000) -> str | None:
    """点击第一个存在的按钮；全部不存在时返回 None，由调用方记 skip。

    用于随活动模式/数据才出现的入口（如「查看未填写名单」）：
    不存在只说明当前数据没有该入口，不等同功能缺失。
    """
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        for label in labels:
            locator = frame.locator("button").filter(has_text=label)
            if not locator.count():
                continue
            try:
                locator.first.click(timeout=3000)
                return label
            except Exception:  # noqa: BLE001 - Vant 过场期间可重试
                pass
        page.wait_for_timeout(150)
    return None


def run_viewport(browser, viewport: tuple[int, int], chk: Checker) -> None:
    width, height = viewport
    tag = str(width)
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    add_session_cookie(context, BASE_URL, ADMIN_COOKIE)
    page = context.new_page()
    console_errors = collect_console(page)
    page.on("dialog", lambda dialog: dialog.accept())

    frame = open_admin(page)
    page.wait_for_selector(".embed-frame--ready", timeout=15000)

    # 1) 总览：活动入口是 native 地址；匹配分组与两个入口已移除
    entry = frame.query_selector('.admin-entry[href^="/app?native_admin=1"]')
    chk.check(entry is not None, f"{tag}:总览缺少活动 native 入口")
    chk.check(frame.query_selector('.admin-entry[href="/admin/matching"]') is None,
              f"{tag}:总览仍渲染匹配入口")
    chk.check("分组匹配" not in (frame.content() or ""), f"{tag}:总览仍渲染「分组匹配」")
    chk.check("一对一匹配" not in (frame.content() or ""), f"{tag}:总览仍渲染「一对一匹配」")
    if entry is None:
        context.close()
        return
    shot(page, tag, "01-dashboard")

    # 2) 原版活动列表：无 tabbar、无 App 外壳、无横向滚动
    entry.click()
    wait_frame_url(page, frame, "/app?native_admin=1")
    wait_ga_title(page, frame, "活动管理")
    chk.check(frame.evaluate("() => !!document.querySelector('#app.native-admin')"),
              f"{tag}:native 模式未给 #app 加 native-admin")
    chk.check(frame.query_selector(".van-tabbar") is None, f"{tag}:native 活动列表仍有 tabbar")
    row_count = wait_activity_list(page, frame)
    if row_count == 0:
        if frame.query_selector(".ga-empty-retry") is not None:
            chk.check(False, f"{tag}:活动列表加载失败（非数据为空）")
        else:
            chk.check(False, f"{tag}:活动列表为空（数据为空，非功能缺失；无法验证控制台链路）")
        chk.check(not console_errors, f"{tag}:console/pageerror 非空 {console_errors[:3]}")
        context.close()
        return
    no_horizontal_scroll(chk, page, frame, "活动列表", tag)
    shot(page, tag, "02-native-list")

    # 3) 控制台：模式卡 / 签到码 / 现场签到 / 编辑（功能级硬断言）
    frame.locator(".ga-act-row").first.click()
    wait_detail(frame)
    page.wait_for_timeout(600)
    chk.check(frame.locator(".ga-card-title").filter(has_text="模式").count() > 0,
              f"{tag}:控制台缺少模式卡")
    chk.check(frame.query_selector(".ga-signin-code-field input") is not None,
              f"{tag}:控制台签到码输入框缺失")
    chk.check(frame.locator(".ga-title button").filter(has_text="现场签到").count() > 0,
              f"{tag}:控制台缺少现场签到入口")
    chk.check(frame.locator(".ga-title button").filter(has_text="编辑").count() > 0,
              f"{tag}:控制台缺少编辑入口")
    # 执行区随活动模式变化；四态都渲染不出才是功能缺失。
    group_run = frame.locator("button").filter(has_text="执行分组").count() > 0
    oto_run = frame.locator("button").filter(has_text="执行一对一").count() > 0
    conflict = frame.locator(".ga-card-title").filter(has_text="两个模式都开着").count() > 0
    no_mode = frame.locator(".ga-card-title").filter(has_text="还没有选择模式").count() > 0
    mode = ("group" if group_run else "onetoone" if oto_run
            else "conflict" if conflict else "none" if no_mode else "")
    chk.check(bool(mode), f"{tag}:控制台模式执行区缺失（分组/一对一/冲突/未选均未渲染）")
    if mode:
        chk.note(f"{tag}:活动模式={mode}")
    no_horizontal_scroll(chk, page, frame, "活动控制台", tag)
    shot(page, tag, "03-console")

    # 4) 报名名单 → 返回控制台（入口随模式出现：分组=未填写，一对一=未提交）
    roster_label = click_optional_button(page, frame, ("查看未填写名单", "查看未提交名单"))
    if roster_label is None:
        chk.note(f"{tag}:无「查看未填写/未提交名单」入口（mode={mode or 'unknown'}），跳过报名名单步骤")
    else:
        wait_ga_title(page, frame, "报名名单")
        wait_roster_ready(frame)
        chk.check(frame.query_selector(".ga-roster-tabs") is not None,
                  f"{tag}:报名名单缺少未填/已填切换")
        chk.check(frame.query_selector(".ga-empty-retry") is None,
                  f"{tag}:报名名单加载失败（非数据为空）")
        if frame.locator(".van-empty").count():
            chk.note(f"{tag}:报名名单为空（数据为空，非功能缺失）")
        no_horizontal_scroll(chk, page, frame, "报名名单", tag)
        shot(page, tag, "04-roster")
        frame.query_selector(".back-nav").click()
        page.wait_for_timeout(500)
        wait_detail(frame)
        page.wait_for_timeout(400)

    # 5) 现场签到 → 返回列表（历史塌缩：控制台入口的签到回列表）
    click_frame_text(page, frame, "button", "现场签到")
    wait_ga_title(page, frame, "现场签到")
    wait_roster_ready(frame)
    chk.check(frame.query_selector(".ga-roster-tabs") is not None,
              f"{tag}:现场签到页未渲染名册/筛选")
    chk.check(frame.query_selector(".ga-empty-retry") is None,
              f"{tag}:现场签到名册加载失败（非数据为空）")
    if frame.locator(".van-empty").count():
        chk.note(f"{tag}:现场签到名册为空（数据为空，非功能缺失）")
    no_horizontal_scroll(chk, page, frame, "现场签到", tag)
    shot(page, tag, "05-checkin")
    frame.query_selector(".back-nav").click()
    page.wait_for_timeout(500)
    wait_ga_title(page, frame, "活动管理")

    # 6) 再次进控制台 → 编辑（嵌套 iframe）→ 关闭回控制台
    frame.locator(".ga-act-row").first.click()
    wait_detail(frame)
    page.wait_for_timeout(400)
    click_frame_text(page, frame, "button", "编辑")
    page.wait_for_timeout(800)
    nested = None
    for candidate in page.frames:
        if "/admin/activities/" in candidate.url and "/edit" in candidate.url:
            nested = candidate
            break
    chk.check(nested is not None, f"{tag}:编辑表单 iframe 未打开")
    if nested is not None:
        try:
            nested.wait_for_selector(".admin-page-head", timeout=10000)
            chk.check("编辑活动" in (nested.content() or ""),
                      f"{tag}:编辑表单内容异常")
            shot(page, tag, "06-edit")
        except Exception as exc:  # noqa: BLE001
            chk.check(False, f"{tag}:编辑表单加载失败 {exc}")
        back = frame.query_selector(".embed-back")
        chk.check(back is not None, f"{tag}:编辑浮层缺少返回按钮")
        if back is not None:
            back.click()
            page.wait_for_timeout(500)
            wait_detail(frame)

    # 7) 控制台返回列表，列表返回管理台总览
    frame.query_selector(".back-nav").click()
    page.wait_for_timeout(500)
    wait_ga_title(page, frame, "活动管理")
    frame.query_selector(".back-nav").click()
    page.wait_for_timeout(800)
    wait_frame_url(page, frame, "/admin")
    chk.check(frame.query_selector(".admin-topbar") is not None,
              f"{tag}:native 返回后未回到管理台总览")

    chk.check(not console_errors, f"{tag}:console/pageerror 非空 {console_errors[:3]}")
    context.close()


def run_normal_user(browser, chk: Checker) -> None:
    if not USER_COOKIE:
        chk.note("未提供 USER_SESSION_COOKIE，跳过普通用户退回首页检查")
        return
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    add_session_cookie(context, BASE_URL, USER_COOKIE)
    page = context.new_page()
    errors = collect_console(page)
    page.goto(BASE_URL + NATIVE_PATH, wait_until="load")
    # 退回首页发生在 /legacy/api/home 返回、can_admin 校验之后；慢链路需显式等待。
    try:
        page.wait_for_function("() => !location.search.includes('native_admin=1')", timeout=20000)
    except Exception:  # noqa: BLE001 - 超时后由下面的断言报出真实 URL
        pass
    page.wait_for_timeout(400)
    chk.check("native_admin=1" not in page.url, f"普通用户未被退回首页，URL={page.url}")
    chk.check(page.locator(".ga-page").count() == 0, "普通用户仍渲染了原生活动管理页")
    chk.check(errors == [], f"普通用户退回首页出现 console error {errors[:3]}")
    context.close()


def main() -> int:
    if not ADMIN_COOKIE:
        sys.exit("缺少环境变量 SESSION_COOKIE（管理员 yxq_session 原始值）")
    chk = Checker()
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        try:
            for viewport in VIEWPORTS:
                run_viewport(browser, viewport, chk)
            run_normal_user(browser, chk)
        finally:
            browser.close()
    for note in chk.notes:
        print("NOTE:", note)
    if chk.failures:
        print("FAILURES:")
        for failure in chk.failures:
            print(" -", failure)
        return 1
    print(f"PASS: 3 viewports, screenshots at {OUT_DIR}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
