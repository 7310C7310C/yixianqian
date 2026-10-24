# 当前状态（唯一进度来源）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-24
sources:
  - cmd/**
  - migrations/**
  - deploy/**
  - internal/httpserver/routes.go
  - internal/httpserver/legacy_api.go
  - internal/db/admin*.go
  - internal/db/errors*.go
  - internal/httpserver/admin_*.go
  - internal/httpserver/templates.go
  - internal/httpserver/grouping_admin_routes.go
  - internal/httpserver/grouping_view.go
  - internal/httpserver/onetoone_admin.go
  - internal/httpserver/onetoone_admin_view.go
  - internal/httpserver/legacy_activity_admin_routes.go
  - internal/httpserver/legacy_activity_admin_view.go
  - internal/httpserver/legacy_points_admin_routes.go
  - internal/httpserver/legacy_points_admin_view.go
  - internal/httpserver/legacy_messages_report_handle.go
  - internal/db/admin_activity_legacy.go
  - internal/db/admin_activity_settings.go
  - internal/db/points_admin_legacy.go
  - internal/db/grouping.go
  - internal/db/grouping_admin.go
  - web/static/admin.css
  - web/static/admin.js
  - web/static/legacy/index.html
  - web/templates/pages/admin.tmpl
  - web/templates/pages/admin_activities.tmpl
  - web/templates/pages/admin_activity_edit.tmpl
  - web/templates/pages/admin_activity_detail.tmpl
  - web/templates/pages/admin_users.tmpl
  - web/templates/pages/admin_user_detail.tmpl
  - web/templates/pages/admin_matching.tmpl
  - web/templates/pages/admin_matching_group.tmpl
  - web/templates/pages/admin_matching_onetoone.tmpl
  - web/templates/pages/admin_points.tmpl
  - web/templates/pages/admin_reports.tmpl
  - web/templates/pages/admin_roles.tmpl
  - web/templates/pages/admin_audit.tmpl
  - web/static/admin_points.css
  - web/static/admin_user_detail.css
  - internal/httpserver/admin_ui_phase_bc_test.go
  - internal/httpserver/middleware.go
  - tools/browser/admin_ui_phase_bc.py
  - tools/browser/admin_native_activity_smoke.py
  - web/templates/partials/admin_nav.tmpl
  - web/templates/partials/admin_ui.tmpl
  - web/templates/layout.tmpl
  - tools/browser/admin_ui_phase_a.py
  - .env.example
-->

- 更新日期：2026-10-24
- 维护者：项目维护者
- 下一更新点：P0 身份证门禁决策、下一次 staging 部署、M6 验收；不要等新阶段才开始更新。
- 事实基准：部署/数据事实于 2026-10-24 对 server1 只读复核；代码基线 `main` @ `2d0e29b`（staging 已部署）。

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
| 部署 HEAD | `2d0e29b` |
| `/healthz` version | `staging-2d0e29b` |
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
- 站内导航：登录后的常规操作（四个 tab、活动详情、我的活动、麦穗、通知、反馈、报名、身份切换）都在 SPA 内完成，不触发整页导航；新版本只在右上角提示「有新版本，点此更新」（30 分钟冷却、输入中不打扰），401/会话失效先弹「登录已过期」由用户确认后再跳登录页；服务端壳注入构建版本，`GET /legacy/api/version` 返回同一版本号。
- 公开首页：匿名访问 `/` 返回 200 服务端页面（品牌介绍、人工审核/匿名喜欢/动态通知、注册主按钮与登录入口、飞书老用户先设密码指引、飞书入口链接）；只保留入口链接，不展示二维码图片。登录后 `/` 仍由旧 SPA 入口。
- 活动管理（v1 原版 SPA 页面）：管理台总览「活动列表」在 iframe 内直接打开 `/app?native_admin=1&p=group_admin`，复用原版 group_admin/group_detail/group_roster/checkin；`/legacy/api/admin/*` 兼容层与同步执行分支已接入。SSR 活动列表不再从导航暴露，活动工作台与新建/编辑表单保留（编辑仍从原版控制台进入）。
- 麦穗管理（v1 原版 SPA 页面）：`/legacy/api/admin/points/*` 兼容层已接入（总览/流水/兑换/邀请/单用户详情；调整、退穗、兑换单标记走 CSRF、`points.adjust` 与审计）；页面代码保留，但入口已撤回、暂不暴露。
- 举报管理（v1 原版 SPA 页面）：列表复用 `/legacy/api/messages/reported`，处理动作补 `POST /legacy/api/messages/{id}/handle`（`content.moderate` + CSRF + 审计）；页面代码保留，但入口已撤回、暂不暴露；`user.is_admin` 语义为「有任一管理角色」，与 `can_admin` 同值。
- 管理入口：活动管理经管理台 iframe 内的原版 SPA 承载；麦穗/举报原生化页面仍不暴露（「我的→管理」只有「🛠️ 管理后台」）。
- 管理台只经 App 内同源 iframe 全屏层承载（`/?admin=1` overlay），返回栏关闭回到原 tab；浏览器顶层 `/admin` 一律 303 到 `/?admin=1`，由 SPA 自动打开 overlay。

### 用户端审计修复（2026-10-24，UD-02/04/05/06/07 + 管理台广播/iframe 兜底，待部署）

- 活动时间（UD-02）：活动列表卡片显示开始时间（无时间显示「活动时间待定」），详情显示「🗓️ 活动时间：开始 – 结束」与地点；字段沿用 legacy 视图 `start_time`/`end_time`/`location`。
- 断网文案（UD-04）：`api()`、管理台 `gaApi()` 与上传 XHR 的网络层失败统一映射为「网络不稳定，请稍后重试」，业务错误原文保留；启动错误页不再出现 `Failed to fetch`。
- iOS 安全区（UD-06）：通用 `.back-nav` 增加 `env(safe-area-inset-top)`（含 `constant()` 回退），分组志愿页 sticky 搜索框同步下移；底部 tabbar/操作条保持 `safe-area-inset-bottom`。
- 照片删除（UD-07）：删除前二次确认，说明不可恢复；最后一张强提示会触发人脸门禁；删除按钮视觉仍 20px 圆点，命中区扩大到 44×44。
- 改手机号（UD-05）：资料页改号保存时弹出「当前密码」输入并带 `current_password`；`password_required`/`password_invalid`/`password_not_set`/`phone_taken` 给中文提示；成功后重拉资料详情。
- 原版活动控制台：控制台页头新增「📣 广播」，在站内 iframe 浮层打开 `/admin/activities/{id}#broadcast`，返回回控制台。
- 管理台 iframe 兜底：载入失败/被拦截/SSR 空白时显示「管理台加载失败」+「重试 / 返回管理台」，不再白屏；`/?admin=1` 入口保持可用。
- 证据（本地实例 + staging 克隆库，未部署）：`tools/browser/spa_user_fixes_smoke.py` 390/1280 PASS（活动时间、断网、安全区、照片确认、改号密码、广播往返、iframe 兜底、无 console error）；既有 `public_home_smoke`、`spa_nav_smoke`、`tab_scroll_smoke`、`app_integration_smoke` 全 PASS；`make ci` EXIT=0、`make docs-check` 0 errors。

### 管理台与后端审计修复（2026-10-24，ADM/UD，待部署）

- 用户列表分页（ADM-01）：`/admin/users` 支持 `page`/`per_page`（默认 50、上限 200），显示筛选后总数与上一页/下一页，搜索与状态筛选覆盖全量；越界页码夹到最后一页；列表快速操作保留筛选与页码。
- 活动编辑服务端校验（ADM-02/08）：分组与一对一不能同时开启；开启签到必须 4 位数字签到码；结束时间不得早于开始时间。DB 层 `AdminActivityInput.Validate` 与表单解析双重校验。
- 临时密码（ADM-06）：HTML POST 改为 303 PRG，密码经一次性内存 token 回显一次；刷新不再重复重置。`admin.js` 用 `POST /api/admin/users/{id}/temp-password` 就地展示，保留无 JS 降级。
- 管理台 iframe 错误页（ADM-04/05）：`/admin/**` 的 4xx/5xx、CSRF 与会话过期渲染管理台外壳（返回管理台 / 重新登录，`target="_top"`），未匹配路径返回管理台 404；不再落用户端错误页或 303 到 `/login`。
- 广播入口（ADM-09）：SSR 工作台头部与活动列表新增「📣 广播」，深链 `#broadcast` 由 `admin.js` 激活。
- 活动时间窗（UD-03）：状态非「报名中」或已过开始时间的报名、已过开始时间的取消被拒绝；自助签到要求活动进行中（未开始/已结束/已取消均拒绝），错误码 `activity_started`/`activity_not_started`/`activity_ended`。
- 修改手机号（UD-05）：`PUT /legacy/api/profile` 改号需 `current_password`（缺失 400 `password_required`、错误 400 `password_invalid`、无密码 409 `password_not_set`），保留唯一性 409 `phone_taken`，写 `profile.phone.changed` 脱敏审计；未改号时不要求密码。
- 同号判定（UD-05 修复细节）：资料更新的手机号比较改用 `auth.NormalizePhone`，与登录/注册同一套归一化；带 `+86`、空格或分隔符的同一号码不再误判为换号而要求当前密码，无法解析的值仍只匹配自身。
- 静态资源版本（修复细节）：`admin_points.css`/`admin_user_detail.css` 的 `@import` 版本与 `assetVersion=4` 对齐；`TestStaticCSSImportVersionMatches` 断言两者一致，避免部署后仍命中旧 `admin.css`。
- 证据：`TestIntegrationAdminUserPagination`、`TestIntegrationAdminTempPasswordPRG`、`TestIntegrationAdminActivityFormValidation`、`TestIntegrationAdminErrorPages`、`TestIntegrationPhoneChangeRequiresPassword`、`TestIntegrationActivityTimeWindowRules`、`TestAdminActivityInputValidate`、`TestAdminTemplatesRender`（admin_error）、`TestSamePhoneNormalizesLikeLogin`、`TestStaticCSSImportVersionMatches`。
- 前端契约：`.team/backend-admin-fixes-2026-10-24.md`（SPA 手机号输入与原生控制台广播按钮由前端 workstream 接入）。

### 媒体与上传（2026-10-24 修复，待部署）

- 共享 media 删除安全：资料照片删除只移除 attachment；仅当无其它 `profile_photos` 引用且 `media.user_id` 为调用者时才删 media 行与 R2 对象。迁移数据中一个 media 被多人引用时，不再跨用户误删或级联删他人照片。
- 上传原图入库前重编码：去除 EXIF/GPS/XMP/文本元数据，先按 EXIF 方向 auto-orient 再编码，JPEG/PNG 与缩图同口径；迁移存量原图不做批量重写（见 `docs/MIGRATION.md`）。
- 上传上限：`MEDIA_MAX_PIXELS` 默认 `12_500_000`，iPhone 12MP（4032×3024=12,192,768）可通过；单张 10MiB、单边 8000px 不变；注册表单理论上限 92MiB 与 nginx `96m` 一致。
- HEIC/HEIF 仍不支持（不加重依赖）：服务端 400 文案与 SPA 上传前检测均提示「先在相册转成 JPEG，或关闭高效」。
- 重编码不保留 ICC 色彩配置（JPEG APP2/PNG iCCP），按 sRGB 解释；属去元数据的已知取舍，真机反馈偏色再评估。
- 迁移工具已同步 12.5MP 上限，并在上传前重编码去 EXIF/GPS、按方向校正；迁移报告提示 `byte_size` 可能变化（见 `docs/MIGRATION.md`）。
- 证据：`TestIntegrationProfileSharedMediaDelete`、`TestIntegrationSharedMediaDeleteKeepsObjects`、`TestProcessStripsEXIFAndAppliesOrientation`、`TestProcessOrientationNonSquare`、`TestProcessPNGEXIFOrientation`、`TestProcessPixelLimitBoundary`、`TestProcessRejectsExactlyOnePixelOver12_5MP`、`TestProcessAcceptsIPhone12MP`、`TestUploadErrorDetailsHEICActionable`、`tools/test_migrate_testapp.py` 的 GPS/方向与像素上限用例。

### 管理台（SSR + 活动原生 SPA，仅 iframe 承载）

- 入口与框架：顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`（不渲染顶层页面），管理台只经 SPA 内 `/?admin=1` 打开 iframe overlay 使用；受权照片/海报原图串流是显式例外。
- 导航（Phase A，2026-10-23）：取消 9 项平铺 subnav；顶部固定「管理台 / 当前页 / 返回总览」，总览改为 4 个关键统计 + 用户与权限/活动/内容与安全/麦穗分组入口卡。设计系统在 `web/static/admin.css`（`--adm-*` token、44px 触控、390/768/1280 响应式、浅色 v1 粉；`html.yxq-embed-dark` 有对应深色 token 覆盖），`web/static/admin.js` 负责列表筛选、工作台 tabs/hash 深链与二次确认。
- 分组顺序（2026-10-24）：总览入口分组固定为 用户与权限 → 活动 → 内容与安全 → 麦穗，`CanPoints` 门槛不变；内容与安全不再是末尾宽幅分组，桌面四个分组按 2×2 排布。
- 活动入口（2026-10-24）：总览「活动列表」在 iframe 内跳到 `/app?native_admin=1&p=group_admin`，复用 v1 原版活动管理（列表/控制台/报名名单/现场签到）；总览不再有「匹配」分组或分组/一对一入口，两种匹配执行都在活动控制台内。`/admin/activities` 的 iframe 请求 303 到该原生地址，顶层 document 仍 303 `/?admin=1`；SSR 活动工作台与新建/编辑表单保留。
- 活动（SSR，Phase A）：列表（统计条 + 搜索/状态 chips + 活动卡）、新建/编辑（分组表单 + 手机 sticky 操作条）、工作台（模式卡 + 报名名单/签到/广播/设置 tabs + 行内签到 + 广播二次确认）；SSR 列表不再从导航暴露，路由/handler 契约/RBAC/CSRF/审计与表单字段名不变。
- 用户：审核通过/拒绝、用户详情与受权照片串流、停用/启用、临时密码。纵向权限（2026-10-24）：对 `super_admin`/`admin` 目标仅 `super_admin` 可重置密码/停用/恢复/审核；`approve`/`reject` 仅接受 `pending`/`rejected`，active 账号走 `user.manage`；handler 与 DB 双重校验，见 DECISIONS §23。
- 举报与反馈：列表与处理。
- 活动：列表、创建/编辑、活动工作台（海报上传与私有串流、报名/签到名单与搜索/出席筛选/分页、现场签到、站内广播预览+服务端签名确认、快速设置）。
- 用户/匹配/麦穗/内容/系统（Phase B/C，2026-10-24）：用户列表与详情、匹配总览与分组/一对一控制台、麦穗 7 个 tab、举报/反馈、角色、审计全部改用同一设计系统（`.admin-page-head` + `.admin-section` + `.admin-record-list` 记录卡 + 状态 chips + 空态/分页 + 44px 触控）；不再用 6–7 列表格承载多字段列表，768 宽不再出现按钮三行堆叠或日期折行。
- 顶栏身份块：总览右侧改为「盾牌图标 + 管理员 + 姓名」胶囊；staging 管理员姓名是单字「一」，旧裸文本看起来像空按钮（2026-10-24 修复）。
- 深色对比度：新增 `--adm-on-brand`（浅色白字 / 深色 `#2a1a1e`），实心主按钮、选中 chip/tab、徽标对比度由 2.17:1 提到 7.66:1（Chromium 实测）。
- 验收（2026-10-24，临时库 + 临时服务）：`tools/browser/admin_ui_phase_bc.py` 三视口（390/768/1280）逐页 + 流程截图 108 张，`failures=[]`；覆盖用户列表→详情→审核、匹配→分组控制台→一对一、麦穗调整、举报/反馈处理、角色指派/移除、审计筛选与 CSV 导出，无 console error、无页面/表格横向滚动；模板断言 `TestAdminPhaseBCTemplatesUseDesignSystem` / `TestAdminPointsTabsKeepFormContract` / `TestAdminTopbarIdentityChip`。证据目录 `.team/audits/admin-ui-phase-bc/`（未提交，属团队 artifact）。
- 验收（2026-10-24，staging `1a3144e`）：`tools/browser/admin_native_activity_smoke.py` 三视口（390/768/1280）PASS：管理台总览→活动列表→控制台（模式/签到码/名单/现场签到）→编辑（嵌套 iframe）→返回，无 console error、无页面/iframe 横向滚动；普通用户访问 native 地址退回首页。名单入口随活动模式出现（分组=未填写、一对一=未提交），缺失时记 NOTE skip，已区分「加载失败」与「数据为空」；截图 `/tmp/opencode/native-activity-live`（团队 artifact，未提交）。
- 复跑（2026-10-24，staging `2d0e29b`）：真实环境首屏引导/浮层退场会挡住点击；新增 `tools/browser/guide_guard.py` 统一关闭 `.swipe-guide-mask`、`知道了` 与残留 `.van-overlay`，点击前用 trial click 确认无遮挡，并等 `.embed-overlay` 等业务浮层退场。`admin_native_activity_smoke.py`、`spa_user_fixes_smoke.py`（新增 768 视口）三视口 PASS，截图 `/tmp/opencode/native-activity-live`（18 张）、`/tmp/opencode/user-fixes-live`（40 张）。
- 匹配：`/admin/matching` 清单，分组控制台与一对一会话控制台（预览/执行/重置/轮次结果/名单/聊天概览）；沿用 `matching.run` 权限、CSRF 与审计。
- 麦穗：`/admin/points` 总览、调整、流水、邀请、兑换、心愿、推荐；调整向用户写入站内通知。
- 审计：`/admin/audit` 查看与 `/admin/audit.csv` 导出。
- 角色：RBAC 角色指派；最后一位 `super_admin` 受保护。
- 响应式：390（iframe 手机）/768/1280 三档；390 单列、列表卡片化，桌面内容 1080 居中；关键触控区 ≥44px，无页面级横向滚动。
- 静态资源版本：`/static/*` 仍按 `public, max-age=3600` 下发，模板引用的 `app.css`/`shell.css`/`admin.css`/`admin.js`/`admin_matching.js` 统一带 `?v=`（唯一来源 `httpserver.assetVersion`，模块 CSS 的 `@import` 同值），部署后立即取新样式；改静态资源时同步提升该常量。

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

- **人脸照片缺口**：`104` 个 `active` 普通用户没有 `has_face=true` 的照片，其中 `101` 个完全没有照片；可通过资料页上传修复，不是死锁（与身份证 P0 区分）。
- **媒体孤儿（P3）**：非属主删除最后一个 `profile_photos` 引用时，按安全规则保留 `media` 行与 R2 对象；当前无 GC，staging 量小（约 29 个共享 media），未造成越权访问。后续可加 refs=0 的离线清理任务。
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
