#!/usr/bin/env python3
"""管理台 Phase A 真实浏览器验收（Playwright / Chromium）。

覆盖：总览 → 活动列表 → 新建 → 编辑 → 工作台 → 报名名单 → 签到 → 广播 → 设置。
视口：390×844 / 768×1024 / 1280×900，均为 SPA 同源 iframe overlay 内的真实页面。
断言：无 console error、无页面级横向滚动、关键触控区 ≥44px。

仅用于临时库 + 临时服务；不部署、不接触 staging。

    BASE_URL=http://127.0.0.1:18095 \
    SESSION_COOKIE=<yxq_session raw> \
    MAIN_ACTIVITY_ID=1 \
    SCREENSHOT_DIR=/tmp/opencode/yxq-admin-ui-shots \
    python3 tools/browser/admin_ui_phase_a.py
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path
from urllib.parse import urlparse

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

VIEWPORTS = ((390, 844), (768, 1024), (1280, 900))
TAB_LABELS = ("报名名单", "签到", "广播", "设置")


def check(condition: bool, message: str, failures: list[str]) -> None:
    if not condition:
        failures.append(message)


def frame_metrics(frame) -> dict:
    return frame.evaluate(
        """() => ({
          scrollWidth: document.documentElement.scrollWidth,
          innerWidth: window.innerWidth,
          scrollHeight: document.documentElement.scrollHeight,
        })"""
    )


def touch_targets(frame) -> list[str]:
    return frame.evaluate(
        """() => {
          const selectors = ['.button', '.admin-tab', '.admin-chip-btn', 'a.admin-entry',
            '.admin-mode-card', '.admin-topbar-back', '.admin-topbar-brand',
            '.admin-roster-actions button', '.admin-form-actions .button'];
          const seen = new Set();
          const bad = [];
          selectors.forEach((sel) => {
            document.querySelectorAll(sel).forEach((el) => {
              if (seen.has(el) || el.offsetParent === null) return;
              seen.add(el);
              const r = el.getBoundingClientRect();
              if (r.height < 43.5) {
                bad.push(sel + ':' + (el.textContent || '').trim().slice(0, 20) + '=' + Math.round(r.height));
              }
            });
          });
          return bad;
        }"""
    )


def open_admin_frame(page, base_url: str):
    page.goto(base_url + "/?admin=1", wait_until="networkidle")
    page.wait_for_selector(".embed-overlay", timeout=15000)
    page.wait_for_selector(".embed-frame", timeout=15000)
    page.wait_for_timeout(800)
    for frame in page.frames:
        try:
            if frame.url.startswith(base_url + "/admin"):
                return frame
        except Exception:
            continue
    raise RuntimeError("未找到 /admin iframe")


def shot(frame, out_dir: Path, name: str) -> None:
    frame.page.screenshot(path=str(out_dir / f"{name}.png"), full_page=False)


def run_viewport(browser, viewport: tuple[int, int], base_url: str, cookie: str,
                 main_activity_id: str, out_dir: Path) -> list[str]:
    width, height = viewport
    tag = str(width)
    failures: list[str] = []
    console_errors: list[str] = []
    ctx = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    ctx.add_cookies([{
        "name": "yxq_session",
        "value": cookie,
        "url": base_url,
        "httpOnly": True,
        "sameSite": "Lax",
    }])
    page = ctx.new_page()
    page.on("console", lambda msg: console_errors.append(msg.text) if msg.type == "error" else None)
    page.on("pageerror", lambda exc: console_errors.append("pageerror: " + str(exc)))
    page.on("dialog", lambda dialog: dialog.accept())

    frame = open_admin_frame(page, base_url)

    def frame_goto(path: str) -> None:
        frame.goto(base_url + path, wait_until="networkidle")
        page.wait_for_timeout(350)

    def assert_layout(label: str) -> None:
        metrics = frame_metrics(frame)
        check(metrics["scrollWidth"] <= metrics["innerWidth"] + 1,
              f"{tag}:{label} 横向滚动 {metrics['scrollWidth']}>{metrics['innerWidth']}", failures)
        bad = touch_targets(frame)
        check(not bad, f"{tag}:{label} 触控区不足 {bad[:5]}", failures)
        outer = page.evaluate("() => ({sw: document.documentElement.scrollWidth, iw: window.innerWidth})")
        check(outer["sw"] <= outer["iw"] + 1,
              f"{tag}:{label} 外层横向滚动 {outer['sw']}>{outer['iw']}", failures)

    # 1. 总览
    assert_layout("dashboard")
    shot(frame, out_dir, f"01-dashboard-{tag}")

    # 2. 活动列表
    frame_goto("/admin/activities")
    assert_layout("activities")
    check(frame.locator(".admin-activity-card").count() >= 4, f"{tag}: 活动卡不足", failures)
    check(frame.locator("[data-adm-activity-filter]").count() >= 6, f"{tag}: 状态筛选 chips 不足", failures)
    shot(frame, out_dir, f"02-activities-{tag}")

    # 3. 新建活动
    frame.goto(base_url + "/admin/activities/new", wait_until="networkidle")
    page.wait_for_timeout(300)
    assert_layout("new-activity")
    frame.evaluate("window.scrollTo(0, 0)")
    page.wait_for_timeout(150)
    shot(frame, out_dir, f"03-new-activity-{tag}")
    frame.fill('input[name="name"]', f"深秋读书会 UI 验收 {tag}")
    frame.fill('input[name="location"]', "西湖文化广场 · 城市书房")
    frame.fill('textarea[name="description"]', "带一本最近在读的书，围坐分享十分钟。")
    frame.fill('input[name="condition"]', "22–36 岁单身会员")
    frame.fill('input[name="max_signup"]', "12")
    frame.fill('input[name="fee"]', "免费")
    frame.fill('input[name="food"]', "含茶点")
    frame.check('input[name="signin_enabled"]')
    frame.fill('input[name="signin_code"]', "1357")
    frame.check('input[name="group_enabled"]')
    with frame.expect_navigation():
        frame.click('.admin-form-actions button[type="submit"]')
    page.wait_for_timeout(400)
    created_path = urlparse(frame.url).path.rstrip("/")
    check(created_path.startswith("/admin/activities/") and created_path.rsplit("/", 1)[-1].isdigit(),
          f"{tag}: 新建后未进入工作台 {frame.url}", failures)
    created_id = created_path.rsplit("/", 1)[-1]

    # 4. 编辑
    frame.goto(base_url + f"/admin/activities/{created_id}/edit", wait_until="networkidle")
    page.wait_for_timeout(300)
    assert_layout("edit-activity")
    frame.evaluate("window.scrollTo(0, 0)")
    page.wait_for_timeout(150)
    shot(frame, out_dir, f"04-edit-activity-{tag}")
    frame.fill('input[name="name"]', f"深秋读书会 UI 验收 {tag} ·已编辑")
    with frame.expect_navigation():
        frame.click('.admin-form-actions button[type="submit"]')
    page.wait_for_timeout(400)

    # 5. 工作台 + tabs
    assert_layout("workbench")
    check(frame.locator("[data-adm-tab]").count() == 4, f"{tag}: tabs 数量不是 4", failures)
    shot(frame, out_dir, f"05-workbench-{tag}")

    for index, label in enumerate(TAB_LABELS):
        frame.click(f'[data-adm-tab="{("roster", "signin", "broadcast", "settings")[index]}"]')
        page.wait_for_timeout(250)
        assert_layout(f"tab-{label}")
        shot(frame, out_dir, f"06-tab-{index + 1}-{label}-{tag}")

    # 6. 广播预览 + 发送（临时库，观察员 2 人）
    frame.click('[data-adm-tab="broadcast"]')
    page.wait_for_timeout(200)
    frame.check('input[name="include_observers"]')
    frame.fill('input[name="broadcast_title"]', f"读书会集合提醒 {tag}")
    frame.fill('textarea[name="broadcast_body"]', "周六下午两点城市书房见，请带上最近在读的书。")
    with frame.expect_navigation():
        frame.click('button[name="broadcast_action"][value="preview"]')
    page.wait_for_timeout(400)
    assert_layout("broadcast-preview")
    check(frame.locator(".admin-confirm").count() == 1, f"{tag}: 广播预览确认卡缺失", failures)
    shot(frame, out_dir, f"07-broadcast-preview-{tag}")
    with frame.expect_navigation():
        frame.click('button[name="broadcast_action"][value="send"]')
    page.wait_for_timeout(400)
    check("广播已发送" in frame.content(), f"{tag}: 广播发送结果缺失", failures)
    shot(frame, out_dir, f"08-broadcast-sent-{tag}")

    # 7. 真实活动：报名名单 → 现场签到 → 行内签到
    frame.goto(base_url + f"/admin/activities/{main_activity_id}", wait_until="networkidle")
    page.wait_for_timeout(400)
    assert_layout("roster")
    check(frame.locator('[id^="member-"]').count() >= 5, f"{tag}: 报名名单行不足", failures)
    shot(frame, out_dir, f"09-roster-{tag}")
    frame.click('[data-adm-tab="signin"]')
    page.wait_for_timeout(250)
    assert_layout("signin")
    check(frame.locator(".admin-signin-code-value").count() == 1, f"{tag}: 签到码缺失", failures)
    shot(frame, out_dir, f"10-signin-{tag}")
    unchecked = frame.locator('#signin .admin-roster-actions button[title="按时签到"]')
    if unchecked.count():
        with frame.expect_navigation():
            unchecked.first.click()
        page.wait_for_timeout(400)
        check("签到状态已更新" in frame.content(), f"{tag}: 行内签到结果缺失", failures)
        shot(frame, out_dir, f"11-signin-done-{tag}")

    check(not console_errors, f"{tag}: console error {console_errors[:3]}", failures)
    ctx.close()
    return failures


def main() -> int:
    base_url = os.environ.get("BASE_URL", "").rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "")
    main_activity_id = os.environ.get("MAIN_ACTIVITY_ID", "1")
    if not base_url or not cookie:
        sys.exit("BASE_URL 与 SESSION_COOKIE 必填")
    out_dir = Path(os.environ.get("SCREENSHOT_DIR") or "/tmp/opencode/yxq-admin-ui-shots")
    out_dir.mkdir(parents=True, exist_ok=True)

    failures: list[str] = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for viewport in VIEWPORTS:
                failures.extend(run_viewport(browser, viewport, base_url, cookie, main_activity_id, out_dir))
        finally:
            browser.close()
    payload = {"screenshots": str(out_dir), "failures": failures}
    print(json.dumps(payload, ensure_ascii=False, indent=2))
    if failures:
        print("\n".join("FAIL " + item for item in failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
