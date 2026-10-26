#!/usr/bin/env python3
"""app.min.js 生成物门禁（CI 不依赖 Node）。

`make spa-check` 的一部分。检查项：

1. manifest 的 source（app.js）sha256 + bytes 必须等于当前源文件——防止改了
   app.js 没重建 min；
2. manifest 的 min（app.min.js）sha256 + bytes 必须等于当前提交的生成物——
   防止手改生成物；
3. 壳 web/static/legacy/index.html 必须在按序注入的 ASSETS 数组里包含 app.min.js，
   且不得再把 app.js 当脚本加载；同时 app.js 源文件必须仍在（回退与调试用）。
   批次10 起主脚本不再 preload（预下载会与首屏 hero 图抢带宽，实测 LCP 更差），
   因此 preload 不是门禁项。

精确的「重新 minify 后逐字节一致」由 Node 侧 `npm run check`（make spa-check-node，
开发机）保证；CI 只校验已提交产物与源的一致性。

用法：
    python3 tools/spa_minify/check.py            # 校验仓库
    python3 tools/spa_minify/check.py --root DIR # 校验 fixture 目录（测试用）
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]

# 批次10 起主脚本不再用静态 defer 标签执行：壳里必须有一个按序注入的
# ASSETS 数组，且其中包含带版本的 app.min.js。
SHELL_SCRIPT = re.compile(
    r'<script[^>]+src="app\.min\.js\?v=__ASSET_URL_VERSION__"'
    r'|ASSETS\s*=\s*\[[^\]]*app\.min\.js\?v=__ASSET_URL_VERSION__',
    re.S,
)
SHELL_ASSETS = re.compile(r'ASSETS\s*=\s*\[(.*?)\]', re.S)
SHELL_SOURCE_SCRIPT = re.compile(r'<script[^>]+src="app\.js\?')


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check(root: Path) -> list[str]:
    """返回错误列表；空列表表示通过。"""
    legacy = root / "web" / "static" / "legacy"
    manifest_path = legacy / "app.min.manifest.json"
    source_path = legacy / "app.js"
    min_path = legacy / "app.min.js"
    shell_path = legacy / "index.html"

    if not manifest_path.exists():
        return ["app.min.manifest.json 缺失（run: cd tools/spa_minify && npm run build）"]
    try:
        data = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        return [f"app.min.manifest.json 不是合法 JSON：{exc}"]
    for key in ("source", "min", "tool"):
        if key not in data:
            return [f"app.min.manifest.json 缺字段 {key}"]

    errors: list[str] = []
    for label, entry, path in (
        ("app.js（manifest.source）", data["source"], source_path),
        ("app.min.js（manifest.min）", data["min"], min_path),
    ):
        if not path.exists():
            errors.append(f"{label} 文件缺失：{path}")
            continue
        actual_bytes = path.stat().st_size
        if entry.get("bytes") != actual_bytes:
            errors.append(
                f"{label} bytes 过期：manifest={entry.get('bytes')} 实际={actual_bytes}"
                "（改完源文件请重建并提交生成物）"
            )
        actual_sha = sha256(path)
        if entry.get("sha256") != actual_sha:
            errors.append(
                f"{label} sha256 过期：manifest={str(entry.get('sha256'))[:12]} 实际={actual_sha[:12]}"
            )

    if not shell_path.exists():
        errors.append("web/static/legacy/index.html 缺失")
        return errors
    html = shell_path.read_text(encoding="utf-8")
    if not SHELL_SCRIPT.search(html):
        errors.append('壳必须按序注入 app.min.js?v=__ASSET_URL_VERSION__（ASSETS 数组）')
    else:
        assets = SHELL_ASSETS.search(html)
        if not assets:
            errors.append("壳缺少 ASSETS 注入数组")
        else:
            order = [
                name
                for name in ("vue.global.prod.js", "app.render.js", "app.min.js")
                if name in assets.group(1)
            ]
            if order != ["vue.global.prod.js", "app.render.js", "app.min.js"]:
                errors.append(f"ASSETS 注入顺序必须为 vue → render → app.min，实际 {order}")
    if SHELL_SOURCE_SCRIPT.search(html):
        errors.append("壳不得再直接加载 app.js（app.js 只作为源文件/回退保留）")
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=str(REPO), help="仓库根目录（默认本仓库）")
    args = parser.parse_args(argv)
    root = Path(args.root)
    errors = check(root)
    if errors:
        for err in errors:
            print(f"spa_minify: {err}", file=sys.stderr)
        return 1
    data = json.loads((root / "web/static/legacy/app.min.manifest.json").read_text(encoding="utf-8"))
    print(
        "spa_minify ok: app.js {}B -> app.min.js {}B (esbuild {}, source sha {})".format(
            data["source"]["bytes"],
            data["min"]["bytes"],
            data.get("tool", {}).get("version", "?"),
            str(data["source"]["sha256"])[:12],
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
