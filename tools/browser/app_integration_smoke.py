#!/usr/bin/env python3
"""用户端 App 启动骨架 / 改密子页 / 管理台内嵌回归（Playwright）。

覆盖：
1. 未登录访问 / 渲染公开首页（200，含注册/登录 CTA 与飞书说明，不含二维码图片），点登录进入 /login 后出现注册入口；
2. 已登录刷新：home 接口被挂起时先出现 App 骨架（品牌顶栏 + 卡片 shimmer +
   四 tab 占位），没有「正在登录」；接口放行后进入主界面，普通 reload 同样不闪登录页；
3. 登录后未读红点显示数字；进入消息 tab 后红点清零；
4. 「修改密码」是 App 内 Vant 子页（不是 iframe）：错误旧密码显示「目前密码不正确」
   且停留在子页，返回落在「我的」；设置 PASSWORD_TEST_OLD/PASSWORD_TEST_NEW 时
   额外验证成功改密与改回，避免污染账号；
5. 「管理后台」仍是同源 iframe：首次加载与内部导航显示管理台骨架、隐藏 SSR 页头/
   页脚、注入旧 App 底色/字体/token，内部导航不改变顶层 URL；返回只关 overlay；
6. 返回栏 `.embed-bar` 对齐旧 `.back-nav`（高度差 ≤2px，title 18px）；
7. SSE 实时未读：页面不刷新，新增通知后 badge 在数秒内出现；SSE 被阻断时
   12s 轮询兜底；
8. 无 console error / pageerror（忽略 SSE 阻断与预期 400 改密响应）；
9. 390×844 与 1280×900 截图。

需要已部署的 staging、一个可登录账号的原始 session cookie，以及能写测试通知的
psql 命令。不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<yxq_session 原始值> \
    PSQL_CMD='runuser -u yxq -- psql -d yixianqian_staging -Atc' \
    USER_LEGACY_ID=U-0006 \
    SHOT_DIR=/tmp/opencode/app-smoke \
    python3 tools/browser/app_integration_smoke.py

可选成功改密验证（同一账号会短暂改成新密码再改回）：

    PASSWORD_TEST_OLD='<当前密码>' PASSWORD_TEST_NEW='<至少 8 位的新密码>' \
    BASE_URL=... SESSION_COOKIE=... PSQL_CMD=... \
    python3 tools/browser/app_integration_smoke.py

PSQL_CMD 只用于插入/清理标题以「集成测试通知」开头的测试通知。
"""

from __future__ import annotations

import json
import os
import re
import shlex
import subprocess
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
TITLE_PREFIX = "集成测试通知"


class SmokeFailure(AssertionError):
    pass


class HeldRoute:
    """挂起首个匹配请求，让测试先断言骨架，再显式放行。

    比在 route handler 里 sleep 更稳定：sleep 会阻塞 Playwright 的事件分发，
    页面实际已经加载完才轮到断言，骨架就看不到了。
    """

    def __init__(self) -> None:
        self._held = None
        self._used = False

    def __call__(self, route) -> None:
        if not self._used:
            self._used = True
            self._held = route
            return
        route.continue_()

    def release(self) -> None:
        if self._held is None:
            raise SmokeFailure("请求未被挂起，无法放行")
        route, self._held = self._held, None
        route.continue_()

    def release_if_held(self) -> None:
        if self._held is not None:
            self.release()


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
    """插入测试通知并显式 pg_notify。

    批次7 后端把 SSE 从「每 4s 查询」改成 `pg_notify('yxq_notify', user_id)` 唤醒
    + 15/30s 兜底 ticker；psql 直接 INSERT 不会再被 4s 轮询捡到，必须自己发一次
    notify，否则这条「页面不刷新就出红点」的断言会等不到（实测 >10s 才靠 ticker）。
    """
    body = "页面无需刷新应看到红点"
    psql(
        "INSERT INTO notifications (user_id, kind, title, body) "
        f"SELECT id, 'system', '{title}', '{body}' FROM users "
        f"WHERE legacy_user_id = '{user_legacy_id}';"
        "SELECT pg_notify('yxq_notify', id::text) FROM users "
        f"WHERE legacy_user_id = '{user_legacy_id}';"
    )


def cleanup_notifications() -> None:
    psql(f"DELETE FROM notifications WHERE title LIKE '{TITLE_PREFIX}%';")


def badge(page):
    item = page.locator(".van-tabbar-item").filter(has_text="消息")
    return item.locator(".van-badge")


def dismiss_guides(page) -> None:
    """关掉首次进入的滑动引导/活动/更新说明等一次性弹层。

    1280 下部分 dialog 会在 reload 后晚 1s 左右才出现；先等一轮，再循环最多
    6 次处理接续弹出的引导，避免后续点击被 overlay 拦住。
    """
    page.wait_for_timeout(800)
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
    """点击底部 tab；晚到的小白引导可能拦截点击，带重试与遮罩诊断。"""
    item = page.locator(".van-tabbar-item").filter(has_text=label)
    last_error = None
    for _ in range(4):
        dismiss_guides(page)
        try:
            item.click(timeout=4000)
            return
        except PlaywrightTimeoutError as exc:
            last_error = exc
            dismiss_guides(page)
            page.keyboard.press("Escape")
            page.wait_for_timeout(400)
    detail = []
    overlays = page.locator(".van-overlay")
    for i in range(min(overlays.count(), 3)):
        try:
            if overlays.nth(i).is_visible():
                detail.append(
                    f"class={overlays.nth(i).get_attribute('class')!r} "
                    f"text={overlays.nth(i).inner_text().strip()[:120]!r}"
                )
        except Exception:  # noqa: BLE001 - diagnostic only
            pass
    raise SmokeFailure(f"无法点击 tab {label}（4 次尝试）：{last_error}; overlays={detail or '无'}")


def attach_error_capture(page) -> list[dict[str, str]]:
    errors: list[dict[str, str]] = []

    def on_console(msg):
        if msg.type != "error":
            return
        loc = msg.location or {}
        errors.append({"kind": "console", "text": msg.text, "url": loc.get("url", "")})

    def on_pageerror(exc):
        errors.append({"kind": "pageerror", "text": str(exc), "url": ""})

    page.on("console", on_console)
    page.on("pageerror", on_pageerror)
    return errors


def assert_no_console_errors(errors: list[dict[str, str]], context: str) -> None:
    real: list[str] = []
    for item in errors:
        url = item.get("url", "")
        text = item.get("text", "")
        if "legacy/api/notifications/stream" in url or "legacy/api/notifications/stream" in text:
            continue
        # 改密错误路径的 400 是预期交互，浏览器仍会记一条网络 console error。
        if (
            item.get("kind") == "console"
            and url.rstrip("/").endswith("/legacy/api/password")
            and "status of 400" in text
        ):
            continue
        real.append(f"{item.get('kind')}: {text}" + (f" ({url})" if url else ""))
    if real:
        raise SmokeFailure(f"{context} 存在 console error：\n" + "\n".join(real))


def assert_no_login_flash(page, context: str) -> None:
    """刷新后不得出现**可见**的登录页。

    注意：`#app` 里是壳模板，Vue 挂载前 `v-cloak` 会把整棵树隐藏，但 DOM 里
    仍然有 `<div v-if="loggingIn" class="login-page">`。用 count() 判会把它当成
    「出现登录页」——批次7 集成排查确认这是假红（挂载后 159ms 内消失，且
    `window.__YXQ_AUTHENTICATED__ === true`）。所以只看可见性。
    """
    body = page.locator("body").inner_text()
    if "正在登录" in body:
        raise SmokeFailure(f"{context}: 页面出现「正在登录」")
    if page.locator(".login-page:visible").count():
        raise SmokeFailure(f"{context}: 出现可见的 SPA 登录页 .login-page")


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


def open_password_page(page) -> None:
    dismiss_guides(page)
    page.locator(".van-cell").filter(has_text="修改密码").first.click()
    page.locator(".password-page").wait_for(state="visible", timeout=10000)
    if page.locator(".embed-overlay").count():
        raise SmokeFailure("修改密码不应再打开 embed overlay")
    if page.locator("iframe[src*='/password']").count():
        raise SmokeFailure("修改密码不应再使用 /password iframe")


def fill_password_form(page, current: str, new: str) -> None:
    fields = page.locator(".password-page .van-field input")
    if fields.count() != 3:
        raise SmokeFailure(f"改密子页应有 3 个密码输入框，实际 {fields.count()}")
    fields.nth(0).fill(current)
    fields.nth(1).fill(new)
    fields.nth(2).fill(new)


def submit_password_form(page) -> None:
    page.locator(".password-page button").filter(has_text="确认修改").click()


def close_password_page(page) -> None:
    page.locator(".password-page .back-nav").click()
    page.locator(".password-page").wait_for(state="detached", timeout=10000)
    page.locator(".van-tabbar").wait_for(state="visible", timeout=10000)


def change_password(page, current: str, new: str) -> None:
    open_password_page(page)
    fill_password_form(page, current, new)
    submit_password_form(page)
    page.get_by_text("密码已更新").first.wait_for(state="visible", timeout=10000)
    page.locator(".password-page").wait_for(state="detached", timeout=10000)
    page.locator(".van-tabbar").wait_for(state="visible", timeout=10000)
    page.locator(".van-toast").wait_for(state="hidden", timeout=10000)


def run_anonymous(browser, width: int, height: int, base_url: str, out_dir: Path) -> list[str]:
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    page = context.new_page()
    errors = attach_error_capture(page)
    tag = str(width)
    try:
        resp = page.goto(base_url + "/", wait_until="domcontentloaded")
        if resp is None or resp.status != 200:
            raise SmokeFailure(f"{width}px 未登录: / status={getattr(resp, 'status', None)}")
        if page.url.rstrip("/") != base_url.rstrip("/"):
            raise SmokeFailure(f"{width}px 未登录: 被重定向到 {page.url}")
        page.locator(".home-hero").wait_for(state="visible", timeout=10000)
        register = page.locator('.home-hero a[href="/register?from=home"]')
        register.wait_for(state="visible", timeout=10000)
        register_class = register.get_attribute("class") or ""
        if "primary" not in register_class or "block" not in register_class:
            raise SmokeFailure(f"{width}px 未登录: 注册按钮不是 primary block（{register_class}）")
        page.locator('.home-hero a[href="/login"]').wait_for(state="visible", timeout=10000)
        feishu = page.locator('.home-feishu a[href="https://applink.feishu.cn/T996FhMkTERp"]')
        feishu.wait_for(state="visible", timeout=10000)
        if feishu.get_attribute("target") != "_blank":
            raise SmokeFailure(f"{width}px 未登录: 飞书链接缺少 target=_blank")
        if "noopener" not in (feishu.get_attribute("rel") or ""):
            raise SmokeFailure(f"{width}px 未登录: 飞书链接缺少 rel=noopener")
        if page.locator(".home-qr img").count() or "feishu-qr" in page.content():
            raise SmokeFailure(f"{width}px 未登录: 公开首页不应再有二维码图片")
        assert_no_login_flash(page, f"{width}px 未登录")
        page.screenshot(path=str(out_dir / f"home-anon-{tag}.png"), full_page=True)

        page.locator('.home-hero a[href="/login"]').click()
        page.wait_for_url(lambda url: "/login" in url, timeout=10000)
        login_register = page.locator('a[href="/register"]')
        login_register.wait_for(state="visible", timeout=10000)
        login_class = login_register.get_attribute("class") or ""
        if "primary" not in login_class or "block" not in login_class:
            raise SmokeFailure(f"{width}px 登录页: 注册按钮不是 primary block（{login_class}）")
        page.get_by_text("已在飞书版使用过", exact=False).first.wait_for(state="visible", timeout=10000)
        page.screenshot(path=str(out_dir / f"login-anon-{tag}.png"), full_page=True)
        assert_no_console_errors(errors, f"{width}px 未登录")
        return []
    except Exception as exc:  # noqa: BLE001 - report all viewport failures together
        return [f"{width}px 未登录: {exc}"]
    finally:
        context.close()


def run_viewport(browser, width: int, height: int, base_url: str, cookie: str,
                 user_legacy_id: str, out_dir: Path, live_sse: bool,
                 old_password: str = "", new_password: str = "") -> list[str]:
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
    errors = attach_error_capture(page)

    home_route = HeldRoute()
    admin_route = HeldRoute()
    users_route = HeldRoute()
    context.route("**/legacy/api/home", home_route)
    # iframe 首载 URL 带 ?embed=1：glob 会连查询串一起匹配，`**/admin` 匹配不到，
    # 之前只是靠整页加载慢才偶尔挂住骨架。这里用带查询串/前缀的模式。
    context.route("**/admin?embed=1", admin_route)
    context.route("**/admin/users*", users_route)

    top_url = base_url.rstrip("/") + "/"
    try:
        # 1. 已登录刷新：home 挂起时先显示 App 骨架，不出现登录页/「正在登录」。
        page.goto(base_url + "/", wait_until="domcontentloaded")
        if not page.evaluate("window.__YXQ_AUTHENTICATED__ === true"):
            raise SmokeFailure("window.__YXQ_AUTHENTICATED__ 不是 true")
        if page.evaluate("!!window.__YXQ_BOOTSTRAP__"):
            # 批次4：服务端已注入首页数据，不再等 /legacy/api/home，也不应出现骨架。
            page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
            if page.locator(".app-boot-skeleton").count() and page.locator(".app-boot-skeleton").is_visible():
                raise SmokeFailure(f"{width}px: 有 bootstrap 时仍显示启动骨架")
            home_route.release_if_held()
            assert_no_login_flash(page, f"{width}px 已登录 bootstrap")
        else:
            page.locator(".app-boot-skeleton").wait_for(state="visible", timeout=10000)
            assert_no_login_flash(page, f"{width}px 已登录骨架")
            home_route.release()
            page.locator(".app-boot-skeleton").wait_for(state="hidden", timeout=20000)
            page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        dismiss_guides(page)
        page.screenshot(path=str(out_dir / f"app-home-{tag}.png"))

        # 2. 普通刷新：同样不闪登录页。
        page.reload(wait_until="domcontentloaded")
        assert_no_login_flash(page, f"{width}px 刷新")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        dismiss_guides(page)

        # 3. 登录后未读红点（页面已存在两条测试通知）
        # 应用写路径即时 notify，正常应 <1s；保留 10s 余量但不依赖 12s 客户端轮询，
        # 否则 SSE 完全失效也会绿（reviewer N6）。
        badge(page).wait_for(state="visible", timeout=10000)
        unread = badge(page).inner_text().strip()
        if not unread.isdigit() or int(unread) < 2:
            raise SmokeFailure(f"{width}: 未读 badge={unread!r}，期望至少 2")
        page.screenshot(path=str(out_dir / f"badge-{tag}.png"))

        # 4. 进入消息 tab：通知可见、红点清零
        tap_tab(page, "消息")
        page.get_by_text(titles[0]).first.wait_for(state="visible", timeout=10000)
        badge(page).wait_for(state="hidden", timeout=10000)

        # 5. 修改密码：App 内子页；错误旧密码留在子页，返回落回「我的」。
        tap_tab(page, "我的")
        page.locator(".van-cell").filter(has_text="修改密码").first.wait_for(state="visible", timeout=10000)
        open_password_page(page)
        if page.url.rstrip("/") != top_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 修改密码改变了顶层 URL {page.url}")
        fill_password_form(page, "definitely-wrong-old-password", "valid-new-password-2026")
        submit_password_form(page)
        page.locator(".password-page").get_by_text("目前密码不正确").first.wait_for(
            state="visible", timeout=10000)
        if not page.locator(".password-page").is_visible():
            raise SmokeFailure(f"{width}: 旧密码错误后离开了改密子页")
        if page.url.rstrip("/") != top_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 改密失败后改变了顶层 URL {page.url}")
        page.screenshot(path=str(out_dir / f"password-{tag}.png"))
        close_password_page(page)

        # 5b. 可选成功路径：改成 PASSWORD_TEST_NEW，再用新密码改回，避免污染账号。
        if old_password and new_password:
            change_password(page, old_password, new_password)
            change_password(page, new_password, old_password)

        # 6. 旧子页返回栏高度，用于和管理台 embed-bar 对比。
        back_nav_height = None
        page.locator(".van-cell").filter(has_text="我的活动").first.click()
        page.locator(".back-nav").first.wait_for(state="visible", timeout=10000)
        box = page.locator(".back-nav").first.bounding_box()
        if box:
            back_nav_height = box["height"]
        page.locator(".back-nav").first.click()
        page.locator(".van-tabbar").wait_for(state="visible", timeout=10000)

        # 7. 管理后台：首次加载与内部导航都先骨架、隐藏 SSR 外壳、顶层 URL 不变。
        dismiss_guides(page)
        page.locator(".van-cell").filter(has_text="管理后台").first.click()
        page.locator(".embed-overlay").wait_for(state="visible", timeout=10000)
        page.locator(".embed-loading .embed-skeleton").wait_for(state="visible", timeout=10000)
        if page.locator(".embed-frame--ready").count():
            raise SmokeFailure(f"{width}: iframe 在骨架阶段已 ready")
        admin_route.release()
        page.locator(".embed-loading").wait_for(state="hidden", timeout=15000)
        page.locator(".embed-frame--ready").wait_for(state="visible", timeout=10000)
        wait_frame_path(page, base_url, "/admin")
        admin = frame_for_path(page, "/admin")
        admin.locator(".admin-topbar").first.wait_for(state="visible", timeout=10000)
        display = admin.locator(".shell-header").first.evaluate(
            "el => getComputedStyle(el).display"
        ) if admin.locator(".shell-header").count() else ""
        if display != "none":
            raise SmokeFailure(f"{width}: 管理台 .shell-header 未隐藏（display={display!r}）")
        admin_bg = admin.locator("body").evaluate("el => getComputedStyle(el).backgroundColor")
        if admin_bg != "rgb(247, 248, 250)":
            raise SmokeFailure(f"{width}: 管理台 body 背景 {admin_bg!r}，期望 rgb(247, 248, 250)")
        admin_font = admin.locator("body").evaluate("el => getComputedStyle(el).fontFamily")
        if not any(tok in admin_font for tok in ("-apple-system", "BlinkMacSystemFont", "system-ui", "Segoe UI")):
            raise SmokeFailure(f"{width}: 管理台字体未套用 system 字族：{admin_font!r}")
        if page.url.rstrip("/") != top_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 管理后台改变了顶层 URL {page.url}")
        title_size = page.locator(".embed-title").evaluate("el => getComputedStyle(el).fontSize")
        if title_size != "18px":
            raise SmokeFailure(f"{width}: embed-title 字号 {title_size!r}，期望 18px")
        bar_box = page.locator(".embed-bar").bounding_box()
        if not bar_box or not (50 <= bar_box["height"] <= 56):
            raise SmokeFailure(f"{width}: embed-bar 高度异常 {bar_box and bar_box['height']}")
        page.screenshot(path=str(out_dir / f"admin-{tag}.png"))

        # 管理台内部导航：beforeunload 盖骨架，新页加载后继续隐藏外壳。
        admin.locator('a[href^="/admin/users"]').first.click()
        page.locator(".embed-loading .embed-skeleton").wait_for(state="visible", timeout=10000)
        if not page.locator(".embed-overlay").is_visible():
            raise SmokeFailure(f"{width}: 管理台内部导航时 overlay 消失了")
        users_route.release()
        page.locator(".embed-loading").wait_for(state="hidden", timeout=15000)
        wait_frame_path(page, base_url, "/admin/users")
        users = frame_for_path(page, "/admin/users")
        users.locator(".admin-topbar").first.wait_for(state="visible", timeout=10000)
        users_display = users.locator(".shell-header").first.evaluate(
            "el => getComputedStyle(el).display"
        ) if users.locator(".shell-header").count() else ""
        if users_display != "none":
            raise SmokeFailure(f"{width}: 内部导航后 .shell-header 未隐藏（display={users_display!r}）")
        if page.url.rstrip("/") != top_url.rstrip("/"):
            raise SmokeFailure(f"{width}: 管理台内部导航改变了顶层 URL {page.url}")
        page.screenshot(path=str(out_dir / f"admin-users-{tag}.png"))

        if back_nav_height is not None and abs(bar_box["height"] - back_nav_height) > 2:
            raise SmokeFailure(
                f"{width}: embed-bar 高 {bar_box['height']}px 与 back-nav {back_nav_height}px 相差超过 2px"
            )
        page.locator(".embed-back").click()
        page.locator(".embed-overlay").wait_for(state="detached", timeout=10000)

        # 8. SSE 实时未读：不刷新页面，新增通知后 badge 出现
        tap_tab(page, "我的")
        live_title = f"{TITLE_PREFIX}-{tag}-LIVE-{int(time.time())}"
        insert_notification(user_legacy_id, live_title)
        badge(page).wait_for(state="visible", timeout=10000)
        if not badge(page).inner_text().strip().isdigit():
            raise SmokeFailure(f"{width}: SSE 后 badge 不是数字")
        page.screenshot(path=str(out_dir / f"badge-sse-{tag}.png"))
        tap_tab(page, "消息")
        page.get_by_text(live_title).first.wait_for(state="visible", timeout=10000)
        badge(page).wait_for(state="hidden", timeout=10000)

        assert_no_console_errors(errors, f"{width}px")
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
    errors = attach_error_capture(page)
    try:
        page.goto(base_url + "/", wait_until="domcontentloaded")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        assert_no_login_flash(page, "poll fallback")
        dismiss_guides(page)
        # 先清零现有未读：进消息 tab 再回来
        tap_tab(page, "消息")
        badge(page).wait_for(state="hidden", timeout=15000)
        tap_tab(page, "我的")
        insert_notification(user_legacy_id, title)
        badge(page).wait_for(state="visible", timeout=20000)
        page.screenshot(path=str(out_dir / "badge-poll-390.png"))
        assert_no_console_errors(errors, "poll fallback")
        return []
    except Exception as exc:  # noqa: BLE001
        return [f"poll fallback: {exc}"]
    finally:
        context.close()


def run_bootstrap_fallback(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    """故障注入：强制壳不带 bootstrap，验证骨架→数据回退这条路径仍有回归覆盖。

    带 bootstrap 时页面直达 tabbar（批次4），骨架路径平时不会被走到；
    这里把文档里的 __YXQ_BOOTSTRAP__ 改写成 null 并挂起 /legacy/api/home，
    断言先出现骨架、放行后骨架消失且 tabbar 可见。
    """
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    page = context.new_page()
    errors = attach_error_capture(page)
    home_route = HeldRoute()

    def strip_bootstrap(route) -> None:
        response = route.fetch()
        body = re.sub(
            r"window\.__YXQ_BOOTSTRAP__=\{.*?\};",
            "window.__YXQ_BOOTSTRAP__=null;",
            response.text(),
            count=1,
            flags=re.S,
        )
        route.fulfill(response=response, body=body)

    context.route(base_url.rstrip("/") + "/", strip_bootstrap)
    context.route("**/legacy/api/home", home_route)
    try:
        page.goto(base_url.rstrip("/") + "/", wait_until="domcontentloaded")
        if page.evaluate("!!window.__YXQ_BOOTSTRAP__"):
            raise SmokeFailure("故障注入失败：壳里仍是完整 bootstrap")
        page.locator(".app-boot-skeleton").wait_for(state="visible", timeout=10000)
        home_route.release()
        page.locator(".app-boot-skeleton").wait_for(state="hidden", timeout=20000)
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        page.screenshot(path=str(out_dir / "app-bootstrap-fallback-390.png"))
        assert_no_console_errors(errors, "390px bootstrap 回退")
        return []
    except Exception as exc:  # noqa: BLE001 - report every failure
        return [f"390px bootstrap 回退: {exc}"]
    finally:
        context.close()


def _boot_failure_case(browser, base_url: str, cookie: str, out_dir: Path,
                       abort_pattern: str, label: str, shot: str) -> list[str]:
    """中断一个关键脚本，断言显示 .boot-fail 且没有 pageerror（不是白屏、不是崩栈）。"""
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    context.route(abort_pattern, lambda route: route.abort())
    page = context.new_page()
    pageerrors: list[str] = []
    page.on("pageerror", lambda exc: pageerrors.append(str(exc)))
    try:
        page.goto(base_url.rstrip("/") + "/", wait_until="domcontentloaded")
        page.locator(".boot-fail").wait_for(state="visible", timeout=10000)
        if "页面加载失败" not in page.locator(".boot-fail-title").inner_text():
            raise SmokeFailure("错误页标题缺失")
        if not page.locator(".boot-fail-detail").inner_text().strip():
            raise SmokeFailure("错误页没有可诊断详情")
        if pageerrors:
            raise SmokeFailure(f"出现 pageerror: {pageerrors[:2]}")
        page.screenshot(path=str(out_dir / shot))
        return []
    except Exception as exc:  # noqa: BLE001 - report every failure
        return [f"390px {label}: {exc}"]
    finally:
        context.close()


def run_render_missing(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    """runtime-only Vue 的两种缺失：渲染产物没了、运行库没了，都必须可见报错且不崩栈。"""
    failures = _boot_failure_case(
        browser, base_url, cookie, out_dir,
        "**/static/legacy/app.render.js*", "渲染器缺失兜底", "app-render-missing-390.png",
    )
    failures.extend(_boot_failure_case(
        browser, base_url, cookie, out_dir,
        "**/static/legacy/lib/vue.global.prod.js*", "Vue 运行库缺失兜底", "app-vue-missing-390.png",
    ))
    failures.extend(_boot_failure_case(
        browser, base_url, cookie, out_dir,
        "**/static/legacy/lib/vant.min.js*", "Vant 组件库缺失兜底", "app-vant-missing-390.png",
    ))
    return failures


def run_mutual_wechat_prefill(browser, base_url: str, cookie: str, out_dir: Path) -> list[str]:
    """互喜页微信号预填/填写回归：数据源必须是 /legacy/api/likes/me（no-store）。

    批次4 起壳内 bootstrap 不再携带 my_wechat/their_wechat（PII 落盘口径），
    所以这里把 likes 接口替换成固定 payload，验证「进入互喜页仍能显示并填写微信号」。
    """
    context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    payload = {
        "liked_me": [],
        "mutual": [{
            "initiator_openid": "U-PREFILL-1",
            "nickname": "预填检查",
            "my_wechat": "wx-prefill-check",
            "their_wechat": "",
            "wechat_required": True,
            "wechat_status": "need_wechat",
            "waiting_other": False,
            "message": "",
        }],
    }
    context.route(
        "**/legacy/api/likes/me",
        lambda route: route.fulfill(
            status=200, content_type="application/json", body=json.dumps(payload)
        ),
    )
    page = context.new_page()
    errors = attach_error_capture(page)
    try:
        page.goto(base_url.rstrip("/") + "/", wait_until="domcontentloaded")
        page.locator(".van-tabbar").wait_for(state="visible", timeout=20000)
        tap_tab(page, "消息")
        row = page.locator(".msg-card").filter(has_text="相互喜欢").first
        row.wait_for(state="visible", timeout=15000)
        row.click()
        field = page.locator("input[placeholder='请输入微信号']")
        field.wait_for(state="visible", timeout=15000)
        prefilled = field.input_value()
        if prefilled != "wx-prefill-check":
            raise SmokeFailure(f"微信号预填错误 {prefilled!r}（应来自 /legacy/api/likes/me）")
        field.fill("wx-typed-check")
        if field.input_value() != "wx-typed-check":
            raise SmokeFailure("互喜页微信号无法填写")
        page.screenshot(path=str(out_dir / "mutual-wechat-prefill-390.png"))
        return []
    except Exception as exc:  # noqa: BLE001 - report every failure
        return [f"390px 互喜微信号预填: {exc}"]
    finally:
        context.close()


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    user_legacy_id = os.environ.get("USER_LEGACY_ID", "U-0006").strip()
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/app-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)
    old_password = os.environ.get("PASSWORD_TEST_OLD", "")
    new_password = os.environ.get("PASSWORD_TEST_NEW", "")
    if bool(old_password) != bool(new_password):
        sys.exit("PASSWORD_TEST_OLD 与 PASSWORD_TEST_NEW 必须同时设置")
    if new_password and len(new_password) < 8:
        sys.exit("PASSWORD_TEST_NEW 至少 8 位")
    if old_password and old_password == new_password:
        sys.exit("PASSWORD_TEST_OLD 与 PASSWORD_TEST_NEW 不能相同")
    failures: list[str] = []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            for width, height in VIEWPORTS:
                failures.extend(run_anonymous(browser, width, height, base_url, out_dir))
                failures.extend(run_viewport(
                    browser, width, height, base_url, cookie, user_legacy_id, out_dir,
                    live_sse=True, old_password=old_password, new_password=new_password,
                ))
            failures.extend(run_bootstrap_fallback(browser, base_url, cookie, out_dir))
            failures.extend(run_render_missing(browser, base_url, cookie, out_dir))
            failures.extend(run_mutual_wechat_prefill(browser, base_url, cookie, out_dir))
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
