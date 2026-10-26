#!/usr/bin/env python3
"""静态资源版本门禁：web/static 变更必须同步 bump httpserver.assetVersion。

为什么需要：静态资源带 `?v=<assetVersion>` 且 `immutable` 1 年；只改文件不改版本，
浏览器/边缘会继续用旧缓存。批次10 曾出现 `app.render.js` 已更新但版本仍 12 的窗口。

检查范围（相对 base，base 默认 origin/main，缺失时 HEAD^）：
1. 逐提交检查 base..HEAD：任一提交改了 `web/static/**` 却未相对父提交 bump
   `assetVersion` → 失败（覆盖「A 静态不 bump + B docs/test」）。
2. 工作区未提交改动（`git status --porcelain`，含 untracked）：有静态变更但
   `assetVersion` 与 HEAD 相同 → 失败。
3. 聚合兜底：base 与工作区版本相同且范围内有静态变更 → 失败。
4. `web/static/admin_*.css` 的 `admin.css?v=N` 必须等于当前 assetVersion。

CI 用 fetch-depth: 0，并把 push before / PR base 作为 `--base`；本地默认 origin/main。
测试模式：`--repo <临时仓库>`。不需要 Node。
"""
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

DEFAULT_REPO = Path(__file__).resolve().parents[1]
TEMPLATES = "internal/httpserver/templates.go"
VERSION_RE = re.compile(r'const\s+assetVersion\s*=\s*"([0-9]+)"')
IMPORT_RE = re.compile(r'/static/admin\.css\?v=([0-9]+)')
ZERO_SHA = "0" * 40


def git(repo: Path, *args: str) -> str:
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True, capture_output=True, text=True,
    ).stdout


def resolve_base(repo: Path) -> str:
    for cand in ("origin/main", "HEAD^"):
        try:
            git(repo, "rev-parse", "--verify", "--quiet", f"{cand}^{{commit}}")
            return cand
        except subprocess.CalledProcessError:
            continue
    return ""


def read_version(text: str | None) -> str | None:
    if text is None:
        return None
    m = VERSION_RE.search(text)
    return m.group(1) if m else None


def show_file(repo: Path, rev: str, path: str) -> str | None:
    try:
        return git(repo, "show", f"{rev}:{path}")
    except subprocess.CalledProcessError:
        return None


def committed_changes(repo: Path, base: str) -> list[str]:
    if not base:
        out = git(repo, "ls-tree", "-r", "--name-only", "HEAD")
    else:
        out = git(repo, "diff", "--name-only", f"{base}...HEAD")
    return [p for p in out.splitlines() if p]


def range_commits(repo: Path, base: str) -> list[str]:
    if not base:
        out = git(repo, "rev-list", "--reverse", "HEAD")
    else:
        out = git(repo, "rev-list", "--reverse", f"{base}..HEAD")
    return [c for c in out.splitlines() if c]


def commit_static_without_bump(repo: Path, base: str) -> list[str]:
    """返回改了 web/static/** 但未相对父提交 bump assetVersion 的提交。"""
    bad: list[str] = []
    for commit in range_commits(repo, base):
        files = git(repo, "diff-tree", "--no-commit-id", "--name-only", "-r",
                    commit).splitlines()
        if not any(f.startswith("web/static/") for f in files):
            continue
        cur = read_version(show_file(repo, commit, TEMPLATES))
        try:
            parent = git(repo, "rev-parse", "--verify", "--quiet", f"{commit}^").strip()
        except subprocess.CalledProcessError:
            parent = ""
        prev = read_version(show_file(repo, parent, TEMPLATES)) if parent else None
        if cur == prev:
            bad.append(commit)
    return bad


def worktree_changes(repo: Path) -> list[str]:
    out = git(repo, "status", "--porcelain", "--untracked-files=all")
    paths: list[str] = []
    for line in out.splitlines():
        if len(line) < 4:
            continue
        path = line[3:]
        if " -> " in path:  # rename/copy
            path = path.split(" -> ", 1)[1]
        paths.append(path.strip('"'))
    return paths


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default=os.environ.get("ASSET_VERSION_BASE", ""),
                    help="git base revision（默认 origin/main，缺失时 HEAD^）")
    ap.add_argument("--repo", default=str(DEFAULT_REPO), help="仓库路径（测试用）")
    args = ap.parse_args(argv)

    repo = Path(args.repo).resolve()
    try:
        base = args.base or resolve_base(repo)
        if not base or base == ZERO_SHA:
            print("asset-version: 无法确定 base（origin/main/HEAD^ 均不可用）", file=sys.stderr)
            return 1
        try:
            git(repo, "rev-parse", "--verify", "--quiet", f"{base}^{{commit}}")
        except subprocess.CalledProcessError:
            print(f"asset-version: base {base!r} 不是有效提交", file=sys.stderr)
            return 1

        committed = committed_changes(repo, base)
        worktree = worktree_changes(repo)
        static_changed = sorted({
            p for p in committed + worktree if p.startswith("web/static/")
        })
        bad_commits = commit_static_without_bump(repo, base)

        current = read_version((repo / TEMPLATES).read_text(encoding="utf-8"))
        base_version = read_version(show_file(repo, base, TEMPLATES))
        head_version = read_version(show_file(repo, "HEAD", TEMPLATES))
    except (OSError, subprocess.CalledProcessError) as exc:
        print(f"asset-version: 无法读取仓库/base {args.base!r}: {exc}", file=sys.stderr)
        return 1

    errors: list[str] = []
    if not current:
        errors.append(f"{TEMPLATES} 缺少 assetVersion 常量")
    for commit in bad_commits:
        errors.append(
            f"提交 {commit[:7]} 改了 web/static/** 但未 bump assetVersion；"
            "必须在同一提交 bump internal/httpserver/templates.go 并同步 admin CSS @import"
        )
    worktree_static = [p for p in worktree if p.startswith("web/static/")]
    if worktree_static and head_version == current:
        preview = ", ".join(worktree_static[:5])
        more = "…" if len(worktree_static) > 5 else ""
        errors.append(
            f"工作区有 {len(worktree_static)} 个静态变更（{preview}{more}），"
            f"但 assetVersion 仍为 {current}（= HEAD）；必须 bump 后再提交"
        )
    if static_changed and base_version == current:
        preview = ", ".join(static_changed[:5])
        more = "…" if len(static_changed) > 5 else ""
        errors.append(
            f"web/static/** 有 {len(static_changed)} 个变更（{preview}{more}），"
            f"但 assetVersion 仍为 {current}（base={base}）；"
            "必须 bump internal/httpserver/templates.go 并同步 admin CSS @import"
        )
    for css in sorted((repo / "web/static").glob("admin_*.css")):
        m = IMPORT_RE.search(css.read_text(encoding="utf-8"))
        if m and current and m.group(1) != current:
            errors.append(
                f"{css.relative_to(repo)} @import admin.css?v={m.group(1)} != assetVersion={current}"
            )

    if errors:
        for err in errors:
            print(f"asset-version: {err}", file=sys.stderr)
        return 1
    print(
        f"asset-version ok: assetVersion={current} (base {base_version}), "
        f"static changes={len(static_changed)}, base={base}, "
        f"committed={len(committed)}, worktree={len(worktree)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
