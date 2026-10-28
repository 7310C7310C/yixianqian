#!/usr/bin/env python3
"""Unit tests for tools/check_docs.py (stdlib only, no network/Go/DB)."""

from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import date
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_docs  # noqa: E402
import docs_common  # noqa: E402
import gen_docs  # noqa: E402

NOW = date(2026, 10, 23)


def meta(
    owner: str = "maintainer",
    status: str = "current",
    last_verified: str = "2026-10-23",
    sources: list[str] | None = None,
    superseded_by: str | None = None,
) -> str:
    lines = [
        "<!-- doc-meta",
        f"owner: {owner}",
        f"status: {status}",
    ]
    if last_verified is not None:
        lines.append(f"last_verified: {last_verified}")
    if sources is None:
        lines.append("sources: []")
    else:
        lines.append("sources:")
        for source in sources:
            lines.append(f"  - {source}")
    if superseded_by is not None:
        lines.append(f"superseded_by: {superseded_by}")
    lines.append("-->")
    return "\n".join(lines)


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
        "README.md",
        "# Demo\n\n"
        + meta()
        + "\n\nGo 1.24\n\n## 结构\n\n```text\n"
        "cmd/demo/\ninternal/config/\ninternal/httpserver/\n```\n\n"
        "运行 `make docs-check`。\n",
    )
    agent_paths = [
        "README.md",
        "CLAUDE.md",
        "docs/PRODUCT.md",
        "docs/STATUS.md",
        "docs/DECISIONS.md",
        "docs/runbooks/staging.md",
        "docs/reference/REFERENCE.md",
        "tools/README.md",
    ]
    write(
        root,
        "AGENTS.md",
        "# Agents\n\n"
        + meta()
        + "\n\n"
        + "\n".join(f"- `{path}`" for path in agent_paths)
        + "\n\n`cmd/demo`\n",
    )
    write(
        root,
        "docs/PRODUCT.md",
        "# Product\n\n" + meta(owner="pm") + "\n\nGo 1.24\n",
    )
    write(root, "docs/STATUS.md", "# Status\n\n" + meta() + "\n")
    write(root, "docs/DECISIONS.md", "# Decisions\n\n" + meta() + "\n")
    write(root, "docs/runbooks/staging.md", "# Staging\n\n" + meta() + "\n")
    write(
        root,
        "docs/reference/REFERENCE.md",
        "# Reference\n\ncmd/demo\ninternal/config\ninternal/httpserver\n",
    )
    write(root, "tools/README.md", "# Tools\n\n" + meta() + "\n")
    write(
        root,
        "tools/docs_budget.json",
        '{\n'
        '  "README.md": 500,\n'
        '  "AGENTS.md": 500,\n'
        '  "docs/PRODUCT.md": 500,\n'
        '  "docs/STATUS.md": 500,\n'
        '  "docs/DECISIONS.md": 500,\n'
        '  "docs/runbooks/staging.md": 500,\n'
        '  "tools/README.md": 500\n'
        '}\n',
    )
    write(
        root,
        "cmd/demo/main.go",
        'package main\n\nimport "flag"\n\nfunc main() {\n\tname := flag.String("name", "demo", "name")\n\tflag.Parse()\n\t_ = name\n}\n',
    )
    write(
        root,
        "internal/httpserver/routes.go",
        'package httpserver\n\nfunc routes(mux *Mux) { mux.HandleFunc("GET /x", h) }\n',
    )
    write(
        root,
        "internal/config/config.go",
        'package config\n\nfunc Load() {\n\tget("APP_ENV", "development")\n}\n',
    )
    write(root, ".env.example", "APP_ENV=development\n")
    write(root, "migrations/0001_init.sql", "-- init schema\nCREATE TABLE t (id int);\n")
    write(
        root,
        "Makefile",
        """.PHONY: ci test docs-gen docs-check docs-check-stale

ci: test docs-check
\t@echo ok

test:
\tgo test ./...

docs-gen:
\tpython3 tools/gen_docs.py

docs-check:
\tpython3 tools/check_docs.py --strict

docs-check-stale:
\tpython3 tools/check_docs.py --strict --base=origin/main
""",
    )
    gen_docs.write_outputs(root, gen_docs.generate(root))


def codes(findings: list[docs_common.Finding]) -> set[str]:
    return {finding.code for finding in findings}


class DocsCheckTests(unittest.TestCase):
    maxDiff = None

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        build_fixture(self.root)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    # -- full check -------------------------------------------------------

    def test_full_check_passes(self):
        findings = check_docs.run_checks(self.root, now=NOW)
        errors = [finding for finding in findings if finding.severity == "error"]
        self.assertEqual(errors, [], check_docs.format_text(errors))

    def test_every_error_has_fix(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n[x](missing.md)\n\n`cmd/gone`\n\n`make no-such-target`\n")
        findings = check_docs.run_checks(self.root, now=NOW)
        problems = [finding for finding in findings if finding.severity in {"error", "warning"}]
        self.assertTrue(problems)
        for finding in problems:
            self.assertTrue(finding.fix, finding)
            self.assertTrue(
                any(marker in finding.fix for marker in ("make ", "docs/", "修正", "删除", "补齐", "改走", "统一")),
                finding,
            )

    # -- doc-meta ---------------------------------------------------------

    def test_doc_meta_missing(self):
        write(self.root, "docs/no-meta.md", "# No meta\n")
        findings, _ = check_docs.check_meta(self.root, ["docs/no-meta.md"], NOW)
        self.assertIn("E-META-001", codes(findings))

    def test_owner_enum(self):
        write(
            self.root,
            "docs/legacy.md",
            "# Legacy\n\n" + meta(owner="dev-lead") + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/legacy.md"], NOW)
        self.assertIn("E-META-002", codes(findings))
        for owner in ("pm", "maintainer"):
            write(
                self.root,
                "docs/legacy.md",
                "# Legacy\n\n" + meta(owner=owner) + "\n",
            )
            findings, _ = check_docs.check_meta(self.root, ["docs/legacy.md"], NOW)
            self.assertNotIn("E-META-002", codes(findings), owner)

    def test_obsolete_generated_files(self):
        write(self.root, "GEMINI.md", "# old\n")
        write(self.root, ".cursor/rules/yixianqian.mdc", "old\n")
        findings = check_docs.check_obsolete_generated(self.root)
        self.assertEqual(
            {finding.file for finding in findings},
            {"GEMINI.md", ".cursor/rules/yixianqian.mdc"},
        )
        self.assertIn("E-META-004", codes(findings))

    def test_doc_meta_duplicate_block(self):
        text = "# Duplicate\n\n" + meta() + "\n\n" + meta() + "\n"
        write(self.root, "docs/dup.md", text)
        findings, _ = check_docs.check_meta(self.root, ["docs/dup.md"], NOW)
        self.assertIn("E-META-001", codes(findings))

    def test_doc_meta_example_in_fence_ignored(self):
        text = (
            "# Doc\n\n"
            + meta()
            + "\n\n```md\n<!-- doc-meta\nowner: lead\nstatus: current\n"
            "last_verified: 2026-10-23\nsources: []\n-->\n```\n"
        )
        write(self.root, "docs/fenced.md", text)
        findings, _ = check_docs.check_meta(self.root, ["docs/fenced.md"], NOW)
        self.assertEqual(findings, [])

    def test_doc_meta_empty_sources_list(self):
        write(
            self.root,
            "docs/empty-sources.md",
            "# Empty\n\n<!-- doc-meta\nowner: maintainer\nstatus: current\n"
            "last_verified: 2026-10-23\nsources:\n-->\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/empty-sources.md"], NOW)
        self.assertIn("E-META-002", codes(findings))

    def test_doc_meta_future_date(self):
        write(
            self.root,
            "docs/future.md",
            "# Future\n\n" + meta(last_verified="2026-12-31") + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/future.md"], NOW)
        self.assertIn("E-META-002", codes(findings))

    def test_historical_requires_banner(self):
        write(
            self.root,
            "docs/hist.md",
            "# Hist\n\n" + meta(status="historical") + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/hist.md"], NOW)
        self.assertIn("E-META-005", codes(findings))

    def test_superseded_requires_target(self):
        write(
            self.root,
            "docs/old.md",
            "# Old\n\n" + meta(status="superseded") + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/old.md"], NOW)
        self.assertIn("E-META-006", codes(findings))
        write(
            self.root,
            "docs/new.md",
            "# New\n\n" + meta(status="current") + "\n",
        )
        write(
            self.root,
            "docs/old.md",
            "# Old\n\n" + meta(status="superseded", superseded_by="docs/new.md") + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/old.md"], NOW)
        self.assertNotIn("E-META-006", codes(findings))

    def test_dead_source_mapping(self):
        write(
            self.root,
            "docs/dead.md",
            "# Dead\n\n" + meta(sources=["internal/nope/**"]) + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/dead.md"], NOW)
        self.assertIn("E-META-003", codes(findings))

    def test_generated_must_be_registered(self):
        write(
            self.root,
            "docs/fake-generated.md",
            "# Fake\n\n" + meta(status="generated", last_verified=None) + "\n",
        )
        findings, _ = check_docs.check_meta(self.root, ["docs/fake-generated.md"], NOW)
        self.assertIn("E-META-004", codes(findings))

    # -- index / links ----------------------------------------------------

    def test_agents_missing_document(self):
        write(self.root, "docs/probe.md", "# Probe\n\n" + meta() + "\n")
        findings = check_docs.check_agents_docs(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-001", codes(findings))
        self.assertTrue(any("docs/probe.md" in finding.message for finding in findings))

    def test_agents_missing_reference(self):
        with (self.root / "AGENTS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n`docs/nope.md`\n")
        findings = check_docs.check_agents_docs(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-002", codes(findings))
        self.assertTrue(any("docs/nope.md" in finding.message for finding in findings))

    def test_agents_missing_root_reference(self):
        with (self.root / "AGENTS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n`MISSING.md`\n")
        findings = check_docs.check_agents_docs(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-002", codes(findings))

    def test_agents_link_missing_document(self):
        with (self.root / "AGENTS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n[x](docs/nope.md)\n")
        findings = check_docs.check_agents_docs(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-002", codes(findings))

    def test_broken_link_detected(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n[x](missing.md)\n")
        findings = check_docs.check_links(self.root, ["README.md"])
        self.assertIn("E-LINK-001", codes(findings))

    def test_link_in_fenced_code_ignored(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n```md\n[x](missing.md)\n```\n")
        findings = check_docs.check_links(self.root, ["README.md"])
        self.assertNotIn("E-LINK-001", codes(findings))

    def test_doc_path_reference_missing(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n`docs/nope.md`\n")
        findings = check_docs.check_refs(self.root, ["README.md"])
        self.assertIn("E-REF-001", codes(findings))

    # -- governance / structure ------------------------------------------

    def test_go_version_drift(self):
        charter = self.root / "docs/PRODUCT.md"
        charter.write_text(
            charter.read_text(encoding="utf-8").replace("1.24", "1.27"),
            encoding="utf-8",
        )
        findings = check_docs.check_governance(self.root)
        self.assertIn("E-GOVER-001", codes(findings))

    def test_cmd_missing_from_readme(self):
        for rel in ("README.md", "AGENTS.md", "docs/reference/REFERENCE.md"):
            path = self.root / rel
            path.write_text(
                path.read_text(encoding="utf-8").replace("cmd/demo", "demo"), encoding="utf-8"
            )
        findings = check_docs.check_structure(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-CMD-001", codes(findings))

    def test_stale_cmd_mention(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n`cmd/gone`\n")
        findings = check_docs.check_structure(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-CMD-002", codes(findings))

    def test_internal_pkg_missing_from_structure(self):
        path = self.root / "docs/reference/REFERENCE.md"
        path.write_text(
            path.read_text(encoding="utf-8").replace("internal/config", "config"),
            encoding="utf-8",
        )
        findings = check_docs.check_structure(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-STRUCT-001", codes(findings))

    def test_make_target_reference(self):
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n`make nope`\n")
        findings = check_docs.check_structure(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-MAKE-001", codes(findings))

    # -- budgets / current-only headings ---------------------------------

    def test_doc_budget_exceeded(self):
        write(self.root, "tools/docs_budget.json", '{"README.md": 1}\n')
        findings = check_docs.check_doc_budgets(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-BUDGET", codes(findings))

    def test_doc_budget_missing_file(self):
        (self.root / "tools/docs_budget.json").unlink()
        findings = check_docs.check_doc_budgets(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-BUDGET", codes(findings))

    def test_doc_budget_unlisted_doc(self):
        write(
            self.root,
            "tools/docs_budget.json",
            '{"README.md": 500, "AGENTS.md": 500}\n',
        )
        findings = check_docs.check_doc_budgets(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-BUDGET", codes(findings))

    def test_doc_budget_invalid_json(self):
        write(self.root, "tools/docs_budget.json", "{not json\n")
        findings = check_docs.check_doc_budgets(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-BUDGET", codes(findings))

    def test_doc_budget_missing_entry_file(self):
        write(
            self.root,
            "tools/docs_budget.json",
            '{"README.md": 500, "docs/gone.md": 10}\n',
        )
        findings = check_docs.check_doc_budgets(
            self.root, docs_common.governed_docs(self.root)
        )
        self.assertIn("E-DOC-BUDGET", codes(findings))

    def test_current_only_headings_reject_dated_batch(self):
        with (self.root / "docs/STATUS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n## 2026-10-27 用户端报障批次\n")
        findings = check_docs.check_current_only_headings(self.root)
        self.assertIn("E-DOC-HISTORY", codes(findings))

    def test_current_only_headings_reject_date_suffix(self):
        with (self.root / "docs/STATUS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n## 2026-10-27c 扫描防护\n")
        findings = check_docs.check_current_only_headings(self.root)
        self.assertIn("E-DOC-HISTORY", codes(findings))

    def test_current_only_headings_allow_snapshot(self):
        with (self.root / "docs/STATUS.md").open("a", encoding="utf-8") as handle:
            handle.write("\n## 1. 当前快照（2026-10-28 02:36 UTC）\n")
        findings = check_docs.check_current_only_headings(self.root)
        self.assertEqual(findings, [])

    # -- env --------------------------------------------------------------

    def test_env_missing_key(self):
        path = self.root / "internal/config/config.go"
        path.write_text(
            path.read_text(encoding="utf-8").replace(
                '\tget("APP_ENV", "development")',
                '\tget("APP_ENV", "development")\n\tget("PROBE_KEY", "x")',
            ),
            encoding="utf-8",
        )
        findings = check_docs.check_env(self.root)
        self.assertIn("E-ENV-001", codes(findings))
        write(
            self.root,
            "tools/docs_env_whitelist.txt",
            "PROBE_KEY\tcode-only\tprobe only\n",
        )
        findings = check_docs.check_env(self.root)
        self.assertNotIn("E-ENV-001", codes(findings))

    def test_env_example_only(self):
        with (self.root / ".env.example").open("a", encoding="utf-8") as handle:
            handle.write("EXTRA_KEY=value\n")
        findings = check_docs.check_env(self.root)
        self.assertIn("E-ENV-002", codes(findings))
        write(
            self.root,
            "tools/docs_env_whitelist.txt",
            "EXTRA_KEY\texample-only\tno Go config\n",
        )
        findings = check_docs.check_env(self.root)
        self.assertNotIn("E-ENV-002", codes(findings))

    def test_env_whitelist_stale(self):
        write(
            self.root,
            "tools/docs_env_whitelist.txt",
            "GHOST\tcode-only\tstale\n",
        )
        findings = check_docs.check_env(self.root)
        self.assertIn("E-ENV-003", codes(findings))

    def test_env_duplicate_is_error(self):
        with (self.root / ".env.example").open("a", encoding="utf-8") as handle:
            handle.write("APP_ENV=development\n")
        findings = check_docs.check_env(self.root)
        self.assertIn("E-ENV-004", codes(findings))

    def test_os_getenv_outside_config(self):
        write(
            self.root,
            "internal/demo/read.go",
            'package demo\n\nimport "os"\n\nvar X = os.Getenv("X")\n',
        )
        findings = check_docs.check_env(self.root)
        self.assertIn("E-ENV-005", codes(findings))

    # -- generated --------------------------------------------------------

    def test_generated_file_drift(self):
        path = self.root / gen_docs.REFERENCE_PATH
        path.write_text(path.read_text(encoding="utf-8") + "\nmanual edit\n", encoding="utf-8")
        findings, _ = check_docs.check_generated(self.root)
        self.assertIn("E-GEN-001", codes(findings))

    # -- stale ------------------------------------------------------------

    def test_stale_match_exact_and_glob(self):
        metas = {
            "docs/exact.md": {"meta": {"status": "current", "sources": ["internal/config/config.go"]}, "line": 5},
            "docs/glob.md": {"meta": {"status": "current", "sources": ["internal/db/**"]}, "line": 5},
        }
        findings = check_docs.compute_stale_findings(
            metas, {"internal/config/config.go", "internal/db/x/y.go"}
        )
        stale = {finding.file for finding in findings if finding.code == "E-STALE-001"}
        self.assertEqual(stale, {"docs/exact.md", "docs/glob.md"})

    def test_stale_star_does_not_cross_slash(self):
        metas = {
            "docs/cmd.md": {"meta": {"status": "current", "sources": ["cmd/*/main.go"]}, "line": 5}
        }
        findings = check_docs.compute_stale_findings(
            metas, {"cmd/a/main.go", "cmd/a/sub/main.go"}
        )
        stale = [finding for finding in findings if finding.code == "E-STALE-001"]
        self.assertEqual(len(stale), 1)

    def test_stale_doc_changed(self):
        metas = {
            "docs/run.md": {"meta": {"status": "current", "sources": ["internal/config/config.go"]}, "line": 5}
        }
        findings = check_docs.compute_stale_findings(
            metas, {"internal/config/config.go", "docs/run.md"}
        )
        self.assertFalse(
            [finding for finding in findings if finding.code in {"E-STALE-001", "W-STALE-001"}]
        )

    def test_stale_exemptions(self):
        metas = {
            "docs/hist.md": {"meta": {"status": "historical", "sources": ["internal/**"]}, "line": 5},
            "docs/gen.md": {"meta": {"status": "generated", "sources": ["internal/**"]}, "line": 5},
        }
        findings = check_docs.compute_stale_findings(metas, {"internal/config/config.go"})
        self.assertFalse(
            [finding for finding in findings if finding.code in {"E-STALE-001", "W-STALE-001"}]
        )

    def test_stale_draft_is_warning_then_strict(self):
        metas = {
            "docs/draft.md": {"meta": {"status": "draft", "sources": ["internal/config/config.go"]}, "line": 5}
        }
        findings = check_docs.compute_stale_findings(
            metas, {"internal/config/config.go"}
        )
        self.assertEqual(findings[0].code, "W-STALE-001")
        self.assertEqual(findings[0].severity, "warning")

    def test_git_range_integration(self):
        if not shutil.which("git"):
            self.skipTest("git not installed")
        runbook = (
            "# Runbook\n\n"
            + meta(sources=["internal/config/config.go"])
            + "\n\n内容。\n"
        )
        write(self.root, "docs/runbook.md", runbook)
        agents = self.root / "AGENTS.md"
        agents.write_text(
            agents.read_text(encoding="utf-8") + "\n- `docs/runbook.md`\n",
            encoding="utf-8",
        )
        gen_docs.write_outputs(self.root, gen_docs.generate(self.root))

        git = ["git"]
        subprocess.run(git + ["init", "-q"], cwd=self.root, check=True)
        subprocess.run(git + ["add", "-A"], cwd=self.root, check=True)
        subprocess.run(
            git
            + [
                "-c",
                "user.name=Test",
                "-c",
                "user.email=test@example.com",
                "-c",
                "commit.gpgsign=false",
                "commit",
                "-qm",
                "fixture",
            ],
            cwd=self.root,
            check=True,
        )

        config = self.root / "internal/config/config.go"
        config.write_text(
            config.read_text(encoding="utf-8") + "\n// changed\n", encoding="utf-8"
        )
        findings = check_docs.run_checks(self.root, now=NOW, base="HEAD")
        stale = [finding for finding in findings if finding.code == "E-STALE-001"]
        self.assertEqual([finding.file for finding in stale], ["docs/runbook.md"])

        with (self.root / "docs/runbook.md").open("a", encoding="utf-8") as handle:
            handle.write("\n复核完成。\n")
        findings = check_docs.run_checks(self.root, now=NOW, base="HEAD")
        stale = [finding for finding in findings if finding.code == "E-STALE-001"]
        self.assertEqual(stale, [])

    def test_ephemeral_untracked_filter(self):
        for path in (
            ".team/probe.go",
            "__pycache__/x.pyc",
            "internal/config/debug.tmp",
            "notes.md~",
            ".DS_Store",
        ):
            self.assertTrue(check_docs._is_ephemeral_untracked(path), path)
        self.assertFalse(check_docs._is_ephemeral_untracked("internal/config/probe.go"))

    def test_untracked_source_triggers_stale(self):
        if not shutil.which("git"):
            self.skipTest("git not installed")
        write(
            self.root,
            "docs/runbooks/runbook.md",
            "# Runbook\n\n"
            + meta(sources=["internal/config/**"])
            + "\n\n内容。\n",
        )
        agents = self.root / "AGENTS.md"
        agents.write_text(
            agents.read_text(encoding="utf-8") + "\n- `docs/runbooks/runbook.md`\n",
            encoding="utf-8",
        )
        write(self.root, ".gitignore", ".team/\n*.tmp\n")
        gen_docs.write_outputs(self.root, gen_docs.generate(self.root))

        git = ["git"]
        subprocess.run(git + ["init", "-q"], cwd=self.root, check=True)
        subprocess.run(git + ["add", "-A"], cwd=self.root, check=True)
        subprocess.run(
            git
            + [
                "-c",
                "user.name=Test",
                "-c",
                "user.email=test@example.com",
                "-c",
                "commit.gpgsign=false",
                "commit",
                "-qm",
                "fixture",
            ],
            cwd=self.root,
            check=True,
        )

        write(self.root, "internal/config/probe.go", "package config\n")
        write(self.root, ".team/probe.go", "package team\n")
        write(self.root, "internal/config/debug.tmp", "scratch\n")

        changed = check_docs.git_changed_paths(self.root, "HEAD")
        self.assertIn("internal/config/probe.go", changed)
        self.assertNotIn(".team/probe.go", changed)
        self.assertNotIn("internal/config/debug.tmp", changed)

        findings = check_docs.run_checks(self.root, now=NOW, base="HEAD")
        stale = [
            finding
            for finding in findings
            if finding.code == "E-STALE-001"
            and finding.file == "docs/runbooks/runbook.md"
        ]
        self.assertTrue(stale)

        # Reviewer scenario: an untracked migration must also be visible.
        write(self.root, "migrations/0002_probe.sql", "-- probe\n")
        changed = check_docs.git_changed_paths(self.root, "HEAD")
        self.assertIn("migrations/0002_probe.sql", changed)
        metas = {
            "docs/runbooks/runbook.md": {
                "meta": {"status": "current", "sources": ["migrations/**"]},
                "line": 1,
            }
        }
        stale = [
            finding
            for finding in check_docs.compute_stale_findings(metas, changed)
            if finding.code == "E-STALE-001"
        ]
        self.assertEqual([finding.file for finding in stale], ["docs/runbooks/runbook.md"])

    # -- age / output / exit codes ---------------------------------------

    def test_age_is_info_only_even_strict(self):
        path = self.root / "README.md"
        path.write_text(
            path.read_text(encoding="utf-8").replace(
                "last_verified: 2026-10-23", "last_verified: 2025-01-01"
            ),
            encoding="utf-8",
        )
        findings = check_docs.run_checks(self.root, now=NOW, strict=True)
        self.assertIn("I-AGE-001", codes(findings))
        self.assertFalse(
            any(finding.severity == "error" for finding in findings),
            check_docs.format_text(findings),
        )

    def test_check_does_not_write(self):
        before = {
            rel: (self.root / rel).read_bytes()
            for rel in docs_common.repo_files(self.root)
        }
        check_docs.run_checks(self.root, now=NOW)
        after = {
            rel: (self.root / rel).read_bytes()
            for rel in docs_common.repo_files(self.root)
        }
        self.assertEqual(before, after)

    def test_github_format(self):
        write(self.root, "docs/no-meta.md", "# No meta\n")
        findings = check_docs.run_checks(self.root, now=NOW)
        output = check_docs.format_github(findings)
        self.assertIn("::error file=docs/no-meta.md", output)
        self.assertIn("修复:", output)

    def test_exit_codes(self):
        script = str(Path(__file__).resolve().parent / "check_docs.py")

        def run(*args: str):
            return subprocess.run(
                [sys.executable, script, "--root", str(self.root), "--now", "2026-10-23", *args],
                capture_output=True,
                text=True,
                check=False,
            )

        self.assertEqual(run().returncode, 0)
        with (self.root / "README.md").open("a", encoding="utf-8") as handle:
            handle.write("\n[x](missing.md)\n")
        self.assertEqual(run().returncode, 1)
        bad_root = subprocess.run(
            [sys.executable, script, "--root", str(self.root / "nope")],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(bad_root.returncode, 2)
        self.assertEqual(run("--base", "HEAD").returncode, 2)


if __name__ == "__main__":
    unittest.main()
