#!/usr/bin/env python3
"""Scan-safe /register smoke (WS3, 2026-10-27).

Contract (docs/DECISIONS.md decision 42):
- GET /register (plain, ?from=home, ?from=welcome) and rejected POST re-renders
  return HTML with 0 Han characters outside <title>; every referenced same-origin
  CSS/JS is Han-free; Chinese copy, option values and echoed values travel in the
  ASCII-only <body data-copy-json> catalog and are injected by /static/register.js.
- Option values stay Chinese on submit, selections/filled values survive a
  rejected POST, and the browser reports no console error.

Usage:
    BASE_URL=http://127.0.0.1:18095 \
    python3 tools/browser/register_scan_smoke.py

Optional env: VIEWPORTS=390x844,1280x900  SHOT_DIR=/tmp/opencode/register-scan-20261027
"""
from __future__ import annotations

import html
import http.cookiejar
import json
import os
import re
import sys
import urllib.error
import urllib.request
import uuid
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE = os.environ.get("BASE_URL", "").rstrip("/")
if not BASE:
    sys.exit("BASE_URL required")
VIEWPORTS = os.environ.get("VIEWPORTS", "390x844,1280x900").strip()
SHOT_DIR = Path(os.environ.get("SHOT_DIR", "/tmp/opencode/register-scan-20261027"))
HAN = re.compile(r"[\u4e00-\u9fff]")
TITLE = re.compile(r"<title>.*?</title>", re.S)
STATIC_REF = re.compile(r'(?:src|href)="(/static/[^"?#]+)')
COPY_JSON = re.compile(r'data-copy-json="([^"]*)"')
CONSOLE_IGNORED = ("Failed to load resource", "net::ERR", "favicon", "ResizeObserver")
SUCCESS_HTML = "<!doctype html><html lang=\"zh-CN\"><head><title>ok</title></head><body><h1 id=\"e2e-success\">E2E-SUCCESS</h1></body></html>"

SECTION_TITLES = ["个人照片", "登录密码", "基本资料", "教会与家乡", "工作与生活", "关于我", "理想中的 TA", "其他"]
LABELS = ["姓名", "昵称", "性别", "生日", "手机号码", "身份证号码", "微信号", "圣名",
          "经常去的教堂", "参加的团体", "教堂所在省-市-区", "家乡", "现居/工作城市",
          "家庭成员情况", "身高（cm）", "学历", "从事行业", "职位", "年收入", "房产状况",
          "是否有车", "你结过婚吗？", "婚后是否与父母同住", "我是一个怎样的人", "我的性格",
          "我的爱好", "我的运动", "我的 MBTI 人格", "我理想中的 TA", "TA 的性格",
          "TA 的爱好", "TA 的运动", "您替子女注册吗？", "你是怎么知道一线牵的？",
          "邀请人 ID"]
HERO = ["一线牵 · 注册", "填写你的资料", "已有账号？", "直接登录"]
SUBMIT = ["送出注册资料", "已经填过？", "登录查看审核状态"]


def fetch(path: str, opener=None) -> tuple[int, str, str]:
    req = urllib.request.Request(BASE + path)
    target = opener.open if opener else urllib.request.urlopen
    try:
        resp = target(req, timeout=20) if opener else target(req, timeout=20)
        return resp.getcode(), resp.read().decode("utf-8", "replace"), resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace"), exc.headers.get("Content-Type", "")


def assert_no_han(label: str, body: str) -> None:
    rest = TITLE.sub("", body, count=1)
    match = HAN.search(rest)
    if match:
        i = rest.index(match.group(0))
        raise SystemExit(f"{label}: Han outside <title> near {rest[max(0, i - 40):i + 40]!r}")
    match = COPY_JSON.search(body)
    if not match:
        raise SystemExit(f"{label}: missing data-copy-json catalog")
    if any(ord(ch) > 127 for ch in match.group(1)):
        raise SystemExit(f"{label}: catalog attribute contains non-ASCII characters")
    bad = sorted({ch for ch in rest if ord(ch) > 127})
    if bad:
        raise SystemExit(f"{label}: non-ASCII characters outside <title>: {bad[:8]}")


def assert_raw_contract(path: str, want_status: int = 200, opener=None, expect_error: str = "") -> dict:
    status, body, ctype = fetch(path, opener)
    label = path.split("?", 1)[0]
    if status != want_status:
        raise SystemExit(f"{label}: status={status}, want {want_status}")
    if "text/html" not in ctype:
        raise SystemExit(f"{label}: unexpected content-type {ctype}")
    if 'lang="zh-CN"' not in body:
        raise SystemExit(f"{label}: missing ASCII html lang")
    if "/static/register.js" not in body:
        raise SystemExit(f"{label}: register.js not referenced")
    assert_no_han(label, body)
    if "/static/app.css" in body or "/static/shell.css" in body or "public-copy.js" in body:
        raise SystemExit(f"{label}: loads a stylesheet/script outside the scan-safe set")
    catalog = json.loads(html.unescape(COPY_JSON.search(body).group(1)))
    if expect_error:
        if expect_error not in catalog["text"].get("page.error", ""):
            raise SystemExit(f"{label}: page.error={catalog['text'].get('page.error')!r}, want {expect_error!r}")
    for ref in sorted(set(STATIC_REF.findall(body))):
        if not ref.split("?", 1)[0].endswith((".css", ".js", ".svg", ".json", ".webmanifest")):
            continue
        s, data, _ = fetch(ref)
        if s != 200:
            raise SystemExit(f"{label}: asset {ref} status={s}")
        if HAN.search(data):
            raise SystemExit(f"{label}: asset {ref} contains Han")
    print(f"PASS raw {label} [{want_status}]: 0 Han outside <title>, assets ASCII, "
          f"text={len(catalog['text'])} options={len(catalog['options'])}")
    return catalog


def post_register_error() -> None:
    """Real server round trip: a rejected POST must stay Han-free."""
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    _, body, _ = fetch("/register", opener)
    match = re.search(r'name="csrf_token" value="([^"]+)"', body)
    if not match:
        raise SystemExit("POST check: csrf token missing")
    boundary = "----yxq" + uuid.uuid4().hex
    fields = {
        "csrf_token": match.group(1),
        "name": "测试用户",
        "nickname": "小测",
        "gender": "女性",
        "self_traits": ["积极乐观", "容易相处"],
    }
    chunks = []
    for key, value in fields.items():
        values = value if isinstance(value, list) else [value]
        for item in values:
            chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{item}\r\n')
    chunks.append(f"--{boundary}--\r\n")
    data = "".join(chunks).encode("utf-8")
    req = urllib.request.Request(BASE + "/register", data=data, headers={
        "Content-Type": f"multipart/form-data; boundary={boundary}",
    })
    try:
        resp = opener.open(req, timeout=20)
        status, out = resp.getcode(), resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        status, out = exc.code, exc.read().decode("utf-8", "replace")
    if status != 400:
        raise SystemExit(f"POST re-render: status={status}, want 400")
    assert_no_han("POST /register", out)
    catalog = json.loads(html.unescape(COPY_JSON.search(out).group(1)))
    if "请填写" not in catalog["text"].get("page.error", ""):
        raise SystemExit(f"POST re-render: page.error={catalog['text'].get('page.error')!r}")
    if catalog["values"].get("name") != "测试用户":
        raise SystemExit(f"POST re-render: echoed name={catalog['values'].get('name')!r}")
    checked = len(re.findall(r'data-register-option="self_traits\.\d+" checked', out))
    if checked != 2:
        raise SystemExit(f"POST re-render: checked traits={checked}, want 2")
    if not re.search(r'data-register-option="gender\.1" selected', out):
        raise SystemExit("POST re-render: gender selection lost")
    print("PASS raw POST /register [400]: 0 Han outside <title>, error+values in ASCII catalog, "
          "2 traits checked, gender selected")


def fill_region_codes(page) -> None:
    """Set the hidden cascade codes so the client-side region guard passes."""
    page.evaluate("""() => {
      const set = (name, value) => {
        const el = document.querySelector('input[name="' + name + '"]');
        if (el) el.value = value;
      };
      set('city_province', '44'); set('city_city', '4403');
      set('native_place_province', '44'); set('native_place_city', '4401');
      set('church_location_province', '44');
      set('church_location_city', '4403');
      set('church_location_area', '440305');
    }""")


def unlock_angel(page) -> None:
    """Test-only: bypass the QR long-press gate when staging has an asset."""
    page.evaluate("""() => {
      const ack = document.querySelector('input[name="angel_ack"]');
      if (ack) { ack.disabled = false; ack.checked = true; }
    }""")


def wait_ready(page) -> None:
    page.wait_for_function("() => !document.body.classList.contains('copy-pending')", timeout=15000)
    page.wait_for_function(
        "() => Array.from(document.querySelectorAll('[data-register-text]'))"
        ".every(el => el.textContent.trim().length > 0)", timeout=15000)


def rendered_checks(page, viewport: str) -> None:
    page.goto(BASE + "/register", wait_until="networkidle")
    wait_ready(page)
    main = page.inner_text("main")
    for want in HERO + SECTION_TITLES + LABELS + SUBMIT:
        if want not in main:
            raise SystemExit(f"/register [{viewport}]: rendered copy missing {want!r}")
    sections = page.eval_on_selector_all(".register-section h2", "els => els.map(e => e.textContent.trim())")
    if sections != SECTION_TITLES:
        raise SystemExit(f"/register [{viewport}]: section order {sections!r}")
    options = page.eval_on_selector_all(
        "#f_gender option", "els => els.map(e => [e.textContent.trim(), e.value])")
    if options != [["请选择", ""], ["男性", "男性"], ["女性", "女性"]]:
        raise SystemExit(f"/register [{viewport}]: gender options {options!r}")
    empty = page.eval_on_selector_all(
        "[data-register-option]", "els => els.filter(e => !e.value).length")
    if empty:
        raise SystemExit(f"/register [{viewport}]: {empty} option hooks without a value")
    empty_labels = page.eval_on_selector_all(
        "option[data-register-option]", "els => els.filter(e => !e.textContent.trim()).length")
    if empty_labels:
        raise SystemExit(f"/register [{viewport}]: {empty_labels} select options without a label")
    chips = page.eval_on_selector_all(
        "#field_self_traits label.chip span", "els => els.map(e => e.textContent.trim())")
    if len(chips) < 5 or not all(HAN.search(c) for c in chips):
        raise SystemExit(f"/register [{viewport}]: multi-select chips look wrong: {chips[:6]!r}")
    shot = SHOT_DIR / f"register-{viewport}.png"
    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(shot), full_page=True)
    print(f"PASS render /register [{viewport}]: {len(SECTION_TITLES)} sections, "
          f"{len(options)} gender options, {len(chips)} trait chips, screenshot={shot.name}")


def submit_payload_check(page, viewport: str) -> None:
    page.goto(BASE + "/register", wait_until="networkidle")
    wait_ready(page)
    captured: dict[str, bytes] = {}

    def handle(route):
        request = route.request
        if request.method == "POST":
            payload = request.post_data_buffer or b""
            captured["body"] = payload
            route.fulfill(status=200, content_type="text/html; charset=utf-8", body=SUCCESS_HTML)
            return
        route.continue_()

    page.route("**/register", handle)
    fill_region_codes(page)
    unlock_angel(page)
    page.fill("#f_name", "测试用户")
    page.fill("#f_nickname", "小测")
    page.select_option("#f_gender", label="女性")
    page.click('#field_self_traits label.chip:has-text("积极乐观")')
    page.evaluate("() => document.getElementById('register-form').setAttribute('novalidate', '')")
    try:
        with page.expect_navigation():
            page.click("#register-form button[type=submit]")
        page.wait_for_selector("#e2e-success", timeout=10000)
    finally:
        page.unroute("**/register", handle)
    payload = captured.get("body", b"")
    if not payload:
        raise SystemExit(f"/register submit [{viewport}]: no POST payload captured")
    if b"E2E-SUCCESS" in payload:
        raise SystemExit(f"/register submit [{viewport}]: unexpected payload")
    for token in ("女性", "积极乐观", "测试用户"):
        if token.encode("utf-8") not in payload:
            raise SystemExit(f"/register submit [{viewport}]: payload lost Chinese value {token!r}")
    print(f"PASS submit /register [{viewport}]: multipart keeps Chinese option values "
          f"(gender/trait/name), mocked success page rendered")


def error_rerender_check(page, viewport: str) -> None:
    page.goto(BASE + "/register", wait_until="networkidle")
    wait_ready(page)
    fill_region_codes(page)
    unlock_angel(page)
    page.fill("#f_name", "测试用户")
    page.fill("#f_nickname", "小测")
    page.select_option("#f_gender", label="女性")
    page.click('#field_self_traits label.chip:has-text("积极乐观")')
    page.evaluate("() => document.getElementById('register-form').setAttribute('novalidate', '')")
    with page.expect_navigation():
        page.click("#register-form button[type=submit]")
    wait_ready(page)
    error_text = page.inner_text(".alert.alert-error")
    if "请填写" not in error_text:
        raise SystemExit(f"/register error [{viewport}]: browser error copy missing: {error_text!r}")
    if page.input_value("#f_name") != "测试用户" or page.input_value("#f_nickname") != "小测":
        raise SystemExit(f"/register error [{viewport}]: text values not preserved")
    if page.input_value("#f_gender") != "女性":
        raise SystemExit(f"/register error [{viewport}]: select value not preserved")
    checked = page.eval_on_selector_all(
        "#field_self_traits input[type=checkbox]:checked", "els => els.map(e => e.value)")
    if checked != ["积极乐观"]:
        raise SystemExit(f"/register error [{viewport}]: selections not preserved: {checked!r}")
    shot = SHOT_DIR / f"register-error-{viewport}.png"
    SHOT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(shot), full_page=True)
    print(f"PASS error /register [{viewport}]: Chinese error rendered, values+selection preserved, "
          f"screenshot={shot.name}")


def main() -> int:
    assert_raw_contract("/register")
    assert_raw_contract("/register?from=home")
    assert_raw_contract("/register?from=welcome")
    post_register_error()

    errors: list[str] = []
    with sync_playwright() as p:
        for viewport in VIEWPORTS.split(","):
            width, _, height = viewport.partition("x")
            browser = p.chromium.launch()
            context = browser.new_context(
                viewport={"width": int(width), "height": int(height)},
                device_scale_factor=1,
            )
            page = context.new_page()

            def on_console(msg):
                if msg.type == "error" and not any(x in msg.text for x in CONSOLE_IGNORED):
                    errors.append(f"[{viewport}] console: {msg.text}")

            page.on("console", on_console)
            page.on("pageerror", lambda exc: errors.append(f"[{viewport}] pageerror: {exc}"))
            rendered_checks(page, viewport)
            submit_payload_check(page, viewport)
            error_rerender_check(page, viewport)
            context.close()
            browser.close()
    if errors:
        raise SystemExit("console errors: " + " | ".join(errors[:5]))
    print("PASS console: 0 error across viewports")
    return 0


if __name__ == "__main__":
    sys.exit(main())
