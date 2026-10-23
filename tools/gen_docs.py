#!/usr/bin/env python3
"""Generate docs/reference/REFERENCE.md and thin AI entry pointers.

The generator is deliberately deterministic: no timestamps, commit SHAs,
hostnames or absolute paths. It reads only repository text inputs (never
``.env``) and writes only the paths registered in ``OUTPUTS``.

Usage:
    python3 tools/gen_docs.py [--root PATH] [--check] [--stdout PATH]
"""

from __future__ import annotations

import argparse
import difflib
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import docs_common as common  # noqa: E402

METHOD_ORDER = {
    "GET": 0,
    "POST": 1,
    "PUT": 2,
    "PATCH": 3,
    "DELETE": 4,
    "HEAD": 5,
    "OPTIONS": 6,
    "CONNECT": 7,
    "TRACE": 8,
    "ANY": 9,
}
SUPPORTED_FLAG_TYPES = {
    "String": "string",
    "Bool": "bool",
    "Int": "int",
    "Int64": "int64",
    "Uint": "uint",
    "Float64": "float64",
    "Duration": "duration",
}

REFERENCE_PATH = "docs/reference/REFERENCE.md"

_FLAG_CALL_RE = re.compile(r"\bflag\.([A-Za-z_][A-Za-z0-9_]*)\s*\(")
_ROUTE_CALL_RE = re.compile(r"\b([A-Za-z_][A-Za-z0-9_]*)\.(HandleFunc|Handle)\s*\(")
_ROUTE_GUARD_RE = re.compile(r"\.(HandleFunc|Handle)\s*\(")
_MIGRATION_RE = re.compile(r"^(\d{4})_[A-Za-z0-9_.-]+\.sql$")
_MAKE_TARGET_RE = re.compile(r"^([A-Za-z0-9][\w.-]*)\s*:(?!=)(.*)$")
_GO_DIRECTIVE_RE = re.compile(r"(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$")
_MODULE_RE = re.compile(r"(?m)^module\s+(\S+)")
_CI_GO_VERSION_RE = re.compile(r"go-version:\s*['\"]?([^'\"\s]+)")
_CONFIG_ENV_RE = re.compile(
    r"(?:\bgetenv|\bget|os\.Getenv|os\.LookupEnv)\(\s*\"([A-Z][A-Z0-9_]*)\""
)
_PACKAGE_RE = re.compile(r"^package\s+([A-Za-z_][A-Za-z0-9_]*)")


class ExtractionError(Exception):
    def __init__(self, issues: list[common.ExtractionIssue]):
        super().__init__("generation inputs could not be parsed")
        self.issues = issues


# ---------------------------------------------------------------------------
# Go source scanning
# ---------------------------------------------------------------------------


def strip_go_comments(src: str) -> str:
    """Replace Go comments with spaces while preserving length/newlines."""
    out: list[str] = []
    i = 0
    n = len(src)
    state = None  # line | block | str | raw | char
    while i < n:
        char = src[i]
        nxt = src[i + 1] if i + 1 < n else ""
        if state is None:
            if char == "/" and nxt == "/":
                state = "line"
                out.extend("  ")
                i += 2
                continue
            if char == "/" and nxt == "*":
                state = "block"
                out.extend("  ")
                i += 2
                continue
            if char == '"':
                state = "str"
            elif char == "`":
                state = "raw"
            elif char == "'":
                state = "char"
            out.append(char)
            i += 1
            continue
        if state == "line":
            if char == "\n":
                state = None
                out.append(char)
            else:
                out.append(" ")
            i += 1
            continue
        if state == "block":
            if char == "*" and nxt == "/":
                state = None
                out.extend("  ")
                i += 2
                continue
            out.append("\n" if char == "\n" else " ")
            i += 1
            continue
        if state == "raw":
            out.append(char)
            if char == "`":
                state = None
            i += 1
            continue
        # str / char
        out.append(char)
        if char == "\\" and i + 1 < n:
            out.append(src[i + 1])
            i += 2
            continue
        if (state == "str" and char == '"') or (state == "char" and char == "'"):
            state = None
        i += 1
    return "".join(out)


def mask_go_strings(src: str) -> str:
    """Blank string/char literals while preserving length and newlines."""
    out: list[str] = []
    i = 0
    n = len(src)
    state = None
    while i < n:
        char = src[i]
        if state is None:
            if char == '"':
                state = "str"
                out.append(" ")
            elif char == "`":
                state = "raw"
                out.append(" ")
            elif char == "'":
                state = "char"
                out.append(" ")
            else:
                out.append(char)
            i += 1
            continue
        if state == "raw":
            out.append("\n" if char == "\n" else " ")
            if char == "`":
                state = None
            i += 1
            continue
        if char == "\\" and i + 1 < n:
            out.append("  ")
            i += 2
            continue
        out.append("\n" if char == "\n" else " ")
        if (state == "str" and char == '"') or (state == "char" and char == "'"):
            state = None
        i += 1
    return "".join(out)


def parse_call_args(text: str, open_index: int) -> list[str] | None:
    """Split the argument list of a call whose ``(`` is at *open_index*."""
    if open_index >= len(text) or text[open_index] != "(":
        return None
    args: list[str] = []
    start = open_index + 1
    depth = 1
    i = start
    state = None
    while i < len(text):
        char = text[i]
        if state is not None:
            if state == "raw":
                if char == "`":
                    state = None
            elif state in {"str", "char"}:
                if char == "\\":
                    i += 2
                    continue
                if (state == "str" and char == '"') or (state == "char" and char == "'"):
                    state = None
            i += 1
            continue
        if char in {'"', "`", "'"}:
            state = {"\"": "str", "`": "raw", "'": "char"}[char]
            i += 1
            continue
        if char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
            if depth == 0:
                tail = text[start:i].strip()
                if tail:
                    args.append(tail)
                elif args:
                    pass
                return [arg for arg in args if arg]
        elif char == "," and depth == 1:
            args.append(text[start:i].strip())
            start = i + 1
        i += 1
    return None


def go_string_value(raw: str) -> str | None:
    raw = raw.strip()
    if len(raw) >= 2 and raw[0] == "`" and raw[-1] == "`":
        return raw[1:-1]
    if len(raw) >= 2 and raw[0] == '"' and raw[-1] == '"':
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            # Fallback for Go escapes that JSON rejects (e.g. \xNN).
            try:
                return bytes(raw[1:-1], "utf-8").decode("unicode_escape")
            except UnicodeDecodeError:
                return raw[1:-1]
    return None


def _line_of(text: str, index: int) -> int:
    return text.count("\n", 0, index) + 1


def scan_flags(text: str, path: str) -> tuple[list[dict], list[common.ExtractionIssue]]:
    rows: list[dict] = []
    issues: list[common.ExtractionIssue] = []
    clean = strip_go_comments(text)
    masked = mask_go_strings(clean)
    for match in _FLAG_CALL_RE.finditer(masked):
        name = match.group(1)
        line = _line_of(clean, match.start())
        if name == "Parse":
            continue
        args = parse_call_args(clean, match.end() - 1)
        if name not in SUPPORTED_FLAG_TYPES:
            issues.append(
                common.ExtractionIssue(
                    "E-FLAG-001",
                    path,
                    line,
                    f"未支持的 flag 形态 flag.{name}；改用 flag.String/Bool/Int/Int64/Uint/Float64/Duration",
                )
            )
            continue
        if args is None or len(args) < 3:
            issues.append(
                common.ExtractionIssue(
                    "E-FLAG-001", path, line, f"flag.{name} 参数无法解析（需要 name/default/usage）"
                )
            )
            continue
        flag_name = go_string_value(args[0])
        usage = go_string_value(args[2])
        if flag_name is None:
            issues.append(
                common.ExtractionIssue(
                    "E-FLAG-001", path, line, "flag 名称必须是字符串字面量"
                )
            )
            continue
        if usage is None:
            issues.append(
                common.ExtractionIssue(
                    "E-FLAG-001", path, line, "flag usage 必须是字符串字面量"
                )
            )
            continue
        rows.append(
            {
                "flag": flag_name,
                "type": SUPPORTED_FLAG_TYPES[name],
                "default": args[1].strip(),
                "usage": usage,
                "source": path,
            }
        )
    return rows, issues


def parse_route_pattern(literal: str) -> tuple[str, str] | None:
    match = re.match(
        r"^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE) (/.+)$", literal
    )
    if match:
        return match.group(1), match.group(2)
    if literal.startswith("/"):
        return "ANY", literal
    return None


def scan_routes(
    text: str, path: str, seen: dict[tuple[str, str], dict] | None = None
) -> tuple[list[dict], list[common.ExtractionIssue]]:
    clean = strip_go_comments(text)
    masked = mask_go_strings(clean)
    seen = {} if seen is None else seen
    routes: list[dict] = []
    issues: list[common.ExtractionIssue] = []
    attributed: set[int] = set()

    for match in _ROUTE_CALL_RE.finditer(masked):
        open_index = match.end() - 1
        attributed.add(open_index)
        line = _line_of(clean, match.start())
        args = parse_call_args(clean, open_index)
        if not args:
            issues.append(
                common.ExtractionIssue(
                    "E-ROUTE-001", path, line, "Handle/HandleFunc 参数无法解析"
                )
            )
            continue
        literal = go_string_value(args[0])
        if literal is None:
            issues.append(
                common.ExtractionIssue(
                    "E-ROUTE-001",
                    path,
                    line,
                    "路由 pattern 必须是字符串字面量；改成字面量或扩展抽取器",
                )
            )
            continue
        parsed = parse_route_pattern(literal)
        if parsed is None:
            issues.append(
                common.ExtractionIssue(
                    "E-ROUTE-001",
                    path,
                    line,
                    f"无法识别的路由 pattern：{literal}（应以 / 开头）",
                )
            )
            continue
        method, route_path = parsed
        routes.append(
            {
                "method": method,
                "path": route_path,
                "source": path,
                "line": line,
            }
        )

    # Coverage guard: any .Handle/.HandleFunc not attributed above is a
    # fail-closed extraction gap (e.g. getMux().HandleFunc(...)).
    for match in _ROUTE_GUARD_RE.finditer(masked):
        open_index = match.end() - 1
        if open_index in attributed:
            continue
        issues.append(
            common.ExtractionIssue(
                "E-ROUTE-001",
                path,
                _line_of(clean, match.start()),
                "Handle/HandleFunc 调用形态未被抽取器识别；改成字面量或扩展抽取器",
            )
        )

    for route in routes:
        key = (route["method"], route["path"])
        if key in seen:
            first = seen[key]
            issues.append(
                common.ExtractionIssue(
                    "E-ROUTE-002",
                    route["source"],
                    route["line"],
                    f"重复路由 {route['method']} {route['path']}（首次：{first['source']}:{first['line']}）",
                )
            )
        else:
            seen[key] = route
    return routes, issues


def collect_commands(root: Path) -> tuple[list[dict], list[common.ExtractionIssue]]:
    rows: list[dict] = []
    issues: list[common.ExtractionIssue] = []
    cmd_root = root / "cmd"
    if not cmd_root.is_dir():
        return rows, issues
    for path in sorted(cmd_root.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        rel = path.relative_to(root).as_posix()
        text = path.read_text(encoding="utf-8")
        flag_rows, flag_issues = scan_flags(text, rel)
        issues.extend(flag_issues)
        cmd = "cmd/" + rel.split("/")[1]
        for row in flag_rows:
            row = dict(row)
            row["cmd"] = cmd
            rows.append(row)
    rows.sort(key=lambda row: (row["cmd"], row["flag"]))
    return rows, issues


def collect_routes(root: Path) -> tuple[list[dict], list[common.ExtractionIssue]]:
    routes: list[dict] = []
    issues: list[common.ExtractionIssue] = []
    base = root / "internal" / "httpserver"
    if not base.is_dir():
        return routes, issues
    seen: dict[tuple[str, str], dict] = {}
    for path in sorted(base.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        rel = path.relative_to(root).as_posix()
        text = path.read_text(encoding="utf-8")
        file_routes, file_issues = scan_routes(text, rel, seen)
        routes.extend(file_routes)
        issues.extend(file_issues)
    routes.sort(key=lambda row: (row["path"], METHOD_ORDER.get(row["method"], 99)))
    return routes, issues


# ---------------------------------------------------------------------------
# Non-Go inputs
# ---------------------------------------------------------------------------


def parse_go_mod(text: str) -> tuple[str | None, str | None, list[common.ExtractionIssue]]:
    issues: list[common.ExtractionIssue] = []
    go_match = _GO_DIRECTIVE_RE.search(text)
    module_match = _MODULE_RE.search(text)
    if not go_match:
        issues.append(
            common.ExtractionIssue("E-GEN-002", "go.mod", 1, "go.mod 缺少 go 指令")
        )
    if not module_match:
        issues.append(
            common.ExtractionIssue("E-GEN-002", "go.mod", 1, "go.mod 缺少 module 指令")
        )
    go_version = go_match.group(1) if go_match else None
    module = module_match.group(1) if module_match else None
    return go_version, module, issues


def parse_ci_go_versions(text: str) -> list[str]:
    versions: list[str] = []
    for match in _CI_GO_VERSION_RE.finditer(text):
        value = match.group(1).strip()
        if value not in versions:
            versions.append(value)
    return sorted(versions)


def parse_env_example(text: str) -> dict:
    """Return key sets and duplicate lines. Values are intentionally discarded."""
    active: dict[str, int] = {}
    commented: dict[str, int] = {}
    duplicates: list[tuple[str, int]] = []
    for line_no, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith("#"):
            match = re.match(r"^#\s*([A-Z][A-Z0-9_]*)\s*=", line)
            if not match:
                continue
            key = match.group(1)
            if key in active or key in commented:
                duplicates.append((key, line_no))
            commented.setdefault(key, line_no)
            continue
        match = re.match(r"^\s*([A-Z][A-Z0-9_]*)\s*=", line)
        if not match:
            continue
        key = match.group(1)
        if key in active or key in commented:
            duplicates.append((key, line_no))
        active.setdefault(key, line_no)
    return {
        "active": active,
        "commented": commented,
        "duplicates": duplicates,
    }


def scan_config_env(text: str) -> dict[str, int]:
    keys: dict[str, int] = {}
    clean = strip_go_comments(text)
    for match in _CONFIG_ENV_RE.finditer(clean):
        key = match.group(1)
        if key not in keys:
            keys[key] = _line_of(clean, match.start())
    return keys


def parse_migrations(entries: list[tuple[str, str]]) -> tuple[list[dict], list[common.ExtractionIssue]]:
    rows: list[dict] = []
    issues: list[common.ExtractionIssue] = []
    for name, text in entries:
        match = _MIGRATION_RE.match(name)
        if not match:
            issues.append(
                common.ExtractionIssue(
                    "E-GEN-002",
                    f"migrations/{name}",
                    1,
                    "migration 文件名必须为 NNNN_name.sql",
                )
            )
            continue
        number = int(match.group(1))
        first_line = text.splitlines()[0].strip() if text.splitlines() else ""
        title = first_line[2:].strip() if first_line.startswith("--") else "—"
        rows.append({"number": number, "file": name, "title": title})
    rows.sort(key=lambda row: (row["number"], row["file"]))

    seen: dict[int, str] = {}
    expected = 1
    for row in rows:
        if row["number"] in seen:
            issues.append(
                common.ExtractionIssue(
                    "E-MIG-001",
                    f"migrations/{row['file']}",
                    1,
                    f"migration 编号 {row['number']:04d} 重复（首次：{seen[row['number']]}）",
                )
            )
            continue
        seen[row["number"]] = row["file"]
        if row["number"] != expected:
            issues.append(
                common.ExtractionIssue(
                    "E-MIG-001",
                    f"migrations/{row['file']}",
                    1,
                    f"migration 编号不连续：期望 {expected:04d}，实际 {row['number']:04d}",
                )
            )
            expected = row["number"] + 1
        else:
            expected += 1
    return rows, issues


def collect_migrations(root: Path) -> tuple[list[dict], list[common.ExtractionIssue]]:
    base = root / "migrations"
    if not base.is_dir():
        return [], []
    entries = [
        (path.name, path.read_text(encoding="utf-8"))
        for path in sorted(base.glob("*.sql"))
    ]
    return parse_migrations(entries)


def parse_makefile(text: str) -> list[dict]:
    targets: list[dict] = []
    for line_no, line in enumerate(text.splitlines(), 1):
        if line.startswith("\t"):
            continue
        match = _MAKE_TARGET_RE.match(line)
        if not match:
            continue
        name = match.group(1)
        deps = match.group(2).split("#", 1)[0].split(";", 1)[0].split()
        deps = [dep for dep in deps if dep != "|"]
        targets.append({"target": name, "deps": deps, "line": line_no})
    targets.sort(key=lambda row: row["target"])
    return targets


def collect_make_targets(root: Path) -> list[dict]:
    makefile = root / "Makefile"
    if not makefile.is_file():
        return []
    return parse_makefile(makefile.read_text(encoding="utf-8"))


def parse_package_info(text: str) -> tuple[str, str]:
    lines = text.splitlines()
    package_index = None
    package_name = "—"
    for index, line in enumerate(lines):
        match = _PACKAGE_RE.match(line.strip())
        if match:
            package_index = index
            package_name = match.group(1)
            break
    if package_index is None:
        return "—", "—"
    doc_lines: list[str] = []
    index = package_index - 1
    while index >= 0 and lines[index].strip().startswith("//"):
        doc_lines.insert(0, lines[index].strip()[2:].strip())
        index -= 1
    doc = doc_lines[0] if doc_lines and doc_lines[0] else "—"
    return package_name, doc


def collect_structure(root: Path) -> list[dict]:
    rows: list[dict] = []
    for parent in ("cmd", "internal"):
        base = root / parent
        if not base.is_dir():
            continue
        for directory in sorted(base.iterdir()):
            if not directory.is_dir():
                continue
            go_files = sorted(
                path
                for path in directory.glob("*.go")
                if not path.name.endswith("_test.go")
            )
            if not go_files:
                continue
            package, doc = parse_package_info(go_files[0].read_text(encoding="utf-8"))
            rows.append(
                {
                    "path": f"{parent}/{directory.name}",
                    "package": package,
                    "doc": doc,
                }
            )
    for name in ("web", "migrations", "tools", "deploy"):
        if (root / name).is_dir():
            rows.append({"path": name, "package": "—", "doc": "—"})
    rows.sort(key=lambda row: row["path"])
    return rows


def collect_doc_sources(root: Path) -> list[dict]:
    rows: list[dict] = []
    for rel in common.governed_docs(root):
        path = root / rel
        text = path.read_text(encoding="utf-8")
        result = common.parse_doc_meta(text, rel)
        if result.errors or not result.meta:
            continue  # tolerate missing/invalid meta until the checker reports it
        if result.meta.get("status") == "generated":
            continue
        rows.append(
            {
                "path": rel,
                "sources": list(result.meta.get("sources") or []),
            }
        )
    rows.sort(key=lambda row: row["path"])
    return rows


# ---------------------------------------------------------------------------
# Rendering
# ---------------------------------------------------------------------------


def _toolchain_lines(go_version: str | None, module: str | None, ci_versions: list[str]) -> list[str]:
    lines = [
        "## 1. 工具链",
        "",
        "| 项 | 值 | 来源 |",
        "|---|---|---|",
        f"| Go（canonical） | `{go_version or '—'}` | `go.mod` |",
        f"| CI Go | {'、'.join(f'`{v}`' for v in ci_versions) if ci_versions else '—'} | `.github/workflows/ci.yml` |",
        f"| module | `{module or '—'}` | `go.mod` |",
        "",
    ]
    return lines


def _command_lines(rows: list[dict], cmd_names: list[str]) -> list[str]:
    lines = [
        "## 2. 命令入口与 flags",
        "",
        "| cmd | flag | 类型 | 默认 | 说明 | 来源 |",
        "|---|---|---|---|---|---|",
    ]
    by_cmd: dict[str, list[dict]] = {name: [] for name in cmd_names}
    for row in rows:
        by_cmd.setdefault(row["cmd"], []).append(row)
    for cmd in sorted(by_cmd):
        flag_rows = by_cmd[cmd]
        if not flag_rows:
            lines.append(f"| `{cmd}` | — | — | — | — | `{cmd}/main.go` |")
            continue
        for row in flag_rows:
            lines.append(
                "| `{cmd}` | `-{flag}` | {type} | `{default}` | {usage} | `{source}` |".format(
                    cmd=cmd,
                    flag=common.md_table_cell(row["flag"]),
                    type=common.md_table_cell(row["type"]),
                    default=common.md_table_cell(row["default"]),
                    usage=common.md_table_cell(row["usage"]),
                    source=common.md_table_cell(row["source"]),
                )
            )
    lines.append("")
    return lines


def _route_group(path: str) -> str:
    for prefix in ("/admin", "/api", "/legacy"):
        if path == prefix or path.startswith(prefix + "/"):
            return prefix
    return "其他"


def _route_lines(rows: list[dict]) -> list[str]:
    lines = [
        "## 3. HTTP 路由",
        "",
        "| 方法 | 路径 | 来源 |",
        "|---|---|---|",
    ]
    for row in rows:
        lines.append(
            f"| {row['method']} | `{row['path']}` | `{row['source']}` |"
        )
    counts = {"/admin": 0, "/api": 0, "/legacy": 0, "其他": 0}
    for row in rows:
        counts[_route_group(row["path"])] += 1
    lines.extend(
        [
            "",
            "### 3.1 分组统计",
            "",
            "| 前缀 | 数量 |",
            "|---|---:|",
            f"| `/admin` | {counts['/admin']} |",
            f"| `/api` | {counts['/api']} |",
            f"| `/legacy` | {counts['/legacy']} |",
            f"| 其他 | {counts['其他']} |",
            f"| **合计** | **{len(rows)}** |",
            "",
        ]
    )
    return lines


def _env_lines(config_keys: dict[str, int], example: dict) -> list[str]:
    active = example["active"]
    commented = example["commented"]
    keys = sorted(set(config_keys) | set(active) | set(commented))
    lines = [
        "## 4. 环境变量（key 集合对比）",
        "",
        "| KEY | `internal/config/config.go` | `.env.example` |",
        "|---|---|---|",
    ]
    for key in keys:
        in_config = "是" if key in config_keys else "—"
        if key in active:
            in_example = "是"
        elif key in commented:
            in_example = "是（注释默认值）"
        else:
            in_example = "否"
        lines.append(f"| `{key}` | {in_config} | {in_example} |")
    lines.extend(["", "> 本表不复制任何 env 值。", ""])
    return lines


def _migration_lines(rows: list[dict]) -> list[str]:
    lines = [
        "## 5. Migrations",
        "",
        "| 编号 | 文件 | 首行说明 |",
        "|---:|---|---|",
    ]
    for row in rows:
        lines.append(
            f"| {row['number']:04d} | `{row['file']}` | {common.md_table_cell(row['title'])} |"
        )
    lines.append("")
    return lines


def _make_lines(rows: list[dict]) -> list[str]:
    lines = [
        "## 6. make targets",
        "",
        "| target | 依赖 |",
        "|---|---|",
    ]
    for row in rows:
        deps = "`" + " ".join(row["deps"]) + "`" if row["deps"] else "—"
        lines.append(f"| `{row['target']}` | {deps} |")
    lines.append("")
    return lines


def _structure_lines(rows: list[dict]) -> list[str]:
    lines = [
        "## 7. 顶层包与目录",
        "",
        "| 路径 | Go package | 源码说明 |",
        "|---|---|---|",
    ]
    for row in rows:
        lines.append(
            f"| `{row['path']}` | {common.md_table_cell(row['package'])} | {common.md_table_cell(row['doc'])} |"
        )
    lines.append("")
    return lines


def _doc_source_lines(rows: list[dict]) -> list[str]:
    lines = [
        "## 8. 文档影响映射（来自 doc-meta）",
        "",
        "| 文档 | sources |",
        "|---|---|",
    ]
    for row in rows:
        sources = (
            ", ".join(f"`{source}`" for source in row["sources"])
            if row["sources"]
            else "—"
        )
        lines.append(f"| `{row['path']}` | {sources} |")
    lines.append("")
    return lines


def render_reference(data: dict) -> str:
    lines = [
        "<!-- generated by tools/gen_docs.py; do not edit -->",
        "<!-- doc-meta",
        "owner: maintainer",
        "status: generated",
        "sources: []",
        "-->",
        "",
        "# 代码参考（自动生成）",
        "",
        "> 本文件由 `make docs-gen` 生成，禁止手工编辑。",
        "> 只记录机械事实；语义说明看 `README.md`、`AGENTS.md`。",
        "> 权威内容永远是源码；本文件与源码冲突时以源码为准，并运行 `make docs-gen`。",
        "",
    ]
    lines += _toolchain_lines(data["go_version"], data["module"], data["ci_versions"])
    lines += _command_lines(data["commands"], data["cmd_names"])
    lines += _route_lines(data["routes"])
    lines += _env_lines(data["config_env"], data["env_example"])
    lines += _migration_lines(data["migrations"])
    lines += _make_lines(data["make_targets"])
    lines += _structure_lines(data["structure"])
    lines += _doc_source_lines(data["doc_sources"])
    return "\n".join(lines).rstrip("\n") + "\n"


def render_claude_pointer() -> str:
    lines = [
        "<!-- doc-meta",
        "owner: maintainer",
        "status: generated",
        "sources: []",
        "-->",
        "",
        "# yixianqian — Claude Code 入口",
        "",
        "> 本文件由 `tools/gen_docs.py` 生成，不要手工编辑。",
        "> 项目规则只有一份：`AGENTS.md`。",
        "",
        "@AGENTS.md",
        "",
        "1. 先读 `AGENTS.md`。",
        "2. 项目概览看 `README.md`；当前状态看 `docs/STATUS.md`。",
        "3. 机械事实看 `docs/reference/REFERENCE.md`（自动生成，勿手改）；与源码冲突时以源码为准。",
        "4. 改完运行 `make docs-check`；生成物用 `make docs-gen` 刷新。",
    ]
    return "\n".join(lines) + "\n"


OUTPUTS = {
    REFERENCE_PATH: render_reference,
    "CLAUDE.md": lambda _data: render_claude_pointer(),
}
OBSOLETE_OUTPUTS = common.OBSOLETE_GENERATED


# ---------------------------------------------------------------------------
# Orchestration
# ---------------------------------------------------------------------------


def collect_data(root: Path) -> dict:
    issues: list[common.ExtractionIssue] = []

    go_mod_path = root / "go.mod"
    if not go_mod_path.is_file():
        raise ExtractionError(
            [common.ExtractionIssue("E-GEN-002", "go.mod", 1, "缺少 go.mod")]
        )
    go_version, module, go_issues = parse_go_mod(
        go_mod_path.read_text(encoding="utf-8")
    )
    issues.extend(go_issues)

    ci_path = root / ".github" / "workflows" / "ci.yml"
    ci_versions = (
        parse_ci_go_versions(ci_path.read_text(encoding="utf-8"))
        if ci_path.is_file()
        else []
    )

    commands, command_issues = collect_commands(root)
    issues.extend(command_issues)
    routes, route_issues = collect_routes(root)
    issues.extend(route_issues)
    migrations, migration_issues = collect_migrations(root)
    issues.extend(migration_issues)

    config_path = root / "internal" / "config" / "config.go"
    config_env = (
        scan_config_env(config_path.read_text(encoding="utf-8"))
        if config_path.is_file()
        else {}
    )
    env_path = root / ".env.example"  # never .env
    env_example = (
        parse_env_example(env_path.read_text(encoding="utf-8"))
        if env_path.is_file()
        else {"active": {}, "commented": {}, "duplicates": []}
    )

    cmd_names = sorted(
        "cmd/" + path.parent.name
        for path in (root / "cmd").glob("*/main.go")
        if path.is_file()
    )

    if issues:
        raise ExtractionError(issues)

    return {
        "go_version": (
            re.match(r"(\d+\.\d+)", go_version).group(1) if go_version else None
        ),
        "module": module,
        "ci_versions": ci_versions,
        "commands": commands,
        "cmd_names": cmd_names,
        "routes": routes,
        "config_env": config_env,
        "env_example": env_example,
        "migrations": migrations,
        "make_targets": collect_make_targets(root),
        "structure": collect_structure(root),
        "doc_sources": collect_doc_sources(root),
    }


def generate(root: Path | str) -> dict[str, str]:
    root = Path(root)
    data = collect_data(root)
    return {path: renderer(data) for path, renderer in OUTPUTS.items()}


def write_outputs(root: Path | str, contents: dict[str, str]) -> None:
    root = Path(root)
    for rel in sorted(contents):
        target = root / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(contents[rel], encoding="utf-8", newline="\n")


def _print_issues(issues: list[common.ExtractionIssue]) -> None:
    for issue in issues:
        print(
            f"{issue.code} {issue.file}:{issue.line}: {issue.message}",
            file=sys.stderr,
        )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="生成文档参考与 AI 工具指针")
    parser.add_argument("--root", help="仓库根目录（默认向上查找）")
    parser.add_argument("--check", action="store_true", help="只比较，不写盘")
    parser.add_argument("--stdout", metavar="PATH", help="把某个生成物打到 stdout")
    args = parser.parse_args(argv)

    try:
        root = (
            Path(args.root).resolve()
            if args.root
            else common.find_repo_root(Path(__file__).resolve().parent)
        )
    except FileNotFoundError as exc:
        print(f"gen_docs: {exc}", file=sys.stderr)
        return 2

    if not (root / "go.mod").is_file() or not (root / "docs").is_dir():
        print(f"gen_docs: {root} 不是有效仓库根（需要 go.mod + docs/）", file=sys.stderr)
        return 2

    try:
        contents = generate(root)
    except ExtractionError as exc:
        _print_issues(exc.issues)
        print("gen_docs: 抽取失败，未写入任何文件", file=sys.stderr)
        return 2

    if args.stdout:
        if args.stdout not in contents:
            print(
                f"gen_docs: 未注册的生成物：{args.stdout}（可选：{', '.join(sorted(contents))}）",
                file=sys.stderr,
            )
            return 2
        sys.stdout.write(contents[args.stdout])
        return 0

    if args.check:
        drift: list[str] = []
        diff_lines = 0
        for rel in sorted(contents):
            target = root / rel
            current = (
                target.read_text(encoding="utf-8") if target.is_file() else None
            )
            if current == contents[rel]:
                continue
            drift.append(rel)
            if diff_lines < 200:
                diff = difflib.unified_diff(
                    (current or "").splitlines(),
                    contents[rel].splitlines(),
                    fromfile=f"{rel} (current)",
                    tofile=f"{rel} (generated)",
                    lineterm="",
                )
                for line in diff:
                    if diff_lines >= 200:
                        break
                    print(line)
                    diff_lines += 1
        if drift:
            print(
                f"gen_docs: {len(drift)} 个生成物不一致：{', '.join(drift)}",
                file=sys.stderr,
            )
            print("运行 make docs-gen 刷新生成物", file=sys.stderr)
            return 1
        print("gen_docs: 生成物一致")
        return 0

    write_outputs(root, contents)
    print(f"gen_docs: 已写入 {len(contents)} 个生成物")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
