# 一线牵 v2 项目宪章（Source of Truth）

<!-- doc-meta
owner: pm
status: current
last_verified: 2026-10-23
sources: []
-->

- 版本：1.3
- 日期：2026-10-23
- 状态：生效；本文件优先于口头讨论与临时约定。
- 相关：`docs/product-spec.md`、`docs/DECISIONS.md`、`docs/STATUS.md`、`docs/MIGRATION.md`、`docs/runbooks/staging.md`。

## 1. 使命

把「一线牵」从飞书机器人 + 飞书 H5 改造成独立网页产品：

- 用户不需要飞书账号，也不在飞书聊天。
- 既有用户可从 testapp 迁移，先用临时密码启用，之后手机号＋密码登录。
- 用户端必须完整、可用、与原版基本无感。
- 管理台必须可用：审核、活动、匹配、积分、举报、角色权限。
- 数据库与媒体不依赖飞书；飞书只存在于 v1 旧系统。

## 2. 最高原则

1. **成品导向，不做 POC。** 每个功能都要能给真实用户使用。
2. **用户功能不可省。** `docs/product-spec.md` 的 Must-Have 全部通过前，不宣布完工。
3. **单一真相源。** PostgreSQL 是权威；R2 只存媒体/备份；Bitable 不是 runtime。
4. **用户端复用 v1，管理台 SSR。** 用户端用 v1 Vue 3 + Vant SPA；管理页面用 Go 服务端渲染，只经用户 SPA 的同源 iframe（`/admin?embed=1`）使用；浏览器顶层访问 `/admin` 一律 404，不提供独立页面。登录、注册、启用、密码页仍无 JS 可用。
5. **UI 可辨识、可延续。** 保留品牌、四个主入口、卡片浏览、主要用语与操作习惯；重写技术与大改版分开。
6. **安全与隐私默认开启。** 密码、session、CSRF、RBAC、审计、私有媒体、最小权限。
7. **先 staging 后生产。** 不在生产做实验；所有变更先过 staging 与审查。
8. **没有证据就不算完成。** 每个判准要有指令、输出、测试或验收记录。
9. **文字统一简体中文。** 代码注释、界面、提交说明与变更摘要一律简体中文；不重写已推送历史。

## 3. 架构硬约束

### 3.1 技术栈

| 层 | 决策 |
|---|---|
| 后端 | Go 1.24（以 `go.mod` 为准；CI 为 1.24.x） |
| 模板 | 管理台 `html/template`（SSR 顶层直开或由 SPA 内嵌 iframe 渲染）；用户端复用 v1 SPA |
| DB | PostgreSQL 16（staging `yixianqian_staging`；production `yixianqian`） |
| 媒体 | Cloudflare R2 私有桶；浏览器只经 app `/media/{id}` |
| 部署 | systemd + Nginx；server1 staging；未来 HK app server |
| 反向代理 | server2（HK）是唯一公开入口；server1 origin 只允许 server2（`45.125.16.205/32`）+ localhost（`127.0.0.1/32`，本机健康检查/nginx 需要） |
| 缓存/队列 | 不用 Redis；需要时再加，不以 Redis 为正确性前提 |

### 3.2 网络与 relay

- **DNS 不直指 server1，不开 Cloudflare 橙云**（两类 IP 均曾被运营商封锁）。
- `next.nantou.love` → server2（HK, `45.125.16.205`）→ server1（LA）→ `127.0.0.1:8093`。
- 上线前购买 HK app server；server2 只改 upstream，DNS 不动。
- allow gate 用 `geo $realip_remote_addr`，不能直接用 `allow`（global realip 会改写 `$remote_addr`）。
- `TRUSTED_PROXY=127.0.0.1`（本机 nginx）；server2 设 `X-Real-IP`，server1 nginx 转传。

### 3.3 媒体

- R2 桶：`yixianqian-media-staging`、`yixianqian-media`、`yixianqian-backups-staging`、`yixianqian-backups`。
- 全部 private；不开 `r2.dev`、不开 custom domain；不把 R2 URL 给浏览器。
- 上传限制：JPEG/PNG、10MiB、单边 8000px、12MP、并发 1；HEIC 返回 400。

### 3.4 凭证

- staging：server2 certbot 签 `next.nantou.love`，同步到 server1 `/etc/ssl/yxq-next/`。
- production：用 DNS-01（`*.nantou.love` 或 per-host）或自动同步，不长期手动搬 key。

## 4. 产品范围（摘要）

- **用户端**：登录/启用/注册与审核、资料与照片、牵线卡片与喜欢/留言、活动报名与签到、分组、一对一、麦穗、站内通知、观察员。完整验收项见 `docs/product-spec.md`。
- **管理台**：用户审核与详情、活动 CRUD/工作台/名单/签到/广播、分组与一对一控制台、麦穗、举报/反馈、RBAC、审计。
- **首版不做**：飞书 runtime、短信 OTP、即时聊天、用户端重做、第二套完整用户 UI、微服务/K8s。
- **上线阻断**：迁移用户身份证门禁死锁为 P0，见 `docs/STATUS.md` §4；验收前必须关闭。

## 5. UI 与兼容性

- 用户端保留 v1 四个主入口、卡片滑动、喜欢/点赞/留言、活动/分组/一对一的用语与流程；只修间距、层级、无障碍与键盘遮挡。
- 管理台可按运营效率自行设计，但使用简体中文、服务端权限与审计。
- 支持范围：iOS 14.5+/Safari 14.1+、Chrome/WebView 84+、Firefox 74+；低于范围不承诺完整 UI。登录、注册、启用、密码页无 JS 可用；管理台需要 SPA JavaScript。
- 不维护第二套完整用户 UI；若旧设备占比显著，另追加决策评估。

## 6. 资料迁移

- 来源为 testapp 的 Bitable/Drive/SQLite；目标为 PostgreSQL + R2；保留 `legacy_*` 键用于对账。
- 接受「大概一样」不算完成：必须有对账报告；测试资料可进 staging，生产资料不得进 dev。
- 详细来源、工具、对账口径与已知限制见 `docs/MIGRATION.md`。

## 7. 安全与隐私

- 密码 Argon2id；session/activation token 只存 hash；cookie HttpOnly/Secure/SameSite。
- CSRF：表单 + API header；登录/启用轮换。
- 登录失败锁定、IP/phone rate limit；Argon2/图片并发上限。
- disabled 用户登录一律 401，不建 session；CreateSession SQL 也检查 status。
- RBAC server-side；保护最后一位 `super_admin`；audit 所有管理操作。
- 媒体私有、owner-only；不直连 R2；不公开 activation token/密码。
- production fail-fast：Secure cookie、https BASE_URL、https R2、明确 `TRUSTED_PROXY`；不把密钥写进 repo。

## 8. 交付流程

1. PM 定范围与验收标准；`docs/product-spec.md` 的勾选是验收目标，不是实时进度。
2. 执行者实现；审查者独立对抗审查；必要时由独立验收者复核证据。
3. CI 必须绿：gofmt/vet/test/race/build 与 PostgreSQL integration。
4. 部署 staging；做 E2E、真机、视觉与安全检查。
5. 用户试用；不到「完整可用」不宣布完工。
6. 生产切换另立 runbook；v1 保留 rollback。
7. 偏离本宪章的决策追加到 `docs/DECISIONS.md` 并更新本文件。

## 9. 目前状态

- 代码能力已完成：Go/PostgreSQL/R2、activation/login/session/media、testapp 迁移、staging relay、用户端 API 与管理台。
- 用户端复用 v1 Vue 3 + Vant SPA；管理台 SSR 可顶层直开，也可由 SPA 内嵌 iframe 承载（见 `docs/DECISIONS.md`）。
- **验收与上线阻断见 `docs/STATUS.md`**：当前 P0 为迁移用户身份证门禁死锁，product-spec 尚未签核。
- 生产未切换：`app.nantou.love` 仍是 v1；上线前购买 HK app server，server2 只改 upstream。

## 10. 变更记录

- 2026-10-21：建立宪章；确认 Go/PostgreSQL/R2、server2 relay、成品导向。
- 2026-10-22：确认用户端沿用 v1 Vue 3 + Vant SPA，Go 提供 API 适配与媒体代理。
- 2026-10-23：v1.3。文档集精简为 10 个 md；旧决策记录合并为 `docs/DECISIONS.md`；Go 版本固定 1.24；管理台入口与无 JS 范围按现网行为固化。
