#!/usr/bin/env python3
"""改密页进入闪现回归（Playwright，390/1280）。

验收口径（PM 2026-10-27，Android Via）：
- 在「我的」页滚到「修改密码」后点击，改密页可见之前旧「我的」页不得被提前滚到顶部；
- 改密页稳定后 scrollY=0、表单可见；
- 返回「我的」后滚动位置还原到点击前（±2px）；
- 全程无 console error/pageerror、无主框架文档导航。

可证伪点：Transition 是 out-in，离场期间旧「我的」页仍在 DOM 且可见；旧实现
openPasswordPage() 里 nextTick 同步 scrollTo(0,0) 会把旧页闪到顶部。本脚本在页内用
requestAnimationFrame（≈16ms）逐帧记录 scrollY / page / 旧页 .profile-header 位置与
opacity / 新页 .password-page opacity，命中「旧页仍可见（opacity>0.35）且 scrollY<=2」
即 FAIL。改前代码应 FAIL，改后应 PASS。

用法（staging 真实链路）：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=yxq_session=<token> \
      python3 tools/browser/password_no_flash_smoke.py

部署前验证本机生成物：
    LOCAL_ASSETS_DIR=/root/wt/yxq-pw-flash \
    BASE_URL=https://next.nantou.love SESSION_COOKIE=yxq_session=<token> \
      python3 tools/browser/password_no_flash_smoke.py

可选：
    VIEWPORT=390x844   只跑单视口（默认 390x844,1280x900）
    SHOT_DIR=<dir>     截图与 timeline JSON 目录
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
LOCAL_ASSETS_DIR = os.environ.get("LOCAL_ASSETS_DIR", "").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/password-no-flash"))
VIEWPORTS = os.environ.get("VIEWPORT", "390x844,1280x900").strip()
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
try{
  localStorage.setItem('yxq_swipe_guide_v1','1');
  localStorage.setItem('yxq_self_card_guide_v1','1');
  localStorage.setItem('yxq_update_notes_v1','1');
  sessionStorage.setItem('yxq_activity_promo_v1','1');
}catch(e){}
"""

# 页内逐帧记录：click 前安装，等改密页稳定后 stop。
RECORDER = r"""
window.__pwRec = {frames: [], stop: false, t0: performance.now()};
(function rec(){
  var r = window.__pwRec;
  if (!r || r.stop) return;
  try {
    var proxy = window.yxqRootProxy || {};
    var root = document.querySelector('.profile-header');
    var prof = root ? root.parentElement : null;
    var pw = document.querySelector('.password-page');
    function opacity(el){
      if (!el) return null;
      var s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') return 0;
      var v = parseFloat(s.opacity);
      return isNaN(v) ? 1 : v;
    }
    var topEl = document.elementFromPoint(Math.round(innerWidth / 2), Math.min(80, Math.round(innerHeight / 2)));
    var topText = topEl ? String(topEl.innerText || topEl.textContent || '').trim().slice(0, 24) : '';
    r.frames.push({
      t: Math.round(performance.now() - r.t0),
      y: Math.round((window.scrollY || 0) * 100) / 100,
      page: proxy.page || '',
      profile: !!root,
      profileTop: root ? Math.round(root.getBoundingClientRect().top * 10) / 10 : null,
      profileOpacity: opacity(prof),
      password: !!pw,
      passwordOpacity: opacity(pw),
      topText: topText,
      docH: Math.round((document.scrollingElement && document.scrollingElement.scrollHeight) || 0)
    });
  } catch (e) {
    r.frames.push({error: String(e)});
  }
  requestAnimationFrame(rec);
})();
"""

STOP_RECORDER = r"""
window.__pwRec && (window.__pwRec.stop = true);
JSON.stringify(window.__pwRec ? window.__pwRec.frames : []);
"""


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
            sys.exit(f"VIEWPORT 格式应为 390x844，收到 {part!r}")
    if not out:
        sys.exit("VIEWPORT 为空")
    return out


def cookie_value(raw: str) -> str:
    return raw.split("=", 1)[1].strip() if raw.startswith("yxq_session=") else raw


def build_local_arm():
    repo = Path(LOCAL_ASSETS_DIR)
    legacy = repo / "web/static/legacy"
    shell = (legacy / "index.html").read_text(encoding="utf-8")
    marker = "window.__YXQ_AUTHENTICATED__=false;"
    if marker not in shell:
        raise Failure(f"{legacy / 'index.html'} 缺少 {marker}")
    from local_assets import Arm  # noqa: PLC0415 - 依赖脚本同目录

    return Arm(
        name="local",
        shell_html=shell.replace(marker, "window.__YXQ_AUTHENTICATED__=true;"),
        script_name="app.min.js",
        script_bytes=(legacy / "app.min.js").read_bytes(),
    )


def attach_errors(page) -> list[dict]:
    errors: list[dict] = []

    def on_pageerror(exc):
        errors.append({"kind": "pageerror", "text": str(exc)})

    def on_console(msg):
        if msg.type != "error":
            return
        if any(skip in msg.text for skip in IGNORED_CONSOLE):
            return
        loc = msg.location or {}
        errors.append({"kind": "console", "text": msg.text, "url": loc.get("url", "")})

    page.on("pageerror", on_pageerror)
    page.on("console", on_console)
    return errors


def wait_app(page, timeout: int = 40000) -> None:
    try:
        page.wait_for_function("window.__YXQ_APP_MOUNTED__ === true", timeout=timeout)
        page.locator("#boot-skeleton").wait_for(state="detached", timeout=timeout)
    except PlaywrightTimeoutError as exc:
        raise Failure("SPA 未完成挂载（__YXQ_APP_MOUNTED__ / #boot-skeleton）") from exc
    page.wait_for_timeout(500)


def page_name(page) -> str:
    try:
        return page.evaluate("window.yxqRootProxy && window.yxqRootProxy.page")
    except Exception:  # noqa: BLE001
        return ""


def wait_page(page, name: str, timeout: int = 15000) -> None:
    try:
        page.wait_for_function(
            "window.yxqRootProxy && window.yxqRootProxy.page === %r" % name,
            timeout=timeout,
        )
    except PlaywrightTimeoutError as exc:
        raise Failure(f"等待 page={name} 超时，当前={page_name(page)!r}") from exc


def snapshot(page, name: str) -> None:
    try:
        SHOT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(SHOT_DIR / f"{name}.png"), full_page=False)
    except Exception:  # noqa: BLE001
        pass


def open_profile(page) -> None:
    page.locator(".van-tabbar-item").filter(has_text="我的").first.click(timeout=12000)
    wait_page(page, "profile")
    page.locator(".profile-header").first.wait_for(state="visible", timeout=15000)
    page.wait_for_timeout(350)


def scroll_password_entry(page) -> int:
    cell = page.locator(".van-cell").filter(has_text="修改密码").first
    cell.wait_for(state="visible", timeout=12000)
    page.evaluate(
        """() => {
          const cell = [...document.querySelectorAll('.van-cell')]
            .find((el) => el.innerText.includes('修改密码'));
          if (!cell) return;
          const r = cell.getBoundingClientRect();
          const target = window.scrollY + r.top - Math.max(120, window.innerHeight / 2 - r.height / 2);
          window.scrollTo(0, Math.max(0, target));
        }"""
    )
    page.wait_for_timeout(400)
    return int(page.evaluate("Math.round(window.scrollY || 0)"))


def click_password_entry(page) -> None:
    page.locator(".van-cell").filter(has_text="修改密码").first.click(timeout=12000)


def analyze(frames: list[dict], before: int) -> tuple[list[dict], dict]:
    valid = [f for f in frames if "error" not in f]
    hits = []
    for f in valid:
        # 旧「我的」页顶部仍在视口内且可见，同时文档已被滚到顶部 = PM 看到的闪现
        if (
            f.get("page") == "password"
            and f.get("y", 999) <= 2
            and f.get("profile")
            and (f.get("profileOpacity") or 0) > 0.35
            and f.get("profileTop") is not None
            and f["profileTop"] > -8
        ):
            hits.append(f)
    stats = {
        "frames": len(valid),
        "pageErrors": len(frames) - len(valid),
        "before": before,
        "minY": min((f.get("y", 0) for f in valid), default=None),
        "firstPasswordFrame": next((f for f in valid if f.get("password")), None),
        "stableFrame": next(
            (f for f in valid if (f.get("passwordOpacity") or 0) >= 0.9 and f.get("page") == "password"), None
        ),
    }
    return hits, stats


def run_viewport(browser, width: int, height: int) -> None:
    context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    context.add_init_script(INIT)
    context.add_cookies([{
        "name": "yxq_session",
        "value": cookie_value(COOKIE),
        "url": BASE,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    if LOCAL_ASSETS_DIR:
        from local_assets import install  # noqa: PLC0415
        install(context, BASE, build_local_arm())
    page = context.new_page()
    errors = attach_errors(page)
    navs: list[str] = []

    def on_nav(frame):
        if frame == page.main_frame:
            navs.append(frame.url)

    page.on("framenavigated", on_nav)
    try:
        page.goto(BASE + "/", wait_until="domcontentloaded", timeout=60000)
        wait_app(page)
        if not page.evaluate("window.__YXQ_AUTHENTICATED__ === true"):
            raise Failure("未登录：window.__YXQ_AUTHENTICATED__ 不是 true")
        navs.clear()  # 启动导航之后开始计数

        open_profile(page)
        before = scroll_password_entry(page)
        if before <= 100:
            print(f"SKIP {width}x{height}: 「我的」页文档可滚动距离不足（scrollY={before}），无法复现")
            return
        snapshot(page, f"before-{width}")

        page.evaluate(RECORDER)
        click_password_entry(page)
        wait_page(page, "password", timeout=15000)
        try:
            page.wait_for_function(
                "() => { const el = document.querySelector('.password-page');"
                "return el && parseFloat(getComputedStyle(el).opacity) >= 0.9; }",
                timeout=8000,
            )
        except PlaywrightTimeoutError as exc:
            raise Failure("改密页未进入稳定可见状态") from exc
        page.wait_for_timeout(120)
        frames = json.loads(page.evaluate(STOP_RECORDER))

        hits, stats = analyze(frames, before)
        SHOT_DIR.mkdir(parents=True, exist_ok=True)
        (SHOT_DIR / f"timeline-{width}x{height}.json").write_text(
            json.dumps({"before": before, "hits": hits, "stats": stats, "frames": frames},
                       ensure_ascii=False, indent=1),
            encoding="utf-8",
        )
        if hits:
            snapshot(page, f"flash-{width}")
            first = hits[0]
            raise Failure(
                f"{width}x{height}: 改密页可见前旧「我的」页被滚到顶部（{len(hits)} 帧命中；"
                f"t={first['t']}ms y={first['y']} profileTop={first['profileTop']} "
                f"profileOpacity={first['profileOpacity']} passwordOpacity={first['passwordOpacity']}）"
            )

        # 稳定态：滚动清零、表单可见
        page.wait_for_timeout(250)
        stable = page.evaluate(
            """() => ({
              y: Math.round(window.scrollY || 0),
              page: window.yxqRootProxy && window.yxqRootProxy.page,
              card: !!document.querySelector('.pw-card'),
              fields: document.querySelectorAll('.pw-card input[type=password]').length,
              cardBox: (() => { const el = document.querySelector('.pw-card');
                if (!el) return null; const r = el.getBoundingClientRect();
                return {top: Math.round(r.top), h: Math.round(r.height)}; })()
            })"""
        )
        if stable["page"] != "password" or not stable["card"] or stable["fields"] != 3:
            raise Failure(f"{width}x{height}: 改密页稳定态异常 {stable}")
        if stable["y"] > 1:
            raise Failure(f"{width}x{height}: 改密页稳定后 scrollY={stable['y']}，期望 0")
        snapshot(page, f"password-stable-{width}")

        # 返回：还原点击前滚动位置（±2px）
        page.locator(".password-page .back-nav").first.click(timeout=12000)
        wait_page(page, "profile", timeout=15000)
        page.locator(".profile-header").first.wait_for(state="visible", timeout=15000)
        restored = None
        for _ in range(40):
            page.wait_for_timeout(100)
            y = int(page.evaluate("Math.round(window.scrollY || 0)"))
            if abs(y - before) <= 2:
                restored = y
                break
        if restored is None:
            restored = int(page.evaluate("Math.round(window.scrollY || 0)"))
            raise Failure(f"{width}x{height}: 返回「我的」滚动位置未还原：before={before} now={restored}")
        snapshot(page, f"back-{width}")

        if navs:
            raise Failure(f"{width}x{height}: 出现主框架文档导航 {navs[:3]}")
        real_errors = [e for e in errors if not any(s in e["text"] for s in IGNORED_CONSOLE)]
        if real_errors:
            raise Failure(f"{width}x{height}: console/pageerror {real_errors[:4]}")

        print(
            f"PASS {width}x{height}: 帧数={stats['frames']} before={before} "
            f"稳定 y={stable['y']} 返回 y={restored} 命中=0"
        )
    finally:
        context.close()


def main() -> int:
    global BASE, COOKIE
    BASE = require_env("BASE_URL", BASE)
    COOKIE = require_env("SESSION_COOKIE", COOKIE)
    if LOCAL_ASSETS_DIR and not (Path(LOCAL_ASSETS_DIR) / "web/static/legacy/app.min.js").exists():
        sys.exit(f"LOCAL_ASSETS_DIR 无效：{LOCAL_ASSETS_DIR}")
    viewports = parse_viewports(VIEWPORTS)
    print(
        f"password-no-flash smoke: BASE={BASE} viewports={viewports} "
        f"local_assets={LOCAL_ASSETS_DIR or 'off'}"
    )
    with sync_playwright() as p:
        browser = p.chromium.launch()
        try:
            for width, height in viewports:
                run_viewport(browser, width, height)
        finally:
            browser.close()
    print("ALL PASS password-no-flash smoke")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Failure as exc:
        print(f"FAIL {exc}", file=sys.stderr)
        sys.exit(1)
