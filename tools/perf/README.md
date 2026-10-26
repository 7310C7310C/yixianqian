# tools/perf — 性能护栏与可复跑基准

本目录把一线牵 v2 的性能约束变成**可执行门禁 + 可复跑基准**，防止代码变更/交接时误删。
规则入口是 `AGENTS.md` 的「性能护栏」；本目录只读产品代码，不改运行时行为。

## 文件

| 文件 | 作用 |
|---|---|
| `budgets.json` | 首载 gzip 基线/预算（当前基线 +10%），8 项指标 |
| `check_budget.py` | 静态预算门禁：逐项打印 当前/预算/余量，超限 exit 1 |
| `check_nginx_guard.py` | 解析 `deploy/nginx/**`，断言 19 项 keepalive/缓存/gzip/TLS/SSE/HSTS/无 API 缓存 |
| `bench.py` | HTTP 延迟（edge/origin/app，N、warmup、p50/p95、JSON） |
| `lcp.py` | 浏览器 LCP 慢机档（CPU×4 / 1.6Mbps / 150ms，冷/热） |
| `protocol.md` | 参考主机、采样口径、判定与安全约定 |

## 常用命令

```sh
make perf-budget          # 静态预算 + nginx 护栏（CI/prepush 已接入）
make perf-guard-test      # 24 个自测：合成超限/删除 nginx 指令必红
make perf-bench PERF_SOURCE=edge PERF_N=10 PERF_WARMUP=3
make perf-lcp PERF_LCP_RUNS=3   # 需 BASE_URL + SESSION_COOKIE + Playwright
```

`bench.py` 只依赖 Python 标准库；`edge` 建议在 server2 跑，`origin`/`app` 在 server1 跑。
`lcp.py` 复用 `tools/browser/perf_boot.py` 的 Playwright 采集，部署/CI 不需要 Node 或 Playwright。

## 预算更新

`budget ≤ baseline×1.10` 由守卫校验；预算改动必须同时说明 `baseline_bytes` 与实测依据，
由 Reviewer 核对 diff。**没有自动更新命令**；合法放宽流程是手工修改
`budgets.json` 的 `baseline_bytes`/`budget_bytes`/`verified_at`，在 commit 说明原因
（为什么必须增长、已做的优化、回滚方式）。CI 不运行 update。

预算不是验收目标本身：`docs/product-spec.md` 的 PM 验收口径优先，不得用改预算代替优化。

## 安全

- `bench.py` 的 cookie 只从 `--cookie-env` 或 `--cookie-file` 读取，原样作为 `Cookie` 头发送，
  **不打印、不写 JSON**；`lcp.py` 只从 `SESSION_COOKIE` 环境变量读取。
- 临时 session 文件应放 `0700` 目录、权限 `0600`，用完删除；报告/提交不得出现 token。
- nginx 护栏会拒绝任何 `proxy_cache_key` 含 `$cookie_`，以及任何 `/legacy/api` 边缘缓存。
