# 技术决策日志（DECISIONS）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-23
sources: []
-->

> 只增不改；新决策追加一节，不改写既有条目。历史细节见 `git log`。
> 本文件取代旧 ADR 目录（0001–0007，以及 0008 草案；2026-10-23 合并后已删除）。
> 决策影响代码、部署或验收时，同步更新 `docs/STATUS.md`、本文件与相关文档。

## 1. 模块化单体（2026-10-21）

- 背景：团队规模与流量未知，微服务、信息队列或 Redis 会增加部署与一致性成本。
- 决策：单一 Go binary（`cmd/server`），按 config/db/auth/media/httpserver/platform 分包；handler 不写 SQL，DB 不依赖 HTTP。
- 后果：部署只需 binary + PostgreSQL + R2；交易可跨用户、凭证、token 与 session。
- 边界：in-memory rate limit 只在单实例精确；多实例时再换共享存储，不以 Redis 为正确性前提。

## 2. 管理台 SSR + 用户 SPA 内嵌入口（2026-10-21；2026-10-23 调整）

- 背景：管理台需要服务端权限与审计；独立 SSR 入口与用户 SPA 割裂，返回体验差。
- 决策：管理页面继续用 Go `html/template`；浏览器顶层 document 导航访问 `/admin` 一律 303 到 `/?admin=1`，由用户 SPA 打开同源 iframe `/admin?embed=1` 承载；`?embed=1` 仅在非 document（iframe/无 header）时渲染 SSR，RBAC、CSRF、审计不变。
- 后果：管理台需要用户 SPA 的 JavaScript；登录、注册、启用、密码页仍保持无 JS 可用。
- 边界：不新增第二套管理 UI；不用 `window.location='/admin'` 做顶层跳转；smoke 必须同时覆盖 303 与 iframe SSR。

## 3. DB session + 双重提交 CSRF（2026-10-21）

- 背景：需要可撤销的登录状态与 CSRF 防护，且首版不引入 Redis。
- 决策：session token 为 32 bytes CSPRNG，DB 只存 SHA-256；cookie HttpOnly/SameSite=Lax/Secure（可 env 关闭）；CSRF 用 HMAC 签名 double-submit，表单 hidden 字段、JSON API 用 `X-CSRF-Token`。
- 后果：session 可即时撤销；每个带 cookie 请求多一次索引查询；登录、启用、退出都轮换或撤销。
- 边界：CSRF cookie 可被同站子域覆写，但无签名密钥无法伪造；production 必须 https + Secure。

## 4. R2 媒体代理（2026-10-21）

- 背景：照片是私人物品，浏览器不得直接取得 R2 URL，同时要控制格式与大小。
- 决策：上传经 `POST /api/media` 或 `/media/upload`；服务端 sniff + decode 验证 JPEG/PNG，HEIC 拒绝；缩图写入时生成；R2 私有桶，key 由服务端产生。
- 后果：浏览器只看到 `/media/{id}`，app 鉴权后从 R2 串流；缩图存储加倍但读取便宜。
- 边界：不使用 presigned URL、不暴露 bucket；EXIF 方向未校正；上传上限 10MiB、单边 8000px、12MP、并发 1。

## 5. 一次性 activation token（2026-10-21）

- 背景：迁移用户与新用户都需要在没有初始密码时设定第一组密码。
- 决策：`cmd/token` 发行 32 bytes CSPRNG token，DB 只存 SHA-256，默认 24h 一次性；`ActivateUser` 列锁 + 交易写入 Argon2id credential、作废旧 token、撤销旧 session 并自动登录。
- 后果：链接即凭证，明文只输出一次；遗失只能重新发行，没有自助重寄。
- 边界：新 token 作废同 user 未用 token；token 走 URL query，全站 `Referrer-Policy: no-referrer`、页面 no-store、access log 不记录 token。

## 6. server2 HK relay + server1 allowlist（2026-10-21）

- 背景：Cloudflare 橙云 IP 与 server1 美国 IP 都曾被中国电信商封锁；server2 香港 relay 是已验证入口。
- 决策：`next.nantou.love` DNS 指向 server2（DNS-only），server2 终止 TLS 并反代到 server1:443；server1 origin 只允许 server2（`45.125.16.205/32`）+ localhost（`127.0.0.1/32`，本机健康检查/nginx 需要）。
- 后果：staging 有 HK→LA 延迟，功能可用但不代表最终；上线前购买 HK app server，只改 upstream。
- 边界：不开 Cloudflare 橙云、不把 DNS 直指 server1；allow gate 用 `geo $realip_remote_addr`，不能用 `allow`；媒体仍只经 app 代理。

## 7. 用户端复用 v1 Vue 3 + Vant SPA（2026-10-22）

- 背景：v1 用户端已在真实活动使用，包含完整四 tab、卡片、喜欢/留言、活动、分组与一对一流程。
- 决策：保留 v1 SPA 的 DOM、CSS、状态机与 AJAX 行为；Go 提供 `/legacy/api/*` 兼容 API、session 与媒体代理；只改登录、API 前缀、媒体 URL 与飞书文案。
- 后果：UI 一致性高，不维护两套用户页面；需要长期维护兼容层与旧版浏览器支持。
- 边界：用户页仅由 SPA 提供，不另建 Go 用户页；`/cards`、`/likes` 等旧路径 404；不维护第二套完整用户 UI。

## 8. 文档治理：生成 + 语义复核 + 精简文档集（2026-10-23）

- 背景：文档数量膨胀、互相重复且持续漂移；PM 决定大胆精简，只保留必要且通用的文件。
- 决策：最终只保留 10 个 md：`README.md`、`AGENTS.md`、`CLAUDE.md`、`docs/PROJECT_CHARTER.md`、`docs/STATUS.md`、`docs/product-spec.md`、`docs/DECISIONS.md`、`docs/MIGRATION.md`、`docs/runbooks/staging.md`、`docs/reference/REFERENCE.md`。
- 决策：机械事实由 `tools/gen_docs.py` 生成、`tools/check_docs.py` 校验，生成物不得手改；语义内容由对应 owner 复核。
- 边界：新决策追加本文件，不恢复 ADR 目录；历史细节查 `git log`；`check_docs` 是合并门禁。

## 9. 身份证门禁：不 grandfather、不豁免 observer（2026-10-22）

- 背景：迁移只写 `id_card_last4`/`id_card_valid`、不写 ciphertext，1103 个 active + 4 个 observer `id_card_valid=false`；用户端无补录入口。
- 决策：迁移用户 `id_valid=false` 不自动放行；observer 只豁免 `has_face`，不豁免身份证；不做 grandfather，也不伪造 `id_valid=true`。
- 后果：绝大多数迁移用户被全屏门禁拦截，补录/人工放行方式未决，构成 P0 上线阻断（`docs/STATUS.md` §4）。
- 边界：任何放行必须可审计、可回滚；不得用 `yxq_view_role` 或手工改库绕过。

## 10. 顶层 `/admin` 不再跳 SPA（2026-10-23）

- 背景：第 2 节曾要求顶层 document 访问 `/admin` 一律 303 到 `/?admin=1`，入口因此依赖 SPA JavaScript 与重定向链；PM 裁定现阶段不需要该兼容跳转。
- 决策：浏览器顶层 document 访问 `/admin`、`/admin/...`（含 `?embed=1`）直接渲染 SSR，不再 303 到 `/?admin=1`；未登录仍跳 `/login?next=<原路径>`，已登录无权限仍 403。SPA 内 `/?admin=1` 打开 iframe overlay 的逻辑与 `/admin?embed=1` iframe 路由保留。
- 后果：顶层入口不再依赖 SPA JavaScript；SPA 内嵌入口仍保留。现阶段不为顶层入口做兼容跳转，个别路径 404 可接受。
- 边界：不删除 `/admin` 路由；不新增第二套管理 UI；smoke 断言顶层 SSR/登录跳转与 iframe SSR，不再断言 303。本节取代第 2 节中“顶层一律 303 到 `/?admin=1`”的入口约束，SSR、RBAC、CSRF、审计等其余约束不变。

## 11. 顶层 `/admin` 直接 404，管理台只经站内 iframe（2026-10-23）

- 背景：第 10 节让顶层 document 直接渲染 SSR 后，浏览器直接打开 `/admin` 与管理台 iframe 内看到的是同一套 SSR，但外壳/样式观感不同（PM：「为何 /admin 及 ?admin=1 看起来不一样样式，我压根不需要 /admin 那个」）。
- 决策：`adminHTML` 在所有 `/admin`、`/admin/...`（含 `?embed=1`）路由的鉴权之前判断 `Sec-Fetch-Dest: document`，命中即 404，不跳登录、不跳 `/?admin=1`、不渲染管理台；非 document（iframe、无 header 的测试/CLI）行为不变，仍 SSR 鉴权与渲染。
- 后果：管理台唯一入口是用户 SPA 内的同源 iframe（`/?admin=1` overlay → `/admin?embed=1`）；`/login?next=/admin` 的 next 原样保留，登录后落到 404 属可接受。不支持 `Sec-Fetch-*` 的旧浏览器无法与服务端区分，仍会看到 SSR 页面。
- 边界：不删除 `/admin` 路由（iframe 与 `/api/admin/*` 依赖同一套 RBAC、CSRF、模板）；不新增第二套管理 UI；不改模板结构；smoke 改为断言顶层 404、iframe SSR。本节取代第 10 节「顶层 document 直接渲染 SSR」与第 2 节「顶层一律 303 到 `/?admin=1`」的入口约束，SSR、RBAC、CSRF、审计等其余约束不变。

## 12. 管理端媒体串流不受顶层 404 约束（2026-10-23）

- 背景：第 11 节按 `Sec-Fetch-Dest: document` 一律 404 后，管理台详情页「点击缩略图在新窗口查看原图」被连带打死：`target="_blank"` 的新标签页正是 document 语义。
- 决策：新增 `adminHTMLMedia` 包装器，用于 `GET /admin/users/{id}/photos/{mediaID}` 与 `GET /admin/activities/{id}/poster` 两个媒体串流路由。它们不做 document 404，鉴权、权限与串流逻辑与页面路由同一套；其余 `/admin` 页面路由继续一律 404。
- 后果：管理台照片可在新窗口看原图（顶层直接打开该 URL 会返回图片，仍要求管理权限）；顶层直接打开管理台页面仍是 404。
- 边界：例外只覆盖受权媒体串流，`/api/admin/*`、静态资源与页面路由不适用；页面 404 响应带 `Cache-Control: no-store`，页面响应带 `Vary: Sec-Fetch-Dest`。

## 13. 回到 303 单一入口（2026-10-23）

- 背景：第 11 节把顶层 document 一律 404 后，浏览器直接打开 `/admin` 是死路；服务端还得靠 `Sec-Fetch-Dest` 嗅探区分顶层与 iframe。PM 决定回到第 2 节的 303 单一入口。
- 决策：`adminHTML` 在鉴权之前对 `Sec-Fetch-Dest: document` 返回 303 `/?admin=1`（含 `/admin/...` 与 `?embed=1`，POST 同样 303），由用户 SPA 自动打开同源 iframe overlay；`adminHTMLMedia` 例外保留（`/admin/users/{id}/photos/{mediaID}`、`/admin/activities/{id}/poster` 的 document 请求直接串流）；`safeNext` 把 `/admin`、`/admin/...` 重写为 `/?admin=1`；登录默认仍 `/`；SPA 仅在 query 有 `admin=1` 且 `can_admin` 时自动开 overlay，SPA 代码不改。
- 后果：顶层入口统一落到 SPA overlay，无 404 死路；页面响应保留 `Vary: Sec-Fetch-Dest`，303 带 `Cache-Control: no-store` 防缓存串味；媒体仍先鉴权再串流。
- 边界：不删除 `/admin` 路由（iframe 与 `/api/admin/*` 仍依赖同一套 RBAC、CSRF、模板）；不新增第二套管理 UI；不改 SPA 代码；本节取代第 10、11 节与第 2 节冲突的入口约束，SSR、RBAC、CSRF、审计不变，历史条目保留不改。

## 14. 公开首页与飞书老用户迁移路径（2026-10-23）

- 背景：v2 生产首页将由 `nantou.love` 承担品牌与引流；匿名访问 `/` 原来 303 到 `/login`，没有介绍、注册入口或飞书老用户指引。PM 要求公开首页参考 v1 `public.html`，并把「已在飞书版用过的人先设密码，再用手机号＋密码登录」讲清楚。
- 决策：匿名 `GET /` 返回 200 服务端公开首页（品牌 Hero、人工审核/匿名喜欢/动态通知、注册主按钮与登录次按钮、飞书老用户三步说明、飞书入口链接与二维码）；登录后 `GET /` 仍返回 v1 Vue SPA。注册主 CTA 指向 `/register?from=home`；飞书入口固定 `https://applink.feishu.cn/T996FhMkTERp`。二维码图由 `PUBLIC_QR_IMAGE` 指定（默认 `qrcode.png`，staging 用 `qrcode_test.png`），只作为静态图引用，不解析内容。
- 后果：匿名首屏不再依赖 SPA JavaScript，可独立承接 `nantou.love` 的公开流量；登录页同步出现主按钮「注册新账号」和同一条飞书说明。公开页不展示任何用户照片或资料，避免隐私暴露。匿名访问 `/?admin=1` 时先 303 `/login?next=...`，登录后由 SPA 依据 `admin=1` 自动打开管理台 overlay。
- 边界：不改 `/admin` 的 303 `/?admin=1`、`/app` 的匿名跳转，以及 `/login`、`/register`、`/welcome` 现有行为；不新增第二套注册/登录流程；不把二维码内容写进代码或日志；公开页只做展示与跳转，不读取用户数据。

## 15. 公开首页二维码改为固定生成资产（2026-10-23）

- 背景：`5734f10` 原样复制了 v1 的 `qrcode.png` 与 `qrcode_test.png`。部署前独立审查判 FAIL：`qrcode_test.png` 经 zbar/pyzbar、zxing-cpp、OpenCV、微信解码器均不可读；`qrcode.png` 解出 `https://applink.feishu.cn/T98YejHHBnrA`，与页面按钮/链接的 `https://applink.feishu.cn/T996FhMkTERp` 不是同一个 applink token。`.team/audits/2026-10-23-migration-gaps.md` 也已警告不要复制 v1 二维码充数。
- 决策：删除两个 v1 PNG 与 `PUBLIC_QR_IMAGE`（`internal/config`、`.env.example`、模板、文档全部移除），公开首页模板固定引用 `web/static/feishu-qr.png`。该图由页面同一个入口 URL `https://applink.feishu.cn/T996FhMkTERp`（即 `publicFeishuAppURL`）生成，入口 URL 仍是唯一事实来源；运行时不解析二维码，也不新增 QR 生成依赖。
- 生成与验收：资产需变更时运行一次性工具 `python3 tools/gen_qr.py`（依赖 `qrcode[pil]`，故意不写入 `tools/requirements*.txt`）；生成后必须用独立解码器（如 `zbarimg --raw web/static/feishu-qr.png`、pyzbar 或 zxing-cpp）验证解出的 URL 与 `publicFeishuAppURL` 完全一致；staging 发布前按 runbook 再验一次。
- 后果：首页二维码在 staging 与生产使用同一张可扫、与按钮一致的图；不再需要 `config/staging.env` 的二维码配置，旧值变成无效果并在下次改 env 时删除；删除 v1 资产消除审计冲突。
- 边界：本节取代第 14 节中「二维码图由 `PUBLIC_QR_IMAGE` 指定」的部分，其余第 14 节不变；不修改飞书入口 URL、不改公开页布局与跳转；二维码内容只是第 14 节已公开的入口 URL，不含用户数据或密钥。

## 16. 活动管理恢复 v1 SPA：后端兼容层与同步执行（2026-10-23）

- 背景：v2 管理台改用 SSR 后，SPA「我的→管理→活动管理」页面仍保留 v1 原版 UI，但它调用 `/legacy/api/admin/*`，而 v2 只注册了 `/api/admin/*`，页面 0 端点可用（见 `.team/audits/2026-10-23-activity-admin-compat.md`）。PM 决定恢复 v1 原版活动管理界面（签到/执行），后端补兼容层。
- 决策：新增 `/legacy/api/admin/*` 薄适配层（21 条路由），只做取数与形状映射，业务逻辑复用 `internal/db`；响应严格使用 v1 契约：`{"error":"..."}` 错误信封、`是/否` flag、成员/名单带 `open_id`、列表字段恒为 `[]`、`latest_round` 可为 `null`。v2 `/api/admin/*` 与 SSR 不改语义。
- 决策：执行先同步。`group|onetoone run` 完成即回 `{ok:true,result:{code,round,...}}`，业务失败（`ALREADY_DONE`/`EXCLUSIVE_MODE`/`NOT_ENOUGH_PEOPLE`/`EMPTY_GROUPS`/`NO_PARTICIPANTS`）回 HTTP 200 + v1 `result.code`；参数、权限、CSRF 错误才用 4xx。`group/job`、`onetoone/job` 恒回 400（缺 `job_id`）或 404（任务不存在），不建 `jobs` 表；SPA 侧同步分支另行交付。
- 权限：总览/详情需 `activity.manage` 或 `matching.run`；预览/执行/名单/结果/聊天需 `matching.run`；模式/状态/签到码/签到开关/现场签到需 `activity.manage`；`group/status.signin_code` 仅对 `activity.manage` 返回。未登录 401「未登录」、无角色/无权限 403「没有权限」，写操作沿用 HMAC CSRF；审计沿用 `group.run`/`group.reset`/`onetoone.run`/`onetoone.reset` 与 `admin.activity.*`，签到码值不进审计。
- 后果：一段时间内同一业务有两套 JSON 形状，由集成契约测试防漂移；`/api/admin/group/result`、`/api/admin/onetoone/result`、`/api/admin/onetoone/chat-overview` 增量补 `open_id`/`display_name`/`nickname`，`/api/admin/group/run` 同时接受单数与复数每组人数键；v1 的「迟到」仍拒绝写入（两态签到只写「按时」/空）。
- 边界：不改数据库 schema，不改 SSR 与 v2 现有字段含义，不恢复 202+job 轮询；SPA 入口恢复、执行分支适配与「编辑活动」入口不在本阶段。

## 17. 公开首页取消二维码图片（2026-10-23）

- 背景：PM「next 首页不需要那个二维码图片方式」。首页原本除飞书入口按钮外还展示 `web/static/feishu-qr.png`，并配「扫一扫」文案。
- 决策：删除首页模板的二维码图片区块与「扫一扫」文案，只保留飞书入口链接按钮与「飞书老用户先设密码」说明；同步删除 `web/static/feishu-qr.png`、一次性生成工具 `tools/gen_qr.py` 与 `.home-qr` 样式。测试、Playwright smoke、runbook 与状态文档改为断言页面不含 `feishu-qr`/「二维码」。
- 后果：首页少一张图片与一条资产维护链；飞书入口仍是唯一链接，登录页的飞书说明不变。
- 边界：不改飞书入口 URL（`https://applink.feishu.cn/T996FhMkTERp`）、不改公开页其他布局与跳转、不新增其它二维码。本节取代第 14、15 节的二维码展示与生成约定，历史条目保留不改。

## 18. 活动管理恢复 v1 SPA 前端入口（2026-10-23）

- 背景：第 16 节的后端兼容层已就绪，但 SPA 的入口、执行分支与「编辑活动」入口尚未接，页面无法从 UI 打开。
- 决策：在「我的→管理」恢复 v1 的「👥 活动管理」cell（`openGroupAdmin`），管理员判定用 legacy `user.can_admin`；与现有「🛠️ 管理后台」SSR iframe 入口并存。`doGaRun`/`doOtoRun` 优先处理同步响应 `{ok:true,result}`，直接复用 v1 `handleGaJobDone`/`handleOtoJobDone` 的成功与错误码分支；202+job 与 409+job_id 分支保留但 v2 不触发。`group_detail` 页头新增「✏️ 编辑」，用 `gaDetail.id`（缺省回退列表项 `id`）打开 `/admin/activities/{id}/edit` iframe；该路径仍是 `/admin` 页面，顶层 document 303 不影响 iframe 语义。新增 `tools/browser/admin_activity_native_smoke.py` 做真实浏览器回归。
- 后果：管理员可在 App 内完成活动列表→控制台（签到码/状态/模式）→报名名单→现场签到与编辑；执行同步完成，不建 job 表、不改 schema。
- 边界：不改 v2 `/api/admin/*` 与 SSR，不改第 16 节后端契约；编辑仅经同源 iframe，不新增第二套管理 UI。本节接续第 16 节边界中「SPA 入口恢复、执行分支适配与编辑活动入口不在本阶段」的部分。

## 19. 麦穗管理与举报管理恢复 v1 SPA（2026-10-23）

- 背景：第 18 节完成「我的→管理→活动管理」后，SPA 仍保留 v1 的 `points_admin` 与 `reported_messages` 两个页面，但 `/legacy/api/admin/points/*` 全部 404，举报处理动作 `POST /legacy/api/messages/{id}/handle` 也未注册；`user.is_admin` 又固定为 `false`，页面即使有数据也打不开。
- 决策：新增 `/legacy/api/admin/points/*` 薄适配层（overview/ledger/redemptions/invites/user 五个读端点，adjust/refund/mark 三个写端点），只做取数与形状映射，聚合查询集中在 `internal/db/points_admin_legacy.go`，业务写入仍走现有 `db.AdjustPoints`/`db.RefundRedemption`/`db.MarkRedemption`；读与写都用 `points.adjust`，写操作先过 HMAC CSRF，审计沿用 `admin.points.adjust`/`admin.points.refund`/`admin.points.mark`，调整继续写站内通知并使用 `admin_adjust:<request_key>` 幂等键。
- 决策：举报处理补 `POST /legacy/api/messages/{id}/handle`，action `delete|approve`（兼容 `reject|restore`），权限 `content.moderate` + CSRF，复用 `db.HandleReportForMessage`，审计 `message.report.handle`；重复处理返回 404「找不到待处理的举报」，不产生第二条审计或二次删除。
- 决策：`legacyUserMap` 的 `is_admin` 改为与 `can_admin` 同值（有任一管理角色），修复 v1 页面里 `is_admin` 判断；具体接口仍按权限码鉴权，普通用户仍 403，「我的→管理」仍由 `can_admin` 控制。
- 后果：「我的→管理」现在并存「👥 活动管理」「🌾 麦穗管理」「🚨 举报管理」「🛠️ 管理后台」；麦穗页四个 tab（用户/流水/兑换/邀请）与单用户详情、举报页列表与同意/驳回按钮均可用，Playwright 双视口 smoke 覆盖管理员路径与普通用户 403。
- 边界：不改数据库 schema、不改 SSR、不改 v2 `/api/admin/*` 与 `/api/messages/*` 语义；不恢复 v1 的 202+job；写端点当前只作为兼容能力，SPA 麦穗页仍为只读；手机号只输出 masked。

## 20. 管理入口收敛回单一「管理后台」（2026-10-23）

- 背景：第 18、19 节把「👥 活动管理」「🌾 麦穗管理」「🚨 举报管理」三个 v1 SPA 入口并列放进「我的→管理」，与「🛠️ 管理后台」形成四个入口。PM 要求管理功能统一融入「管理后台」，不要在「我的」页单列。
- 决策：删除「我的→管理」中的三个 SPA cell，只保留「🛠️ 管理后台」；活动/麦穗/举报的 v1 SPA 页面与 `/legacy/api/admin/*`、举报处理兼容层保留在代码中，但暂不暴露入口；管理操作统一经管理后台的 SSR iframe 承载。
- 决策：原入口级 Playwright smoke（`admin_activity_native_smoke.py`、`admin_points_report_smoke.py`）删除，替换为 API 级 `tools/admin_legacy_api_smoke.py`：校验管理员 SPA 无三个单列入口、管理员三端点 200 且响应形状正确、普通用户三端点 403 且返回 v1 error 信封。
- 后果：「我的→管理」回到单一入口；原生化页面如后续要重新暴露，需另立决策并恢复对应入口回归。
- 边界：不改数据库 schema、不改 SSR 页面、不改 `/legacy/api/admin/*`、`/legacy/api/messages/reported` 契约与 RBAC/CSRF/审计；不删除 SPA 页面代码；第 18、19 节中关于入口并存的描述由本节取代，其余后端与页面能力描述不变。
