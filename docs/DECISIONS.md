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
