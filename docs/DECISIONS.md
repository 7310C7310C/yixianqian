# 现行决策（DECISIONS）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-11-06
sources: []
-->

> 只记「今天仍约束代码/部署/验收」的判断：决策、why、不要做、验证入口。
> 机械事实看 code/config/Makefile；历史看 git log（旧编号 34–44 映射见旧版）；进度看 STATUS；验收看 PRODUCT。
> 一个行为只留一条；同日多条合并；不写批次/日期标题。

## 1. 安全与数据

### 1.1 身份与门禁
- 决策：迁移用户 `id_card_valid=false` 不 grandfather；observer 只豁免 `has_face`，不豁免身份证（身份证曾是上线阻断，不能伪造 `id_valid=true`）。资料页可补填 18 位（校验位、18+、`id_card_hash` 唯一）；生日为空或不一致时以证内生日静默更正并审计。非豁免用户访问 `/legacy/api/*`、`/app`、`/admin` 返回 403 `id_card_required`；白名单仅首页/版本/埋点/资料/me/通知/改密/退出/mutual。
- 不要做：不批量 grandfather、不手工改库、不把明文身份证写日志（只记后四位）。验证：`TestIntegrationIDCardGateAndBackfill`、`TestIntegrationObserverStillBlockedByIDCard`。

### 1.2 媒体与授权
- 决策：R2 私有，浏览器只经 `/media/{id}`；上传入库统一 JPEG（HEIC 先 ispe 预检再 `heif-convert`，原始 HEIC 不留；透明填白、GIF 首帧）；单张 10MiB/8000px/12.5MP、并发 1；重编码按 EXIF/eXIf auto-orient 并去 EXIF/GPS/XMP（照片是私人物品；不信任 Content-Type，必须 sniff+decode；iPhone 12MP 必须可通过）。照片授权默认拒绝，按 viewer 发短时 grant（15 分钟）；放行本人、管理员、任一方向有效喜欢（含互喜）、未取消的同活动报名、消息对手方；匿名 401、无权/不存在 404、禁用 403，矩阵见测试。删除只删本人 attachment；非属主删最后一张保留 media/R2。
- 决策：牌堆/公开资料未授权时返回 200 + 空 photos/thumb/meta，不复用 media 级关系或其他画廊 grant；多实例前改共享 grant 或 HMAC 签名 URL。
- 不要做：不把 R2 URL/key 给前端或日志；不跳过重编码；不恢复 12MP 旧上限；不批量重写历史原图；不整体放宽 `/media/{id}`（仅 broadcasts 引用例外）。验证：`TestProcessAcceptsIPhone12MP`、`TestProcessStripsEXIFAndAppliesOrientation`、`TestIntegrationProfileSharedMediaDelete`、`TestIntegrationCardPhotoAuthorizationMatrix`。

### 1.3 腾讯合规与照片配额
- 决策：腾讯图片合规同步调用、fail-open，只决定 `has_face`；`facebackfill -all` 需显式 `TENCENT_FACE_BACKFILL=true`（按次计费）；任一腾讯开关为 true 时缺凭证拒绝运行；fail-open skip 仍记 `face_checked`，`unchecked=0` ≠ 已判定。
- 决策：照片按账号/自然日配额（默认 10/0 关闭，429 `photo_daily_limit`），跨上海午夜重置；注册按提交张数一次性计入、不足整笔回滚，删除不返还（补齐 v1 `photo_quota.py`，不新增队列）。
- 不要做：不静默回填；不重试；不把合规失败当注册失败；不绕过配额。验证：`TestIntegrationTencentComplianceBlocksHasFace`、`TestIntegrationPhotoQuotaDailyLimit`、`TestIntegrationPhotoQuotaCrossesShanghaiMidnight`。

### 1.4 Session、activation 与 CSRF
- 决策：session/activation token 32B CSPRNG，DB 只存 SHA-256；cookie HttpOnly/SameSite=Lax/Secure；CSRF 用 HMAC double-submit（表单 hidden、JSON `X-CSRF-Token`）；activation 24h 一次性、列锁+交易、明文只输出一次；token 走 URL query 时全站 `Referrer-Policy: no-referrer`、页面 `no-store`。壳 bootstrap 递归剔除 phone/wechat/id_card；互喜微信号只从 `no-store` 的 `/legacy/api/likes/me` 取。
- 不要做：不把明文 token/session 写日志或边缘缓存；不在 cookie 放权限；不把联系方式内联进可缓存壳。验证：`TestSanitizeBootstrapStripsSensitiveKeys`；登录/启用/退出轮换与写请求 CSRF 测试。

### 1.5 管理台权限
- 决策：临时密码/停用/恢复/审核对同级或上级仅 `super_admin` 可执行；临时密码还需 `user.manage`；`approve/reject` 只接受 pending/rejected；handler 与 DB 双重校验；最后一位 super_admin 不可删。
- 不要做：不让 admin 重置同级/上级；不让 reviewer 停用 active；不跳过审计。验证：`TestIntegrationAdminPrivilegeBoundaries`、`TestIntegrationAdminRBAC`。

## 2. 产品行为

### 2.1 相互喜欢与微信号
- 决策：注册必填微信号；双方都就绪（微信号 + `contact_consent=true`）后 mutual API 才返回对方微信号并给双方发通知（按 pair+收件人去重）；未就绪只返回 `wechat_required`/`wechat_status`，不泄露匿名喜欢者。
- 不要做：不在未同意时返回微信号；不把微信号放进可缓存壳。验证：`TestIntegrationMutualWechatExchange`、`TestIntegrationMutualWechatDoesNotLeakAnonymousLike`、`TestMutualAPIContactConsent`。

### 2.2 每周推荐位（D10）
- 决策：`cmd/server` 启动补跑 + 整点检查，不新增常驻进程；每人每 ISO 周 7 条（必显更多时取 max；候选不足可少），推荐对象置顶并 `recommended=true`；管理端只读汇总 + 手动重跑（`matching.run` + CSRF + 审计），不展示必显原因。
- 不要做：不新增调度进程；不展示必显原因；不改变 v1 parity 分配口径。验证：`TestIntegrationWeeklyRecommendations`、`TestIntegrationWeeklyRecommendationsOnCards`。

### 2.3 活动、签到与脱单
- 决策：活动「已结束」由时间派生（仅报名中/已满员且 `end_at <= now`）；现场签到要求 4 位码，未签到/未到不得进入志愿选择，前端只看签到状态，后端 `loadSignupGate` 权威；`can_self_signin` 还需已设 4 位码。脱单 gate 只算未结束的「已报名」。分组与一对一共用 `group_selections`、入口互斥；志愿选择入口在任一模式开启时可用，分组结果仅对分组模式开放。
- 决策：已结束/已取消隐藏；`group_flag`/`onetoone_flag=否` 时 result/chat 返回 409 `activity_ended`/`onetoone_disabled`；DB flags 不改，管理台仍可管理。
- 不要做：不把历史报名当永久阻塞；不在无签到码时展示可用签到入口；不因「可自助签到」放宽志愿门槛。验证：`TestCanSelfSigninBoundaries`、`TestIntegrationActivityAutoEndUserViews`、`TestIntegrationEndedSignupDoesNotBlockMatched`。

### 2.4 公开页、登录与注册
- 决策：匿名公开 GET 页除 `<title>` 外源码 0 汉字（POST 表单错误、内部 500、管理台错误页例外）；`/welcome` 301 首页，独立 `/login` 并入首页弹框（`GET /login` 303 `/?login=1`，POST 契约不变）；注册地区省-市/市-区级联且必填，行业/来源纯文本，身份证 18+ 即时校验，单字段单错误（生日与证内不一致静默更正），照片 3–9 张/首张封面/10MB，小天使二维码后台管理（`/angel-qr`，720px JPEG q92）。
- 不要做：不把中文写回公开页；不恢复二维码到首页；不恢复 `/welcome` 或独立登录页；不把身份证改回选填。验证：`TestPublicScanPagesAreHanFree`、`TestParseRegisterRegionFields`、`TestRegisterRegionsEndpoint`。

### 2.5 消息中心与广播
- 决策：消息页默认动态流 + 喜欢/留言子页；收到留言 = 卡上留言 + 回复我的留言，已读存 `message_reads`；tabbar 红点 = 非留言动态未读 + 收到留言未读（message 通知不重复计数），管理员待办也计入；单条/全部已读/清空持久，清空写 `notifications.cleared_at` 不删行；发送喜欢/留言也写 sender-side 已读动态；卡上留言和回复可点进留言串高亮，纯结果类通知不跳转。全员图文广播只经 `/admin/broadcasts`（`content.moderate`），写 broadcasts + 批量 notifications + 审计；注册审核、举报、反馈、兑换等管理员待办同时写入管理员动态（notifications），点击进入对应处理队列。
- 不要做：不用 `notifications.read_at` 冒充已读；不删行清空；清空不得清除收到留言未读；不整体放宽 `/media/{id}`；不假设 `legacy_open_id` 唯一。验证：`TestIntegrationMessageInboxAndNotifications`、`TestIntegrationLegacyMessageInboxHTTP`、`TestIntegrationAdminBroadcast`、`TestIntegrationFindUserByDuplicateLegacyOpenID`。

### 2.6 牵线筛选：现居/家乡省份多选
- 决策：牵线筛选只加「现居省份」「家乡省份」两个**省份多选**（不选=不限），不做城市多选/省内城市细化；省份选择器默认折叠且展开/收起箭头随状态切换，触摸滚动到列表边界后继续拖动会接力滚动筛选面板；选项按公开资料估算的天主教徒规模排序（广东置顶，港澳台因统计口径与用户来源排在末尾）；后端把省份 code 展开成该省城市的 canonical/全名/短名集合，用 `u.city = ANY(...)` / `u.native_place = ANY(...)` 匹配，兼容 v1 遗留短名（如「深圳」）与新注册 canonical（「广东深圳」）；两条件 AND，未知 code 忽略。
- 数据前置：staging 已用 `cmd/regionfix` 把 `city`/`native_place`/`church_location` 全部规范化为数据集合法值；生产迁移时先跑同一工具再开放硬筛。
- 不要做：不按 `LIKE '广东%'` 直接匹配（会漏 v1 短名）；不做城市多选；不把省份筛选做成排序权重。验证：`TestIntegrationCardsProvinceFilter`、`TestIntegrationCardFiltersByProvinceValues`、`TestCardFilterProvinceExpansion`。

### 2.7 牵线爱心光晕与全览模式
- 决策：停在「牵线」tab 时底栏爱心持续光晕（CSS 心跳：图标 `yxqHeartBeat` 2.4s + 径向 halo `yxqHeartHalo`，仅 `page==='cards'` 挂 `.tab-heart-live`；光晕包含块固定在图标 wrapper；`prefers-reduced-motion` 只留静态光晕）。已在牵线时**再点一次** tab 进入全览：独立子页 `cards_overview`（隐藏底栏、页头返回），按注册时间倒序（`u.id DESC`，最新在前），滚动分页 30 条/页；子页铺满视口（负 margin 收回 `#app` 的底栏预留，不留底部空白带）。
- 全览交互：网格 tile 支持多图左右滑动（滑动切换、滑动后抑制误点）；点 tile 打开卡片浮层（`van-popup`，88% 高）：大图多图滑动 + 点开大图查看器、完整资料分区、留言列表与发送框、反馈、点赞、爱心（后两者复用 deck 的额度/隐私规则）；浮层关闭后列表与滚动位置不变。筛选复用牵线筛选面板与同一套参数（`parseCardFilter` + `gender`），应用/清除后全览按筛选重载；筛选只看牌堆/全览，互不影响各自结果集。
- 全览边界：资格与牌堆完全一致（异性、`account_status=active` 即未脱单、有照片；observer 可看全性别）；接口 `GET /legacy/api/cards/overview` 返回浅字段 + 全部缩略图（`photos`）+ `liked/praised`，分页并按 viewer 发 15 分钟照片 grant，不接 10s 读缓存；完整资料只从既有 `/legacy/api/users/{id}/public` 取。
- 不要做：不把全览做成第五个 tab 或浮层；不按推荐/随机/在线排序；不在瓦片里塞操作按钮（操作放在卡片浮层）；不返回微信号/身份证等敏感字段；不在全览里绕过 deck 的喜欢/点赞额度与 observer 限制。验证：`TestBuildCardOverviewItem`、`TestIntegrationCardsOverviewNewestFirst`、`tools/browser/cards_overview_smoke.py`。

## 3. 架构与交付

### 3.1 模块化单体 + PostgreSQL + R2
- 决策：单一 Go binary（config/db/auth/media/httpserver/platform），handler 不写 SQL（团队/流量未知；微服务/队列/Redis 增加部署与一致性成本）；PostgreSQL 唯一权威，R2 只存私有媒体；migration 只增不改，回滚 binary 不等于回滚 schema；生产大表索引另行走手工窗口。
- 不要做：不新增 Redis/消息队列/微服务/K8s；不以缓存作为正确性前提。验证：`go test ./...`；部署只需 binary + PostgreSQL + R2。

### 3.2 公开入口固定 server2 HK relay
- 决策：`next.nantou.love` DNS 指向 server2（HK），TLS 终止后回源 server1；server1 origin 只允许 server2 + localhost。理由：Cloudflare 橙云 IP 与 server1 美国 IP 均曾被运营商封锁。
- 不要做：DNS 不直指 server1；不开橙云；allow gate 用 `geo $realip_remote_addr`，不改回直接 `allow`。验证：直连 origin 403；公网 `/healthz` 200。

### 3.3 管理台：SSR + iframe
- 决策：管理台 Go SSR，只经用户 SPA 同源 iframe（`/admin?embed=1`）；顶层 document `/admin` 一律 303 `/?admin=1`；活动管理在 iframe 内 `/app?native_admin=1&p=group_admin` 复用 v1 原版；走 `/legacy/api/admin/*` 薄适配层 + 同步执行，不建 jobs 表/202 轮询；受权媒体串流为显式例外；RBAC/CSRF/审计沿用。
- 不要做：不恢复顶层 admin SSR/404；不新增第二套管理 UI；不重写用户端。验证：顶层 303；iframe SSR 200；`/api/admin/*` 走同一套 RBAC/CSRF/审计。

### 3.4 文档治理
- 决策：路由/env/migration/Makefile/目录以 code 为准，历史以 git log 为准；docs 只写意图、非目标、硬约束、验收、当前状态、现行决策、操作陷阱（AI 可直接读 code/git；复制机械事实会漂移并增加文档税）；同一事实只一个 owner，其他只链接。
- 不要做：不在 docs 写批次历史、旧快照、路由表、测试输出、重复部署命令。验证：`make docs-check`（预算 + 日期行检查）；fresh clone 按 AGENTS 找到入口。

## 4. 性能与前端

### 4.1 首载预算与生成物
- 决策：首载 gzip 预算在 `tools/perf/budgets.json`，每项基线 +10%，超限 CI 红，只能手工改并走 Review；SPA 生成物提交仓库，部署/CI 不依赖 Node；任何 `web/static/**` 变更 bump `assetVersion` 并同步 admin CSS `@import`；生成物走 `make spa-check`。
- 不要做：不改预算代替优化；不手改生成物；不把 app.css/app.min.js/app.render.js 大段内联回壳；不退回 `vant.full.min.*`。验证：`make perf-budget`、`make spa-check`。

### 4.2 边缘、缓存与 SSE
- 决策：server2 upstream keepalive、`proxy_http_version 1.1`、TLS session ticket；server1 `/static/` gzip/identity 分键缓存；`/legacy/api` 禁止边缘 `proxy_cache`，cache key 不含 `$cookie_`；home/cards 10s 进程内读缓存 + 写后 epoch++；媒体 ETag/304；热索引 0008–0010 只增不改。通知 SSE 用 PostgreSQL `LISTEN/NOTIFY`（应用写通知/已读时同事务 `pg_notify`）+ 30s 兜底/15s 心跳。
- 不要做：不绕过 epoch；不把错误写进缓存；不恢复 SSE 轮询；多实例前先做 epoch pub/sub 与共享 fan-out。验证：`TestReadCache*`、`TestIntegrationLegacyHomeCardsReadCache`、`make perf-bench`、`tools/perf/check_nginx_guard.py`。

### 4.3 SPA 历史栈与前端不变量
- 决策：顶层 SPA 默认虚拟历史栈（`pushHistory`/`hist.replace`/`handleBack`），`native_admin` 与 `?history=legacy` 例外；照片变更同步本人卡/牌堆；门禁返回栈保留父级 tab；大图 viewer 用 `photos_meta` 定框、缩图先显原图后升；**大图打开（含打开中遮罩）时方向键只翻图，不切底层卡片**；卡面照片滑动后快速轻点也必须立即打开大图（触摸 touchend 直接打开，兼容 mouse click 只吞一次）；卡面资料/留言切换为左右滑动过渡，我的页匿名/实名额度文案同换行；签到/匹配入口见 §2.3；详细规则在 `web/AGENTS.md`。
- 不要做：不直接调用 `window.history.*`；不删启动哨兵；不改 `native_admin` 历史语义。验证：`no_history_smoke.py`、`spa_user_fixes_smoke.py`、`TestIntegrationLegacyHomeSelfCard`。

### 4.4 首屏交接与启动预取
- 决策：三个主脚本并行下载、首帧后启动；hero 在 App 骨架挂载后立即交接（不等数据/首卡绘制，不再用 2s + 长任务静默）；启动骨架的 tabbar 可点并按目标 tab 排队，数据就绪后自动切过去；公开首页在 idle/打开登录弹框时预取 SPA 资源（Save-Data/2g 跳过）。不要做：不把 hero 当加载闸门；不恢复 2s notBefore/长任务静默；不恢复静态岛/品牌启动页。验证：`tools/browser/perf_boot.py`、`tools/browser/app_hero_deck_smoke.py`、`tools/browser/tab_first_load_smoke.py`。

## 5. 不要复活

- 顶层 `/admin` SSR/404；第二套管理 UI；`/welcome` 独立页；首页二维码；公开页源码中文。
- `/legacy/api` 边缘缓存；`$cookie_` 进 cache key；`vant.full.min.*`；未预编译模板；手改生成物。
- Redis/队列/微服务/K8s；飞书 runtime；第二套用户 UI；短信 OTP；品牌启动页/静态岛；首卡引导气泡。
- 未授权生产部署；批量 grandfather 身份证；静默回填腾讯合规。

## 6. 新增决策

- 先确认是否真的改变现行行为；一个行为一条，同日合并，不写日期批次；产品/验收/安全/权限/数据先取得 PM 或 Auditor 同意；细节放测试/局部 AGENTS，本文档只留 why 与指针。
