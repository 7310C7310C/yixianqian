# 一线牵 v2

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-23
sources:
  - go.mod
-->

一线牵是一个面向教会单身青年的独立网页交友平台，替代 v1 的「飞书机器人 + 飞书 H5」。用户不需要飞书账号，使用手机号与密码登录；资料与业务数据存 PostgreSQL，照片存私有 R2。用户端复用 v1 的 Vue 3 + Vant 界面；Go 提供 API 适配层，管理台使用服务端渲染（SSR），活动管理在管理台 iframe 内复用 v1 原版 SPA。

> 本 README 只维护**稳定的项目事实、边界与入口**。
> 当前进度与上线阻断只看 `docs/STATUS.md`；验收看 `docs/product-spec.md`；
> 架构与产品决策看 `docs/DECISIONS.md`；迁移看 `docs/MIGRATION.md`；
> 部署看 `docs/runbooks/staging.md`。历史看 `git log`，不维护变更日志。
> 不要把逐功能清单、逐路由表或每日进度堆回这里，否则它会很快过期。

## 1. 产品边界

### 用户端
- 公开首页：匿名访问 `/` 渲染品牌介绍页（人工审核/匿名喜欢/动态通知、注册与登录入口、飞书老用户先设密码的指引与飞书入口链接）；登录后 `/` 进入用户 SPA。
- 手机号 + 密码登录；管理员可发一次性启用链接或临时密码。
- 注册、资料与照片、人工审核、牵线卡片、喜欢/点赞/留言、活动报名与签到、分组志愿、一对一必聊、麦穗/邀请/兑换、站内通知。
- 用户端的界面语言与操作习惯以 v1 原版为基准：四个主入口（牵线/消息/活动/我的）、卡片浏览、左右滑卡、喜欢/留言/报名/志愿/结果等流程不重新发明。
- 首发不依赖短信验证码；不做即时聊天，相互喜欢后交换微信号，站内留言保留。

### 管理端
- 用户审核与详情/照片、活动与广播、报名/签到名单、分组/一对一控制台、麦穗与兑换、举报/反馈、审计与 CSV 导出、RBAC。
- 管理台以运营效率为先，可自行设计，不受 v1 视觉限制；但同样使用简体中文、服务端权限与审计。
- 管理台只经用户 SPA 的同源内嵌 iframe（`/admin?embed=1`）使用；浏览器顶层 `/admin` 一律 303 到 `/?admin=1`，由 SPA 自动打开管理台，不提供独立顶层页面。

### 明确不做
- 飞书机器人、飞书表单、Bitable 运行时、飞书 Drive 作为 v2 依赖。
- 微服务、Kubernetes。
- 重写用户端 UI（不做；用户端直接复用 v1 前端）；管理台不引入 SPA 框架与前端构建链。
- 为旧浏览器维护第二套完整用户 UI；如确有需要，另立决策。
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

- **后端 API**：Go 模块化单体；PostgreSQL + R2；管理台 `html/template` SSR。
- **用户端**：复用 v1 Vue 3 + Vant SPA；Go 提供旧形状兼容 API、session 与媒体代理。
- **数据库**：PostgreSQL 16；迁移随应用启动执行。
- **媒体**：R2 只做对象存储，桶不公开；浏览器只拿应用路径 `/media/{id}`，不拿 R2/Cloudflare 直链。
- **用户端前端**：v1 Vue 3 + Vant SPA，保留旧版 DOM/CSS/AJAX/tab 行为；不新写 React/Vue 应用。
- **管理台前端**：Go 模板 + 少量原生 JS；不引入 SPA 框架。管理台只经用户 SPA 的同源 iframe（`/admin?embed=1`）承载；浏览器顶层 document 访问 `/admin`、`/admin/...` 页面路由一律 303 到 `/?admin=1`，由 SPA 自动打开管理台，服务端不渲染顶层页面；受权媒体串流（照片原图、活动海报）是显式例外，document 也直接串流。活动管理在管理台 iframe 内复用 v1 原版 SPA（`/app?native_admin=1&p=group_admin`），其余管理页仍为 Go SSR。
- **无 JS**：用户 SPA 需要 JavaScript；登录、注册、启用、改密页仍保持服务端可用（无 JS 可完成）。管理台只经用户 SPA 内嵌入口（同源 iframe）使用，需要用户 SPA 的 JavaScript，不承诺全部管理操作无 JS 可用。
- **不引入 Redis**：当前正确性不依赖缓存或队列；需要时再评估。
- **外部依赖**：不引外部字体/CDN；前端资产随仓库本地提供。

网络硬约束：`next.nantou.love` 必须经 server2 香港 relay 回源，不要把 DNS 直指 server1，也不要开 Cloudflare 橙云；实测直连美国 IP 或橙云 IP 可能被运营商拦截。详见 `docs/DECISIONS.md`。

### 用户端浏览器范围

复用 v1 前端原样（不转译、不补 CSS gap fallback）的完整支持范围：

- iOS 14.5+ / Safari 14.1+
- Chrome / Android WebView 84+
- Firefox 74+

低于此范围可能白屏或版面错乱；登录、注册、改密页仍可用。若未来真实用户数据表明旧设备占比显著，再另立决策评估转译 + CSS 兼容层。

## 3. 仓库结构

```text
cmd/server/             HTTP 服务入口
cmd/seed/               本地/测试账号与启用 URL
cmd/token/              对既有或新用户发一次性启用 token
cmd/facebackfill/       批量人脸回填（运维/一次性）
internal/config/        环境变量解析与启动校验（唯一入口）
internal/db/            pgxpool、SQL、事务、迁移、领域仓储
internal/auth/          Argon2id、session、CSRF、限流
internal/media/         图片校验/缩图、R2 存储与读取
internal/face/          本地人脸检测、has_face 门禁与回填
internal/matching/      分组与一对一的纯算法
internal/platform/      时钟、日志、请求 ID、构建版本等基础设施
internal/httpserver/    路由、中间件、handler、模板渲染、各功能模块
web/templates/          管理台/服务端页面模板
web/static/             服务端页面静态资源
web/static/legacy/      v1 用户端 SPA 资产与兼容适配
migrations/             只增不改的 SQL migration
tools/                  数据迁移、人脸探针、浏览器 smoke、文档生成/校验
deploy/                 nginx vhost/allow/snippet 与 systemd 模板
docs/                   规则、状态、规格、决策、迁移、runbook、参考
```

功能模块通过 `init()` 注册自己的路由与模板；共享文件尽量少改，以减少并行开发冲突。

## 4. 本地开发

### 前置
- Go 1.24 或以上（以 `go.mod` 为准）
- PostgreSQL 16（14+ 应可；schema 未用 16 专属语法）
- Python 3（迁移工具、文档工具与部分 smoke 需要）
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
公开首页只保留飞书入口链接（`https://applink.feishu.cn/T996FhMkTERp`），不再展示二维码图片，
也没有二维码相关 env 或生成工具。

### 构建、测试、启动

```sh
make ci                 # fmt-check + docs-check + vet + 单测 + 工具测试 + build
make test-race          # -race 单测（排除 internal/db、internal/httpserver）
make test-integration   # 需要 TEST_DATABASE_URL；只跑 TestIntegration*
make docs-check         # 文档元数据/地图/链接/生成物检查
make docs-gen           # 刷新 docs/reference 与入口指针
make run                # 或 go run ./cmd/server
```

需要真实 Postgres 的集成测试：

```sh
TEST_DATABASE_URL='postgres://yixianqian:yixianqian@localhost:5432/yixianqian_test?sslmode=disable' \
  make test-integration
```

`make test-integration` 覆盖 `internal/db` 与 `internal/httpserver` 的 `TestIntegration*`；两者共用测试库，必须串行（`-p 1`）。

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
- 迁移工具、汇出格式、手机号唯一化与对账流程见 `docs/MIGRATION.md`。
- 生产数据不得导入开发库；staging 可用脱敏/测试数据。

## 7. 部署与发布

- **staging**：`https://next.nantou.love`，server2 香港 relay → server1 nginx → `127.0.0.1:8093`。
- **v2 生产**：尚未切换；`app.nantou.love` 仍是 v1。切换必须另行走查、备份与回滚演练。
- **CI 是硬闸门**：当前 HEAD 的 CI（含 Docs workflow）必须为绿才能部署；红灯先修根因，不跳过、不 `[skip ci]`。
- 部署命令、systemd 服务名、证书同步、回滚与验收 smoke 见 `docs/runbooks/staging.md`。
- 人脸检测（migration 0005）已于 2026-10-22 首次上线；后续常规部署只走 runbook「常规部署」；首次上线/重建步骤见 git 历史旧版 runbook，当前不要执行 pre-0005 回滚或全量回填。
- 任何对外不可逆操作（生产部署、DNS 切换、删数据、发版）都需要 PM（使用者）明确批准。

## 8. 用户界面与兼容性

- 用户端以 v1 为视觉与交互基准；改版不得顺带重设计。关键页面必须做旧版/新版截图对照与真实手机验收。
- 保留四个底部主入口与顺序：牵线、消息、活动、我的；主 tab 切换不整页刷新，保留各 tab 的状态与滚动位置。
- 管理台可不同，但必须使用简体中文、服务端权限与审计。
- 支持矩阵：iOS 14.5+/Safari 14.1+、Chrome/Android WebView 84+、Firefox 74+ 完整支持；低于此范围可能白屏或版面错乱，登录/注册/改密页仍可用。
- 用户 SPA 需要 JavaScript；登录、注册、启用、改密页保持服务端可用。未登录访问 `/` 是服务端渲染的公开首页；登录后 `/` 才是用户 SPA。管理台只经用户 SPA 的同源 iframe 进入，因此也依赖 SPA 的 JavaScript；浏览器顶层访问 `/admin` 会 303 到 `/?admin=1`，由 SPA 自动打开管理台。
- 视觉与交互验收标准见 `docs/product-spec.md`；用户端以 v1 前端为基线，改版对照记录随对应变更提交。

## 9. 安全基线

- 密码使用 Argon2id；session 与一次性 token 在数据库中只存哈希。
- Cookie：HttpOnly、SameSite=Lax、生产 Secure；session 存服务端，可撤销。
- CSRF：HTML 表单 hidden token，API 写请求要求 `X-CSRF-Token`。
- 管理操作走服务端 RBAC 与 audit；最后一位 `super_admin` 受保护。
- 媒体私有、按权限串流；日志不记录密码、原始 token 或完整启用链接。
- 详细安全设计与检查项见 `docs/DECISIONS.md` 与 `security-gate` skill。

## 10. 文档地图

| 文件 | 用途 |
|---|---|
| `README.md` | 本文件；稳定概览与开发入口 |
| `AGENTS.md` | 唯一规则入口与文档地图 |
| `CLAUDE.md` | Claude Code 入口指针（生成） |
| `docs/PROJECT_CHARTER.md` | 使命、范围、架构硬约束 |
| `docs/STATUS.md` | 当前状态、阶段、P0 与缺口（唯一进度来源） |
| `docs/product-spec.md` | 用户端/管理台验收清单 |
| `docs/DECISIONS.md` | 架构与产品决策合并记录 |
| `docs/MIGRATION.md` | testapp 数据迁移与对账 |
| `docs/runbooks/staging.md` | staging 部署、验收、回滚 |
| `docs/reference/REFERENCE.md` | 机械事实（生成，勿手改） |

## 11. 协作约定

- 项目文档、代码注释、用户界面、管理界面、commit/PR/Release 文字一律使用简体中文；分支名可用英文。
- 每个改动先定义可验证判准（命令 + 期望输出），再实现；没有证据不算完成。
- 用户端 UI/交互改动必须附旧版/新版截图与真实设备检查；管理台改动至少附权限与审计测试。
- 并行开发使用独立 git worktree；共享路由/模板文件由维护者整合，避免互相覆盖。
- 当前分支流程：只有 `main`；较大改动在 worktree 分支开发，由维护者本地合并后 push `main`；
  不使用 PR（除非 PM 要求）。
- 合并前 `make ci` 绿；部署前确认当前 HEAD 的 CI 与 Docs run 成功。
- 不在生产做实验；v1 保持可回滚，直到 v2 正式切换。

## 12. README 维护规则

本文件只写**半年内不应频繁变化**的内容：产品边界、架构、目录、开发入口、部署原则、文档索引、协作约定。

以下内容不要写进 README：
- 功能完成度、当前缺口、上线阻断 → `docs/STATUS.md`
- 验收勾选 → `docs/product-spec.md`
- 逐条路由、逐张表、逐个环境变量 → 代码、`.env.example`、`docs/reference/REFERENCE.md`
- 每日部署状态、临时 IP、证书到期日 → `docs/runbooks/staging.md` 或运维记录
- 一次性迁移命令、临时脚本 → `docs/MIGRATION.md` / `tools/`

只有当上述**稳定事实**改变（例如架构换代、部署模型改变、支持矩阵调整）时才改本 README。
