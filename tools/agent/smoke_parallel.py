#!/usr/bin/env python3
"""Run the browser smoke script once per viewport, in parallel.

The smoke script is Playwright-based and single-viewport. This wrapper keeps
its assertions untouched while cutting wall time by running 390x844 and
390x700 (and optionally 1280x900) concurrently.

Examples:
    SUITE=fast BASE_URL=https://next.nantou.love \
      SESSION_COOKIE_FILE=/tmp/yxq-smoke-cookie \
      python3 tools/agent/smoke_parallel.py --suite fast

    python3 tools/agent/smoke_parallel.py --suite full --viewports 390x844,390x700,1280x900
"""
from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEFAULT_SCRIPT = HERE.parent / "browser" / "user_reported_bugs_20261027.py"
PRINT_LOCK = threading.Lock()


def log(msg: str) -> None:
    with PRINT_LOCK:
        print(msg, flush=True)


def parse_viewports(raw: str) -> list[tuple[str, int, int]]:
    out: list[tuple[str, int, int]] = []
    for part in raw.split(","):
        part = part.strip()
        if not part:
            continue
        try:
            w, h = part.lower().split("x", 1)
            out.append((f"{int(w)}x{int(h)}", int(w), int(h)))
        except ValueError:
            raise SystemExit(f"invalid viewport: {part!r}")
    if not out:
        raise SystemExit("no viewports")
    return out


def read_cookie(args: argparse.Namespace) -> str:
    if args.cookie:
        return args.cookie
    if args.cookie_file:
        try:
            return Path(args.cookie_file).read_text(encoding="utf-8").strip()
        except OSError as exc:
            raise SystemExit(f"cannot read cookie file {args.cookie_file}: {exc}")
    cookie = os.environ.get("SESSION_COOKIE", "").strip()
    if cookie:
        return cookie
    raise SystemExit("SESSION_COOKIE or --cookie-file is required")


def redact(cmd: list[str]) -> str:
    return " ".join("<cookie>" if "yxq_session=" in part else part for part in cmd)


def suite_for(tag: str, suite: str) -> str:
    """Wide/short viewports only need the layout suite; full checks are mobile-first."""
    if suite in {"fast", "full"} and tag != "390x844":
        return "layout"
    return suite


def run_one(
    tag: str,
    width: int,
    height: int,
    args: argparse.Namespace,
    cookie: str,
    extra_env: dict[str, str],
    suite: str,
) -> tuple[str, int, float]:
    env = os.environ.copy()
    env.update(
        {
            "VIEWPORTS": tag,
            "SUITE": suite,
            "BASE_URL": args.base_url,
            "SESSION_COOKIE": cookie,
        }
    )
    if args.local_assets:
        env["LOCAL_ASSETS_DIR"] = str(Path(args.local_assets).resolve())
    env.update(extra_env)

    cmd = [sys.executable, str(Path(args.script).resolve())]
    started = time.monotonic()
    log(f"[{tag}] start suite={suite}: {redact(cmd)}")
    proc = subprocess.Popen(
        cmd,
        cwd=str(HERE.parent.parent),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
        start_new_session=True,
    )
    assert proc.stdout is not None
    tail: list[str] = []

    def pump() -> None:
        assert proc.stdout is not None
        for line in proc.stdout:
            line = line.rstrip()
            tail.append(line)
            if len(tail) > 40:
                tail.pop(0)
            log(f"[{tag}] {line}")

    reader = threading.Thread(target=pump, daemon=True)
    reader.start()
    try:
        rc = proc.wait(timeout=args.timeout)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except ProcessLookupError:
            proc.kill()
        rc = 124
        log(f"[{tag}] TIMEOUT after {args.timeout}s")
    reader.join(timeout=5)
    elapsed = time.monotonic() - started
    if rc != 0:
        log(f"[{tag}] FAIL rc={rc} in {elapsed:.1f}s")
        log(f"[{tag}] last output:\n" + "\n".join(tail[-20:]))
    else:
        log(f"[{tag}] PASS in {elapsed:.1f}s")
    return tag, rc, elapsed


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--script", default=str(DEFAULT_SCRIPT))
    parser.add_argument("--suite", default=os.environ.get("SUITE", "fast"))
    parser.add_argument("--viewports", default=os.environ.get("VIEWPORTS", ""))
    parser.add_argument("--workers", type=int, default=int(os.environ.get("SMOKE_WORKERS", "3")))
    parser.add_argument("--base-url", default=os.environ.get("BASE_URL", ""))
    parser.add_argument("--cookie", default="")
    parser.add_argument("--cookie-file", default=os.environ.get("SESSION_COOKIE_FILE", ""))
    parser.add_argument("--local-assets", default=os.environ.get("LOCAL_ASSETS_DIR", ""))
    parser.add_argument("--timeout", type=int, default=int(os.environ.get("SMOKE_TIMEOUT", "900")))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    if args.suite == "full":
        # 1280 桌面不在本批回归面；full 也只跑 mobile 两档。
        default_viewports = "390x844,390x700"
    elif args.suite in {"history", "cards"}:
        default_viewports = "390x844"
    else:
        default_viewports = "390x844,390x700"
    viewports = parse_viewports(args.viewports or default_viewports)

    if not args.base_url:
        raise SystemExit("BASE_URL or --base-url is required")

    cookie = read_cookie(args)

    extra_env: dict[str, str] = {}
    if os.environ.get("SKIP_PAGINATION"):
        extra_env["SKIP_PAGINATION"] = os.environ["SKIP_PAGINATION"]

    if args.dry_run:
        for tag, _, _ in viewports:
            print(f"[{tag}] would run {args.script} suite={suite_for(tag, args.suite)} base={args.base_url}")
        return 0

    log(
        f"smoke: suite={args.suite} viewports={','.join(v[0] for v in viewports)} "
        f"workers={args.workers} base={args.base_url}"
    )
    results: list[tuple[str, int, float]] = []
    with ThreadPoolExecutor(max_workers=max(1, min(args.workers, len(viewports)))) as pool:
        futures = [
            pool.submit(run_one, tag, w, h, args, cookie, extra_env, suite_for(tag, args.suite))
            for tag, w, h in viewports
        ]
        for fut in futures:
            results.append(fut.result())

    bad = [r for r in results if r[1] != 0]
    log("\n== smoke summary ==")
    for tag, rc, elapsed in sorted(results):
        log(f"  {tag}: {'PASS' if rc == 0 else 'FAIL'} ({elapsed:.1f}s)")
    return 1 if bad else 0


if __name__ == "__main__":
    raise SystemExit(main())
