#!/usr/bin/env python3
"""tools/check_asset_version.py 的门禁自测。

覆盖 Reviewer 要求的三形态 + 一个正向对照：
1. 工作区改静态但不 bump → 红。
2. 提交 A 改静态不 bump + 提交 B 仅 docs/test → 红（多提交范围）。
3. 仅 docs 提交 → 绿。
4. 静态变更 + bump（含 admin CSS 同步）+ docs 提交 → 绿。

用临时 git 仓库，不依赖 Node / 网络。
"""
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CHECKER = Path(__file__).with_name("check_asset_version.py")
VERSION = "14"


class AssetVersionGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="yxq-asset-gate-"))
        self.repo = self.tmp / "repo"
        self.repo.mkdir()
        files = {
            "internal/httpserver/templates.go": (
                "package httpserver\n\n"
                f'const assetVersion = "{VERSION}"\n'
            ),
            "web/static/legacy/app.js": "console.log('v1');\n",
            "web/static/admin_points.css": (
                f'@import url("/static/admin.css?v={VERSION}");\n'
            ),
        }
        for rel, body in files.items():
            path = self.repo / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body, encoding="utf-8")
        self.git("init", "-q", "-b", "main")
        self.git("add", "-A")
        self.git("commit", "-qm", "base")
        self.base = self.rev("HEAD")
        self.git("update-ref", "refs/remotes/origin/main", self.base)

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def git(self, *args: str) -> None:
        subprocess.run(
            ["git", "-C", str(self.repo), "-c", "user.name=test",
             "-c", "user.email=test@example.com", *args],
            check=True, capture_output=True, text=True,
        )

    def rev(self, ref: str) -> str:
        return subprocess.run(
            ["git", "-C", str(self.repo), "rev-parse", ref],
            check=True, capture_output=True, text=True,
        ).stdout.strip()

    def write(self, rel: str, body: str) -> None:
        path = self.repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body, encoding="utf-8")

    def run_gate(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(CHECKER), "--repo", str(self.repo),
             "--base", "origin/main"],
            capture_output=True, text=True,
        )

    def test_worktree_static_without_bump_is_red(self) -> None:
        self.write("web/static/legacy/app.js", "console.log('v2');\n")
        proc = self.run_gate()
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("web/static/** 有 1 个变更", proc.stderr)

    def test_static_commit_then_docs_commit_without_bump_is_red(self) -> None:
        self.write("web/static/legacy/app.js", "console.log('v2');\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "静态改动未 bump")
        self.write("docs/STATUS.md", "# docs\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "仅文档")
        proc = self.run_gate()
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("assetVersion 仍为", proc.stderr)

    def test_per_commit_static_without_bump_when_base_differs(self) -> None:
        # base=14，A 改静态不 bump，B 才 bump 到 15：聚合同意（base≠current），
        # 只有逐提交检查能发现 A，防止「后一个提交补 bump」掩盖前一个提交。
        self.write("web/static/legacy/app.js", "console.log('v2');\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "A 静态改动未 bump")
        self.write("internal/httpserver/templates.go",
                   'package httpserver\n\nconst assetVersion = "15"\n')
        self.write("web/static/admin_points.css",
                   '@import url("/static/admin.css?v=15");\n')
        self.git("add", "-A")
        self.git("commit", "-qm", "B 补 bump")
        self.write("docs/STATUS.md", "# docs\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "docs")
        proc = self.run_gate()
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("未 bump assetVersion", proc.stderr)

    def test_docs_only_is_green(self) -> None:
        self.write("docs/STATUS.md", "# docs\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "仅文档")
        proc = self.run_gate()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertIn("asset-version ok", proc.stdout)

    def test_static_commit_with_bump_is_green(self) -> None:
        self.write("web/static/legacy/app.js", "console.log('v2');\n")
        self.write("internal/httpserver/templates.go",
                   'package httpserver\n\nconst assetVersion = "15"\n')
        self.write("web/static/admin_points.css",
                   '@import url("/static/admin.css?v=15");\n')
        self.git("add", "-A")
        self.git("commit", "-qm", "静态改动并 bump")
        self.write("docs/STATUS.md", "# docs\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "仅文档")
        proc = self.run_gate()
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertIn("assetVersion=15", proc.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
