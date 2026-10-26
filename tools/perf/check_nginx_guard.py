#!/usr/bin/env python3
"""nginx 性能/安全护栏静态断言（只读 deploy/nginx/**，不需要 nginx）。

每个断言对应 AGENTS.md「性能护栏」的“禁止移除清单”：
  * 跨洋 upstream keepalive / HTTP 1.1 / `Connection $yxq_conn` / TLS session reuse；
  * `/static/` 边缘 proxy_cache，key 含 gzip/identity 变体 `$yxq_static_enc`；
  * gzip_types 必须含 text/javascript；TLS ticket / HSTS；
  * SSE 唯一允许 `proxy_buffering off` + `proxy_cache off`；
  * server1 upstream keepalive + `proxy_max_temp_file_size 0`；
  * **不存在 `/legacy/api` 边缘缓存**，任何 proxy_cache_key 不得含 `$cookie_`
    （明文 session 落盘）。

失败 exit 1；`--self-test` 用临时副本做删除/篡改突变，确认每条断言真能变红。
"""

from __future__ import annotations

import argparse
import dataclasses
import shutil
import sys
import tempfile
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parents[2]
SSE_PATH = "/legacy/api/notifications/stream"
STATIC_PATH = "/static/"
EDGE_FILE = "deploy/nginx/relay-next.conf"
ORIGIN_FILE = "deploy/nginx/next.nantou.love.conf"
SNIPPET_FILE = "deploy/nginx/snippets/relay-next-proxy.conf"


class GuardError(RuntimeError):
    pass


@dataclasses.dataclass
class Directive:
    name: str
    args: list[str]
    children: list["Directive"]
    file: str
    line: int


@dataclasses.dataclass
class Token:
    value: str
    line: int


def tokenize(text: str) -> list[Token]:
    tokens: list[Token] = []
    i, line = 0, 1
    while i < len(text):
        ch = text[i]
        if ch == "\n":
            line += 1
            i += 1
            continue
        if ch.isspace():
            i += 1
            continue
        if ch == "#":
            while i < len(text) and text[i] != "\n":
                i += 1
            continue
        if ch in "{};":
            tokens.append(Token(ch, line))
            i += 1
            continue
        if ch == '"':
            i += 1
            start_line = line
            buf: list[str] = []
            while i < len(text):
                c = text[i]
                if c == "\\" and i + 1 < len(text):
                    buf.append(text[i : i + 2])
                    i += 2
                    continue
                if c == '"':
                    i += 1
                    break
                if c == "\n":
                    line += 1
                buf.append(c)
                i += 1
            tokens.append(Token("".join(buf), start_line))
            continue
        start = i
        while i < len(text) and not text[i].isspace() and text[i] not in '{};#"':
            i += 1
        tokens.append(Token(text[start:i], line))
    return tokens


def parse_file(path: Path, root: Path) -> list[Directive]:
    tokens = tokenize(path.read_text(encoding="utf-8"))
    display = str(path.relative_to(root))
    root_nodes: list[Directive] = []
    stack: list[Directive] = []
    pending: list[str] = []
    pending_line = 0
    for token in tokens:
        if token.value == "{":
            if not pending:
                raise GuardError(f"{display}:{token.line}: 左花括号前没有指令名")
            node = Directive(pending[0], pending[1:], [], display, pending_line)
            (stack[-1].children if stack else root_nodes).append(node)
            stack.append(node)
            pending = []
            continue
        if token.value == ";":
            if pending:
                node = Directive(pending[0], pending[1:], [], display, pending_line)
                (stack[-1].children if stack else root_nodes).append(node)
                pending = []
            continue
        if token.value == "}":
            if pending:
                raise GuardError(f"{display}:{token.line}: 右花括号前有多余 token")
            if not stack:
                raise GuardError(f"{display}:{token.line}: 多余的右花括号")
            stack.pop()
            continue
        if not pending:
            pending_line = token.line
        pending.append(token.value)
    if stack or pending:
        raise GuardError(f"{display}: 配置块未闭合")
    return root_nodes


def parse_tree(root: Path) -> dict[str, list[Directive]]:
    nginx = root / "deploy/nginx"
    if not nginx.is_dir():
        raise GuardError("缺少 deploy/nginx 目录")
    return {
        str(path.relative_to(root)): parse_file(path, root)
        for path in sorted(nginx.rglob("*")) if path.is_file()
    }


def walk(nodes: list[Directive]):
    for node in nodes:
        yield node
        yield from walk(node.children)


def directives(node: Directive, name: str) -> list[Directive]:
    return [child for child in node.children if child.name == name]


def one(node: Directive, name: str) -> Directive | None:
    found = directives(node, name)
    return found[0] if found else None


def require(node: Directive | None, what: str) -> Directive:
    if node is None:
        raise GuardError(f"缺少 {what}")
    return node


def arg(node: Directive, what: str) -> str:
    if not node.args:
        raise GuardError(f"{what} 没有参数")
    return node.args[0]


def find_block(nodes: list[Directive], name: str, needle: str) -> Directive:
    for node in walk(nodes):
        if node.name == name and any(needle in a for a in node.args):
            return node
    raise GuardError(f"缺少 {name} ... {needle}")


def find_upstream(nodes: list[Directive], name: str) -> Directive:
    for node in walk(nodes):
        if node.name == "upstream" and node.args and node.args[0] == name:
            return node
    raise GuardError(f"缺少 upstream {name}")


def find_server(nodes: list[Directive], name: str) -> Directive:
    for node in walk(nodes):
        if node.name != "server":
            continue
        server_name = one(node, "server_name")
        listens = directives(node, "listen")
        if server_name and name in server_name.args and any("443" in " ".join(l.args) for l in listens):
            return node
    raise GuardError(f"缺少 {name} 的 443 server 块")


def locations(server: Directive) -> list[Directive]:
    return [node for node in server.children if node.name == "location"]


def location_path(node: Directive) -> str:
    return node.args[-1] if node.args else ""


def find_location(server: Directive, path: str) -> Directive:
    return find_location_in(server.children, path)


def find_location_in(nodes: list[Directive], path: str) -> Directive:
    for node in nodes:
        if node.name == "location" and location_path(node) == path:
            return node
    raise GuardError(f"缺少 location {path}")


def require_header(node: Directive, header: str, *expected: str) -> None:
    found = [
        d for d in directives(node, "proxy_set_header")
        if d.args and d.args[0] == header
    ]
    if not found:
        raise GuardError(f"{node.name} 缺少 proxy_set_header {header}")
    text = " ".join(found[0].args[1:])
    for item in expected:
        if item not in text:
            raise GuardError(f"{node.name} 的 proxy_set_header {header} 缺少 {item!r}（实际：{text}）")


def require_empty_header(node: Directive, header: str) -> None:
    found = [
        d for d in directives(node, "proxy_set_header")
        if d.args and d.args[0] == header
    ]
    if not found or found[0].args != [header, ""]:
        actual = found[0].args if found else "（缺失）"
        raise GuardError(f"{node.name} 的 proxy_set_header {header} 必须为空（实际：{actual}）")


def require_direct(node: Directive, name: str, *expected: str) -> Directive:
    found = require(one(node, name), f"{node.name} 里的 {name}")
    text = " ".join(found.args)
    for item in expected:
        if item not in text:
            raise GuardError(f"{node.name} 的 {name} 缺少 {item!r}（实际：{text}）")
    return found


def expect_keepalive(upstream: Directive, expected: int) -> None:
    node = require(one(upstream, "keepalive"), f"{upstream.args[0]} 的 keepalive")
    try:
        value = int(arg(node, "keepalive"))
    except ValueError as exc:
        raise GuardError(f"{upstream.args[0]} keepalive 不是整数") from exc
    if value != expected:
        raise GuardError(f"{upstream.args[0]} keepalive={value}，必须精确为 {expected}")
    require(one(upstream, "keepalive_timeout"), f"{upstream.args[0]} 的 keepalive_timeout")


def expect_tls(server: Directive) -> None:
    require_direct(server, "ssl_session_tickets", "on")
    cache = require(one(server, "ssl_session_cache"), "ssl_session_cache")
    if not cache.args or any(arg.lower() == "none" for arg in cache.args):
        raise GuardError("ssl_session_cache 不得为 none")


def expect_proxy_ssl_reuse(server: Directive) -> None:
    require_direct(server, "proxy_ssl_session_reuse", "on")


def expect_hsts(server: Directive) -> None:
    found = [d for d in directives(server, "add_header") if d.args and d.args[0] == "Strict-Transport-Security"]
    if not found:
        raise GuardError("缺少 HSTS add_header")
    if not any("max-age=" in " ".join(d.args) for d in found):
        raise GuardError("HSTS 缺少 max-age")


def expect_no_buffering_outside_sse(tree: dict[str, list[Directive]]) -> None:
    for file, nodes in tree.items():
        for node in nodes:
            if node.name == "proxy_buffering" and node.args and node.args[0] == "off":
                raise GuardError(f"{file}:{node.line}: http 根级关闭 proxy_buffering 会波及非 SSE 请求")
        for node in walk(nodes):
            if node.name == "server":
                inherited = one(node, "proxy_buffering")
                if inherited and inherited.args and inherited.args[0] == "off":
                    raise GuardError(f"{file}:{inherited.line}: server 级关闭 proxy_buffering 会波及非 SSE 请求")
            if node.name != "location":
                continue
            if location_path(node) == SSE_PATH:
                continue
            buffering = one(node, "proxy_buffering")
            if buffering and buffering.args and buffering.args[0] == "off":
                raise GuardError(f"{file}:{buffering.line}: {location_path(node) or '/'} 不应关闭 proxy_buffering（只有 SSE 例外）")


def expect_no_api_cache(tree: dict[str, list[Directive]]) -> None:
    for file, nodes in tree.items():
        # http 上下文根节点：一行 proxy_cache 就会被所有 location 继承。
        for node in nodes:
            if node.name == "proxy_cache":
                raise GuardError(f"{file}:{node.line}: http 根级不得出现 proxy_cache")
        for node in walk(nodes):
            if node.name == "proxy_cache_key" and "$cookie_" in " ".join(node.args):
                raise GuardError(f"{file}:{node.line}: proxy_cache_key 含 $cookie_，会把明文 session 写入磁盘")
            if node.name == "server" and one(node, "proxy_cache") is not None:
                raise GuardError(f"{file}:{node.line}: server 级不得出现 proxy_cache（会波及 /legacy/api）")
            if node.name == "proxy_ssl_session_reuse" and node.args and node.args[0] == "off":
                raise GuardError(f"{file}:{node.line}: proxy_ssl_session_reuse 不得为 off")
            if node.name != "location":
                continue
            path = location_path(node)
            cache = one(node, "proxy_cache")
            if cache is None:
                continue
            zone = cache.args[0] if cache.args else ""
            if path == STATIC_PATH and zone == "yxqstatic":
                continue
            if path == SSE_PATH and zone == "off":
                continue
            raise GuardError(f"{file}:{cache.line}: proxy_cache 只允许 {STATIC_PATH}=yxqstatic 或 SSE=off，发现 {path or '/'}={zone}")


def validate(root: Path) -> list[tuple[str, callable]]:
    tree = parse_tree(root)
    checks: list[tuple[str, callable]] = []

    def add(label: str, fn) -> None:
        checks.append((label, fn))

    relay = tree.get(EDGE_FILE)
    origin = tree.get(ORIGIN_FILE)
    snippet = tree.get(SNIPPET_FILE)
    if not relay or not origin or not snippet:
        raise GuardError("relay-next.conf / next.nantou.love.conf / snippets/relay-next-proxy.conf 缺一")
    all_nodes = relay + origin + snippet

    add("边缘 upstream keepalive", lambda: expect_keepalive(find_upstream(relay, "yxq_origin"), 64))
    add("源站 upstream keepalive", lambda: expect_keepalive(find_upstream(origin, "yxq_app"), 32))

    def conn_map() -> None:
        block = find_block(relay, "map", "$yxq_conn")
        entries = {d.name: (d.args[0] if d.args else "") for d in block.children}
        if entries.get("default") != "" or entries.get("websocket") != "upgrade":
            raise GuardError("$yxq_conn map 必须 default=\"\"、websocket=\"upgrade\"")
    add("Connection map", conn_map)

    def static_encoding_map() -> None:
        block = find_block(relay, "map", "$yxq_static_enc")
        values = [d.args[0] for d in block.children if d.args]
        if values.count("gzip") < 2 or "identity" not in values:
            raise GuardError("$yxq_static_enc 必须同时产出 gzip 与 identity")
        upstream_map = find_block(relay, "map", "$yxq_upstream_ae")
        pairs = {d.name: (d.args[0] if d.args else "") for d in upstream_map.children}
        if pairs.get("gzip") != "gzip":
            raise GuardError("$yxq_upstream_ae 必须把 gzip 映射为 gzip")
    add("gzip/identity 变体 map", static_encoding_map)

    add("静态缓存 zone", lambda: find_block(relay, "proxy_cache_path", "yxqstatic"))

    edge = find_server(relay, "next.nantou.love")
    add("边缘 TLS ticket/session cache", lambda: expect_tls(edge))
    add("边缘 proxy_ssl_session_reuse", lambda: expect_proxy_ssl_reuse(edge))
    add("边缘 HSTS", lambda: expect_hsts(edge))
    add("边缘 gzip on", lambda: require_direct(edge, "gzip", "on"))
    add("边缘 gzip_types", lambda: require_direct(edge, "gzip_types", "text/javascript"))
    add("边缘 include snippet", lambda: require_direct(edge, "include", "relay-next-proxy.conf"))

    def edge_conn() -> None:
        loc = find_location_in(snippet, "/")
        require_direct(loc, "proxy_http_version", "1.1")
        require_header(loc, "Connection", "$yxq_conn")
        require_direct(loc, "proxy_ssl_session_reuse", "on")
    add("边缘普通请求 keepalive", edge_conn)

    def static_cache() -> None:
        loc = find_location_in(snippet, STATIC_PATH)
        require_direct(loc, "proxy_http_version", "1.1")
        require_direct(loc, "proxy_cache", "yxqstatic")
        require_direct(loc, "proxy_cache_key", "$yxq_static_enc")
        require_header(loc, "Accept-Encoding", "$yxq_upstream_ae")
        require_empty_header(loc, "Connection")
        require_direct(loc, "proxy_cache_lock", "on")
        require_direct(loc, "proxy_ssl_session_reuse", "on")
        require_direct(loc, "proxy_cache_use_stale")
        require_hsts = [d for d in directives(loc, "add_header") if d.args and d.args[0] == "Strict-Transport-Security"]
        if not require_hsts:
            raise GuardError("/static/ 覆盖 add_header 后缺少 HSTS")
    add("静态边缘缓存", static_cache)

    def sse() -> None:
        loc = find_location_in(snippet, SSE_PATH)
        require_direct(loc, "proxy_buffering", "off")
        require_direct(loc, "proxy_cache", "off")
        timeout = require_direct(loc, "proxy_read_timeout")
        if not timeout.args or not timeout.args[0].rstrip("s").isdigit() or int(timeout.args[0].rstrip("s")) < 3600:
            raise GuardError("SSE proxy_read_timeout 必须 ≥3600s")
    add("SSE 唯一缓冲例外", sse)

    source = find_server(origin, "next.nantou.love")
    add("源站 TLS ticket/session cache", lambda: expect_tls(source))
    add("源站 HSTS", lambda: expect_hsts(source))

    def origin_keepalive() -> None:
        for path in ("/", SSE_PATH):
            loc = find_location(source, path)
            require_direct(loc, "proxy_http_version", "1.1")
            require_empty_header(loc, "Connection")
            require_direct(loc, "proxy_max_temp_file_size", "0")
        require_direct(find_location(source, SSE_PATH), "proxy_buffering", "off")
    add("源站 keepalive/不落临时文件", origin_keepalive)

    add("不存在 API 边缘缓存/明文 cookie key", lambda: expect_no_api_cache(tree))
    add("SSE 外不关闭 proxy_buffering", lambda: expect_no_buffering_outside_sse(tree))
    return checks


def run_checks(root: Path) -> tuple[list[str], list[tuple[str, str]]]:
    try:
        checks = validate(root)
    except GuardError as exc:
        return [], [("解析 deploy/nginx", str(exc))]
    passed: list[str] = []
    failed: list[tuple[str, str]] = []
    for label, fn in checks:
        try:
            fn()
            passed.append(label)
        except (GuardError, ValueError) as exc:
            failed.append((label, str(exc)))
    return passed, failed


def report(root: Path) -> int:
    passed, failed = run_checks(root)
    print(f"nginx 护栏检查（{root / 'deploy/nginx'}）")
    for label in passed:
        print(f"PASS  {label}")
    for label, detail in failed:
        print(f"FAIL  {label}: {detail}", file=sys.stderr)
    print(f"\n{len(passed)} passed, {len(failed)} failed")
    if failed:
        print("详见 AGENTS.md「性能护栏」；这些指令是跨洋延迟/首屏预算约束，不得静默删除。", file=sys.stderr)
        return 1
    return 0


def self_test(root: Path) -> int:
    """在临时副本上删除/篡改关键指令，确认对应断言变红。"""
    origin = root / "deploy/nginx"
    cases = [
        ("删除 upstream keepalive", EDGE_FILE, "keepalive 64;", "", "边缘 upstream keepalive"),
        ("keepalive 由 64 改 32", EDGE_FILE, "keepalive 64;", "keepalive 32;", "边缘 upstream keepalive"),
        ("删除静态 proxy_cache", SNIPPET_FILE, "proxy_cache yxqstatic;", "", "静态边缘缓存"),
        ("删除缓存 key 编码维度", SNIPPET_FILE, "proxy_cache_key \"$scheme$host$request_uri$yxq_static_enc\";", "proxy_cache_key \"$scheme$host$request_uri\";", "静态边缘缓存"),
        ("静态删 proxy_http_version", SNIPPET_FILE, 'proxy_set_header Connection "";\n\n    proxy_hide_header Strict-Transport-Security;\n    proxy_http_version 1.1;', 'proxy_set_header Connection "";\n\n    proxy_hide_header Strict-Transport-Security;', "静态边缘缓存"),
        ("删除 TLS ticket", EDGE_FILE, "ssl_session_tickets on;", "", "边缘 TLS"),
        ("ssl_session_cache 改 none", EDGE_FILE, "ssl_session_cache shared:YXQNEXT:20m;", "ssl_session_cache none;", "边缘 TLS"),
        ("proxy_ssl_session_reuse 改 off", EDGE_FILE, "proxy_ssl_session_reuse on;", "proxy_ssl_session_reuse off;", "边缘 proxy_ssl_session_reuse"),
        ("删除 gzip on", EDGE_FILE, "gzip on;", "", "gzip on"),
        ("删除 SSE 关闭缓冲", SNIPPET_FILE, "proxy_buffering off;", "", "SSE"),
        ("删除临时文件上限", ORIGIN_FILE, "proxy_max_temp_file_size 0;", "", "源站"),
        ("删除 gzip js 类型", EDGE_FILE, " text/javascript", "", "gzip_types"),
        ("删除 HSTS", EDGE_FILE, 'add_header Strict-Transport-Security "max-age=31536000" always;', "", "HSTS"),
        ("删除 Connection 复用", SNIPPET_FILE, "proxy_set_header Connection $yxq_conn;", 'proxy_set_header Connection "";', "keepalive"),
        ("http 根级 proxy_cache", EDGE_FILE, "upstream yxq_origin {", "proxy_cache yxqstatic;\nupstream yxq_origin {", "API 边缘缓存"),
        ("http 根级 proxy_buffering off", EDGE_FILE, "upstream yxq_origin {", "proxy_buffering off;\nupstream yxq_origin {", "proxy_buffering"),
        ("给 API 加边缘缓存", EDGE_FILE, "server {", 'server { location /legacy/api/ { proxy_cache yxqstatic; proxy_cache_key "$cookie_yxq_session"; } ', "API 边缘缓存"),
        ("给非 SSE 关闭缓冲", EDGE_FILE, "location / {", "location / { proxy_buffering off; ", "proxy_buffering"),
    ]
    failures = 0
    with tempfile.TemporaryDirectory(prefix="yxq-nginx-guard-") as tmp:
        for label, rel, old, new, expected in cases:
            case_root = Path(tmp) / label.replace(" ", "_")
            shutil.copytree(origin, case_root / "deploy/nginx")
            target = case_root / rel
            text = target.read_text(encoding="utf-8")
            if old not in text:
                print(f"SELFTEST ERROR {label}: 找不到待突变片段 {old!r}", file=sys.stderr)
                failures += 1
                continue
            target.write_text(text.replace(old, new, 1), encoding="utf-8")
            _, failed = run_checks(case_root)
            if any(expected in failed_label for failed_label, _ in failed):
                print(f"SELFTEST PASS {label} -> {failed[0][0]}")
            else:
                print(f"SELFTEST FAIL {label}: 期望 {expected!r} 变红，实际 {failed}", file=sys.stderr)
                failures += 1
    print(f"self-test: {len(cases) - failures}/{len(cases)} 突变被拦截")
    return 1 if failures else 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=str(DEFAULT_ROOT), help="仓库根目录")
    parser.add_argument("--self-test", action="store_true", help="运行删除/篡改突变自测")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    if args.self_test:
        return self_test(root)
    try:
        return report(root)
    except GuardError as exc:
        print(f"nginx 护栏检查失败：{exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
