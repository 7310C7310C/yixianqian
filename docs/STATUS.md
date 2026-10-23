# 当前状态（唯一进度来源）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-23
sources:
  - cmd/**
  - migrations/**
  - deploy/**
  - internal/httpserver/routes.go
  - internal/httpserver/legacy_api.go
  - internal/httpserver/admin_*.go
  - internal/httpserver/grouping_admin_routes.go
  - internal/httpserver/grouping_view.go
  - internal/httpserver/onetoone_admin.go
  - internal/httpserver/onetoone_admin_view.go
  - internal/httpserver/legacy_activity_admin_routes.go
  - internal/httpserver/legacy_activity_admin_view.go
  - internal/db/admin_activity_legacy.go
  - internal/db/admin_activity_settings.go
  - internal/db/grouping.go
  - internal/db/grouping_admin.go
  - .env.example
-->

- 更新日期：2026-10-23
- 维护者：项目维护者
- 下一更新点：P0 身份证门禁决策、下一次 staging 部署、M6 验收；不要等新阶段才开始更新。
- 事实基准：以下部署/数据事实于 2026-10-23 对 server1 只读复核；代码基线 `main` @ `a2ce99a`。

## 1. 阶段

M0–M5 代码已交付，M6 未完成；进度只以本文件为准。

| 阶段 | 内容与状态 |
|---|---|
| M0 基础 | Go/Postgres/R2、activation/login/session/media、staging、server2 relay、CI 已完成 |
| M1 Schema 与迁移 | schema/导入/U-0006 登录完成；对账报告未归档、账号数差异待核对；`docs/product-spec.md` M4 对账验收未通过（口径见 `docs/MIGRATION.md`） |
| M2 用户核心 | 资料/照片/门禁、卡片、喜欢/点赞/留言、通知已交付，验收未签核 |
| M3 活动与匹配 | 活动/报名/签到、分组志愿、一对一必聊/已聊已交付，验收未签核 |
| M4 麦穗与观察员 | 麦穗账本/邀请/兑换、观察员切换、公开页已交付，验收未签核 |
| M5 管理端 | 审核、活动工作台、匹配控制台、麦穗、举报/反馈、审计、RBAC 已交付，验收未签核 |
| M6 成品验收 | **未完成**：真机 iOS/Android/微信、旧设备宽度、性能、备份还原、安全最终审查、全流程 E2E、PM 签核 |

M6 进入条件：上述验收项完成，`docs/product-spec.md` 有证据地签核，且 P0 身份证门禁已关闭；
在此之前不得宣布“用户功能完成”或切换生产。

## 2. 当前部署快照（staging）

| 项目 | 值 |
|---|---|
| 域名 | `https://next.nantou.love`（server2 HK relay → server1） |
| 部署 HEAD | `a2ce99a` |
| `/healthz` version | `staging-a2ce99a` |
| migration 最大版本 | `5` |
| `profile_photos` | 总数 `1028` / `has_face=1019` / `unchecked=0` |
| 人脸定时增量 | `/etc/cron.d/yxq-facebackfill` 已安装，每 10 分钟运行 |
| nginx 请求体上限 | server1 `next.nantou.love` 与 server2 `relay-next.conf` 均为 `96m` |
| 账号数 | 用户 `1115`：`active=1111`、`observer=4` |
| v2 生产 | 未切换；`app.nantou.love` 仍是 v1 |

## 3. 已实现/可用

### 用户端（staging）

- v1 Vue 3 + Vant SPA，由 Go `/legacy/api/*` 提供数据：四 tab、卡片、喜欢/点赞/留言、活动/分组/一对一、资料、麦穗、观察员切换已接入。
- 通知：未读数字红点；`GET /legacy/api/notifications/stream` 推送未读/最新通知变化，EventSource 不可用时轮询兜底。
- 登录/启用/注册/密码：Go SSR；改密为 SPA 内 Vant 子页，离开时还原滚动位置。
- 公开首页：匿名访问 `/` 返回 200 服务端页面（品牌介绍、人工审核/匿名喜欢/动态通知、注册主按钮与登录入口、飞书老用户先设密码指引、飞书入口链接）；只保留入口链接，不展示二维码图片。登录后 `/` 仍由旧 SPA 入口。
- 活动管理（v1 原版 SPA 页面）：`/legacy/api/admin/*` 兼容层与 SPA 入口、同步执行分支均已接入；「我的→管理」同时保留「👥 活动管理」（v1 卡片/控制台/名单/签到）与「🛠️ 管理后台」（SSR iframe）两个入口，`group_detail` 页头可打开「✏️ 编辑」iframe。
- 管理台只经 App 内同源 iframe 全屏层承载（`/?admin=1` overlay），返回栏关闭回到原 tab；浏览器顶层 `/admin` 一律 303 到 `/?admin=1`，由 SPA 自动打开 overlay。

### 管理台（SSR，仅 iframe 承载）

- 入口与框架：管理台导航（总览/用户/活动/匹配/麦穗/举报/反馈/角色/审计）；顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`（不渲染顶层页面），管理台只经 SPA 内 `/?admin=1` 打开 iframe overlay 使用；受权照片/海报原图串流是显式例外。
- 用户：审核通过/拒绝、用户详情与受权照片串流、停用/启用、临时密码。
- 举报与反馈：列表与处理。
- 活动：列表、创建/编辑、活动工作台（海报上传与私有串流、报名/签到名单与搜索/出席筛选/分页、现场签到、站内广播预览+服务端签名确认、快速设置）。
- 匹配：`/admin/matching` 清单，分组控制台与一对一会话控制台（预览/执行/重置/轮次结果/名单/聊天概览）；沿用 `matching.run` 权限、CSRF 与审计。
- 麦穗：`/admin/points` 总览、调整、流水、邀请、兑换、心愿、推荐；调整向用户写入站内通知。
- 审计：`/admin/audit` 查看与 `/admin/audit.csv` 导出。
- 角色：RBAC 角色指派；最后一位 `super_admin` 受保护。
- 响应式：640–999px 列表卡片化、1280px 表单限宽、390px 单列可读。

### 质量门

- `make ci` = fmt-check + docs-check + vet + 单测 + `tools/` Python 测试 + build；`tools/test_browser_call_arity.py` 用 AST 校验 `tools/browser/*.py` 的调用参数与定义一致（smoke 删形参后漏改调用点这类错误在 CI 即报）。

### 人脸与数据

- 人脸检测：本地 YuNet 优先、Haar 回退；上传后检测、注册后补检测、`has_face` 门禁；观察员账号豁免 `has_face`（身份证不豁免）。
- 回填：`cmd/facebackfill` 已部署；staging migration 5、`profile_photos` 1028/1019/0；cron 每 10 分钟增量。
- 迁移数据计数：users `1115`、activities `6`、signups `245`、profile_photos `1028`、points_ledger `131`（2026-10-23 复核）。
- 管理员：U-0006 为 `super_admin`，U-0001 为 `admin`；两人均 `observer_enabled=true`。

## 4. P0 上线阻断：身份证门禁死锁（必须先决策，不改代码绕过）

**结论**：绝大多数迁移用户进入 SPA 后会被全屏身份证门禁拦住，而资料编辑页没有身份证补录字段，形成死锁；当前不能宣称“主路径可用”。

- 数据（2026-10-23 staging）：`active` 用户 `id_card_valid=false` 共 `1103`（另有 `observer` 4 个 false）；`active` true 仅 `8`；`id_card_last4` 为空 `1105`；有 `id_card_ciphertext` 仅 `1`。
- 成因链：
  1. `migrations/0002_domain.sql` 的 `id_card_valid` 默认 `false`；
  2. `tools/migrate_testapp.py` 只写 `id_card_last4`/`id_card_valid`/`id_card_present`，不写 `id_card_ciphertext`；
  3. 只有注册流程写密文（`internal/httpserver/register_routes.go` + `internal/db/register.go`）；
  4. 管理员批准不会修复：`internal/db/admin.go` 仅在 `id_card_ciphertext IS NOT NULL` 时置 true；
  5. 用户端可编辑字段（`internal/httpserver/legacy_profile_helpers.go`）没有身份证号；管理端详情只读展示；
  6. 用户 SPA 读取 `id_valid=false` 后启动全屏提示、阻止切 tab，并跳转资料编辑页。
- 影响：`1103` 个 `active` 迁移用户进入 SPA 即被拦截，无法通过正常路径补录或放行；`observer` 也不豁免身份证。
- PM（使用者）已裁定（2026-10-22）：不 grandfather、不豁免 observer；补录/放行方式未决。
- 待 PM 选择的选项（不在本文替 PM 决定）：
  1. **补录**：新增用户自助或管理员代录身份证的写入/复核路径（代码任务，需安全评审）；
  2. **grandfather**：打标 + 审计后放行（PM 已否决，保留为决策记录）；
  3. **人工放行**：按批次人工核对并记录放行依据（需审计与可回滚口径）。
- 验收规格：`docs/product-spec.md` C4；在 PM 决策前，本项保持 P0 上线阻断。

## 5. 已知缺口/未验收

- **活动管理 SPA 待部署验收**：入口、同步执行分支与「✏️ 编辑」iframe 已在代码接入；staging 仍运行旧 binary，部署后按 runbook 跑 `tools/browser/admin_activity_native_smoke.py` 留真实浏览器证据。

- **人脸照片缺口**：`104` 个 `active` 普通用户没有 `has_face=true` 的照片，其中 `101` 个完全没有照片；可通过资料页上传修复，不是死锁（与身份证 P0 区分）。
- **内容合规**：腾讯云照片内容合规（宠物/卡通判定、活体）未实现。
- **裁切**：v1 黑边裁切未移植，`faces.tb/lr` 固定 `[0,0]`。
- **真机与性能**：真机 iOS/Android/微信、旧设备宽度、图片/首屏性能未完成。
- **运维演练**：备份还原、回滚演练、安全最终审查未完成。
- **E2E**：全流程 E2E（登录→卡片→喜欢→留言→活动→分组→结果→麦穗）未建。
- **验收签核**：`docs/product-spec.md` 仍 `0/83` 未勾选，未经 PM 签核；不要从 0 勾选推断“未做”，也不要当作已完成。
- **生产切换**：v2 未切生产；`app.nantou.love` 仍是 v1。
- **管理员核对**：v1 `ADMIN_OPEN_IDS` 不在 Bitable 导出中，其余管理员需按 `docs/MIGRATION.md` 人工核对。
- **账号数差异**：迁移记录为 `1114`，staging 当前 `1115`，多出的 1 个账号来源待核对。

## 6. 文档自动化

- 命令：`make docs-gen`（刷新生成物）、`make docs-check`（元数据/地图/链接/命令/生成物）、`make docs-check-stale`（push 前漂移）。
- CI：`.github/workflows/docs.yml` 对 push/PR 跑文档检查；`ci.yml` 的 `lint-build` 也包含文档一致性步骤。
- 入口：`AGENTS.md` 是唯一规则入口与文档地图；`docs/reference/REFERENCE.md` 是自动生成的机械事实。
- 本文件在每次 staging 部署、阶段验收与 P0/缺口变化时更新；`last_verified` 必须与复核日期一致。

## 7. 状态维护规则

- 本文件是唯一“当前进度”来源；README 只写稳定事实，`docs/product-spec.md` 的 `[ ]` 是验收目标而非实时状态。
- 不得无证据写“已可用”：部署/人脸/数据事实必须重新查询（healthz、schema_migrations、profile_photos、cron）。
- 人脸、`/admin` 入口、迁移计数等易漂移事实，以最近一次实测为准；历史文档与旧 runbook 不得当现状。
- 任何人可在证据齐全时更新“已实现/缺口”事实条目与 `last_verified`；不得改 PM 决策、
  `docs/PROJECT_CHARTER.md`、`docs/product-spec.md` 勾选或 `docs/DECISIONS.md` 已定决策。
