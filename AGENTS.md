# AGENTS.md — yixianqian

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-23
sources:
  - Makefile
  - .github/workflows/**
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
（品牌介绍、注册/登录入口、飞书老用户先设密码指引与固定二维码
`web/static/feishu-qr.png`）；登录后 `/` 才进入用户 SPA。
用户端复用 v1 Vue 3 + Vant SPA；
管理台使用 Go 服务端渲染（SSR），只经用户 SPA 的同源 iframe（`/admin?embed=1`）使用；
浏览器顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`，由 SPA 载入后自动打开管理台 overlay
（受权媒体串流是显式例外，直接串流）；RBAC、CSRF 与审计不变。
不做 POC；用户功能必须完整，完成标准见 `docs/product-spec.md`。

## 结构

- `cmd/`：`server`、`seed`、`token`、`facebackfill` 四个入口。
- `internal/config`：唯一环境变量入口；不得在别处直接读 `os.Getenv`。
- `internal/db`：pgxpool、SQL、事务、迁移、领域仓储。
- `internal/auth`：Argon2id、session/activation token 哈希、CSRF、进程内限流。
- `internal/media`：图片校验/缩图、R2 存储与读取。
- `internal/face`：人脸检测、`has_face` 门禁与回填。
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
make test              # go test -p 1 -count=1 ./...
make test-race         # -race 单测，排除 internal/db、internal/httpserver
make vet
make build             # server/seed/token/facebackfill
make test-integration  # 需要 TEST_DATABASE_URL；只跑 TestIntegration*
make test-migration    # 迁移与文档工具的 Python 测试
make test-broadcast-ui # 需要 BASE_URL/SESSION_COOKIE/ACTIVITY_ID
make docs-gen          # 刷新 REFERENCE 与入口指针
make docs-check        # 文档元数据/地图/链接/生成物检查
make docs-check-stale  # 先 git fetch origin main；base 默认 origin/main，须可解析
make ci                # 本地合并门禁：fmt-check + docs-check + vet + test + 工具测试 + build
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
- 图片只接受 JPEG/PNG；HEIC 回 400；不信任 Content-Type，需 sniff + decode。
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
