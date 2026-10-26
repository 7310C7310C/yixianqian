#!/usr/bin/env python3
"""静态资源版本门禁：web/static 变更必须同步 bump httpserver.assetVersion。

为什么需要：静态资源带 `?v=<assetVersion>` 且 `immutable` 1 年；只改文件不改版本，
浏览器/边缘会继续用旧缓存。批次10 曾出现 `app.render.js` 已更新但版本仍 12 的窗口。

规则（相对 --base，默认 HEAD^）：
1. 若 `web/static/**` 有内容变更，`internal/httpserver/templates.go` 的
   `assetVersion` 必须与 base 不同；否则失败。
2. `web/static/admin_*.css` 的 `admin.css?v=N` 必须等于当前 assetVersion。

CI 使用 fetch-depth >= 2 后运行 `python3 tools/check_asset_version.py --base HEAD^`；
本地 `make ci` 同样执行。不需要 Node。
"""
from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
TEMPLATES = "internal/httpserver/templates.go"
VERSION_RE = re.compile(r'const\s+assetVersion\s*=\s*"([0-9]+)"')
IMPORT_RE = re.compile(r'/static/admin\.css\?v=([0-9]+)')


def git(*args: str) -> str:
    return subprocess.run(
        ["git", "-C", str(REPO), *args],
        check=True, capture_output=True, text=True,
    ).stdout


def read_version(text: str) -> str | None:
    m = VERSION_RE.search(text)
    return m.group(1) if m else None


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default="HEAD^", help="git base revision (default HEAD^)")
    args = ap.parse_args(argv)

    try:
        current = read_version((REPO / TEMPLATES).read_text(encoding="utf-8"))
        base_text = git("show", f"{args.base}:{TEMPLATES}")
        base_version = read_version(base_text)
        changed = [
            p for p in git("diff", "--name-only", args.base).splitlines()
            if p.startswith("web/static/")
        ]
    except subprocess.CalledProcessError as exc:
        print(f"asset-version: 无法读取 git base {args.base!r}: {exc}", file=sys.stderr)
        return 1

    errors: list[str] = []
    if not current:
        errors.append(f"{TEMPLATES} 缺少 assetVersion 常量")
    if changed and base_version == current:
        errors.append(
            f"web/static/** 有 {len(changed)} 个变更，但 assetVersion 仍为 {current}；"
            "必须 bump internal/httpserver/templates.go 并同步 admin CSS @import"
        )
    for css in sorted((REPO / "web/static").glob("admin_*.css")):
        m = IMPORT_RE.search(css.read_text(encoding="utf-8"))
        if m and current and m.group(1) != current:
            errors.append(f"{css.relative_to(REPO)} @import admin.css?v={m.group(1)} != assetVersion={current}")

    if errors:
        for err in errors:
            print(f"asset-version: {err}", file=sys.stderr)
        return 1
    print(f"asset-version ok: assetVersion={current}, web/static changes={len(changed)}, base={args.base}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
