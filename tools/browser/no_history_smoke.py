#!/usr/bin/env python3
"""顶层 SPA 虚拟历史栈回归（Playwright）。

验收口径（PM 2026-10-26，Via 顶部进度条）：
- 登录后的所有子页/浮层/应用内返回不再调用原生 History API；
- 启动哨兵与安卓硬件返回后的哨兵重压是唯一允许的 pushState；
- 全程无主框架文档导航、无 pageerror/console error；
- 安卓硬件返回（page.goBack 模拟）能逐层回退虚拟栈；栈空时退出或至少不崩；
- 深链 #/likes、刷新、未保存拦截、控制台→签到塌缩、native_admin iframe 内部导航保持可用；
- ?history=legacy / localStorage yxq_history_mode=legacy 可回退旧逻辑。

Via 的顶部进度条本身无法在 Chromium 断言；本脚本用 History API 调用计数作为
可证伪代理，真机 Via 需由 PM 复验。

用法：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<管理员 yxq_session> \
      python3 tools/browser/no_history_smoke.py

未部署代码（推荐先在 staging 上验证本地生成物）：
    LOCAL_ASSETS_DIR=/root/wt/yxq-no-history \
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<...> \
      python3 tools/browser/no_history_smoke.py
LOCAL_ASSETS_DIR 只替换文档壳与 app.min.js，接口/其他静态资源仍走 BASE_URL。

可选：
    VIEWPORT=390x844       默认手机视口，可改成 1280x900
    SKIP_OBSERVER=1        跳过观察员身份切换（默认会尝试，失败只 SKIP 不 FAIL）
    SHOT_DIR=<dir>         失败/关键节点截图目录
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
LOCAL_ASSETS_DIR = os.environ.get("LOCAL_ASSETS_DIR", "").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/no-history-smoke"))
SKIP_OBSERVER = os.environ.get("SKIP_OBSERVER", "").strip() == "1"
VIEWPORT = os.environ.get("VIEWPORT", "390x844").strip()
IGNORED_CONSOLE = (
    "notifications/stream",
    "Failed to load resource",
    "net::ERR",
    "401",
    "403",
    "favicon",
    "ResizeObserver",
)

INIT = r"""
window.__hist = {calls: []};
(function(){
  var proto = History.prototype;
  ['pushState','replaceState','back','go','forward'].forEach(function(name){
    var orig = proto[name];
    if (!orig) return;
    proto[name] = function(){
      try {
        window.__hist.calls.push({
          name: name,
          state: arguments[0] === undefined ? null : arguments[0],
          url: arguments[2] === undefined ? null : arguments[2],
          t: Math.round((performance.now ? performance.now() : 0))
        });
      } catch(e) {}
      return orig.apply(this, arguments);
    };
  });
})();
window.__pageErrors = [];
window.addEventListener('error', function(e){ window.__pageErrors.push(String(e.message)); });
window.addEventListener('unhandledrejection', function(e){ window.__pageErrors.push('rejection:' + String(e.reason)); });
try{
  localStorage.setItem('yxq_swipe_guide_v1','1');
  localStorage.setItem('yxq_self_card_guide_v1','1');
  localStorage.setItem('yxq_update_notes_v1','1');
  sessionStorage.setItem('yxq_activity_promo_v1','1');
}catch(e){}
"""


class Failure(AssertionError):
    pass


def require_env(name: str, value: str) -> str:
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def parse_viewport(raw: str) -> tuple[int, int]:
    try:
        w, h = raw.lower().split("x", 1)
        return int(w), int(h)
    except Exception:  # noqa: BLE001
        sys.exit(f"VIEWPORT 格式应为 390x844，收到 {raw!r}")


def is_sentinel(call: dict) -> bool:
    state = call.get("state") or {}
    return call.get("name") == "pushState" and state.get("yxqSentinel") == 1


def call_tag(call: dict) -> str:
    state = call.get("state") or {}
    return str(state.get("page") or state.get("overlay") or ("sentinel" if state.get("yxqSentinel") else "-"))


def hist_calls(page) -> list[dict]:
    return page.evaluate("window.__hist ? window.__hist.calls : []")


def clear_hist(page) -> None:
    page.evaluate("window.__hist && (window.__hist.calls = [])")


def assert_no_history(page, step: str) -> None:
    calls = hist_calls(page)
    bad = [c for c in calls if not is_sentinel(c)]
    if bad:
        raise Failure(f"{step}: 出现原生 History 调用 {[(c['name'], call_tag(c)) for c in bad]}")
    clear_hist(page)


def assert_sentinel_only(page, step: str, expect=True) -> None:
    calls = hist_calls(page)
    bad = [c for c in calls if not is_sentinel(c)]
    if bad:
        raise Failure(f"{step}: 硬件返回出现非哨兵 History 调用 {[(c['name'], call_tag(c)) for c in bad]}")
    sentinels = [c for c in calls if is_sentinel(c)]
    if expect and not sentinels:
        raise Failure(f"{step}: 虚拟栈非空，但未重压哨兵（下一次硬件返回会漏）")
    clear_hist(page)


def page_name(page) -> str:
    try:
        return page.evaluate("window.yxqRootProxy && window.yxqRootProxy.page")
    except Exception:  # noqa: BLE001
        return ""


def wait_page(page, name: str, timeout: int = 20000) -> None:
    try:
        page.wait_for_function(
            "window.yxqRootProxy && window.yxqRootProxy.page === %r" % name,
            timeout=timeout,
        )
    except PlaywrightTimeoutError as exc:
        raise Failure(f"等待 page={name} 超时，当前={page_name(page)!r}") from exc
    page.wait_for_timeout(650)


def assert_state(page, expr: str, message: str) -> None:
    try:
        ok = page.evaluate(f"window.yxqRootProxy && ({expr})")
    except Exception:  # noqa: BLE001
        ok = False
    if not ok:
        raise Failure(message)


def wait_app(page, timeout: int = 40000) -> None:
    try:
        page.wait_for_function("window.__YXQ_APP_MOUNTED__ === true", timeout=timeout)
        page.locator("#boot-card").wait_for(state="detached", timeout=timeout)
    except PlaywrightTimeoutError as exc:
        raise Failure("SPA 未完成挂载（__YXQ_APP_MOUNTED__ / #boot-card）") from exc
    page.wait_for_timeout(500)


def click_text(page, text: str, timeout: int = 12000) -> None:
    loc = page.get_by_text(text)
    try:
        loc.first.wait_for(state="visible", timeout=timeout)
    except PlaywrightTimeoutError:
        pass
    for i in range(min(loc.count(), 8)):
        try:
            if loc.nth(i).is_visible():
                loc.nth(i).click(timeout=timeout)
                page.wait_for_timeout(400)
                return
        except (PlaywrightTimeoutError, Failure):
            continue
    raise Failure(f"找不到可点击文本：{text}")


def click_visible(page, selector: str, timeout: int = 12000) -> None:
    loc = page.locator(selector)
    try:
        loc.first.wait_for(state="visible", timeout=timeout)
    except PlaywrightTimeoutError as exc:
        raise Failure(f"找不到可见元素：{selector}") from exc
    for i in range(loc.count()):
        try:
            if loc.nth(i).is_visible():
                loc.nth(i).click(timeout=timeout)
                page.wait_for_timeout(400)
                return
        except (PlaywrightTimeoutError, Failure):
            continue
    raise Failure(f"元素不可点击：{selector}")


def click_back(page, parent: str) -> None:
    click_visible(page, ".back-nav", timeout=12000)
    wait_page(page, parent)


def tap_tab(page, label: str, page_key: str) -> None:
    page.locator(".van-tabbar-item").filter(has_text=label).first.click(timeout=12000)
    wait_page(page, page_key)
    page.wait_for_timeout(450)


class NavProbe:
    """主框架跨文档导航探针（CDP type=Navigation + 文档请求）。"""

    def __init__(self, context, page):
        self.page = page
        self.armed = False
        self.navigations: list[str] = []
        self.doc_requests: list[str] = []
        self.cdp = context.new_cdp_session(page)
        self.cdp.send("Page.enable")
        self.cdp.on("Page.frameNavigated", self._on_frame_navigated)
        page.on("request", self._on_request)

    def _on_frame_navigated(self, params) -> None:
        frame = params.get("frame") or {}
        if frame.get("parentId"):
            return
        if params.get("type") != "Navigation":
            return
        url = frame.get("url") or ""
        if self.armed and url and url != "about:blank":
            self.navigations.append(url)

    def _on_request(self, request) -> None:
        try:
            if not request.is_navigation_request():
                return
            if request.frame != self.page.main_frame:
                return
        except Exception:  # noqa: BLE001
            return
        if self.armed:
            self.doc_requests.append(request.url)

    def arm(self) -> None:
        self.navigations.clear()
        self.doc_requests.clear()
        self.armed = True

    def checkpoint(self, step: str) -> None:
        if self.navigations:
            raise Failure(f"{step}: 出现主框架整页导航 {self.navigations}")
        if self.doc_requests:
            raise Failure(f"{step}: 主框架发起文档请求 {self.doc_requests}")
        self.navigations.clear()
        self.doc_requests.clear()


def attach_errors(page) -> list[dict]:
    errors: list[dict] = []

    def on_pageerror(exc):
        errors.append({"kind": "pageerror", "text": str(exc)})

    def on_console(msg):
        if msg.type != "error":
            return
        text = msg.text
        if any(skip in text for skip in IGNORED_CONSOLE):
            return
        loc = msg.location or {}
        errors.append({"kind": "console", "text": text, "url": loc.get("url", "")})

    page.on("pageerror", on_pageerror)
    page.on("console", on_console)
    return errors


def assert_errors(errors: list[dict], step: str) -> None:
    if errors:
        raise Failure(f"{step}: console/pageerror {errors[:4]}")


def build_local_arm():
    repo = Path(LOCAL_ASSETS_DIR)
    legacy = repo / "web/static/legacy"
    shell = (legacy / "index.html").read_text(encoding="utf-8")
    marker = "window.__YXQ_AUTHENTICATED__=false;"
    if marker not in shell:
        raise Failure(f"{legacy/'index.html'} 缺少 {marker}")
    shell = shell.replace(marker, "window.__YXQ_AUTHENTICATED__=true;")
    script = os.environ.get("LOCAL_SCRIPT", "app.min.js").strip() or "app.min.js"
    if script == "app.js":
        # 调试用：让壳的主脚本列表指向未压缩 app.js，堆栈可读。
        shell = shell.replace("'app.min.js?v=__ASSET_URL_VERSION__'", "'app.js'")
    from local_assets import Arm  # noqa: PLC0415 - 依赖脚本同目录
    return Arm(
        name="local",
        shell_html=shell,
        script_name=script,
        script_bytes=(legacy / script).read_bytes(),
    )


def open_context(browser, *, start: str = "/", legacy: bool = False):
    width, height = parse_viewport(VIEWPORT)
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.add_init_script(INIT)
    if legacy:
        context.add_init_script(
            "try{localStorage.setItem('yxq_history_mode','legacy')}catch(e){}"
        )
    context.add_cookies([{
        "name": "yxq_session",
        "value": COOKIE,
        "url": BASE,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    if LOCAL_ASSETS_DIR:
        from local_assets import install  # noqa: PLC0415
        install(context, BASE, build_local_arm())
    page = context.new_page()
    errors = attach_errors(page)
    probe = NavProbe(context, page)
    page.goto(BASE + start, wait_until="domcontentloaded", timeout=60000)
    wait_app(page)
    if not page.evaluate("window.__YXQ_AUTHENTICATED__ === true"):
        raise Failure("未登录：window.__YXQ_AUTHENTICATED__ 不是 true")
    probe.arm()
    return context, page, probe, errors


def assert_boot_history(page, step: str) -> None:
    calls = hist_calls(page)
    bad = [c for c in calls if c["name"] in ("back", "go", "forward")]
    if bad:
        raise Failure(f"{step}: 启动期出现 {bad}")
    pushes = [c for c in calls if c["name"] == "pushState"]
    if len(pushes) != 1 or not is_sentinel(pushes[0]):
        raise Failure(f"{step}: 启动应只有 1 次哨兵 pushState，实际 {[(c['name'], call_tag(c)) for c in calls]}")
    clear_hist(page)


def snapshot(page, name: str) -> None:
    try:
        SHOT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(SHOT_DIR / f"{name}.png"), full_page=False)
    except Exception:  # noqa: BLE001
        pass


def main_flow(browser) -> None:
    context, page, probe, errors = open_context(browser)
    try:
        assert_boot_history(page, "启动")
        print("PASS 启动：仅哨兵 pushState，无 replaceState/back")

        # 1) 四 tab 往返
        for label, key in (("消息", "likes"), ("活动", "home"), ("我的", "profile"), ("牵线", "cards"),
                           ("我的", "profile"), ("消息", "likes"), ("牵线", "cards")):
            tap_tab(page, label, key)
            assert_no_history(page, f"切 tab {label}")
            probe.checkpoint(f"切 tab {label}")
        print("PASS 四 tab 往返：0 次 History 调用、0 次文档导航")

        # 2) 消息子页
        tap_tab(page, "消息", "likes")
        for text, key in (("我的留言", "my_messages"), ("收到的留言", "received_messages")):
            click_text(page, text)
            wait_page(page, key)
            assert_no_history(page, f"进入 {text}")
            probe.checkpoint(f"进入 {text}")
            click_back(page, "likes")
            assert_no_history(page, f"返回 {text}")
            probe.checkpoint(f"返回 {text}")
        print("PASS 我的留言/收到的留言：进入与返回 0 次 History 调用")

        # 3) 我的子页
        tap_tab(page, "我的", "profile")
        click_text(page, "我的活动")
        wait_page(page, "my_activities")
        assert_no_history(page, "进入我的活动")
        probe.checkpoint("进入我的活动")
        click_back(page, "profile")
        assert_no_history(page, "返回我的活动")
        probe.checkpoint("返回我的活动")

        click_text(page, "我的资料")
        wait_page(page, "profile_detail")
        assert_no_history(page, "进入资料详情")
        probe.checkpoint("进入资料详情")
        click_text(page, "修改资料")
        wait_page(page, "profile_edit")
        assert_no_history(page, "进入资料编辑")
        probe.checkpoint("进入资料编辑")
        click_back(page, "profile_detail")
        assert_no_history(page, "返回资料编辑")
        probe.checkpoint("返回资料编辑")
        click_back(page, "profile")
        assert_no_history(page, "返回资料详情")
        probe.checkpoint("返回资料详情")

        click_text(page, "修改密码")
        wait_page(page, "password")
        assert_no_history(page, "进入修改密码")
        probe.checkpoint("进入修改密码")
        click_back(page, "profile")
        assert_no_history(page, "返回修改密码")
        probe.checkpoint("返回修改密码")

        # 麦穗子页（入口是 profile 卡片行，不是 tab）
        points_row = page.locator(".profile-card-row").filter(has_text="我的麦穗").first
        if points_row.count():
            points_row.click(timeout=12000)
            wait_page(page, "points")
            assert_no_history(page, "进入我的麦穗")
            probe.checkpoint("进入我的麦穗")
            click_back(page, "profile")
            assert_no_history(page, "返回我的麦穗")
            probe.checkpoint("返回我的麦穗")
            print("PASS 我的活动/资料/改密/麦穗子页：0 次 History 调用")
        else:
            print("SKIP 我的麦穗入口未显示")

        # 意见反馈浮层
        for _ in range(3):
            click_text(page, "意见反馈")
            if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showSuggestionDialog === true"):
                break
            page.wait_for_timeout(500)
        if not page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showSuggestionDialog === true"):
            snapshot(page, "feedback-fail")
            print("DEBUG page=", page_name(page),
                  "proxy=", page.evaluate("({sug: window.yxqRootProxy.showSuggestionDialog, ov: window.yxqRootProxy.showUnsavedConfirm, busy: window.yxqRootProxy.actionLoading})"),
                  "hist=", [(c['name'], call_tag(c)) for c in hist_calls(page)],
                  "body=", page.evaluate("document.body.innerText.slice(-240)").replace("\n", " | "))
            raise Failure("意见反馈未打开")
        assert_no_history(page, "打开意见反馈")
        page.evaluate("window.yxqRootProxy.showSuggestionDialog = false")
        page.wait_for_timeout(700)
        if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showSuggestionDialog !== false"):
            snapshot(page, "feedback-close-fail")
            print("DEBUG close page=", page_name(page),
                  "proxy=", page.evaluate("({sug: window.yxqRootProxy.showSuggestionDialog, unsaved: window.yxqRootProxy.showUnsavedConfirm, filter: window.yxqRootProxy.showFilter, embed: window.yxqRootProxy.embedOpen})"),
                  "dialogs=", page.evaluate("[...document.querySelectorAll('.van-dialog')].map(e=>getComputedStyle(e).display)"),
                  "hist=", [(c['name'], call_tag(c)) for c in hist_calls(page)])
            raise Failure("意见反馈未关闭")
        assert_no_history(page, "关闭意见反馈")
        probe.checkpoint("意见反馈浮层")
        print("PASS 意见反馈浮层：打开/关闭 0 次 History 调用")

        # 4) 管理台 overlay（顶层 SPA 只开 iframe，不 pushState）
        click_text(page, "管理后台")
        page.locator(".embed-back").first.wait_for(state="visible", timeout=20000)
        assert_no_history(page, "打开管理后台 overlay")
        page.wait_for_timeout(800)
        probe.checkpoint("打开管理后台 overlay")
        click_visible(page, ".embed-back", timeout=12000)
        page.wait_for_timeout(600)
        try:
            page.locator(".embed-overlay").first.wait_for(state="detached", timeout=6000)
        except PlaywrightTimeoutError:
            pass
        page.wait_for_timeout(500)
        assert_no_history(page, "关闭管理后台 overlay")
        probe.checkpoint("关闭管理后台 overlay")
        print("PASS 管理后台 overlay：打开/关闭 0 次 History 调用")

        # 5) 牵线页浮层：筛选 / 喜欢 / 举报 / 照片查看器
        tap_tab(page, "牵线", "cards")
        for _ in range(4):
            try:
                click_visible(page, ".filter-btn", timeout=12000)
            except Failure:
                page.wait_for_timeout(900)
                continue
            if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showFilter === true"):
                break
            page.wait_for_timeout(700)
        if not page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showFilter === true"):
            snapshot(page, "filter-fail")
            print("DEBUG filter page=", page_name(page),
                  "proxy=", page.evaluate("({filter: window.yxqRootProxy.showFilter, embed: window.yxqRootProxy.embedOpen, unsaved: window.yxqRootProxy.showUnsavedConfirm, hist: window.__hist.calls.slice(-5)})"),
                  "btns=", page.evaluate("[...document.querySelectorAll('.filter-btn')].map(e=>({vis:e.offsetParent!==null,box:e.getBoundingClientRect().toJSON()}))"),
                  "body=", page.evaluate("document.body.innerText.slice(0,160)").replace("\n", " | "))
            raise Failure("筛选未打开")
        assert_no_history(page, "打开筛选")
        click_text(page, "取消")
        page.wait_for_timeout(600)
        if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showFilter !== false"):
            snapshot(page, "filter-close-fail")
            print("DEBUG filter close page=", page_name(page),
                  "proxy=", page.evaluate("({filter: window.yxqRootProxy.showFilter, unsaved: window.yxqRootProxy.showUnsavedConfirm, hist: window.__hist.calls.slice(-5)})"),
                  "cancels=", page.evaluate("[...document.querySelectorAll('button,.van-button')].filter(e=>e.innerText.trim()==='取消'&&e.offsetParent!==null).length"))
            raise Failure("筛选未关闭")
        assert_no_history(page, "关闭筛选")
        probe.checkpoint("筛选浮层")

        # 照片查看器：用同一 Vue 状态开关驱动 watch（PhotoSwipe 第三方 DOM 不在本用例范围）
        page.evaluate("window.yxqRootProxy.showPhotoViewer = true")
        page.wait_for_timeout(600)
        assert_state(page, "window.yxqRootProxy.showPhotoViewer === true", "照片查看器未打开")
        assert_no_history(page, "打开照片查看器")
        page.evaluate("window.yxqRootProxy.showPhotoViewer = false")
        page.wait_for_timeout(700)
        assert_state(page, "window.yxqRootProxy.showPhotoViewer === false", "照片查看器未关闭")
        assert_no_history(page, "关闭照片查看器")
        probe.checkpoint("照片查看器")
        like_opened = False
        for idx in range(1, 8):
            page.evaluate("window.yxqRootProxy.cardIndex = %d" % idx)
            page.wait_for_timeout(700)
            info = page.evaluate("({idx: window.yxqRootProxy.cardIndex, self: !!(window.yxqRootProxy.currentCard && window.yxqRootProxy.currentCard.is_self), liked: !!(window.yxqRootProxy.currentCard && window.yxqRootProxy.currentCard.liked), hearts: document.querySelectorAll('.heart-btn:not([disabled])').length})")
            if info["self"] or info["liked"] or not info["hearts"]:
                continue
            page.locator(".heart-btn:not([disabled])").first.click(timeout=12000)
            page.wait_for_timeout(600)
            if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showLikeDialog === true"):
                like_opened = True
                break
        if like_opened:
            assert_no_history(page, "打开喜欢附言")
            page.evaluate("window.yxqRootProxy.showLikeDialog = false")
            page.wait_for_timeout(700)
            if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showLikeDialog !== false"):
                snapshot(page, "like-close-fail")
                print("DEBUG like close page=", page_name(page),
                      "proxy=", page.evaluate("({like: yxqRootProxy.showLikeDialog, filter: yxqRootProxy.showFilter, report: yxqRootProxy.showReportDialog, sug: yxqRootProxy.showSuggestionDialog, unsaved: yxqRootProxy.showUnsavedConfirm, embed: yxqRootProxy.embedOpen, card: yxqRootProxy.cardIndex, hist: window.__hist.calls.slice(-8)})"))
                raise Failure("喜欢附言未关闭")
            assert_no_history(page, "关闭喜欢附言")
            probe.checkpoint("喜欢附言浮层")
            print("PASS 喜欢附言浮层：0 次 History 调用")
        else:
            print("SKIP 找不到可点赞的卡片（7 张内均为自己/已喜欢/观察员）")

        # 举报浮层：用真实 openReportDialog 入口（不发请求，只开/关）
        for _ in range(3):
            page.evaluate("window.yxqRootProxy.openReportDialog({id: 1, target_nickname: '测试', content: '测试留言'})")
            page.wait_for_timeout(500)
            if page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showReportDialog === true"):
                break
        if not page.evaluate("window.yxqRootProxy && window.yxqRootProxy.showReportDialog === true"):
            snapshot(page, "report-fail")
            print("DEBUG report page=", page_name(page),
                  "proxy=", page.evaluate("({report: window.yxqRootProxy.showReportDialog, filter: window.yxqRootProxy.showFilter, like: window.yxqRootProxy.showLikeDialog, target: window.yxqRootProxy.reportTarget, hist: window.__hist.calls.slice(-8)})"),
                  "dialogs=", page.evaluate("[...document.querySelectorAll('.van-dialog')].map(e=>({d:getComputedStyle(e).display,t:e.innerText.slice(0,20)}))"))
            raise Failure("举报未打开")
        assert_no_history(page, "打开举报")
        page.evaluate("window.yxqRootProxy.showReportDialog = false")
        r0 = page.evaluate("window.yxqRootProxy.showReportDialog")
        page.wait_for_timeout(120)
        r1 = page.evaluate("window.yxqRootProxy.showReportDialog")
        page.wait_for_timeout(700)
        r2 = page.evaluate("window.yxqRootProxy.showReportDialog")
        if r2 is not False:
            print("DEBUG report close t0/t120/t820=", r0, r1, r2,
                  "hist=", [(c['name'], call_tag(c)) for c in hist_calls(page)],
                  "dialogs=", page.evaluate("[...document.querySelectorAll('.van-dialog')].map(e=>getComputedStyle(e).display)"))
            raise Failure("举报未关闭")
        assert_no_history(page, "关闭举报")
        probe.checkpoint("举报浮层")

        print("PASS 筛选/喜欢/举报/照片查看器：0 次 History 调用")

        # 6) 活动详情 / 志愿选择 / 分组结果 / 必聊结果
        tap_tab(page, "活动", "home")
        card = page.locator(".activity-card").filter(has_text="2026深圳南头堂一线牵").first
        if card.count():
            card.locator("h3").first.click()
            wait_page(page, "activity_detail")
            assert_no_history(page, "进入活动详情")
            probe.checkpoint("进入活动详情")
            click_back(page, "home")
            assert_no_history(page, "返回活动详情")
            probe.checkpoint("返回活动详情")

            if card.get_by_text("志愿选择").count():
                card.get_by_text("志愿选择").first.click(timeout=12000)
                wait_page(page, "group_select")
                assert_no_history(page, "进入志愿选择")
                probe.checkpoint("进入志愿选择")
                click_back(page, "home")
                assert_no_history(page, "返回志愿选择")
                probe.checkpoint("返回志愿选择")

            for label, key in (("查看我的分组", "group_result"), ("查看我的必聊", "onetoone_result")):
                if card.get_by_text(label).count():
                    card.get_by_text(label).first.click(timeout=12000)
                    wait_page(page, key)
                    assert_no_history(page, f"进入{label}")
                    probe.checkpoint(f"进入{label}")
                    click_back(page, "home")
                    assert_no_history(page, f"返回{label}")
                    probe.checkpoint(f"返回{label}")
            print("PASS 活动详情/志愿选择/结果页：0 次 History 调用")
        else:
            raise Failure("活动列表找不到「2026深圳南头堂一线牵」，无法覆盖活动详情/志愿选择")

        # 现场签到浮层（用户自助签到码）
        signin_btn = page.locator(".activity-card").filter(has_text="📋 现场签到").get_by_text("现场签到")
        if signin_btn.count():
            signin_btn.first.click(timeout=12000)
            page.wait_for_timeout(500)
            assert_state(page, "window.yxqRootProxy.signinDialogShow === true", "现场签到浮层未打开")
            assert_no_history(page, "打开现场签到浮层")
            page.evaluate("window.yxqRootProxy.closeSigninDialog()")
            page.wait_for_timeout(500)
            assert_state(page, "window.yxqRootProxy.signinDialogShow === false", "现场签到浮层未关闭")
            assert_no_history(page, "关闭现场签到浮层")
            probe.checkpoint("现场签到浮层")
            print("PASS 现场签到浮层：0 次 History 调用")
        else:
            print("SKIP 当前账号/活动没有自助签到按钮")

        # 7) 控制台→签到历史塌缩（顶层 checkin 页；真实入口在 native_admin iframe 内）
        page.evaluate("window.yxqRootProxy.page = 'group_admin'")
        wait_page(page, "group_admin")
        assert_no_history(page, "打开分组管理")
        page.evaluate("window.yxqRootProxy.page = 'group_detail'")
        wait_page(page, "group_detail")
        assert_no_history(page, "打开活动管理详情")
        clear_hist(page)
        page.evaluate("window.yxqRootProxy.openCheckinPage({activity_id: 'A-0011', name: '2026深圳南头堂一线牵'})")
        wait_page(page, "checkin")
        assert_no_history(page, "控制台→签到塌缩")
        click_back(page, "group_admin")
        assert_no_history(page, "返回签到页")
        probe.checkpoint("控制台→签到")
        print("PASS 控制台→签到塌缩：replace 只改内存栈，返回落到分组管理")

        # 回到主 tab（tabbar 在子页隐藏）
        page.evaluate("window.yxqRootProxy.page = 'cards'")
        wait_page(page, "cards")
        assert_no_history(page, "回到主 tab")

        # 8) 安卓硬件返回：逐层回退 + 哨兵重压
        tap_tab(page, "我的", "profile")
        click_text(page, "我的资料")
        wait_page(page, "profile_detail")
        click_text(page, "修改资料")
        wait_page(page, "profile_edit")
        clear_hist(page)
        page.go_back(wait_until="commit")
        wait_page(page, "profile_detail")
        assert_sentinel_only(page, "硬件返回 1")
        page.go_back(wait_until="commit")
        wait_page(page, "profile")
        assert_sentinel_only(page, "硬件返回 2")
        probe.checkpoint("硬件返回")
        print("PASS 硬件返回：逐层回退，哨兵按需重压")

        # 9) 未保存拦截 + 硬件返回
        click_text(page, "我的资料")
        wait_page(page, "profile_detail")
        click_text(page, "修改资料")
        wait_page(page, "profile_edit")
        inputs = page.locator(".edit-row input:not([disabled]):not([readonly])")
        if not inputs.count():
            inputs = page.locator(".edit-row textarea:not([disabled]):not([readonly])")
        if not inputs.count():
            raise Failure("资料编辑页找不到可编辑输入框")
        inputs.first.fill("nohist" + str(int(time.time())))
        page.wait_for_timeout(300)
        clear_hist(page)
        page.go_back(wait_until="commit")
        page.locator(".modal-body").filter(has_text="资料已修改").first.wait_for(state="visible", timeout=12000)
        assert_sentinel_only(page, "未保存硬件返回")
        click_text(page, "继续编辑")
        page.wait_for_timeout(500)
        if page_name(page) != "profile_edit":
            raise Failure("取消离开后应留在资料编辑页")
        assert_no_history(page, "未保存取消")
        page.go_back(wait_until="commit")
        page.locator(".modal-body").filter(has_text="资料已修改").first.wait_for(state="visible", timeout=12000)
        assert_sentinel_only(page, "未保存再次返回")
        click_text(page, "放弃")
        wait_page(page, "profile_detail")
        assert_no_history(page, "未保存放弃离开")
        probe.checkpoint("未保存拦截")
        print("PASS 未保存拦截：硬件返回/取消/放弃均不调原生 History API")

        assert_errors(errors, "主流程")
    finally:
        snapshot(page, "main-final")
        context.close()


def deep_link_flow(browser) -> None:
    context, page, probe, errors = open_context(browser, start="/#/likes")
    try:
        # 深链清 hash 允许一次 replaceState；哨兵仍只一次
        calls = hist_calls(page)
        if not any(is_sentinel(c) for c in calls):
            raise Failure(f"深链启动缺少哨兵：{[(c['name'], call_tag(c)) for c in calls]}")
        clear_hist(page)   # 启动清 hash 的 replaceState 属允许项；之后子页必须 0 次
        wait_page(page, "likes")
        assert_no_history(page, "深链进入消息 tab")
        probe.checkpoint("深链启动")

        click_text(page, "我的留言")
        wait_page(page, "my_messages")
        assert_no_history(page, "深链后进入我的留言")
        click_back(page, "likes")
        assert_no_history(page, "深链后返回")
        probe.checkpoint("深链子页")

        page.reload(wait_until="domcontentloaded", timeout=60000)
        wait_app(page)
        probe.arm()
        assert_boot_history(page, "刷新")
        wait_page(page, "cards")
        assert_no_history(page, "刷新后")
        probe.checkpoint("刷新")
        print("PASS 深链 #/likes 与刷新：行为保持，0 次子页 History 调用")
        assert_errors(errors, "深链/刷新")
    finally:
        context.close()


def legacy_flow(browser, *, via_query: bool) -> None:
    start = "/?history=legacy" if via_query else "/"
    context, page, probe, errors = open_context(browser, start=start, legacy=not via_query)
    label = "?history=legacy" if via_query else "localStorage legacy"
    try:
        calls = hist_calls(page)
        if any(is_sentinel(c) for c in calls):
            raise Failure(f"{label}: legacy 模式不应压哨兵")
        clear_hist(page)
        tap_tab(page, "我的", "profile")
        click_text(page, "我的资料")
        wait_page(page, "profile_detail")
        calls = hist_calls(page)
        pushes = [c for c in calls if c["name"] == "pushState" and call_tag(c) == "profile_detail"]
        if not pushes:
            raise Failure(f"{label}: 旧逻辑应 pushState profile_detail，实际 {[(c['name'], call_tag(c)) for c in calls]}")
        clear_hist(page)
        click_back(page, "profile")
        calls = hist_calls(page)
        if not any(c["name"] == "back" for c in calls):
            raise Failure(f"{label}: 旧逻辑返回应调用 history.back()，实际 {calls}")
        clear_hist(page)
        print(f"PASS {label}：回退旧逻辑可用（pushState + back）")
        assert_errors(errors, label)
    finally:
        context.close()


def observer_flow(browser) -> None:
    if SKIP_OBSERVER:
        print("SKIP 观察员（SKIP_OBSERVER=1）")
        return
    context, page, probe, errors = open_context(browser)
    try:
        # 观察员是「视图角色」cookie（httpOnly，由 /legacy/api/account/switch 设置）；
        # 这里直接注入同一 cookie，避免测试改账号状态，也不依赖 CSRF。
        context.add_cookies([{
            "name": "yxq_view_role",
            "value": "observer",
            "url": BASE,
            "httpOnly": True,
            "sameSite": "Lax",
        }])
        page.reload(wait_until="domcontentloaded", timeout=60000)
        wait_app(page)
        probe.arm()
        if not page.evaluate("!!window.yxqRootProxy.isObserver"):
            print("SKIP 观察员：cookie 注入后 isObserver 仍为 false")
            return
        clear_hist(page)
        for label, key in (("消息", "likes"), ("活动", "home"), ("我的", "profile"), ("牵线", "cards")):
            tap_tab(page, label, key)
            assert_no_history(page, f"观察员切 tab {label}")
        tap_tab(page, "消息", "likes")
        click_text(page, "我的留言")
        wait_page(page, "my_messages")
        assert_no_history(page, "观察员进入我的留言")
        click_back(page, "likes")
        assert_no_history(page, "观察员返回我的留言")
        probe.checkpoint("观察员导航")
        print("PASS 观察员：tab/子页/返回 0 次 History 调用")
        assert_errors(errors, "观察员")
    finally:
        context.close()


def native_admin_flow(browser) -> None:
    context, page, probe, errors = open_context(browser)
    try:
        page.evaluate("window.yxqRootProxy.openEmbed('/app?native_admin=1&p=group_admin', '活动管理')")
        page.locator(".embed-back").first.wait_for(state="visible", timeout=25000)
        assert_no_history(page, "打开 native_admin iframe")
        probe.checkpoint("打开 native_admin iframe")

        iframe = None
        deadline = time.time() + 25
        while time.time() < deadline:
            for frame in page.frames:
                if "native_admin=1" in frame.url:
                    iframe = frame
                    break
            if iframe:
                break
            page.wait_for_timeout(400)
        if not iframe:
            raise Failure("native_admin iframe 未加载")
        iframe.wait_for_selector(".ga-act-row", timeout=25000)
        clear_hist(page)

        iframe.locator(".ga-act-row").first.click(timeout=15000)
        iframe.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.page === 'group_detail'", timeout=20000)
        page.wait_for_timeout(600)
        assert_no_history(page, "iframe 内进入活动详情")

        # 现场签到（iframe 内部旧逻辑：控制台→签到塌缩）
        signin = iframe.get_by_text("现场签到")
        if signin.count():
            signin.first.click(timeout=15000)
            iframe.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.page === 'checkin'", timeout=20000)
            page.wait_for_timeout(600)
            assert_no_history(page, "iframe 内进入现场签到")
            iframe.locator(".back-nav").first.click(timeout=15000)
            iframe.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.page === 'group_admin'", timeout=20000)
            page.wait_for_timeout(600)
            assert_no_history(page, "iframe 内从签到返回")
        else:
            iframe.locator(".back-nav").first.click(timeout=15000)
            iframe.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.page === 'group_admin'", timeout=20000)

        click_visible(page, ".embed-back", timeout=15000)
        page.wait_for_timeout(700)
        assert_no_history(page, "关闭 native_admin iframe")
        probe.checkpoint("native_admin iframe")
        print("PASS native_admin iframe：内部导航/签到塌缩可用，顶层 0 次 History 调用")
        assert_errors(errors, "native_admin")
    finally:
        context.close()


def exit_flow(browser) -> None:
    context, page, probe, errors = open_context(browser)
    try:
        clear_hist(page)
        try:
            page.go_back(wait_until="commit", timeout=8000)
        except PlaywrightTimeoutError:
            pass
        page.wait_for_timeout(1500)
        url = page.url
        if url.startswith(BASE):
            # 无上一页可退（例如直接打开）：不拦截也不崩，哨兵应补回
            calls = hist_calls(page)
            if not any(is_sentinel(c) for c in calls):
                raise Failure(f"栈空退出失败：仍在 {url} 且未补回哨兵 {calls}")
            print("PASS 栈空返回：无上一页可退，未崩且哨兵已补回")
        else:
            print(f"PASS 栈空返回：已离开 SPA（{url}）")
        assert_errors(errors, "栈空退出")
    finally:
        context.close()


def main() -> int:
    global BASE, COOKIE
    BASE = require_env("BASE_URL", BASE)
    COOKIE = require_env("SESSION_COOKIE", COOKIE)
    if LOCAL_ASSETS_DIR and not (Path(LOCAL_ASSETS_DIR) / "web/static/legacy/app.min.js").exists():
        sys.exit(f"LOCAL_ASSETS_DIR 无效：{LOCAL_ASSETS_DIR}")
    width, height = parse_viewport(VIEWPORT)
    print(f"no-history smoke: BASE={BASE} viewport={width}x{height} local_assets={LOCAL_ASSETS_DIR or 'off'}")
    with sync_playwright() as p:
        browser = p.chromium.launch()
        try:
            main_flow(browser)
            deep_link_flow(browser)
            legacy_flow(browser, via_query=True)
            legacy_flow(browser, via_query=False)
            observer_flow(browser)
            native_admin_flow(browser)
            exit_flow(browser)
        finally:
            browser.close()
    print("ALL PASS no-history smoke")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Failure as exc:
        print(f"FAIL {exc}", file=sys.stderr)
        sys.exit(1)
