#!/usr/bin/env python3
"""管理入口收敛后的 API 级 smoke（无需浏览器）。

背景：PM 要求管理功能统一融入「我的 → 管理 → 🛠️ 管理后台」，活动/麦穗/举报
不再在「我的」页单列入口；原 Playwright 入口 smoke 随之失效。本脚本只验证：

1. 管理员 SPA 壳仍有「🛠️ 管理后台」，且没有三个单列入口的 cell title；
2. 管理员直调 `/legacy/api/admin/*` 与举报列表：200 且响应形状正确；
3. 普通用户同端点：403 且返回 v1 `{"error": ...}` 信封。

需要管理员与普通用户两个 session；不放进 make ci，手动运行：

    BASE_URL=https://next.nantou.love \
    SESSION_COOKIE=<管理员 yxq_session> \
    USER_SESSION_COOKIE=<普通用户 yxq_session> \
    python3 tools/admin_legacy_api_smoke.py

只读：只发 GET，不写数据、不改状态。
"""

from __future__ import annotations

import json
import os
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

# (路径, 期望存在的顶层键)
ADMIN_ENDPOINTS = (
    ("/legacy/api/admin/group/activities", "activities"),
    ("/legacy/api/admin/points/overview", "stats"),
    ("/legacy/api/messages/reported", "messages"),
)
REMOVED_ENTRIES = ("👥 活动管理", "🌾 麦穗管理", "🚨 举报管理")
CONSOLE_ENTRY = "🛠️ 管理后台"
TIMEOUT = 20


class SmokeFailure(AssertionError):
    pass


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"缺少环境变量 {name}")
    return value


def fetch(base: str, path: str, cookie_name: str, cookie: str) -> tuple[int, str]:
    headers = {
        "Cookie": f"{cookie_name}={cookie}",
        "Accept": "application/json,text/html;q=0.9",
    }
    request = Request(base + path, headers=headers)
    try:
        with urlopen(request, timeout=TIMEOUT) as resp:  # noqa: S310 - 明确只访问 BASE_URL
            return resp.status, resp.read().decode("utf-8", "replace")
    except HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except URLError as exc:
        raise SmokeFailure(f"{path}: 请求失败：{exc}") from exc


def parse_json(path: str, status: int, body: str) -> dict:
    try:
        payload = json.loads(body)
    except json.JSONDecodeError as exc:
        raise SmokeFailure(f"{path}: status={status} 响应不是 JSON：{body[:120]!r}") from exc
    if not isinstance(payload, dict):
        raise SmokeFailure(f"{path}: status={status} 响应不是 JSON 对象")
    return payload


def check_admin_shell(base: str, cookie_name: str, admin_cookie: str) -> None:
    status, body = fetch(base, "/", cookie_name, admin_cookie)
    if status != 200:
        raise SmokeFailure(f"管理员 GET / status={status}，want 200")
    if f'title="{CONSOLE_ENTRY}"' not in body:
        raise SmokeFailure(f"管理员 SPA 缺少「{CONSOLE_ENTRY}」入口")
    for entry in REMOVED_ENTRIES:
        if f'title="{entry}"' in body:
            raise SmokeFailure(f"管理员 SPA 仍有单列入口「{entry}」，应只保留「{CONSOLE_ENTRY}」")
    print("[ok] SPA 管理区只有「管理后台」，活动/麦穗/举报入口已撤")


def check_admin_endpoints(base: str, cookie_name: str, admin_cookie: str) -> None:
    for path, key in ADMIN_ENDPOINTS:
        status, body = fetch(base, path, cookie_name, admin_cookie)
        if status != 200:
            raise SmokeFailure(f"管理员 {path} status={status}，want 200：{body[:160]!r}")
        payload = parse_json(path, status, body)
        if key not in payload:
            raise SmokeFailure(f"管理员 {path} 缺少顶层键 {key!r}：{sorted(payload)}")
    print("[ok] 管理员三端点 200，activities/stats/messages 形状正确")


def check_plain_forbidden(base: str, cookie_name: str, user_cookie: str) -> None:
    for path, _ in ADMIN_ENDPOINTS:
        status, body = fetch(base, path, cookie_name, user_cookie)
        if status != 403:
            raise SmokeFailure(f"普通用户 {path} status={status}，want 403：{body[:160]!r}")
        payload = parse_json(path, status, body)
        if not payload.get("error"):
            raise SmokeFailure(f"普通用户 {path} 403 响应缺少 error 字段")
    print("[ok] 普通用户三端点 403，均返回 v1 error 信封")


def main() -> int:
    base = require("BASE_URL").rstrip("/")
    admin_cookie = require("SESSION_COOKIE")
    user_cookie = require("USER_SESSION_COOKIE")
    cookie_name = os.environ.get("SESSION_COOKIE_NAME", "yxq_session").strip() or "yxq_session"

    try:
        check_admin_shell(base, cookie_name, admin_cookie)
        check_admin_endpoints(base, cookie_name, admin_cookie)
        check_plain_forbidden(base, cookie_name, user_cookie)
    except SmokeFailure as exc:
        print("FAIL")
        print(" -", exc)
        return 1
    print("PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
