#!/usr/bin/env python3
"""Check documentation metadata, AGENTS registry, links, facts and drift.

Read-only: this script never writes repository files.

Usage:
    python3 tools/check_docs.py [--root PATH] [--strict] [--base REF]
                                [--now YYYY-MM-DD] [--max-age-days 180]
                                [--format text|github] [--quiet] [--verbose]
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import docs_common as common  # noqa: E402
import gen_docs  # noqa: E402


class CheckRuntimeError(Exception):
    """Runtime failure that maps to exit code 2 (not a doc finding)."""


FIX_META = (
    "按 doc-meta 规范补齐 owner/status/last_verified/sources"
    "（owner 只允许 pm|maintainer），然后运行 make docs-check"
)
FIX_DOC_001 = "在 AGENTS.md 增加该精确路径（反引号或链接），然后运行 make docs-check"
FIX_DOC_002 = "修正 AGENTS.md 中的路径引用或补上缺失文件，然后运行 make docs-check"
FIX_LINK = "修正链接目标或补上缺失文件，然后运行 make docs-check"
FIX_REF = "修正反引号中的 docs 路径或补上缺失文件，然后运行 make docs-check"
FIX_MAKE = "修正文档命令或在 Makefile 补 target，然后运行 make docs-check"
FIX_GEN = "运行 make docs-gen 刷新生成物"
FIX_ENV = (
    "补齐 config/.env.example 的 key 对称性，或在 tools/docs_env_whitelist.txt "
    "登记带原因的单边白名单"
)
FIX_ROUTE = "把 pattern 改成字符串字面量/删除重复路由，或扩展 tools/gen_docs.py 抽取器并加测试"
FIX_FLAG = "改用受支持的 flag 形态（String/Bool/Int/Int64/Uint/Float64/Duration），或扩展抽取器并加测试"
FIX_MIG = "按 NNNN_name.sql 从 0001 连续编号，只增不改"
FIX_STALE = (
    "更新文档内容与 last_verified（今天），或修正 doc-meta 的 sources；"
    "然后运行 make docs-gen && make docs-check"
)


def _format_location(finding: common.Finding) -> str:
    return finding.file + (f":{finding.line}" if finding.line else "")


def _wrap_issue(issue: common.ExtractionIssue, fix: str) -> common.Finding:
    return common.Finding(
        code=issue.code,
        severity="error",
        file=issue.file,
        line=issue.line,
        message=issue.message,
        fix=fix,
    )


# ---------------------------------------------------------------------------
# A. generated outputs
# ---------------------------------------------------------------------------


def check_generated(root: Path) -> tuple[list[common.Finding], dict[str, str] | None]:
    try:
        contents = gen_docs.generate(root)
    except gen_docs.ExtractionError as exc:
        findings = []
        for issue in exc.issues:
            if issue.code.startswith("E-GEN"):
                findings.append(_wrap_issue(issue, FIX_GEN))
        return findings, None

    findings: list[common.Finding] = []
    for rel in sorted(contents):
        target = root / rel
        if not target.is_file():
            findings.append(
                common.Finding(
                    "E-GEN-001",
                    "error",
                    rel,
                    1,
                    "生成物缺失",
                    FIX_GEN,
                )
            )
            continue
        if target.read_text(encoding="utf-8") != contents[rel]:
            findings.append(
                common.Finding(
                    "E-GEN-001",
                    "error",
                    rel,
                    1,
                    "生成物与重新生成结果不一致（被手改或输入已变化）",
                    FIX_GEN,
                )
            )
    return findings, contents


def check_obsolete_generated(root: Path) -> list[common.Finding]:
    """Previously generated pointers that must be deleted from the workspace."""
    findings: list[common.Finding] = []
    for rel in common.OBSOLETE_GENERATED:
        if (root / rel).exists():
            findings.append(
                common.Finding(
                    "E-META-004",
                    "error",
                    rel,
                    1,
                    "已下线的生成物仍存在（GEMINI/Copilot/Cursor 指针已移除）",
                    "删除该文件；当前只保留 docs/reference/REFERENCE.md 与 CLAUDE.md",
                )
            )
    return findings


# ---------------------------------------------------------------------------
# B. doc-meta
# ---------------------------------------------------------------------------


def check_meta(
    root: Path, docs: list[str], now: date
) -> tuple[list[common.Finding], dict[str, dict]]:
    findings: list[common.Finding] = []
    metas: dict[str, dict] = {}
    files = common.repo_files(root)
    registry = set(gen_docs.OUTPUTS)

    for doc in docs:
        text = (root / doc).read_text(encoding="utf-8")
        result = common.parse_doc_meta(text, doc)
        for issue in result.errors:
            findings.append(_wrap_issue(issue, FIX_META))
        if result.meta is None:
            continue
        meta = result.meta
        metas[doc] = {"meta": meta, "line": result.line}

        owner = meta.get("owner")
        if owner is not None and owner not in common.OWNER_VALUES:
            findings.append(
                common.Finding(
                    "E-META-002",
                    "error",
                    doc,
                    result.line,
                    f"owner 非法：{owner}；只允许 pm|maintainer（旧角色请改为 maintainer）",
                    FIX_META,
                )
            )
        status = meta.get("status")
        if status is not None and status not in common.STATUS_VALUES:
            findings.append(
                common.Finding(
                    "E-META-002",
                    "error",
                    doc,
                    result.line,
                    f"status 非法：{status}",
                    FIX_META,
                )
            )

        last_verified = meta.get("last_verified")
        if status != "generated":
            if not last_verified:
                findings.append(
                    common.Finding(
                        "E-META-002",
                        "error",
                        doc,
                        result.line,
                        "非 generated 文档必须填写 last_verified",
                        FIX_META,
                    )
                )
        if last_verified:
            parsed = common.parse_iso_date(last_verified)
            if parsed is None:
                findings.append(
                    common.Finding(
                        "E-META-002",
                        "error",
                        doc,
                        result.line,
                        f"last_verified 日期格式非法：{last_verified}（需要 YYYY-MM-DD）",
                        FIX_META,
                    )
                )
            elif parsed > now + timedelta(days=1):
                findings.append(
                    common.Finding(
                        "E-META-002",
                        "error",
                        doc,
                        result.line,
                        f"last_verified 晚于 --now+1 天：{last_verified}",
                        FIX_META,
                    )
                )

        sources = meta.get("sources")
        if isinstance(sources, list):
            for source in sources:
                if not common.source_exists(root, source, files):
                    findings.append(
                        common.Finding(
                            "E-META-003",
                            "error",
                            doc,
                            result.line,
                            f"sources 在仓库中无匹配：{source}",
                            "修正 doc-meta 的 sources（删除已不存在的路径或改 glob）；"
                            "文件已删除时同时复核正文，然后运行 make docs-check",
                        )
                    )

        if status == "historical":
            banner_lines = "\n".join(text.splitlines()[:15])
            if "> 历史文件（" not in banner_lines:
                findings.append(
                    common.Finding(
                        "E-META-005",
                        "error",
                        doc,
                        1,
                        "status: historical 缺少前 15 行内的 `> 历史文件（` 横幅",
                        "在文首加 `> 历史文件（日期/原因）` 横幅，或改为 current/draft",
                    )
                )
        if status == "superseded":
            target = meta.get("superseded_by")
            if not target or not (root / target).is_file():
                findings.append(
                    common.Finding(
                        "E-META-006",
                        "error",
                        doc,
                        result.line,
                        "status: superseded 必须提供存在的 superseded_by",
                        "补齐 superseded_by 字段并指向实际文档，然后运行 make docs-check",
                    )
                )
        if status == "generated" and doc not in registry:
            findings.append(
                common.Finding(
                    "E-META-004",
                    "error",
                    doc,
                    result.line,
                    "未注册的 status: generated 文档；请删除该文件或加入生成器注册表",
                    "删除该生成物，或把它加入 tools/gen_docs.py 的 OUTPUTS 并加测试",
                )
            )
        if doc in registry and doc.endswith(".md") and status != "generated":
            findings.append(
                common.Finding(
                    "E-META-004",
                    "error",
                    doc,
                    result.line,
                    "生成器注册表内的文档必须是 status: generated",
                    "由 tools/gen_docs.py 管理该文件，不要手改 doc-meta",
                )
            )

    return findings, metas


# ---------------------------------------------------------------------------
# C. AGENTS registry, links, docs path references
# ---------------------------------------------------------------------------


def _mentions_path(text: str, path: str) -> bool:
    pattern = r"(?<![\w./-])" + re.escape(path) + r"(?![\w./-])"
    return re.search(pattern, text) is not None


def _is_doc_ref(token: str) -> bool:
    if "/" not in token:
        return token.endswith(".md")
    return token.startswith("docs/") and token.endswith(".md")


def _doc_ref_exists(root: Path, token: str, files: list[str]) -> bool:
    if any(ch in token for ch in "*?"):
        return any(common.match_path(rel, token) for rel in files)
    return (root / token).is_file()


def check_agents_docs(root: Path, docs: list[str]) -> list[common.Finding]:
    """E-DOC-001 coverage and E-DOC-002 reference validity in AGENTS.md."""
    findings: list[common.Finding] = []
    agents_path = root / "AGENTS.md"
    if not agents_path.is_file():
        return [
            common.Finding(
                "E-DOC-001",
                "error",
                "AGENTS.md",
                1,
                "缺少 AGENTS.md，无法作为文档入口",
                "创建 AGENTS.md 并登记所有受治理文档",
            )
        ]
    text = agents_path.read_text(encoding="utf-8")

    for doc in docs:
        if doc == "AGENTS.md":
            continue
        if not _mentions_path(text, doc):
            findings.append(
                common.Finding(
                    "E-DOC-001",
                    "error",
                    "AGENTS.md",
                    1,
                    f"受治理文档未在 AGENTS.md 中登记：{doc}",
                    FIX_DOC_001,
                )
            )

    files = common.repo_files(root)
    seen: set[tuple[int, str]] = set()
    for line_no, token in common.extract_inline_code(text):
        token = token.strip().rstrip("，。；：,;:")
        if not token or any(ch in token for ch in "<>") or any(ch.isspace() for ch in token):
            continue
        token = token.split("#", 1)[0].split("?", 1)[0]
        if not _is_doc_ref(token):
            continue
        key = (line_no, token)
        if key in seen:
            continue
        seen.add(key)
        if not _doc_ref_exists(root, token, files):
            findings.append(
                common.Finding(
                    "E-DOC-002",
                    "error",
                    "AGENTS.md",
                    line_no,
                    f"AGENTS.md 引用的文档路径不存在：{token}",
                    FIX_DOC_002,
                )
            )

    for line_no, raw in common.extract_links(text):
        target = common.normalize_link_target(raw)
        if target is None or any(ch in target for ch in "<>"):
            continue
        target = target.split("#", 1)[0].split("?", 1)[0]
        if not _is_doc_ref(target):
            continue
        key = (line_no, target)
        if key in seen:
            continue
        seen.add(key)
        if not _doc_ref_exists(root, target, files):
            findings.append(
                common.Finding(
                    "E-DOC-002",
                    "error",
                    "AGENTS.md",
                    line_no,
                    f"AGENTS.md 链接的文档路径不存在：{target}",
                    FIX_DOC_002,
                )
            )
    return findings


def check_links(root: Path, docs: list[str]) -> list[common.Finding]:
    findings: list[common.Finding] = []
    for doc in docs:
        text = (root / doc).read_text(encoding="utf-8")
        for line_no, raw in common.extract_links(text):
            target = common.normalize_link_target(raw)
            if target is None:
                continue
            if any(ch in target for ch in "<>"):
                continue
            if doc == "AGENTS.md":
                plain = target.split("#", 1)[0].split("?", 1)[0]
                if _is_doc_ref(plain):
                    continue  # E-DOC-002 owns AGENTS.md document references
            if target.startswith("/"):
                resolved = root / target.lstrip("/")
            else:
                resolved = (root / doc).parent / target
            if not resolved.exists():
                findings.append(
                    common.Finding(
                        "E-LINK-001",
                        "error",
                        doc,
                        line_no,
                        f"相对链接/图片目标不存在：{target}",
                        FIX_LINK,
                    )
                )
    return findings


def check_refs(root: Path, docs: list[str]) -> list[common.Finding]:
    findings: list[common.Finding] = []
    files = common.repo_files(root)
    for doc in docs:
        if doc == "AGENTS.md":
            continue  # E-DOC-002 owns AGENTS.md document references
        text = (root / doc).read_text(encoding="utf-8")
        for line_no, token in common.extract_inline_code(text):
            token = token.strip()
            if any(ch in token for ch in "<>") or any(ch.isspace() for ch in token):
                continue
            token = token.split("#", 1)[0].split("?", 1)[0]
            if not token.startswith("docs/") or not token.endswith(".md"):
                continue
            if any(ch in token for ch in "*?"):
                if not any(common.match_path(rel, token) for rel in files):
                    findings.append(
                        common.Finding(
                            "E-REF-001",
                            "error",
                            doc,
                            line_no,
                            f"正文反引号中的 docs glob 无匹配：{token}",
                            FIX_REF,
                        )
                    )
                continue
            if not (root / token).is_file():
                findings.append(
                    common.Finding(
                        "E-REF-001",
                        "error",
                        doc,
                        line_no,
                        f"正文反引号中的文档路径不存在：{token}",
                        FIX_REF,
                    )
                )
    return findings


# ---------------------------------------------------------------------------
# D. governance and structure
# ---------------------------------------------------------------------------


def _major_minor(version: str | None) -> str | None:
    match = re.match(r"(\d+\.\d+)", version or "")
    return match.group(1) if match else None


def check_governance(root: Path) -> list[common.Finding]:
    findings: list[common.Finding] = []
    go_mod = root / "go.mod"
    if not go_mod.is_file():
        return findings
    go_version, _, _ = gen_docs.parse_go_mod(go_mod.read_text(encoding="utf-8"))
    canonical = _major_minor(go_version)
    if not canonical:
        return findings

    def add(file: str, line: int, found: str) -> None:
        findings.append(
            common.Finding(
                "E-GOVER-001",
                "error",
                file,
                line,
                f"Go 版本 {found} 与 go.mod 的 {canonical} 不一致",
                "统一 go.mod/CI/README/PRODUCT 的 Go major.minor；"
                "PRODUCT 变更需 PM 批准后单独提交",
            )
        )

    readme = root / "README.md"
    if readme.is_file():
        text = readme.read_text(encoding="utf-8")
        match = re.search(r"\bGo\s+(\d+\.\d+)", text)
        if not match:
            add("README.md", 1, "（未找到）")
        elif match.group(1) != canonical:
            add("README.md", text.count("\n", 0, match.start()) + 1, match.group(1))

    product = root / "docs" / "PRODUCT.md"
    if product.is_file():
        text = product.read_text(encoding="utf-8")
        match = re.search(r"\bGo\s+(\d+\.\d+)", text)
        if not match:
            add("docs/PRODUCT.md", 1, "（未找到）")
        elif match.group(1) != canonical:
            add(
                "docs/PRODUCT.md",
                text.count("\n", 0, match.start()) + 1,
                match.group(1),
            )

    ci = root / ".github" / "workflows" / "ci.yml"
    if ci.is_file():
        text = ci.read_text(encoding="utf-8")
        versions = gen_docs.parse_ci_go_versions(text)
        if not versions:
            add(".github/workflows/ci.yml", 1, "（未找到 go-version）")
        for version in versions:
            major_minor = _major_minor(version)
            if major_minor and major_minor != canonical:
                line = text.count("\n", 0, text.find(version)) + 1
                add(".github/workflows/ci.yml", line, version)
    return findings


def _mention(text: str, token: str) -> bool:
    pattern = r"(?<![\w.-])" + re.escape(token) + r"(?![\w.-])"
    return re.search(pattern, text) is not None


def _readme_structure_text(text: str) -> str:
    """Prefer README fenced blocks that look like the repository structure."""
    blocks: list[str] = []
    current: list[str] | None = None
    for line in text.splitlines():
        if line.lstrip().startswith(("```", "~~~")):
            if current is None:
                current = []
            else:
                block = "\n".join(current)
                if "cmd/" in block or "internal/" in block:
                    blocks.append(block)
                current = None
            continue
        if current is not None:
            current.append(line)
    return "\n".join(blocks) if blocks else text


def check_structure(root: Path, docs: list[str]) -> list[common.Finding]:
    findings: list[common.Finding] = []
    readme_text = (root / "README.md").read_text(encoding="utf-8") if (root / "README.md").is_file() else ""
    agents_text = (root / "AGENTS.md").read_text(encoding="utf-8") if (root / "AGENTS.md").is_file() else ""
    reference_path = root / gen_docs.REFERENCE_PATH
    reference_text = reference_path.read_text(encoding="utf-8") if reference_path.is_file() else ""
    structure_text = reference_text or _readme_structure_text(readme_text)
    combined = readme_text + "\n" + agents_text + "\n" + reference_text

    cmd_root = root / "cmd"
    if cmd_root.is_dir():
        for entry in sorted(cmd_root.iterdir()):
            if not (entry / "main.go").is_file():
                continue
            token = f"cmd/{entry.name}"
            if not _mention(combined, token):
                findings.append(
                    common.Finding(
                        "E-CMD-001",
                        "error",
                        "README.md",
                        1,
                        f"cmd 入口 {token} 未在 README/AGENTS/REFERENCE 提及",
                        "在 README 快速入口或 AGENTS 结构清单补该入口；机械清单由 make docs-gen 生成，然后运行 make docs-check",
                    )
                )

    for doc in docs:
        text = (root / doc).read_text(encoding="utf-8")
        for match in re.finditer(r"(?<![\w.-])cmd/([A-Za-z0-9_.-]+)", text):
            name = match.group(1)
            if not (cmd_root / name).is_dir():
                findings.append(
                    common.Finding(
                        "E-CMD-002",
                        "error",
                        doc,
                        text.count("\n", 0, match.start()) + 1,
                        f"文档提及不存在的 cmd 入口：cmd/{name}",
                        "删除过时提及或补回入口；然后运行 make docs-check",
                    )
                )

    internal_root = root / "internal"
    if internal_root.is_dir():
        for entry in sorted(internal_root.iterdir()):
            if not entry.is_dir():
                continue
            has_go = any(
                path.suffix == ".go" and not path.name.endswith("_test.go")
                for path in entry.glob("*.go")
            )
            if not has_go:
                continue
            token = f"internal/{entry.name}"
            if not _mention(structure_text, token):
                findings.append(
                    common.Finding(
                        "E-STRUCT-001",
                        "error",
                        "docs/reference/REFERENCE.md",
                        1,
                        f"{token} 未在 README 结构块/REFERENCE 提及",
                        "在 README 结构块补该目录，或运行 make docs-gen 刷新 REFERENCE；然后运行 make docs-check",
                    )
                )

    targets = {
        row["target"] for row in gen_docs.collect_make_targets(root)
    }
    for doc in docs:
        text = (root / doc).read_text(encoding="utf-8")
        # Only treat inline code and command lines as commands; prose such as
        # the heading "make targets" is not a make invocation.
        occurrences: dict[tuple[int, str], None] = {}
        for line_no, token in common.extract_inline_code(text):
            for match in re.finditer(r"\bmake\s+([A-Za-z0-9][\w.-]*)", token):
                occurrences[(line_no, match.group(1))] = None
        for line_no, line in enumerate(text.splitlines(), 1):
            if line.lstrip().startswith("#"):
                continue
            for match in re.finditer(r"\bmake\s+([A-Za-z0-9][\w.-]*)", line):
                occurrences[(line_no, match.group(1))] = None
        for (line_no, target) in sorted(occurrences):
            if target not in targets:
                findings.append(
                    common.Finding(
                        "E-MAKE-001",
                        "error",
                        doc,
                        line_no,
                        f"文档引用了不存在的 make target：{target}",
                        FIX_MAKE,
                    )
                )

    for pointer in common.POINTER_DOCS:
        path = root / pointer
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        line_count = len(text.splitlines())
        if line_count > 20:
            findings.append(
                common.Finding(
                    "E-POINTER-001",
                    "error",
                    pointer,
                    1,
                    f"指针文件超过 20 行（{line_count} 行）",
                    "压缩生成模板后运行 make docs-gen",
                )
            )
        for required in (
            "AGENTS.md",
            "README.md",
            "docs/STATUS.md",
            "docs/reference/REFERENCE.md",
        ):
            if required not in text:
                findings.append(
                    common.Finding(
                        "E-POINTER-001",
                        "error",
                        pointer,
                        1,
                        f"指针文件缺少必需链接：{required}",
                        "修正生成模板后运行 make docs-gen",
                    )
                )
    return findings


# ---------------------------------------------------------------------------
# D2. document budgets and current-only headings
# ---------------------------------------------------------------------------

DOC_BUDGET_PATH = "tools/docs_budget.json"
HISTORY_DOCS = ("docs/STATUS.md", "docs/runbooks/staging.md")
HISTORY_HEADING = re.compile(
    r"^#{1,6}\s+(?:20\d{2}-\d{2}-\d{2}[A-Za-z]?(?![\w])|.*(?:批次|性能批次))"
)


def check_doc_budgets(root: Path, docs: list[str]) -> list[common.Finding]:
    """Fail when a hand-written doc exceeds its line budget.

    Budgets live in tools/docs_budget.json so that raising one is a visible,
    reviewable change instead of a silent append.
    """
    findings: list[common.Finding] = []
    budget_path = root / DOC_BUDGET_PATH
    if not budget_path.is_file():
        return [
            common.Finding(
                "E-DOC-BUDGET",
                "error",
                DOC_BUDGET_PATH,
                1,
                "缺少行数预算文件",
                "创建 tools/docs_budget.json；每个受治理手写文档都要有预算，生成物可豁免",
            )
        ]
    try:
        budgets = json.loads(budget_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        return [
            common.Finding(
                "E-DOC-BUDGET",
                "error",
                DOC_BUDGET_PATH,
                1,
                f"无法读取行数预算：{exc}",
                "修正 tools/docs_budget.json；格式为 {相对路径: 最大行数}",
            )
        ]
    if not isinstance(budgets, dict):
        return [
            common.Finding(
                "E-DOC-BUDGET",
                "error",
                DOC_BUDGET_PATH,
                1,
                "行数预算必须是 JSON object",
                "改为 {相对路径: 最大行数}",
            )
        ]
    generated = {"CLAUDE.md", gen_docs.REFERENCE_PATH}
    for rel in docs:
        if rel in generated:
            continue
        if rel not in budgets:
            findings.append(
                common.Finding(
                    "E-DOC-BUDGET",
                    "error",
                    rel,
                    1,
                    "受治理文档未设置行数预算",
                    "在 tools/docs_budget.json 增加该文件的正整数预算；"
                    "生成物可加入 generated 豁免",
                )
            )
    for rel, limit in sorted(budgets.items()):
        path = root / rel
        if not path.is_file():
            findings.append(
                common.Finding(
                    "E-DOC-BUDGET",
                    "error",
                    DOC_BUDGET_PATH,
                    1,
                    f"预算条目对应的文件不存在：{rel}",
                    "删除该预算条目，或补回文件；然后运行 make docs-check",
                )
            )
            continue
        try:
            lines = len(path.read_text(encoding="utf-8").splitlines())
        except OSError as exc:
            findings.append(
                common.Finding(
                    "E-DOC-BUDGET",
                    "error",
                    rel,
                    1,
                    f"无法读取文档：{exc}",
                    "确认文件存在且可读，然后运行 make docs-check",
                )
            )
            continue
        if not isinstance(limit, int) or limit <= 0:
            findings.append(
                common.Finding(
                    "E-DOC-BUDGET",
                    "error",
                    DOC_BUDGET_PATH,
                    1,
                    f"{rel} 的预算不是正整数：{limit!r}",
                    "改为正整数行数；放宽容必须说明理由并走 Review",
                )
            )
            continue
        if lines > limit:
            findings.append(
                common.Finding(
                    "E-DOC-BUDGET",
                    "error",
                    rel,
                    1,
                    f"文档超过行数预算：{lines}/{limit} 行",
                    "把历史/机械细节移到 git log 或 code，只留非可推导知识；"
                    "确需放宽时修改 tools/docs_budget.json 并说明理由，然后运行 make docs-check",
                )
            )
    return findings


def check_current_only_headings(root: Path) -> list[common.Finding]:
    """STATUS/runbook must not grow dated or batch-history headings."""
    findings: list[common.Finding] = []
    for rel in HISTORY_DOCS:
        path = root / rel
        if not path.is_file():
            continue
        for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if HISTORY_HEADING.match(line.strip()):
                findings.append(
                    common.Finding(
                        "E-DOC-HISTORY",
                        "error",
                        rel,
                        line_no,
                        f"当前状态文档出现历史/批次标题：{line.strip()}",
                        "把该段移到 commit message/.team/git log；STATUS/runbook 只写当前状态与操作步骤",
                    )
                )
    return findings


def check_env(root: Path) -> list[common.Finding]:
    findings: list[common.Finding] = []
    config_path = root / "internal" / "config" / "config.go"
    example_path = root / ".env.example"
    if not config_path.is_file() or not example_path.is_file():
        return findings

    config_keys = gen_docs.scan_config_env(config_path.read_text(encoding="utf-8"))
    example = gen_docs.parse_env_example(example_path.read_text(encoding="utf-8"))
    active = example["active"]
    commented = example["commented"]
    example_keys = set(active) | set(commented)

    whitelist_path = root / "tools" / "docs_env_whitelist.txt"
    code_only: dict[str, int] = {}
    example_only: dict[str, int] = {}
    seen: set[str] = set()
    if whitelist_path.is_file():
        for line_no, line in enumerate(
            whitelist_path.read_text(encoding="utf-8").splitlines(), 1
        ):
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            parts = line.split("\t")
            if (
                len(parts) != 3
                or not parts[0].strip()
                or not parts[2].strip()
                or parts[1] not in {"code-only", "example-only"}
            ):
                findings.append(
                    common.Finding(
                        "E-ENV-003",
                        "error",
                        "tools/docs_env_whitelist.txt",
                        line_no,
                        "白名单格式非法（需要 KEY<TAB>code-only|example-only<TAB>原因）",
                        "按格式重写该行；失效条目直接删除",
                    )
                )
                continue
            key = parts[0].strip()
            if key in seen:
                findings.append(
                    common.Finding(
                        "E-ENV-003",
                        "error",
                        "tools/docs_env_whitelist.txt",
                        line_no,
                        f"白名单 key 重复：{key}",
                        "删除重复条目",
                    )
                )
            seen.add(key)
            target = code_only if parts[1] == "code-only" else example_only
            target[key] = line_no

    for key, line_no in sorted(config_keys.items()):
        if key in example_keys or key in code_only:
            continue
        findings.append(
            common.Finding(
                "E-ENV-001",
                "error",
                "internal/config/config.go",
                line_no,
                f"config 读取的 key 未在 .env.example 出现：{key}",
                FIX_ENV,
            )
        )
    for key in sorted(example_keys):
        if key in config_keys or key in example_only:
            continue
        line_no = active.get(key, commented.get(key, 1))
        findings.append(
            common.Finding(
                "E-ENV-002",
                "error",
                ".env.example",
                line_no,
                f".env.example 的 key 未被 config 读取：{key}",
                FIX_ENV,
            )
        )

    for key, line_no in sorted(code_only.items()):
        if not (key in config_keys and key not in example_keys):
            findings.append(
                common.Finding(
                    "E-ENV-003",
                    "error",
                    "tools/docs_env_whitelist.txt",
                    line_no,
                    f"白名单条目已失效（code-only）：{key}",
                    "删除该白名单条目",
                )
            )
    for key, line_no in sorted(example_only.items()):
        if not (key in example_keys and key not in config_keys):
            findings.append(
                common.Finding(
                    "E-ENV-003",
                    "error",
                    "tools/docs_env_whitelist.txt",
                    line_no,
                    f"白名单条目已失效（example-only）：{key}",
                    "删除该白名单条目",
                )
            )

    for key, line_no in example["duplicates"]:
        findings.append(
            common.Finding(
                "E-ENV-004",
                "error",
                ".env.example",
                line_no,
                f".env.example 重复 key：{key}",
                "删除重复行，只保留一处定义",
            )
        )

    for rel in common.repo_files(root):
        if not rel.endswith(".go") or rel.endswith("_test.go"):
            continue
        if rel.startswith("internal/config/"):
            continue
        path = root / rel
        text = path.read_text(encoding="utf-8")
        for match in re.finditer(r"\bos\.(?:Getenv|LookupEnv)\s*\(", text):
            findings.append(
                common.Finding(
                    "E-ENV-005",
                    "error",
                    rel,
                    text.count("\n", 0, match.start()) + 1,
                    "internal/config 之外直接读取环境变量",
                    "改走 internal/config.Load，不在其他包直接读 os.Getenv/os.LookupEnv",
                )
            )
    return findings


# ---------------------------------------------------------------------------
# F. routes, flags, migrations
# ---------------------------------------------------------------------------


def check_routes_flags(root: Path) -> list[common.Finding]:
    findings: list[common.Finding] = []
    _, command_issues = gen_docs.collect_commands(root)
    _, route_issues = gen_docs.collect_routes(root)
    _, migration_issues = gen_docs.collect_migrations(root)
    for issue in command_issues:
        findings.append(_wrap_issue(issue, FIX_FLAG))
    for issue in route_issues:
        findings.append(_wrap_issue(issue, FIX_ROUTE))
    for issue in migration_issues:
        findings.append(_wrap_issue(issue, FIX_MIG))
    return findings


# ---------------------------------------------------------------------------
# G. age and stale drift
# ---------------------------------------------------------------------------


def check_age(
    metas: dict[str, dict], now: date, max_age_days: int
) -> list[common.Finding]:
    findings: list[common.Finding] = []
    for doc, entry in sorted(metas.items()):
        meta = entry["meta"]
        status = meta.get("status")
        if status in common.STALE_EXEMPT_STATUS:
            continue
        parsed = common.parse_iso_date(meta.get("last_verified", ""))
        if parsed is None:
            continue
        age = (now - parsed).days
        if age > max_age_days:
            findings.append(
                common.Finding(
                    "I-AGE-001",
                    "info",
                    doc,
                    entry["line"],
                    f"last_verified 距 --now 已 {age} 天（阈值 {max_age_days}）",
                    "安排 owner 对照 sources 复核并更新 last_verified",
                )
            )
    return findings


def compute_stale_findings(
    metas: dict[str, dict], changed: set[str], verbose: bool = False
) -> list[common.Finding]:
    findings: list[common.Finding] = []
    matched: set[str] = set()
    for doc, entry in sorted(metas.items()):
        meta = entry["meta"]
        status = meta.get("status")
        if status in common.STALE_EXEMPT_STATUS:
            continue
        doc_changed = doc in changed
        for source in meta.get("sources") or []:
            hits = sorted(path for path in changed if common.match_path(path, source))
            if not hits:
                continue
            # A changed document still represents a declared mapping; record it
            # so I-SRC-001 does not double-report the same code change.
            matched.update(hits)
            if doc_changed:
                continue
            severity = "warning" if status == "draft" else "error"
            code = "W-STALE-001" if status == "draft" else "E-STALE-001"
            shown = ", ".join(hits[:5]) + (" 等" if len(hits) > 5 else "")
            findings.append(
                common.Finding(
                    code,
                    severity,
                    doc,
                    entry["line"],
                    f"sources 命中变更（{source}）：{shown}，但本文档未更新",
                    FIX_STALE,
                )
            )

    changed_code = {
        path
        for path in changed
        if not common.is_governed_path(path) and not path.startswith(".team/")
    }
    unmatched = sorted(changed_code - matched)
    if unmatched:
        fix = (
            "如需强制复核，把相关路径加入对应文档的 sources；"
            "或确认无需更新后在任务日志记录原因"
        )
        if verbose:
            for path in unmatched:
                findings.append(
                    common.Finding(
                        "I-SRC-001",
                        "info",
                        path,
                        0,
                        "代码变更未匹配任何文档 sources",
                        fix,
                    )
                )
        else:
            findings.append(
                common.Finding(
                    "I-SRC-001",
                    "info",
                    "",
                    0,
                    f"{len(unmatched)} 个代码变更未匹配任何文档 sources",
                    fix,
                )
            )
    return findings


def _git(root: Path, *args: str) -> str:
    try:
        proc = subprocess.run(
            ["git", *args],
            cwd=str(root),
            text=True,
            capture_output=True,
            check=False,
        )
    except FileNotFoundError as exc:  # pragma: no cover - environment dependent
        raise CheckRuntimeError("找不到 git 可执行文件") from exc
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout).strip().splitlines()
        raise CheckRuntimeError(
            f"git {' '.join(args)} 失败：{detail[0] if detail else '未知错误'}"
        )
    return proc.stdout


_EPHEMERAL_SUFFIXES = ("~", ".swp", ".swo", ".tmp", ".temp", ".bak", ".orig", ".rej")
_EPHEMERAL_NAMES = {".DS_Store", "Thumbs.db"}


def _is_ephemeral_untracked(path: str) -> bool:
    """Ignore local scratch files even when they are not in .gitignore."""
    parts = path.split("/")
    if any(part in common.EXCLUDED_DIRS for part in parts):
        return True
    name = parts[-1]
    return name in _EPHEMERAL_NAMES or name.endswith(_EPHEMERAL_SUFFIXES)


def git_changed_paths(root: Path, base: str) -> set[str]:
    merge_base = _git(root, "merge-base", base, "HEAD").strip()
    if not merge_base:
        raise CheckRuntimeError(f"无法解析 --base {base} 的 merge-base")
    changed: set[str] = set()
    for args in (
        ("diff", "--name-only", "--no-renames", f"{merge_base}..HEAD"),
        ("diff", "--name-only", "--no-renames", "HEAD"),
        ("diff", "--cached", "--no-renames", "--name-only", "HEAD"),
    ):
        for line in _git(root, *args).splitlines():
            path = line.strip()
            if path:
                changed.add(path)
    # Untracked files are invisible to git diff, but a new source/migration
    # must still require the mapped docs to be reviewed.
    untracked = _git(root, "ls-files", "--others", "--exclude-standard", "--full-name")
    for line in untracked.splitlines():
        path = line.strip()
        if path and not _is_ephemeral_untracked(path):
            changed.add(path)
    return changed


# ---------------------------------------------------------------------------
# Orchestration and output
# ---------------------------------------------------------------------------


def run_checks(
    root: Path,
    *,
    now: date,
    max_age_days: int = 180,
    base: str | None = None,
    strict: bool = False,
    verbose: bool = False,
) -> list[common.Finding]:
    docs = common.governed_docs(root)
    findings: list[common.Finding] = []

    gen_findings, _ = check_generated(root)
    findings.extend(gen_findings)

    meta_findings, metas = check_meta(root, docs, now)
    findings.extend(meta_findings)

    findings.extend(check_agents_docs(root, docs))
    findings.extend(check_obsolete_generated(root))
    findings.extend(check_links(root, docs))
    findings.extend(check_refs(root, docs))
    findings.extend(check_governance(root))
    findings.extend(check_structure(root, docs))
    findings.extend(check_doc_budgets(root, docs))
    findings.extend(check_current_only_headings(root))
    findings.extend(check_env(root))
    findings.extend(check_routes_flags(root))
    findings.extend(check_age(metas, now, max_age_days))

    if base:
        changed = git_changed_paths(root, base)
        findings.extend(compute_stale_findings(metas, changed, verbose=verbose))

    if strict:
        findings = [
            common.Finding(
                finding.code,
                "error" if finding.severity == "warning" else finding.severity,
                finding.file,
                finding.line,
                finding.message,
                finding.fix,
            )
            for finding in findings
        ]

    deduped: dict[tuple, common.Finding] = {}
    for finding in findings:
        key = (finding.file, finding.code, finding.line, finding.message)
        deduped.setdefault(key, finding)
    return sorted(deduped.values(), key=common.finding_sort_key)


def format_text(findings: list[common.Finding]) -> str:
    blocks: list[str] = []
    for finding in findings:
        lines = [f"{finding.code}  {_format_location(finding)}", f"  原因: {finding.message}"]
        if finding.fix:
            lines.append(f"  修复: {finding.fix}")
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks)


def _escape_github(value: str) -> str:
    return value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")


def format_github(findings: list[common.Finding]) -> str:
    lines: list[str] = []
    for finding in findings:
        level = {"error": "error", "warning": "warning", "info": "notice"}[finding.severity]
        props = f"title={finding.code}"
        if finding.file:
            props = f"file={finding.file}," + (f"line={finding.line}," if finding.line else "") + props
        message = finding.message
        if finding.fix:
            message += f"；修复: {finding.fix}"
        lines.append(f"::{level} {props}::{_escape_github(message)}")
    return "\n".join(lines)


def summarize(findings: list[common.Finding]) -> str:
    errors = sum(1 for f in findings if f.severity == "error")
    warnings = sum(1 for f in findings if f.severity == "warning")
    infos = sum(1 for f in findings if f.severity == "info")
    lines = [f"{errors} errors, {warnings} warnings, {infos} info"]
    if errors:
        lines.append("修复入口: AGENTS.md")
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="文档一致性、生成物与漂移检查")
    parser.add_argument("--root", help="仓库根目录（默认向上查找）")
    parser.add_argument("--strict", action="store_true", help="把 warning 提升为 error")
    parser.add_argument("--base", help="stale 检查基线（如 origin/main）")
    parser.add_argument("--now", help="注入今天（YYYY-MM-DD），用于测试")
    parser.add_argument("--max-age-days", type=int, default=180)
    parser.add_argument("--format", choices=("text", "github"), default="text")
    parser.add_argument("--quiet", action="store_true", help="只输出汇总")
    parser.add_argument("--verbose", action="store_true", help="输出更多细节")
    args = parser.parse_args(argv)

    try:
        root = (
            Path(args.root).resolve()
            if args.root
            else common.find_repo_root(Path(__file__).resolve().parent)
        )
    except FileNotFoundError as exc:
        print(f"check_docs: {exc}", file=sys.stderr)
        return 2
    if not (root / "go.mod").is_file() or not (root / "docs").is_dir():
        print(f"check_docs: {root} 不是有效仓库根（需要 go.mod + docs/）", file=sys.stderr)
        return 2

    if args.now:
        try:
            now = date.fromisoformat(args.now)
        except ValueError:
            print(f"check_docs: --now 需要 YYYY-MM-DD，收到 {args.now}", file=sys.stderr)
            return 2
    else:
        now = datetime.now(timezone.utc).date()

    try:
        findings = run_checks(
            root,
            now=now,
            max_age_days=args.max_age_days,
            base=args.base,
            strict=args.strict,
            verbose=args.verbose,
        )
    except CheckRuntimeError as exc:
        print(f"check_docs: {exc}", file=sys.stderr)
        return 2
    except Exception as exc:  # keep CI output actionable, not a traceback
        print(f"check_docs: 内部错误：{exc}", file=sys.stderr)
        return 2

    formatter = format_github if args.format == "github" else format_text
    if findings and not args.quiet:
        print(formatter(findings))
    print(summarize(findings))

    return 1 if any(finding.severity == "error" for finding in findings) else 0


if __name__ == "__main__":
    raise SystemExit(main())
