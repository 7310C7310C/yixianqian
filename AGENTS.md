# AGENTS.md — yixianqian

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-28
sources:
  - Makefile
  - .github/workflows/**
  - tools/perf/**
  - deploy/nginx/**
  - internal/httpserver/middleware.go
  - internal/httpserver/handlers_media.go
  - internal/httpserver/media_serve.go
  - internal/httpserver/legacy_ui.go
  - internal/httpserver/readcache.go
  - internal/httpserver/app.go
  - internal/httpserver/register_pages.go
  - tools/test_perf_guardrails.py
  - web/static/public-copy.js
  - web/static/register.js
  - web/static/auth.css
-->

> **本文件是项目规则的唯一入口。** 开工先读本文件；当前状态看 `docs/STATUS.md`；
> 机械事实（Go 版本、cmd、路由、env、migration、make 目标）看
> `docs/reference/REFERENCE.md`（自动生成，勿手改）。
> 需求与验收看 `docs/product-spec.md`；决策看 `docs/DECISIONS.md`；
> 迁移看 `docs/MIGRATION.md`；部署看 `docs/runbooks/staging.md`。
> 本文件不假设固定团队或角色；需要时使用“维护者、执行者、审查者、PM（使用者）”。

## 文档地图（最终 10 个）

| 文件 | 用途 |
|---|---|
| `AGENTS.md` | 本文件；唯一规则入口与文档地图 |
| `README.md` | 产品边界、架构、目录、本地开发、部署与安全概览 |
| `CLAUDE.md` | Claude Code 入口指针（生成，指向本文件；勿手改） |
| `docs/PROJECT_CHARTER.md` | 使命、范围、架构硬约束（PM 权威） |
| `docs/STATUS.md` | 当前部署、阶段、已实现、P0 与缺口（唯一进度来源） |
| `docs/product-spec.md` | 验收清单（PM 定范围；勾选需证据） |
| `docs/DECISIONS.md` | 架构/产品决策合并记录（新决策追加，不改历史） |
| `docs/MIGRATION.md` | testapp 数据迁移、对账与已知限制 |
| `docs/runbooks/staging.md` | staging 部署、回滚、证书、人脸切换、smoke |
| `docs/reference/REFERENCE.md` | 机械事实（生成物，勿手改） |

新增或删除文档必须同步本表；生成物（`CLAUDE.md`、`docs/reference/REFERENCE.md`）由
`make docs-gen` 刷新，不得手改。

## 工具兼容

- 通用入口是 `AGENTS.md`；Codex、opencode、Claude Code、GitHub Copilot、Cursor、Windsurf、RooCode、Junie、Zed、Warp、Amp、Factory、Devin 等可直接读取或按各自配置读取。
- Claude Code 的 `CLAUDE.md` 只是 `AGENTS.md` 的 `@` 导入兼容指针，不复制规则。
- Gemini CLI / Aider 若未自动读取，用各自配置指向 `AGENTS.md`；WorkBuddy 等未约定工具由使用者先读本文件。

## 项目定位与架构

把「一线牵」改造成不依赖飞书的独立网页产品。Go 模块化单体、PostgreSQL、R2，
公开入口是 server2（香港）TLS relay，回源 server1。匿名访问 `/` 渲染服务端公开首页
（品牌介绍、注册/登录入口、飞书老用户先设密码指引与飞书入口链接）；该页 HTML 除 `<title>`
外不得出现汉字，文案由 `/static/home.js`（`\uXXXX` 转义）在浏览器注入，页面引用的 CSS/JS
必须 0 汉字（扫描防护；见 `docs/DECISIONS.md` 决策 38）。同一契约自 2026-10-27 起覆盖
`/login`、`/activate`、`/password`、`/register/done` 与 404：源码 0 汉字，文案由
`/static/public-copy.js` 注入，样式用 `home.css` + `auth.css`（决策 39）；公开 GET 错误页
（`/activate` 400/410、`/password` 409）同样纳入契约（`data-error-code`，决策 40）；
`/register` 表单页由服务端生成 ASCII 文案目录（`data-copy-json`，非 ASCII 全 `\uXXXX`）+
`/static/register.js` 注入标签/提示/占位符/选项，option 的中文 value 由 JS 写入后按原契约提交，
POST 失败重渲染的原始响应同样 0 汉字（决策 42）；
POST 表单错误消息与内部 500/管理台错误页是明示例外。旧 `/welcome` 已并入首页，`GET /welcome` 301 → `/`，
`/register?from=welcome` 仍记录 `join_click`。登录后 `/` 才进入用户 SPA。
用户端复用 v1 Vue 3 + Vant SPA；
管理台使用 Go 服务端渲染（SSR），只经用户 SPA 的同源 iframe（`/admin?embed=1`）使用；
浏览器顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`，由 SPA 载入后自动打开管理台 overlay
（受权媒体串流是显式例外，直接串流）；RBAC、CSRF 与审计不变。
不做 POC；用户功能必须完整，完成标准见 `docs/product-spec.md`。

## SPA 历史与返回（2026-10-26）

- 顶层 SPA 默认**虚拟历史栈**（`web/static/legacy/app.js` 的 `hist`）：子页/浮层/应用内返回不得直接调用
  `window.history.pushState/replaceState/back`；统一用 `pushHistory`、`hist.replace`、`handleBack`。
- 真实历史只保留启动哨兵；`popstate` 仅负责安卓硬件返回。`native_admin` iframe 与
  `?history=legacy` / `localStorage.yxq_history_mode='legacy'` 保持旧真实历史逻辑。
- 新增子页/浮层沿用现有 watcher/guard 状态机；读取历史状态用 `hist.state`，不要直接读 `history.state`。
- Via/WebView 顶部进度条只能真机复验；Chromium 回归用 `tools/browser/no_history_smoke.py` 断言
  子页 History 调用与主框架文档导航均为 0。

## 结构

- `cmd/`：`server`、`seed`、`token`、`facebackfill`、`thumbwarm`（缩略图缓存预热）五个入口。
- `internal/config`：唯一环境变量入口；不得在别处直接读 `os.Getenv`。
- `internal/db`：pgxpool、SQL、事务、迁移、领域仓储。
- `internal/auth`：Argon2id、session/activation token 哈希、CSRF、进程内限流。
- `internal/media`：图片校验/缩图、R2 存储与读取。
- `internal/face`：人脸检测、`has_face` 门禁与回填。
- `internal/tencentface`：可选的腾讯云照片合规判定（图像标签 + 静默活体），
  TC3 直连、fail-open，只影响 `has_face`，不产生人脸几何。
- `internal/matching`：分组与一对一纯算法。
- `internal/platform`：时钟、日志、请求 ID、客户端 IP、构建版本。
- `internal/httpserver`：路由、中间件、handler、模板渲染、`/legacy/api/*`。
- `web/`：embed 模板与静态资源；`web/static/legacy/` 为 v1 用户端 SPA。
- `migrations/`：embed 的 SQL migration，`NNNN_name.sql`，只增不改。
- `tools/`：迁移、人脸探针、浏览器 smoke、文档生成/校验。
- `deploy/`：nginx 与 systemd 模板；实际操作以 runbook 为准。

## 通用工作规则

- 开工先看 `git status`、`git log --oneline -10`，再读 `docs/STATUS.md` 与相关代码；
  不要假设旧文档正确。
- 先定义可证伪判准（命令 + 期望输出），再改；没有证据不算完成。
- 不写密钥；日志、错误、audit、文档与提交里不得出现密码、token、cookie。
- staging 验证先于生产；生产切换、删数据、凭证/权限变更必须 PM（使用者）明确批准。
- commit message 用简体中文；不 force-push、不重写已推送历史。
- 小改动可直接在 `main`；较大改动用独立 worktree/分支，合并前跑 `make ci`；
  不默认使用 PR（除非 PM 明确要求）。
- 不假设有固定团队或角色；按任务需要由维护者/执行者/审查者协作，最终对 PM 负责。

## 标准交付流水线（2026-10-24 PM 确认；唯一标准流程）

顺序固定，不得跳步、不得重排：

1. **改动**：小改动直接在 `main`；较大改动用独立 worktree/分支，由维护者本地合并后 push。
2. **本地验证**：`gofmt -l .` 为空 + `go vet ./...` + 定向测试 + `make ci`（够用时可先 `make prepush` 快速门禁）；前端改动跑对应 browser smoke。
   - 日常 edit 后只跑 `make fast`（<30s，受影响检查）；`make ci` 每个 commit 一次即可，不要每次编辑全跑。
   - 本机 `test-race` 已废弃为提示 target（不跑测试）；CI 已对全部 package 跑 `-race`。发版前需要时用 `make test-race-full`。

3. **commit**：简体中文；带 `Docs:` 或 `Docs-Impact: none（理由）`。
4. **push**：常规/高风险须在 push 前拿到独立 Reviewer 对本地 commit 的 PASS；低风险快速通道可直接 push。
5. **CI 绿**：当前 HEAD 的 `lint-build`/`unit`/`integration`；有文档变更时 `Docs` 也须绿。
6. **自动部署 staging**：`yxq-autodeploy` 在 CI 绿后 60 秒内构建/重启/health，失败自动回滚。
7. **部署后 smoke**：对应 Playwright/curl 冒烟；以 `/healthz` 的 `version=staging-<sha>` 对齐部署 HEAD。
8. **批次总报告**：改动、证据、部署状态与残余风险。

硬约束：

- **push 不等于已部署**；以 `/healthz` 版本为准，不以 `git push` 成功为完成。
- **CI 红不部署**；不允许 `[skip ci]`、绕过 CI、红/取消时先部署“看看”。
- **静态资源版本门禁**：改动 `web/static/**`（含生成物）必须在同一提交范围 bump
  `internal/httpserver/templates.go` 的 `assetVersion` 并同步 admin CSS `@import`；
  `make ci`/prepush 与 CI lint-build 都会跑 `tools/check_asset_version.py`，未 bump 直接失败。
- **staging 部署闸**：autodeploy 拒绝缺少 `Reviewed-By:` trailer 的代码提交（docs-only 豁免；
  `--force` 仅限回滚/热修）；Reviewer PASS 必须落成 trailer 才能自动部署。
  CI lint-build 跑 `deploy/autodeploy/test_autodeploy.sh` 沙箱单测，防止门禁回归。
- 不得绕过本流程；异常（冻结自动部署、回滚）须记录到 `.team/status.md`/`.team/decisions.md`。

通道分级（唯一差别在第 4 步；第 5–7 步完全相同）：

- **快速通道（低风险）**：纯文案/样式/排序/静态资源，且不涉及 auth、权限、数据、API、schema、支付 → 自测 + 截图/浏览器 smoke，可免独立 Reviewer 直接 push；须在 `.team/decisions.md` 记录范围、风险与批准（PM 2026-10-24 授权）。目标 ≤10 分钟（开工→上线）。**快速通道不适用于热路径与静态生成物**：命中 `web/static/legacy/**`、`deploy/nginx/**`、`internal/httpserver/{middleware,handlers_media,media_serve,legacy_ui,readcache,app}.go`、DB 热查询或 `migrations/**` 热索引时，仍须按 `AGENTS.md`「性能护栏」跑 `make perf-budget` 与 `make perf-bench` 前后对比并附数字；快速通道只豁免独立 Reviewer，不豁免这些门禁与证据。
- **常规通道**：行为、逻辑、接口、模板结构变更 → 独立 Reviewer PASS 后 push。
- **高风险**：安全/支付/数据/权限/凭据 → Reviewer + Auditor/security gate，PASS 后 push。

自动部署运维：`systemctl is-active yxq-autodeploy.timer`、`tail -n 50 /var/log/yxq-autodeploy.log`、紧急冻结 `systemctl stop yxq-autodeploy.timer`；细节见 `docs/runbooks/staging.md`「自动部署」。人工部署仅用于回滚/热修。

**文档门禁减负**：仅当改动命中 `docs/`、env、route、migration 时跑 `docs-check-stale`；纯前端模板/样式允许 `Docs-Impact: none（理由）`，不强制刷新 `docs/STATUS.md` 的 sources。

## Agent 快速路径（2026-10-27）

这一节是给 opencode/Codex 等 agent 的固定入口；不要每次重新拼命令。

- **每个 edit 后**：`make fast`。只跑受影响的 gofmt/vet/test/static/docs，<30s。
- **每个 commit 前**：`make ci` 一次；不要 46 次。`make fast` 已覆盖大部分即时错误。
- **每个 batch**：`make smoke-fast`（390x844 + 390x700 并行）；**优先在 server1** 跑，`BASE_URL=http://127.0.0.1:8093`，staging 就在本机。
- **每个 batch 只跑一次全套**：`make smoke-full`；不要每修一个 bug 全跑 19 项。
- **等部署**：`make deploy-wait`，一个阻塞命令等 CI + healthz；不要每 10 秒发一轮 `gh run list`。
- **Review 前**：`make review-packet` 生成 diffstat/风险/证据清单；Reviewer 只跑必要的复核。
- 本机不跑 race；CI 已覆盖全量 race。发版前需要时用 `make test-race-full`。
- **session 粒度**：一个 batch 最多 3 个 bug 或一次 deploy；之后开新 builder session，context 控制在 ~150k。
- **docs stale**：只有 docs/config/route/migration 变更才强制；test-only/frontend 用 `Docs-Impact: none（理由）`。

## 文档自动更新协议

- 机械事实（Go 版本、cmd、路由、env、migration、结构、make 目标）由
  `tools/gen_docs.py` 生成、`tools/check_docs.py` 校验，不得手写副本。
- 语义内容由文档 owner 复核；`last_verified` 是“对照来源核实过”的日期。
- 每份文档文件开头必须有 doc-meta（H1 前后均可；generated 的 CLAUDE/REFERENCE 除外），字段含 `owner`、`status`、
  `last_verified`、`sources`；owner 只用 `pm` 或 `maintainer`。
- 任何人可凭证据更新 `docs/STATUS.md` 的已实现/缺口事实与 `last_verified`；
  但不得改 `docs/PROJECT_CHARTER.md`、`docs/product-spec.md` 的验收勾选或
  `docs/DECISIONS.md` 的已定决策。
- 每个任务写文档影响；提交 trailer 二选一：`Docs: <路径列表>` 或
  `Docs-Impact: none（理由）`。
- 改完运行 `make docs-check`；push 前先 `git fetch origin main`，再运行 `make docs-check-stale`
  （base 默认 `origin/main`，必须可解析）。
- 历史看 `git log`，不维护变更日志；迁移看 `docs/MIGRATION.md`；决策看
  `docs/DECISIONS.md`；进度看 `docs/STATUS.md`。

### 变更 → 文档

| 变更 | 需更新 |
|---|---|
| cmd / flag | `AGENTS.md` 结构/命令、`README.md`；REFERENCE 自动 |
| HTTP 路由 | REFERENCE 自动；影响契约时更新 `docs/product-spec.md` 或 `docs/DECISIONS.md` |
| env | REFERENCE 自动、`README.md`、`docs/runbooks/staging.md` |
| migration | REFERENCE 自动、`docs/MIGRATION.md`、runbook、`docs/STATUS.md` |
| 部署/证书/CI | `docs/runbooks/staging.md`、`AGENTS.md`、`README.md` |
| Go 版本 | `docs/PROJECT_CHARTER.md`（PM 批准）、`README.md`、CI、REFERENCE 自动 |
| 安全/auth/media 行为 | runbook 安全段；决策变化写入 `docs/DECISIONS.md` |
| 产品范围/验收 | `docs/PROJECT_CHARTER.md`、`docs/product-spec.md`（PM）→ `docs/STATUS.md` |

## 任务契约（通用六条）

1. **开工检查**：`git status`/`git log`、读 `docs/STATUS.md`、定位相关代码与文档。
2. **可证伪判准**：写清命令、期望输出、正常与边界路径。
3. **证据**：命令 + 关键输出；没有输出视为未验证。
4. **文档影响**：列出要更新的文档；无影响写 `Docs-Impact: none（理由）`。
5. **报告**：改动、证据、未完成/阻塞、残余风险。
6. **回滚**：说明如何撤销；schema 只增不改，回滚 binary 不等于回滚数据。

## 常用指令

```sh
make tidy
make test              # go test -p 4 -count=1 ./...
make test-race-full    # 发版前：全部 package -race（不含 httpserver，CI integration 覆盖）
make vet
make build             # server/seed/token/facebackfill
make fast              # agent 每次 edit 后的快速检查（<30s）
make smoke-fast        # 每个 batch 的浏览器子集（390x844+390x700 并行）
make smoke-full        # nightly/release 完整浏览器矩阵
make smoke-viewer-arrows # 大图左右按键/首尾循环（390 触摸 + 1280 鼠标）；需 BASE_URL + SESSION_COOKIE(_FILE)
make deploy-wait       # 阻塞等 CI + staging healthz
make review-packet     # 生成 Reviewer 用的 diff/风险/证据清单
make test-integration  # 需要 TEST_DATABASE_URL；只跑 TestIntegration*
make test-migration    # 迁移、文档与 asset-version 门禁自测（Python）
make test-broadcast-ui # 需要 BASE_URL/SESSION_COOKIE/ACTIVITY_ID
make static-gzip       # 重生成 web/static/** 的 .gz 预压缩变体（改静态文件后必跑）
make spa-check         # 校验 SPA 生成物（Vant 子集/render chunk/app.min.js 的 sha/bytes/manifest；CI 不依赖 Node）
make spa-check-node    # 开发机 Node 重建后复核（部署/CI 不依赖 Node）
make docs-gen          # 刷新 REFERENCE 与入口指针
make docs-check        # 文档元数据/地图/链接/生成物检查
make docs-check-stale  # 先 git fetch origin main；base 默认 origin/main，须可解析
make prepush           # 快速本地门禁（push 前必跑）：fmt-check + static-gzip-check + spa-check + docs-check + asset-version-check + vet + build
make asset-version-check # 单独跑静态资源版本门禁（默认 base=origin/main）
make ci                # 本地合并门禁：fmt-check + static-gzip-check + spa-check + docs-check + asset-version-check + vet + test + 工具测试 + build
make run
make seed
go run ./cmd/token -legacy-user-id U-0006
```

CI 以当前 HEAD 的 push run 为准；红灯先看 `gh run view <id> --log-failed` 修根因，
不跳过、不 `[skip ci]`、不在红/取消时部署。本地 `make ci` 不含 `-race`，不等价于 CI。

## 安全硬约束

- 所有配置从环境变量读；不得写死 DB/R2/session 密钥。
- 密码只存 Argon2id PHC；session/activation token 只存 SHA-256 hash。
- session cookie：HttpOnly + Secure（可 env 关闭供本机 HTTP）+ SameSite=Lax。
- 所有写入（含 logout、上传）必须验 CSRF；`/api/*` 写入只接受 `X-CSRF-Token`。
- R2 对象路径不得外泄；媒体一律经 `GET /media/{id}` 鉴权串流。
- 只有 `TRUSTED_PROXY` 来源可提供 `X-Real-IP` / `X-Forwarded-For`。
- 图片只接受 JPEG/PNG；HEIC/HEIF 回 400 并提示转 JPEG；不信任 Content-Type，需 sniff + decode；原图入库前重编码去除 EXIF/GPS/XMP（保留方向：先 auto-orient 再编码），缩图同样；像素上限 12.5MP。
- 不新增 Redis；session 与 rate-limit 以 Postgres / 进程内为限。
- 浏览器范围：iOS 14.5+/Safari 14.1+、Chrome/WebView 84+、Firefox 74+。
- 身份证门禁是 P0 上线阻断；补录/放行方式等 PM 决策，不得私自绕过。

## 部署要点

- 公开入口固定 server2（HK）→ server1；不要直指 server1，不要开 Cloudflare 橙云。
- staging：server1 `/opt/yixianqian-next`、systemd `yixianqian-next-test`、8093；
  DB `yixianqian_staging`；R2 `yixianqian-media-staging`。
- 部署前保留上一版 binary；migration 只增不改；媒体只经应用代理。
- 人脸检测（migration 0005）已于 2026-10-22 首次上线；常规部署走 runbook「常规部署」一节；
  首次上线/重建见 git 历史旧版 runbook，当前不要执行 pre-0005 回滚或全量回填。
- 顶层 document 访问 `/admin`、`/admin/...` 页面路由（含 `?embed=1`）一律 303 到 `/?admin=1`，
  由 SPA 自动打开管理台 overlay，不渲染顶层页面；受权媒体串流（`/admin/users/{id}/photos/{mediaID}`、
  `/admin/activities/{id}/poster`）是显式例外，document 直接串流；
  管理台只经站内 iframe `/admin?embed=1` 使用，smoke 断言顶层 303（`Location: /?admin=1`）与 iframe SSR。
- ID 门禁 P0 与已知缺口以 `docs/STATUS.md` 为准。
- v1 旧部署 skill 不适用；详细步骤见 `docs/runbooks/staging.md`。

## 性能护栏（2026-10-26）

这些性能来自一组“看起来可删、删了就退化”的配置与构建约束。改热路径前先读本节；
`make perf-budget`（静态预算 + nginx 断言）已接入 `prepush`、`make ci` 与 CI `lint-build`；
`make perf-guard-test`（自测）在 `prepush` 运行，并由 `make ci` 的 Python 测试发现统一覆盖。
**生产未授权；护栏只防回归，不改变运行时行为。**

### 静态首载预算（gzip，当前基线 + 10%）

| 指标 | 基线 B | 预算 B | 说明 |
|---|---:|---:|---|
| SPA 壳（含 critical CSS + bootstrap 余量） | 27,978 | 30,776 | 静态壳 24,669B + 3,309B bootstrap 余量；按 batch10 staging-0f14252 `/app` 实测 27,978B 校准 |
| `app.min.js` | 47,960 | 52,756 | 主业务包，禁止重新内联进壳 |
| `app.render.js`（根 + cards） | 25,034 | 27,538 | 预编译根模板；改模板必须重建 |
| Vant 子集 JS / CSS | 23,882 / 37,664 | 26,271 / 41,431 | 不得退回 `vant.full.min.*` |
| critical CSS | 7,641 | 8,406 | 壳内联首屏子集 |
| 页面 chunks 合计 | 43,521 | 47,874 | `pages/*.render.js` 懒加载；不得全部内联 |
| 首载静态估算 | 242,310 | 266,541 | 不含 API 与用户照片；critical 只计一次 |

超限即红；预算只能手工修改 `tools/perf/budgets.json` 并走 Review，**没有自动 `--update`**。
在 commit/评审里说明原因，让 Reviewer 看 baseline/budget 的 diff（budget 不得超过 baseline×1.10）。
数据在 `tools/perf/budgets.json`，门禁是 `tools/perf/check_budget.py`，口径见
[`tools/perf/protocol.md`](tools/perf/protocol.md)。

### 禁止移除清单（动这些必须按“热路径流程”给前后数字）

- **边缘 keepalive**：server2 `upstream yxq_origin { keepalive 64; keepalive_timeout 60s; }`、
  `proxy_http_version 1.1`、普通/SSE 请求 `Connection $yxq_conn`、`proxy_ssl_session_reuse on`；
  server1 `upstream yxq_app { keepalive 32; }`、`proxy_set_header Connection ""`、
  `proxy_max_temp_file_size 0`。删除会回到每请求跨洋重连（≈470–580ms 地板）。
- **静态边缘缓存**：`/static/` 的 `proxy_cache yxqstatic`、`proxy_cache_key` 含
  `$yxq_static_enc`（gzip/identity 分键）、`proxy_cache_lock/use_stale`、`X-Cache-Status`。
  **不得给 `/legacy/api` 加边缘 proxy_cache**：key 含明文 session 会落盘，不含会话会串用户；
  `proxy_cache_key` 里任何 `$cookie_` 都是安全违规。API 提速靠应用内 10s 读缓存 + keepalive。
- **预压缩与 gzip 变体**：`web/static/**` 的 `.gz` 由 `make static-gzip` 生成并随 `go:embed`
  发布；Go 按 `Accept-Encoding` 返回预压缩字节 + ETag；边缘 `gzip on` 且 `gzip_types`
  必须含 `text/javascript`。改静态源文件后不重生成 `.gz` 会被 `static-gzip-check` 拒绝。
- **TLS 恢复**：边缘 server 与 `/static` 上游的 `ssl_session_cache`、`ssl_session_tickets on`、
  `proxy_ssl_session_reuse on`；server1 的 `yxq_app` upstream 是 `http://`，不适用
  `proxy_ssl_session_reuse`。TLS1.3 恢复依赖 ticket，关闭等于每次新连接完整握手。
- **SSE buffering 例外**：只有 `location = /legacy/api/notifications/stream` 允许
  `proxy_buffering off` + `proxy_cache off` + 3600s 读超时；其余 location 不得关闭 buffering。
- **壳拆分与 hero 先绘制**：壳只保留启动旗标/版本/单块 ≤5KB 内联脚本（当前最大 4,429B）+ critical CSS；
  `app.css`/`app.min.js`/`app.render.js` 外置；主脚本按 `vue → app.render → app.min`
  在首卡 hero `load+decode` 后注入（`yxq-hero-ready`/`yxq-app-start` 标记）。
  不要把脚本改回静态 `defer`，也不要把大段 CSS/JS 内联回壳。
- **Vant 子集与页面 chunks**：`lib/vant.subset.min.{js,css}` 由 `tools/spa_vendor` 生成；
  `app.render.js` + `pages/*.render.js` 由 `tools/spa_precompile` 生成；manifest/sha 门禁
  （`make spa-check`）不得绕过。不要用 `vant.full.min.*` 或把 26 个页面模板塞回根包。
- **媒体**：缩略图有界磁盘缓存（`MEDIA_CACHE_BYTES`，默认 256MiB，授权先于缓存）；
  原图/缩略图都带 ETag + `If-None-Match` 304 + `private, max-age`。不得移除缓存或 ETag。
- **10s 读缓存 + epoch**：`/legacy/api/home`、`/legacy/api/cards` 共享进程内缓存
  （TTL 10s、总 32MiB、单条 256KiB、字节 LRU）；任何成功的非 GET/HEAD/OPTIONS 请求
  （排除 `/static/`、`/healthz`、`track*`、`client-events`）后全局 epoch++。
  不得绕过 epoch、扩大成跨用户键或把错误响应写进缓存；多实例需先做 epoch 的 pub/sub。
- **关键索引与查询形状**：`0008`（likes/notifications/users 卡池）、`0009`（praises/messages/
  signups/notifications）、`0010`（照片授权四索引）只增不改；新增/改 home、cards、profile、
  notifications、照片授权等热查询必须附 `EXPLAIN (ANALYZE, BUFFERS)`，不得静默 drop 索引。

### 热路径变更流程

命中以下任一路径即视为热路径：`web/static/legacy/**`、`deploy/nginx/**`、
`internal/httpserver/{middleware,handlers_media,media_serve,legacy_ui,readcache,app}.go`、
`internal/db/**` 的 home/cards/profile/notifications/照片授权查询、`migrations/**` 热索引。

1. 改前在参考主机跑一次基线：`make perf-bench`（同 source、N≥10、warmup≥3；主机/口径见
   [`tools/perf/protocol.md`](tools/perf/protocol.md)）；
   浏览器/首屏改动再加 `make perf-lcp`（慢机 CPU×4/1.6Mbps/150ms，冷/热各 N≥3，建议 5）。
2. 改后同机同参数复跑；报告附 p50/p95、N、主机、时间，不只贴“变快了”。
3. 判定：p95 不得比基线差 >10%；超过必须解释根因或回退。边缘静态命中目标 p95 ≤50ms；
   API/首屏目标以 `docs/STATUS.md` 与 `docs/product-spec.md` 的 PM 口径为准，不得自行放宽验收。
4. 改了静态源文件：`make static-gzip`（再跑 `-check`）并按规则 bump `assetVersion`；
   改了 Node 生成物：`make spa-check-node` 后提交生成物，部署/CI 仍只跑 `make spa-check`（不依赖 Node）。
5. 改了 nginx：先在目标机 `nginx -t`，按 `docs/runbooks/staging.md` 备份/回滚；护栏单测必绿。
6. 预算需要变化时：手工改 `budgets.json` 的 `baseline_bytes`/`budget_bytes` + 说明 + Reviewer 审 diff；
   不得只改 `budget_bytes` 不留 `baseline_bytes`，也不提供自动更新命令。

### 交接检查表

- [ ] `make perf-budget`、`make perf-guard-test`、`make ci` 全绿；`git diff` 无运行时逻辑/静态生成物误改。
- [ ] `make static-gzip-check` 绿；`make spa-check` 绿；manifest sha 未被手改。
- [ ] nginx 护栏 19 项全 PASS；确认没有 `/legacy/api` 边缘缓存、没有 `$cookie_` 进 cache key。
- [ ] 热路径改动附 `make perf-bench` / `make perf-lcp` 前后 JSON 与 p50/p95 对比；异常有解释或回滚。
- [ ] 无 cookie/session/token 进入日志、报告、JSON、commit；基准 cookie 只从 env/0600 文件读取。
- [ ] 预算/生成物变更在 `.team/perf/guardrails-report.md` 或任务报告里说明理由与回滚方式。

### 构建物再生成命令

```sh
make static-gzip && make static-gzip-check   # 改 web/static 文本资源后
make spa-check                              # CI/部署门禁（Go + Python，不需要 Node）
make spa-check-node                         # 开发机 Node 重建后复核（部署不依赖）
make docs-gen                               # 文档生成物
make perf-budget && make perf-guard-test    # 护栏
# API 路径需有效 session：cookie 只从 env/0600 文件读取，不打印、不进 JSON
make perf-bench PERF_SOURCE=edge PERF_N=10 PERF_COOKIE_ENV=YXQ_SESSION_COOKIE
make perf-lcp PERF_LCP_RUNS=3               # 需 BASE_URL + SESSION_COOKIE + Playwright
```
