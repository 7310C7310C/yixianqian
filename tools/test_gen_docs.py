#!/usr/bin/env python3
"""Unit tests for tools/gen_docs.py (no network, Go or database needed)."""

from __future__ import annotations

import contextlib
import io
import re
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import docs_common  # noqa: E402
import gen_docs  # noqa: E402


def write(root: Path, rel: str, content: str) -> None:
    path = root / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def build_fixture(root: Path) -> None:
    write(root, "go.mod", "module example.com/demo\n\ngo 1.24.0\n")
    write(
        root,
        ".github/workflows/ci.yml",
        "name: CI\njobs:\n  build:\n    steps:\n"
        "      - uses: actions/setup-go@v5\n"
        "        with:\n          go-version: '1.24.x'\n",
    )
    write(
        root,
        "cmd/demo/main.go",
        """package main

import "flag"

func main() {
\tname := flag.String("name", "demo", "demo name")
\tcount := flag.Int("count", 2, "count")
\tdry := flag.Bool("dry-run", false, "dry run")
\tttl := flag.Duration("ttl", 0, "ttl")
\tflag.Parse()
\t_ = name
\t_ = count
\t_ = dry
\t_ = ttl
}
""",
    )
    write(
        root,
        "internal/httpserver/routes.go",
        """package httpserver

func routes(mux *Mux) {
\tmux.HandleFunc("GET /x", h)
\tmux.HandleFunc(
\t\t"POST /y",
\t\th,
\t)
\tmux.Handle("/", h)
}
""",
    )
    write(
        root,
        "internal/config/config.go",
        """package config

func Load() {
\tget("APP_ENV", "development")
\tgetenv("SESSION_COOKIE_SECURE")
}
""",
    )
    write(root, ".env.example", "APP_ENV=development\n# R2_SECURE=true\n")
    write(root, "migrations/0001_init.sql", "-- init schema\nCREATE TABLE t (id int);\n")
    write(root, "migrations/0002_more.sql", "-- more schema\nALTER TABLE t ADD COLUMN x int;\n")
    write(
        root,
        "Makefile",
        """.PHONY: ci test docs-gen docs-check docs-check-stale

ci: test docs-check
\t@echo done

test:
\tgo test ./...

docs-gen:
\tpython3 tools/gen_docs.py

docs-check:
\tpython3 tools/check_docs.py --strict

docs-check-stale:
\tpython3 tools/check_docs.py --strict --base=origin/main

VAR := ignored
""",
    )
    write(root, "docs/README.md", "# docs\n")


class GeneratorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        build_fixture(self.root)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_generate_creates_expected_files(self):
        contents = gen_docs.generate(self.root)
        self.assertEqual(set(contents), {gen_docs.REFERENCE_PATH, "CLAUDE.md"})
        gen_docs.write_outputs(self.root, contents)
        for rel in gen_docs.OUTPUTS:
            self.assertTrue((self.root / rel).is_file(), rel)
        reference = contents[gen_docs.REFERENCE_PATH]
        for heading in (
            "## 1. 工具链",
            "## 2. 命令入口与 flags",
            "## 3. HTTP 路由",
            "## 4. 环境变量",
            "## 5. Migrations",
            "## 6. make targets",
            "## 7. 顶层包与目录",
            "## 8. 文档影响映射",
        ):
            self.assertIn(heading, reference)
        self.assertIn("`/`", reference)
        self.assertIn("| `cmd/demo` | `-name` | string | `\"demo\"` | demo name | `cmd/demo/main.go` |", reference)
        claude = contents["CLAUDE.md"]
        self.assertIn("@AGENTS.md", claude)
        self.assertIn("README.md", claude)
        self.assertIn("docs/STATUS.md", claude)
        self.assertIn("docs/reference/REFERENCE.md", claude)

    def test_missing_or_invalid_meta_does_not_break_generation(self):
        write(self.root, "docs/no-meta.md", "# No meta\n")
        write(
            self.root,
            "docs/bad-meta.md",
            "# Bad\n\n<!-- doc-meta\nowner: maintainer\nstatus: current\n"
            "last_verified: 2026-10-23\nsources:\n  - Makefile\nunknown: x\n-->\n",
        )
        contents = gen_docs.generate(self.root)
        self.assertIn(gen_docs.REFERENCE_PATH, contents)

    def test_generate_is_byte_identical_twice(self):
        first = gen_docs.generate(self.root)
        second = gen_docs.generate(self.root)
        self.assertEqual(first, second)

    def test_multiline_route_is_extracted(self):
        routes, issues = gen_docs.scan_routes(
            'func f() {\n\tmux.HandleFunc(\n\t\t"GET /x",\n\t\th,\n\t)\n}\n',
            "routes.go",
        )
        self.assertEqual(issues, [])
        self.assertEqual([(r["method"], r["path"]) for r in routes], [("GET", "/x")])

    def test_methodless_fallback_route(self):
        routes, issues = gen_docs.scan_routes(
            'func f() { mux.HandleFunc("/", h) }\n', "routes.go"
        )
        self.assertEqual(issues, [])
        self.assertEqual([(r["method"], r["path"]) for r in routes], [("ANY", "/")])

    def test_unattributed_route_is_reported(self):
        _, issues = gen_docs.scan_routes(
            'func f() { getMux().HandleFunc("GET /x", h) }\n', "routes.go"
        )
        self.assertEqual([issue.code for issue in issues], ["E-ROUTE-001"])

    def test_handle_call_inside_string_is_ignored(self):
        routes, issues = gen_docs.scan_routes(
            'func f() { _ = "x.HandleFunc("; mux.HandleFunc("GET /x", h) }\n',
            "routes.go",
        )
        self.assertEqual(issues, [])
        self.assertEqual([(r["method"], r["path"]) for r in routes], [("GET", "/x")])

    def test_duplicate_route_across_files(self):
        write(
            self.root,
            "internal/httpserver/other.go",
            'package httpserver\n\nfunc other(m *Mux) { m.HandleFunc("GET /x", h) }\n',
        )
        _, issues = gen_docs.collect_routes(self.root)
        self.assertIn("E-ROUTE-002", [issue.code for issue in issues])

    def test_dynamic_pattern_is_error(self):
        _, issues = gen_docs.scan_routes(
            'func f() { pattern := "GET /x"; mux.HandleFunc(pattern, h) }\n',
            "routes.go",
        )
        self.assertEqual([issue.code for issue in issues], ["E-ROUTE-001"])

    def test_duplicate_route_is_error(self):
        _, issues = gen_docs.scan_routes(
            'func f() {\n\tmux.HandleFunc("GET /x", h)\n\tmux.HandleFunc("GET /x", h)\n}\n',
            "routes.go",
        )
        self.assertEqual([issue.code for issue in issues], ["E-ROUTE-002"])

    def test_flags_types_and_defaults(self):
        rows, issues = gen_docs.scan_flags(
            """func main() {
\tname := flag.String("name", "demo", "demo name")
\tcount := flag.Int("count", 2, "count")
\tdry := flag.Bool("dry-run", false, "dry run")
\tttl := flag.Duration("ttl", 0, "ttl")
\tflag.Parse()
}
""",
            "cmd/demo/main.go",
        )
        self.assertEqual(issues, [])
        self.assertEqual(
            [(row["flag"], row["type"], row["default"]) for row in rows],
            [
                ("name", "string", '"demo"'),
                ("count", "int", "2"),
                ("dry-run", "bool", "false"),
                ("ttl", "duration", "0"),
            ],
        )

    def test_unsupported_flag_form_is_error(self):
        _, issues = gen_docs.scan_flags(
            'func main() { var x string; flag.StringVar(&x, "x", "", "x") }\n',
            "cmd/demo/main.go",
        )
        self.assertEqual([issue.code for issue in issues], ["E-FLAG-001"])

    def test_make_targets_and_deps(self):
        rows = gen_docs.parse_makefile(
            """.PHONY: ci test
ci: test docs-check # comment
\t@echo done
test:
\tgo test ./...
VAR := ignored
docs-check: ; python3 tools/check_docs.py
"""
        )
        targets = {row["target"]: row["deps"] for row in rows}
        self.assertEqual(targets["ci"], ["test", "docs-check"])
        self.assertEqual(targets["test"], [])
        self.assertEqual(targets["docs-check"], [])
        self.assertNotIn("VAR", targets)
        self.assertNotIn(".PHONY", targets)

    def test_env_keys_active_and_commented(self):
        parsed = gen_docs.parse_env_example(
            "APP_ENV=development\n# R2_SECURE=true\n# comment without key\n"
        )
        self.assertEqual(set(parsed["active"]), {"APP_ENV"})
        self.assertEqual(set(parsed["commented"]), {"R2_SECURE"})
        self.assertEqual(parsed["duplicates"], [])
        # Values are never retained.
        self.assertNotIn("development", parsed["active"])

    def test_migrations_sorted_and_titles(self):
        rows, issues = gen_docs.parse_migrations(
            [
                ("0002_more.sql", "-- more schema\n"),
                ("0001_init.sql", "-- init schema\n"),
            ]
        )
        self.assertEqual(issues, [])
        self.assertEqual(
            [(row["number"], row["file"], row["title"]) for row in rows],
            [
                (1, "0001_init.sql", "init schema"),
                (2, "0002_more.sql", "more schema"),
            ],
        )

    def test_migration_gap_is_error(self):
        _, issues = gen_docs.parse_migrations(
            [
                ("0001_init.sql", "-- init schema\n"),
                ("0003_skip.sql", "-- skipped\n"),
            ]
        )
        self.assertEqual([issue.code for issue in issues], ["E-MIG-001"])

    def test_claude_pointer(self):
        text = gen_docs.render_claude_pointer()
        self.assertLessEqual(len(text.splitlines()), 20)
        self.assertIn("@AGENTS.md", text)
        for required in (
            "AGENTS.md",
            "README.md",
            "docs/STATUS.md",
            "docs/reference/REFERENCE.md",
        ):
            self.assertIn(required, text)
        self.assertIn("本文件由 `tools/gen_docs.py` 生成", text)
        # Guard against reintroducing removed entry files.
        self.assertNotIn("docs/INDEX.md", text)
        self.assertNotIn("AI-HANDOFF", text)

    def test_output_has_no_timestamp_or_sha(self):
        contents = gen_docs.generate(self.root)
        for rel, text in contents.items():
            self.assertNotIn(str(self.root), text, rel)
            self.assertIsNone(re.search(r"\b[0-9a-f]{40}\b", text), rel)
            self.assertIsNone(re.search(r"\b20\d{2}-\d{2}-\d{2}\b", text), rel)

    def test_stdout_and_check_cli(self):
        # --stdout does not write; --check reports drift for missing outputs.
        self.assertFalse((self.root / gen_docs.REFERENCE_PATH).exists())
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = gen_docs.main(
                ["--root", str(self.root), "--stdout", gen_docs.REFERENCE_PATH]
            )
        self.assertEqual(rc, 0)
        self.assertIn("# 代码参考", out.getvalue())
        self.assertFalse((self.root / gen_docs.REFERENCE_PATH).exists())
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            rc = gen_docs.main(["--root", str(self.root), "--check"])
        self.assertEqual(rc, 1)

    def test_unknown_stdout_path_is_runtime_error(self):
        with contextlib.redirect_stderr(io.StringIO()):
            rc = gen_docs.main(["--root", str(self.root), "--stdout", "nope.md"])
        self.assertEqual(rc, 2)


class CommonGlobTests(unittest.TestCase):
    def test_match_exact_directory_glob(self):
        self.assertTrue(docs_common.match_path("internal/config/config.go", "internal/config/config.go"))
        self.assertFalse(docs_common.match_path("internal/config/other.go", "internal/config/config.go"))
        self.assertTrue(docs_common.match_path("deploy/nginx/x.conf", "deploy/"))
        self.assertTrue(docs_common.match_path("migrations/0006_x.sql", "migrations/**"))
        self.assertTrue(docs_common.match_path("cmd/server/main.go", "cmd/*/main.go"))
        self.assertFalse(docs_common.match_path("cmd/server/sub/main.go", "cmd/*/main.go"))
        self.assertTrue(docs_common.match_path("internal/db/x/y.go", "internal/db/**"))


if __name__ == "__main__":
    unittest.main()
