# 技术决策日志（DECISIONS）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-26
sources: []
-->

> 只增不改；新决策追加一节，不改写既有条目。历史细节见 `git log`。
> 本文件取代旧 ADR 目录（0001–0007，以及 0008 草案；2026-10-23 合并后已删除）。
> 决策影响代码、部署或验收时，同步更新 `docs/STATUS.md`、本文件与相关文档。

## 2026-10-25 SPA 构建期打包（性能批次6）

- 决策：允许**开发期**使用 Rollup/esbuild 生成 Vant 子集与按 tab/页面拆分的 render chunk，生成物提交仓库；部署/CI 不依赖 Node（同预编译模式）。
- 约束：组件/样式覆盖清单与缺失降级（可见 `.boot-fail`）；生成物带 manifest/sha256 门禁；可回退到 UMD/revert；`assetVersion` 随静态变更递增。
- 影响：`tools/spa_vendor`（Vant 4.8.7 子集）与 `tools/spa_precompile`（root+6 chunk、pages/*.html）为 dev-time 工具；`make spa-check` 用 Go 校验 manifest，Node 检查用 `make spa-check-node`。

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
- 边界：不使用 presigned URL、不暴露 bucket；EXIF 方向未校正；上传上限 10MiB、单边 8000px、12MP、并发 1（2026-10-24 §24 起：上传前 auto-orient 去元数据，像素上限调整为 12.5MP）。

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

## 21. 管理台剩余页面沿用 Phase A 设计系统（2026-10-24）

- 背景：Phase A 只重做了外壳、总览与活动；用户/匹配/麦穗/举报/反馈/角色/审计仍用旧 `.card` + 6–7 列表格，768 宽按钮堆叠、日期折行，深色实心按钮对比度 2.17:1，总览右上角因管理员姓名单字「一」看起来像空按钮。
- 决策：不新增第二套设计系统；在 `web/static/admin.css` 的 `Phase B/C` 分区补齐共享组件（身份块、记录卡 `.admin-record-list`、键值网格 `.admin-kv`、角色卡、审计行卡、`.admin-table--stack`），页面统一使用 `.admin-page-head` + `.admin-section` + 状态 partial + 空态/分页；多字段列表改记录卡，不再用 6–7 列表格；顶栏右侧改为「图标 + 管理员 + 姓名」胶囊；实心品牌控件统一用 `--adm-on-brand`（深色 `#2a1a1e`）保证 ≥4.5:1。
- 边界：只改模板/CSS/交互钩子；路由、handler、字段名、表单 action/method、`data-matching-*`/`data-adm-*` 钩子、RBAC、CSRF 与审计不变；Phase A 的 719px 表格断点不改，新表格卡片化用 `.admin-table--stack`（≤899）。
- 后果：剩余 10 页在三视口下统一为粉色 Vant 观感；`assetVersion` 提升到 3，两个 `@import` 同步；证据与断言见 `docs/STATUS.md` 管理台章节。

## 22. 活动管理回归 v1 原版 SPA，匹配入口收进活动控制台（2026-10-24）

- 背景：管理台 Phase A 的 SSR 活动工作台替代了 v1 原版活动管理；PM 反馈「原版现场签到很好用，还是喜欢原版的」，要求活动管理基于原版做简约调整，且执行分组/一对一不再作为总览独立管理项。
- 决策：管理台总览「活动列表」在 iframe 内直接打开 `/app?native_admin=1&p=group_admin`，复用 v1 SPA 的 group_admin/group_detail/group_roster/checkin 页面；native 模式只保留管理页内容与 `.back-nav`（列表页返回 `/admin?embed=1`，子页按层级返回），隐藏 tabbar/App 外壳，仅 `can_admin` 可用，普通用户 `location.replace('/')`。`/admin/activities` 的 iframe 请求 303 到 native 地址，顶层 document 仍 303 `/?admin=1`，无 header 请求仍渲染 SSR；SSR 活动工作台与新建/编辑表单保留（编辑仍由控制台 `openEmbed('/admin/activities/{id}/edit')` 打开）。
- 决策：总览删除「匹配」分组与「分组匹配/一对一匹配」入口；执行分组、执行一对一保留在活动控制台内。其他管理页（用户/麦穗/举报/反馈/角色/审计）设计不变。
- 决策：仅对 `/app?native_admin=1` 的同源 iframe 放宽 framing（CSP `frame-ancestors 'self'`、`X-Frame-Options: SAMEORIGIN`）；普通 `/`、`/app` 及登录页仍保持 `frame-ancestors 'none'` / `DENY`，避免扩大点击劫持面。
- 边界：不改 `/legacy/api/admin/*` 契约与 RBAC/CSRF/审计；不改数据库与匹配算法；不重做原版页面视觉；不改用户端普通 App 模式行为。
- 后果：管理台活动入口回到 PM 认可的原版交互；匹配操作收敛到单场活动上下文；SSR 活动列表不再从导航暴露。

## 23. 管理台纵向权限边界：临时密码与审核状态机（2026-10-24）

- 背景：后端安全审计（`.team/audits/2026-10-24-bugs-backend.md` P0-1/P1-11）确认 `admin` 可对 `super_admin` 生成临时密码并接管账号；`reviewer` 可借 `approve`/`reject` 停用任意 active 用户或恢复被 `user.manage` 停用的账号。
- 决策：临时密码在 handler 与 `AdminSetTempPassword` DB 事务内双重校验。目标持有 `super_admin` 或 `admin` 角色时，仅 `super_admin` 可重置；调用者还必须持有 `user.manage` 或 `super_admin`。DB 层先锁目标行再校验，不依赖 handler。
- 决策：`approve`/`reject` 只处理审核状态（`account_status` 为 `pending` 或 `rejected`）。`active`（含被 `user.manage` 停用但 `account_status=active`）、`left`/`matched` 等状态返回 409，停用/恢复必须走 `POST /admin/users/{id}/disable|enable`（`user.manage`）。`reviewer` 只有 `user.review`，不能停用/恢复。
- 决策：对 `super_admin`/`admin` 目标的临时密码、停用、恢复、审核通过/拒绝，actor 必须是 `super_admin`；非 super 返回 403。最后一位 `super_admin` 的既有保护（停用/拒绝/移除角色）不变。UI 按同一规则隐藏按钮，服务端仍为最终判定。
- 审计：`admin.user.temp_password` 记录 actor、target、`target_roles`、`actor_roles`、`actor_super`、`must_change`、`expires_at`；不记录临时密码或 token。
- 边界：不改数据库 schema、不改路由权限码；`pending`/`rejected` 仍可由 `user.review` 审核，`rejected` 仍可通过 `approve` 重审（恢复路径保留）。非 super 的 `admin` 不能操作同级/上级，也不能重置 reviewer 等非受保护角色之外的管理账号密码——受保护集合明确为 `super_admin`/`admin`。
- 证据：`TestIntegrationAdminPrivilegeBoundaries`、`TestIntegrationAdminRBAC`、`TestIntegrationAdminHTTP`、`TestIntegrationAdminUserDetailHTTP`；变异验证见 `.team/security/2026-10-24-admin-vertical-rbac.md`。

## 24. 上传媒体去元数据、共享 media 删除与 12.5MP 兼容（2026-10-24）

- 背景：后端审计（`.team/audits/2026-10-24-bugs-backend.md` P1-1/P1-2/P1-3）确认：迁移数据中一个 media 可被上千用户引用，删除照片会误删共享 R2 对象或级联删除他人 attachment；上传原图原样入库，`/cards/photo`、`/profile/photo` 串流时暴露 EXIF/GPS；iPhone 12MP JPEG（4032×3024=12,192,768）超过旧 12,000,000 像素上限，HEIC 直接 400 且文案不可操作。
- 决策：`DeleteProfilePhoto` 只删调用者的 attachment；仅当「无 `profile_photos` 再引用」且「`media.user_id` == 调用者」时才删 media 行并返回可删对象标志，handler 只有收到该标志才删 R2 原图/缩图。`RemoveMediaOwned`（上传/海报回滚）同样加引用计数与 `RowsAffected` 守卫。非属主删除最后一个 attachment 时保留 media 行，留给属主或离线清理。
- 决策：上传原图入库前重编码（JPEG q90 / PNG），先解析 EXIF Orientation（JPEG APP1 与 PNG eXIf）并 auto-orient，再生成原图与缩图；JPEG/PNG 均不保留 EXIF/GPS/XMP/文本元数据。缩图沿用同一方向。迁移存量原图**不做批量重写**；迁移工具在上传前同样用 Pillow 重编码去元数据（见 `docs/MIGRATION.md`）。
- 决策：`MEDIA_MAX_PIXELS` 默认 `12_500_000`（12.5MP），覆盖 iPhone 12MP 原图 4032×3024=12,192,768；单边 8000px、单张 10MiB 不变；注册表单理论上限 92MiB 与 nginx `96m` 保持一致。HEIC 仍不支持（不新增 pillow-heif 等重依赖）：服务端 400 文案与 SPA 上传前检测均提示「先在相册转成 JPEG 或关闭高效」。
- 口径：`docs/PROJECT_CHARTER.md` §3.3 仍写 12MP；本实现按 **12.5MP 实现口径**执行（12MP 手机实际输出 4032×3024=12,192,768，12,000,000 的旧上限会拒绝），**不改 Charter**，留 PM 复核后决定是否同步权威文本。
- 取舍：重编码不保留 ICC 色彩配置（JPEG APP2/PNG iCCP），像素值按 sRGB 解释；属「一律去元数据」的已知代价，若真机反馈 Display P3 偏色，再评估保留 ICC 或色彩管理。
- 边界：不改 schema、不改公开路由与权限；R2 对象 key 不进日志（错误与清理日志只带 `media_id`）。历史 R2 对象清洗不在本次范围。非属主删除最后一个 attachment 时会留下 refs=0 的 media 行与 R2 对象；当前无 GC，记 P3（staging 量小，未造成越权访问）。
- 证据：`TestIntegrationProfileSharedMediaDelete`、`TestProcessStripsEXIFAndAppliesOrientation`、`TestProcessOrientationNonSquare`、`TestProcessPNGEXIFOrientation`、`TestProcessStripsPNGTextChunks`、`TestProcessPixelLimitBoundary`、`TestProcessRejectsExactlyOnePixelOver12_5MP`、`TestProcessAcceptsIPhone12MP`、`TestUploadErrorDetailsHEICActionable`、`TestIntegrationHTTPFlow`（HEIC 400 文案）、`tools/test_migrate_testapp.py`（GPS 去元数据、方向、12.5MP 上限）。

## 25. E2 相互喜欢展示微信号、D10 每周推荐位不做（2026-10-24）

- 背景：后端审计（`.team/audits/2026-10-24-bugs-backend.md` P1-8/P1-10）确认 E2 未交付（mutual 列表无微信字段，SPA 只有「微信号已交换」文案）且 D10 `weekly_recommendations` 0 引用；两项均待 PM 决定是否首版 Must-Have。
- 决策（PM 2026-10-24）：**E2 不做，首版不展示双方微信号；D10 不做，不做推荐位/每周推荐。** 对应产品规格条目保留为历史条目并标注「不做（PM 2026-10-24）」，不删除、不勾选，移出验收与缺口追踪。
- 后果：相互喜欢仅保留列表与通知，不交换联系方式；`weekly_recommendations` 表与 `CardView.Recommended` 保持未接线，作为已知死表/死代码保留，不再以 D10 名义补实现；`contact_consent` 不再作为 E2 前置，P1-12 的展示门控随之失效。
- 边界：本次只改文档，不改代码、schema、路由与 UI；SPA mutual 页现有「微信号已交换」文案与实际不符，留后续文案清理；注册硬编码 `contact_consent=true` 的明示同意/合规口径另行处理，不因本决策视为已解决。
- 证据：`docs/product-spec.md` E2/D10 与「首版明确不做」；`docs/STATUS.md` §5「明确不做」；审计 P1-8/P1-10 标注关闭。

## 26. D10 每周推荐位按 v1 原版补实现（2026-10-24）

- 背景：第 25 节按 PM 2026-10-24 的决定「D10 不做」，`weekly_recommendations` 因此保持 0 引用、`CardView.Recommended` 永远 false。同日 PM 追加要求「按 v1 原版补 D10 每周推荐」，第 25 节中 D10 部分由本节取代；E2（相互喜欢展示微信号）另见第 27 节。
- 决策：选人规则逐条移植 v1 `lib/recommend.py` 到 `internal/matching/recommend.go`：推荐位总数 `max(7, 必显数)`、算法位保底 3、每周保留 4 个（先被必显占）、必显=匿名喜欢我的异性单身（最近优先、去重、排除自己与相互喜欢）、ISO 周跨年连续、同周只剔除失效并补空位；相似度复用 `matching.MatchScore`（100 分制，缺失中性 0.5），不新增第二套打分。
- 决策：生成任务用 `cmd/server` 现有后台循环（启动补跑 + 整点检查），不新增常驻进程；`db.GenerateWeeklyRecommendations` 在单事务内读取用户/喜欢/上一周名单，`pg_advisory_xact_lock` 串行化，名单未变化时不重写（幂等且不刷新 `created_at`）。
- 决策：`weekly_recommendations` 就地修正——主键改为 `(week, user_id, position)`、新增 `target_user_id`；`user_id` 为名单所有者，`pinned_by_user_id` 非空表示必显位且值为喜欢名单主人的用户，只在服务端保存。不新建表，避免再造一张死表。
- 决策：读取侧在 `buildCardsPageData` 把本周推荐对象作为 `OrderCards` 的 pinned 列表整段置顶（必显在前），并置 `CardView.Recommended=true`；`/legacy/api/cards` 既有 `recommended` 字段直接生效，SPA 既有「推荐」角标不新增前端代码。
- 决策：管理端不新增页面，`/admin/matching` 加「本周推荐位」只读区块（覆盖人数、条目数、生成时间、最近 20 人）与手动重跑按钮；权限沿用 `matching.run`，CSRF 与审计 `admin.recommend.run` 不变。管理端与用户接口都不展示必显原因。
- 决策：学历档位补 `大专以下 = 1`（与 v1「高中及以下」同档），否则 v2 资料页新选项在 `MatchScore` 里会被当成未填而拿中性分；golden fixtures 未使用该值，一对一历史结果不受影响。
- 边界：不恢复 v1 把名单写多维表格/文件的行为；不展示推荐理由；观察员与性别未填用户不生成名单；推荐对象仍需在卡片候选池（有照片、符合筛选）中才可见，候选不足时名单可短于 7 个；不做历史周回填。
- 证据：`internal/matching/recommend_test.go`（槽位/轮换/必显≥4/幂等/空池/ISO 跨年/打分）、`TestIntegrationWeeklyRecommendations`（16 用户 + 喜欢关系，推荐位/轮换/必显/幂等/失效剔除）、`TestIntegrationRecommendationAdminPage`（RBAC/CSRF/审计/不泄漏必显）、`TestIntegrationWeeklyRecommendationsOnCards`（置顶 + `recommended` + SPA 角标）。

## 27. 身份证 P0 补填+服务端门禁；相互喜欢按双方就绪交换微信号（2026-10-24）

- 背景：第 25 节把 E2 记为不做后，PM 重新确认需要按 v1 原版补齐两件事：注册必填微信号（v1 `_REGISTER_RULES`），以及迁移用户 `id_card_valid=false` 的补填与服务端门禁（审计 P0）。staging 1103 个 active + 4 个 observer 被客户端门禁拦住且无补录入口；服务端 `/legacy/api/*` 完全未校验 `id_card_valid`。
- 决策（微信号）：注册表单微信号必填并做长度/字符校验（5–64 个 ASCII 字母、数字、`_ - . @ +`），写入 `users.wechat`；资料编辑保留可改。数据导入重复执行时，数据库已有非空微信号不得被源记录覆盖；身份证后四位与 `id_card_valid=true` 同样不被回退。
- 决策（相互喜欢交换）：以 `users.wechat` 为联系方式，`contact_consent=true` 为“同意在相互喜欢后交换”。资料已有微信号且已同意的，直接视为就绪；没有微信号或未同意的，在「相互喜欢」页填写并显式打开同意开关。**只有双方都就绪**时才在 mutual API 返回 `their_wechat`，并给双方各发一条站内通知，正文含对方微信号；未就绪返回空值并给 `wechat_required`/`wechat_status`（`need_wechat`、`need_consent`、`waiting_other`、`ready`）。通知用 `mutual-wechat:<低ID>:<高ID>:<收件人ID>` 去重，重复读取不重发。
- 决策（身份证 P0）：资料编辑在 `id_card_valid=false` 时显示「身份证号」补填字段，仅接受 18 位、校验位正确、证内生日与资料生日一致、未被其他账号使用的号码；通过后 AES-GCM 加密写 `id_card_ciphertext`、写 `id_card_last4` 与 HMAC 唯一哈希 `id_card_hash`、置 `id_card_valid=true`，审计只记后四位。服务端门禁对非豁免用户拦截 `/legacy/api/*` 与 `/app`、`/admin` 页面：仅放行首页、资料（含照片）、通知、改密、退出、版本/埋点与 mutual 微信号表单；其余返回 403 `id_card_required`（JSON/HTML 按路径）。SPA 全屏门禁保留，但以服务端 `id_card_valid` 为准，补填成功后解除。
- 观察员口径：观察员继续豁免 `has_face`，**不豁免身份证**（PM 2026-10-22）；观察员 `id_card_valid=false` 时同样 403，资料接口仍可读到 `id_valid=false`。为拆掉「资料只读 + 门禁拦截」死锁，观察员资料页只暴露「身份证号」一个可写字段（服务端拒绝任何夹带的其他字段），补填成功后恢复只读。
- 边界：不改注册审核状态机、不改 v1 的喜欢/通知匿名语义；相互喜欢通知只在双方就绪时发送，绝不把匿名喜欢者身份带入 mutual 页。`id_card_hash` 只用于唯一性，不写日志/响应；存量密文不批量解密回填哈希。
- 证据：`TestIntegrationIDCardGateAndBackfill`、`TestIntegrationIDCardBackfillUniqueness`、`TestIntegrationObserverStillBlockedByIDCard`、`TestIntegrationMutualWechatExchange`、`TestIntegrationMutualWechatDoesNotLeakAnonymousLike`、`TestWechatIDValidation`、`TestBackfillIDCardRequires18Digits`、`tools/test_migrate_testapp.py`（微信号空值不覆盖）。

## 28. 性能批次4：壳内联首页 bootstrap 的数据边界与前端兜底（2026-10-25）

- 背景：把首页聚合内联进 `/`、`/app` 壳后，壳从「无数据」变成「含个人首页数据」。壳仍为 `private, no-cache`（浏览器可落盘、304 可复用），而原 `/legacy/api/home` 是 `no-store`；若整份载荷内联，联系方式与身份证字段的落盘口径会比 API 更宽。
- 决策：壳内 bootstrap **只含非敏感首页数据**。服务端 `sanitizeBootstrap` 递归剔除**按模式匹配**的键：含 `wechat` 或 `phone` 子串（覆盖 `wechat`、`wechat_id`、`wechat_payment`、`phone`、`telephone`、`likes.mutual[].my_wechat`、`their_wechat`）、以 `id_card`/`idcard` 开头（大小写不敏感，覆盖 `id_card_last4`、`id_card_no`、`IdCardHash`）；门禁类布尔（`id_valid`、`has_face`、`account_status`）与展示字段保留。精确键名白名单此前漏掉互喜的 `my_wechat`/`their_wechat`，故改为模式匹配。
- 决策：互喜页的微信号预填/展示只取自 `no-store` 的 `/legacy/api/likes/me`（`loadLikes()` → `normalizeMutualItems` 用 `my_wechat`、`their_wechat`），壳内 bootstrap 的 `likes.mutual` 只保留昵称/状态等非联系方式字段。`/legacy/api/home`、`/legacy/api/likes/me`、`/legacy/api/profile` 的字段与 `no-store` 语义不变。壳的 ETag 覆盖 bootstrap 字节（64bit hash）+ `assetVersion` + 用户 id，`Vary: Cookie`（**壳**；API 的 Vary 实测为空），因此 304 只复用逐字节相同的个人数据。
- 决策：Vue 采用 runtime-only 3.4.27（与官方 `vue.runtime.global.prod.js` 字节一致）后，**不再承诺「`app.render.js` 缺失时回退运行时编译」**。改为显式失败可见：`Vue` 未加载或 `window.__YXQ_RENDER__` 非函数时，`app.js` 渲染 `.boot-fail` 错误页（含原因与「重新加载」）并写 `console.error('[yxq] boot failed')`；相关文档与注释同步改写。
- 决策：Vant 不再作为壳里的独立 `<script>`，改由 `app.js` 在 Vue 就绪后注入（`<link rel=preload>` 保留；批次10 起该 script preload 已随首屏优化移除）。原因：Vant 是依赖 Vue 的 UMD，Vue 缺失时它会先抛 pageerror，盖住 `app.js` 的统一错误页；按需注入后「Vue 缺失」「Vant 缺失」「渲染产物缺失」三种情况都只显示 `.boot-fail`，无 pageerror（`app_integration` smoke 三用例覆盖）。代价是首屏挂载等 Vant 加载完成；批次10 起 Vant 也改为 hero 绘制后按需注入，不再有 script preload。
- 决策：静态预压缩门禁以「内容」为准而非「压缩字节」：`tools/static_gzip -check` 校验 `.gz` 存在且解压后与源文件逐字节一致，`-strict-bytes` 仅本机重生后使用。原因：flate 输出跨 Go 版本不同（1.24.7 与 1.27.3 字节不同、内容相同），go.mod/CI 是 1.24.x；`ci.yml` lint-build 执行该检查，新增源文件忘生成 `.gz` 必红。
- 决策：边缘 `Accept-Encoding` 两态规整。`map $yxq_static_enc` 只把「裸 gzip token」或「q 值含非零数字」判为 gzip（`x-gzip`、`q=0`、非法 q 落 identity），回源再经 `map $yxq_static_enc $yxq_upstream_ae` 发送 `gzip` 或空值。这样 Go 与边缘永远做同一决定，不会出现「identity 缓存键里存 gzip 字节」这种会伤客户端的错配。
- 边界：不改媒体授权/隐私语义；不把 bootstrap 写入共享缓存（`private` + `Vary: Cookie`（壳），边缘 `/` 无 `proxy_cache`）；`/legacy/api/home` 与 `/legacy/api/profile` 的字段与 `no-store` 不变。
- 证据：`TestSanitizeBootstrapStripsSensitiveKeys`、`TestIntegrationShellBootstrapOmitsSensitiveFields`（壳无 phone/wechat/值，API 仍是 no-store 且含联系方式）、`TestAcceptsGzip`（x-gzip/q=abc/冲突 q）、`tools/static_gzip -check` 在 Go 1.24.7 与 1.27.3 均 OK、`app_integration` smoke 新增 `run_bootstrap_fallback`（故障注入走骨架路径）。

## 29. 性能批次5：壳内首卡 hero 保持与首屏任务拆分（2026-10-25）

- 背景：批次4 后 LCP 元素变为 Vue 挂载后新建的 `IMG.card-photo-item`，热启 LCP 仍受 2–3s 的挂载长任务限制；静态骨架里的首图在挂载时被删除，新元素无法继承更早的绘制时间。
- 决策：`/app` 壳静态渲染首卡 hero，几何**复用**牵线页的 `.cards-page`/`.swipe-card`/`.card-photo-wrap` 类（而不是复制尺寸常量），首卡缩图 URL 来自同一 bootstrap；Vue 首卡图 decode+paint 后 hero 淡出，避免新 LCP 候选被长任务重置。hero 只在已登录壳注入，交互/切 tab/浏览器回退/深链/restore/换卡/门禁/启动失败时立即移除；无卡、无图、首图加载失败退化为 shimmer 骨架，不留白屏或破图。
- 决策：首屏长任务拆分为「Vant 执行」与「mount + 卡片页首次渲染」两段：Vant 注入前让出两帧（`requestAnimationFrame` 双帧 + 1s 兜底），Vant onload 后 `setTimeout(0)` 再 mount；`onMounted` 后的 `bootAuthenticated`/`doLogin` 通过 `yxqAfterFirstPaint` 在首帧后启动，保证 hero 有绘制机会。新增 `yxq-mount`（mount+首屏骨架）与 `yxq-boot`（mount→卡片页 DOM patch）两个 Performance mark，供 perf_boot.py 采证。
- 决策：牌堆**不新增** ±1 预渲染 DOM。复核批次4 部署版本：bootstrap 保留 20 张卡数据，但模板只对 `currentCard` 渲染 `.swipe-card`，实测 DOM 160/161 节点、`.swipe-card=1`；不存在「挂载渲染 20 张卡 DOM」。新增 Go 单测与 Playwright 断言锁定「数据窗口可多张、DOM 只 1 张」，避免后续误加窗口导致节点数上升。
- 边界：不改认证/CSRF/RBAC/审计/媒体授权语义；hero 不含敏感字段（只复用已脱敏 bootstrap 的首卡缩图）；不改牌堆滑动、回退、restore、图片预取行为；`assetVersion` 10→11。
- 证据：`TestLegacyShellRendersStaticHero`、`TestDeckRendersOnlyCurrentCard`、`TestBootstrapPreloadUsesThumbURL`；`make ci`（Go 1.24.7）；5 个既有 smoke + `app_hero_deck_smoke.py`（390/1280/375/768、暗色、CLS、无卡/首图失败）；`tools/browser/perf_boot.py` 冷热 N=3 数据见 `.team/perf/batch5-report.md`。

## 30. 性能批次7：通知即时性边界、SSE 兜底与 staging PG 参数（2026-10-25）

- 背景：批次7 把通知 SSE 从「每 4s 查询」改为 `LISTEN/NOTIFY` 唤醒，并把 staging PG 参数从默认值调到面向小内存单机的组合；独立 Review 认为需要在决策层明确语义边界与运维口径（N1/N4/N10/T1）。
- 决策（即时性）：**只有应用写入路径**（创建通知、标记已读）在同一事务 `pg_notify('yxq_notify', user_id)`，客户端通常 <1s 收到；绕过应用直接写库（psql、迁移脚本、外部修复）不触发 notify，最坏等兜底 ticker（无连接 15s / 已连接 30s；实测 25.7s）。因此「页面不刷新就看到红点」这类断言只对应用写入成立；测试/运维直插数据后需要显式 `SELECT pg_notify(...)`。
- 决策（兜底与心跳）：保留 30s（无连接时 15s）兜底与 15s 心跳，作为重连遗漏/直插数据的保险；兜底间隔在连接建立时一次性决定，hub 之后重连不切换已建连接（多一倍查询可接受）。空闲/无订阅者不做业务查询。
- 决策（并发安全）：`fanout`/`broadcastAll` 必须在锁内复制订阅者、解锁后再非阻塞投递；禁止在解锁后遍历 live map（退订会 delete 同一 map，Go runtime 的并发 map 迭代/写入是 fatal，不可 recover）。`internal/db` 单元测试纳入 `make test-race` 与 CI unit job（无 DB，约 8s）；`internal/httpserver` 的 -race 仍由 CI integration job 覆盖。
- 决策（PG 参数）：staging 单机 PG16 采用 `shared_buffers=512MB`、`work_mem=8MB`、`random_page_cost=1.1`、`effective_io_concurrency=200`、`jit=off`、`track_io_timing=on`；`effective_cache_size` 维持默认 4GB（仅影响 planner 估算，先观察其他项收益，若 `pg_stat_statements` 显示低估再降 2–3GB）。值写 `postgresql.auto.conf`，`/etc/postgresql` 不动，回滚 = `ALTER SYSTEM RESET` + 重启（`/var/backups/yxq-pg-b7/postgresql.conf.<ts>` 只是未修改的 /etc 副本，不存在 `.bak` 属正常）。影响同实例全部 DB（含 test 库）；重启窗口 2026-10-25 18:04:07，数秒。
- 边界：不改通知内容/未读数语义、不改 RBAC/审计；SSE 事件字段不变；PG 参数调整属运维变更，生产集群需另行批准。
- 证据：`TestNotificationHubFanoutConcurrentSubscribe`（`-race`；旧实现可复现 DATA RACE）、`TestNotificationHubFanoutDeliversToUserOnly`、`TestInflightWaiterHonoursOwnContext`、SSE 集成用例（双客户端/隔离/重连/直插 15–30s/空闲零查询）、`make test-race`、`make ci`。

## 31. 性能批次8：缩略图磁盘缓存与壳 critical CSS（2026-10-25）

- 背景：媒体 thumb 边缘 TTFB ~557ms（R2 取对象 + 跨洋），是首屏 LCP 的主要剩余项；壳 38.4KB gzip 仍含整份 89KB CSS。
- 决策（缩略图缓存）：只缓存 `?size=thumb`，上限按总字节 LRU（`MEDIA_CACHE_BYTES` 默认 256MiB，目录 `MEDIA_CACHE_DIR`）；
  key 含 mediaID + thumb object key + 字节数 → 替换照片自然失效；删除经 `removeObject` 触发按 mediaID 前缀清理；
  **授权在缓存之前**（handler 先鉴权再读缓存），缓存层不参与鉴权也不缓存原图。预热用 `cmd/thumbwarm`（并发≤8，只读 R2/DB）。
- 决策（壳 CSS）：首屏只内联 `app.critical.css` 子集（生成物，≤15KB gzip；当前 6.3KB），完整 `app.css` 以
  `media=print onload` 异步加载；critical 由 `tools/css_split/build.py` 从 app.css 顶层块复制生成，`check.py` 校验
  「每块逐字存在于 app.css」与预算，禁止手改。壳 gzip 38,153B→25,380B。
- 决策（bootstrap 不裁剪）：壳 gzip 已达 ≤28KB 目标，本轮**不**裁剪 bootstrap JSON（34KB raw/4.3KB gzip）——
  裁剪需要前端首屏数据契约/补拉逻辑改造，收益 ~3-4KB gzip，风险与收益不匹配；留批次8.1 评估。
- 决策（管理台延迟口径）：批次7 的「edge p95 1.3s」来自 server1→边缘的**冷连接**测量；边缘本地（server2）实测 admin
  p95 251–314ms（≤400ms）。后续报表必须标注测量点（server2 本地 = edge；server1 = 跨洋回源视角）。
- 边界：不改媒体授权/隐私语义、不改 ETag/Cache-Control（thumb 仍 private, max-age=86400, immutable）；不改 nginx。
- 证据：`TestThumbCache*`（含 LRU/purge/重载/并发）、`TestServeThumbUsesDiskCache`、`TestServeThumbCacheInvalidatesOnReplacedObject`、
  `TestServeOriginalNeverUsesThumbCache`、`tools/test_css_split.py`、`make spa-check`、`batch8_critical_css.py`（阻断 app.css 首屏仍正确）。

## 32. 性能批次9：10s 读缓存、边缘 gzip 变体与验收口径（2026-10-26）

- 背景：Auditor 复核发现 edge home p95 262/271ms、origin home/cards p95 62.5/45.1ms、冷 LCP 2.60–4.14s 不稳定；
  同时口径漂移（home 查询 15 vs 目标 ≤8）。
- 决策（读缓存）：`/legacy/api/home`、`/legacy/api/cards` 的成功响应在进程内缓存 **10s**，键=用户+kind+viewRole+RawQuery
  **+全局世代号 epoch**；**任何成功的非 GET/HEAD/OPTIONS 请求**后 `epoch++`（中间件统一），覆盖
  `/legacy/api/*`、`/api/*`、`/admin/*`、`/media/*`（含管理员调整积分/照片上传），因此点赞、资料、照片、报名、
  审核、管理员写都会立即 flush 全站读缓存；**排除** `/static/`、`/healthz` 与埋点（`/legacy/api/track*`、
  `/legacy/api/client-events`），避免高频埋点刷穿缓存；GET 与 4xx/5xx 不 bump。
  内存：home 与 cards **共享同一个缓存实例与同一份预算**——总字节 `READ_CACHE_BYTES`（默认 32MiB，**键长也计入**、按字节 LRU；
  配置上限 clamp 到 64MiB，避免超过 unit 768M）+ 单条 256KiB + 总条数 1024；
  `READ_CACHE_ENABLED=false` 或 TTL/ITEMS/BYTES 任一为 0 → 完全关闭（零值 Config 同样关闭，不回落默认）。
  构建期间 epoch 变化则丢弃回填（CAS，避免旧数据进新代）。多实例部署需 pub/sub 传播 epoch（未实现，单实例语义）。
  错误/未授权/重定向不缓存；响应头 `Cache-Control: no-store`；`/legacy/api/*` 的 `Vary` **实测为空**（不要声称）。
- 决策（边缘，**不做 API 缓存**）：原方案在 server2 对 `/legacy/api/` 做 proxy_cache，key 需含会话才能不串用户，
  但 `$cookie_yxq_session` 会把**明文 session token 写进 server2 磁盘**（违反「session token 只存 hash」）；
  不含会话则会跨用户命中。该方案**已删除、未 apply**；API 提速改由应用内 10s 读缓存 + 上游 keepalive 承担
  （目标边缘 p95 ≤220ms）。未来若确需边缘 API 缓存，前置条件是用 `HMAC(server_secret, session)` 派生不可逆 key，
  且需 PM/安全评审，本批不做。
- 决策（边缘静态）：`/static/` 保留 gzip/identity 变体缓存（key 含 `$yxq_static_enc`，不涉及 cookie）；
  2026-10-26 复核：gzip 命中 47,033B（=预压缩字节）、单层 gzip、identity 变体独立、q=0/非法头落 identity、
  同连接命中 0.87/1.89ms。
- 决策（查询口径，**待 PM 确认，不静默改验收**）：home 现为聚合接口（profile+viewer+额度+赞+cards+self+likes+activities），
  15–16 条 SQL 是聚合口径；cards 8 条。建议验收目标改为 **home ≤16（聚合）、cards ≤8**；在 PM 书面确认前，
  本文件只记录「现状与建议」，不修改 `docs/product-spec.md` 的既有勾选/目标。
- 边界：不改 RBAC/审计/媒体授权；不缓存个人敏感响应到磁盘（边缘缓存 TTL≤10s，属短期内存/磁盘缓存，需监控清理）。
- 证据：`TestReadCache*`、`TestIntegrationLegacyHomeCardsReadCache`（命中 0 build 查询/写路径失效/跨用户隔离）、
  `bench_rounds.py` 三轮区间。


## 33. 批次10：首屏渐进启动与跨用户照片授权（2026-10-26）

- 背景：staging 慢机（QEMU vCPU、CPU×4/1.6Mbps）冷 LCP 3.74s / 热 1.91s，最长任务 2.10s/0.83s；
  壳内真实首卡 hero 已存在，但 `vue.global.prod.js` + `app.render.js` + `app.min.js` 的解析/执行/挂载长任务
  发生在 hero 绘制之前。同期审计发现 `/cards/photo/{uuid}` 只校验「双方可浏览」，任意登录用户可用他人
  media UUID 取图；`/profile/photo/{uuid}` 是同类越权端点，`/legacy/api/users/{id}/public` 还会下发照片 URL。
- 决策（1B 渐进启动）：三个主脚本不再用静态 `defer` 标签执行；**移除全部 script/style preload**（实测与首图抢带宽），由壳内 loader 在
  **首卡 hero 图片 load+decode 并提交一帧后**按 `vue → app.render → app.min` 顺序注入/执行（硬上限 2.5s，
  无 hero 时 600ms 后启动）。启动前点击/按键只记 `__YXQ_BOOT_PENDING_INTERACT__`，不重放、不报错；
  `loadHome` 4s 硬兜底、hero 生命周期、管理台 iframe 语义不变。Vue/Vant 缺失仍由 app.js 渲染可见错误页；
  app.min.js 缺失由壳内联 `__YXQ_BOOT_FAIL__` 渲染错误页。启动/hero 时间写入 `yxq-app-start`/`yxq-hero-ready`
  PerformanceMark，供 `perf_boot.py` 采集。
- 决策（2A 照片授权）：`/cards/photo` 与 `/profile/photo` 共用同一策略，**默认拒绝**；授权对象始终是画廊主人
  `profile_photos.user_id`（迁移数据中可能与 `media.user_id` 不同，共享 media 只用于取对象）；放行条件仅为：
  本人、任一 RBAC 管理员角色、**任一方向的有效喜欢（含互喜）**、未取消的同活动报名（同活动为并集，覆盖同组/同轮）、
  **任一方向消息的对手方**。匿名 401、无权/不存在 404（不泄露存在性）；禁用账号仍由账号门禁返回 403。
  跨用户 owner 仅限 `active/matched/observer`（pending/rejected 仅本人可见）；observer 本人照片可读。
- 决策（候选池）：**不采用**「候选池 = 所有活跃异性用户」的宽口径（那仍可用 UUID 扫全站）；候选由
  cards/home 构建时按本次真正下发的**目标用户 ID** 登记 per-viewer 短时 grant（TTL 15 分钟，单 viewer ≤2000 个目标、
  全局 ≤2000 viewer；每 viewer 单一有序目标集合（merge/去重，15m TTL 刷新），超 2000 只淘汰最低 ID，viewer 按 lastSeen LRU fail-closed）；最坏 2000×2000×8B=32MiB + slice/map 头（≤64MiB）。图片请求先查 grant，命中走无关系子查询的单条索引 SQL，
  未命中走关系 SQL。公开资料页**只按 viewer 与目标本人（画廊主人）的关系**（本人/grant/`CanViewGallery(target)`）返回照片 URL，不得复用 media 级关系/其他画廊 grant，否则共享 media 会越权；不通过返回空数组。
- 决策（不采用签名 URL）：本批用进程内 grant 而非 HMAC 签名 URL，避免壳 bootstrap 的签名/过期时间
  造成 ETag 抖动；代价是**多实例部署需改共享 grant 或签名 URL**，进程重启后首次图片请求可能回落到关系
  SQL/404（浏览器已缓存的私有图片不受影响），作为已知限制记录。
- 性能：migration `0010_photo_authz_indexes.sql` 只加索引（`profile_photos(media_id)`、`likes(target_id,initiator_id)`、
  `signups(activity_id,user_id)`、`messages(author_id,target_user_id)`）；staging 克隆 EXPLAIN 实测单图授权
  14.3ms → **1.9ms**（无 `profile_photos_media_idx` 时全表扫 1028 行）。cards 批量照片在既有查询中一次取回，
  grant 循环 O(n) 无 SQL，15 图不产生 N+1。
- 边界：不改管理台受权媒体串流 `/admin/...`、RBAC/CSRF/审计语义；不改照片 `Cache-Control`/ETag；
  migration 只增不改；staging 部署前仍需独立 Reviewer + security-gate/Auditor；生产未授权。
- 证据：`TestIntegrationCardPhotoAuthorizationMatrix`（本人/陌生人/候选/喜欢/互喜/同活动同组/消息/管理员/
  观察员/禁用/匿名/猜测 ID）、`TestIntegrationPublicProfileHidesUnauthorizedPhotos`、`PhotoGrantStore*`、
  `TestIntegrationLegacyProfileFlow`（陌生人 404 → 候选 200）、
  `TestIntegrationGrantOwnerDeckPagination`（60 候选×9 图翻页 540/540 张 200）、
  `TestIntegrationPhotoGrantCrossViewerAndRevocation`（跨 viewer/喜欢/消息/报名撤销/owner 禁用）、
  `TestIntegrationObserverSelfAndDisabledViewerGates`、`make ci`/`make test-race`/`make test-integration`。

### 33.1 批次10 追加：首次 tab 无「加载中」文字（PM 2026-10-26）

- 需求：进入 `/` 后首次点击 4 个 tab 不得出现「加载中...」文字；chunk 未就绪时不得文字中断。
- 实现：应用挂载且 LCP 条目出现后由 `requestIdleCallback`（timeout 100ms，另 2.5s 绝对兜底）按
  `likes → home → profile → user_detail → group_admin` 顺序预取页 chunk，复用 `yxqLoadPageChunk`
  的 in-flight 去重与缓存；预取不参与首屏图/主脚本下载。tabbar `before-change` 守卫在目标 chunk
  未就绪时**保留当前页**并等待加载完成（4s 超时）；加载失败走 `.boot-fail`，后台预取失败只跳过。
  `page-host` 兜底改为无文字同构骨架（深链/异常路径），`cards.html`/`home.html` 的数据加载态改为
  仅转圈、无文字。切换动画与 tabbar 状态不变。壳 bootstrap cards 由 20 降为 3；hero 图去掉 0.12s opacity 过渡，首帧即按 decode 后立绘。
- 证据：新增 `tools/browser/tab_first_load_smoke.py`（CPU×4/1.6Mbps）：首次依次点消息/活动/我的/牵线、
  快速连点、回退、深链 `#/likes`、管理台 iframe 全 PASS；`加载中` 文本命中 **0**、CLS noInput **0.0000**；
  预取完成后点击前 `yxqPageChunkReady` 对 likes/home/profile/detail/group 均为 true。
- 性能复测（同慢机本地实例，N=10）：冷 FCP/LCP **614/1144ms**（P95 1396）、热 **448/482ms**（P95 592），CLS 0；
  冷/热均达 ≤2.5/≤1.2s。共享 media 修复让壳内首图不再 404；bootstrap cards 20→3、hero 去 opacity 过渡、
  LCP 后预取共同生效。本地传输 430,741B/20；edge gzip 预取合计约 43.5KB，按 edge 基线 289,475B
  估算约 **333KB ≤350KB**（部署后以实测为准）。
