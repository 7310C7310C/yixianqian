#!/usr/bin/env python3
"""用户端 App 内嵌页 / 未读红点回归（Playwright）。

覆盖：
1. 登录后未读红点显示数字；进入消息 tab 后红点清零；
2. 「修改密码」在同源 iframe 内打开，隐藏 SSR 页头/页脚，返回只关 overlay；
3. 「管理后台」在同源 iframe 内打开，内部导航不改变顶层 URL，返回只关 overlay；
4. SSE 实时未读：页面不刷新，新增通知后 badge 在数秒内出现；
5. SSE 被阻断时 12s 轮询兜底；
6. 无 console error / pageerror；
7. 390×844 与 1280×900 截图。

需要已部署的 staging、一个可登录账号的原始 session cookie，以及能写测试通知的
psql 命令。不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    PSQL_CMD='runuser -u yxq -- psql -d yixianqian_staging -Atc' \
    USER_LEGACY_ID=U-0006 \
    SHOT_DIR=/tmp/opencode/app-smoke \
    python3 tools/browser/app_integration_smoke.py

PSQL_CMD 只用于插入/清理标题以「集成测试通知」开头的测试通知。
"""

from __future__ import annotations

import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
TITLE_PREFIX = "集成测试通知"


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def psql(sql: str) -> str:
    cmd = os.environ.get("PSQL_CMD", "").strip()
    if not cmd:
        return ""
    result = subprocess.run(shlex.split(cmd) + [sql], capture_output=True, text=True, timeout=30)
    if result.returncode != 0:
        raise SmokeFailure(f"psql 失败: {result.stderr.strip()}")
    return result.stdout.strip()


def insert_notification(user_legacy_id: str, title: str) -> None:
    body = "页面无需刷新应看到红点"
    psql(
        "INSERT INTO notifications (user_id, kind, title, body) "
        f"SELECT id, 'system', '{title}', '{body}' FROM users "
        f"WHERE legacy_user_id = '{user_legacy_id}';"
    )


def cleanup_notifications() -> None:
    psql(f"DELETE FROM notifications WHERE title LIKE '{TITLE_PREFIX}%';")


def badge(page):
    item = page.locator(".van-tabbar-item").filter(has_text="消息")
    return item.locator(".van-badge")


def assert_no_console_errors(errors: list[str], context: str) -> None:
    if errors:
        raise SmokeFailure(f"{context} 存在 console error：\n" + "\n".join(errors))


def wait_frame_path(page, base_url: str, path_prefix: str, timeout: int = 15000) -> None:
    page.wait_for_function(
        """([base, prefix]) => {
          const frame = document.querySelector('.embed-frame');
          if (!frame || !frame.contentWindow) return false;
          try {
            const p = frame.contentWindow.location.pathname;
            return p === prefix || p.startsWith(prefix + '/');
          } catch (e) { return false; }
        }""",
        arg=[base_url, path_prefix],
        timeout=timeout,
    )


def frame_for_path(page, path_prefix: str):
    for frame in page.frames:
        if frame == page.main_frame:
            continue
        if path_prefix in frame.url:
            return frame
    raise SmokeFailure(f"找不到 {path_prefix} 对应的 iframe")


def open_embed(page, label: str, path_prefix: str, base_url: str) -> None:
    page.locator(".van-cell").filter(has_text=label).first.click()
    page.locator(".embed-overlay").wait_for(state="visible", timeout=10000)
    wait_frame_path(page, base_url, path_prefix)
    frame = frame_for_path(page, path_prefix)
    frame.locator(".shell-container").first.wait_for(state="visible", timeout=10000)
    display = frame.locator(".shell-header").first.evaluate(
        "el => getComputedStyle(el).display"
    ) if frame.locator(".shell-header").count() else ""
    if display != "none":
        raise SmokeFailure(f"{label}: .shell-header 未隐藏（display={display!r}）")


def close_embed(page) -> None:
    page.locator(".embed-back").click()
    page.locator(".embed-overlay").wait_for(state="detached", timeout=10000)


def run_viewport(browser, width: int, height: int, base_url: str, cookie: str,
                 user_legacy_id: str, out_dir: Path, live_sse: bool) -> list[str]:
    failures: list[str] = []
    tag = str(width)
    titles = [f"{TITLE_PREFIX}-{tag}-A-{int(time.time())}", f"{TITLE_PREFIX}-{tag}-B-{int(time.time())}"]
    for title in titles:
        insert_notification(user_legacy_id, title)

    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    if not live_sse:
        context.route("**/legacy/api/notifications/stream", lambda route: route.abort())
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    page = context.new_page()
    errors: list[str] = []
    page.on("console", lambda msg: errors.append(f"console: {msg.text}") if msg.type == "error" else None)
    page.on("pageerror", lambda exc: errors.append(f"pageerror: {exc}"))

    try:
        page.goto(base_url + "/", wait_until="domcontentloaded")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)

        # 1. 登录后未读红点（页面已存在两条测试通知）
        badge(page).wait_for(state="visible", timeout=10000)
        unread = badge(page).inner_text().strip()
        if not unread.isdigit() or int(unread) < 2:
            raise SmokeFailure(f"{width}: 未读 badge={unread!r}，期望至少 2")
        page.screenshot(path=str(out_dir / f"badge-{tag}.png"))

        # 2. 进入消息 tab：通知可见、红点清零
        page.locator(".van-tabbar-item").filter(has_text="消息").click()
        page.get_by_text(titles[0]).first.wait_for(state="visible", timeout=10000)
        badge(page).wait_for(state="hidden", timeout=10000)

        # 3. 修改密码：iframe 同源、隐藏页头、返回关闭、顶层 URL 不变
        page.locator(".van-tabbar-item").filter(has_text="我的").click()
        page.locator(".van-cell").filter(has_text="修改密码").first.wait_for(state="visible", timeout=10000)
        open_embed(page, "修改密码", "/password", base_url)
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 修改密码改变了顶层 URL {page.url}")
        page.screenshot(path=str(out_dir / f"embed-password-{tag}.png"))
        close_embed(page)

        # 4. 管理后台：内部导航留在 iframe，顶层 URL 不变
        open_embed(page, "管理后台", "/admin", base_url)
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 管理后台改变了顶层 URL {page.url}")
        page.screenshot(path=str(out_dir / f"embed-admin-{tag}.png"))
        admin = frame_for_path(page, "/admin")
        admin.locator('.admin-nav a[href="/admin/users"]').first.click()
        wait_frame_path(page, base_url, "/admin/users")
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 管理台内部导航改变了顶层 URL {page.url}")
        page.screenshot(path=str(out_dir / f"embed-admin-users-{tag}.png"))
        close_embed(page)

        # 5. SSE 实时未读：不刷新页面，新增通知后 badge 出现
        page.locator(".van-tabbar-item").filter(has_text="我的").click()
        live_title = f"{TITLE_PREFIX}-{tag}-LIVE-{int(time.time())}"
        insert_notification(user_legacy_id, live_title)
        badge(page).wait_for(state="visible", timeout=10000)
        if not badge(page).inner_text().strip().isdigit():
            raise SmokeFailure(f"{width}: SSE 后 badge 不是数字")
        page.screenshot(path=str(out_dir / f"badge-sse-{tag}.png"))
        page.locator(".van-tabbar-item").filter(has_text="消息").click()
        page.get_by_text(live_title).first.wait_for(state="visible", timeout=10000)
        badge(page).wait_for(state="hidden", timeout=10000)

        assert_no_console_errors(
            [e for e in errors if "/legacy/api/notifications/stream" not in e],
            f"{width}px",
        )
        return failures
    except Exception as exc:  # noqa: BLE001 - report all viewport failures together
        failures.append(f"{width}px: {exc}")
        return failures
    finally:
        context.close()


def run_polling_fallback(browser, base_url: str, cookie: str, user_legacy_id: str,
                         out_dir: Path) -> list[str]:
    """SSE 被阻断时，12s 轮询仍应更新红点。"""
    title = f"{TITLE_PREFIX}-poll-{int(time.time())}"
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    context.route("**/legacy/api/notifications/stream", lambda route: route.abort())
    context.add_cookies([{
        "name": "yxq_session", "value": cookie, "url": base_url,
        "httpOnly": True, "sameSite": "Lax",
    }])
    page = context.new_page()
    errors: list[str] = []
    page.on("console", lambda msg: errors.append(msg.text) if msg.type == "error" else None)
    page.on("pageerror", lambda exc: errors.append(str(exc)))
    try:
        page.goto(base_url + "/", wait_until="domcontentloaded")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        # 先清零现有未读：进消息 tab 再回来
        page.locator(".van-tabbar-item").filter(has_text="消息").click()
        badge(page).wait_for(state="hidden", timeout=15000)
        page.locator(".van-tabbar-item").filter(has_text="我的").click()
        insert_notification(user_legacy_id, title)
        badge(page).wait_for(state="visible", timeout=20000)
        page.screenshot(path=str(out_dir / "badge-poll-390.png"))
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"poll fallback: {exc}"]
    finally:
        context.close()


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    user_legacy_id = os.environ.get("USER_LEGACY_ID", "U-0006").strip()
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/app-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)
    failures: list[str] = []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            for width, height in VIEWPORTS:
                failures.extend(run_viewport(
                    browser, width, height, base_url, cookie, user_legacy_id, out_dir,
                    live_sse=True,
                ))
            failures.extend(run_polling_fallback(browser, base_url, cookie, user_legacy_id, out_dir))
            browser.close()
    finally:
        cleanup_notifications()
    if failures:
        print("FAIL")
        for item in failures:
            print(" -", item)
        return 1
    print("PASS")
    print(f"screenshots: {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
