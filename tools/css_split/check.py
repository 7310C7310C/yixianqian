#!/usr/bin/env python3
"""app.critical.css 门禁（CI 不依赖 Node，纯标准库）。

`make spa-check` 的一部分：

1. 生成物必须与当前 app.css 一致（重算后逐字节比对，防止改 CSS 没重建）；
2. critical 里的每个顶层块都必须能在 app.css 中逐字找到（关键子集，不允许手改）；
3. critical gzip ≤ 15KB（首屏内联预算）；
4. 壳必须内联 critical（`<!--YXQ_CRITICAL_CSS-->` 由服务端替换）并且异步链接
   `app.css`（media=print onload 或 noscript 兜底），不得再把 app.css 当阻塞样式。
"""

from __future__ import annotations

import gzip
import importlib.util
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
LEGACY = REPO / "web" / "static" / "legacy"
BUILD = Path(__file__).resolve().parent / "build.py"

spec = importlib.util.spec_from_file_location("css_split_build", BUILD)
build_mod = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = build_mod
spec.loader.exec_module(build_mod)

MAX_GZIP = 15 * 1024


def check(root: Path) -> list[str]:
    legacy = root / "web" / "static" / "legacy"
    source = legacy / "app.css"
    critical = legacy / "app.critical.css"
    shell = legacy / "index.html"
    errors: list[str] = []
    if not source.exists():
        return ["app.css 缺失"]
    if not critical.exists():
        return ["app.critical.css 缺失（run: python3 tools/css_split/build.py）"]

    build_mod.SOURCE = source
    build_mod.OUTPUT = critical
    expected, manifest = build_mod.build()
    actual = critical.read_text(encoding="utf-8")
    if actual != expected:
        errors.append("app.critical.css 与 app.css 不一致（重跑 tools/css_split/build.py）")

    src_text = source.read_text(encoding="utf-8")
    for block in build_mod.split_blocks(actual):
        block = block.strip()
        if not block or block.startswith("/*!"):
            continue
        if block not in src_text:
            errors.append(f"critical 块不在 app.css 中（疑似手改）：{block[:60]!r}")
            break

    manifest_path = legacy / "app.critical.manifest.json"
    if not manifest_path.exists():
        errors.append("app.critical.manifest.json 缺失（run: python3 tools/css_split/build.py）")
    else:
        import json

        try:
            mf = json.loads(manifest_path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            errors.append(f"app.critical.manifest.json 不是合法 JSON：{exc}")
            mf = {}
        crit = mf.get("critical", {})
        if crit.get("bytes") != len(actual.encode()):
            errors.append(f"manifest bytes 过期：{crit.get('bytes')} != {len(actual.encode())}")
        if crit.get("gzip_bytes") != len(gzip.compress(actual.encode(), 9)):
            errors.append("manifest gzip_bytes 过期")
        if crit.get("sha256") != __import__("hashlib").sha256(actual.encode()).hexdigest():
            errors.append("manifest sha256 过期（改 0…0 会红）")

    gz = len(gzip.compress(actual.encode(), 9))
    if gz > MAX_GZIP:
        errors.append(f"critical gzip {gz}B > {MAX_GZIP}B（首屏预算）")

    html = shell.read_text(encoding="utf-8")
    if "<!--YXQ_CRITICAL_CSS-->" not in html:
        errors.append("壳缺少 <!--YXQ_CRITICAL_CSS--> 占位")
    if 'href="app.css?v=__ASSET_URL_VERSION__"' not in html:
        errors.append("壳必须链接 app.css（完整样式）")
    for line in html.splitlines():
        if "app.css" not in line or 'rel="stylesheet"' not in line:
            continue
        in_noscript = line.strip().startswith("<noscript>")
        async_ok = 'media="print"' in line and "onload=" in line
        if not async_ok and not in_noscript:
            errors.append("壳不得把 app.css 当渲染阻塞样式加载（需 media=print onload 或 noscript）")
            break
    if manifest["critical"]["gzip_bytes"] > MAX_GZIP:
        errors.append("manifest gzip 超预算")
    return errors


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    root = REPO
    if "--root" in argv:
        root = Path(argv[argv.index("--root") + 1])
    errors = check(root)
    if errors:
        for err in errors:
            print(f"css_split: {err}", file=sys.stderr)
        return 1
    critical = (root / "web/static/legacy/app.critical.css").read_text(encoding="utf-8")
    print(f"css_split ok: critical {len(critical.encode())}B raw, "
          f"{len(gzip.compress(critical.encode(), 9))}B gzip")
    return 0


if __name__ == "__main__":
    sys.exit(main())
