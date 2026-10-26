#!/usr/bin/env python3
"""tools/spa_minify/check.py 的单元测试（`make test-migration` 会跑到）。

fixture 在临时目录里搭一份最小的 web/static/legacy，覆盖三种情境：
1. 一致 → 通过；
2. 改了 app.js 没重建（manifest.source 过期）→ 红；
3. 壳回退成直接加载 app.js → 红；
另加一条：手改 app.min.js（manifest.min 过期）→ 红。
"""

from __future__ import annotations

import hashlib
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

_CHECK_PATH = Path(__file__).resolve().parent / "spa_minify" / "check.py"
_spec = importlib.util.spec_from_file_location("spa_minify_check", _CHECK_PATH)
check = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = check
_spec.loader.exec_module(check)  # noqa: E402  tools/spa_minify/check.py

SHELL_OK = """<!doctype html>
<html><head>
<link rel="preload" as="script" href="app.min.js?v=__ASSET_URL_VERSION__">
</head><body>
<script>
var ASSETS = [
  'lib/vue.global.prod.js?v=__ASSET_URL_VERSION__',
  'app.render.js?v=__ASSET_URL_VERSION__',
  'app.min.js?v=__ASSET_URL_VERSION__'
];
</script>
</body></html>
"""

SHELL_FALLBACK = SHELL_OK.replace("'app.min.js?v=", "'app.js?v=")
SHELL_PRELOAD_ONLY = """<!doctype html>
<html><head>
<link rel="preload" as="script" href="app.min.js?v=__ASSET_URL_VERSION__">
</head><body></body></html>
"""
SHELL_STATIC_APP_JS = SHELL_OK + (
    '<script src="app.js?v=__ASSET_URL_VERSION__" defer></script>\n'
)


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class SpaMinifyGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.legacy = self.root / "web" / "static" / "legacy"
        self.legacy.mkdir(parents=True)
        self.source = b"var a = 1;\n"
        self.minified = b"var a=1;\n"
        (self.legacy / "app.js").write_bytes(self.source)
        (self.legacy / "app.min.js").write_bytes(self.minified)
        (self.legacy / "index.html").write_text(SHELL_OK, encoding="utf-8")
        self.write_manifest(self.source, self.minified)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def write_manifest(self, source: bytes, minified: bytes) -> None:
        (self.legacy / "app.min.manifest.json").write_text(
            json.dumps(
                {
                    "source": {"path": "app.js", "bytes": len(source), "sha256": digest(source)},
                    "min": {"path": "app.min.js", "bytes": len(minified), "sha256": digest(minified)},
                    "tool": {"name": "esbuild", "version": "test", "target": ["safari14.1"]},
                }
            ),
            encoding="utf-8",
        )

    def test_gate_passes_on_consistent_tree(self) -> None:
        self.assertEqual(check.check(self.root), [])

    def test_gate_fails_when_source_changed_without_rebuild(self) -> None:
        (self.legacy / "app.js").write_bytes(self.source + b"var b = 2;\n")
        errors = check.check(self.root)
        self.assertTrue(any("manifest.source" in e for e in errors), errors)

    def test_gate_fails_when_minified_hand_edited(self) -> None:
        (self.legacy / "app.min.js").write_bytes(self.minified + b"var c=3;\n")
        errors = check.check(self.root)
        self.assertTrue(any("manifest.min" in e for e in errors), errors)

    def test_gate_fails_when_shell_loads_unminified(self) -> None:
        (self.legacy / "index.html").write_text(SHELL_FALLBACK, encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("app.min.js" in e for e in errors), errors)

    def test_gate_fails_when_shell_loads_source_script(self) -> None:
        (self.legacy / "index.html").write_text(SHELL_STATIC_APP_JS, encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("app.js" in e for e in errors), errors)

    def test_gate_fails_on_preload_only_shell(self) -> None:
        (self.legacy / "index.html").write_text(SHELL_PRELOAD_ONLY, encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("ASSETS" in e or "app.min.js" in e for e in errors), errors)

    def test_gate_fails_when_source_deleted(self) -> None:
        (self.legacy / "app.js").unlink()
        errors = check.check(self.root)
        self.assertTrue(any("文件缺失" in e for e in errors), errors)

    def test_cli_reports_failure_exit_code(self) -> None:
        (self.legacy / "app.js").write_bytes(self.source + b"var d = 4;\n")
        self.assertEqual(check.main(["--root", str(self.root)]), 1)
        self.write_manifest((self.legacy / "app.js").read_bytes(), self.minified)
        self.assertEqual(check.main(["--root", str(self.root)]), 0)


if __name__ == "__main__":
    unittest.main()
