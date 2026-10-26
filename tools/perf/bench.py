#!/usr/bin/env python3
"""HTTP 延迟基准（edge / origin / app，仅标准库，无第三方依赖）。

三个采集点：
  edge    在 server2（HK）上跑，测公开入口 `https://next.nantou.love`；复用连接=浏览器稳态。
  origin  在 server1（LA）上跑，TCP 连 127.0.0.1:443、SNI/Host 仍为 next.nantou.love。
  app     在 server1 上跑，直连 `http://127.0.0.1:8093`，绕过 nginx。

协议：N≥10、warmup≥3；warm 复用同一连接，cold 每样本新建连接；nearest-rank p95。
cookie 只从 `--cookie-env` 或 `--cookie-file` 读取，原样作为 Cookie 头发送，**永不打印**。
输出 JSON 不含 cookie / session；`Cookie` 头只在内存中。

示例：
  python3 tools/perf/bench.py --source edge --n 10 --warmup 3
  BASE_URL=https://next.nantou.love python3 tools/perf/bench.py --source edge \
      --cookie-file /tmp/yxq-perf/session.cookie --out /tmp/yxq-perf/bench.json
  python3 tools/perf/bench.py --source app --cookie-env YXQ_SESSION_COOKIE
"""

from __future__ import annotations

import argparse
import http.client
import json
import math
import os
import socket
import ssl
import statistics
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit

DEFAULT_UA = "yxq-perf-guardrails/1.0"
SOURCES = {
    "edge": {"base": "https://next.nantou.love", "connect_to": None},
    "origin": {"base": "https://next.nantou.love", "connect_to": "127.0.0.1"},
    "app": {"base": "http://127.0.0.1:8093", "connect_to": None},
    "custom": {"base": "", "connect_to": None},
}
DEFAULT_PATHS = [
    ("healthz", "/healthz"),
    ("app", "/app"),
    ("static", "/static/legacy/app.min.js"),
    ("api-home", "/legacy/api/home"),
    ("api-cards", "/legacy/api/cards"),
]


class BenchError(RuntimeError):
    pass


class TimedHTTPSConnection(http.client.HTTPSConnection):
    connect_host = None
    t_connect = None
    t_tls = None

    def connect(self) -> None:
        host = self.connect_host or self.host
        sock = socket.create_connection((host, self.port), self.timeout)
        self.t_connect = time.perf_counter()
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)
        self.t_tls = time.perf_counter()


class TimedHTTPConnection(http.client.HTTPConnection):
    connect_host = None
    t_connect = None
    t_tls = None

    def connect(self) -> None:
        host = self.connect_host or self.host
        self.sock = socket.create_connection((host, self.port), self.timeout)
        self.t_connect = time.perf_counter()
        self.t_tls = self.t_connect


class Target:
    def __init__(self, name: str, url: str):
        self.name = name
        self.url = url
        parts = urlsplit(url)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise BenchError(f"非法 URL：{url}")
        self.scheme = parts.scheme
        self.host = parts.hostname
        self.port = parts.port or (443 if parts.scheme == "https" else 80)
        self.path = (parts.path or "/") + (("?" + parts.query) if parts.query else "")


def new_conn(target: Target, connect_to: str | None, timeout: float):
    if target.scheme == "https":
        conn = TimedHTTPSConnection(target.host, target.port, timeout=timeout, context=ssl.create_default_context())
    else:
        conn = TimedHTTPConnection(target.host, target.port, timeout=timeout)
    if connect_to:
        conn.connect_host = connect_to
    return conn


def default_headers(target: Target) -> dict[str, str]:
    host = target.host if target.port in (80, 443) else f"{target.host}:{target.port}"
    return {
        "Host": host,
        "User-Agent": DEFAULT_UA,
        "Accept": "*/*",
        "Accept-Encoding": "gzip, deflate",
        "Connection": "keep-alive",
    }


def request_once(conn, target: Target, cookie: str | None) -> dict:
    headers = default_headers(target)
    if cookie:
        headers["Cookie"] = cookie
    reused = conn.sock is not None
    t0 = time.perf_counter()
    if conn.sock is None:
        conn.connect()
    t_req = time.perf_counter()
    try:
        conn.request("GET", target.path, headers=headers)
        resp = conn.getresponse()
        t_hdr = time.perf_counter()
        body = resp.read()
        t_end = time.perf_counter()
    except (http.client.HTTPException, OSError):
        conn.close()
        raise
    if resp.will_close:
        conn.close()
    connect_ms = tls_ms = None
    if not reused and conn.t_connect is not None:
        connect_ms = (conn.t_connect - t0) * 1000.0
        if conn.t_tls is not None:
            tls_ms = (conn.t_tls - conn.t_connect) * 1000.0
    return {
        "status": resp.status,
        "reused": reused,
        "wire_bytes": len(body),
        "content_encoding": resp.getheader("Content-Encoding") or "identity",
        "connect_ms": None if connect_ms is None else round(connect_ms, 3),
        "tls_ms": None if tls_ms is None else round(tls_ms, 3),
        "ttfb_ms": round((t_hdr - t_req) * 1000.0, 3),
        "ttfb_total_ms": round((t_hdr - t0) * 1000.0, 3),
        "total_ms": round((t_end - t0) * 1000.0, 3),
    }


def percentile(values: list[float], p: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    k = max(1, math.ceil(p / 100.0 * len(ordered)))
    return ordered[k - 1]


def summarize(values: list[float]) -> dict:
    if not values:
        return {"n": 0, "p50": None, "p95": None, "max": None, "mean": None}
    return {
        "n": len(values),
        "p50": round(percentile(values, 50) or 0, 2),
        "p95": round(percentile(values, 95) or 0, 2),
        "max": round(max(values), 2),
        "mean": round(statistics.fmean(values), 2),
    }


def run_mode(target: Target, args, cookie: str | None, mode: str) -> dict:
    conn = new_conn(target, args.connect_to, args.timeout)
    for _ in range(args.warmup):
        request_once(conn, target, cookie)
        if mode == "cold":
            conn.close()
            conn = new_conn(target, args.connect_to, args.timeout)
    samples = []
    for _ in range(args.n):
        if mode == "cold":
            conn.close()
            conn = new_conn(target, args.connect_to, args.timeout)
        try:
            samples.append(request_once(conn, target, cookie))
        except (http.client.HTTPException, OSError) as exc:
            samples.append({"status": 0, "error": f"{type(exc).__name__}: {exc}"})
            conn.close()
            conn = new_conn(target, args.connect_to, args.timeout)
    conn.close()
    ok = [s for s in samples if s.get("status") in args.allowed]
    return {
        "count": len(samples),
        "ok": len(ok),
        "errors": len(samples) - len(ok),
        "statuses": sorted({s.get("status") for s in samples}),
        "wire_bytes": ok[0]["wire_bytes"] if ok else None,
        "content_encoding": ok[0]["content_encoding"] if ok else None,
        "reused_ratio": round(sum(1 for s in ok if s.get("reused")) / len(ok), 3) if ok else None,
        "ttfb_ms": summarize([s["ttfb_ms"] for s in ok]),
        "ttfb_total_ms": summarize([s["ttfb_total_ms"] for s in ok]),
        "total_ms": summarize([s["total_ms"] for s in ok]),
        "connect_ms": summarize([s["connect_ms"] for s in ok if s.get("connect_ms") is not None]),
        "tls_ms": summarize([s["tls_ms"] for s in ok if s.get("tls_ms") is not None]),
        "samples": [
            {k: s.get(k) for k in ("status", "reused", "wire_bytes", "ttfb_ms", "ttfb_total_ms", "total_ms")}
            for s in samples
        ],
    }


def load_cookie(args) -> tuple[str | None, str | None]:
    if args.cookie_file:
        path = Path(args.cookie_file)
        try:
            lines = [line.strip() for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
        except OSError as exc:
            raise BenchError(f"读取 cookie 文件失败：{exc}") from exc
        if not lines:
            raise BenchError("cookie 文件为空")
        raw = lines[0]
        return (raw if "=" in raw else f"yxq_session={raw}"), "file"
    if args.cookie_env:
        raw = os.environ.get(args.cookie_env, "").strip()
        if not raw:
            return None, None  # 未设置：仍可测公开路径；认证路径会以 401/303 记录为失败
        return (raw if "=" in raw else f"yxq_session={raw}"), f"env:{args.cookie_env}"
    return None, None


def load_targets(args, base: str) -> list[Target]:
    specs: list[tuple[str, str]] = []
    if args.paths_file:
        path = Path(args.paths_file)
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except OSError as exc:
            raise BenchError(f"读取路径文件失败：{exc}") from exc
        for line in lines:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            sep = "\t" if "\t" in line else "="
            if sep not in line:
                raise BenchError(f"路径文件行格式应为 name<TAB>path 或 name=path：{line}")
            name, raw = line.split(sep, 1)
            specs.append((name.strip(), raw.strip()))
    elif args.paths:
        for raw in args.paths.split(","):
            raw = raw.strip()
            if not raw:
                continue
            name, path = raw.split("=", 1) if "=" in raw else (raw, raw)
            specs.append((name.strip(), path.strip()))
    if not specs:
        specs = DEFAULT_PATHS
    targets = []
    for name, raw in specs:
        url = raw if raw.startswith(("http://", "https://")) else base.rstrip("/") + raw
        targets.append(Target(name, url))
    return targets


def git_head() -> str | None:
    try:
        import subprocess

        return subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            check=True, capture_output=True, text=True, cwd=Path(__file__).resolve().parents[2],
        ).stdout.strip() or None
    except Exception:
        return None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--source", choices=sorted(SOURCES), default="edge", help="采集点（默认 edge）")
    parser.add_argument("--base", default="", help="覆盖 source 的 base URL")
    parser.add_argument("--connect-to", default="", help="把 TCP 连到指定 IP（SNI/Host 不变）")
    parser.add_argument("--paths", default="", help="逗号分隔 name=path；默认 healthz/app/static/home/cards")
    parser.add_argument("--paths-file", default="", help="name<TAB>path 或 name=path，每行一个")
    parser.add_argument("--n", type=int, default=10, help="每目标每模式样本数（≥10）")
    parser.add_argument("--warmup", type=int, default=3, help="预热请求数（不计入）")
    parser.add_argument("--mode", choices=("warm", "cold", "both"), default="warm")
    parser.add_argument("--cookie-env", default="YXQ_SESSION_COOKIE", help="含 Cookie 头或 session token 的环境变量名")
    parser.add_argument("--cookie-file", default="", help="首行原样作为 Cookie 头；内容不会打印")
    parser.add_argument("--timeout", type=float, default=20.0)
    parser.add_argument("--allow-status", default="200,304", help="允许计入统计的状态码")
    parser.add_argument("--out", default="/tmp/yxq-perf/bench.json", help="JSON 输出路径")
    args = parser.parse_args(argv)

    if args.n < 1 or args.warmup < 0:
        parser.error("--n 必须 ≥1，--warmup 必须 ≥0")
    args.allowed = {int(x) for x in args.allow_status.split(",") if x.strip()}
    base = args.base or SOURCES[args.source]["base"]
    if not base:
        parser.error("custom source 需要 --base")
    connect_to = args.connect_to or SOURCES[args.source]["connect_to"]
    args.connect_to = connect_to
    try:
        cookie, cookie_source = load_cookie(args)
        targets = load_targets(args, base)
    except BenchError as exc:
        print(f"基准失败：{exc}", file=sys.stderr)
        return 2

    modes = ("warm", "cold") if args.mode == "both" else (args.mode,)
    results = []
    for target in targets:
        row = {"name": target.name, "url": target.url, "modes": {}}
        for mode in modes:
            try:
                row["modes"][mode] = run_mode(target, args, cookie, mode)
            except (http.client.HTTPException, OSError) as exc:
                row["modes"][mode] = {"count": 0, "ok": 0, "errors": 1, "statuses": [0], "error": str(exc)}
        results.append(row)

    payload = {
        "metadata": {
            "source": args.source,
            "base": base,
            "connect_to": connect_to or None,
            "host": socket.gethostname(),
            "git_head": git_head(),
            "started_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "n": args.n,
            "warmup": args.warmup,
            "mode": args.mode,
            "cookie_source": cookie_source,
            "user_agent": DEFAULT_UA,
            "allowed_status": sorted(args.allowed),
        },
        "results": results,
    }
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

    print(f"source={args.source} base={base} host={socket.gethostname()} n={args.n} warmup={args.warmup} mode={args.mode}")
    print(f"{'目标':<14}{'模式':<7}{'OK':>4}{'ERR':>4}  {'TTFB p50':>9}{'p95':>8}  {'总时长 p50':>10}{'p95':>8}  {'字节':>8}")
    failed = False
    for row in results:
        for mode, data in row["modes"].items():
            ttfb = data.get("ttfb_total_ms") or {}
            total = data.get("total_ms") or {}
            if data.get("errors"):
                failed = True
                statuses = data.get("statuses")
                print(f"  FAIL {row['name']} {mode}: statuses={statuses} errors={data['errors']}", file=sys.stderr)
            print(
                f"{row['name']:<14}{mode:<7}{data.get('ok', 0):>4}{data.get('errors', 0):>4}  "
                f"{(ttfb.get('p50') or 0):>9.1f}{(ttfb.get('p95') or 0):>8.1f}  "
                f"{(total.get('p50') or 0):>10.1f}{(total.get('p95') or 0):>8.1f}  "
                f"{(data.get('wire_bytes') or 0):>8}"
            )
    print(f"JSON: {out}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
