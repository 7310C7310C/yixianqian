#!/usr/bin/env python3
"""tools/css_split/check.py 的单元测试（`make test-migration` 会跑到）。"""

from __future__ import annotations

import gzip
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

CHECK = Path(__file__).resolve().parent / "css_split" / "check.py"
_spec = importlib.util.spec_from_file_location("css_split_check", CHECK)
check = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = check
_spec.loader.exec_module(check)

SHELL = """<!doctype html>
<html><head>
<link rel="stylesheet" href="lib/vant.min.css?v=__ASSET_URL_VERSION__" media="print" onload="this.media='all'">
<!--YXQ_CRITICAL_CSS-->
<link rel="stylesheet" href="app.css?v=__ASSET_URL_VERSION__" media="print" onload="this.media='all'">
<noscript><link rel="stylesheet" href="app.css?v=__ASSET_URL_VERSION__"></noscript>
</head><body><div id="app"></div></body></html>
"""

APP_CSS = """:root { --a: 1; }
.back-nav { color: red; }
.cards-page { display: block; }
.swipe-card { border-radius: 16px; }
.msg-card { padding: 8px; }
"""


class CssSplitGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.legacy = self.root / "web" / "static" / "legacy"
        self.legacy.mkdir(parents=True)
        (self.legacy / "app.css").write_text(APP_CSS, encoding="utf-8")
        (self.legacy / "index.html").write_text(SHELL, encoding="utf-8")
        check.build_mod.SOURCE = self.legacy / "app.css"
        check.build_mod.OUTPUT = self.legacy / "app.critical.css"
        text, manifest = check.build_mod.build()
        (self.legacy / "app.critical.css").write_text(text, encoding="utf-8")
        import json

        (self.legacy / "app.critical.manifest.json").write_text(
            json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
        )

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_passes_on_consistent_tree(self) -> None:
        self.assertEqual(check.check(self.root), [])

    def test_fails_when_manifest_is_tampered(self) -> None:
        import json

        path = self.legacy / "app.critical.manifest.json"
        data = json.loads(path.read_text(encoding="utf-8"))
        data["critical"]["sha256"] = "0" * 64
        data["critical"]["bytes"] = 0
        path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("manifest" in e for e in errors), errors)

    def test_fails_when_critical_stale(self) -> None:
        (self.legacy / "app.css").write_text(APP_CSS + ".page { margin: 0; }\n", encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("不一致" in e for e in errors), errors)

    def test_fails_when_critical_hand_edited(self) -> None:
        path = self.legacy / "app.critical.css"
        path.write_text(path.read_text(encoding="utf-8") + ".evil { color: red; }\n", encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(errors)

    def test_fails_when_shell_loads_app_css_blocking(self) -> None:
        html = SHELL.replace(
            '<link rel="stylesheet" href="app.css?v=__ASSET_URL_VERSION__" media="print" onload="this.media=\'all\'">',
            '<link rel="stylesheet" href="app.css?v=__ASSET_URL_VERSION__">',
        )
        (self.legacy / "index.html").write_text(html, encoding="utf-8")
        errors = check.check(self.root)
        self.assertTrue(any("阻塞" in e for e in errors), errors)

    def test_fails_when_critical_over_budget(self) -> None:
        # 生成一个超预算的 critical（不可压缩内容）
        big = ".page { content: '" + "x" * (16 * 1024) + "'; }\n"
        (self.legacy / "app.css").write_text(APP_CSS + big, encoding="utf-8")
        text, manifest = check.build_mod.build()
        (self.legacy / "app.critical.css").write_text(text, encoding="utf-8")
        import json

        (self.legacy / "app.critical.manifest.json").write_text(
            json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
        )
        if len(gzip.compress(text.encode(), 9)) <= check.MAX_GZIP:
            self.skipTest("压缩后仍低于预算，跳过")
        errors = check.check(self.root)
        self.assertTrue(any("预算" in e or "gzip" in e for e in errors), errors)


if __name__ == "__main__":
    unittest.main()
