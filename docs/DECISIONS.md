# 现行决策（DECISIONS）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-28
sources: []
-->

> 本文件只保留**今天仍然约束代码/部署/验收的决策**。每条写清：决策、理由、不要做什麼、验证入口。
> 被取代的决策只留一行索引；完整历史、实现细节、测试输出与逐批证据看 `git log`。
> 产品范围/验收勾选由 `docs/PRODUCT.md` 的 PM 所有；本文件不代替验收。

## 1. 架构与交付

### 1.1 模块化单体 + PostgreSQL + R2

- 决策：单一 Go binary（`cmd/server`），按 config/db/auth/media/httpserver/platform 分包；handler 不写 SQL，DB 不依赖 HTTP。
- 理由：团队与流量规模未知，微服务/队列/Redis 会增加部署与一致性成本。
- 不要做：不新增 Redis、消息队列、微服务或 Kubernetes；不以缓存作为正确性前提。
- 验证：`go test ./...`；部署只需 binary + PostgreSQL + R2。

### 1.2 用户端复用 v1 SPA；管理台 SSR + iframe

- 决策：用户端保留 v1 Vue 3 + Vant SPA 的 DOM/CSS/状态机；Go 提供 `/legacy/api/*`、session 与媒体代理。管理台用 Go `html/template` SSR，只经用户 SPA 同源 iframe（`/admin?embed=1`）使用。
- 决策：浏览器顶层 document 访问 `/admin`、`/admin/...`（含 `?embed=1`）一律 **303 到 `/?admin=1`**，由 SPA 自动打开 overlay；受权媒体串流（照片原图、活动海报）是显式例外，document 直接串流。
- 决策：管理台视觉沿用 `web/static/admin.css` 设计系统（粉色 Vant、44px 触控、390/768/1280、深色 token），不引入 SPA 框架；活动管理在 iframe 内打开 `/app?native_admin=1&p=group_admin`，复用 v1 原版列表/控制台/名单/签到。
- 决策：活动管理继续用 `/legacy/api/admin/*` 薄适配层 + 同步执行（`group/onetoone run` 直接回 result；不建 jobs 表、不恢复 202+job 轮询）；RBAC/CSRF/审计沿用。
- 不要做：不恢复顶层 `/admin` SSR 或 404；不新增第二套管理 UI；不重写用户端。
- 验证：顶层 303 `Location: /?admin=1`；iframe SSR 200；`/api/admin/*` 仍走同一套 RBAC/CSRF/审计。

### 1.3 公开入口固定 server2 HK relay

- 决策：`next.nantou.love` DNS 指向 server2（HK, `45.125.16.205`，DNS-only），server2 终止 TLS 并回源 server1；server1 origin 只允许 server2 + localhost。
- 理由：Cloudflare 橙云 IP 与 server1 美国 IP 均曾被运营商封锁；HK relay 是已验证入口。
- 不要做：DNS 不直指 server1；不开 Cloudflare 橙云；allow gate 用 `geo $realip_remote_addr`，不改回直接 `allow`。
- 验证：直连 origin 应 403；公网 `/healthz` 200；证书同步后 `nginx -t`。

### 1.4 文档治理：code/git 是事实，docs 只写判断

- 决策：路由/env/migration/Makefile target/目录结构以 code/config/Makefile 为准；历史以 git log/commit message 为准。docs 只保留意图、非目标、硬约束、验收、当前状态、现行决策、操作顺序与陷阱。
- 理由：AI 能直接读 code/git；docs 复制机械事实只会漂移并增加每次改动的文档税。
- 不要做：不在 docs 写批次历史、旧版本快照、逐条路由表、测试输出或重复的部署命令。
- 验证：`make docs-check` 的行数预算与「禁止历史标题」检查；fresh clone 能按 README/AGENTS 找到入口。

## 2. 认证、安全与媒体

### 2.1 Session、activation 与 CSRF

- 决策：session token 32 bytes CSPRNG，DB 只存 SHA-256；cookie HttpOnly/SameSite=Lax/Secure（可 env 关闭供本机 HTTP）；CSRF 用 HMAC 签名 double-submit，表单 hidden 字段、JSON API 用 `X-CSRF-Token`。
- 决策（activation）：`cmd/token` 发 32 bytes CSPRNG token，DB 只存 SHA-256，默认 24h 一次性；`ActivateUser` 列锁 + 交易写入 Argon2id credential、作废旧 token/session 并自动登录；链接即凭证，明文只输出一次。token 走 URL query，全站 `Referrer-Policy: no-referrer`、页面 `no-store`、access log 不记录 token。
- 决策（壳数据）：`/`、`/app` 壳的 bootstrap 必须递归剔除含 `phone`/`wechat`、以 `id_card`/`idcard` 开头的键；互喜微信号只从 `no-store` 的 `/legacy/api/likes/me` 取，壳只留非联系方式字段。
- 不要做：不把明文 token/session 写日志或边缘缓存；不在 cookie 里放权限；不把个人联系方式内联进可缓存的壳。
- 验证：登录/启用/退出轮换；所有写入（含 logout、上传）验 CSRF；`TestSanitizeBootstrapStripsSensitiveKeys`。

### 2.2 身份证门禁：不 grandfather、observer 不豁免

- 决策：迁移用户 `id_card_valid=false` 不自动放行；observer 只豁免 `has_face`，不豁免身份证。资料页可补填 18 位身份证（校验位、生日一致；资料生日为空时按证内生日补全并审计 `profile.birthday.from_id`、`id_card_hash` 唯一）。服务端对非豁免用户拦截 `/legacy/api/*` 与 `/app`、`/admin`，白名单放行首页、版本/埋点、资料（含照片/封面）、`/legacy/api/user/me`、通知、改密、退出与 mutual 微信号表单；其余返回 403 `id_card_required`。observer 资料页只暴露身份证号一个可写字段，补填后恢复只读。
- 理由：身份证曾是上线阻断；补填入口与门禁已解除死锁，但仍是 P0 验收项，不能伪造 `id_valid=true`。
- 不要做：不批量 grandfather；不手工改库绕过；不把明文身份证写日志/审计（只记后四位）。
- 验证：`TestIntegrationIDCardGateAndBackfill`、`TestIntegrationObserverStillBlockedByIDCard`；未实名账号 `/legacy/api/cards` 403。

### 2.3 管理台纵向权限

- 决策：临时密码、停用/恢复、审核通过/拒绝对 `super_admin`/`admin` 目标仅 `super_admin` 可执行；临时密码调用者还必须持有 `user.manage` 或 `super_admin`。`approve`/`reject` 只接受 `pending`/`rejected`，active 账号必须走 `user.manage` 停用/启用。handler 与 DB 双重校验。
- 不要做：不让 `admin` 重置同级/上级密码；不让 `reviewer` 停用 active 账号；不删除最后一位 `super_admin`。
- 验证：`TestIntegrationAdminPrivilegeBoundaries`、`TestIntegrationAdminRBAC`；审计只记角色与目标，不记密码/token。

### 2.4 媒体私有、上传去元数据

- 决策：R2 私有桶，浏览器只经 `/media/{id}`；上传只接受 JPEG/PNG，HEIC/HEIF 回 400 并提示转 JPEG；单张 10MiB、单边 8000px、像素上限 12.5MP、并发 1；原图与缩图入库前重编码，按 EXIF/eXIf 方向 auto-orient，去除 EXIF/GPS/XMP/文本元数据。
- 决策（删除）：资料照片删除只删调用者 attachment；仅当无其它 `profile_photos` 引用且 `media.user_id` 为调用者时，才删 media 行与 R2 对象；非属主删最后一个 attachment 时保留 media/R2（当前无 GC，记 P3）。
- 理由：照片是私人物品；不信任 Content-Type，必须 sniff + decode；iPhone 12MP（4032×3024）必须可通过。
- 不要做：不把 R2 URL/key 给前端或日志；不跳过重编码；不恢复 12MP 旧上限；不批量重写已入库历史原图。
- 验证：`TestProcessStripsEXIFAndAppliesOrientation`、`TestProcessAcceptsIPhone12MP`、`TestUploadErrorDetailsHEICActionable`。

### 2.5 照片授权：默认拒绝 + 短时 grant

- 决策：`/cards/photo/{id}`、`/profile/photo/{id}` 共用默认拒绝策略；授权对象是画廊主人 `profile_photos.user_id`。放行仅限：本人、任一管理员角色、任一方向有效喜欢（含互喜）、未取消的同活动报名、任一方向消息对手方。匿名 401；无权/不存在 404；禁用账号 403。
- 决策：牌堆候选由 cards/home 下发时按目标用户登记 per-viewer 15 分钟 grant（单 viewer ≤2000 目标、全局 ≤2000 viewer）；公开资料页只按 viewer 与目标本人的关系返回照片 URL。
- 不要做：不用「所有活跃异性」宽候选池；不把 grant 扩大到 media 级共享；未授权时返回 200 + 空 `photos`/`photos_thumb`/`photos_meta`，不得复用 media 级关系或其他画廊 grant；多实例部署前必须改共享 grant 或 HMAC 签名 URL。
- 验证：`TestIntegrationCardPhotoAuthorizationMatrix`、`TestIntegrationPublicProfileHidesUnauthorizedPhotos`；migration `0010` 四索引。

### 2.6 腾讯云照片合规：同步、fail-open、只影响 has_face

- 决策：`TENCENT_FACE_ENABLED=true` 时，上传照片压到最长边 1080 的 JPEG 后调用腾讯云 tiia `DetectLabel` + iai `DetectLiveFaceAccurate`；只有「标签命中动物（≥30）/明确卡通（≥80）+ 活体 `NoFaceInPhoto`」判为非人类。结果只影响 `has_face = 本地 YuNet 检出 && !blocked`，几何仍用本地检测。
- 决策：异常/超时/限频/缺凭证一律 fail-open；不重试（避免重复计费）；日志只记 `action/result/latency_ms/photo_id`。fail-open 的 skip 仍写 `face_checked=true`，因此 `unchecked=0` 不代表已真正判定；需纠正只能显式 `TENCENT_FACE_BACKFILL=true -all`。
- 决策：`facebackfill -all` 必须同时 `TENCENT_FACE_BACKFILL=true`，否则拒绝执行；任一腾讯开关为 true 时缺凭证也拒绝运行，避免静默撤销合规结论。
- 不要做：不用 Python/SDK 运行时；不因合规失败阻断上传；不把腾讯结果伪造人脸几何；不在未开预算时回填历史照片。
- 验证：`internal/tencentface` 单测、`TestIntegrationTencentComplianceBlocksHasFace`、`cmd/facebackfill/guard_test.go`。

### 2.7 照片每日配额

- 决策（PM 2026-10-28）：每账号每自然日最多 `PHOTO_DAILY_LIMIT` 张（默认 10，0 关闭）；日界用 `PHOTO_QUOTA_TZ`（默认 `Asia/Shanghai`）；超限 429 `photo_daily_limit`。单张上传在 media 写入后、腾讯判定前取 1 次；注册按提交张数一次性计入，额度不足整笔回滚；删除不返还。
- 理由：补齐 v1 `photo_quota.py` 的行为；腾讯判定维持同步，不新增队列。
- 不要做：不用服务器本地时区；不把注册做成部分建号；不因删除照片退额度。
- 验证：`TestIntegrationPhotoQuota*`、`TestIntegrationLegacyPhotoDailyQuota`；migration `0011`。

## 3. 产品行为

### 3.1 相互喜欢交换微信号

- 决策：注册必填微信号（5–64 个 ASCII 字母/数字/`_ - . @ +`）；资料 `wechat` + `contact_consent=true` 即就绪。只有双方都就绪，mutual API 才返回 `their_wechat`，并给双方各发一条含对方微信号的站内通知（按 pair+收件人去重）。
- 已知缺口：注册当前硬编码 `contact_consent=true`（v1 兼容），**尚无显式同意步骤**；这是合规口径未解决项，需 PM 决定是否补显式勾选。不得据此宣称「双方已明确同意」。
- 不要做：不在未就绪时泄露；不把匿名喜欢者身份带进 mutual 列表；不恢复飞书名片。
- 验证：`TestIntegrationMutualWechatExchange`、`TestIntegrationMutualWechatDoesNotLeakAnonymousLike`。

### 3.2 每周推荐位（D10）

- 决策：按 v1 `lib/recommend.py` 移植；推荐位总数 `max(7, 必显数)`、算法位至少 3、每周保留 4（先被必显占）、必显（匿名喜欢我的异性单身）排最前且最近优先、ISO 周跨年连续；同周重跑只剔除失效并补空位。
- 决策：`cmd/server` 启动补跑 + 整点检查，不新增常驻进程；管理端 `/admin/matching` 只读汇总 + 手动重跑（`matching.run` + CSRF + 审计 `admin.recommend.run`）；管理端与接口都不展示必显原因。
- 不要做：不新建第二套打分；不把 `pinned_by_user_id` 导出/进管理台；不因候选不足伪造名单。
- 验证：`TestRecommend*`、`TestIntegrationWeeklyRecommendations`、`TestIntegrationRecommendationAdminPage`。

### 3.3 活动按时间结束与签到

- 决策：用户活动 JSON 在读路径派生「已结束」：**仅当原状态为「报名中/已满员」且已到结束时间（`end_at <= now`）**；`can_signup`/`can_cancel=false`；新增 `can_self_signin` 综合签到开关、报名、签到状态与时间窗。小时级任务幂等把过期「报名中/已满员」活动落库为「已结束」；已结束/已取消也按 ended 隐藏用户入口。
- 决策：活动结束后用户侧 `group_flag`/`onetoone_flag=否`，分组/一对一 result/chat 返回 409 `activity_ended`/`onetoone_disabled`；数据库 flags 不改，管理台仍可管理。
- 不要做：不靠前端自行按 `end_time` 推算状态；不批量改写历史活动状态；不删除管理台入口。
- 验证：`TestCanSelfSigninBoundaries`、`TestIntegrationActivityAutoEndUserViews`、`TestIntegrationCloseEndedActivities`。

### 3.4 脱单 gate 只算未结束报名

- 决策：`HasActiveSignups` 与 `SetAccountStatus(matched)` 只统计「已报名」且活动状态为报名中/已满员/未开始、`end_at` 为空或未到的报名；已结束/已取消/过期报名不阻塞脱单。
- 不要做：不把历史报名当永久阻塞；不改变 matched 状态机其他校验。
- 验证：`TestIntegrationEndedSignupDoesNotBlockMatched`。

### 3.5 公开页扫描防护

- 决策：匿名 `/`、`/?login=1`（`/login` 303）、`/activate`、`/password`、`/register/done`、404、公开 GET 错误页、`/register` 表单页：响应体除 `<title>` 外 **0 汉字**；引用同源 CSS/JS 也 0 汉字；中文由 `home.js`/`public-copy.js`/`register.js` 注入。
- 决策：`/register` 由服务端生成 ASCII 文案目录（`data-copy-json`，非 ASCII 全 `\uXXXX`），option 的中文 value 由 JS 写入后按原契约提交；POST 失败重渲染也 0 汉字。POST 表单错误消息、内部 500、管理台错误页是明示例外。
- 决策：`/welcome` 已并入首页，`GET /welcome` 301 → `/`；首页只保留飞书入口链接，不展示二维码。
- 决策：大图 viewer 用 API `photos_meta` 定框；缩图先显、原图后台升级；无 meta 时回退自然尺寸，10s timeout/cancel 保留。
- 不要做：不把中文写回公开页源码；不恢复二维码；不恢复 `/welcome` 独立页；不给公开页引入含中文注释的 `app.css`/`shell.css`。
- 决策（2026-10-29）：独立 `/login` 页并入匿名首页登录弹框；`GET /login` 303 `/?login=1`（保留 `next`/`logged_out`/`activated`），`POST /login` 契约不变；弹框文案仍由 `home.js` 注入，首页保持 0 汉字。
- 决策（2026-10-29）：注册页「小天使二维码」由后台「内容与安全 → 小天使二维码」上传/更换；资产存 `site_assets`，公开只读 `/angel-qr`（5 分钟缓存）；与已废弃的首页二维码无关，首页仍不展示二维码。
- 决策（2026-10-29）：注册地区改省-市 / 市-区联动（值保持 `广东深圳`/`深圳南山`），行业/来源改 combobox，身份证即时校验并限 18+，手机限 11 位大陆号码；地区数据 vendored（2023，WTFPL）。
- 决策（2026-10-29）：注册校验每个字段只给一条通用错误（身份证不再提示校验位/证内生日/省份子原因）；房/车补充内容为条件字段；用户照片统一转 JPEG，二维码统一转 PNG，支持 JPEG/PNG/WebP/GIF/BMP/TIFF（HEIC 仍需转换）。
- 不要做：不恢复独立登录页；不把二维码放回首页；不把身份证改回选填（与门禁冲突）；不再按输入格式分叉存储图片。
- 验证：`TestPublicScanPagesAreHanFree`、`TestRegisterCopyCatalogIsASCII`、`TestParseRegisterRegionFields`、`TestRegisterRegionsEndpoint`、Playwright `public_scan_pages_smoke.py`。

## 4. 性能与前端构建

### 4.1 静态首载预算

- 决策：首载 gzip 预算在 `tools/perf/budgets.json`，每项为基线 +10%；超限即 CI 红。预算只能手工修改并走 Review，没有自动更新。
- 不要做：不通过改预算代替优化；不把 `app.css`/`app.min.js`/`app.render.js` 大段内联回壳；不退回 `vant.full.min.*`。
- 验证：`make perf-budget`、`make perf-guard-test`；详见 `tools/README.md`。

### 4.2 边缘 keepalive 与静态缓存

- 决策：server2 `upstream yxq_origin` keepalive、`proxy_http_version 1.1`、`proxy_ssl_session_reuse on`；server1 `upstream yxq_app` keepalive、`Connection ""`；`/static/` 用 gzip/identity 分键缓存；TLS session ticket/cache 开启。
- 不要做：不给 `/legacy/api` 加边缘 `proxy_cache`；`proxy_cache_key` 不含任何 `$cookie_`；只有 SSE location 允许关闭 buffering。
- 验证：`make perf-budget` 的 19 项 nginx 断言；`tools/perf/check_nginx_guard.py`。

### 4.3 SPA 生成物与版本

- 决策：`app.js` 由 `tools/spa_minify` 压成 `app.min.js`；根模板与页面由 `tools/spa_precompile` 预编译成 `app.render.js` + `pages/*.render.js`；Vant 用 `tools/spa_vendor` 子集；生成物提交仓库，部署/CI 不依赖 Node。
- 决策：任何 `web/static/**` 变更必须 bump `internal/httpserver/templates.go` 的 `assetVersion` 并同步 admin CSS `@import`；`make spa-check` 校验 manifest/sha/bytes。
- 决策（壳/首屏）：壳只保留启动旗标/版本/单块 ≤5KB inline script + critical CSS；`app.css`/`app.min.js`/`app.render.js` 外置；主脚本按 `vue → app.render → app.min` 在首卡 hero `load+decode` 后注入，不把大段 CSS/JS 内联回壳、不改回静态 `defer`。
- 不要做：不手改生成物；不在模板改动后不重建；不生成 sourcemap 到 `web/`；不破坏 classic script 语义。
- 验证：`make spa-check`、`make static-gzip-check`；详见 `tools/README.md`。

### 4.4 缓存、ETag 与热索引

- 决策：缩略图有界磁盘缓存（`MEDIA_CACHE_BYTES` 默认 256MiB，授权先于缓存）；原图/缩略图带 ETag + `If-None-Match` 304 + `private, max-age`；`/legacy/api/home`、`/legacy/api/cards` 共享 10s 进程内读缓存（TTL 10s、总 32MiB、单条 256KiB、字节 LRU），任何成功写请求后全局 epoch++；排除 `/static/`、`/healthz`、`/readyz`、`track*`、`client-events`，避免埋点刷穿缓存。
- 决策：`0008`/`0009`/`0010` 热索引只增不改；新增/改 home、cards、profile、notifications、照片授权查询必须附 `EXPLAIN (ANALYZE, BUFFERS)`。
- 不要做：不绕过 epoch；不把错误响应写进缓存；不扩大成跨用户键；多实例前先做 epoch pub/sub。
- 验证：`TestReadCache*`、`TestIntegrationLegacyHomeCardsReadCache`、`make perf-bench`。

### 4.5 顶层 SPA 虚拟历史栈

- 决策：顶层 SPA 默认虚拟历史栈；子页/浮层/应用内返回统一用 `pushHistory`、`hist.replace`、`handleBack`，不直接调用 `window.history.pushState/replaceState/back`；`native_admin` iframe 与 `?history=legacy` / `localStorage.yxq_history_mode='legacy'` 保持真实历史。
- 不要做：不在新增子页直接读写 `history.state`；不删除启动哨兵；不改 `native_admin` 内部历史语义。
- 验证：`tools/browser/no_history_smoke.py`；Via 真机进度条需 PM 复验。

### 4.6 通知 SSE

- 决策：`/legacy/api/notifications/stream` 用 PostgreSQL `LISTEN/NOTIFY`；只有应用写入通知/标记已读时在同一事务 `pg_notify`；保留 30s 兜底（未连接 15s）与 15s 心跳；绕过应用直插 DB 最坏等 25–30s。
- 不要做：不恢复 4s 轮询；不在无订阅者时查业务；多实例前需共享 fan-out 或 pub/sub。
- 验证：SSE 集成用例、`TestNotificationHub*`；直插数据后需显式 `SELECT pg_notify(...)`。

## 5. 被取代决策索引

| 旧条目 | 现行为 | 说明 |
|---|---|---|
| 10 / 11 / 12 | 13（现行 1.2） | 顶层 `/admin` 经历 SSR → 404 → 303；现行只认 303 + iframe SSR，媒体串流例外保留 |
| 14 / 15 / 17 | 3.5（现行） | 首页二维码先加后删；现行只用飞书入口链接，不展示二维码 |
| 18 / 19 | 20 / 22（现行 1.2） | 管理入口先并列三个 v1 SPA，后收敛为单一管理后台；活动管理走 native SPA |
| 25 | 26 / 27（现行 3.1、3.2） | E2/D10 先暂缓，后按 v1 原版重新纳入 |
| 28–33 | 2.1、2.5、4.1–4.4、4.6（现行） | 性能批次 4–10 的实现细节已合并为现行约束；全文见 git log |
| 34–44 | 1.2、2.2–2.7、3.1–3.5、4.5（现行） | 历史栈、活动状态、保存返回、扫描防护、腾讯合规、照片配额等已并入现行条目 |

## 6. 不要复活

- 顶层 `/admin` SSR 或 404；第二套管理 UI。
- `/welcome` 独立页；首页二维码；公开页源码中文。
- `/legacy/api` 边缘缓存；任何 `$cookie_` 进 cache key。
- `vant.full.min.*`；未预编译的运行时模板；手改生成物。
- Redis、消息队列、微服务/K8s；飞书 runtime；第二套用户 UI；短信 OTP/IM。
- 未授权生产部署、批量 grandfather 身份证、静默回填腾讯合规。

## 7. 新增决策

1. 先确认是否真的改变现行行为；能由 code/git 推导的事实不写进本文件。
2. 在对应主题下追加一节：决策 / 理由 / 不要做什麼 / 验证入口，最多 5 行。
3. 若取代旧决策：把旧条目压成 §5 的一行索引；全文交给 git log。
4. 涉及产品范围、验收、安全/权限/数据的决策，先取得 PM 或 Auditor 同意。
