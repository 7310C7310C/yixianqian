# 交付里程碑与完成定义（No-POC Track）

> 本文件把「成品」拆成可验收的里程碑。完成与否以 `docs/product-spec.md`
> 勾选与 staging 实测为准，不以「程式写完」为准。
>
> **当前状态以 `docs/STATUS.md` 为准（2026-10-22）。** 本文件只定义阶段，
> 不承担实时进度；过时的“进行中”字样不得当作当前状态。

## M0 — 基础（已完成，仅为地基）

- Go/Postgres/R2、activation/login/session/media 骨架。
- staging `next.nantou.love`、server2 relay、CI、runbook。
- 安全修复：realip/allow、Argon2/图片并发、nginx 权限、disabled login。
- **不是产品完成**。

## M1 — Schema 与 testapp 迁移（已完成，以 staging 对账为准）

- `0002_domain.sql`：users/profile/media/likes/praise/messages/activities/
  signups/group/onetoone/points/invites/notifications/RBAC/jobs/audit。
- testapp 汇出工具（Bitable/Drive/SQLite）。
- import 工具（Postgres + R2）；手机唯一化 report；对账 report。
- U-0006 以 testapp 真资料在 staging 登录。
- 验收：users/likes/activities/signups/group/onetoone/messages/points 笔数与
  checksum 对上；照片可读；U-0006 activation/login 成功。

## M2 — 用户核心（卡片/喜欢/点赞/留言/通知）

- 个人资料/照片/编辑/门禁。
- 卡片池、左右滑动、照片轮播、筛选、喜欢（匿名/实名）、点赞、举报。
- 谁喜欢我/相互喜欢/我喜欢的/留言/动态通知；相互喜欢微信号。
- 验收：product-spec C/D/E/J 全勾；手机真机 E2E；原版行为 parity。

## M3 — 活动/报名/签到/分组/一对一

- 活动列表/详情/报名/取消/名单/自助签到。
- 分组候选/7 志愿/结果；一对一必聊/已聊。
- 管理台 preview/run/reset/job/result/roster。
- 验收：product-spec F/G/H 全勾；演算法 parity；锁与幂等。

## M4 — 麦穗/邀请/兑换/观察员/公开页

- 麦穗账本、邀请 7 天、兑换/退还、心愿/优先/红娘。
- 观察员角色/切换；公开页/注册 CTA/埋点。
- 验收：product-spec I/K 全勾；余额=流水求和；对账。

## M5 — 管理端/RBAC

- 审核、活动 CRUD/广播、分组/一对一、积分、举报/反馈。
- 角色/权限/管理员管理；最后一位 super_admin 保护；审计。
- 验收：product-spec L 全勾；server-side 权限测试。

## M6 — 成品验收

- 用户端复用 v1 Vue/Vant；旧/新版关键行为截图对照；真机；效能预算；安全审查。
- 管理端 Go SSR 可用；用户 SPA 不承诺无 JS（见 ADR 0007）。
- E2E 全流程；迁移对账；备份还原；回滚演练。
- 用户（你）在 staging 用真 testapp 资料完成一轮完整流程，签核。
- **只有到这里才宣布「用户功能完成」。**

## 团队与节奏

| 角色 | 职责 |
|---|---|
| Lead | 架构/优先级/验收/风险/对外窗口 |
| Analyst | 旧行为 spec、演算法 parity、资料映射 |
| Developer | Go/模板/DB/迁移实作 |
| Reviewer | 对抗审查（安全/正确性/测试） |
| Auditor | 里程碑独立验收 |
| Designer | UI 对齐/可用性/老设备 |

- Schema 先行；不同功能用独立 worktree，避免互相覆盖。
- 每个 PR：CI 绿、测试、reviewer；跨模组变更加 auditor。
- 不在生产做实验；所有验收在 staging。
- 旧 `yixianqian-deploy` skill 不适用；v2 使用 `yixianqian-deploy-v2`。

## 现行方向

用户端沿用 v1 Vue 3 + Vant SPA，Go 提供兼容 API、session 与媒体代理；管理端使用 Go SSR。
后续里程碑按 `docs/product-spec.md` 验收清单推进，UI 验收基准为 v1 原版。
架构决策见 ADR 0007。
