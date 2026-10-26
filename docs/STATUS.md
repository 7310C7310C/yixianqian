# 当前状态（唯一进度来源）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-26
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
  - internal/db/recommend.go
  - internal/db/recommend_integration_test.go
  - internal/matching/recommend.go
  - internal/matching/onetoone.go
  - internal/matching/recommend_test.go
  - internal/httpserver/recommend_integration_test.go
  - internal/httpserver/social_pages.go
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
- 下一更新点：P0 身份证补填/服务端门禁变更部署与验收、下一次 staging 部署、M6 验收；不要等新阶段才开始更新。
- 事实基准：部署/数据事实于 2026-10-24 对 server1 只读复核；staging 已部署 `2d0e29b`；本次身份证/微信号变更在本地分支待 Review/部署（migration 6）。

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
| 部署 HEAD | `30e4b96` |
| `/healthz` version | `staging-30e4b96` |
| migration 最大版本 | `9`（`0009_perf_indexes_b7`） |
| `profile_photos` | 总数 `1028` / `has_face=1019` / `unchecked=0` |
| 人脸定时增量 | `/etc/cron.d/yxq-facebackfill` 已安装，每 10 分钟运行 |
| 自动部署 | `yxq-autodeploy.service/.timer` 已安装并**已启用**（CI+Docs 绿后自动部署，失败自动回滚）；脚本 `/opt/yixianqian-next/autodeploy.sh`，`deployed.sha=30e4b96` |
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

### 微信号交换与身份证补填（2026-10-24，待 Review/部署）

- 注册：微信号必填，5–64 个 ASCII 字母/数字/`_ - . @ +`；写入 `users.wechat`；身份证重复注册由 `id_card_hash` 唯一索引兜底。数据导入重复执行时，已有非空微信号不被源值覆盖（含身份证后四位/valid 标记）。
- 相互喜欢交换：资料微信号 + `contact_consent=true` 即就绪；缺失时 SPA「相互喜欢」页填写并显式同意。双方就绪后 mutual API 返回 `their_wechat`，并给双方各发一条含对方微信号的站内通知（`wechat_exchange`，按 pair+收件人去重）。未就绪返回空值 + `wechat_required`/`wechat_status`（`need_wechat`/`need_consent`/`waiting_other`/`ready`），匿名喜欢者身份不进 mutual 列表。
- 身份证补填：资料编辑按需显示「身份证号」；18 位、校验位、生日一致、唯一性校验；加密存储、审计只记后四位。服务端门禁见 §4。
- 证据：`TestIntegrationMutualWechatExchange`、`TestIntegrationMutualWechatDoesNotLeakAnonymousLike`、`TestIntegrationIDCardGateAndBackfill`、`TestIntegrationIDCardBackfillUniqueness`、`TestIntegrationObserverStillBlockedByIDCard`、`TestWechatIDValidation`、`TestBackfillIDCardRequires18Digits`。

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
- 静态资源版本（修复细节）：`admin_points.css`/`admin_user_detail.css` 的 `@import` 版本与 `assetVersion=7` 对齐；`TestStaticCSSImportVersionMatches` 断言两者一致，避免部署后仍命中旧 `admin.css`。
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

### 后端审计 P1-4/5/6/7/9 功能接线（2026-10-24，待部署）

- 邀请确认窗口（P1-5）：审核通过（HTML `POST /admin/users/{id}/approve` 与 `POST /api/admin/users/{id}/approve`）后幂等调用 `StartInviteConfirmWindowForUser`，以审核时点固定 7 天 `confirm_due_at`；`cmd/server` 启动时先回填「pending 且被邀请人已 active」的邀请，随后每小时调用 `SettleDueInvites` 结算。重复审批或重试不会移动到期时间，也不会重复发穗（`invite_reward:<id>` 唯一键）。回填按手机号 join 解析出的账号（`u.id`）启动窗口，兼容 `invitee_id IS NULL` 的历史邀请；单条冲突只跳过并返回首个错误，不中止整批。
- 取消报名退穗（P1-6）：删除 `SignupCancelRefundHook`（生产从未赋值），`CancelSignup` 提交后直接调用 `CancelPriorityOnSignupCancel`：距开始 ≥48h 退穗并把兑换单置 `refunded`，<48h 置 `cancelled` 不退穗；两种都会释放优先名额 cap。取消本身不受退穗失败影响，失败写入日志并由管理员/重试处理。
- 报名双向通知（P1-7）：两个报名入口提交后调用 `NotifySignupLikes`——喜欢报名者的人收到实名「你喜欢的 X 报名了 Y」，报名者喜欢的人收到匿名「喜欢你的人报名了 Y」，相互喜欢只发实名一条；去重键 `signup:<signupID>:<recipient>:real|anon`，匿名通知的标题/正文/payload 均不含报名者身份；沿用现有通知已读/全部已读关闭。
- 全站活动广播（P1-9）：受众新增「全站单身会员」（`status=active`、`account_status=active`、`account_kind=user`，排除观察员/待审核/已脱单/已退出/停用），保留报名者与观察员；工作台显示三类人数与去重合计，预览签名 token 同时绑定第三类 checkbox；发送改为单事务批量插入（`CreateNotificationsOnce`），全站量级不再逐人开事务。
- 额外实名喜欢兑换（P1-4）：`CreateLike` 在同事务读取 active `real_like` 订单数作为 `permanent`，`LikeQuotaFor` 同步；兑换后用穗购买的额度立即生效（`real_total/real_permanent` 与 `/points` 一致），退款/取消后立即失效，匿名额度不受影响。语义按现有 `real_permanent`（永久每月 +1）实现，待 PM 最终确认。
- 证据：`TestIntegrationApprovalStartsInviteWindow`、`TestIntegrationStartPendingInviteWindows`、`TestIntegrationBackfillLegacyInvite`、`TestIntegrationBackfillSkipsBrokenInvite`、`TestIntegrationInviteConfirmWindowBoundary`、`TestIntegrationCancelRefundPriority`、`TestIntegrationSignupLikesNotifications`、`TestIntegrationAdminActivityBroadcastAudience`、`TestIntegrationAdminActivityBroadcastAllSingles`、`TestIntegrationExtraRealLikeQuota`、`TestAdminBroadcastRecipients`；`make ci` EXIT=0；`go test -race -p 1 ./internal/db ./internal/httpserver` 全量绿。
- 仍未接线（P1-5 余项，待 PM）：`IsHistoricalParticipant` 注册时校验、封禁/拒绝时 `ClawBackInvitesForInvitee`。

### 每周推荐位（D10，2026-10-24，待部署）

- 规则：按 v1 `lib/recommend.py` 移植到 `internal/matching/recommend.go`——推荐位总数 `max(7, 必显数)`、算法位至少 3 个、每周保留 4 个（先被必显占）、必显（匿名喜欢我的异性单身）排最前且最近优先、ISO 周跨年连续；相似度 100 分制复用 `matching.MatchScore`（爱好 25/运动 15/性格 15/学历 15/MBTI 15/同城 10/同教堂 5，缺失中性 0.5）。
- 生成：`db.GenerateWeeklyRecommendations` 每周一（ISO 周切换）换批，同周重跑只「剔除失效 + 补空位」；`cmd/server` 启动补跑 + 整点检查，不新增常驻进程；事务 + advisory lock + 幂等（名单未变不重写、不刷 `created_at`）。
- 数据：迁移 `0006` 就地修正 `weekly_recommendations` 主键为 `(week, user_id, position)`、新增 `target_user_id`；`pinned_by_user_id` 只存服务端必显原因，API 与管理台都不读，不暴露「谁暗恋谁」。
- 读取：卡片页把本周推荐对象整段置顶（必显在前），`OrderCards` 保持给定顺序；`CardView.recommended` 与 `/legacy/api/cards` 的 `recommended` 已接线，SPA 既有「推荐」角标现在会实际出现。
- 管理端：`/admin/matching` 新增只读汇总（覆盖人数/条目数/生成时间 + 最近 20 人）与「重新生成本周名单」按钮；权限 `matching.run`、CSRF、审计 `admin.recommend.run`；必显原因不展示。
- 已知口径：观察员与性别未填用户不生成名单；推荐对象仍需在卡片候选池（有照片、符合筛选）里才会显示；候选不足时名单可短于 7 个。
- 证据：`TestRecommendBuildSlots`、`TestRecommendNextAlgoRotatesKeepFour`、`TestRecommendNextAlgoPinnedFourKeepsNoAlgo`、`TestRecommendNextAlgoPinnedOneKeepsThree`、`TestRecommendNextAlgoSameWeekKeepsWholeBatch`、`TestRecommendNextAlgoDeduplicatesPinnedFromCandidates`、`TestRecommendWeekKeyISOAcrossYear`、`TestRecommendMatchScore*`、`TestIntegrationWeeklyRecommendations`、`TestIntegrationRecommendationAdminPage`、`TestIntegrationWeeklyRecommendationsOnCards`；staging 未部署。

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
- 静态资源版本：`/static/*` 带 `?v=` 的响应为 `public, max-age=31536000, immutable`，裸路径 `max-age=3600`；模板与旧 SPA 引用的 `app.css`/`shell.css`/`admin.css`/`admin.js`/`admin_matching.js`/`app.js` 均带 `?v=`（唯一来源 `httpserver.assetVersion`，模块 CSS 的 `@import` 同值）。**任何 `web/static/**` 变更必须同步提升该常量**，否则 1y immutable 不可逆。

### 质量门

- `make ci` = fmt-check + docs-check + vet + 单测 + `tools/` Python 测试 + build；`tools/test_browser_call_arity.py` 用 AST 校验 `tools/browser/*.py` 的调用参数与定义一致（smoke 删形参后漏改调用点这类错误在 CI 即报）。

### 性能批次1（2026-10-25，实现待审）

- 用户端 hot path：`/legacy/api/home` 两段式候选（只排 id、按页取档案）、消息计数按页收敛、quota/praise 每请求一次、home 默认 20 张卡；目标 home p95 ≤40ms（改前 137ms）。
- 微信号交换通知移出 GET：写入路径触发 + 启动/每小时回填；GET 只读。
- migration `0008_perf_indexes.sql`：`likes(initiator_id,status)`、`notifications(user_id) WHERE read_at IS NULL`、`users(id) WHERE status='active' AND account_kind='user' AND account_status='active'`（`users_card_pool_idx`），只加索引不改数据。
- 边缘/传输 nginx 已于 2026-10-25 apply 到 staging：server1 `20261025-0315`；server2 relay `20261025-0320` + snippet `20261025-0322`（首次因 `ssl_session_cache` zone 冲突 `nginx -t` 失败并自动回滚，修正为 `YXQNEXT:20m`）。备份/回滚见 `.team/perf/batch1-report.md` §6。

### 性能批次2（2026-10-25，实现待审）

- 媒体：R2 去掉额外 `Stat()`（单次 GET + 首块再写头）；`/media`、`/cards/photo`、`/profile/photo`、活动海报、管理台照片统一 `ETag` + `private, max-age`（thumb 24h/orig 1h，immutable）+ `If-None-Match` 304；未改授权语义、未做 Range。
- 头像/资料九宫格改用缩图 URL（`photos_thumb`、`photo?size=thumb`）；卡片首图用 `photos_thumb`。
- SPA：内联 CSS/JS 拆为 `app.css`/`app.js`，壳 583KB→178KB（gzip 141KB→37KB）；库改 `defer`+`preload`；PhotoSwipe 按需加载；首图 eager/high、其余 lazy；骨架 + 首屏缩图早取。
- 静态：带 `?v=` 的响应 1y immutable，裸路径 1h；壳 `private, no-cache` + ETag。
- 本地（gzip 前置）实测：FCP 冷 6.1s→2.3–2.6s、热 4.24s→1.0–1.3s；LCP 仍约 8.4–9.1s（Vue 运行时编译主导，未达 2.5s，下一批需拆模板/预编译）。
- 未部署；证据与回滚见 `.team/perf/batch2-report.md`。

### 性能批次3（2026-10-25，实现待审）

- 根模板预编译：`tools/spa_precompile`（Node + `@vue/compiler-dom@3.4.27`，与 `vue.global.prod.js` 一致，package-lock 锁定）从 `index.html` 的 `#app` 抽取模板，生成 `web/static/legacy/app.render.js`（768KB / gzip 69.6KB，挂 `window.__YXQ_RENDER__`，无 eval）；`app.js` 根 options 使用它。改模板后必须 `npm run build`，`npm run check` 校验 hash；**批次4 起 Vue 为 runtime-only，文件缺失不再回退运行时编译，而是渲染可见错误页**（见批次4 段）。
- 首屏图：牌堆只解码当前+下一张照片（其余同尺寸占位），预取只取下一张卡首图；9 张→**2 个图片请求**。
- `assetVersion` 5→6→7；壳/静态/媒体缓存语义同批次2。`TestRenderManifestMatchesTemplate` 比对 `index.html` 的 `#app` 模板 sha256 与 `app.render.js` 头部，模板改了没重建会让 `make ci` 变红（Reviewer I-1）。
- 本地（gzip 前置，390×844，1.6Mbps/150ms/CPU×4，N=3 中位）：auth-cold FCP **2.60s**、LCP **6.12s**、524KB/16 请求；warm FCP **1.41s**、LCP **4.96s**；最长任务 1.77s（原 5.6s）。
- **已部署 staging**：`008bd9f`（自动部署 2026-10-25 06:04:55）；真实链路 390×844 实测冷 FCP 2.46s / LCP 5.90s、热 1.34s / 4.50s、521KB/16 请求；瀑布显示剩余瓶颈是 app.render.js 渲染长任务（2.2s）与 ~640KB JS 解析。详见 `.team/perf/batch3-report.md`。

### 性能批次4（2026-10-25，实现待审）

- 首屏数据内联：`/`、`/app` 壳由服务端预取与 `/legacy/api/home` **同一**聚合函数（`buildLegacyHomePayload`，handler 变薄包装）注入 `window.__YXQ_BOOTSTRAP__`（800ms 超时），SPA 启动优先消费、缺失即回退 API；首卡缩图在 `<head>` preload（`fetchpriority=high`）。匿名不注入；ETag 覆盖 `assetVersion`+用户+bootstrap 64bit hash，`Vary: Cookie`（**壳**，非 API）、`private, no-cache`，304 只复用逐字节相同的个人数据。
- 静态预压缩：`make static-gzip` 生成 `web/static/**` 的 `.gz`（gzip -9）随 `go:embed` 发布；Go 按 `Accept-Encoding` 回预压缩变体（`Vary`、编码分离 ETag、304），`make ci` 增加 `static-gzip-check` 与逐字节一致性测试。server2 边缘缓存 key 加入 `$yxq_static_enc`（`gzip;q=0` 判 identity），命中不再压缩，省掉 1 vCPU 的跨洋压缩成本。
- runtime-only Vue 3.4.27（147,796→90,713B）；`GaLoading` 改 `render()`，全仓无运行时模板。
- 渲染失败可见化：`app.render.js`/`vue.global.prod.js`/`vant.min.js` 任一缺失都渲染 `.boot-fail` 错误页且无 pageerror（Vant 改为 Vue 就绪后由 `app.js` 注入；smoke `run_render_missing` 三用例）。
- 壳数据最小化：`sanitizeBootstrap` 按模式递归剔除敏感键（含 `wechat`/`phone` 子串、`id_card`/`idcard` 前缀，覆盖互喜 `my_wechat`/`their_wechat`、`wechat_payment`、`IdCardHash`）；单测 + 互喜 ready 集成用例断言壳内无这些键与值，`/legacy/api/home`、`/legacy/api/likes/me`、`/legacy/api/profile` 仍 `no-store` 且字段不变。互喜页微信号预填来自 likes API（smoke `run_mutual_wechat_prefill`）。
- static-gzip 门禁改为「解压后与源逐字节一致」（与 Go 版本无关），并接入 `ci.yml` lint-build；`-strict-bytes` 仅本机用。
- 回滚：`git revert` + 重新构建部署（静态在二进制内）；nginx 配置回滚见批次1 §6。详见 `.team/perf/batch4-report.md`。

### 性能批次5（2026-10-25，实现待审）

- 壳内首卡 hero：`#boot-skeleton` 不再在挂载时立即删除，而是静态渲染与 `.cards-page/.swipe-card/.card-photo-wrap` **同一套几何**的首卡缩图（含品牌/底栏文本先出 FCP）；Vue 首卡图 decode+paint 后淡出，交互/切 tab/回退/深链/restore 立即移除；无卡/无图/加载失败退化为 shimmer 骨架，暗色/375/390/768/1280 适配。`assetVersion` 10→11。
- 首屏任务拆分：Vant 加载/执行前让位两帧，Vant 与 `mount` 分开；`onMounted` 后的数据加载/卡片页渲染再让出一帧（`yxqAfterFirstPaint`），避免把 hero 首图绘制压到 2–3s 长任务之后；新增 `yxq-mount`/`yxq-boot` 性能标记。
- 牌堆单卡窗口：复核 HEAD `c0506e3` 已是**只渲染 currentCard**（20 张 bootstrap 数据、DOM 160/161 节点、`.swipe-card=1`），不存在「20 张卡 DOM」；新增 `TestDeckRendersOnlyCurrentCard` 与 `app_hero_deck_smoke.py` 防止回归，不新增 ±1 预渲染 DOM（会反向增加节点）。
- 本地节流（390×844，1.6Mbps/150ms/CPU×4，N=3 中位）：冷 FCP 2.67s / LCP **4.66s**、暖 FCP 0.76s / LCP **1.71s**、最长任务 1.98s/0.79s、mount 0.44s/0.05s；1280×900：冷 2.45/4.70s、暖 0.68/1.51s、最长 1.85/0.75s、mount 0.49s/0.08s；CLS 0，字节/请求 685.7KB/15（冷）、29.5KB/16（热）。暖 LCP 达标；冷最长任务仍受 Vant UMD 执行（~1s）限制，需 P1 子集构建。
- 回归：`make ci`（Go 1.24.7）+ 5 个既有 Playwright smoke + 新 hero/deck smoke（390/1280/375/768、暗色、交互、无卡/首图失败降级）全 PASS。详见 `.team/perf/batch5-report.md`。

### 性能批次6（2026-10-25，本地实现待审，未 push/未部署）

- Vant 子集：`tools/spa_vendor`（dev-time esbuild + `vant@4.8.7`）生成 `lib/vant.min.js` 17 组件（button/icon/empty/loading/field/tag/radio/radio-group/popup/dialog/tab/tabs/cell/cell-group/tabbar/tabbar-item/switch）+ Toast/Dialog 命令式 API；CSS 同步子集，并移除外部 alicdn woff 回退（CSP 只允许 self/data）。JS 77.8→23.9KB gzip（實際提交 23,882B）、CSS 52.7→37.1KB gzip（實際 37,664B）。
- render 拆 chunk：`tools/spa_precompile` 从 26 个 page 模板生成 `app.render.js`（root+cards，768→182KB raw / 24,898B gzip）与 `pages/{likes,home,profile,detail,group}.render.js`；`<page-host>` 用根组件 proxy 调用页面 render，模板 ref（photoInput/gsReorderBtnEl 等）经 setupState 代理写回根 ref；chunk 首次进入时加载，失败显示 `.boot-fail`。`pages/pages.manifest.json` 记录 page→chunk/模板 sha/size；`TestPageChunksMatchTemplates` 与 `TestVantSubsetCoversTemplate` 为 CI 门禁（改模板/组件未重建即红）。
- 首屏：`app.css` 由服务端内联为 critical CSS（`<!--YXQ_CRITICAL_CSS-->`），Vant CSS `media=print onload` 非阻塞；下一张卡预取改为 `load` 后 5s + `requestIdleCallback`；`assetVersion` 11→12。
- 本地节流（390×844，1.6Mbps/150ms/CPU×4，N=3 中位，Reviewer FAIL 修复后）：冷 FCP 1.27s / LCP **2.74s**、热 1.21/2.73s；字节 457,282B（本地 HTML 未 gzip；按 staging gzip 口径约 314KB/13 请求）；冷最长任务 1.55s（目标 1.2 未达，剩余为 app.js 挂载/首卡渲染），热 1.66s。
- 修复（GaLoading 全局注册）后：6 个既有 smoke + `batch6_bundle_smoke`（含 ga-loading 探针）全 PASS；A/B 扩到 user_detail/activity_detail/group_detail/admin iframe/filter popup/profile_edit，全部像素一致；`TestGeneratedBundleHashes` + `make spa-check` 接入 CI，手改生成物即红；`make ci`（Go 1.24.7）与集成 0 FAIL。详见 `.team/perf/batch6-report.md` §6。

### 性能批次7（2026-10-25，已部署；见下方批次8/9）

- 通知 SSE 去轮询：`/legacy/api/notifications/stream` 改为 PostgreSQL `LISTEN/NOTIFY`（`yxq_notify`，payload 为 user_id），写入通知/标记已读时在同一事务 `pg_notify`；单实例 LISTEN 协程 fan-out（订阅者快照后再投递，避免与退订 delete 竞态）+ 断线指数退避重连 + 重连 resync；30s 兜底 ticker（监听未就绪 15s）、15s 心跳；无事件/无订阅者不做业务查询。语义（`unread` 事件字段、未读数、最新通知、EventSource 自动重连）不变。
- 通知时效性边界：**只有应用写入路径**（通知/已读）会即时 `pg_notify`；绕过应用直接写库（psql、外部脚本、数据修复）最坏要等兜底 ticker，实测 **25.7s** 才到客户端（设计 15–30s）。兜底间隔在连接建立时一次性确定（未连接 15s / 已连接 30s），hub 之后重连不会改已建连接；`app_integration_smoke` 直插场景会额外补一次 `pg_notify`。
- 会话 touch 5m→15m：只改 SQL 内条件与中间件常量，会话有效期/登出即时性不变；`last_seen_at` 误差上限 15m。
- home/cards 查询合并（gender 未填时仍按旧行为显示 self 卡 1 张 + `cards_total=1`，牌堆为空）：`LoadCardDeckState`（likes 双向 + praises + 本周推荐 1 条）、`LoadInteractionCounters`（额度 + 点赞状态 1 条）、`GetSocialProfilesWithMessageCounts`（分页档案 + 留言数 1 条）、`ListActivitiesForUser`（活动 + 我的报名 1 条）；home self 卡并入分页档案查询；`/legacy/api/cards` 无上游计数器时用 `LoadCardPageState` 一条 SQL；session 带 `id_card_valid`，消除门禁重复查 viewer。同用户同 query 的并发 home/cards 用 singleflight 合并（不缓存，不改新鲜度）。
- pgx pool：MaxConns 20 / MinConns 2 / `statement_timeout=5000`；迁移用独立 `pgx.Connect` 并 `SET statement_timeout=0`，`cmd/seed`、`cmd/facebackfill` 改用 `OpenMaintenance`（无 5s 上限）。
- migration `0009_perf_indexes_b7.sql`：`praises_active_initiator_idx`、`praises_active_target_week_idx`、`messages_target_active_idx`、`signups_user_activity_idx`、`notifications_user_created_id_idx`；staging 临时库 20 万级数据 EXPLAIN 实测（见 `docs/MIGRATION.md`）。
- staging PostgreSQL 参数已于 2026-10-25 **18:04:07**（server1）应用并重启：shared_buffers 512MB、work_mem 8MB、random_page_cost 1.1、effective_io_concurrency 200、jit off、track_io_timing on；`effective_cache_size` **维持默认 4GB**（planner 估算用，不涉正确性）。`/etc/postgresql` 未改，值在 `postgresql.auto.conf`（`ALTER SYSTEM`）；同实例所有 DB 共享，回滚 = `ALTER SYSTEM RESET` + 重启（无 `.bak` 属正常）。该变更在独立 Review 前已 apply，属**待审项**，见 `docs/runbooks/staging.md`「性能批次7」。
- 同机基线（旧代码 `7cd0f45`，app 直连 8093，N=30）：home p50/p95 52.8/76.0ms → 46.4/63.4ms、cards 41.2/62.4 → 35.1/54.0ms。新代码 fresh clone A/B（N=100，GOMAXPROCS=2，同一 DB/同一 cookie）：home p50 63.9→**44.9ms**、p95 101.1→**65.2ms**；cards 53.3→**29.4ms**、p95 85.4→**44.5ms**；p95 目标（60/40）仍差 5.2/4.5ms，p50 已达标（host 噪声大，p95 第二次跑升到 79/60）。管理台基线：`/admin?embed=1` 12.7/19.7ms、`/admin/users` 47.9/69.3ms、`/api/admin/users` 28.5/39.4ms、`/legacy/api/admin/group/activities` 20.6/43.5ms（新代码、warm）；均已是 `no-store` 且列表分页，暂不建议 304/缓存。详见 `.team/perf/batch7-be-report.md`。

### 性能批次8（2026-10-25，**已部署 staging-cf457f0**）

- 缩略图磁盘缓存：只缓存 `?size=thumb`，key=mediaID+thumb object key+size（替换自然失效，删除按前缀清理），
  有界 LRU（`MEDIA_CACHE_BYTES`，默认 256MiB）、授权先于缓存；`cmd/thumbwarm` 预热（并发≤8）。
  server1 实测：无缓存 p50 255ms、命中 p50 **9.9–16ms**；37 张/1.23MB 预热 6s。
- 壳关键 CSS 拆分：`tools/css_split` 从 app.css 生成首屏 `app.critical.css`（24,329B raw / **6,256B gzip**，213 块），
  壳内联 critical + 异步加载完整 app.css（`media=print onload` + noscript）；壳 gzip **38,153B → 25,380B（−33.5%）**，
  raw 181,005B → 116,137B。`make spa-check` 与 `tools/test_css_split.py` 门禁。
- 管理台边缘延迟归因：批次7 报的「edge p95 1.3s」是**测量口径**问题——从 server1 到边缘、每请求新建连接（冷 TLS/跨洋），
  同法测 home 也是 p95 936ms；在 server2 本地测 `/admin` p50/p95 **218/251ms**、`/admin/users` **274/314ms**、
  admin API **225/242ms**（均 ≤400ms 目标）。无需 nginx 改动。
- 修 `app_integration_smoke`：无 `PSQL_CMD` 时对 badge/SSE 用例打 NOTE 跳过而不是假红。

### 人脸与数据

- 人脸检测：本地 YuNet 优先、Haar 回退；上传后检测、注册后补检测、`has_face` 门禁；观察员账号豁免 `has_face`（身份证不豁免）。
- 回填：`cmd/facebackfill` 已部署；staging migration 5、`profile_photos` 1028/1019/0；cron 每 10 分钟增量。
- 迁移数据计数：users `1115`、activities `6`、signups `245`、profile_photos `1028`、points_ledger `131`（2026-10-23 复核）。
- 管理员：U-0006 为 `super_admin`，U-0001 为 `admin`；两人均 `observer_enabled=true`。

## 4. P0 身份证门禁：补填 + 服务端强制（已实现，待部署/验收）

**结论**：P0 死锁已按 PM 2026-10-24 方案实现：资料编辑提供身份证补填；服务端对 `id_card_valid=false` 的非豁免用户强制 403，补填成功后解除。Reviewer 2026-10-24 判 FAIL 的 B1（无生日账号补填死路）与 I1–I3 已在本地修复，代码尚未部署；部署前仍需 Reviewer 复审与 staging smoke。

- 补填：`legacyProfileEditable` 在 `id_card_valid=false` 时返回「身份证号」字段（普通用户附带其余可编辑字段，观察员只返回该字段）；提交后校验 18 位、校验位、`id_card_hash` 未被其他账号占用。资料生日为空时按证内生日补全并在同一次 `UpdateProfile` 写入（审计 `profile.birthday.from_id`）；已有生日与证内生日不一致仍拒绝。AES-GCM 写 `id_card_ciphertext`、`id_card_last4`、`id_card_hash`，置 `id_card_valid=true`；审计 `profile.id_card.backfilled` 只记后四位，不记明文。
- 15/18 位归一：注册接受旧 15 位号码时先归一为 18 位（`19` 前缀 + 校验位）再加密/HMAC；资料补填仍只收 18 位。同一张证无论 15/18 拼写都命中同一 `id_card_hash`。
- 服务端门禁：`withSession` 在注册门禁之后执行 `idCardGate`。`id_card_valid=false` 时（观察员同样适用），`/legacy/api/*` 仅放行首页、资料（含照片）、通知、改密、退出、版本/埋点与 mutual 微信号表单；其余返回 403 `id_card_required`。`/app` 与 `/admin` 页面入口返回 403 HTML（带 `X-Error-Code: id_card_required`）。
- 观察员：继续豁免 `has_face`（资料投影 `has_face=true`），但身份证不豁免；`id_valid=false` 时 `/legacy/api/cards` 等仍 403。资料页对观察员只暴露「身份证号」一个可写字段（其余只读），补填成功后解除门禁，避免只读账号死锁。
- 数据：staging 现有 `id_card_valid=false` 的 active/observer 用户仍可在部署后第一次进入资料页补填；其中 `birthday IS NULL` 的 4 个观察员与 100 个 active 测试账号会由证号推导生日，不再是死路。存量 `id_card_ciphertext` 仅 1 人，不批量解密回填哈希。
- 审查修复（2026-10-25，本地未 push）：B1 生日推导 + 集成测试；I1 15/18 位同哈希 + 非法 15 位拒绝；I2 推荐位/管理端测试可在共享库连续重跑；I3 迁移 runner 校验 `schema_migrations.name`（附 v5→7 与名冲突测试），本机开发库错误行已清理并升级到 7。
- 验收规格：`docs/product-spec.md` C4；证据见 `docs/DECISIONS.md` §27 与集成测试。

## 5. 已知缺口/未验收

- **人脸照片缺口**：`104` 个 `active` 普通用户没有 `has_face=true` 的照片，其中 `101` 个完全没有照片；可通过资料页上传修复，不是死锁（与身份证 P0 区分）。
- **媒体孤儿（P3）**：非属主删除最后一个 `profile_photos` 引用时，按安全规则保留 `media` 行与 R2 对象；当前无 GC，staging 量小（约 29 个共享 media），未造成越权访问。后续可加 refs=0 的离线清理任务。
- **内容合规**：腾讯云照片内容合规（宠物/卡通判定、活体）未实现。
- **裁切**：v1 黑边裁切未移植，`faces.tb/lr` 固定 `[0,0]`。
- **真机与性能**：真机 iOS/Android/微信、旧设备宽度、图片/首屏性能未完成。
- **运维演练**：备份还原、回滚演练、安全最终审查未完成。
- **E2E**：全流程 E2E（登录→卡片→喜欢→留言→活动→分组→结果→麦穗）未建。
- **验收签核**：`docs/product-spec.md` 仍 `0/83` 未勾选，未经 PM 签核；其中 E2 已按 §4/`docs/DECISIONS.md` §27 新方案实现待验收，D10 已于 2026-10-24 按 v1 原版补实现（见 §3），均待验收签核。不要从 0 勾选推断“未做”，也不要当作已完成。
- **生产切换**：v2 未切生产；`app.nantou.love` 仍是 v1。
- **管理员核对**：v1 `ADMIN_OPEN_IDS` 不在 Bitable 导出中，其余管理员需按 `docs/MIGRATION.md` 人工核对。
- **账号数差异**：迁移记录为 `1114`，staging 当前 `1115`，多出的 1 个账号来源待核对。

### 原「明确不做」条目（PM 2026-10-24，均已被后续决策取代）

- **E2 相互喜欢展示双方微信号**：原「不做」记录已被 `docs/DECISIONS.md` §27 取代：相互喜欢按双方就绪交换微信号，重新纳入验收。
- **D10 推荐位/每周推荐**：原「不做」记录已被 `docs/DECISIONS.md` §26 取代：v1 原版规则已移植（见 §3「每周推荐位」），`weekly_recommendations` 已接线，重新纳入验收。

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

### 性能批次9（2026-10-26，**已部署 staging-30e4b96**）

- 当前 staging：`/healthz=staging-30e4b96`、`deployed.sha=30e4b96`、migration max=9/9；批次7、8、9 均已部署（autodeploy 自动）。
- `/legacy/api/home`、`/legacy/api/cards` 增加 10s 进程内成功响应缓存（`READ_CACHE_TTL/ITEMS/BYTES`，默认 10s/1024 条/32MiB，任一为 0 关闭），
  home+cards **共享** 32MiB/1024 条预算（单条 256KiB，字节 LRU，键长计入；config 上限 clamp 64MiB）；
  键含用户/kind/viewRole/query + 全局 epoch；**全站写（排除 /static/、/healthz、埋点 track*/client-events）成功后 epoch++** 全员失效；
  错误/未授权不缓存；no-store 不变、Vary 实测为空。
- 缩略图缓存目录不可写时降级关闭（不再拒绝启动）；运行目录 `/opt/yixianqian-next/cache/thumbs`。
- 边缘：**不做 `/legacy/api/` 缓存**（key 含明文 session 会落盘，违反 token 只存 hash）；静态 gzip 变体沿用已部署配置并复核（gzip HIT 47,033B/0.87ms、q=0 落 identity）。

### 性能批次10 部署快照（2026-10-26）

- **已部署 staging**：代码 HEAD `9bc7888`，`/healthz=staging-9bc7888`；migration max=10/10，四索引存在；
  env 未变更（sha256 `e533a86b…`）；`server.prev=staging-e561949`。
- 首次部署 `staging-3cf68c5` 后发现 `assetVersion` 仍为 12，边缘 immutable 缓存继续返回旧
  `app.render.js`/`pages/*.render.js`；修正为 **13**（含 admin CSS @import）后重新部署，`?v=13` 强制新缓存键。
- 追加修复：消息 tab likes chunk 从「LCP 后」改为「挂载后优先预取」，staging 首次点消息 **1183ms**（其余 919–1140ms），
  `加载中` 0、CLS 0；LCP edge N=10 冷 1802ms（P95 2064）/热 772ms（P95 1244），bytes 315,567/19。
- 部署后 7 smoke + tab + 授权矩阵 16/16 PASS；staging 真实共享 media：B/C 共享、viewer 只 like C → B public 空、
  私有照片按 C 关系 200、候选卡图 200；卡图 naturalWidth>0。

### 性能批次10 hotfix（本地，待部署）

- Reviewer 复审发现 `assetVersion` 在静态变更后仍为 13、消息 tab 首点慢。本地修复：`assetVersion` **15**
  （admin CSS @import 同步）；新增 `tools/check_asset_version.py`：逐提交检查 base..HEAD（含「A 静态不 bump +
  B docs」）、工作区静态改动、聚合兜底，并要求 admin CSS `@import` 一致。接入 `make ci`/prepush 与 CI
  lint-build（`fetch-depth: 0`，push before / PR base），无需 Node；自测
  `tools/test_asset_version.py` 覆盖工作区未提交、两提交、仅 docs、bump 正向 4 形态。
- likes chunk 改为应用挂载后立即加载（不等 LCP/idle）；安静主机（load 0.24）N=10：
  冷 FCP/LCP **1178/1584ms（P95 LCP 2128）**、热 **674/784ms（P95 1020）**、CLS 0、442,665B/20；
  tab 首点 消息/活动/我的/牵线 **1491/1344/1325/1518ms**，`加载中` 0、CLS 0.0001。
- **staging 部署闸**：`deploy/autodeploy/autodeploy.sh` 要求代码提交含 `Reviewed-By:` trailer，缺失则拒绝并记录
  `missing Reviewed-By`；docs-only 豁免，`--force`/`YXQ_REQUIRE_REVIEW=0` 为例外；沙箱单测
  `deploy/autodeploy/test_autodeploy.sh` **66 PASS**。
- 7 smoke 本地最终全 PASS；`make ci`/test-race/integration（fresh DB）绿。**当前 staging 仍是 `staging-9bc7888`，
  本地 hotfix 代码 HEAD `898994f`（`197df08` 之后的门禁/部署闸提交）未 push/未部署**；本文件为其后 docs 快照。

### 性能批次10（2026-10-26，已部署 staging-9bc7888）

- **1B 渐进启动**：壳移除三个主脚本的静态 `defer` 与全部 script/style preload；新增 loader 在首卡 hero `load+decode` 并提交一帧后，按 `vue → app.render → app.min` 顺序注入执行（硬上限 2.5s；无 hero 600ms 后启动）。主脚本不 preload 是实测取舍：1.6Mbps 下 preload 与首图抢带宽，冷 LCP 反而高约 350ms；loader 在 hero 绘制后立即开始下载，`app_start` 与带 preload 版本基本一致。hero 在首帧按 bootstrap faces 套用与 app.js `computeFaceCrop` 相同公式，挂载后无裁切跳动；启动前点击只记 pending、不重放；`loadHome` 4s 兜底与管理台 iframe 不变；Vue/Vant/app.min 缺失均有可见错误页。
- **2A 照片授权**：`/cards/photo/{id}` 与 `/profile/photo/{id}` 共用默认拒绝策略，授权按画廊主人 `profile_photos.user_id`（共享 media 的 `media.user_id` 仅取对象）：本人、任一管理员角色、**任一方向的有效喜欢（含互喜）**、未取消的同活动报名（覆盖同组/同轮）、**任一方向消息的对手方**放行；牌堆候选由 cards/home 下发时按目标用户登记 per-viewer 15 分钟 grant（单 viewer ≤2000 目标、全局 ≤2000 viewer，单一有序集合 15m TTL + viewer LRU fail-closed）；匿名 401、无权/不存在 404、禁用账号 403。公开资料页仅按 viewer 与目标本人的关系（本人/grant/CanViewGallery(target)）返回照片 URL，不复用 media 级关系，否则空数组。`/admin` 受权媒体串流不变。
- **migration**：新增 `0010_photo_authz_indexes.sql`（`profile_photos(media_id)`、`likes(target_id,initiator_id)`、`signups(activity_id,user_id)`、`messages(author_id,target_user_id)`），只加索引；staging 克隆 EXPLAIN 单图授权 **14.3ms → 1.9ms**；cards 照片批量已在既有查询取回，grant 无 SQL，15 图无 N+1。
- **测试**：`make ci`（Go 1.24.7）EXIT=0；`make test-race` EXIT=0；`make test-integration` 在全新库 `yxq_b10_finaltest` EXIT=0（db 7.50s / httpserver 46.85s；migration max=10/10，四索引存在）；7 smoke（本地实例 `staging-b10test*` + staging 克隆库）全 PASS：public_home / spa_nav / tab_scroll / batch6_bundle / app_hero_deck / admin_native_activity / app_integration。授权矩阵测试覆盖本人/候选/陌生人/喜欢/互喜/同活动同组/消息/管理员/观察员/禁用/匿名/猜测 ID；新增 owner grant 翻页 60 候选×9 图 540/540、250 目标 5 页 page1 全 200、共享 media（media owner≠gallery owner）候选 200/陌生人 404/公开页 URL 200、跨 viewer、喜欢/消息/报名撤销、owner 禁用、observer 本人、disabled `/profile/photo` 403；`TestIntegrationLegacyProfileFlow` 改为陌生人 404 → 候选 200。
- **浏览器性能（server1 QEMU 慢机本地实例 A/B，390×844、1.6Mbps/150ms、CPU×4、N=5 中位；同库同 cookie；本地无边缘 JSON gzip，字节只作同机对比）**：

| 指标 | 旧 `staging-30e4b96` | 新 `staging-b10final` | 变化 |
|---|---|---|---|
| 冷 FCP | 584ms | 632ms | +48ms（≈8%，同机噪声；不视为稳定退化） |
| 冷 LCP | 2308ms | **1572ms** | −736ms |
| 冷最长任务 | 915ms | 1035ms | +120ms（记录项，PM 不作为本批阻断） |
| 热 FCP | 560ms | 528ms | −32ms |
| 热 LCP | 988ms | **828ms** | −160ms |
| 热最长任务 | 433ms | 365ms | −68ms |
| CLS | 0 | 0 | — |
| DOM 节点 | 177 | 174 | −3 |
| 传输字节/请求（本地） | 400,015 / 15 | 406,202 / 15 | +6.2KB（壳内 loader/crop；本地无边缘 JSON gzip；staging edge 基线 289,475 / 15，≤350KB） |

- **交替 A/B 复核（old/new 各 5 轮单跑、同机同 cookie）**：cold LCP 中位 old 1852 / new 1868、hot 中位 old 2188 / new 1140；主机与 R2 首图抖动大，cold 优势不稳定，但新代码 cold/hot 绝对值仍在目标内（new cold max 2412、hot max 1356，p95 容差内）。FCP 在交替 A/B 中 old 1004 / new 1156；clean N=5 为 old 584/560、new 632/528（冷/热），差异在噪声范围，未见稳定退化。
- **追加（首次 tab 无「加载中」）**：应用挂载后等 LCP 条目出现再 idle 预取（likes 优先）likes/home/profile/detail/group chunk；tabbar 守卫在 chunk 未就绪时保留当前页等待（4s 超时），`page-host` 改为无文字骨架，cards/home 数据加载态仅转圈。新增 `tools/browser/tab_first_load_smoke.py`：首次四 tab、快速连点、回退、深链 `#/likes`、管理台 iframe 全 PASS；`加载中` 文本 0 次、CLS noInput 0.0000。**慢机 N=10 复测（最终）**：冷 FCP/LCP 614/1144ms（P95 1396）、热 448/482ms（P95 592，max 592）、CLS 0；冷/热均达 ≤2.5s/≤1.2s（热 P95 592≤1320）。共享 media 修复 + hero 去 opacity 过渡 + bootstrap cards 20→3 + LCP 后预取共同生效。本地 430,741B/20；edge 估算 289,475+43.5KB≈333KB≤350KB。
- **staging edge 基线（部署前，`staging-30e4b96`，同慢机 N=5）**：冷 FCP/LCP **1948/3264ms**、最长 1530ms、289,475B/15；热 **1284/2348ms**、最长 961ms、7,414B/16。新代码尚未部署到 staging，edge 后值需部署后由 Auditor 复测；本批最终本地慢机冷 LCP 中位 1144ms（P95 1396）、热 482ms（P95 592），均达目标。
- **已知限制/待审**：grant 为进程内内存，多实例部署需共享 grant 或 HMAC 签名 URL；进程重启后首次图片请求可能回落关系 SQL/404（浏览器已缓存图片不受影响）。`/profile/photo` 对陌生人由原「可浏览即可取」改为 404 属预期行为变化。staging 部署前仍需独立 Reviewer + security-gate/Auditor；生产未授权。
