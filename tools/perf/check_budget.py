#!/usr/bin/env python3
"""首载静态 gzip 预算门禁（无 Node、无 Go、无网络）。

指标定义（全部只读仓库文件，结果确定）：
  shell_gzip               `/app` 壳：index.html + 内联 critical CSS，替换版本占位符后
                           以 zlib gzip level 6 压缩，再加 budgets.json 的
                           `bootstrap_allowance_bytes`（按 batch10 `/app` 实测校准）。
  app_min_gzip             app.min.js 的预压缩字节（部署时 Go 原样返回）。
  initial_render_gzip      app.render.js 的预压缩字节（根模板 + cards）。
  vant_subset_js/css_gzip  Vant 子集预压缩字节。
  critical_css_gzip        app.critical.css 预压缩字节；它同时内联在 shell 里。
  page_chunks_gzip         pages/*.render.js 五个懒加载 chunk 合计预压缩字节。
  first_load_static_gzip   首载静态估算：shell + app.min + app.render + Vant 子集 JS/CSS
                           + vue + app.css + PhotoSwipe 三件套 + favicon.ico；
                           不含 API 与用户照片，且 critical CSS 已在 shell 内只计一次。

超限 exit 1。预算只能手工修改 `budgets.json` 并走 Review；不提供自动更新命令。
budget 必须 ≤ baseline×1.10，防止只改上限不留基线。
"""

from __future__ import annotations

import argparse
import gzip
import json
import re
import sys
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_BUDGETS = Path(__file__).resolve().parent / "budgets.json"

VERSION_RE = re.compile(r'const\s+assetVersion\s*=\s*"([0-9]+)"')
MARKERS = (
    b"__YXQ_AUTHENTICATED__=false;",
    b"__YXQ_VERSION_VALUE__",
    b"__ASSET_VERSION_VALUE__",
    b"__ASSET_URL_VERSION__",
    b"<!--YXQ_CRITICAL_CSS-->",
)


class GuardError(RuntimeError):
    pass


def asset_version(root: Path) -> str:
    text = (root / "internal/httpserver/templates.go").read_text(encoding="utf-8")
    match = VERSION_RE.search(text)
    if not match:
        raise GuardError("internal/httpserver/templates.go 缺少 assetVersion 常量")
    return match.group(1)


def file_size(path: Path) -> int:
    if not path.is_file():
        raise GuardError(f"缺少文件：{path}")
    return path.stat().st_size


def shell_gzip(root: Path, bootstrap_allowance: int, boot_card_allowance: int = 0) -> int:
    legacy = root / "web/static/legacy"
    index = (legacy / "index.html").read_bytes()
    critical = (legacy / "app.critical.css").read_bytes()
    missing = [m.decode() for m in MARKERS if m not in index]
    if missing:
        raise GuardError("壳模板缺少占位符：" + ", ".join(missing))
    version = asset_version(root).encode()
    body = index.replace(b"__YXQ_AUTHENTICATED__=false;", b"__YXQ_AUTHENTICATED__=true;", 1)
    # 固定占位版本长度，保证预算可复现（实际版本只差几个字节）。
    body = body.replace(b"__YXQ_VERSION_VALUE__", b"staging-6b1fb23", 1)
    body = body.replace(b"__ASSET_VERSION_VALUE__", version)
    body = body.replace(b"__ASSET_URL_VERSION__", version)
    body = body.replace(b"<!--YXQ_CRITICAL_CSS-->", b"<style>" + critical + b"</style>", 1)
    return len(gzip.compress(body, compresslevel=6, mtime=0)) + bootstrap_allowance + boot_card_allowance


def measure(root: Path, bootstrap_allowance: int = 0, boot_card_allowance: int = 0) -> dict[str, int]:
    legacy = root / "web/static/legacy"
    metrics = {
        "shell_gzip": shell_gzip(root, bootstrap_allowance, boot_card_allowance),
        "app_min_gzip": file_size(legacy / "app.min.js.gz"),
        "initial_render_gzip": file_size(legacy / "app.render.js.gz"),
        "vant_subset_js_gzip": file_size(legacy / "lib/vant.subset.min.js.gz"),
        "vant_subset_css_gzip": file_size(legacy / "lib/vant.subset.min.css.gz"),
        "critical_css_gzip": file_size(legacy / "app.critical.css.gz"),
        "page_chunks_gzip": sum(file_size(p) for p in sorted((legacy / "pages").glob("*.render.js.gz"))),
    }
    photoswipe = sum(
        file_size(legacy / name)
        for name in (
            "lib/photoswipe.css.gz",
            "lib/photoswipe.umd.min.js.gz",
            "lib/photoswipe-lightbox.umd.min.js.gz",
        )
    )
    metrics["first_load_static_gzip"] = (
        metrics["shell_gzip"]
        + metrics["app_min_gzip"]
        + metrics["initial_render_gzip"]
        + metrics["vant_subset_js_gzip"]
        + metrics["vant_subset_css_gzip"]
        + file_size(legacy / "lib/vue.global.prod.js.gz")
        + file_size(legacy / "app.css.gz")
        + file_size(legacy / "boot-card.js.gz")
        + photoswipe
        + file_size(legacy / "favicon.ico")
    )
    return metrics


def ceil_10pct(value: int) -> int:
    return (value * 110 + 99) // 100


def load_budgets(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise GuardError(f"读取预算失败：{path}: {exc}") from exc
    if not isinstance(data.get("limits"), dict):
        raise GuardError(f"预算文件缺少 limits：{path}")
    return data


def validate_specs(data: dict, measured: dict[str, int]) -> list[str]:
    errors: list[str] = []
    limits = data["limits"]
    for key in measured:
        spec = limits.get(key)
        if not isinstance(spec, dict):
            errors.append(f"{key}: 预算文件缺少该指标")
            continue
        baseline = spec.get("baseline_bytes")
        budget = spec.get("budget_bytes")
        if not isinstance(baseline, int) or baseline <= 0:
            errors.append(f"{key}: baseline_bytes 必须是正整数")
            continue
        if not isinstance(budget, int) or budget <= 0:
            errors.append(f"{key}: budget_bytes 必须是正整数")
            continue
        max_budget = ceil_10pct(baseline)
        if budget > max_budget:
            errors.append(
                f"{key}: budget {budget:,} 超过 baseline×1.10={max_budget:,}；"
                "请手工修改 baseline/budget 并走 Review"
            )
    return errors


def render_table(measured: dict[str, int], data: dict) -> tuple[str, list[str]]:
    limits = data["limits"]
    rows = ["指标                          当前       预算       余量      状态"]
    rows.append("-" * 68)
    over: list[str] = []
    for key, current in measured.items():
        spec = limits.get(key, {})
        budget = spec.get("budget_bytes", 0)
        if not isinstance(budget, int):
            budget = 0
        headroom = budget - current
        status = "OK" if current <= budget else f"OVER +{current - budget:,}"
        if current > budget:
            over.append(key)
        rows.append(f"{key:<28} {current:>8,}  {budget:>9,}  {headroom:>+9,}  {status}")
    return "\n".join(rows), over


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=str(DEFAULT_ROOT), help="仓库根目录")
    parser.add_argument("--budgets", default=str(DEFAULT_BUDGETS), help="budgets.json 路径")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    budgets_path = Path(args.budgets).resolve()
    try:
        data = load_budgets(budgets_path)
        allowance = data.get("bootstrap_allowance_bytes", 0)
        if not isinstance(allowance, int) or allowance < 0:
            raise GuardError("bootstrap_allowance_bytes 必须是非负整数")
        boot_card_allowance = data.get("boot_card_allowance_bytes", 0)
        if not isinstance(boot_card_allowance, int) or boot_card_allowance < 0:
            raise GuardError("boot_card_allowance_bytes 必须是非负整数")
        measured = measure(root, allowance, boot_card_allowance)
        errors = validate_specs(data, measured)
        table, over = render_table(measured, data)
    except GuardError as exc:
        print(f"性能预算检查失败：{exc}", file=sys.stderr)
        return 2

    print(table)
    print()
    if errors:
        for err in errors:
            print(f"CONFIG {err}", file=sys.stderr)
    if over:
        for key in over:
            spec = data["limits"].get(key, {})
            print(
                f"OVER {key}: 实测 {measured[key]:,}B > 预算 {spec.get('budget_bytes', 0):,}B；"
                "先优化；确需放宽时手工修改 budgets.json 并走 Review",
                file=sys.stderr,
            )
    if errors or over:
        return 1
    print("PASS：所有首载静态 gzip 指标在预算内。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
