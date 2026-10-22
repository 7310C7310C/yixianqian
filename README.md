# 一线牵 v2

一线牵是一个面向教会单身青年的独立网页交友平台，替代 v1 的「飞书机器人 + 飞书 H5」。用户不需要飞书账号，使用手机号与密码登录；资料与业务数据存 PostgreSQL，照片存私有 R2。用户端复用 v1 的 Vue 3 + Vant 界面；Go 提供 API 适配层，管理端使用服务端渲染。

> 本 README 只维护**稳定的项目事实、边界与入口**。
> 功能是否完成看 `docs/product-spec.md`，进度看 `docs/milestones.md`，部署看 `docs/runbooks/staging.md`，架构决策看 `docs/adr/`。不要把逐功能清单、逐路由表或每日进度堆回这里，否则它会很快过期。

## 1. 产品边界

### 用户端
- 手机号 + 密码登录；管理员可发一次性启用链接或临时密码。
- 注册、资料与照片、人工审核、牵线卡片、喜欢/点赞/留言、活动报名与签到、分组志愿、一对一必聊、麦穗/邀请/兑换、站内通知。
- 用户端的界面语言与操作习惯以 v1 原版为基准：四个主入口（牵线/消息/活动/我的）、卡片浏览、左右滑卡、喜欢/留言/报名/志愿/结果等流程不重新发明。
- 首发不依赖短信验证码；不做即时聊天，相互喜欢后交换微信号，站内留言保留。

### 管理端
- 用户审核、活动与广播、报名/签到名单、分组/一对一控制台、麦穗与兑换、举报/反馈、RBAC 与审计。
- 管理端以运营效率为先，可自行设计，不受 v1 视觉限制；但同样使用简体中文、服务端权限与审计。

### 明确不做
- 飞书机器人、飞书表单、Bitable 运行时、飞书 Drive 作为 v2 依赖。
- 微服务、Kubernetes。
- 重写用户端 UI（不做；用户端直接复用 v1 前端）；管理端不引入 SPA 框架与前端构建链。
- 为旧浏览器维护第二套完整用户 UI；如确有需要，另立 ADR。
- 首版短信 OTP、即时聊天/IM。

v1 仍保留在 `app.nantou.love`，v2 不修改 v1 的数据库与运行环境。两者是独立系统；v2 生产切换必须另行走查与回滚方案。

## 2. 架构

```text
浏览器
  │ HTTPS
  ▼
server2（香港）—— 唯一公开入口，TLS relay
  │ HTTPS（回源）
  ▼
server1（洛杉矶）—— nginx + Go app（127.0.0.1:8093）
  ├── PostgreSQL 16
  └── Cloudflare R2（私有桶，仅 app 代理读取）
```

- **后端 API**：Go 模块化单体；PostgreSQL + R2；管理端 `html/template` SSR。
- **用户端**：复用 v1 Vue 3 + Vant SPA；Go 提供旧形状兼容 API、session 与媒体代理。
- **数据库**：PostgreSQL 16；迁移随应用启动执行。
- **媒体**：R2 只做对象存储，桶不公开；浏览器只拿应用路径 `/media/{id}`，不拿 R2/Cloudflare 直链。
- **用户端前端**：v1 Vue 3 + Vant SPA，保留旧版 DOM/CSS/AJAX/tab 行为；不新写 React/Vue 应用。
- **管理端前端**：Go 模板 + 少量原生 JS；不引入 SPA 框架。
- **无 JS**：用户 SPA 需要 JavaScript；登录/注册/管理端仍保持服务端可用。
- **不引入 Redis**：当前正确性不依赖缓存或队列；需要时再评估。
- **外部依赖**：不引外部字体/CDN；前端资产随仓库本地提供。

网络硬约束：`next.nantou.love` 必须经 server2 香港 relay 回源，不要把 DNS 直指 server1，也不要开 Cloudflare 橙云；实测直连美国 IP 或橙云 IP 可能被运营商拦截。详见 `docs/adr/0006-hong-kong-relay.md`。

### 用户端浏览器范围

复用 v1 前端原样（不转译、不补 CSS gap fallback）的完整支持范围：

- iOS 14.5+ / Safari 14.1+
- Chrome / Android WebView 84+
- Firefox 74+

低于此范围可能白屏或版面错乱；登录、注册、管理端仍可用。若未来真实用户数据表明旧设备占比显著，再另立 ADR 评估转译 + CSS 兼容层。

## 3. 仓库结构

```text
cmd/server/             HTTP 服务入口
cmd/seed/               本地/测试账号与启用 URL
cmd/token/              对既有或新用户发一次性启用 token
internal/config/        环境变量解析与启动校验（唯一入口）
internal/db/            pgxpool、SQL、事务、迁移、领域仓储
internal/auth/          Argon2id、session、CSRF、限流
internal/media/         图片校验/缩图、R2 存储与读取
internal/httpserver/    路由、中间件、handler、模板渲染、各功能模块
internal/matching/      分组与一对一的纯算法
internal/platform/      时钟、日志、请求 ID、客户端 IP 等基础设施
web/templates/          管理端/服务端页面模板
web/static/             服务端页面静态资源
web/static/legacy/      v1 用户端 SPA 资产与兼容适配
migrations/             只增不改的 SQL migration
tools/                  testapp 迁移与一次性运维脚本
docs/                   charter、规格、里程碑、runbook、ADR
```

功能模块通过 `init()` 注册自己的路由与模板；共享文件尽量少改，以减少并行开发冲突。

## 4. 本地开发

### 前置
- Go 1.24 或以上（以 `go.mod` 为准）
- PostgreSQL 16（14+ 应可；schema 未用 16 专属语法）
- Python 3（仅迁移工具测试需要）
- 一个 R2 或 S3 兼容 bucket（本地可先用测试桶；不配置则上传/读取媒体会失败）

### 启动数据库

```sh
docker run --rm -d --name yxq-pg \
  -e POSTGRES_USER=yixianqian \
  -e POSTGRES_PASSWORD=yixianqian \
  -e POSTGRES_DB=yixianqian \
  -p 5432:5432 postgres:16
```

### 配置

```sh
cp .env.example .env
# 至少改 DATABASE_URL、SESSION_SECRET、R2_*；
# 本机走 HTTP 时设 SESSION_COOKIE_SECURE=false。
set -a; . ./.env; set +a
```

`.env.example` 是配置项的权威清单；不要在本 README 复制整张表。密钥、`.env`、`local_config` 一律不进 git。

### 构建、测试、启动

```sh
make ci                 # gofmt 检查 + vet + 单测 + 迁移工具测试 + build
make test-integration   # 需要 TEST_DATABASE_URL；跑 PostgreSQL 集成测试
make run                # 或 go run ./cmd/server
```

需要真实 Postgres 的集成测试：

```sh
TEST_DATABASE_URL='postgres://yixianqian:yixianqian@localhost:5432/yixianqian_test?sslmode=disable' \
  go test -p 1 -count=1 ./internal/db ./internal/httpserver
```

数据库包与 HTTP 集成测试共用测试库，必须串行（`-p 1`）；CI 已如此配置。

### 建测试账号

```sh
go run ./cmd/seed
# 输出一行启用 URL：
# http://localhost:8080/activate?token=...
```

也可以对既有用户或新手机号签发 token：

```sh
go run ./cmd/token -legacy-user-id U-0006
go run ./cmd/token -phone 13900000000 -name 王小明
```

打开启用 URL 设置密码后即可登录。`/healthz` 返回数据库健康状态与构建版本。

## 5. 配置与运行原则

- 所有配置来自环境变量，入口在 `internal/config/`；`.env.example` 列出完整清单与默认值。
- 生产必须使用 HTTPS、Secure cookie、明确的 `TRUSTED_PROXY`，并让 R2 endpoint 使用 HTTPS；启动时会做基本 fail-fast。
- 反向代理必须覆写而不是追加客户端 IP 头；只信任 `TRUSTED_PROXY` 列表中的来源。
- 数据库迁移随服务启动自动执行（`AUTO_MIGRATE=true`），用 advisory lock 防止多实例并发迁移。
- 已应用的 migration 不可修改；只能新增后续文件。迁移不记录 checksum，改动历史文件会造成环境漂移。

## 6. 数据与媒体

- PostgreSQL 是唯一权威数据源；R2 只存媒体与备份，不作为运行时业务库。
- 从 v1/testapp 迁入时保留 `legacy_open_id`、`legacy_user_id`、`legacy_record_id`，用于对账与回溯。
- 用户照片与活动海报均为私有媒体；读取必须经过应用鉴权，不能把对象存储 URL 暴露给浏览器。
- 迁移工具、汇出格式、手机号唯一化与对账流程见 `docs/data-migration.md`。
- 生产数据不得导入开发库；staging 可用脱敏/测试数据。

## 7. 部署与发布

- **staging**：`https://next.nantou.love`，server2 香港 relay → server1 nginx → `127.0.0.1:8093`。
- **v2 生产**：尚未切换；`app.nantou.love` 仍是 v1。切换必须另行走查、备份与回滚演练。
- **CI 是硬闸门**：当前 HEAD 的 CI 必须为绿才能部署；红灯先修根因，不跳过、不 `[skip ci]`。
- 部署命令、systemd 服务名、证书同步、回滚与验收 smoke 见 `docs/runbooks/staging.md` 与 `yixianqian-deploy-v2` skill。
- 任何对外不可逆操作（生产部署、DNS 切换、删数据、发版）都需要使用者明确确认。

## 8. 用户界面与兼容性

- 用户端以 v1 为视觉与交互基准；改版不得顺带重设计。关键页面必须做旧版/新版截图对照与真实手机验收。
- 保留四个底部主入口与顺序：牵线、消息、活动、我的；主 tab 切换不整页刷新，保留各 tab 的状态与滚动位置。
- 管理端可不同，但必须使用简体中文、服务端权限与审计。
- 支持矩阵：iOS 14.5+/Safari 14.1+、Chrome/Android WebView 84+、Firefox 74+ 完整支持；低于此范围可能白屏或版面错乱，登录/注册/管理端仍可用。
- 用户 SPA 需要 JavaScript；登录、注册、管理端保持服务端可用。
- 视觉与交互验收标准见 `docs/adr/0007-reuse-v1-user-frontend.md`、`docs/product-spec.md` 的非功能章节；用户端以 v1 前端为基线，改版对照记录随对应变更提交。

## 9. 安全基线

- 密码使用 Argon2id；session 与一次性 token 在数据库中只存哈希。
- Cookie：HttpOnly、SameSite=Lax、生产 Secure；session 存服务端，可撤销。
- CSRF：HTML 表单 hidden token，API 写请求要求 `X-CSRF-Token`。
- 管理操作走服务端 RBAC 与 audit；最后一位 `super_admin` 受保护。
- 媒体私有、按权限串流；日志不记录密码、原始 token 或完整启用链接。
- 详细安全设计与检查项见 `docs/adr/` 与 `security-gate` skill。

## 10. 文档地图

| 文档 | 用途 |
|---|---|
| `docs/INDEX.md` | 文档权威顺序、每份文件的 owner 与更新规则；AI 先读这份 |
| `docs/STATUS.md` | 当前 staging/生产状态与已知缺口；唯一进度来源 |
| `docs/PROJECT_CHARTER.md` | 使命、最高原则、范围、架构硬约束；冲突时以此为准 |
| `docs/product-spec.md` | 用户端/管理端 Must-Have 与验收勾选 |
| `docs/milestones.md` | 里程碑与完成定义 |
| `docs/data-migration.md` | testapp 汇出、转换、导入与对账 |
| `docs/runbooks/staging.md` | staging 部署、验收、回滚 |
| `docs/adr/` | 架构决策记录（模块化单体、管理端 SSR、用户端复用 v1 SPA、session/CSRF、R2、activation、HK relay） |
| `docs/adr/0007-reuse-v1-user-frontend.md` | 用户端复用 v1 Vue 前端的决策与兼容性 |
| `docs/agent-task-template.md` | 给开发/审查角色的任务契约模板 |
| `yixianqian-deploy-v2` skill | v2 的 CI/部署操作闭环；v1 的旧 skill 不适用 |

## 11. 协作约定

- 项目文档、代码注释、用户界面、管理界面、commit/PR/Release 文字一律使用简体中文；分支名可用英文。
- 每个改动先定义可验证判准（命令 + 期望输出），再实现；没有证据不算完成。
- 用户端 UI/交互改动必须附旧版/新版截图与真实设备检查；管理端改动至少附权限与审计测试。
- 并行开发使用独立 git worktree；共享路由/模板文件由 Lead 整合，避免互相覆盖。
- 合并前 CI 绿；部署前确认当前 HEAD 的 CI 成功。
- 不在生产做实验；v1 保持可回滚，直到 v2 正式切换。

## 12. README 维护规则

本文件只写**半年内不应频繁变化**的内容：产品边界、架构、目录、开发入口、部署原则、文档索引、协作约定。

以下内容不要写进 README，写到对应文档：
- 功能完成度、里程碑进度 → `docs/product-spec.md`、`docs/milestones.md`
- 逐条路由、逐张表、逐个环境变量 → 代码、`.env.example` 或 OpenAPI/路由测试
- 每日部署状态、临时 IP、证书到期日 → `docs/runbooks/staging.md` 或运维记录
- 一次性迁移命令、临时脚本 → `docs/data-migration.md` / `tools/`

只有当上述**稳定事实**改变（例如架构换代、部署模型改变、支持矩阵调整）时才改本 README。
