#!/usr/bin/env python3
"""批次7 A2 视觉/结构回归：control（app.js）vs treatment（app.min.js）。

三视口 390/768/1280 × 亮/暗 × 四页（cards/likes/home/profile），两臂各截一张，
存 `.team/perf/batch7-shots/{arm}/{w}-{theme}-{page}.png`；并做结构等价比对：
页面 settle 后 `#app` 的 innerHTML（去掉 `?v=` 查询串、空白归一）sha256 必须一致，
`.van-tabbar` 数量一致，且无 console error / pageerror。

注意：结构等价不是像素等价，截图留档供人眼确认；两臂差异只来自主脚本压缩。

    . .team/perf/tmp/env.sh
    BASE_URL=$BASE_URL SESSION_COOKIE=$SESSION_COOKIE \
      python3 tools/browser/shots_batch7.py --out-dir .team/perf/batch7-shots
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(HERE))

import local_assets  # noqa: E402

LEGACY = REPO / "web" / "static" / "legacy"
PAGES = {
    "cards": ".cards-page",
    "likes": ".msg-card",
    "home": ".activity-card, .van-empty",
    "profile": ".profile-header",
}
# 数据加载稳定的额外判据（避免「加载中占位 vs 数字」造成结构差异噪声）
SETTLE = {
    "cards": "() => !!document.querySelector('.cards-page .card-counter, .cards-page .swipe-card, .cards-page .van-empty')",
    "likes": "() => { const c = document.querySelector('.msg-count'); return !c || !c.closest('.msg-count-wrap').querySelector('.van-loading'); }",
    "home": "() => { const cards = document.querySelectorAll('.activity-card'); return cards.length > 0 || !!document.querySelector('.van-empty'); }",
    "profile": "() => !!document.querySelector('.profile-header')",
}
TAB_LABEL = {"cards": "牵线", "likes": "消息", "home": "活动", "profile": "我的"}
VIEWPORTS = (390, 768, 1280)


def build_arms() -> list[local_assets.Arm]:
    min_js = LEGACY / "app.min.js"
    if not min_js.exists():
        raise SystemExit("app.min.js 不存在：先跑 tools/spa_minify 的 npm run build")
    # control 用未压缩 app.js；from_files 会把 script src 与 preload href 一起改写，
    # 避免 preload 仍指向 app.min.js（staging 上不存在 → 404 噪声）。
    return [
        local_assets.Arm.from_files("control", LEGACY / "index.html", LEGACY / "app.js"),
        local_assets.Arm.from_files("treatment", LEGACY / "index.html", min_js),
    ]


def snapshot(page) -> dict:
    return page.evaluate(
        """() => {
            const app = document.getElementById('app');
            const norm = (app ? app.innerHTML : '').replace(/\\?v=[^"'\\s)]*/g, '').replace(/\\s+/g, ' ').trim();
            return {
                html: norm,
                html_nodigit: norm.replace(/\\d+/g, '#'),
                tabbars: document.querySelectorAll('.van-tabbar').length,
                dark: document.documentElement.classList.contains('van-theme-dark'),
                errors: window.__yxq_shot_errors || []
            };
        }"""
    )


def wait_ready(page, marker: str, timeout: int = 15000) -> None:
    page.wait_for_function(
        """(sel) => {
            if (document.querySelector('.page-chunk-loading')) return false;
            const root = document.querySelector('.tab-main > *');
            if (!root) return false;
            if (!root.matches(sel) && !root.querySelector(sel)) return false;
            const r = root.getBoundingClientRect();
            return r.width > 0 && r.height > 0;
        }""",
        arg=marker,
        timeout=timeout,
    )


def click_tab(page, label: str) -> None:
    page.evaluate(
        """(label) => {
            const mask = document.querySelector('.swipe-guide-mask');
            if (mask) mask.remove();
            const item = [...document.querySelectorAll('.van-tabbar-item')]
                .find((el) => el.innerText.includes(label));
            if (!item) throw new Error('找不到 tab: ' + label);
            item.click();
        }""",
        label,
    )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out-dir", default=str(REPO / ".team/perf/batch7-shots"))
    ap.add_argument("--pages", default="cards,likes,home,profile")
    args = ap.parse_args()
    out_dir = Path(args.out_dir)
    base_url = os.environ.get("BASE_URL", "").strip().rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "").strip()
    if not base_url or not cookie:
        sys.exit("缺少 BASE_URL 或 SESSION_COOKIE")

    arms = build_arms()
    pages = [p.strip() for p in args.pages.split(",") if p.strip()]
    report: dict = {"pages": pages, "arms": {}, "parity": {}}
    failures: list[str] = []

    with sync_playwright() as pw:
        browser = pw.chromium.launch(args=["--no-sandbox"])
        for arm in arms:
            arm_shots = out_dir / arm.name
            arm_shots.mkdir(parents=True, exist_ok=True)
            report["arms"][arm.name] = {}
            for width in VIEWPORTS:
                for theme in ("light", "dark"):
                    context = browser.new_context(
                        viewport={"width": width, "height": 900 if width == 1280 else 844},
                        locale="zh-CN",
                    )
                    context.add_cookies([{
                        "name": "yxq_session", "value": cookie, "url": base_url,
                        "httpOnly": True, "sameSite": "Lax",
                    }])
                    if theme == "dark":
                        context.add_init_script("try{localStorage.setItem('yxq_dark','1')}catch(e){}")
                    context.add_init_script(
                        "window.__yxq_shot_errors=[];"
                        "window.addEventListener('error',e=>window.__yxq_shot_errors.push(String(e.message||e)));"
                    )
                    local_assets.install(context, base_url, arm)
                    # 本机壳顶替文档后服务端不会下发 csrf cookie；用 context.request 走一次
                    # 真实文档请求把 cookie 种上，避免写入类接口 403 污染 console 断言。
                    context.request.get(base_url + "/", timeout=30000)
                    page = context.new_page()
                    console_errors: list[str] = []
                    page.on("console", lambda m: console_errors.append(m.text[:160]) if m.type == "error" else None)
                    page.on("pageerror", lambda e: console_errors.append("pageerror: " + str(e)[:160]))
                    page.goto(base_url + "/", wait_until="domcontentloaded", timeout=60000)
                    page.wait_for_selector(".van-tabbar", timeout=30000)
                    try:
                        page.locator("#boot-splash").wait_for(state="detached", timeout=20000)
                    except Exception:  # noqa: BLE001 - hero 已移除/不存在都算继续
                        pass
                    for name in pages:
                        click_tab(page, TAB_LABEL[name])
                        wait_ready(page, PAGES[name])
                        try:
                            page.wait_for_function(SETTLE[name], timeout=8000)
                        except Exception:  # noqa: BLE001 - 数据没到就照实截图，差异会在 parity 里体现
                            pass
                        page.wait_for_timeout(250)  # 图片/过渡收尾
                        shot = arm_shots / f"{width}-{theme}-{name}.png"
                        page.screenshot(path=str(shot))
                        snap = snapshot(page)
                        report["arms"][arm.name][f"{width}-{theme}-{name}"] = {
                            "shot": str(shot.relative_to(REPO)) if shot.is_relative_to(REPO) else str(shot),
                            "html_sha": hashlib.sha256(snap["html"].encode()).hexdigest()[:16],
                            "html_nodigit_sha": hashlib.sha256(snap["html_nodigit"].encode()).hexdigest()[:16],
                            "html_bytes": len(snap["html"]),
                            "tabbars": snap["tabbars"],
                            "dark": snap["dark"],
                            "console_errors": console_errors[-3:],
                        }
                        if console_errors:
                            failures.append(f"{arm.name} {width}-{theme}-{name}: console {console_errors[-1]}")
                    context.close()

        # 结构等价比对：精确 sha 不等时再看「数字归一」后是否一致，
        # 以区分「计数/时间抖动」与真正的结构差异（压缩破坏语义会是后者）。
        common = sorted(set(report["arms"]["control"]) & set(report["arms"]["treatment"]))
        diffs, digit_only = [], []
        for key in common:
            c = report["arms"]["control"][key]
            t = report["arms"]["treatment"][key]
            if c["html_sha"] == t["html_sha"] and c["tabbars"] == t["tabbars"]:
                continue
            entry = {
                "case": key,
                "control": c["html_sha"],
                "treatment": t["html_sha"],
                "bytes": [c["html_bytes"], t["html_bytes"]],
                "tabbars": [c["tabbars"], t["tabbars"]],
            }
            if c["html_nodigit_sha"] == t["html_nodigit_sha"] and c["tabbars"] == t["tabbars"]:
                digit_only.append(entry)
            else:
                diffs.append(entry)
        report["parity"] = {"cases": len(common), "diffs": diffs, "digit_only": digit_only}
        browser.close()

    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=1), encoding="utf-8")
    parity = report["parity"]
    print(f"shots: {parity['cases']} cases x 2 arms; 结构差异 {len(parity['diffs'])}; 仅数字抖动 {len(parity['digit_only'])}")
    for d in parity["diffs"][:10]:
        print(" diff:", d)
    for d in parity["digit_only"][:5]:
        print(" digit-only:", d["case"], d["bytes"])
    if failures:
        print("FAIL console/pageerror:")
        for f in failures[:10]:
            print(" -", f)
        return 1
    if parity["diffs"]:
        print("注意：存在结构差异（见 report.json），需人眼确认截图")
        return 1
    print("PASS 结构等价（数字抖动单列）+ 0 console error")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
