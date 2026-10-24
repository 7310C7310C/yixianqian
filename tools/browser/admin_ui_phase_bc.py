#!/usr/bin/env python3
"""管理台 Phase B/C 真实浏览器验收（Playwright / Chromium）。

覆盖：总览 → 活动列表回归；用户列表 → 筛选 → 详情 → 审核；
匹配总览 → 分组控制台 → 一对一控制台；麦穗总览 → 各 tab → 调整；
举报/反馈处理；角色指派/移除；审计筛选/导出。
视口：390×844 / 768×1024 / 1280×900，均为 SPA 同源 iframe overlay 内的真实页面。

断言：无 console error、无页面级横向滚动、无表格内部横滚、关键触控 ≥44px、
深色实心按钮对比度 ≥4.5:1、顶栏身份块非空。

仅用于临时库 + 临时服务；不部署、不接触 staging。

    BASE_URL=http://127.0.0.1:18097 \
    SESSION_COOKIE=<yxq_session raw> \
    SCREENSHOT_DIR=.team/audits/admin-ui-phase-bc \
    RESET_CMD="psql -q -d yxq_admin_ui_bc -f .team/audits/admin-ui-phase-bc/reset.sql" \
    python3 tools/browser/admin_ui_phase_bc.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:18097").rstrip("/")
COOKIE = os.environ.get("SESSION_COOKIE", "")
OUT_DIR = Path(os.environ.get("SCREENSHOT_DIR", ".team/audits/admin-ui-phase-bc"))
RESET_CMD = os.environ.get("RESET_CMD", "")
VIEWPORTS = ((390, 844), (768, 1024), (1280, 900))
PAGES = (
    ("dashboard", "/admin"),
    ("users", "/admin/users"),
    ("matching", "/admin/matching"),
    ("points-overview", "/admin/points"),
    ("points-users", "/admin/points?tab=users"),
    ("points-ledger", "/admin/points?tab=ledger"),
    ("points-invites", "/admin/points?tab=invites"),
    ("points-redemptions", "/admin/points?tab=redemptions"),
    ("points-wishes", "/admin/points?tab=wishes"),
    ("points-recommendations", "/admin/points?tab=recommendations"),
    ("reports", "/admin/reports"),
    ("suggestions", "/admin/suggestions"),
    ("roles", "/admin/roles"),
    ("audit", "/admin/audit"),
)

TOUCH_SELECTORS = (
    "a.button, button.button, .admin-chip-btn, .admin-record-actions a, "
    ".admin-record-actions button, .admin-topbar-back, .admin-topbar-brand, "
    ".admin-audit-more summary, .admin-matching-person summary"
)

CONTRAST_JS = """(sel) => {
  const el = document.querySelector(sel);
  if (!el) return null;
  const rgb = (c) => c.match(/[\\d.]+/g).map(Number).slice(0, 3);
  const lum = (v) => {
    const f = v.map((x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
    return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2];
  };
  const bg = lum(rgb(getComputedStyle(el).backgroundColor));
  const fg = lum(rgb(getComputedStyle(el).color));
  return Math.round(((Math.max(bg, fg) + 0.05) / (Math.min(bg, fg) + 0.05)) * 100) / 100;
}"""


class Checker:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.notes: list[str] = []

    def check(self, ok: bool, message: str) -> None:
        if not ok:
            self.failures.append(message)

    def note(self, message: str) -> None:
        self.notes.append(message)


def shot(page, tag: str, name: str) -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(OUT_DIR / f"{name}-{tag}.png"), full_page=False)


def frame_metrics(frame) -> dict:
    return frame.evaluate(
        """() => ({
          scrollWidth: document.documentElement.scrollWidth,
          bodyScrollWidth: document.body ? document.body.scrollWidth : 0,
          innerWidth: window.innerWidth,
          tables: Array.from(document.querySelectorAll('.admin-table-wrap')).map((el) => ({
            scroll: el.scrollWidth, client: el.clientWidth,
          })),
        })"""
    )


def touch_problems(frame) -> list[str]:
    return frame.evaluate(
        """(selectors) => {
          const bad = [];
          document.querySelectorAll(selectors).forEach((el) => {
            if (el.offsetParent === null) return;
            const r = el.getBoundingClientRect();
            if (r.height < 43.5) bad.push((el.textContent || '').trim().slice(0, 16) + '=' + Math.round(r.height));
          });
          return bad.slice(0, 6);
        }""",
        TOUCH_SELECTORS,
    )


def assert_layout(chk: Checker, page, frame, label: str, tag: str) -> None:
    metrics = frame_metrics(frame)
    chk.check(metrics["scrollWidth"] <= metrics["innerWidth"] + 1,
              f"{tag}:{label} iframe 横向滚动 {metrics['scrollWidth']}>{metrics['innerWidth']}")
    outer = page.evaluate("() => [document.documentElement.scrollWidth, window.innerWidth]")
    chk.check(outer[0] <= outer[1] + 1, f"{tag}:{label} 外层横向滚动 {outer[0]}>{outer[1]}")
    bad_tables = [t for t in metrics["tables"] if t["scroll"] > t["client"] + 1]
    chk.check(not bad_tables, f"{tag}:{label} 表格内部横滚 {bad_tables[:2]}")
    bad = touch_problems(frame)
    chk.check(not bad, f"{tag}:{label} 触控区不足 {bad}")


def wait_frame(page, frame) -> None:
    page.wait_for_timeout(220)
    try:
        frame.wait_for_load_state("networkidle", timeout=8000)
    except Exception:  # 页面有 SSE 时 networkidle 不会触发，等待兜底即可
        page.wait_for_timeout(400)


def frame_html(page, frame) -> str:
    """POST 重定向后 frame 可能仍在导航；重试读取，避免 content() 直接抛错。"""
    for _ in range(12):
        try:
            frame.wait_for_load_state("load", timeout=2000)
            return frame.content()
        except Exception:
            page.wait_for_timeout(200)
    return ""


def frame_goto(page, frame, path: str) -> None:
    frame.goto(BASE_URL + path, wait_until="load")
    wait_frame(page, frame)


def open_admin(page):
    page.goto(BASE_URL + "/?admin=1", wait_until="networkidle")
    page.wait_for_selector(".embed-overlay", timeout=15000)
    page.wait_for_selector(".embed-frame", timeout=15000)
    page.wait_for_timeout(700)
    for candidate in page.frames:
        if candidate.url.startswith(BASE_URL + "/admin"):
            return candidate
    raise RuntimeError("未找到 /admin iframe")


def run_viewport(browser, viewport: tuple[int, int], chk: Checker) -> None:
    width, height = viewport
    tag = str(width)
    console_errors: list[str] = []
    ctx = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
    ctx.add_cookies([{"name": "yxq_session", "value": COOKIE, "url": BASE_URL, "httpOnly": True, "sameSite": "Lax"}])
    page = ctx.new_page()
    page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
    page.on("pageerror", lambda exc: console_errors.append("pageerror: " + str(exc)))
    page.on("dialog", lambda dialog: dialog.accept())
    frame = open_admin(page)

    # 1) 总览 + 身份块 + 活动入口回归
    assert_layout(chk, page, frame, "dashboard", tag)
    shot(page, tag, "01-dashboard")
    identity = frame.evaluate(
        """() => {
          const el = document.querySelector('.admin-identity');
          if (!el) return null;
          return {label: el.getAttribute('aria-label') || '', text: (el.textContent || '').replace(/\\s+/g, ' ').trim()};
        }"""
    )
    chk.check(identity is not None, f"{tag}:总览缺少顶栏身份块")
    if identity:
        chk.check("管理员" in identity["text"] and identity["text"].strip() != "", f"{tag}:顶栏身份块为空 {identity}")
        chk.check("当前管理员" in identity["label"], f"{tag}:顶栏身份块 aria-label 缺失 {identity}")
    chk.check(frame.query_selector(".admin-topbar-user") is None, f"{tag}:顶栏仍渲染裸用户文本")
    entry = frame.query_selector('.admin-entry[href^="/app?native_admin=1"]')
    chk.check(entry is not None, f"{tag}:总览缺少活动列表入口")
    if entry:
        entry.click()
        wait_frame(page, frame)
        chk.check("/app?native_admin=1" in frame.url, f"{tag}:活动列表入口未打开原版管理 {frame.url}")
        title = frame.query_selector(".ga-title")
        chk.check(title is not None and "活动管理" in (title.inner_text() or ""),
                  f"{tag}:原版活动管理页未渲染列表标题")
        chk.check(frame.query_selector(".van-tabbar") is None, f"{tag}:native 活动管理仍显示底部 tabbar")
        shot(page, tag, "02-activities-from-entry")
        frame_goto(page, frame, "/admin")

    # 2) 用户列表 → 详情 → 审核
    frame_goto(page, frame, "/admin/users")
    assert_layout(chk, page, frame, "users", tag)
    shot(page, tag, "03-users")
    chip = frame.query_selector('.admin-filter-chips a:has-text("待审核")')
    if chip:
        chip.click()
        wait_frame(page, frame)
    detail = frame.query_selector('.admin-record-actions a:has-text("详情")')
    chk.check(detail is not None, f"{tag}:用户列表缺少详情入口")
    if detail:
        detail.click()
        wait_frame(page, frame)
        chk.check("/admin/users/" in frame.url, f"{tag}:用户详情未打开 {frame.url}")
        assert_layout(chk, page, frame, "user-detail", tag)
        shot(page, tag, "04-user-detail")
        approve = frame.query_selector('button:has-text("审核通过")')
        if approve:
            note = frame.query_selector("#admin-approve-note")
            if note:
                note.fill("浏览器验收：资料完整")
            approve.click()
            wait_frame(page, frame)
            chk.check("已审核通过" in frame_html(page, frame), f"{tag}:审核通过后没有提示")
            shot(page, tag, "05-user-approved")
        else:
            chk.note(f"{tag}: 无待审核用户，跳过审核动作（详情页已验收）")

    # 3) 匹配 → 分组控制台 → 一对一控制台
    frame_goto(page, frame, "/admin/matching")
    assert_layout(chk, page, frame, "matching", tag)
    shot(page, tag, "06-matching")
    group_link = frame.query_selector('a:has-text("分组控制台")')
    if group_link:
        group_link.click()
        wait_frame(page, frame)
        chk.check("/admin/matching/group/" in frame.url, f"{tag}:分组控制台未打开 {frame.url}")
        assert_layout(chk, page, frame, "matching-group", tag)
        shot(page, tag, "07-matching-group")
    one_link = None
    frame_goto(page, frame, "/admin/matching")
    one_link = frame.query_selector('a:has-text("一对一控制台")')
    if one_link:
        one_link.click()
        wait_frame(page, frame)
        chk.check("/admin/matching/onetoone/" in frame.url, f"{tag}:一对一控制台未打开 {frame.url}")
        assert_layout(chk, page, frame, "matching-onetoone", tag)
        shot(page, tag, "08-matching-onetoone")

    # 4) 麦穗 → 调整 → 各 tab
    frame_goto(page, frame, "/admin/points")
    assert_layout(chk, page, frame, "points-overview", tag)
    shot(page, tag, "09-points-overview")
    frame_goto(page, frame, "/admin/points?tab=users")
    search = frame.query_selector("#points-user-q")
    if search:
        search.fill("陈知远")
        frame.click('.admin-filter-bar button:has-text("查询")')
        wait_frame(page, frame)
    adjust = frame.query_selector('a:has-text("调整/流水")')
    chk.check(adjust is not None, f"{tag}:麦穗用户 tab 缺少调整入口")
    if adjust:
        adjust.click()
        wait_frame(page, frame)
        delta = frame.query_selector('input[name="delta"]')
        reason = frame.query_selector('textarea[name="reason"]')
        if delta and reason:
            delta.fill("10")
            reason.fill("浏览器验收调整")
            frame.click('button:has-text("提交调整")')
            wait_frame(page, frame)
            chk.check("麦穗调整已入账" in frame_html(page, frame), f"{tag}:麦穗调整后没有到账提示")
            shot(page, tag, "10-points-adjusted")
    for name in ("ledger", "invites", "redemptions", "wishes", "recommendations"):
        frame_goto(page, frame, f"/admin/points?tab={name}")
        assert_layout(chk, page, frame, f"points-{name}", tag)
        shot(page, tag, f"11-points-{name}")

    # 5) 举报处理
    frame_goto(page, frame, "/admin/reports")
    assert_layout(chk, page, frame, "reports", tag)
    shot(page, tag, "12-reports")
    resolve = frame.query_selector('button[name="action"][value="resolve"]')
    if resolve:
        note = frame.query_selector('input[name="note"]')
        if note:
            note.fill("浏览器验收：已核实")
        resolve.click()
        wait_frame(page, frame)
        chk.check("举报已处理" in frame_html(page, frame), f"{tag}:举报处理后没有提示")
    else:
        chk.note(f"{tag}: 无待处理举报，跳过处理动作（页面已验收）")

    # 6) 反馈处理
    frame_goto(page, frame, "/admin/suggestions")
    assert_layout(chk, page, frame, "suggestions", tag)
    shot(page, tag, "13-suggestions")
    handle = frame.query_selector('button:has-text("标记已处理")')
    if handle:
        handle.click()
        wait_frame(page, frame)
        chk.check("意见反馈已标记为已处理" in frame_html(page, frame), f"{tag}:反馈处理后没有提示")
    else:
        chk.note(f"{tag}: 无待处理反馈，跳过处理动作（页面已验收）")

    # 7) 角色 → 指派 → 移除
    frame_goto(page, frame, "/admin/roles")
    assert_layout(chk, page, frame, "roles", tag)
    shot(page, tag, "14-roles")
    user_ref = frame.query_selector("#role-user-ref")
    if user_ref:
        user_ref.fill("U-0107")
        frame.select_option("#role-code", "reviewer")
        frame.select_option("#role-action", "assign")
        frame.click('button:has-text("送出")')
        wait_frame(page, frame)
        chk.check("角色已指派" in frame_html(page, frame), f"{tag}:角色指派后没有提示")
        frame.select_option("#role-action", "remove")
        frame.fill("#role-user-ref", "U-0107")
        frame.select_option("#role-code", "reviewer")
        frame.click('button:has-text("送出")')
        wait_frame(page, frame)
        chk.check("角色已移除" in frame_html(page, frame), f"{tag}:角色移除后没有提示")
        shot(page, tag, "15-roles-assigned")

    # 8) 审计 → 筛选 → 导出
    frame_goto(page, frame, "/admin/audit")
    assert_layout(chk, page, frame, "audit", tag)
    shot(page, tag, "16-audit")
    frame.fill("#audit-action", "admin.user.approve")
    frame.click('button:has-text("筛选")')
    wait_frame(page, frame)
    chk.check("action=admin.user.approve" in frame.url, f"{tag}:审计筛选未生效 {frame.url}")
    body = frame_html(page, frame)
    chk.check("admin.user.approve" in body, f"{tag}:审计筛选后结果缺少目标操作")
    shot(page, tag, "17-audit-filtered")
    export_href = frame.get_attribute('a:has-text("导出 CSV")', "href")
    chk.check(bool(export_href), f"{tag}:审计页缺少导出链接")
    if export_href:
        resp = page.request.get(BASE_URL + export_href)
        chk.check(resp.status == 200, f"{tag}:审计 CSV 导出 HTTP {resp.status}")
        chk.check("text/csv" in (resp.headers.get("content-type") or ""), f"{tag}:审计 CSV Content-Type 异常")
    frame_goto(page, frame, "/admin/audit")

    # 9) 深色模式对比度（用户页有实心主按钮与选中 chip）
    frame_goto(page, frame, "/admin/users")
    frame.evaluate("() => document.documentElement.classList.add('yxq-embed-dark')")
    page.wait_for_timeout(150)
    shot(page, tag, "18-users-dark")
    contrast = frame.evaluate(CONTRAST_JS, ".button.primary")
    chip_contrast = frame.evaluate(CONTRAST_JS, '.admin-chip-btn[aria-current="page"]')
    if contrast is not None:
        chk.check(contrast >= 4.5, f"{tag}:深色主按钮对比度 {contrast} < 4.5")
    chk.check(chip_contrast is None or chip_contrast >= 4.5, f"{tag}:深色选中 chip 对比度 {chip_contrast} < 4.5")
    assert_layout(chk, page, frame, "users-dark", tag)
    for name, path in (("audit", "/admin/audit"), ("reports", "/admin/reports"),
                       ("points", "/admin/points"), ("roles", "/admin/roles")):
        frame_goto(page, frame, path)
        frame.evaluate("() => document.documentElement.classList.add('yxq-embed-dark')")
        page.wait_for_timeout(120)
        shot(page, tag, f"18-dark-{name}")
        value = frame.evaluate(CONTRAST_JS, ".button.primary")
        chk.check(value is None or value >= 4.5, f"{tag}:深色{name}主按钮对比度 {value} < 4.5")
        assert_layout(chk, page, frame, f"{name}-dark", tag)

    # 10) 其余页面逐页截图
    for name, path in PAGES:
        frame_goto(page, frame, path)
        assert_layout(chk, page, frame, name, tag)
        shot(page, tag, f"19-{name}")

    chk.check(not console_errors, f"{tag}: console error {console_errors[:4]}")
    ctx.close()


def main() -> int:
    if not COOKIE:
        sys.exit("需要 SESSION_COOKIE（管理员 yxq_session）")
    chk = Checker()
    with sync_playwright() as p:
        browser = p.chromium.launch()
        for index, viewport in enumerate(VIEWPORTS):
            if RESET_CMD:
                subprocess.run(RESET_CMD, shell=True, check=False)
            run_viewport(browser, viewport, chk)
        browser.close()

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    (OUT_DIR / "RESULT.md").write_text(
        "# 管理台 Phase B/C 浏览器验收\n\n"
        f"- 视口：{', '.join(f'{w}×{h}' for w, h in VIEWPORTS)}\n"
        f"- 截图：{len(list(OUT_DIR.glob('*.png')))} 张\n"
        f"- 结果：{'PASS' if not chk.failures else 'FAIL'}\n"
        f"- 备注：{'；'.join(chk.notes) or '无'}\n"
        + "".join(f"- FAIL: {item}\n" for item in chk.failures),
        encoding="utf-8",
    )
    print(json.dumps({"screenshots": str(OUT_DIR), "failures": chk.failures, "notes": chk.notes},
                     ensure_ascii=False))
    return 0 if not chk.failures else 1


if __name__ == "__main__":
    sys.exit(main())
