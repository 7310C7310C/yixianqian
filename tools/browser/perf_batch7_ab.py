#!/usr/bin/env python3
"""批次7 A2 A/B：app.js（control）vs app.min.js（treatment）的冷/热首屏比较。

口径沿用 tools/browser/perf_boot.py：390x844、1.6Mbps/150ms、CPU×4、
冷启动＝新 context + 关 HTTP 缓存、热启动＝同 context 预热后连刷 N 次；采
FCP/LCP/LCP 元素/最长长任务/累计长任务/CLS/`yxq-mount`/请求数。

两臂都通过 tools/browser/local_assets.py 把外壳与主脚本换成本机文件（其余资源与
API 仍走 staging），因此两臂的差异**只反映脚本解析/执行**：

- control   ：origin/main 的 index.html + app.js（未压缩）
- treatment ：工作区 index.html + app.min.js（esbuild 压缩）

**限制（写进报告要用）**：route.fulfill 不经过网络频宽模拟，两个脚本的下载时间
都不计入；所以这里只能证明 CPU/执行差异，真实冷/热 FCP/LCP 与首载字节必须在
部署后的 staging 上另测。脚本自身字节用 gzip -9 单独报（见 --bytes）。

    . .team/perf/tmp/env.sh
    BASE_URL=$BASE_URL SESSION_COOKIE=$SESSION_COOKIE \
      python3 tools/browser/perf_batch7_ab.py --n 3 --out /tmp/opencode/perf-b7.json
"""

from __future__ import annotations

import argparse
import json
import os
import statistics
import sys
import time
import zlib
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - 环境守卫
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(HERE))

import local_assets  # noqa: E402
import perf_boot  # noqa: E402

LEGACY = REPO / "web" / "static" / "legacy"
KEYS = ("fcp", "lcp", "longest_task", "longtask_total", "requests", "mount_ms", "boot_ms")


def median(values: list[float]):
    vals = [v for v in values if v is not None]
    return round(statistics.median(vals), 1) if vals else None


def build_arms() -> tuple[list[local_assets.Arm], dict]:
    treatment_shell = LEGACY / "index.html"
    min_js = LEGACY / "app.min.js"
    if not min_js.exists():
        raise SystemExit(
            "app.min.js 不存在：先在 tools/spa_minify 跑 npm run build（A2 未完成，不能量 treatment）"
        )
    html = treatment_shell.read_text(encoding="utf-8")
    # control 用未压缩 app.js；from_files 同时改写 script src 与 preload href，
    # 两臂请求集合完全一致（否则 control 的 preload 会 404，多一个请求）。
    arms = [
        local_assets.Arm.from_files("control", treatment_shell, LEGACY / "app.js"),
        local_assets.Arm.from_files("treatment", treatment_shell, min_js),
    ]
    sizes = {}
    for arm in arms:
        sizes[arm.name] = {
            "script": arm.script_name,
            "raw_bytes": len(arm.script_bytes),
            "gzip_bytes": len(zlib.compress(arm.script_bytes, 9)),
        }
    return arms, sizes


def run_arm(browser, base_url: str, cookie: str, arm, args) -> dict:
    width, height = args.width, args.height
    down = int(args.down_mbps * 1024 * 1024 / 8)
    out = {"cold": [], "warm": []}
    for mode in ("cold", "warm"):
        context = None
        page = None
        for i in range(args.runs):
            if mode == "cold" or context is None:
                if context is not None:
                    context.close()
                context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
                context.add_cookies([{
                    "name": "yxq_session", "value": cookie, "url": base_url,
                    "httpOnly": True, "sameSite": "Lax",
                }])
                local_assets.install(context, base_url, arm)
                page = context.new_page()
                page.add_init_script(perf_boot.INIT)
                cdp = context.new_cdp_session(page)
                cdp.send("Network.enable")
                cdp.send("Network.setCacheDisabled", {"cacheDisabled": mode == "cold"})
                cdp.send("Emulation.setCPUThrottlingRate", {"rate": args.cpu})
                cdp.send("Network.emulateNetworkConditions", {
                    "offline": False, "latency": args.latency,
                    "downloadThroughput": down, "uploadThroughput": int(down / 2),
                })
                if mode == "warm":
                    perf_boot.run_once(context, page, base_url, "?warmup=1")
                    perf_boot.run_once(context, page, base_url, reload=True)
            suffix = f"?ab={arm.name}-{mode}-{i}-{int(time.time() * 1000)}"
            out[mode].append(perf_boot.run_once(context, page, base_url, suffix, reload=(mode == "warm")))
        context.close()
    summary = {}
    for mode in ("cold", "warm"):
        runs = out[mode]
        med = {k: median([r.get(k) for r in runs]) for k in KEYS}
        med["cls"] = max((r.get("cls") or 0) for r in runs)
        med["lcp_el"] = runs[-1].get("lcp_el", "")
        med["lcp_class"] = runs[-1].get("lcp_class", "")
        med["requests"] = median([r.get("requests") for r in runs])
        summary[mode] = med
    return {"summary": summary, "runs": out}


def fmt_delta(control, treatment, key: str) -> str:
    c, t = control.get(key), treatment.get(key)
    if c is None or t is None:
        return "n/a"
    diff = round(t - c, 1)
    pct = (diff / c * 100) if c else 0
    return f"{diff:+.0f}ms ({pct:+.1f}%)"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="/tmp/opencode/perf-b7-ab.json")
    ap.add_argument("--markdown", default="")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--width", type=int, default=390)
    ap.add_argument("--height", type=int, default=844)
    ap.add_argument("--latency", type=float, default=150)
    ap.add_argument("--down-mbps", type=float, default=1.6)
    ap.add_argument("--cpu", type=float, default=4)
    args = ap.parse_args()

    base_url = os.environ.get("BASE_URL", "").strip().rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "").strip()
    if not base_url or not cookie:
        sys.exit("缺少 BASE_URL 或 SESSION_COOKIE")

    arms, sizes = build_arms()
    result = {"meta": {"base_url": base_url, "runs": args.runs, "cpu": args.cpu,
                       "down_mbps": args.down_mbps, "latency": args.latency,
                       "viewport": [args.width, args.height],
                       "note": "fulfill 不经网络模拟，只比较 CPU/执行；字节见 sizes"},
              "sizes": sizes, "arms": {}}
    with sync_playwright() as pw:
        browser = pw.chromium.launch(args=["--no-sandbox"])
        for arm in arms:
            result["arms"][arm.name] = run_arm(browser, base_url, cookie, arm, args)
        browser.close()

    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")

    control = result["arms"]["control"]["summary"]
    treatment = result["arms"]["treatment"]["summary"]
    lines = [
        "# 批次7 A2 A/B（app.js vs app.min.js）",
        "",
        f"- 环境：{base_url}、{args.width}x{args.height}、CPU×{args.cpu}、"
        f"{args.down_mbps}Mbps/{args.latency}ms、N={args.runs}",
        "- 口径：两臂都经 route.fulfill 替换外壳+主脚本，差异只反映解析/执行；"
        "不报传输字节与 TTFB（真实值以部署后 staging 为准）。",
        "",
        "| 脚本 | raw | gzip -9 |",
        "|---|---|---|",
        f"| app.js（control） | {sizes['control']['raw_bytes']:,}B | {sizes['control']['gzip_bytes']:,}B |",
        f"| app.min.js（treatment） | {sizes['treatment']['raw_bytes']:,}B | {sizes['treatment']['gzip_bytes']:,}B |",
        "",
        "| 模式 | 指标 | control | treatment | Δ |",
        "|---|---|---|---|---|",
    ]
    for mode in ("cold", "warm"):
        for key, label in (
            ("fcp", "FCP"), ("lcp", "LCP"), ("longest_task", "最长任务"),
            ("longtask_total", "长任务累计"), ("mount_ms", "yxq-mount"), ("boot_ms", "yxq-boot"),
            ("requests", "请求数"),
        ):
            lines.append(
                f"| {mode} | {label} | {control[mode].get(key)} | {treatment[mode].get(key)} | "
                f"{fmt_delta(control[mode], treatment[mode], key)} |"
            )
    lines += [
        "",
        f"- LCP 元素（control/warm）：`{control['warm'].get('lcp_el', '')[:120]}`",
        f"- LCP 元素（treatment/warm）：`{treatment['warm'].get('lcp_el', '')[:120]}`",
        f"- CLS（max）：control 冷 {control['cold'].get('cls')} / 热 {control['warm'].get('cls')}；"
        f"treatment 冷 {treatment['cold'].get('cls')} / 热 {treatment['warm'].get('cls')}",
    ]
    md = "\n".join(lines) + "\n"
    if args.markdown:
        Path(args.markdown).parent.mkdir(parents=True, exist_ok=True)
        Path(args.markdown).write_text(md, encoding="utf-8")
    print(md)
    print(f"JSON: {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
