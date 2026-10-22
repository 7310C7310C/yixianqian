# 一线牵 v2 专案宪章（Source of Truth）

- 版本：1.1
- 日期：2026-10-22
- 状态：生效；本文件优先于口头讨论与旧 `yixianqian-deploy` skill。
- 相关：`docs/product-spec.md`、`docs/adr/*`、`docs/runbooks/staging.md`。

## 1. 使命

把「一线牵」从**飞书机器人 + 飞书 H5** 改造成**独立网页产品**：

- 用户不需要下载飞书、不需要飞书账号、不需要在飞书聊天。
- 既有用户可从 testapp 资料迁移过来，先用临时密码启用，之后手机号＋密码登录。
- 用户端功能必须**完整、可用、与原版基本无感**（可滑卡片、喜欢、点赞、留言、活动、分组、一对一、麦穗等）。
- 管理端也必须可用（审核、活动、分组、积分、举报、角色权限）。
- 资料库与媒体不依赖飞书；飞书只存在于 v1 旧系统，不再进入 v2 runtime。

## 2. 最高原则

1. **成品导向，不做 POC。** 每个功能都要能给真实用户使用，不是 demo。
2. **用户功能不可省。** 在 `docs/product-spec.md` 的 Must-Have 清单全部通过前，不宣布完工。
3. **单一真相源。** 新系统以 PostgreSQL 为权威；R2 只存媒体/备份；不把 Bitable 当 runtime。
4. **用户端复用 v1，管理端 SSR。** 用户端直接复用 v1 Vue 3 + Vant SPA，保留原版 DOM/CSS/AJAX/tab 行为；管理端使用 Go 服务端渲染。用户 SPA 需要 JavaScript；登录/注册/管理端仍保持无 JS 可用。
5. **UI 可辨识、可延续。** 不要求像素级复刻，但必须保留品牌、四个主入口、卡片浏览、主要用语与操作习惯；重写技术与大改版分开。
6. **安全与隐私默认开启。** 密码、session、CSRF、RBAC、audit、私有媒体、最小权限。
7. **先 staging 后生产。** 不在生产做实验；所有变更先过 staging 与审查。
8. **没有证据就不算完成。** 每个判准要有指令、输出、测试或验收纪录。
9. **GitHub 文字用简体中文。** commit message、PR 标题/说明、Release/变更摘要一律简体中文；分支名可用英文。不重写已推送历史。
10. **语言统一简体中文。** 项目文档、代码注释、用户界面、管理界面一律使用简体中文；用户端文案与操作逻辑以原版为准，管理端可自行设计、不受原版 UI/操作限制。

## 3. 架构决策

### 3.1 技术栈

| 层 | 决策 |
|---|---|
| 后端 | Go（目前 1.27；module `github.com/7310C7310C/yixianqian`） |
| 模板 | 管理端 `html/template`；用户端复用 v1 Vue 3 + Vant SPA |
| DB | PostgreSQL 16（staging `yixianqian_staging`；未来 production `yixianqian`） |
| 用户端 | v1 `index.html` + Vue/Vant 本地资产；Go 提供旧形状兼容 API 与媒体代理 |
| 媒体 | Cloudflare R2，私有桶；浏览器只经 app `/media/{id}` |
| 快取/伫列 | 目前不用 Redis；需要时再加，不以 Redis 为正确性前提 |
| 部署 | systemd + Nginx；server1 staging；未来 HK app server |
| 反向代理 | server2（HK）为唯一公开入口；server1 origin 只允许 server2 |

### 3.2 网络与 relay（硬约束）

- 实测：Cloudflare 橙云 IP 与 server1 美国 IP 都曾被中国电信商封锁。
- **DNS 不直指 server1，不开橙云。**
- `next.nantou.love` → server2（HK, `45.125.16.205`）→ server1（LA）→ `127.0.0.1:8093`。
- 上线前买 HK app server；server2 只改 upstream，DNS 不动。
- origin 以 `geo $realip_remote_addr` 做 allow gate；不能直接用 `allow`（global realip 会改写 `$remote_addr`）。
- app 的 `TRUSTED_PROXY=127.0.0.1`（本机 nginx）；server2 设 `X-Real-IP`，server1 nginx 转传。

### 3.3 媒体

- R2 桶：`yixianqian-media-staging`、`yixianqian-media`、`yixianqian-backups-staging`、`yixianqian-backups`。
- 全部 private；不开 `r2.dev`、不开 custom domain。
- 用户只拿 `/media/{id}`；app 从 R2 串流；不把 R2 URL 给浏览器（Cloudflare IP 可能被拦）。
- 上传限制：JPEG/PNG、10MiB、单边 8000px、12MP、并发 1；HEIC 目前 400（后续再评估）。

### 3.4 凭证

- staging：server2 certbot 签 `next.nantou.love`，同步到 server1 `/etc/ssl/yxq-next/`。
- 生产：改用 DNS-01（`*.nantou.love` 或 per-host）或自动同步；不要长期手动搬 key。

## 4. 产品范围

### 4.1 用户端 Must-Have（完整，不可省）

- 登录/启用：手机号＋密码；一次性启用 token；小天使可生成临时密码；退出登录；忘记密码（管理员重置）。
- 注册：非飞书网页注册、照片上传、人工审核、审核状态。
- 资料：我的资料、编辑、照片上传/删除/封面、身份证/人脸门禁（依原规则）。
- 牵线：异性卡片、左右滑动、照片轮播、筛选、喜欢（匿名/实名＋附言）、点赞、反馈/举报、资料页、留言。
- 消息：谁喜欢我、相互喜欢、我喜欢的、我的留言、收到的留言、动态通知、相互喜欢后交换微信号。
- 活动：列表、详情、报名/取消、缴费 QR 提示、报名名单（依原 feature flag）、自助签到（4 位码）、我的活动。
- 分组：候选、7 志愿、排序/预填、提交、状态、结果（轮次）。
- 一对一：必聊结果（轮次）、已聊标记、留言入口。
- 麦穗：余额、规则、邀请、兑换（实名/优先/心愿/红娘）、兑换记录。
- 观察员：只读角色与切换（依原规则）。
- 公开页：`nantou.love` 引流、注册 CTA、QR、埋点。
- 站内通知：喜欢/相互/审核/报名/活动/积分等；不依赖飞书。
- 聊天替代：不做即时聊天；相互喜欢揭露微信号，保留站内留言。

### 4.2 管理端 Must-Have

- 用户审核（通过/拒绝）、观察员码、用户查询。
- 活动 CRUD、广播、报名/签到名单、现场签到。
- 分组/一对一控制台：模式、preview/run/reset/job/result/roster/chat overview。
- 麦穗：总览、流水、兑换、邀请、加减穗、心愿/推荐单处理。
- 举报/意见反馈处理。
- RBAC：super_admin 可新增角色/管理员/权限；server-side 强制。
- 审计、汇出、备份。

### 4.3 首版不做

- 飞书机器人/表单/名片/Bitable runtime。
- 短信 OTP（先用手机＋密码＋小天使临时密码；短信可日后加）。
- 即时聊天/IM（用微信号交换＋站内留言）。
- 用户端重新设计；用户端直接复用 v1 前端。
- 为旧浏览器维护第二套完整用户 UI；如确有需要另立 ADR。
- 微服务/K8s。

## 5. UI/兼容性标准

- **用户端必须与原版基本一致**：四个主入口及顺序（牵线/消息/活动/我的）、标签、卡片式浏览、左右滑卡、喜欢/点赞/留言/活动报名/分组志愿/一对一结果的用语与操作流程。只做必要的间距、层级、无障碍、键盘遮挡修正，不改变用户心智模型。
- **管理端不受原版限制**：可按运营效率自行设计，但同样使用简体中文、服务端权限与审计。
- 保留品牌粉色、Logo、卡片视觉语言；不趁重写做大幅视觉改版。
- 支持矩阵（v1 前端原样复用，不转译、不补 flex-gap fallback）：
  - 完整：iOS 14.5+ / Safari 14.1+、Chrome/Android WebView 84+、Firefox 74+。
  - 低于上述版本：不承诺完整用户 UI；可能白屏或版面错乱。
  - 登录/注册/管理端仍以 SSR 提供基本可用性；用户 SPA 需要 JavaScript。
- 不维护第二套完整用户 UI；若未来真实数据表明旧设备占比显著，另立 ADR 评估转译 + CSS 兼容层。
- 用户端复用 v1 的 Vant/CSS；不引入外部阻塞 script/CDN；图片以 JPEG fallback。
- 视觉回归：旧版/新版关键页截图与操作行为对照；用户端以原版为准，管理端以可用性为准。


## 6. 资料迁移（testapp）

- 来源：`/opt/yixianqian-test` 的 Bitable + 飞书 Drive（只读）。
- 目标：PostgreSQL staging + R2 staging。
- 保留 legacy 键：`legacy_open_id`、`legacy_user_id`、`legacy_record_id`，用于对账与后续回溯。
- 流程：
  1. 汇出所有表 raw JSON + 字段 metadata。
  2. 转换到新 schema；多选存 JSON/关联表；日期用 timestamptz；金额/积分整数。
  3. 照片从 Drive 下载 → R2，建立 token→key 映射与 checksum。
  4. 汇入 staging；逐表笔数/checksum/抽样比对。
  5. 为既有用户生成 activation token；U-0006 用真实 testapp 资料。
- 不接受「大概一样」：必须有对账报告。
- 测试资料可进 staging；生产资料不得进 dev。

## 7. 安全与隐私

- 密码 Argon2id；session/activation token 只存 hash；cookie HttpOnly/Secure/SameSite。
- CSRF：表单 + API header；登录/启用轮换。
- 登录失败锁定、IP/phone rate limit；Argon2/图片并发上限。
- disabled 用户登录一律 401，不建 session；CreateSession SQL 也检查 status。
- RBAC server-side；super_admin 保护最后一位；audit 所有管理操作。
- 媒体私有、owner-only；不直连 R2；不公开 activation token/密码。
- production fail-fast：Secure cookie、https BASE_URL、https R2、明确 TRUSTED_PROXY。
- 不在 repo 写任何密钥；`.env`/`local_config` 不进 git。

## 8. 交付流程

1. 每个功能先写 acceptance criteria（product-spec 勾选）。
2. Developer 实作；Reviewer 对抗审查；必要时 Auditor 验收。
3. CI 必须绿（gofmt/vet/test/race/build；Postgres integration）。
4. 部署 staging；E2E/真机/视觉/安全检查。
5. 用户试用；不到「完整可用」不宣布完工。
6. 生产切换另立 runbook；v1 保留 rollback。
7. 任何偏离本宪章的决策，必须新增 ADR 并更新本文件。

## 9. 目前状态

- 已完成：Go/Postgres/R2、activation/login/session/media、testapp 迁移、staging relay、用户端功能 API 与管理端。
- 用户端复用 v1 Vue 3 + Vant SPA；Go 提供兼容 API、session 与媒体代理；管理端使用 Go SSR。
- 用户端已完成 staging 首轮整合（v1 SPA 四 tab、主要 legacy API、退出登录与修改密码）。
- 旧 `yixianqian-deploy` skill 不适用；v2 部署使用 `yixianqian-deploy-v2`。

## 10. 变更记录

- 2026-10-21：建立宪章；确认 Go/Postgres/R2、server2 relay、无短信首发、UI 可辨识、testapp 资料迁移、成品导向。
- 2026-10-22：确认用户端沿用 v1 Vue 3 + Vant SPA，Go 提供 API 适配与媒体代理；新增 ADR 0007。
