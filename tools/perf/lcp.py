#!/usr/bin/env python3
"""浏览器 LCP 基准（慢机档）：固定 390×844、CPU×4、1.6Mbps/150ms，冷/热各 N 次。

复用 `tools/browser/perf_boot.py` 的 Playwright 采集口径（FCP/LCP/最长任务/CLS/
传输字节/请求数），不重复实现浏览器探针。依赖：
    pip install playwright && python3 -m playwright install chromium

`BASE_URL` 与 `SESSION_COOKIE` 从环境读取；cookie 不会出现在命令行或输出里。
默认 N=3（建议 ≥3，正式对比用 5）；输出 JSON 后由人工/审查对比同机前后值。

示例：
    BASE_URL=https://next.nantou.love SESSION_COOKIE=... \
      python3 tools/perf/lcp.py --runs 3 --out /tmp/yxq-perf/lcp.json
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PERF_BOOT = ROOT / "tools/browser/perf_boot.py"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--runs", type=int, default=3, help="冷/热各跑几次（默认 3，正式对比建议 5）")
    parser.add_argument("--width", type=int, default=390)
    parser.add_argument("--height", type=int, default=844)
    parser.add_argument("--latency", type=float, default=150.0, help="网络 RTT ms（默认 150）")
    parser.add_argument("--down-mbps", type=float, default=1.6, help="下行 Mbps（默认 1.6）")
    parser.add_argument("--cpu", type=float, default=4.0, help="CPU 节流倍数（默认 4）")
    parser.add_argument("--out", default="/tmp/yxq-perf/lcp.json")
    args = parser.parse_args(argv)

    if not os.environ.get("BASE_URL", "").strip():
        parser.error("缺少 BASE_URL")
    if not os.environ.get("SESSION_COOKIE", "").strip():
        parser.error("缺少 SESSION_COOKIE（只从环境读取，不落命令行）")
    if args.runs < 1:
        parser.error("--runs 必须 ≥1")
    if not PERF_BOOT.is_file():
        print(f"缺少 {PERF_BOOT}", file=sys.stderr)
        return 2

    cmd = [
        sys.executable, str(PERF_BOOT),
        "--runs", str(args.runs),
        "--width", str(args.width),
        "--height", str(args.height),
        "--latency", str(args.latency),
        "--down-mbps", str(args.down_mbps),
        "--cpu", str(args.cpu),
        "--out", args.out,
    ]
    print(
        f"LCP 慢机档：{args.width}x{args.height} CPU×{args.cpu:g} "
        f"{args.down_mbps:g}Mbps/{args.latency:g}ms，冷/热各 {args.runs} 次"
    )
    try:
        return subprocess.run(cmd, env=os.environ.copy(), check=False).returncode
    except FileNotFoundError:
        print("找不到 python3；请确认运行环境", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
