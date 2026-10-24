#!/usr/bin/env python3
"""用户端审计修复回归（UD-02/04/05/06/07 + 管理台广播 / iframe 兜底）。

覆盖（390×844 与 1280×900 两个视口）：

1. 活动：列表卡片显示开始时间（无时间显示「活动时间待定」），详情显示
   「活动时间：…」与地点；
2. 断网：我的资料请求失败时 toast 是中文「网络不稳定，请稍后重试」，
   启动错误页同理，不再出现 Failed to fetch / TypeError；
3. iOS 安全区：CDP 模拟顶部 47 / 底部 34，返回栏让出顶部安全区、
   底部 tabbar 让出底部安全区，正文不被返回栏遮挡；
4. 照片删除：二次确认文案说明不可恢复；只有一张时提示「最后一张」会触发
   人脸门禁；删除按钮命中区 ≥44×44（视觉圆点仍 20px）；取消不删除；
5. 改手机号：改号后保存弹出「当前密码」输入；空密码与错误密码分别给出中文提示，
   错误时不落库、不关闭弹层；
6. 管理台广播：管理台 → 原版活动列表 → 控制台页头「📣 广播」→ iframe 打开
   `/admin/activities/{id}#broadcast` 并激活广播面板 → 返回回控制台；
7. iframe 兜底：`/admin?embed=1` 被拦截时显示「管理台加载失败」+「返回管理台」，
   不留白屏；恢复后 `?admin=1` 入口仍能打开管理台概览；
8. 主流程无 console error / pageerror（断网与故意拦截两个场景单独标记为预期）。

需要已登录环境与三个 cookie（可用本地实例 + 每用户一条 sessions）：

    BASE_URL=http://127.0.0.1:18095 \
    SESSION_COOKIE=<普通用户，资料页有 1 张照片且已设密码> \
    ADMIN_SESSION_COOKIE=<可管理活动的管理员> \
    python3 tools/browser/spa_user_fixes_smoke.py

可选 LAST_PHOTO_SESSION_COOKIE：另一张照片数=1 的账号（不给时用 SESSION_COOKIE 验证）。
可选 PHONE_TEST_PASSWORD：给定时额外验证「正确密码改号成功→资料页刷新→改回原号」。
可选 SHOT_DIR（默认 /tmp/opencode/user-fixes-smoke）。
不放进 make ci。
"""

from __future__ import annotations

import os
import re
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (1280, 900))
NETWORK_MSG = "网络不稳定，请稍后重试"
TAB_CARDS = "牵线"
TAB_PROFILE = "我的"
# 断网 / 故意拦截 iframe 时浏览器必然打网络错误日志，这两类场景不计入 console 断言。
EXPECTED_CONSOLE = (
    "ERR_INTERNET_DISCONNECTED",
    "ERR_FAILED",
    "ERR_NETWORK_CHANGED",
    "net::",
    "Failed to load resource",
)
EXPECTED_PAGEERROR = ()


class Checker:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.notes: list[str] = []

    def check(self, condition: bool, message: str) -> None:
        if not condition:
            self.failures.append(message)

    def note(self, message: str) -> None:
        self.notes.append(message)


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def add_cookie(context, base_url: str, cookie: str) -> None:
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])


def new_context(browser, base_url: str, cookie: str, width: int, height: int):
    context = browser.new_context(
        viewport={"width": width, "height": height},
        device_scale_factor=2,
        locale="zh-CN",
        is_mobile=width <= 480,
        has_touch=width <= 480,
    )
    add_cookie(context, base_url, cookie)
    # 首次引导/更新说明/活动推广弹窗会盖住底栏，回归只关心本次修复，直接标记为看过。
    context.add_init_script(
        "try {"
        "localStorage.setItem('yxq_swipe_guide_v1','1');"
        "localStorage.setItem('yxq_self_card_guide_v1','1');"
        "localStorage.setItem('yxq_update_notes_v1','1');"
        "sessionStorage.setItem('yxq_activity_promo_v1','1');"
        "} catch (e) {}"
    )
    return context


def dismiss_overlays(page) -> None:
    try:
        btn = page.locator(".sg-btn")
        if btn.count() and btn.first.is_visible():
            btn.first.click()
            page.wait_for_timeout(250)
    except Exception:  # noqa: BLE001 - 引导层不影响后续断言
        pass


def attach_errors(page) -> list[str]:
    errors: list[str] = []

    def on_console(msg) -> None:
        if msg.type != "error":
            return
        text = msg.text or ""
        if any(token in text for token in EXPECTED_CONSOLE):
            return
        errors.append(f"console: {text}")

    page.on("console", on_console)
    page.on("pageerror", lambda exc: errors.append(f"pageerror: {exc}"))
    return errors


def shot(page, out_dir: Path, name: str) -> None:
    try:
        page.screenshot(path=str(out_dir / f"{name}.png"), full_page=False)
    except Exception:  # pragma: no cover - 截图失败不影响结论
        pass


def wait_app(page, base_url: str, timeout_ms: int = 30000) -> None:
    page.goto(base_url.rstrip("/") + "/", wait_until="domcontentloaded")
    page.wait_for_selector(".van-tabbar", timeout=timeout_ms)
    page.wait_for_timeout(500)
    dismiss_overlays(page)


def click_tab(page, label: str) -> None:
    page.locator(".van-tabbar-item").filter(has_text=label).first.click()
    page.wait_for_timeout(450)


def open_profile_edit(page) -> None:
    if page.locator(".edit-photo-item").count():
        return   # 已在修改资料页
    if page.locator(".profile-detail").count():
        # 已在「我的资料」详情：子页没有底部 tabbar，直接点「修改资料」
        page.locator(".van-button").filter(has_text="修改资料").first.click()
        page.wait_for_selector(".edit-photo-item", timeout=15000)
        return
    click_tab(page, TAB_PROFILE)
    page.locator(".van-cell").filter(has_text="我的资料").first.click()
    page.wait_for_selector(".profile-detail", timeout=15000)
    page.locator(".van-button").filter(has_text="修改资料").first.click()
    page.wait_for_selector(".edit-photo-item", timeout=15000)


def check_activity_time(page, chk: Checker, tag: str, out_dir: Path) -> None:
    click_tab(page, "活动")
    page.wait_for_selector(".activity-card", timeout=20000)
    cards = page.locator(".activity-card")
    count = cards.count()
    chk.check(count > 0, f"{tag}:活动列表为空，无法验证时间展示")
    chosen = None
    list_text = ""
    for i in range(min(count, 10)):
        text = cards.nth(i).inner_text()
        if re.search(r"\d{4}-\d{2}-\d{2}", text):
            chosen = i
            list_text = text
            break
    if chosen is None:
        list_text = cards.first.inner_text()
        chk.check("活动时间待定" in list_text, f"{tag}:活动列表既没有时间也没有「活动时间待定」")
    else:
        chk.check("🗓️" in list_text and "开始" in list_text,
                  f"{tag}:活动列表缺少开始时间行，实际={list_text[:80]!r}")
    chk.check("📍" in list_text, f"{tag}:活动列表缺少地点")
    shot(page, out_dir, f"{tag}-activity-list")

    target = cards.nth(chosen if chosen is not None else 0)
    target.click()
    page.wait_for_selector(".detail-meta", timeout=20000)
    detail = page.locator(".detail-meta").first.inner_text()
    chk.check("活动时间：" in detail, f"{tag}:活动详情缺少「活动时间：」，实际={detail[:80]!r}")
    chk.check("📍" in detail, f"{tag}:活动详情缺少地点")
    if chosen is not None:
        date = re.search(r"\d{4}-\d{2}-\d{2}", list_text)
        chk.check(date is not None and date.group(0) in detail,
                  f"{tag}:详情时间与列表不一致（列表 {list_text[:50]!r} / 详情 {detail[:80]!r}）")
    shot(page, out_dir, f"{tag}-activity-detail")
    page.locator(".back-nav").first.click()
    page.wait_for_timeout(450)


def check_photo_delete(page, chk: Checker, tag: str, out_dir: Path) -> None:
    open_profile_edit(page)
    photos = page.locator(".edit-photo-item")
    count = photos.count()
    chk.check(count > 0, f"{tag}:修改资料页没有照片，无法验证删除确认")
    if not count:
        return
    delete_btn = page.locator(".edit-photo-del").first
    box = delete_btn.bounding_box() or {}
    chk.check(box.get("width", 0) >= 43.5 and box.get("height", 0) >= 43.5,
              f"{tag}:照片删除命中区不足 44×44，实际={box}")
    icon_box = page.locator(".edit-photo-del-icon").first.bounding_box() or {}
    chk.check(icon_box.get("width", 99) <= 24 and icon_box.get("height", 99) <= 24,
              f"{tag}:照片删除视觉圆点应保持 20px，实际={icon_box}")
    delete_btn.click()
    dialog = page.locator(".van-dialog").filter(has_text="删除这张照片")
    dialog.wait_for(state="visible", timeout=8000)
    message = dialog.inner_text()
    chk.check("无法恢复" in message, f"{tag}:删除确认缺少不可恢复说明，实际={message!r}")
    if count == 1:
        chk.check("最后一张" in message and "无法使用" in message,
                  f"{tag}:仅一张照片时未提示人脸门禁风险，实际={message!r}")
    shot(page, out_dir, f"{tag}-photo-delete-confirm")
    dialog.locator(".van-dialog__cancel").click()
    page.wait_for_timeout(500)
    chk.check(photos.count() == count, f"{tag}:取消删除后照片数量变化")
    still_open = page.locator(".van-dialog").filter(has_text="删除这张照片")
    chk.check(still_open.count() == 0 or not still_open.first.is_visible(),
              f"{tag}:取消删除后确认框未关闭")


def check_last_photo_warning(browser, base_url: str, cookie: str, width: int, height: int,
                             chk: Checker, tag: str, out_dir: Path) -> None:
    context = new_context(browser, base_url, cookie, width, height)
    page = context.new_page()
    try:
        wait_app(page, base_url)
        open_profile_edit(page)
        photos = page.locator(".edit-photo-item")
        chk.check(photos.count() == 1, f"{tag}:最后一张照片账号应只有 1 张，实际 {photos.count()}")
        if photos.count() != 1:
            return
        page.locator(".edit-photo-del").first.click()
        dialog = page.locator(".van-dialog").filter(has_text="删除这张照片")
        dialog.wait_for(state="visible", timeout=8000)
        text = dialog.inner_text()
        chk.check("最后一张" in text and "无法使用" in text,
                  f"{tag}:最后一张照片未提示人脸门禁，实际={text!r}")
        shot(page, out_dir, f"{tag}-last-photo-warning")
        dialog.locator(".van-dialog__cancel").click()
        page.wait_for_timeout(300)
    finally:
        context.close()


def check_phone_change(page, chk: Checker, tag: str, out_dir: Path) -> None:
    open_profile_edit(page)
    row = page.locator(".edit-row").filter(has_text="手机号").first
    chk.check(row.count() > 0, f"{tag}:修改资料页找不到手机号")
    if not row.count():
        return
    phone_input = row.locator("input")
    original = phone_input.input_value()
    digits = re.sub(r"\\D", "", original)
    if len(digits) < 8:
        chk.check(False, f"{tag}:测试账号手机号格式异常（{original!r}）")
        return
    changed = digits[:-1] + ("8" if digits[-1] != "8" else "7")
    phone_input.fill(changed)
    page.locator(".van-button").filter(has_text="保存").first.click()
    mask = page.locator(".phone-pw-mask")
    mask.wait_for(state="visible", timeout=8000)
    text = mask.inner_text()
    chk.check("验证当前密码" in text and "手机号也是登录账号" in text,
              f"{tag}:改手机号未弹出当前密码验证，实际={text[:80]!r}")
    shot(page, out_dir, f"{tag}-phone-password-dialog")

    # 空密码：前端拦截
    mask.locator(".modal-action-primary").click()
    page.wait_for_timeout(300)
    err = mask.inner_text()
    chk.check("请输入当前密码" in err, f"{tag}:空密码未提示「请输入当前密码」，实际={err[:80]!r}")

    # 错误密码：服务端 password_invalid
    mask.locator('input[type="password"]').fill("wrong-password-9x")
    mask.locator(".modal-action-primary").click()
    try:
        page.wait_for_function(
            """() => {
              const el = document.querySelector('.phone-pw-mask');
              return !!el && /当前密码不正确|密码尝试过于频繁/.test(el.innerText);
            }""",
            timeout=15000,
        )
    except PlaywrightTimeoutError:
        chk.check(False, f"{tag}:错误密码未给出中文提示，弹层内容={mask.inner_text()[:100]!r}")
    else:
        chk.check(mask.is_visible(), f"{tag}:错误密码后弹层被关闭（应留在弹层重试）")
        shot(page, out_dir, f"{tag}-phone-password-error")

    mask.locator(".modal-action").filter(has_text="取消").click()
    page.wait_for_timeout(300)
    chk.check(not mask.is_visible(), f"{tag}:取消后密码弹层未关闭")
    # 还原手机号，避免留下未保存改动影响后续导航
    phone_input.fill(original)
    page.wait_for_timeout(200)
    # 返回我的（若脏守卫仍触发，选择放弃）
    page.locator(".back-nav").first.click()
    page.wait_for_timeout(400)
    if page.locator(".modal-card").filter(has_text="放弃").count():
        page.locator(".modal-action").filter(has_text="放弃").first.click()
        page.wait_for_timeout(400)


def check_phone_success(page, chk: Checker, tag: str, out_dir: Path, password: str) -> None:
    """可选：带正确密码改号成功后跳回资料详情并显示新号码，然后再改回。"""
    open_profile_edit(page)
    row = page.locator(".edit-row").filter(has_text="手机号").first
    if not row.count():
        chk.check(False, f"{tag}:改号成功后验证找不到手机号")
        return
    phone_input = row.locator("input")
    original = phone_input.input_value()
    digits = re.sub(r"\D", "", original)
    changed = digits[:-1] + ("7" if digits[-1] != "7" else "8")
    phone_input.fill(changed)
    page.locator(".van-button").filter(has_text="保存").first.click()
    mask = page.locator(".phone-pw-mask")
    mask.wait_for(state="visible", timeout=8000)
    mask.locator('input[type="password"]').fill(password)
    mask.locator(".modal-action-primary").click()
    try:
        page.wait_for_selector(".profile-detail", timeout=20000)
        page.wait_for_function(
            "(phone) => document.body.innerText.includes(phone)", arg=changed, timeout=15000
        )
    except PlaywrightTimeoutError:
        chk.check(False, f"{tag}:正确密码改号后未回到资料详情/未显示新号码")
        return
    chk.check(changed in page.locator(".profile-detail").inner_text(),
              f"{tag}:改号成功后资料页未刷新为新号码")
    shot(page, out_dir, f"{tag}-phone-changed")

    # 改回原号码，保持测试账号可重复运行
    open_profile_edit(page)
    row = page.locator(".edit-row").filter(has_text="手机号").first
    row.locator("input").fill(original)
    page.locator(".van-button").filter(has_text="保存").first.click()
    mask = page.locator(".phone-pw-mask")
    mask.wait_for(state="visible", timeout=8000)
    mask.locator('input[type="password"]').fill(password)
    mask.locator(".modal-action-primary").click()
    try:
        page.wait_for_selector(".profile-detail", timeout=20000)
        page.wait_for_function(
            "(phone) => document.body.innerText.includes(phone)", arg=original, timeout=15000
        )
    except PlaywrightTimeoutError:
        chk.check(False, f"{tag}:改回原号码失败")
        return
    chk.check(original in page.locator(".profile-detail").inner_text(),
              f"{tag}:改回原号码后资料页未刷新")


def check_offline(browser, base_url: str, cookie: str, width: int, height: int,
                  chk: Checker, tag: str, out_dir: Path) -> None:
    context = new_context(browser, base_url, cookie, width, height)
    page = context.new_page()
    try:
        wait_app(page, base_url)
        click_tab(page, TAB_PROFILE)
        context.set_offline(True)
        # 资料页有 gate 预热缓存，断网后点击不会发请求；用每次进入都刷新的「我的活动」验证全局映射。
        page.locator(".van-cell").filter(has_text="我的活动").first.click()
        try:
            page.wait_for_function(
                f"""() => {{ const t = document.querySelector('.van-toast'); return !!t && t.innerText.includes({NETWORK_MSG!r}); }}""",
                timeout=12000,
            )
        except PlaywrightTimeoutError:
            chk.check(False, f"{tag}:断网时未出现中文提示")
        toast_text = page.locator(".van-toast").first.inner_text() if page.locator(".van-toast").count() else ""
        chk.check(NETWORK_MSG in toast_text, f"{tag}:断网提示不是「{NETWORK_MSG}」，实际={toast_text!r}")
        chk.check("Failed to fetch" not in toast_text and "TypeError" not in toast_text,
                  f"{tag}:断网提示仍是英文原文={toast_text!r}")
        shot(page, out_dir, f"{tag}-offline-toast")
        context.set_offline(False)

        # 启动错误页：挂断 home 接口（文档仍能加载，模拟 App 启动时接口断网）
        context.route("**/legacy/api/home", lambda route: route.abort())
        page.reload(wait_until="domcontentloaded")
        page.wait_for_selector(".boot-error-page", timeout=15000)
        boot_text = page.locator(".boot-error-page").inner_text()
        chk.check(NETWORK_MSG in boot_text, f"{tag}:启动错误页不是中文文案，实际={boot_text!r}")
        chk.check("Failed to fetch" not in boot_text and "TypeError" not in boot_text,
                  f"{tag}:启动错误页仍有英文原文={boot_text!r}")
        shot(page, out_dir, f"{tag}-offline-boot")
        context.unroute("**/legacy/api/home")
        page.locator(".boot-error-page .van-button").click()
        page.wait_for_selector(".van-tabbar", timeout=20000)
    finally:
        context.close()


def check_safe_area(browser, base_url: str, cookie: str, chk: Checker, out_dir: Path) -> None:
    """Chromium CDP：顶部 47 / 底部 34，与审计同口径。"""
    context = browser.new_context(
        viewport={"width": 390, "height": 844},
        device_scale_factor=2,
        locale="zh-CN",
        is_mobile=True,
        has_touch=True,
    )
    add_cookie(context, base_url, cookie)
    context.add_init_script(
        "try {"
        "localStorage.setItem('yxq_swipe_guide_v1','1');"
        "localStorage.setItem('yxq_self_card_guide_v1','1');"
        "localStorage.setItem('yxq_update_notes_v1','1');"
        "sessionStorage.setItem('yxq_activity_promo_v1','1');"
        "} catch (e) {}"
    )
    page = context.new_page()
    try:
        cdp = context.new_cdp_session(page)
        try:
            cdp.send("Emulation.setSafeAreaInsetsOverride", {
                "insets": {"top": 47, "bottom": 34, "left": 0, "right": 0},
            })
        except Exception as exc:  # noqa: BLE001
            chk.check(False, f"安全区模拟失败（CDP）: {exc}")
            return
        wait_app(page, base_url)
        click_tab(page, "活动")
        page.wait_for_selector(".activity-card", timeout=20000)
        tab_metrics = page.evaluate(
            """() => {
              const tab = document.querySelector('.van-tabbar');
              if (!tab) return null;
              const ts = getComputedStyle(tab);
              const tr = tab.getBoundingClientRect();
              return {
                paddingBottom: parseFloat(ts.paddingBottom),
                bottom: tr.bottom,
                viewport: innerHeight,
              };
            }"""
        )
        chk.check(tab_metrics is not None, "安全区:活动页缺少底部 tabbar")
        if tab_metrics:
            chk.check(tab_metrics["paddingBottom"] >= 33,
                      f"安全区:底部 tabbar 未让出安全区，padding-bottom={tab_metrics['paddingBottom']}")
            chk.check(abs(tab_metrics["bottom"] - tab_metrics["viewport"]) <= 1,
                      f"安全区:底部 tabbar 未贴底 bottom={tab_metrics['bottom']} viewport={tab_metrics['viewport']}")
        page.locator(".activity-card").first.click()
        page.wait_for_selector(".detail-meta", timeout=20000)
        metrics = page.evaluate(
            """() => {
              const nav = document.querySelector('.back-nav');
              const meta = document.querySelector('.detail-meta');
              const ns = getComputedStyle(nav);
              const nr = nav.getBoundingClientRect();
              const mr = meta.getBoundingClientRect();
              return {
                navPaddingTop: parseFloat(ns.paddingTop),
                navTop: nr.top, navBottom: nr.bottom, navHeight: nr.height,
                metaTop: mr.top,
              };
            }"""
        )
        chk.check(metrics["navPaddingTop"] >= 47, f"安全区:返回栏未加顶部安全区，padding-top={metrics['navPaddingTop']}")
        chk.check(metrics["navTop"] >= 0, f"安全区:返回栏被顶出视口，top={metrics['navTop']}")
        chk.check(metrics["metaTop"] >= metrics["navBottom"] - 1,
                  f"安全区:正文被返回栏遮挡 navBottom={metrics['navBottom']} metaTop={metrics['metaTop']}")
        shot(page, out_dir, "390-safearea-detail")
    finally:
        context.close()


def admin_frame(page, base_url: str):
    for candidate in page.frames:
        if candidate.url.startswith(base_url.rstrip("/") + "/admin"):
            return candidate
    return None


def wait_admin_frame(page, base_url: str, timeout_ms: int = 20000):
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        frame = admin_frame(page, base_url)
        if frame is not None:
            return frame
        page.wait_for_timeout(200)
    return None


def wait_frame_url(page, frame, needle: str, timeout_ms: int = 20000) -> bool:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        try:
            if needle in frame.url:
                return True
        except Exception:  # noqa: BLE001 - frame 导航期间可能失效
            return False
        page.wait_for_timeout(150)
    return False


def wait_text(page, frame, selector: str, text: str, timeout_ms: int = 20000) -> bool:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        try:
            locator = frame.locator(selector).filter(has_text=text)
            if locator.count():
                return True
        except Exception:  # noqa: BLE001
            pass
        page.wait_for_timeout(200)
    return False


def check_admin_flows(browser, base_url: str, cookie: str, chk: Checker, out_dir: Path,
                      width: int, height: int) -> None:
    tag = f"admin-{width}"
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    add_cookie(context, base_url, cookie)
    context.add_init_script(
        "try {"
        "localStorage.setItem('yxq_swipe_guide_v1','1');"
        "localStorage.setItem('yxq_self_card_guide_v1','1');"
        "localStorage.setItem('yxq_update_notes_v1','1');"
        "sessionStorage.setItem('yxq_activity_promo_v1','1');"
        "} catch (e) {}"
    )
    page = context.new_page()
    errors = attach_errors(page)
    try:
        # 1) ?admin=1 正常入口：overlay + SSR 概览
        page.goto(base_url.rstrip("/") + "/?admin=1", wait_until="load")
        page.wait_for_selector(".embed-overlay", timeout=20000)
        page.wait_for_selector(".embed-frame--ready", timeout=20000)
        frame = wait_admin_frame(page, base_url)
        chk.check(frame is not None, f"{tag}:?admin=1 未打开 iframe")
        if frame is None:
            return
        chk.check("/admin" in frame.url, f"{tag}:管理台 iframe 地址异常 {frame.url}")
        chk.check(wait_text(page, frame, ".admin-topbar, .admin-page-head", "管理台", 10000),
                  f"{tag}:管理台概览未渲染")
        shot(page, out_dir, f"{tag}-01-admin-entry")

        # 2) 原版活动控制台 → 📣 广播
        entry = frame.locator('.admin-entry[href^="/app?native_admin=1"]').first
        chk.check(entry.count() > 0, f"{tag}:管理台概览缺少原版活动入口")
        if not entry.count():
            return
        entry.click()
        chk.check(wait_frame_url(page, frame, "/app?native_admin=1", 20000),
                  f"{tag}:未进入原版活动列表 {frame.url}")
        chk.check(wait_text(page, frame, ".ga-title", "活动管理", 20000),
                  f"{tag}:原版活动列表未渲染")
        page.wait_for_timeout(600)
        frame.locator(".ga-act-row").first.click()
        chk.check(wait_text(page, frame, ".ga-card-title", "模式", 20000),
                  f"{tag}:未进入活动控制台")
        page.wait_for_timeout(500)
        broadcast = frame.locator("button").filter(has_text="广播").first
        chk.check(broadcast.count() > 0, f"{tag}:控制台页头缺少「📣 广播」")
        if broadcast.count():
            box = broadcast.bounding_box() or {}
            chk.check(box.get("height", 0) >= 44, f"{tag}:广播按钮高度不足 44px，实际={box.get('height')}")
            broadcast.click()
            # 嵌套 iframe：/admin/activities/{id}#broadcast
            nested = None
            deadline = time.time() + 20
            while time.time() < deadline:
                for candidate in page.frames:
                    if "/admin/activities/" in candidate.url and "#broadcast" in candidate.url:
                        nested = candidate
                        break
                if nested is not None:
                    break
                page.wait_for_timeout(200)
            chk.check(nested is not None, f"{tag}:广播 iframe 未打开，frames={[f.url for f in page.frames]}")
            if nested is not None:
                try:
                    nested.wait_for_selector("#broadcast", timeout=15000)
                except PlaywrightTimeoutError:
                    chk.check(False, f"{tag}:广播面板未渲染 {nested.url}")
                else:
                    chk.check(nested.locator("#broadcast").is_visible(), f"{tag}:广播面板不可见")
                    selected = nested.locator("#tab-broadcast").get_attribute("aria-selected")
                    chk.check(selected == "true", f"{tag}:#broadcast 未激活广播 tab（aria-selected={selected}）")
                    chk.check("广播" in nested.locator("#broadcast").inner_text(),
                              f"{tag}:广播面板内容异常")
                shot(page, out_dir, f"{tag}-02-broadcast")
                back = frame.locator(".embed-back").first
                chk.check(back.count() > 0, f"{tag}:广播浮层缺少返回按钮")
                if back.count():
                    back.click()
                    page.wait_for_timeout(800)
                    chk.check(wait_text(page, frame, ".ga-card-title", "模式", 10000),
                              f"{tag}:广播返回后未回到控制台")
                    shot(page, out_dir, f"{tag}-03-back-console")

        # 3) iframe 拦截 → 兜底面板 → 返回管理台
        page.locator(".embed-back").first.click()
        page.wait_for_timeout(600)
        page.route(re.compile(r".*/admin\?embed=1$"), lambda route: route.abort())
        page.goto(base_url.rstrip("/") + "/?admin=1", wait_until="load")
        try:
            page.wait_for_selector(".embed-fallback", timeout=25000)
            fallback = page.locator(".embed-fallback").inner_text()
            chk.check("管理台加载失败" in fallback, f"{tag}:iframe 失败未显示兜底文案，实际={fallback[:80]!r}")
            chk.check("返回管理台" in fallback, f"{tag}:iframe 兜底缺少「返回管理台」")
            shot(page, out_dir, f"{tag}-04-iframe-fallback")
        except PlaywrightTimeoutError:
            chk.check(False, f"{tag}:iframe 失败时 25s 内未出现兜底面板")
        page.unroute(re.compile(r".*/admin\?embed=1$"))
        page.locator(".embed-fallback .van-button").filter(has_text="返回管理台").first.click()
        page.wait_for_selector(".embed-frame--ready", timeout=25000)
        chk.check(admin_frame(page, base_url) is not None, f"{tag}:兜底后未恢复到管理台")
        shot(page, out_dir, f"{tag}-05-admin-recovered")

        if errors:
            chk.check(False, f"{tag}:管理台流程 console/pageerror={errors[:4]}")
    finally:
        context.close()


def run_viewport(browser, base_url: str, cookie: str, last_photo_cookie: str,
                 admin_cookie: str, phone_password: str, width: int, height: int,
                 chk: Checker, out_dir: Path) -> None:
    tag = str(width)
    context = new_context(browser, base_url, cookie, width, height)
    page = context.new_page()
    errors = attach_errors(page)
    try:
        wait_app(page, base_url)
        check_activity_time(page, chk, tag, out_dir)
        check_photo_delete(page, chk, tag, out_dir)
        check_phone_change(page, chk, tag, out_dir)
        if phone_password:
            check_phone_success(page, chk, tag, out_dir, phone_password)
        shot(page, out_dir, f"{tag}-profile-back")
        if errors:
            chk.check(False, f"{tag}:主流程 console/pageerror={errors[:4]}")
    finally:
        context.close()

    check_offline(browser, base_url, cookie, width, height, chk, tag, out_dir)
    if last_photo_cookie:
        check_last_photo_warning(browser, base_url, last_photo_cookie, width, height,
                                 chk, f"{tag}-last", out_dir)
    if width == 390:
        check_safe_area(browser, base_url, cookie, chk, out_dir)
    if admin_cookie:
        check_admin_flows(browser, base_url, admin_cookie, chk, out_dir, width, height)


def main() -> int:
    base_url = require("BASE_URL").rstrip("/")
    cookie = require("SESSION_COOKIE")
    admin_cookie = require("ADMIN_SESSION_COOKIE")
    last_photo_cookie = os.environ.get("LAST_PHOTO_SESSION_COOKIE", "").strip()
    phone_password = os.environ.get("PHONE_TEST_PASSWORD", "").strip()
    out_dir = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/user-fixes-smoke"))
    out_dir.mkdir(parents=True, exist_ok=True)

    chk = Checker()
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for width, height in VIEWPORTS:
                run_viewport(browser, base_url, cookie, last_photo_cookie, admin_cookie,
                             phone_password, width, height, chk, out_dir)
        finally:
            browser.close()

    if chk.failures:
        print("FAIL")
        for item in chk.failures:
            print(" -", item)
        for note in chk.notes:
            print(" note:", note)
        return 1
    print(f"PASS 用户端审计修复（390/1280）：活动时间 / 断网中文 / iOS 安全区 / 照片删除确认 / 改手机号密码 / 管理台广播与 iframe 兜底；截图 {out_dir}")
    for note in chk.notes:
        print(" note:", note)
    return 0


if __name__ == "__main__":
    sys.exit(main())
