#!/usr/bin/env python3
"""Shared helpers for the documentation generator and checker.

Only the Python standard library is used. Nothing here writes to the
repository; callers decide whether to persist generated content.
"""

from __future__ import annotations

import os
import re
import urllib.parse
from dataclasses import dataclass, field
from datetime import date
from pathlib import Path

# ---------------------------------------------------------------------------
# Governance scope
# ---------------------------------------------------------------------------

ROOT_DOCS = ("README.md", "AGENTS.md", "CLAUDE.md")
POINTER_DOCS = ("CLAUDE.md",)
OBSOLETE_GENERATED = (
    "GEMINI.md",
    ".github/copilot-instructions.md",
    ".cursor/rules/yixianqian.mdc",
)
EXCLUDED_DIRS = {
    ".git",
    ".team",
    ".cursor",
    ".mypy_cache",
    ".pytest_cache",
    "__pycache__",
    "bin",
    "node_modules",
    "vendor",
}

OWNER_VALUES = {"pm", "maintainer"}
STATUS_VALUES = {"current", "draft", "historical", "superseded", "generated"}
STALE_EXEMPT_STATUS = {"historical", "superseded", "generated"}


@dataclass(frozen=True)
class Finding:
    """One deterministic check result."""

    code: str
    severity: str  # error | warning | info
    file: str
    line: int
    message: str
    fix: str = ""


@dataclass(frozen=True)
class ExtractionIssue:
    """A fail-closed extraction problem (generator refuses to continue)."""

    code: str
    file: str
    line: int
    message: str


@dataclass
class MetaResult:
    meta: dict | None
    line: int
    errors: list[ExtractionIssue] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Repository paths and glob semantics
# ---------------------------------------------------------------------------


def find_repo_root(start: Path | str | None = None) -> Path:
    """Walk up from *start* until a directory with go.mod + docs/ is found."""
    if start is None:
        start = Path(__file__).resolve().parent
    current = Path(start).resolve()
    if current.is_file():
        current = current.parent
    for candidate in (current, *current.parents):
        if (candidate / "go.mod").is_file() and (candidate / "docs").is_dir():
            return candidate
    raise FileNotFoundError("找不到仓库根目录（需要同时包含 go.mod 与 docs/）")


def repo_files(root: Path | str) -> list[str]:
    """All files under root (excluding ignored dirs), as sorted POSIX paths."""
    root = Path(root)
    files: list[str] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(name for name in dirnames if name not in EXCLUDED_DIRS)
        rel_dir = Path(dirpath).relative_to(root).as_posix()
        for name in filenames:
            rel = name if rel_dir == "." else f"{rel_dir}/{name}"
            files.append(rel)
    return sorted(files)


def governed_docs(root: Path | str) -> list[str]:
    """Markdown files covered by doc-meta/AGENTS/link checks."""
    root = Path(root)
    docs: set[str] = set()
    for name in ROOT_DOCS:
        if (root / name).is_file():
            docs.add(name)
    for base_name in ("docs", ".github"):
        base = root / base_name
        if not base.is_dir():
            continue
        for path in base.rglob("*.md"):
            rel = path.relative_to(root).as_posix()
            if rel in OBSOLETE_GENERATED:
                continue
            if any(part in EXCLUDED_DIRS for part in rel.split("/")):
                continue
            docs.add(rel)
    return sorted(docs)


def glob_to_regex(pattern: str) -> re.Pattern[str]:
    """Translate repo glob semantics to a full-match regex.

    ``**/`` matches zero or more directories, ``**`` matches anything,
    ``*`` never crosses ``/`` and ``?`` matches one non-slash character.
    """
    pattern = pattern.strip()
    if pattern.startswith("./"):
        pattern = pattern[2:]
    if pattern.endswith("/"):
        pattern += "**"
    out: list[str] = []
    i = 0
    while i < len(pattern):
        char = pattern[i]
        if char == "*":
            if i + 1 < len(pattern) and pattern[i + 1] == "*":
                if i + 2 < len(pattern) and pattern[i + 2] == "/":
                    out.append("(?:.*/)?")
                    i += 3
                else:
                    out.append(".*")
                    i += 2
            else:
                out.append("[^/]*")
                i += 1
        elif char == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(char))
            i += 1
    return re.compile(r"\A" + "".join(out) + r"\Z")


def match_path(path: str, pattern: str) -> bool:
    """Match a repo-relative path against a doc-meta source pattern."""
    pattern = pattern.strip()
    if not pattern:
        return False
    if not any(ch in pattern for ch in "*?"):
        if pattern.endswith("/"):
            return path.startswith(pattern)
        return path == pattern
    return bool(glob_to_regex(pattern).fullmatch(path))


def source_exists(root: Path | str, pattern: str, files: list[str] | None = None) -> bool:
    root = Path(root)
    files = repo_files(root) if files is None else files
    for rel in files:
        if match_path(rel, pattern):
            return True
    # Empty directories cannot match a file walk, but are still valid targets.
    candidate = root / pattern.rstrip("/")
    return candidate.exists()


def is_governed_path(path: str) -> bool:
    if path in ROOT_DOCS:
        return True
    if path in OBSOLETE_GENERATED:
        return False
    if not path.endswith(".md"):
        return False
    if "/" not in path:
        return True  # any root-level markdown is documentation
    return path.startswith("docs/") or path.startswith(".github/")


# ---------------------------------------------------------------------------
# doc-meta parsing
# ---------------------------------------------------------------------------

_META_RE = re.compile(r"<!--\s*doc-meta\b(.*?)-->", re.S)
_META_KEY_RE = re.compile(r"^([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.*)$")
_META_ITEM_RE = re.compile(r"^  -\s+(.+)$")
_META_KEYS = {
    "owner",
    "status",
    "last_verified",
    "sources",
    "superseded_by",
    "generated",
}


def parse_doc_meta(text: str, path: str = "") -> MetaResult:
    """Parse the single HTML-comment doc-meta block (YAML subset)."""
    # Ignore examples that appear inside fenced code blocks.
    outside_fences = {line_no for line_no, _ in markdown_lines(text)}
    matches = [
        match
        for match in _META_RE.finditer(text)
        if text.count("\n", 0, match.start()) + 1 in outside_fences
    ]
    if not matches:
        return MetaResult(
            None,
            0,
            [ExtractionIssue("E-META-001", path, 1, "缺少 doc-meta 注释块")],
        )

    errors: list[ExtractionIssue] = []
    for extra in matches[1:]:
        line = text.count("\n", 0, extra.start()) + 1
        errors.append(
            ExtractionIssue("E-META-001", path, line, "doc-meta 注释块不止一个")
        )

    block = matches[0]
    start_line = text.count("\n", 0, block.start()) + 1
    if start_line > 40:
        errors.append(
            ExtractionIssue(
                "E-META-002",
                path,
                start_line,
                "doc-meta 必须位于文件前 40 行（H1 之后、正文之前）",
            )
        )

    meta: dict = {}
    seen: set[str] = set()
    current_list: list[str] | None = None
    sources_empty = False
    body_lines = block.group(1).splitlines()

    for offset, raw_line in enumerate(body_lines):
        line_no = start_line + offset
        stripped = raw_line.strip()
        if not stripped:
            continue
        item = _META_ITEM_RE.match(raw_line)
        if item:
            if current_list is None:
                errors.append(
                    ExtractionIssue(
                        "E-META-002", path, line_no, "列表项必须位于 sources: 下"
                    )
                )
                continue
            value = item.group(1).strip()
            sources_empty = False
            if not value:
                errors.append(
                    ExtractionIssue("E-META-002", path, line_no, "sources 列表项为空")
                )
            else:
                current_list.append(value)
            continue

        key_match = _META_KEY_RE.match(stripped)
        if not key_match:
            errors.append(
                ExtractionIssue(
                    "E-META-002", path, line_no, f"无法解析 doc-meta 行：{stripped}"
                )
            )
            continue

        if sources_empty:
            errors.append(
                ExtractionIssue(
                    "E-META-002",
                    path,
                    line_no,
                    "sources 列表为空；空映射请写 sources: []",
                )
            )
            sources_empty = False

        key = key_match.group(1)
        value = key_match.group(2).strip()
        if key not in _META_KEYS:
            errors.append(
                ExtractionIssue("E-META-002", path, line_no, f"未知 doc-meta 字段：{key}")
            )
            current_list = None
            continue
        if key in seen:
            errors.append(
                ExtractionIssue("E-META-002", path, line_no, f"重复 doc-meta 字段：{key}")
            )
            current_list = None
            continue
        seen.add(key)

        if key == "sources":
            if value == "[]":
                meta[key] = []
                current_list = None
            elif value == "":
                meta[key] = []
                current_list = meta[key]
                sources_empty = True
            else:
                errors.append(
                    ExtractionIssue(
                        "E-META-002",
                        path,
                        line_no,
                        "sources 只支持 `sources: []` 或每行两空格缩进的 `- item`",
                    )
                )
                current_list = None
            continue

        current_list = None
        if not value:
            errors.append(
                ExtractionIssue("E-META-002", path, line_no, f"{key} 不能为空")
            )
            continue
        meta[key] = value

    if sources_empty:
        errors.append(
            ExtractionIssue(
                "E-META-002",
                path,
                start_line,
                "sources 列表为空；空映射请写 sources: []",
            )
        )

    for required in ("owner", "status", "sources"):
        if required not in meta:
            errors.append(
                ExtractionIssue("E-META-002", path, start_line, f"缺少必填字段：{required}")
            )

    return MetaResult(meta, start_line, errors)


def parse_iso_date(value: str) -> date | None:
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", value or ""):
        return None
    try:
        return date.fromisoformat(value)
    except ValueError:
        return None


# ---------------------------------------------------------------------------
# Markdown helpers
# ---------------------------------------------------------------------------

_FENCE_RE = re.compile(r"^\s{0,3}(`{3,}|~{3,})")
_INLINE_CODE_RE = re.compile(r"`([^`\n]+)`")
_LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)\s]+)(?:\s+[\"'][^\"']*[\"'])?\)")
_SCHEME_RE = re.compile(r"^[A-Za-z][A-Za-z0-9+.-]*:")


def markdown_lines(text: str):
    """Yield (line_no, line) for lines outside fenced code blocks."""
    in_fence = False
    fence_char = ""
    for line_no, line in enumerate(text.splitlines(), 1):
        fence = _FENCE_RE.match(line)
        if fence:
            marker = fence.group(1)
            if not in_fence:
                in_fence = True
                fence_char = marker[0]
            elif marker[0] == fence_char:
                in_fence = False
            continue
        if in_fence:
            continue
        yield line_no, line


def strip_inline_code(line: str) -> str:
    return _INLINE_CODE_RE.sub("", line)


def extract_links(text: str):
    """Yield (line_no, raw_target) for markdown links/images outside code."""
    for line_no, line in markdown_lines(text):
        cleaned = strip_inline_code(line)
        for match in _LINK_RE.finditer(cleaned):
            yield line_no, match.group(1)


def normalize_link_target(raw: str) -> str | None:
    target = raw.strip()
    if target.startswith("<") and target.endswith(">"):
        target = target[1:-1].strip()
    if not target or target.startswith(("#", "//")):
        return None
    if _SCHEME_RE.match(target):
        return None
    target = target.split("#", 1)[0].split("?", 1)[0]
    target = urllib.parse.unquote(target)
    return target or None


def extract_inline_code(text: str):
    """Yield (line_no, token) for inline code spans outside fences."""
    for line_no, line in markdown_lines(text):
        for match in _INLINE_CODE_RE.finditer(line):
            yield line_no, match.group(1).strip()


def md_table_cell(value: str) -> str:
    """Make a value safe inside a Markdown table cell."""
    return " ".join(str(value).splitlines()).replace("|", "\\|")


def finding_sort_key(finding: Finding):
    return (finding.file, finding.code, finding.line, finding.message)
