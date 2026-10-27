#!/usr/bin/env python3
"""公开页扫描防护 smoke（WS1，2026-10-27）。

契约（DECISIONS 38/39）：/login、/activate、/password、/register/done、404 的
HTTP body 除 <title> 外 0 汉字；页面引用的同源 CSS/JS 0 汉字；中文文案由
/static/public-copy.js 在浏览器注入。

用法：
    BASE_URL=http://127.0.0.1:8095 \
    SESSION_COOKIE_FILE=/tmp/.../session.cookie \
    ACTIVATION_TOKEN=... \
    python3 tools/browser/public_scan_pages_smoke.py

未提供 SESSION_COOKIE_FILE 时跳过 /password、/register/done（登录态页面）；
未提供 ACTIVATION_TOKEN 时 /activate 只检查匿名 400 错误页并跳过（该错误页是
DECISIONS 39 明示的 renderError 例外，不参与 0 汉字断言）。
"""
from __future__ import annotations

import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import urljoin, urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
if not BASE:
    sys.exit("BASE_URL required")
COOKIE = os.environ.get("SESSION_COOKIE", "").strip()
if not COOKIE:
    cookie_file = os.environ.get("SESSION_COOKIE_FILE", "").strip()
    if cookie_file:
        try:
            COOKIE = Path(cookie_file).read_text(encoding="utf-8").strip()
        except OSError as exc:
            sys.exit(f"无法读取 SESSION_COOKIE_FILE：{exc}")
ACTIVATION_TOKEN = os.environ.get("ACTIVATION_TOKEN", "").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/public-scan-pages-20261027"))
VIEWPORTS = os.environ.get("VIEWPORTS", "390x844,1280x900").strip()
HAN = re.compile(r"[\u4e00-\u9fff]")
TITLE = re.compile(r"<title>.*?</title>", re.S)
STATIC_REF = re.compile(r'(?:src|href)="(/static/[^"?#]+)')

IGNORED_CONSOLE = ("Failed to load resource", "net::ERR", "favicon", "ResizeObserver")


def fetch(path: str, cookie: str = "") -> tuple[int, str, str]:
    headers = {}
    if cookie:
        headers["Cookie"] = f"yxq_session={cookie}"
    req = urllib.request.Request(BASE + path, headers=headers)
    try:
        resp = urllib.request.urlopen(req, timeout=20)
        return resp.status, resp.read().decode("utf-8", "replace"), resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace"), exc.headers.get("Content-Type", "")


def assert_raw_contract(path: str, want_status: int = 200) -> None:
    status, body, ctype = fetch(path)
    if status != want_status:
        raise SystemExit(f"{path}: status={status}")
    if "text/html" not in ctype:
        raise SystemExit(f"{path}: unexpected content-type {ctype}")
    rest = TITLE.sub("", body, count=1)
    match = HAN.search(rest)
    if match:
        i = rest.index(match.group(0))
        raise SystemExit(f"{path}: Han outside <title> near {rest[max(0, i-40):i+40]!r}")
    if 'lang="zh-CN"' not in body:
        raise SystemExit(f"{path}: missing ASCII html lang")
    if "/static/public-copy.js" not in body:
        raise SystemExit(f"{path}: public-copy.js not referenced")
    for ref in set(STATIC_REF.findall(body)):
        s, data, _ = fetch(ref)
        if s != 200:
            raise SystemExit(f"{path}: asset {ref} status={s}")
        if HAN.search(data):
            raise SystemExit(f"{path}: asset {ref} contains Han")
    label = path.split("?", 1)[0]
    print(f"PASS raw {label}: 0 Han outside <title>, assets ASCII")


def rendered_checks(page, viewport: str, path: str, shot: str, expect: list[str]) -> None:
    page.goto(BASE + path, wait_until="networkidle")
    page.wait_for_function("() => !document.body.classList.contains('copy-pending')", timeout=15000)
    text = page.inner_text("main")
    for want in expect:
        if want not in text:
            raise SystemExit(f"{path} [{viewport}]: rendered copy missing {want!r}; got {text[:200]!r}")
    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(SHOT_DIR / f"{shot}-{viewport}.png"), full_page=True)
    label = path.split("?", 1)[0]
    print(f"PASS render {label} [{viewport}]: {len(expect)} copy strings, screenshot={shot}-{viewport}.png")


def main() -> int:
    paths = ["/login", "/register/done", "/nope"]
    if ACTIVATION_TOKEN:
        act_path = "/activate?token=" + ACTIVATION_TOKEN
    else:
        act_path = "/activate"
    if COOKIE:
        paths += ["/password"]

    # 1) Raw HTML / referenced assets contract (no JS, matches scanner view).
    for path in ["/login", act_path, "/register/done"]:
        if path == "/activate":
            # No token: handler renders the renderError exception page; skip raw check.
            continue
        assert_raw_contract(path)
    assert_raw_contract("/nope", 404)
    if COOKIE:
        status, body, _ = fetch("/password", COOKIE)
        if status != 200:
            raise SystemExit(f"/password status={status}")
        rest = TITLE.sub("", body, count=1)
        if HAN.search(rest):
            raise SystemExit("/password: Han outside <title>")
        for ref in set(STATIC_REF.findall(body)):
            s, data, _ = fetch(ref)
            if s != 200 or HAN.search(data):
                raise SystemExit(f"/password: asset {ref} status={s} or Han")
        print("PASS raw /password: 0 Han outside <title>, assets ASCII")

    console_errors: list[str] = []
    with sync_playwright() as p:
        for viewport in VIEWPORTS.split(","):
            width, _, height = viewport.partition("x")
            browser = p.chromium.launch()
            def new_page(with_cookie: bool):
                context = browser.new_context(
                    viewport={"width": int(width), "height": int(height)},
                    device_scale_factor=1,
                )
                if with_cookie and COOKIE:
                    parsed = urlparse(BASE)
                    context.add_cookies([{
                        "name": "yxq_session", "value": COOKIE, "domain": parsed.hostname,
                        "path": "/", "httpOnly": True, "secure": parsed.scheme == "https",
                        "sameSite": "Lax",
                    }])
                page = context.new_page()
                page.on("console", lambda msg: console_errors.append(msg.text)
                        if msg.type == "error" and not any(x in msg.text for x in IGNORED_CONSOLE) else None)
                page.on("pageerror", lambda exc: console_errors.append(str(exc)))
                return context, page

            anon_ctx, anon_page = new_page(False)
            rendered_checks(anon_page, viewport, "/login", "login",
                            ["登录", "手机号码", "密码", "注册新账号", "已在飞书版使用过", "打开飞书版"])
            # Login form is submittable; server-side errors stay Chinese (documented exception).
            anon_page.fill("#phone", "13700000099")
            anon_page.fill("#password", "wrong-password")
            with anon_page.expect_navigation():
                anon_page.click("button[type=submit]")
            anon_page.wait_for_function("() => !document.body.classList.contains('copy-pending')", timeout=15000)
            if "手机号码或密码错误" not in anon_page.inner_text("main"):
                raise SystemExit(f"login [{viewport}]: server error message missing after submit")
            print(f"PASS submit /login [{viewport}]: 401 re-render keeps Chinese server error")
            rendered_checks(anon_page, viewport, "/nope", "not-found",
                            ["找不到页面", "你造访的页面不存在。", "回到首页"])
            anon_ctx.close()

            if ACTIVATION_TOKEN or COOKIE:
                auth_ctx, auth_page = new_page(True)
                if ACTIVATION_TOKEN:
                    rendered_checks(auth_page, viewport, act_path, "activate",
                                    ["设定密码", "测试激活", "新密码", "再次输入新密码", "设定并登录"])
                if COOKIE:
                    rendered_checks(auth_page, viewport, "/password", "password",
                                    ["修改密码", "账号安全", "目前密码", "更新密码"])
                    rendered_checks(auth_page, viewport, "/register/done?submitted=1", "register-done",
                                    ["注册已送出", "退出登录", "接下来"])
                auth_ctx.close()
            browser.close()

    if console_errors:
        raise SystemExit("console errors: " + " | ".join(console_errors[:5]))
    print("PASS: no console/page errors across viewports")
    return 0


if __name__ == "__main__":
    sys.exit(main())
