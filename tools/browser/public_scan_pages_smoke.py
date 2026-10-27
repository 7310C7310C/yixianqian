#!/usr/bin/env python3
"""公开页扫描防护 smoke（WS1，2026-10-27）。

契约（DECISIONS 38/39 + Reviewer 2026-10-27 修复）：
- 公开页面（GET）HTTP body 除 <title> 外 0 汉字；引用同源 CSS/JS 0 汉字；
  中文文案由 /static/public-copy.js 在浏览器注入。
- 覆盖：/login、/activate（含 400/410 错误页）、/password（含 409 无 credential）、
  /register/done、404。
- POST 表单错误消息仍为中文（明示例外）。

用法：
    BASE_URL=http://127.0.0.1:8095 \
    SESSION_COOKIE_FILE=/tmp/.../session.cookie \
    NO_CREDENTIAL_COOKIE_FILE=/tmp/.../nocred.cookie \
    ACTIVATION_TOKEN=... ACTIVATION_TOKEN_USED=... ACTIVATION_TOKEN_EXPIRED=... \
    ACTIVATION_USER_NAME=... \
    python3 tools/browser/public_scan_pages_smoke.py

可选 env 缺省时，对应的错误状态页仍会测试「缺失/无效」两种（不需要 DB）。
"""
from __future__ import annotations

import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
if not BASE:
    sys.exit("BASE_URL required")


def read_cookie(env_name: str) -> str:
    value = os.environ.get(env_name, "").strip()
    if value:
        return value
    file_name = os.environ.get(env_name + "_FILE", "").strip()
    if not file_name:
        return ""
    try:
        return Path(file_name).read_text(encoding="utf-8").strip()
    except OSError as exc:
        sys.exit(f"无法读取 {env_name}_FILE：{exc}")


COOKIE = read_cookie("SESSION_COOKIE")
NO_CRED_COOKIE = read_cookie("NO_CREDENTIAL_COOKIE")
ACTIVATION_TOKEN = os.environ.get("ACTIVATION_TOKEN", "").strip()
ACTIVATION_USED = os.environ.get("ACTIVATION_TOKEN_USED", "").strip()
ACTIVATION_EXPIRED = os.environ.get("ACTIVATION_TOKEN_EXPIRED", "").strip()
ACTIVATION_USER_NAME = os.environ.get("ACTIVATION_USER_NAME", "").strip()
INVALID_TOKEN = os.environ.get("ACTIVATION_TOKEN_INVALID", "invalid-public-token").strip()
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


def assert_raw_contract(path: str, want_status: int = 200, cookie: str = "", want_code: str = "") -> None:
    status, body, ctype = fetch(path, cookie)
    label = path.split("?", 1)[0]
    if status != want_status:
        raise SystemExit(f"{label}: status={status}, want {want_status}")
    if "text/html" not in ctype:
        raise SystemExit(f"{label}: unexpected content-type {ctype}")
    rest = TITLE.sub("", body, count=1)
    match = HAN.search(rest)
    if match:
        i = rest.index(match.group(0))
        raise SystemExit(f"{label}: Han outside <title> near {rest[max(0, i-40):i+40]!r}")
    if 'lang="zh-CN"' not in body:
        raise SystemExit(f"{label}: missing ASCII html lang")
    if "/static/public-copy.js" not in body:
        raise SystemExit(f"{label}: public-copy.js not referenced")
    if want_code and f'data-error-code="{want_code}"' not in body:
        raise SystemExit(f"{label}: missing data-error-code={want_code}")
    for ref in set(STATIC_REF.findall(body)):
        s, data, _ = fetch(ref)
        if s != 200:
            raise SystemExit(f"{label}: asset {ref} status={s}")
        if HAN.search(data):
            raise SystemExit(f"{label}: asset {ref} contains Han")
    print(f"PASS raw {label} [{want_status}]: 0 Han outside <title>, assets ASCII")


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


def rendered_error_check(page, viewport: str, path: str, code: str) -> None:
    page.goto(BASE + path, wait_until="networkidle")
    page.wait_for_function("() => !document.body.classList.contains('copy-pending')", timeout=15000)
    got = page.get_attribute("body", "data-error-code")
    if got != code:
        raise SystemExit(f"{path} [{viewport}]: data-error-code={got!r}, want {code!r}")
    heading = page.inner_text("main h1").strip()
    message = page.inner_text("main p.muted").strip()
    if not HAN.search(heading) or not HAN.search(message):
        raise SystemExit(f"{path} [{viewport}]: error copy not rendered (heading={heading!r} message={message!r})")
    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(SHOT_DIR / f"error-{code}-{viewport}.png"), full_page=True)
    print(f"PASS error {code} [{viewport}]: heading={heading!r} message-chars={len(message)}")


def main() -> int:
    # 1) Raw HTML / referenced assets contract (no JS, matches scanner view).
    assert_raw_contract("/login")
    assert_raw_contract("/activate", 400, want_code="activate_missing")
    assert_raw_contract("/activate?token=" + INVALID_TOKEN, 400, want_code="activate_invalid")
    assert_raw_contract("/register/done")
    assert_raw_contract("/nope", 404)
    if ACTIVATION_TOKEN:
        assert_raw_contract("/activate?token=" + ACTIVATION_TOKEN)
    if ACTIVATION_USED:
        assert_raw_contract("/activate?token=" + ACTIVATION_USED, 410, want_code="activate_used")
    if ACTIVATION_EXPIRED:
        assert_raw_contract("/activate?token=" + ACTIVATION_EXPIRED, 410, want_code="activate_expired")
    if NO_CRED_COOKIE:
        assert_raw_contract("/password", 409, cookie=NO_CRED_COOKIE, want_code="password_no_credential")
    elif COOKIE:
        assert_raw_contract("/password")

    console_errors: list[str] = []
    with sync_playwright() as p:
        for viewport in VIEWPORTS.split(","):
            width, _, height = viewport.partition("x")
            browser = p.chromium.launch()

            def new_page(cookie: str = ""):
                context = browser.new_context(
                    viewport={"width": int(width), "height": int(height)},
                    device_scale_factor=1,
                )
                if cookie:
                    parsed = urlparse(BASE)
                    context.add_cookies([{
                        "name": "yxq_session", "value": cookie, "domain": parsed.hostname,
                        "path": "/", "httpOnly": True, "secure": parsed.scheme == "https",
                        "sameSite": "Lax",
                    }])
                page = context.new_page()
                page.on("console", lambda msg: console_errors.append(msg.text)
                        if msg.type == "error" and not any(x in msg.text for x in IGNORED_CONSOLE) else None)
                page.on("pageerror", lambda exc: console_errors.append(str(exc)))
                return context, page

            anon_ctx, anon_page = new_page()
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
            rendered_error_check(anon_page, viewport, "/activate", "activate_missing")
            rendered_error_check(anon_page, viewport, "/activate?token=" + INVALID_TOKEN, "activate_invalid")
            if ACTIVATION_USED:
                rendered_error_check(anon_page, viewport, "/activate?token=" + ACTIVATION_USED, "activate_used")
            if ACTIVATION_EXPIRED:
                rendered_error_check(anon_page, viewport, "/activate?token=" + ACTIVATION_EXPIRED, "activate_expired")
            rendered_checks(anon_page, viewport, "/nope", "not-found",
                            ["找不到页面", "你造访的页面不存在。", "回到首页"])
            anon_ctx.close()

            if ACTIVATION_TOKEN or COOKIE:
                auth_ctx, auth_page = new_page(COOKIE)
                if ACTIVATION_TOKEN:
                    expect = ["设定密码", "新密码", "再次输入新密码", "设定并登录"]
                    if ACTIVATION_USER_NAME:
                        expect.append(ACTIVATION_USER_NAME)
                    rendered_checks(auth_page, viewport, "/activate?token=" + ACTIVATION_TOKEN, "activate", expect)
                if COOKIE:
                    rendered_checks(auth_page, viewport, "/password", "password",
                                    ["修改密码", "账号安全", "目前密码", "更新密码"])
                    rendered_checks(auth_page, viewport, "/register/done?submitted=1", "register-done",
                                    ["注册已送出", "退出登录", "接下来"])
                auth_ctx.close()

            if NO_CRED_COOKIE:
                nc_ctx, nc_page = new_page(NO_CRED_COOKIE)
                rendered_error_check(nc_page, viewport, "/password", "password_no_credential")
                nc_ctx.close()
            browser.close()

    if console_errors:
        raise SystemExit("console errors: " + " | ".join(console_errors[:5]))
    print("PASS: no console/page errors across viewports")
    return 0


if __name__ == "__main__":
    sys.exit(main())
