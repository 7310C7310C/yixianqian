#!/usr/bin/env python3
"""PM 2026-10-27 用户端报障回归（Playwright）。

覆盖本批 7 项里可浏览器验证的 1/3/5/7 与第 6 项 UI 断言，390x844 + 390x700：

1. 清除筛选后本人卡回到牌堆首位；筛选期间不出现；后续翻页无重复/不卡死。
3. Android 可视区域溢出：cards tab 文档不超出 innerHeight；.swipe-card 不压底栏；
   --yxq-vh 随视口变化更新；390x844 ↔ 390x700 往返。
5. 点开大图 100ms 内出现加载遮罩，且遮罩在 PhotoSwipe 之上可点击取消；
   原图延迟 2s 后遮罩消失、viewer 可交互关闭、无 console error。
6. 过期活动（mock）显示「已结束」，活动页/我的活动/我的分组不再出现「现场签到」，
   活动详情无「立即报名」入口。
7. 我的→我的资料→修改资料→保存→返回一次到我的，第二次离开 SPA；改密成功同样无残留层。
   virtual 与 ?history=legacy 两种模式都跑；virtual 模式断言栈长度与无原生 History 调用。

用法（staging 部署后）：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
      python3 tools/browser/user_reported_bugs_20261027.py

部署前验证本机生成物（接口仍走 staging，静态资源用本地树）：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=<...> \
    LOCAL_ASSETS_DIR=/root/projects/yixianqian \
      python3 tools/browser/user_reported_bugs_20261027.py

可选：
    VIEWPORTS=390x844,390x700   默认两档
    SHOT_DIR=/tmp/opencode/user-reported-bugs-20261027
    SKIP_PAGINATION=1           跳过第 1 项的连续翻页（数据太少时）
"""

from __future__ import annotations

import mimetypes
import os
import re
import sys
import time
import urllib.request
from pathlib import Path
from urllib.parse import unquote, urljoin, urlparse

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
LOCAL_ASSETS_DIR = os.environ.get("LOCAL_ASSETS_DIR", "").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/user-reported-bugs-20261027"))
VIEWPORTS = os.environ.get("VIEWPORTS", "390x844,390x700").strip()
SKIP_PAGINATION = os.environ.get("SKIP_PAGINATION", "").strip() == "1"
BASE_NETLOC = urlparse(BASE).netloc

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
window.__yxqHist = {calls: []};
(function(){
  var proto = History.prototype;
  ['pushState','replaceState','back','go','forward'].forEach(function(name){
    var orig = proto[name];
    if (!orig) return;
    proto[name] = function(){
      try {
        window.__yxqHist.calls.push({name: name, state: arguments[0] === undefined ? null : arguments[0],
          url: arguments[2] === undefined ? null : arguments[2]});
      } catch(e) {}
      return orig.apply(this, arguments);
    };
  });
})();
try{
  localStorage.setItem('yxq_swipe_guide_v1','1');
  localStorage.setItem('yxq_self_card_guide_v1','1');
  localStorage.setItem('yxq_update_notes_v1','1');
  sessionStorage.setItem('yxq_activity_promo_v1','1');
}catch(e){}
"""

# 与服务端 /app 壳等价的最小本地资源替换：静态文件全部来自 LOCAL_ASSETS_DIR，
# /legacy/api/* 等仍走 BASE_URL。
LOCAL_SHELL = ""


class Failure(AssertionError):
    pass


def require_env(name: str, value: str) -> str:
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def parse_viewports(raw: str) -> list[tuple[int, int]]:
    out = []
    for part in raw.split(","):
        part = part.strip()
        if not part:
            continue
        try:
            w, h = part.lower().split("x", 1)
            out.append((int(w), int(h)))
        except Exception:  # noqa: BLE001
            sys.exit(f"VIEWPORTS 格式应为 390x844,390x700，收到 {part!r}")
    return out


def build_local_shell(root: str) -> str:
    legacy = Path(root) / "web" / "static" / "legacy"
    html = (legacy / "index.html").read_text(encoding="utf-8")
    if "window.__YXQ_AUTHENTICATED__=false;" not in html:
        raise Failure("本地 index.html 缺少登录旗标")
    html = html.replace("window.__YXQ_AUTHENTICATED__=false;", "window.__YXQ_AUTHENTICATED__=true;")
    html = html.replace("__ASSET_URL_VERSION__", "localtest")
    html = html.replace("__ASSET_VERSION_VALUE__", "localtest")
    html = html.replace("__YXQ_VERSION_VALUE__", "localtest")
    css = (legacy / "app.critical.css").read_text(encoding="utf-8")
    html = html.replace("<!--YXQ_CRITICAL_CSS-->", "<style>" + css + "</style>")
    html = html.replace("<!--YXQ_BOOTSTRAP_PRELOAD-->", "")
    return html


def install_local_static(context) -> None:
    legacy = (Path(LOCAL_ASSETS_DIR) / "web" / "static" / "legacy").resolve()

    def handler(route, request):
        try:
            parsed = urlparse(request.url)
            if parsed.netloc != BASE_NETLOC:
                route.fallback()
                return
            if request.resource_type == "document" and parsed.path.rstrip("/") in ("", "/app"):
                route.fulfill(status=200, content_type="text/html; charset=utf-8", body=LOCAL_SHELL)
                return
            if parsed.path.startswith("/static/legacy/"):
                rel = unquote(parsed.path[len("/static/legacy/"):])
                target = (legacy / rel).resolve()
                if legacy not in target.parents and target != legacy:
                    route.fallback()
                    return
                if target.is_file():
                    ctype = mimetypes.guess_type(str(target))[0] or "application/octet-stream"
                    if target.suffix == ".js":
                        ctype = "application/javascript; charset=utf-8"
                    elif target.suffix == ".css":
                        ctype = "text/css; charset=utf-8"
                    elif target.suffix == ".json":
                        ctype = "application/json; charset=utf-8"
                    elif target.suffix == ".html":
                        ctype = "text/html; charset=utf-8"
                    route.fulfill(status=200, content_type=ctype, body=target.read_bytes())
                    return
        except Exception as exc:  # noqa: BLE001
            route.abort()
            raise Failure(f"本地静态资源路由异常 {request.url}: {exc}") from exc
        route.fallback()

    context.route("**/*", handler)


class Probe:
    def __init__(self, page, label: str):
        self.page = page
        self.label = label
        self.errors: list[dict] = []
        page.on("pageerror", lambda exc: self.errors.append({"kind": "pageerror", "text": str(exc)}))
        page.on("console", self._on_console)

    def _on_console(self, msg) -> None:
        if msg.type != "error":
            return
        text = msg.text
        if any(skip in text for skip in IGNORED_CONSOLE):
            return
        self.errors.append({"kind": "console", "text": text,
                            "url": (msg.location or {}).get("url", "")})

    def assert_clean(self, step: str) -> None:
        if self.errors:
            raise Failure(f"{step}: console/pageerror {self.errors[:4]}")


def open_context(browser, viewport: tuple[int, int], *, observer: bool = False,
                 routes=None, history_mode: str = "virtual"):
    width, height = viewport
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.add_init_script(INIT)
    if history_mode == "legacy":
        context.add_init_script("try{localStorage.setItem('yxq_history_mode','legacy')}catch(e){}")
    cookies = [{
        "name": "yxq_session", "value": COOKIE, "url": BASE,
        "httpOnly": True, "sameSite": "Lax",
    }]
    if observer:
        cookies.append({
            "name": "yxq_view_role", "value": "observer", "url": BASE,
            "httpOnly": False, "sameSite": "Lax",
        })
    context.add_cookies(cookies)
    if LOCAL_ASSETS_DIR:
        # 本地壳替换了服务端渲染的文档，但 CSRF cookie 仍由 Go 的 / 响应下发；
        # 先用 APIRequestContext 取一次（不经过 page route），把 yxq_csrf 写进 context。
        try:
            context.request.get(BASE + "/", timeout=30000)
        except Exception:  # noqa: BLE001
            pass
        install_local_static(context)
    if routes:
        routes(context)
    page = context.new_page()
    probe = Probe(page, f"{width}x{height}")
    start = "/?history=legacy" if history_mode == "legacy" else "/"
    page.goto(BASE + start, wait_until="domcontentloaded", timeout=60000)
    wait_app(page)
    return context, page, probe


def wait_app(page, timeout: int = 50000) -> None:
    try:
        page.wait_for_function(
            "window.__YXQ_APP_MOUNTED__ === true && window.yxqRootProxy && "
            "window.yxqRootProxy.bootLoading === false",
            timeout=timeout,
        )
        page.locator("#boot-skeleton").wait_for(state="detached", timeout=timeout)
    except PlaywrightTimeoutError as exc:
        raise Failure("SPA 未完成挂载（__YXQ_APP_MOUNTED__ / bootLoading）") from exc
    page.wait_for_timeout(300)


def proxy(page, expr: str):
    return page.evaluate("() => { const p = window.yxqRootProxy || {}; return (" + expr + "); }")


def wait_page(page, name: str, timeout: int = 20000) -> None:
    try:
        page.wait_for_function(
            "window.yxqRootProxy && window.yxqRootProxy.page === %r" % name,
            timeout=timeout,
        )
    except PlaywrightTimeoutError as exc:
        raise Failure(f"等待 page={name} 超时，当前={proxy(page, 'p.page')!r}") from exc
    page.wait_for_timeout(260)


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
                page.wait_for_timeout(260)
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
                page.wait_for_timeout(220)
                return
        except (PlaywrightTimeoutError, Failure):
            continue
    raise Failure(f"元素不可点击：{selector}")


def tap_tab(page, label: str, key: str) -> None:
    page.locator(".van-tabbar-item").filter(has_text=label).first.click(timeout=12000)
    wait_page(page, key)


def screenshot(page, tag: str, name: str) -> None:
    try:
        SHOT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(SHOT_DIR / f"{tag}-{name}.png"), full_page=False)
    except Exception:  # noqa: BLE001
        pass


def hist_calls(page) -> list[dict]:
    return page.evaluate("window.__yxqHist ? window.__yxqHist.calls : []")


def clear_hist(page) -> None:
    page.evaluate("window.__yxqHist && (window.__yxqHist.calls = [])")


def hist_debug(page) -> dict:
    return page.evaluate("window.__YXQ_HIST_DEBUG__ ? window.__YXQ_HIST_DEBUG__() : {}")


def assert_no_native_history(page, step: str) -> None:
    def sentinel(call: dict) -> bool:
        state = call.get("state") or {}
        return call.get("name") == "pushState" and state.get("yxqSentinel") == 1

    bad = [c for c in hist_calls(page) if not sentinel(c)]
    if bad:
        raise Failure(f"{step}: 出现原生 History 调用 {[(c.get('name'), c.get('state')) for c in bad]}")
    clear_hist(page)


def wait_cards(page, expect_self: bool | None = None) -> dict:
    try:
        page.wait_for_function(
            "window.yxqRootProxy && Array.isArray(window.yxqRootProxy.cards) && "
            "window.yxqRootProxy.cards.length > 0",
            timeout=20000,
        )
    except PlaywrightTimeoutError as exc:
        raise Failure("牌堆为空或未加载") from exc
    if expect_self is not None:
        page.wait_for_function(
            "window.yxqRootProxy && window.yxqRootProxy.cards && "
            "!!window.yxqRootProxy.cards[0] && window.yxqRootProxy.cards[0].is_self === %s"
            % ("true" if expect_self else "false"),
            timeout=20000,
        )
    page.wait_for_timeout(300)
    return {
        "page": proxy(page, "p.page"),
        "cards": proxy(page, "p.cards || []"),
        "total": proxy(page, "p.cardsTotal || 0"),
        "start": proxy(page, "p.cardsStart || 0"),
        "index": proxy(page, "p.cardIndex || 0"),
        "active": proxy(page, "!!p.hasActiveFilter"),
    }


# ---------- 第 1 项：清除筛选后本人卡回首位 + 翻页无重复 ----------

def run_self_card_filter(page, tag: str) -> None:
    tap_tab(page, "牵线", "cards")
    state = wait_cards(page, expect_self=True)
    if not state["cards"][0].get("is_self"):
        raise Failure("初始牌堆首张不是本人卡")
    screenshot(page, tag, "01-self-first")

    click_visible(page, ".filter-btn")
    try:
        page.locator(".van-popup").last.wait_for(state="visible", timeout=5000)
    except PlaywrightTimeoutError as exc:
        raise Failure("第1项 筛选面板未出现") from exc
    page.wait_for_timeout(400)

    gender_row = page.locator(".van-popup .filter-row").filter(has_text="性别")
    gender_used = gender_row.count() > 0
    if gender_used:
        tag_el = gender_row.first.locator(".filter-tag").first
        try:
            tag_el.wait_for(state="visible", timeout=3000)
            tag_el.click()
        except PlaywrightTimeoutError as exc:
            raise Failure("第1项 性别筛选标签不可见") from exc
        page.wait_for_timeout(150)
    else:
        # 普通用户目标性别固定，面板没有性别筛选；用昵称筛选覆盖同一条重载路径。
        nick_input = page.locator(".van-popup .filter-input input").first
        nick_input.fill("__no_such_nickname_20261027__")
        page.wait_for_timeout(150)

    click_text(page, "应用筛选")
    page.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.hasActiveFilter === true",
                           timeout=15000)
    page.wait_for_timeout(500)
    state = proxy(page, "({cards: p.cards || [], total: p.cardsTotal || 0})")
    if any(c.get("is_self") for c in state["cards"]):
        raise Failure("筛选激活时牌堆仍出现本人卡")
    if gender_used and not state["cards"]:
        raise Failure("性别筛选后牌堆为空（预期仍有候选）")
    screenshot(page, tag, "01-filter-active")

    # 重新打开筛选 → 清除筛选
    click_visible(page, ".filter-btn")
    page.wait_for_timeout(350)
    click_text(page, "清除筛选")
    page.wait_for_function("window.yxqRootProxy && window.yxqRootProxy.hasActiveFilter === false",
                           timeout=15000)
    state = wait_cards(page, expect_self=True)
    if not state["cards"][0].get("is_self"):
        raise Failure("清除筛选后本人卡没有回到首位")
    if sum(1 for c in state["cards"] if c.get("is_self")) != 1:
        raise Failure("清除筛选后本人卡重复出现")
    if state["start"] != 0:
        raise Failure(f"清除筛选后 cardsStart={state['start']}，应为 0")
    screenshot(page, tag, "01-self-restored")

    if SKIP_PAGINATION:
        print(f"NOTE {tag} 第1项：SKIP_PAGINATION=1，跳过连续翻页")
        return

    total = int(state["total"] or 0)
    if total <= 1:
        print(f"NOTE {tag} 第1项：牌堆只有 {total} 张，跳过翻页")
        return
    seen = {state["cards"][0].get("openid")}
    steps = min(total - 1, 62)
    for i in range(steps):
        before = proxy(page, "({start: p.cardsStart || 0, index: p.cardIndex || 0})")
        page.wait_for_timeout(140)   # 与 app 的 60ms 连续按键节流错开
        page.keyboard.press("ArrowRight")
        try:
            page.wait_for_function(
                "([s, i]) => { const p = window.yxqRootProxy; return !!p && "
                "(p.cardsStart > s || p.cardIndex > i); }",
                arg=[before["start"], before["index"]],
                timeout=8000,
            )
        except PlaywrightTimeoutError as exc:
            raise Failure(f"第1项翻页在第 {i+1} 步卡死（start={before['start']} index={before['index']}）") from exc
        cur = proxy(page, "p.currentCard && p.currentCard.openid")
        if not cur:
            raise Failure(f"第1项翻页第 {i+1} 步没有当前卡")
        if cur in seen:
            raise Failure(f"第1项翻页重复出现 openid={cur}（第 {i+1} 步）")
        seen.add(cur)
    after = proxy(page, "({start: p.cardsStart || 0, index: p.cardIndex || 0, total: p.cardsTotal || 0})")
    if after["start"] + after["index"] != steps:
        raise Failure(f"第1项翻页位置不连续：期望 {steps}，实际 {after['start'] + after['index']}")
    print(f"PASS {tag} 第1项：本人卡回首位；筛选期间无本人卡；翻页 {steps} 步无重复/不卡死")


# ---------- 第 2 项：人脸裁切随容器重算 ----------

CROP_EXPECTED_JS = r"""
(vh) => {
  const p = window.yxqRootProxy || {};
  const c = p.currentCard;
  if (!c || !c.faces || !c.faces[0]) return null;
  const wrap = document.querySelector('.swipe-card .card-photo-wrap');
  const img = document.querySelector('.swipe-card .card-photo-item');
  if (!wrap || !img || !img.naturalWidth) return null;
  const cardH = Math.min(Math.max(vh - 76, 0), 780);
  const cw = wrap.clientWidth;
  const ch = cardH * 0.48;
  const iw = img.naturalWidth, ih = img.naturalHeight;
  const f = c.faces[0];
  if (!f || !f.c || !cw || !ch || !iw || !ih) return '';
  try {
    const scale = Math.max(cw / iw, ch / ih);
    const dh = ih * scale;
    const tb = (f.tb && f.tb.length === 2) ? f.tb : [0, 0];
    const ct = tb[0], cb = tb[1];
    const cr = 1 - ct - cb;
    if (cr < 0.55) return '';
    const eh = dh * cr;
    const cy_e = (f.c[1] - ct) / cr;
    const fh = (f.box && f.box[1]) ? f.box[1] : 0;
    const fh_e = fh / cr;
    let off_e;
    if (eh <= ch + 1) {
      off_e = (ch - eh) / 2;
    } else {
      const top_e = (cy_e - fh_e / 2) * eh;
      const bottom_e = (cy_e + fh_e / 2) * eh;
      const headTop_e = top_e - fh_e * 0.35 * eh;
      const offDef_e = (ch - eh) / 2;
      if (offDef_e + headTop_e >= 0 && offDef_e + bottom_e <= ch) off_e = offDef_e;
      else if (offDef_e + headTop_e < 0) off_e = -headTop_e;
      else if (offDef_e + bottom_e > ch) off_e = ch - bottom_e;
      else off_e = offDef_e;
      off_e = Math.max(ch - eh, Math.min(0, off_e));
    }
    let off = off_e - ct * dh;
    off = Math.max(ch - dh, Math.min(0, off));
    const offDef = (ch - dh) / 2;
    if (Math.abs(off - offDef) < 1) return '';
    return '50% ' + Math.round(off) + 'px';
  } catch (e) { return ''; }
}
"""


def actual_crop(page) -> str:
    return page.evaluate(
        "() => { const el = document.querySelector('.swipe-card .card-photo-item'); "
        "return el ? (el.style.objectPosition || '') : null; }"
    )


def expected_crop(page, vh: int):
    return page.evaluate(CROP_EXPECTED_JS, vh)


def find_crop_card(page, tag: str) -> int:
    start = proxy(page, "p.cardIndex || 0")
    for i in range(18):
        exp844 = expected_crop(page, 844)
        exp700 = expected_crop(page, 700)
        if exp844 is not None and exp700 is not None and exp844 != exp700:
            return proxy(page, "p.cardIndex || 0")
        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(260)
        if proxy(page, "p.cardIndex || 0") == start and i > 0:
            break
    return -1


def run_face_crop_resize(page, tag: str, base_height: int) -> None:
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    # 本项固定从 844 开始，结束再回到当前视口，保证 390x700 一轮也能断言完整往返。
    if base_height != 844:
        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(500)
    # 找一个在 844/700 两档下裁切值不同的卡（真实数据下第二张通常就满足）。
    idx = find_crop_card(page, tag)
    if idx < 0:
        # 找不到差异也要验证 DOM 与公式一致（不静默跳过）。
        exp = expected_crop(page, 844)
        got = actual_crop(page)
        if exp is not None and exp != got:
            raise Failure(f"第2项 844 高裁切与公式不一致：expected={exp!r} actual={got!r}")
        print(f"NOTE {tag} 第2项：当前账号前 18 张卡两档裁切值相同，仅校验公式一致性")
        if base_height != 844:
            page.set_viewport_size({"width": 390, "height": base_height})
            page.wait_for_timeout(400)
        return

    exp844 = expected_crop(page, 844)
    got = actual_crop(page)
    if exp844 != got:
        raise Failure(f"第2项 844 高裁切不一致：expected={exp844!r} actual={got!r}")
    screenshot(page, tag, "02-crop-844")

    page.set_viewport_size({"width": 390, "height": 700})
    page.wait_for_timeout(700)
    exp700 = expected_crop(page, 700)
    got700 = actual_crop(page)
    if exp700 != got700:
        raise Failure(f"第2项 700 高裁切未重算：expected={exp700!r} actual={got700!r}")
    if exp700 == exp844:
        print(f"NOTE {tag} 第2项：选中卡两档裁切值一致（{exp700!r}）")
    screenshot(page, tag, "02-crop-700")

    page.set_viewport_size({"width": 390, "height": 844})
    page.wait_for_timeout(700)
    got_back = actual_crop(page)
    if got_back != exp844:
        raise Failure(f"第2项 回到 844 后裁切未恢复：expected={exp844!r} actual={got_back!r}")

    page.keyboard.press("ArrowRight")
    page.wait_for_timeout(500)
    page.keyboard.press("ArrowLeft")
    page.wait_for_timeout(700)
    got_switch_back = actual_crop(page)
    if got_switch_back != exp844:
        raise Failure(f"第2项 切走再回来裁切不一致：expected={exp844!r} actual={got_switch_back!r}")
    print(f"PASS {tag} 第2项：844→700→844 裁切随容器重算；切走回来一致"
          f"（844={exp844!r} 700={exp700!r}）")
    if base_height != 844:
        page.set_viewport_size({"width": 390, "height": base_height})
        page.wait_for_timeout(400)


# ---------- 第 3 项：可视区域溢出 ----------

def layout_state(page) -> dict:
    return page.evaluate(
        """() => {
          const se = document.scrollingElement || document.documentElement;
          const rect = (el) => { if (!el) return null; const r = el.getBoundingClientRect();
            return {top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height}; };
          const card = document.querySelector('.swipe-card');
          const bar = document.querySelector('.van-tabbar');
          const buttons = Array.from(document.querySelectorAll('.card-actions button, .card-actions .heart-btn, .card-actions .praise-btn'))
            .filter(el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
          const vars = getComputedStyle(document.documentElement);
          return {
            innerHeight: window.innerHeight,
            innerWidth: window.innerWidth,
            scrollHeight: se.scrollHeight,
            card: rect(card),
            tabbar: rect(bar),
            buttons: buttons.map(rect),
            vh: (vars.getPropertyValue('--yxq-vh') || '').trim(),
            vvBottom: (vars.getPropertyValue('--yxq-vv-bottom') || '').trim(),
            page: window.yxqRootProxy && window.yxqRootProxy.page,
          };
        }"""
    )


def run_layout(page, tag: str, vh_expected: int) -> None:
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    # 本人卡不显示操作按钮；先翻到普通卡再验证按钮在视口内。
    if proxy(page, "p.cardsTotal > 1"):
        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(700)
    page.wait_for_timeout(300)
    st = layout_state(page)
    if not st["card"] or not st["tabbar"]:
        raise Failure("第3项 找不到 .swipe-card 或 .van-tabbar")
    if st["scrollHeight"] > st["innerHeight"] + 1:
        raise Failure(f"第3项 文档溢出：scrollHeight={st['scrollHeight']} innerHeight={st['innerHeight']}")
    if st["card"]["bottom"] > st["tabbar"]["top"] + 1:
        raise Failure(f"第3项 卡片压到底栏：card.bottom={st['card']['bottom']:.1f} "
                      f"tabbar.top={st['tabbar']['top']:.1f}")
    for i, r in enumerate(st["buttons"]):
        if r["bottom"] > st["innerHeight"] + 1 or r["top"] < 0:
            raise Failure(f"第3项 操作按钮 {i} 超出视口：{r} innerHeight={st['innerHeight']}")
    vh_val = float(re.sub(r"[^0-9.]", "", st["vh"] or "0") or 0)
    if abs(vh_val - st["innerHeight"]) > 2:
        raise Failure(f"第3项 --yxq-vh={st['vh']!r} 未跟随 innerHeight={st['innerHeight']}")
    if abs(st["innerHeight"] - vh_expected) > 2:
        raise Failure(f"第3项 视口高度 {st['innerHeight']} != 期望 {vh_expected}")
    screenshot(page, tag, f"03-layout-{vh_expected}")
    print(f"PASS {tag} 第3项：{vh_expected} 高 scrollHeight={st['scrollHeight']}、"
          f"card.bottom={st['card']['bottom']:.0f} ≤ tabbar.top={st['tabbar']['top']:.0f}、"
          f"--yxq-vh={st['vh']}、按钮 {len(st['buttons'])} 个在视口内")


# ---------- 第 5 项：大图加载遮罩 ----------

def mask_center_state(page) -> dict:
    return page.evaluate(
        """() => {
          const el = document.elementFromPoint(Math.round(innerWidth / 2), Math.round(innerHeight / 2));
          return {
            tag: el ? el.tagName : '',
            cls: el ? String(el.className || '') : '',
            inMask: !!(el && el.closest && el.closest('.viewer-loading-mask')),
            maskZ: (() => { const m = document.querySelector('.viewer-loading-mask');
              return m ? getComputedStyle(m).zIndex : ''; })(),
            pswpZ: (() => { const p = document.querySelector('.pswp');
              return p ? getComputedStyle(p).zIndex : ''; })()
          };
        }"""
    )


def arm_viewer_mask_probe(page) -> None:
    page.evaluate(
        """() => {
          window.__yxqMaskSeenAt = null;
          window.__yxqPhotoClickT = null;
          const wrap = document.querySelector('.swipe-card .card-photo-wrap');
          if (!wrap) return;
          const hit = () => { if (window.__yxqPhotoClickT == null) window.__yxqPhotoClickT = performance.now(); };
          wrap.addEventListener('pointerdown', hit, {capture: true, once: true});
          wrap.addEventListener('mousedown', hit, {capture: true, once: true});
          wrap.addEventListener('touchstart', hit, {capture: true, once: true});
          const ob = new MutationObserver(() => {
            if (document.querySelector('.viewer-loading-mask')) {
              window.__yxqMaskSeenAt = performance.now();
              ob.disconnect();
            }
          });
          ob.observe(document.body, {childList: true, subtree: true});
        }"""
    )


def _viewer_open_fallback(page, salt: str) -> str:
    url = proxy(page, "p.currentCard && p.currentCard.photos && p.currentCard.photos[0]")
    if not url:
        raise Failure("第5项 当前卡片没有照片，无法测 fallback")
    sep = "&" if "?" in url else "?"
    url = url + sep + "fallback=" + salt
    page.evaluate("(url) => window.yxqRootProxy.openPhotoViewerForUser({photos:[url], photos_thumb: []})", url)
    return url


def run_viewer_fast_path(page, tag: str) -> None:
    """秒开：卡片缩图先显示，原图延迟 3s 后后台升级。"""
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    # 找一张多图卡，顺带覆盖下一张；找不到就只测当前张。
    multi = False
    for _ in range(15):
        n = proxy(page, "p.currentCard && p.currentCard.photos ? p.currentCard.photos.length : 0")
        if n and n > 1:
            multi = True
            break
        if not proxy(page, "p.cardsTotal > 1"):
            break
        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(250)
    pending = []

    # 等当前卡的缩图真正加载：用户看到卡才点大图；否则 href 缩图要现下载，秒开口径不成立。
    page.wait_for_function(
        """() => { const im = document.querySelector('.swipe-card .card-photo-item');
          return !!(im && im.complete && im.naturalWidth > 0); }""",
        timeout=10000,
    )

    # Playwright route 会让 Chromium 绕过 HTTP 缓存，缩图会被重新请求，秒开口径失真。
    # 预取当前卡缩图字节，route handler 直接 fulfill，模拟用户已看到卡图时的缓存命中。
    thumb_map = {}
    thumbs = proxy(page, "p.currentCard && p.currentCard.photos_thumb ? p.currentCard.photos_thumb : []")
    for thumb in thumbs or []:
        abs_url = urljoin(BASE + "/", thumb)
        try:
            req = urllib.request.Request(abs_url, headers={"Cookie": "yxq_session=" + COOKIE, "User-Agent": "yxq-smoke"})
            with urllib.request.urlopen(req, timeout=20) as resp:
                thumb_map[abs_url] = resp.read()
        except Exception:  # noqa: BLE001
            pass

    def handler(route, request):
        parsed = urlparse(request.url)
        if parsed.path.startswith("/cards/photo/") and "size=thumb" in (parsed.query or ""):
            if request.url in thumb_map:
                route.fulfill(status=200, content_type="image/jpeg", body=thumb_map[request.url],
                              headers={"Cache-Control": "private, max-age=86400, immutable"})
                return
            route.fallback()
            return
        if "/cards/photo/" in parsed.path and "size=thumb" not in (parsed.query or ""):
            pending.append(route)
            return
        route.fallback()

    page.route("**/cards/photo/**", handler)

    def release() -> None:
        for route in pending:
            try:
                route.continue_()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()

    try:
        t0 = time.monotonic()
        page.locator(".swipe-card .card-photo-wrap").first.click(timeout=12000)
        try:
            page.wait_for_function(
                """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content;
                  const e = c && c.element; return !!(p && p.element &&
                  getComputedStyle(p.element).display !== 'none' && e && e.naturalWidth > 0); }""",
                timeout=3000,
            )
        except PlaywrightTimeoutError as exc:
            raise Failure("第5项 秒开失败：pswp/缩图未在 3s 内可见") from exc
        open_ms = (time.monotonic() - t0) * 1000
        try:
            page.wait_for_function("() => !document.querySelector('.viewer-loading-mask')", timeout=3000)
        except PlaywrightTimeoutError as exc:
            raise Failure("第5项 缩图已显示但遮罩未清除") from exc
        mask_ms = (time.monotonic() - t0) * 1000
        st = page.evaluate(
            """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content; const e = c && c.element;
              return {show: !!(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer),
                      mask: !!document.querySelector('.viewer-loading-mask'),
                      src: e ? e.src : '', nw: e ? e.naturalWidth : 0, full: e ? (e.dataset.yxqFull || '') : ''}; }"""
        )
        if not st["show"] or st["mask"] or not st["nw"]:
            raise Failure(f"第5项 秒开状态异常：{st}")
        if "size=thumb" not in st["src"]:
            raise Failure(f"第5项 初始应显示缩图，实际 src={st['src']!r}")
        if open_ms > 300 or mask_ms > 300:
            raise Failure(f"第5项 秒开超时：pswp={open_ms:.0f}ms mask={mask_ms:.0f}ms")
        screenshot(page, tag, "05-fast-thumb")
        print(f"PASS {tag} 第5项秒开：{open_ms:.0f}ms 可见缩图、{mask_ms:.0f}ms 无遮罩")

        # 原生 close 语义：等 opening 动画结束（opener.isOpen）后点关闭，1s 内收起。
        page.wait_for_function(
            "() => window.pswp && window.pswp.opener && window.pswp.opener.isOpen === true",
            timeout=2500,
        )
        page.locator(".pswp__button--close").first.click(timeout=3000)
        page.wait_for_function(
            "() => !(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer) && "
            "(() => { const p = document.querySelector('.pswp'); return !p || getComputedStyle(p).display === 'none'; })()",
            timeout=1200,
        )
        print(f"PASS {tag} 第5项fast：opening 结束后关闭，1s 内收起")

        # 重新打开，继续验证 3s 原图升级与下一张。
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        page.wait_for_timeout(250)
        t0 = time.monotonic()
        page.locator(".swipe-card .card-photo-wrap").first.click(timeout=12000)
        page.wait_for_function(
            """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content;
              const e = c && c.element; return !!(p && p.element &&
              getComputedStyle(p.element).display !== 'none' && e && e.naturalWidth > 0); }""",
            timeout=3000,
        )
        page.wait_for_function("() => !document.querySelector('.viewer-loading-mask')", timeout=3000)

        # 原图延迟到 3s 后放行，再升级 src。
        while time.monotonic() < t0 + 3.0:
            page.wait_for_timeout(50)
        release()
        try:
            page.wait_for_function(
                """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content;
                  return !!(c && c.element && c.element.dataset.yxqFull === '1'); }""",
                timeout=8000,
            )
        except PlaywrightTimeoutError as exc:
            raise Failure("第5项 原图后台升级未完成") from exc
        full = page.evaluate(
            """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content; const e = c && c.element;
              return {src: e ? e.src : '', nw: e ? e.naturalWidth : 0, full: e ? (e.dataset.yxqFull || '') : ''}; }"""
        )
        if full["full"] != "1" or "size=thumb" in full["src"] or not full["nw"]:
            raise Failure(f"第5项 原图升级状态异常：{full}")
        print(f"PASS {tag} 第5项原图升级：3s 后 src 换原图、naturalWidth={full['nw']}")

        # 下一张：缩图即时显示，原图后台升级。
        if multi:
            before = page.evaluate("() => window.pswp ? window.pswp.currIndex : -1")
            page.evaluate("() => window.pswp && window.pswp.next()")
            page.wait_for_function(
                "idx => window.pswp && window.pswp.currIndex !== idx", arg=before, timeout=5000,
            )
            page.wait_for_function(
                """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content;
                  const e = c && c.element; return !!(e && e.naturalWidth > 0); }""",
                timeout=5000,
            )
            next_st = page.evaluate(
                """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content; const e = c && c.element;
                  return {idx: p ? p.currIndex : -1, src: e ? e.src : '', nw: e ? e.naturalWidth : 0}; }"""
            )
            if not next_st["nw"]:
                raise Failure(f"第5项 下一张缩图未显示：{next_st}")
            release()
            page.wait_for_function(
                """() => { const p = window.pswp; const c = p && p.currSlide && p.currSlide.content;
                  return !!(c && c.element && c.element.dataset.yxqFull === '1'); }""",
                timeout=8000,
            )
            print(f"PASS {tag} 第5项下一张：缩图即时显示，原图后台升级")

        close_btn = page.locator(".pswp__button--close").first
        if not close_btn.is_visible():
            raise Failure("第5项 秒开路径关闭按钮不可见")
        close_btn.click(timeout=5000)
        page.wait_for_function(
            "() => !(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer)",
            timeout=5000,
        )
    finally:
        release()
        page.unroute("**/cards/photo/**", handler)


def run_viewer_fallback_mask(page, tag: str) -> None:
    """无缩图（海报/fallback）仍显示遮罩；点击可取消，随后可正常打开。"""
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    pending = []

    def handler(route, request):
        parsed = urlparse(request.url)
        if "/cards/photo/" in parsed.path and "size=thumb" not in (parsed.query or ""):
            pending.append(route)
            return
        route.fallback()

    page.route("**/cards/photo/**", handler)
    try:
        _viewer_open_fallback(page, "maskcancel")
        page.wait_for_function("() => !!document.querySelector('.viewer-loading-mask')", timeout=3000)
        st = mask_center_state(page)
        if not st["inMask"]:
            raise Failure(f"第5项 无缩图 fallback 遮罩被遮挡：{st}")
        vp = page.viewport_size or {"width": 390, "height": 844}
        page.mouse.click(vp["width"] // 2, vp["height"] // 2)
        page.wait_for_function(
            "() => !document.querySelector('.viewer-loading-mask') && "
            "!(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer)",
            timeout=5000,
        )
        page.locator(".pswp").first.wait_for(state="hidden", timeout=5000)
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        print(f"PASS {tag} 第5项fallback：无缩图时遮罩可见、点击取消成功")

        # fallback 正常关闭：先移除拦截 handler 让原图完成、遮罩消失，
        # 等 opening 动画结束后再点原生 close。
        page.unroute("**/cards/photo/**", handler)
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        cached = proxy(page, "p.currentCard && p.currentCard.photos && p.currentCard.photos[0]")
        if not cached:
            raise Failure("第5项 当前卡片没有照片，无法测 fallback 立即关闭")
        # 预热原图到浏览器缓存，确保 fallback（无缩图）打开后遮罩能快速消失。
        page.evaluate(
            """(url) => new Promise((resolve) => {
              const img = new Image();
              img.onload = () => resolve(true);
              img.onerror = () => resolve(false);
              img.src = url;
            })""",
            cached,
        )
        page.evaluate(
            "(url) => window.yxqRootProxy.openPhotoViewerForUser({photos:[url], photos_thumb: []})",
            cached,
        )
        page.wait_for_function(
            "() => { const p = window.pswp; const e = p && p.element; "
            "return !!(e && getComputedStyle(e).display !== 'none'); }",
            timeout=3000,
        )
        page.wait_for_function("() => !document.querySelector('.viewer-loading-mask')", timeout=3000)
        page.wait_for_function(
            "() => window.pswp && window.pswp.opener && window.pswp.opener.isOpen === true",
            timeout=2500,
        )
        page.locator(".pswp__button--close").first.click(timeout=3000)
        page.wait_for_function(
            "() => !(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer) && "
            "(() => { const p = document.querySelector('.pswp'); return !p || getComputedStyle(p).display === 'none'; })()",
            timeout=1500,
        )
        print(f"PASS {tag} 第5项fallback：opening 结束后关闭，1s 内收起")

        # 重新挂回拦截 handler，保留后面的正常 fallback 打开/关闭用例。
        page.route("**/cards/photo/**", handler)
        _viewer_open_fallback(page, "fallbackok")
        page.wait_for_function("() => !!document.querySelector('.viewer-loading-mask')", timeout=3000)
        for _ in range(20):
            if pending:
                break
            page.wait_for_timeout(50)
        for route in pending:
            try:
                route.continue_()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        page.wait_for_function(
            "() => !document.querySelector('.viewer-loading-mask') && "
            "window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer === true",
            timeout=10000,
        )
        page.wait_for_function(
            "() => { const p = window.pswp; return !!(p && p.opener && p.opener.isOpen); }",
            timeout=5000,
        )
        close_btn = page.locator(".pswp__button--close").first
        if not close_btn.is_visible():
            raise Failure("第5项 fallback 成功路径关闭按钮不可见")
        close_btn.click(timeout=5000)
        page.wait_for_function(
            "() => !(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer)",
            timeout=5000,
        )
        print(f"PASS {tag} 第5项fallback：无缩图正常打开并关闭")
    finally:
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        page.unroute("**/cards/photo/**", handler)


def run_viewer_cancel_race(page, tag: str) -> None:
    """取消后 100/200/300ms 内再点图：旧 abort destroy timer 不得杀掉新 pswp。"""
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    pending = []

    def handler(route, request):
        parsed = urlparse(request.url)
        if "/cards/photo/" in parsed.path and "size=thumb" not in (parsed.query or ""):
            pending.append(route)
            return
        route.fallback()

    page.route("**/cards/photo/**", handler)

    def cancel_mask() -> None:
        vp = page.viewport_size or {"width": 390, "height": 844}
        page.mouse.click(vp["width"] // 2, vp["height"] // 2)
        page.wait_for_function(
            "() => !document.querySelector('.viewer-loading-mask') && "
            "!(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer)",
            timeout=5000,
        )
        page.locator(".pswp").first.wait_for(state="hidden", timeout=3000)

    def release_pending() -> None:
        for route in pending:
            try:
                route.continue_()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()

    def abort_pending() -> None:
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()

    delays = (100, 200, 300)
    try:
        for delay in delays:
            pending.clear()
            _viewer_open_fallback(page, f"race{delay}a")
            page.wait_for_function("() => !!document.querySelector('.viewer-loading-mask')", timeout=3000)
            cancel_mask()
            abort_pending()
            page.wait_for_timeout(delay)

            # 旧 abort timer 的 250ms 边界：重新打开必须创建新 pswp 且不被旧 timer 误杀。
            _viewer_open_fallback(page, f"race{delay}b")
            page.wait_for_function(
                "() => !!document.querySelector('.viewer-loading-mask')",
                timeout=5000,
            )
            # 缓存命中时不会再有原图请求，等遮罩由 loadComplete/已加载兜底清除即可。
            for _ in range(80):
                if pending or not page.locator(".viewer-loading-mask").count():
                    break
                page.wait_for_timeout(50)
            release_pending()
            page.wait_for_function(
                "() => !document.querySelector('.viewer-loading-mask')",
                timeout=15000,
            )
            page.wait_for_timeout(500)   # 跨过旧 abort timer 的 250ms
            st = page.evaluate(
                """() => { const p = document.querySelector('.pswp'); const s = p ? getComputedStyle(p) : null;
                  return {show: !!(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer),
                          display: s ? s.display : null,
                          cls: p ? String(p.className || '') : null,
                          mask: !!document.querySelector('.viewer-loading-mask')}; }"""
            )
            if not st["show"] or not st["cls"] or st["display"] == "none" or st["mask"]:
                raise Failure(f"第5项 取消后 {delay}ms 重开失败：{st}")
            if "yxq-viewer-cancelled" in st["cls"]:
                raise Failure(f"第5项 取消后 {delay}ms 重开仍带取消 class：{st}")
            close_btn = page.locator(".pswp__button--close").first
            if not close_btn.is_visible():
                raise Failure(f"第5项 取消后 {delay}ms 重开 close 按钮不可见")
            close_btn.click(timeout=5000)
            page.wait_for_function(
                "() => !(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer)",
                timeout=5000,
            )
            page.locator(".pswp").first.wait_for(state="hidden", timeout=5000)
            page.wait_for_timeout(200)
            print(f"PASS {tag} 第5项取消竞态：{delay}ms 后重开正常（pswp 可见、mask 清除、可关闭）")
    finally:
        abort_pending()
        page.unroute("**/cards/photo/**", handler)


def run_viewer_timeout(page, tag: str) -> None:
    """10s timeout 走同一取消路径；仅在单个视口跑一次，避免双倍等待。"""
    tap_tab(page, "牵线", "cards")
    wait_cards(page, expect_self=True)
    pending = []

    def handler(route, request):
        parsed = urlparse(request.url)
        if "/cards/photo/" in parsed.path and "size=thumb" not in (parsed.query or ""):
            pending.append(route)
            return
        route.fallback()

    page.route("**/cards/photo/**", handler)
    # 无缩图 fallback + 唯一 query，确保 10s 内一直 loading。
    try:
        _viewer_open_fallback(page, "timeout")
        page.wait_for_function("() => !!document.querySelector('.viewer-loading-mask')", timeout=3000)
        page.wait_for_function(
            "() => !document.querySelector('.viewer-loading-mask')",
            timeout=13000,
        )
        page.wait_for_timeout(300)
        st = page.evaluate(
            """() => { const p = document.querySelector('.pswp'); const s = p ? getComputedStyle(p) : null;
              return {show: !!(window.yxqRootProxy && window.yxqRootProxy.showPhotoViewer),
                      display: s ? s.display : null, cls: p ? String(p.className || '') : null}; }"""
        )
        if st["show"] or (st["display"] is not None and st["display"] != "none"):
            raise Failure(f"第5项 10s timeout 未隐藏 viewer：{st}")
        print(f"PASS {tag} 第5项 timeout：10s 后遮罩消失且 pswp 已隐藏")
    finally:
        for route in pending:
            try:
                route.abort()
            except Exception:  # noqa: BLE001
                pass
        pending.clear()
        page.unroute("**/cards/photo/**", handler)


# ---------- 第 6 项：过期活动 UI ----------

EXPIRED_ACT = {
    "record_id": "A-EXPIRED-20261027",
    "activity_id": "A-EXPIRED-20261027",
    "name": "过期活动回归样例",
    "status": "已结束",
    "start_time": "2026-10-01 09:00",
    "end_time": "2026-10-01 12:00",
    "location": "测试地点",
    "description": "PM 报障回归用",
    "fee": 0,
    "max_signup": 20,
    "current_signup": 10,
    "my_signup": True,
    "signin_enabled": True,
    "can_self_signin": False,
    "my_attendance": "未签到",
    "group_flag": "是",
    "group_status": "已完成",
    "onetoone_flag": "否",
    "onetoone_status": "",
    "male_per_group": 4,
    "female_per_group": 4,
    "my_choice_count": 3,
    "poster": "",
}


def install_activity_mock(context) -> None:
    def json_route(route, payload):
        route.fulfill(status=200, content_type="application/json; charset=utf-8",
                      body=__import__("json").dumps(payload, ensure_ascii=False))

    def handler(route, request):
        parsed = urlparse(request.url)
        path = parsed.path
        try:
            if path == "/legacy/api/activities":
                json_route(route, {"activities": [EXPIRED_ACT]})
                return
            if path == "/legacy/api/activities/mine":
                json_route(route, {"activities": [EXPIRED_ACT]})
                return
            if path == "/legacy/api/activities/mine/groups":
                json_route(route, {"activities": [EXPIRED_ACT]})
                return
            if path == "/legacy/api/activities/mine/groups/flag":
                json_route(route, {"has_group_activity": True})
                return
            if re.fullmatch(r"/legacy/api/activities/[^/]+", path):
                json_route(route, EXPIRED_ACT)
                return
        except Exception as exc:  # noqa: BLE001
            raise Failure(f"活动 mock 路由异常 {request.url}: {exc}") from exc
        route.fallback()

    context.route("**/legacy/api/activities**", handler)


def assert_no_signin_ui(page, where: str) -> None:
    if page.get_by_text("📋 现场签到").count():
        if page.get_by_text("📋 现场签到").first.is_visible():
            raise Failure(f"{where} 仍出现「现场签到」按钮")
    if page.get_by_text("立即报名").count():
        if page.get_by_text("立即报名").first.is_visible():
            raise Failure(f"{where} 仍出现「立即报名」入口")


def run_expired_activity(browser, viewport: tuple[int, int], tag: str) -> None:
    context, page, probe = open_context(browser, viewport, routes=install_activity_mock)
    try:
        tap_tab(page, "活动", "home")
        page.get_by_text("过期活动回归样例").first.wait_for(state="visible", timeout=15000)
        card = page.locator(".activity-card").filter(has_text="过期活动回归样例").first
        if "已结束" not in card.inner_text():
            raise Failure("第6项 活动列表卡片未显示「已结束」")
        assert_no_signin_ui(page, "第6项 活动列表")
        screenshot(page, tag, "06-expired-home")

        card.click()
        wait_page(page, "activity_detail")
        page.get_by_text("过期活动回归样例").first.wait_for(state="visible", timeout=15000)
        if "已结束" not in page.locator(".activity-card").first.inner_text():
            raise Failure("第6项 活动详情未显示「已结束」")
        assert_no_signin_ui(page, "第6项 活动详情")
        if page.locator(".signin-status-box").count() and page.locator(".signin-status-box").first.is_visible():
            raise Failure("第6项 活动详情仍显示签到状态框")
        screenshot(page, tag, "06-expired-detail")

        page.evaluate("window.yxqRootProxy.openMyActivities()")
        wait_page(page, "my_activities")
        page.get_by_text("过期活动回归样例").first.wait_for(state="visible", timeout=15000)
        if "已结束" not in page.locator(".activity-card").first.inner_text():
            raise Failure("第6项 我的活动未显示「已结束」")
        assert_no_signin_ui(page, "第6项 我的活动")

        page.evaluate("window.yxqRootProxy.openMyGroups()")
        wait_page(page, "my_groups")
        page.get_by_text("过期活动回归样例").first.wait_for(state="visible", timeout=15000)
        body = page.locator(".activity-card").first.inner_text()
        if "未签到" in body or "未到" in body:
            raise Failure("第6项 我的分组仍显示签到状态标签")
        assert_no_signin_ui(page, "第6项 我的分组")
        screenshot(page, tag, "06-expired-my-groups")
        probe.assert_clean("第6项")
        print(f"PASS {tag} 第6项：过期活动显示已结束；活动/我的活动/我的分组无签到入口；详情无报名入口")
    finally:
        context.close()


# ---------- 第 7 项：资料保存返回链 ----------

def install_password_mock(page) -> None:
    def handler(route, request):
        if request.method != "POST":
            route.fallback()
            return
        route.fulfill(status=200, content_type="application/json; charset=utf-8",
                      body='{"message":"密码已更新"}')

    page.route("**/legacy/api/password", handler)


def stack_info(page, step: str) -> dict:
    info = hist_debug(page)
    if not info:
        raise Failure(f"{step}: window.__YXQ_HIST_DEBUG__ 不可用")
    return info


def assert_left_app(page, step: str) -> None:
    page.wait_for_timeout(800)
    try:
        url = page.url
    except Exception:  # noqa: BLE001
        url = ""
    if url.startswith(BASE):
        page_name = proxy(page, "p.page") if page.evaluate("!!window.yxqRootProxy") else ""
        raise Failure(f"{step}: 第二次返回仍在 SPA（url={url} page={page_name!r}），历史仍多余层")


def run_password_back_chain(page, tag: str, mode: str) -> None:
    install_password_mock(page)
    tap_tab(page, "我的", "profile")
    before = stack_info(page, "改密前")
    click_text(page, "修改密码")
    wait_page(page, "password")
    during = stack_info(page, "改密页")
    if mode == "virtual" and during.get("length") != before.get("length", 0) + 1:
        raise Failure(f"第7项改密 进入 password 栈长度异常：before={before} during={during}")
    fields = page.locator(".password-page input")
    fields.nth(0).fill("current-pass")
    fields.nth(1).fill("new-pass-123")
    fields.nth(2).fill("new-pass-123")
    click_text(page, "确认修改")
    wait_page(page, "profile")
    page.wait_for_timeout(300)
    after = stack_info(page, "改密后")
    if mode == "virtual":
        if after.get("length") != before.get("length"):
            raise Failure(f"第7项改密 保存后栈长度未回到进 password 前：before={before} after={after}")
        if any((s or {}).get("page") == "password" for s in after.get("stack") or []):
            raise Failure(f"第7项改密 栈内残留 password：{after}")
    screenshot(page, tag, f"07-password-{mode}")
    print(f"PASS {tag} 第7项改密[{mode}]：保存后无 password 残留层")


def run_profile_back_chain(page, tag: str, mode: str) -> None:
    tap_tab(page, "我的", "profile")
    clear_hist(page)
    base = stack_info(page, "资料前")
    click_text(page, "我的资料")
    wait_page(page, "profile_detail")
    detail = stack_info(page, "资料详情")
    click_text(page, "修改资料")
    wait_page(page, "profile_edit")
    edit = stack_info(page, "资料编辑")
    if mode == "virtual":
        if detail.get("length") != base.get("length", 0) + 1:
            raise Failure(f"第7项 进入我的资料栈长度异常：base={base} detail={detail}")
        if edit.get("length") != detail.get("length", 0) + 1:
            raise Failure(f"第7项 进入编辑栈长度异常：detail={detail} edit={edit}")

    click_text(page, "保存")
    try:
        page.wait_for_function(
            "window.yxqRootProxy && window.yxqRootProxy.page === 'profile_detail'",
            timeout=30000,
        )
    except PlaywrightTimeoutError as exc:
        raise Failure(f"第7项 保存后未回到我的资料，当前={proxy(page, 'p.page')!r}") from exc
    page.wait_for_timeout(600)
    if proxy(page, "p.page") != "profile_detail":
        raise Failure(f"第7项 保存后页面={proxy(page, 'p.page')!r}，期望 profile_detail")
    saved = stack_info(page, "保存后")
    if mode == "virtual":
        if saved.get("length") != detail.get("length"):
            raise Failure(f"第7项 保存后栈长度应回到进 edit 前（{detail}），实际 {saved}")
        stack = saved.get("stack") or []
        if sum(1 for s in stack if (s or {}).get("page") == "profile_detail") != 1:
            raise Failure(f"第7项 保存后栈内 profile_detail 重复：{saved}")
        if any((s or {}).get("page") == "profile_edit" for s in stack):
            raise Failure(f"第7项 保存后栈内残留 profile_edit：{saved}")
        assert_no_native_history(page, "第7项 保存")
    screenshot(page, tag, f"07-after-save-{mode}")

    click_visible(page, ".back-nav")
    wait_page(page, "profile")
    if proxy(page, "p.page") != "profile":
        raise Failure(f"第7项 第一次返回落点={proxy(page, 'p.page')!r}，期望 profile")
    after_back = stack_info(page, "第一次返回")
    if mode == "virtual":
        if any((s or {}).get("page") in ("profile_edit", "profile_detail") for s in after_back.get("stack") or []):
            raise Failure(f"第7项 第一次返回后栈内仍有 edit/detail：{after_back}")
    screenshot(page, tag, f"07-back-profile-{mode}")

    # 第二次硬件返回：没有重复 detail 时应离开 SPA（或至少不回到 cards/detail）。
    try:
        page.go_back(wait_until="domcontentloaded", timeout=8000)
    except PlaywrightTimeoutError:
        pass
    assert_left_app(page, "第7项 第二次返回")
    print(f"PASS {tag} 第7项[{mode}]：保存后栈无重复 detail；返回一次到我的，第二次离开 SPA")


def run_password_and_profile(page, tag: str, mode: str) -> None:
    run_password_back_chain(page, tag, mode)
    run_profile_back_chain(page, tag, mode)


# ---------- main ----------

def main() -> int:
    require_env("BASE_URL", BASE)
    require_env("SESSION_COOKIE", COOKIE)
    global LOCAL_SHELL
    if LOCAL_ASSETS_DIR:
        root = Path(LOCAL_ASSETS_DIR)
        if not (root / "web/static/legacy/app.min.js").exists():
            sys.exit(f"LOCAL_ASSETS_DIR 无效：{LOCAL_ASSETS_DIR}")
        LOCAL_SHELL = build_local_shell(LOCAL_ASSETS_DIR)

    viewports = parse_viewports(VIEWPORTS)
    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=True)
        for width, height in viewports:
            tag = f"{width}x{height}"
            print(f"\n=== {tag} ===")
            # 第 1 项：observer 视角才有性别筛选；同一重载路径。
            context = page = probe = None
            try:
                context, page, probe = open_context(browser, (width, height), observer=True)
                run_self_card_filter(page, tag)
                probe.assert_clean("第1项")
            except Exception as exc:  # noqa: BLE001
                failures.append(f"{tag} 第1项: {exc}")
                print(f"FAIL {tag} 第1项：{exc}")
                if page:
                    screenshot(page, tag, "fail-01")
            finally:
                if context:
                    context.close()

            # 第 2/3/5/7 项：普通视角；同一页面内依次执行，最后验证一次布局。
            context = page = probe = None
            try:
                context, page, probe = open_context(browser, (width, height))
                run_layout(page, tag, height)
                run_face_crop_resize(page, tag, height)
                run_viewer_fast_path(page, tag)
                run_viewer_fallback_mask(page, tag)
                run_viewer_cancel_race(page, tag)
                if tag == "390x844":
                    run_viewer_timeout(page, tag)
                run_password_and_profile(page, tag, "virtual")
                probe.assert_clean("第2/3/5/7项[virtual]")
            except Exception as exc:  # noqa: BLE001
                failures.append(f"{tag} 第2/3/5/7项: {exc}")
                print(f"FAIL {tag} 第2/3/5/7项：{exc}")
                if page:
                    screenshot(page, tag, "fail-2357")
            finally:
                if context:
                    context.close()

            # 第 7 项 legacy 真实历史模式：B1 同一案例必须同样不多层。
            context = page = probe = None
            try:
                context, page, probe = open_context(browser, (width, height), history_mode="legacy")
                run_password_and_profile(page, tag, "legacy")
                probe.assert_clean("第7项[legacy]")
            except Exception as exc:  # noqa: BLE001
                failures.append(f"{tag} 第7项[legacy]: {exc}")
                print(f"FAIL {tag} 第7项[legacy]：{exc}")
                if page:
                    screenshot(page, tag, "fail-07-legacy")
            finally:
                if context:
                    context.close()

            # 第 6 项：mock 过期活动。
            try:
                run_expired_activity(browser, (width, height), tag)
            except Exception as exc:  # noqa: BLE001
                failures.append(f"{tag} 第6项: {exc}")
                print(f"FAIL {tag} 第6项：{exc}")
        browser.close()

    print("\n=== 汇总 ===")
    if failures:
        for item in failures:
            print(f"FAIL {item}")
        print(f"截图目录：{SHOT_DIR}")
        return 1
    print(f"PASS 全部通过（{len(viewports)} 个视口）；截图目录：{SHOT_DIR}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
