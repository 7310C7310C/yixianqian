# 性能基准协议（v2 护栏版）

> 目的：让「首载静态预算」和「HTTP/LCP 延迟」可复现、可比较。不同主机的 CPU 单核性能
> 会直接改变浏览器结果，因此每次报告必须并列记录主机、采集点、N、warmup 与时间。

## 1. 采集点与参考主机

| 角色 | 主机 | 说明 |
|---|---|---|
| edge | server2（HK，`next.nantou.love`） | 公开入口；nginx relay + `/static` 边缘缓存；建议在 server2 本机跑；server2 无仓库，需把 `bench.py` scp 到 `/tmp`，或从本机用 `--connect-to` |
| origin | server1（LA，172.245.223.118） | 源站 nginx；`bench.py --source origin` 连 `127.0.0.1:443`，SNI/Host 仍为公网域名 |
| app | server1（LA，127.0.0.1:8093） | 绕过 nginx，只看 Go/DB/媒体 |
| browser reference | 本地开发机（Xeon E5-2680 v4，2 vCPU / 2.4GB） | 浏览器 LCP 的参考口径；staging QEMU 主机只做同机前后对比 |

主机差异（尤其 QEMU vCPU、CPU 节流）会让绝对值不可直接横比；**判定优先用同机同口径前后对比**。

## 2. 静态预算（确定性，无网络）

`make perf-budget` 读取仓库文件，计算：
shell（index.html + 内联 critical CSS，gzip level 6 估算 + `bootstrap_allowance_bytes`）、
`app.min.js`、`app.render.js`、Vant 子集 JS/CSS、critical CSS、5 个 page chunk 合计、
首载静态估算（不含 API/用户照片）。基线/预算见 `budgets.json`，超限 exit 1。

- 静态文件基线 = `origin/main@6b1fb23` 实测；shell 基线用 batch10 `staging-0f14252` 的 `/app` 实测 27,978B 校准（静态壳 24,669B + bootstrap 余量 3,309B）；预算 = 基线 ×1.10（向上取整）。
- 修改静态源文件后必须 `make static-gzip` 并 bump `assetVersion`，否则 `static-gzip-check` 会红。
- 预算只能手工修改 `budgets.json` 并走 Review；没有自动 `--update`，CI 也不运行 update。

## 3. HTTP 延迟（bench.py）

```sh
# edge（server2）
python3 tools/perf/bench.py --source edge --n 10 --warmup 3 --mode both \
  --cookie-env YXQ_SESSION_COOKIE --out /tmp/yxq-perf/edge.json

# origin / app（server1）
python3 tools/perf/bench.py --source origin --n 10 --warmup 3 --mode warm \
  --cookie-env YXQ_SESSION_COOKIE --out /tmp/yxq-perf/origin.json
python3 tools/perf/bench.py --source app --n 10 --warmup 3 --mode warm \
  --cookie-file /tmp/yxq-perf/session.cookie --out /tmp/yxq-perf/app.json
```

- **N≥10、warmup≥3**；`warm` 复用同一连接（浏览器稳态），`cold` 每样本新建连接（含 TCP/TLS）。
- 百分位 nearest-rank：`p95 = 排序后第 ceil(0.95n) 个`；n<10 的 p95 只作参考。
- 默认路径：`/healthz`、`/app`、`/static/legacy/app.min.js`、`/legacy/api/home`、`/legacy/api/cards`。
  认证路径需要有效 session；无 cookie 时 401/303 会计为错误（exit 1），不会静默混入统计。
- 记录 `status / reused / wire_bytes / ttfb_ms / ttfb_total_ms / total_ms`；JSON 含主机、git HEAD、
  source、N、warmup、时间，**不含 cookie**。
- 跑前确认边缘静态资源已 HIT（同一路径请求 2 次）；跨洋 TLS/缓存冷启动不要把首连当稳态。

## 4. 浏览器 LCP（lcp.py）

```sh
BASE_URL=https://next.nantou.love SESSION_COOKIE=... \
  python3 tools/perf/lcp.py --runs 3 --out /tmp/yxq-perf/lcp.json
```

- 固定 `390×844`、`CPU×4`、`1.6Mbps / 150ms`；冷启动每次新 context + 关缓存，热启动同 context 预热后连刷。
- 复用 `tools/browser/perf_boot.py`：FCP/LCP/最长任务/CLS/传输字节/请求数/`yxq-*` 标记。
- **N≥3（正式对比建议 5）**，取中位；同时记录 min–max。缓存/边缘未热时先预热再采。

## 5. 判定

| 场景 | 参考目标 |
|---|---|
| 静态预算 | 每项 ≤ `budgets.json`（基线 +10%）；超限即红 |
| 边缘静态 HIT | p95 ≤50ms（缓存未热时先预热） |
| 边缘 API | p95 ≤220ms（10s 读缓存 + keepalive 口径） |
| 同机回归 | 改后 p95 比改前基线差 ≤10%；超过需根因解释或回退 |
| 浏览器（参考主机） | 冷 LCP 中位 ≤2.5s、热 LCP 中位 ≤1.8s；p95 允许 +15% 或绝对值达标 |

绝对目标以 `docs/product-spec.md` / `docs/STATUS.md` 的 PM 验收口径为准；本协议不修改验收标准，
只定义“怎么量、怎么比”。

## 6. 安全与产物

- cookie 只从 `--cookie-env` / `--cookie-file` / `SESSION_COOKIE` 读取；不打印、不进 JSON、不进 commit。
- 临时 session 文件目录 `0700`、文件 `0600`，用完删除；报告只写 `cookie_source`（如 `env:...`）。
- 原始 JSON 可放 `/tmp/yxq-perf/` 或任务证据目录；不要把 session/token 写进 `.team/` 报告。
- 报告至少含：主机、source、N、warmup、时间、p50/p95、改前/改后或基线对比、异常说明。
