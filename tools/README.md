# tools — 生成物与性能护栏

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-28
sources:
  - tools/spa_minify/**
  - tools/spa_precompile/**
  - tools/spa_vendor/**
  - tools/css_split/**
  - tools/perf/**
-->

> 这些工具只在开发期运行；生成物已提交，部署/CI 不需要 Node。
> 规则入口是 `AGENTS.md`；性能判定口径见本文件 §3 与 `tools/perf/budgets.json`。

## 1. 生成物与重建顺序

| 源 | 生成物 | 工具 |
|---|---|---|
| `web/static/legacy/app.js` | `app.min.js` + manifest | `tools/spa_minify` |
| `index.html` + `pages/*.html` | `app.render.js` + `pages/*.render.js` + manifest | `tools/spa_precompile` |
| Vant 4.8.7 | `lib/vant.subset.min.{js,css}` | `tools/spa_vendor` |
| `app.css` | `app.critical.css` | `tools/css_split` |
| `web/static/**` 文本资源 | `.gz` 预压缩变体 | `make static-gzip` |
| `web/templates/partials/boot_card.tmpl` | Go 服务端首卡静态岛（运行时注入 SPA 壳） | `internal/httpserver` |

改完静态源的固定顺序：

```sh
# 1) 改 app.js
cd tools/spa_minify && npm ci && npm run build && cd ../..
# 2) 改模板
cd tools/spa_precompile && npm ci && npm run build && cd ../..
# 3) 改 Vant 子集
cd tools/spa_vendor && npm ci && npm run build && cd ../..
# 4) 改 CSS（如需 critical）
cd tools/css_split && python3 build.py && cd ../..
# 5) 重生 .gz 与门禁
make static-gzip
make spa-check
make static-gzip-check
make ci
```

- 任何 `web/static/**` 变更都要 bump `internal/httpserver/templates.go` 的 `assetVersion`，并同步 admin CSS `@import`；`make ci`/CI 会跑 `tools/check_asset_version.py`。
- `make spa-check` 用 Go/Python 校验 manifest/sha/bytes，不依赖 Node；`make spa-check-node` 只在开发机重建后复核。
- 生成物必须提交；不要手改 `app.min.js`、`app.render.js`、`pages/*.render.js`、manifest 或 `.gz`。
- `web/static/prefetch.js` 是手写外部脚本（登录/注册/激活页预热 SPA 资源）；公开页 CSP 是 `script-src 'self'`，不要把这些逻辑改成 inline script。

## 2. 常见陷阱

- `app.js` 必须是 classic script：用 esbuild `transform`，不要 bundle 成 IIFE/ESM；否则顶层全局会变作用域。
- `@vue/compiler-dom` 必须与 `web/static/legacy/lib/vue.global.prod.js` 完全一致（当前 3.4.27，`package-lock.json` 锁定）；升级 Vue 必须同步重建。
- 不要生成 sourcemap 到 `web/`（会被 `go:embed` 发布）；需要时写 `/tmp`。
- 不要退回 `vant.full.min.*`；不要把 26 个页面模板塞回根包；不要静态 `defer` 主脚本或把大段 CSS/JS 内联回壳。
- 模板改动未重建会让 `TestPageChunksMatchTemplates`、`TestGeneratedBundleHashes`、`TestVantSubsetCoversTemplate` 变红。

## 3. 性能护栏

```sh
make perf-budget          # 静态预算 + 19 项 nginx 断言（CI/prepush）
make perf-guard-test      # 24 个自测：合成超限/删除 nginx 指令必红
make perf-bench PERF_SOURCE=edge PERF_N=10 PERF_WARMUP=3
make perf-lcp PERF_LCP_RUNS=3   # 需 BASE_URL + SESSION_COOKIE + Playwright
# perf_boot.py --gap-p75-max 600：boot card 移除 - loadHome 完成 P75 超过 600ms 即失败
```

- 预算在 `tools/perf/budgets.json`，每项 = 基线 +10%；只能手工修改并走 Review，没有自动更新。
- `shell_gzip` 由「静态壳 + `bootstrap_allowance_bytes`（3 张卡 JSON）+ `boot_card_allowance_bytes`（Go 首卡静态岛，2,300B）」组成；`TestBootCardGzipBudget` 守住静态岛 ≤2,300B gzip。2026-10-28 boot card chrome 后 shell baseline 重新按实测校准为 31,131B，预算 34,245B（+10%）。
- 采集点：`edge` 在 server2 本机、`origin`/`app` 在 server1；浏览器参考主机固定 390×844、CPU×4、1.6Mbps/150ms。
- HTTP：N≥10、warmup≥3；p95 = 排序后第 `ceil(0.95n)` 个；记录 p50/p95、N、主机、时间。
- LCP：N≥3（正式对比建议 5），取中位并记录 min–max；冷/热分开。
- 参考目标：边缘静态 HIT p95 ≤50ms、边缘 API p95 ≤220ms、冷 LCP 中位 ≤2.5s、热 LCP 中位 ≤1.8s；同机回归 p95 不得比基线差 >10%。
- 不得给 `/legacy/api` 加边缘 `proxy_cache`；`proxy_cache_key` 不得含 `$cookie_`。
- 热路径改动给 `make perf-bench`/`make perf-lcp` 前后 p50/p95 与 N、主机、时间；p95 不得比基线差 >10%。
- cookie 只从 `--cookie-env`/`--cookie-file`/`SESSION_COOKIE` 读取；不打印、不进 JSON/commit；临时文件 `0600`。
- 更详细的采集点、判定与产物约定见本文件 §3 与 `tools/perf/` 下的脚本。
