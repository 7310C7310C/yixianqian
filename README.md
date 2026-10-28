# 一线牵 v2

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-28
sources:
  - go.mod
-->

一线牵是面向教会单身青年的独立网页交友平台，替代 v1 的「飞书机器人 + 飞书 H5」。用户不需要飞书账号，使用手机号与密码登录；资料与业务数据存 PostgreSQL，照片存私有 R2。用户端复用 v1 的 Vue 3 + Vant SPA；Go 提供 `/legacy/api/*` 适配层与媒体代理；管理台使用服务端渲染（SSR），只经用户 SPA 的同源 iframe 使用。

## 架构

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

- 后端是 Go 1.24 模块化单体；PostgreSQL 是唯一权威数据源。
- R2 只存媒体，桶不公开；浏览器只拿应用路径 `/media/{id}`。
- 用户端复用 v1 SPA；Go 提供旧形状兼容 API、session 与媒体代理。
- 管理台是 Go `html/template` SSR；浏览器顶层 `/admin` 一律 303 到 `/?admin=1`，由 SPA 自动打开同源 iframe overlay。受权媒体串流是显式例外。
- 不引入 Redis、消息队列、微服务或 Kubernetes；当前正确性不依赖缓存。
- v1 仍在 `app.nantou.love`；v2 生产切换必须另行走查与回滚方案。

## 本地开发

前置：Go 1.24、PostgreSQL 16、Python 3；媒体需要一个 R2 或 S3 兼容 bucket（不配置则上传/读取会失败）。

```sh
cp .env.example .env
# 至少改 DATABASE_URL、SESSION_SECRET、R2_*；本机 HTTP 设 SESSION_COOKIE_SECURE=false
set -a; . ./.env; set +a
make run
```

- `.env.example` 是配置项的权威清单，不在 README 复制整张表。
- 测试账号：`make seed`；输出一次性启用 URL。
- 日常：每个 edit 后 `make fast`；每个 commit 前 `make ci`；push 前 `make prepush`。
- 集成测试需要 `TEST_DATABASE_URL`，`internal/db` 与 `internal/httpserver` 共用测试库，必须串行（`-p 1`）：

```sh
TEST_DATABASE_URL='postgres://yixianqian:yixianqian@localhost:5432/yixianqian_test?sslmode=disable' make test-integration
```

## 文档地图

| 文件 | 用途 |
|---|---|
| `AGENTS.md` | 唯一规则入口：工作流、门禁、安全红线、文档规则 |
| `README.md` | 本文件；稳定概览、架构与本地开发入口 |
| `docs/PRODUCT.md` | PM 权威：使命、非目标、硬约束、验收清单 |
| `docs/STATUS.md` | 唯一进度来源：当前部署、阶段、已实现 vs 已验收、P0/缺口 |
| `docs/DECISIONS.md` | 现行决策、理由与「不要做什麼」；历史在 git log |
| `docs/runbooks/staging.md` | staging 部署、回滚、smoke、人脸/腾讯、迁移附录 |
| `docs/reference/REFERENCE.md` | 自动生成的机械事实索引（勿手改） |
| `tools/README.md` | 生成物重建顺序与性能护栏命令 |
| `CLAUDE.md` | Claude Code 入口指针（生成物） |

## 维护规则

- 本文件只写半年内不常变的稳定事实；功能完成度、当前版本、P0 与缺口只放 `docs/STATUS.md`。
- 路由、env、migration、Makefile target、目录结构以 code/config/Makefile 为准；需要索引时看 `docs/reference/REFERENCE.md`。
- 历史与逐批证据看 `git log` 和 commit message，不写进 README/STATUS/runbook。
- 验收目标只放 `docs/PRODUCT.md`；不要把「已实现」写成「已验收」。
