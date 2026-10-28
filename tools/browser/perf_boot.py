#!/usr/bin/env python3
"""首屏冷/热节流性能采集（批次5 起复用）。

浏览器 390/1280、1.6Mbps/150ms、CPU×4，冷启动 N 次 + 热启动 N 次，输出 JSON：
FCP/LCP、LCP 元素、最长/累计长任务、CLS、传输字节、请求数、DOM 节点、`yxq-mount` 时长。

冷启动：每次新建 context，并关闭 HTTP 缓存；热启动：同一 context 连刷 N 次（静态资源
immutable 命中缓存，壳每次 304/200）。只用于已部署 staging 或本地实例，不放进 make ci。

    BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
    python3 tools/browser/perf_boot.py --out /tmp/perf-b5.json

依赖 playwright；测量口径是 PerformanceResourceTiming.transferSize（冷启动含响应头，
热启动缓存命中为 0）与 PerformanceObserver(largest-contentful-paint/longtask/layout-shift)。
"""

from __future__ import annotations

import argparse
import json
import os
import statistics
import time
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright
except ImportError:  # pragma: no cover - environment guard
    sys.exit("需要 playwright：pip install playwright && python3 -m playwright install chromium")

INIT = """
window.__yxq_perf = {fcp: 0, lcp: 0, lcpEl: '', lcpClass: '', longtasks: [], cls: 0, shifts: []};
try {
  new PerformanceObserver(function (l) {
    for (const e of l.getEntries()) {
      if (e.name === 'first-contentful-paint') window.__yxq_perf.fcp = e.startTime;
    }
  }).observe({type: 'paint', buffered: true});
} catch (e) {}
try {
  new PerformanceObserver(function (l) {
    for (const e of l.getEntries()) {
      window.__yxq_perf.lcp = e.startTime;
      window.__yxq_perf.lcpEl = (e.element && (e.element.currentSrc || e.element.src)) || e.url || '';
      window.__yxq_perf.lcpClass = (e.element && typeof e.element.className === 'string') ? e.element.className : '';
    }
  }).observe({type: 'largest-contentful-paint', buffered: true});
} catch (e) {}
try {
  new PerformanceObserver(function (l) {
    for (const e of l.getEntries()) {
      window.__yxq_perf.longtasks.push({start: e.startTime, dur: e.duration});
    }
  }).observe({type: 'longtask', buffered: true});
} catch (e) {}
try {
  new PerformanceObserver(function (l) {
    for (const e of l.getEntries()) {
      if (e.hadRecentInput) continue;
      window.__yxq_perf.cls += e.value;
      window.__yxq_perf.shifts.push({start: e.startTime, value: e.value});
    }
  }).observe({type: 'layout-shift', buffered: true});
} catch (e) {}
"""


def median(values: list[float]):
    return round(statistics.median(values), 1) if values else None


def collect(page) -> dict:
    return page.evaluate(
        """() => {
          const p = window.__yxq_perf;
          const res = performance.getEntriesByType('resource');
          const nav = performance.getEntriesByType('navigation')[0];
          const mount = performance.getEntriesByName('yxq-mount')[0];
          const boot = performance.getEntriesByName('yxq-boot')[0];
          const appStart = performance.getEntriesByName('yxq-app-start')[0];
          const heroReady = performance.getEntriesByName('yxq-hero-ready')[0];
          let transfer = 0, encoded = 0;
          for (const r of res) { transfer += r.transferSize || 0; encoded += r.encodedBodySize || 0; }
          const scripts = res.filter(r => (r.name || '').indexOf('.js') >= 0 || (r.name || '').indexOf('/app') >= 0)
            .map(r => ({url: r.name.split('/').pop().split('?')[0], start: Math.round(r.startTime),
                        end: Math.round(r.responseEnd), transfer: r.transferSize || 0, initiator: r.initiatorType || ''}));
          const photos = res.filter(r => (r.name || '').indexOf('/cards/photo/') >= 0)
            .map(r => ({url: r.name.split('?')[0].split('/').pop(), start: Math.round(r.startTime),
                        ttfb: Math.round(r.responseStart), end: Math.round(r.responseEnd),
                        transfer: r.transferSize || 0, initiator: r.initiatorType || ''}));
          if (nav) { transfer += nav.transferSize || 0; encoded += nav.encodedBodySize || 0; }
          const lt = p.longtasks.slice().sort((a, b) => b.dur - a.dur);
          return {
            fcp: Math.round(p.fcp), lcp: Math.round(p.lcp), lcp_el: p.lcpEl, lcp_class: p.lcpClass,
            longtask_count: p.longtasks.length,
            longtask_total: Math.round(p.longtasks.reduce((s, e) => s + e.dur, 0)),
            longest_task: Math.round(lt.length ? lt[0].dur : 0),
            longest_task_start: Math.round(lt.length ? lt[0].start : 0),
            longtasks: lt.slice(0, 5).map(e => ({start: Math.round(e.start), dur: Math.round(e.dur)})),
            cls: Math.round(p.cls * 10000) / 10000,
            shifts: p.shifts.slice(0, 5).map(e => ({start: Math.round(e.start), value: e.value})),
            bytes: transfer, encoded: encoded,
            requests: res.length + 1,
            photos: photos,
            scripts: scripts,
            mount_ms: mount ? Math.round(mount.duration) : null,
            boot_ms: boot ? Math.round(boot.duration) : null,
            app_start_ms: window.__YXQ_APP_START_AT__ || (appStart ? Math.round(appStart.startTime) : null),
            app_start_reason: window.__YXQ_APP_START_REASON__ || '',
            hero_ready_ms: window.__YXQ_HERO_READY_AT__ || (heroReady ? Math.round(heroReady.startTime) : null),
            home_done_ms: window.__YXQ_LOAD_HOME_DONE_AT__ || null,
            boot_card_removed_ms: (window.__YXQ_BOOT_CARD__ && window.__YXQ_BOOT_CARD__.removedAt && window.__YXQ_BOOT_CARD__.removedAt()) || null,
            dom: document.querySelectorAll('*').length,
            cards: document.querySelectorAll('.swipe-card').length,
            hero_present: !!document.getElementById('boot-card'),
            counter: (document.querySelector('.card-counter') || {}).textContent || '',
            url: location.href
          };
        }"""
    )


def run_once(context, page, base_url: str, suffix: str = "", reload: bool = False) -> dict:
    if reload:
        page.reload(wait_until="domcontentloaded", timeout=60000)
    else:
        page.goto(base_url.rstrip("/") + "/app" + suffix, wait_until="domcontentloaded", timeout=60000)
    page.wait_for_selector(".van-tabbar", timeout=30000)
    page.wait_for_selector("#boot-card", state="detached", timeout=20000)
    page.wait_for_timeout(1200)  # LCP/长任务收尾
    return collect(page)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="/tmp/perf_boot.json")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--width", type=int, default=0)
    ap.add_argument("--height", type=int, default=0)
    ap.add_argument("--latency", type=float, default=150)
    ap.add_argument("--down-mbps", type=float, default=1.6)
    ap.add_argument("--cpu", type=float, default=4)
    args = ap.parse_args()

    base_url = os.environ.get("BASE_URL", "").strip().rstrip("/")
    cookie = os.environ.get("SESSION_COOKIE", "").strip()
    if not base_url or not cookie:
        sys.exit("缺少 BASE_URL 或 SESSION_COOKIE")
    viewports = [(args.width, args.height)] if args.width else [(390, 844), (1280, 900)]
    down = int(args.down_mbps * 1024 * 1024 / 8)

    result: dict[str, dict] = {}
    with sync_playwright() as pw:
        browser = pw.chromium.launch(args=["--no-sandbox"])
        for width, height in viewports:
            tag = f"{width}x{height}"
            result[tag] = {}
            for mode in ("cold", "warm"):
                runs = []
                # 冷启动每次一个全新 context；热启动复用同一 context，
                # 先做一次预热（不计入），再连刷 N 次。
                context = None
                page = None
                for i in range(args.runs):
                    if mode == "cold" or context is None:
                        if context is not None:
                            context.close()
                        context = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
                        context.add_cookies([{
                            "name": "yxq_session", "value": cookie,
                            "url": base_url, "httpOnly": True, "sameSite": "Lax",
                        }])
                        page = context.new_page()
                        page.add_init_script(INIT)
                        cdp = context.new_cdp_session(page)
                        cdp.send("Network.enable")
                        cdp.send("Network.setCacheDisabled", {"cacheDisabled": mode == "cold"})
                        cdp.send("Emulation.setCPUThrottlingRate", {"rate": args.cpu})
                        cdp.send("Network.emulateNetworkConditions", {
                            "offline": False, "latency": args.latency,
                            "downloadThroughput": down, "uploadThroughput": int(down / 2),
                        })
                        if mode == "warm":
                            # 热启动 = 回访刷新：首载后 reload 复用 immutable 静态缓存。
                            run_once(context, page, base_url, "?warmup=1")
                            run_once(context, page, base_url, reload=True)
                    suffix = f"?cold={i}-{int(time.time() * 1000)}"
                    runs.append(run_once(context, page, base_url, suffix, reload=(mode == "warm")))
                context.close()
                keys = ("fcp", "lcp", "longest_task", "longtask_total", "bytes", "requests",
                        "mount_ms", "boot_ms", "app_start_ms", "hero_ready_ms",
                        "home_done_ms", "boot_card_removed_ms")
                med = {}
                for key in keys:
                    vals = [r[key] for r in runs if r.get(key) is not None]
                    med[key] = median(vals)
                med["lcp_el"] = runs[-1].get("lcp_el", "")
                med["lcp_class"] = runs[-1].get("lcp_class", "")
                med["app_start_reason"] = runs[-1].get("app_start_reason", "")
                med["cls"] = max((r.get("cls") or 0) for r in runs)
                med["dom"] = max((r.get("dom") or 0) for r in runs)
                med["cards"] = max((r.get("cards") or 0) for r in runs)
                result[tag][mode] = {"median": med, "runs": runs}

        browser.close()

    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")
    for tag, modes in result.items():
        print(f"== {tag}")
        for mode, data in modes.items():
            m = data["median"]
            print(
                f"  {mode}: FCP {m['fcp']}ms LCP {m['lcp']}ms longest {m['longest_task']}ms "
                f"mount {m['mount_ms']}ms boot {m['boot_ms']}ms "
                f"app_start {m['app_start_ms']}ms hero_ready {m['hero_ready_ms']}ms "
                f"home_done {m['home_done_ms']}ms card_removed {m['boot_card_removed_ms']}ms "
                f"({m.get('app_start_reason', '')}) "
                f"bytes {m['bytes']} req {m['requests']} "
                f"CLS {m['cls']} DOM {m['dom']} cards {m['cards']} | {m['lcp_el']}"
            )
    print(f"JSON: {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
