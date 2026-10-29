# 当前状态（唯一进度来源）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-29
sources: []
-->

> 2026-10-29 登录弹框 + 注册结构化：`/login` 303 到 `/?login=1`；注册页省市区联动、行业/来源 combobox、身份证/手机即时校验 + 18+；小天使二维码 + 后台上传（migration 12）。
> 2026-10-29 注册表单细化：单字段单错误；房/车补充内容（4 个既有 DB 字段）接入注册与资料编辑；小天使二维码并入选项并加长按解锁；图片统一 JPEG/二维码 PNG，支持 WebP/GIF/BMP/TIFF。
> 本文件只写**当前**阶段、已实现/已验收、P0 与缺口。历史批次、旧快照、
> 测试输出与 commit sha 叙事看 `git log` 和 commit message；机械事实用下方命令复核。
> **不要写死运行版本/SHA**；版本以 `/healthz` 与 `deployed.sha` 为准。
> 更新时机：阶段、P0/缺口、验收状态变化；不要每次部署后补一条 STATUS 提交。

## 1. 当前快照（最后核对 2026-10-28）

| 项目 | 值 |
|---|---|
| staging 入口 | `https://next.nantou.love`（server2 HK relay → server1） |
| 运行版本 | `curl -sS https://next.nantou.love/healthz`（`version=staging-<sha>`） |
| 代码部署提交 | 以 `/healthz` 的 version 为准；`deployed.sha` 可能因 docs-only 提交更大 |
| 服务器 `src` HEAD | `git -C /opt/yixianqian-next/src rev-parse --short HEAD` |
| migration 最大版本 | `12`（`0012_site_assets`） |
| 用户 | `1115`：active 普通用户 `1111`、observer `4` |
| 资料照片 | `1028` / `has_face=1019` / `unchecked=0` |
| 自动部署 | `yxq-autodeploy.timer` enabled/active；CI + Docs 绿后部署 |
| v2 生产 | 未切换；`app.nantou.love` 仍是 v1 |

本文件不记录运行版本；docs-only 提交只推进 `deployed.sha`，不重启。
smoke 以 `/healthz` 对齐最近一次**代码**部署提交，不要拿 docs tip 当运行版本。

复核命令：

```sh
curl -sS https://next.nantou.love/healthz
cat /opt/yixianqian-next/deployed.sha
git -C /opt/yixianqian-next/src rev-parse --short HEAD
systemctl is-active yxq-autodeploy.timer
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "select max(version) from schema_migrations; select count(*) from users; select count(*) from profile_photos;"
git log --oneline -20
```

## 2. 阶段

| 阶段 | 状态 |
|---|---|
| M0 基础 | Go/Postgres/R2、activation/login/session/media、staging relay、CI 已完成 |
| M1 Schema 与迁移 | schema/导入/U-0006 登录完成；对账报告未归档，账号数差异待核对 |
| M2 用户核心 | 资料/照片/门禁、卡片、喜欢/点赞/留言、通知已实现，验收未签核 |
| M3 活动与匹配 | 活动/报名/签到、分组志愿、一对一必聊已实现，验收未签核 |
| M4 麦穗与观察员 | 麦穗账本/邀请/兑换、观察员切换、公开页已实现，验收未签核 |
| M5 管理端 | 审核、活动、匹配、麦穗、举报/反馈、审计、RBAC 已实现，验收未签核 |
| M6 成品验收 | **未完成**：真机 iOS/Android/微信、旧设备宽度、性能、备份还原、安全终审、全流程 E2E、PM 签核 |

M6 进入条件：上述验收项完成，`docs/PRODUCT.md` 有证据地签核，且 P0 已关闭；
在此之前不得宣布「用户功能完成」或切换生产。

## 3. 已实现 vs 已验收

| 范围 | 已实现 | 已验收 |
|---|---|---|
| 用户端：登录/注册/资料/照片/卡片/喜欢/留言/活动/分组/一对一/麦穗/通知/观察员 | 是 | 否（0/83） |
| 管理台：审核/活动原生 SPA/匹配/麦穗/举报/反馈/角色/审计 | 是 | 否 |
| 公开页：匿名首页、登录/注册/启用/错误页扫描防护 | 是 | 否 |
| 平台：CI/自动部署/relay/R2/人脸/腾讯合规/照片每日配额 | 是 | 视对应验收项 |

「已实现」不等于「已验收」；验收只看 `docs/PRODUCT.md` 的勾选与证据。

## 4. P0 与缺口

- **身份证门禁（原 P0）**：死锁已解除。补填入口与服务端 403 门禁已上线；
  现存 `1107` 个迁移账号 `id_card_valid=false`，可在资料页自助补填。剩余是运营补填；
  但身份证门禁仍是 P0 验收项，需 PM 验收关闭。
- **微信号同意合规**：注册当前硬编码 `contact_consent=true`，尚无显式同意步骤；
  合规口径待 PM 决定（见 `docs/DECISIONS.md` §3.1）。
- **验收签核**：`docs/PRODUCT.md` 仍 `0/83` 未勾选，未经 PM 签核；不要从 0 推断「未做」。
- **真机与性能**：iOS/Android/微信、Via/Kiwi/荣耀真机、旧设备宽度、首屏/图片性能未完成 PM 复验。
- **运维演练**：备份还原、回滚演练、安全最终审查未完成。
- **E2E**：全流程 E2E（登录→卡片→喜欢→留言→活动→分组→结果→麦穗）未建。
- **人脸照片缺口**：`104` 个 active 普通用户没有 `has_face=true` 照片，其中 `101` 个完全没有照片；
  可通过资料页上传修复，不是死锁。
- **内容合规历史照片**：`facebackfill` 默认不调用腾讯；历史 1028 张未按新规则重判，
  如需纠正必须显式 `TENCENT_FACE_BACKFILL=true` 短时回填（按次计费）。
- **裁切**：v1 黑边裁切未移植，`faces.tb/lr` 固定 `[0,0]`。
- **媒体孤儿（P3）**：非属主删除最后一个 `profile_photos` 引用时保留 media/R2 对象；当前无 GC。
- **管理员核对/账号数**：v1 `ADMIN_OPEN_IDS` 不在 Bitable 导出；迁移记录 1114、staging 1115，差 1 待核对。
- **生产切换**：v2 未切生产；`app.nantou.love` 仍是 v1。

## 5. 待 PM 决策 / 下一步

1. PM 在 Android Via/Kiwi/荣耀真机复验虚拟历史栈与可视区域修复。
2. PM 按 `docs/PRODUCT.md` 逐项验收并签核；需要证据的项先补 smoke/截图/集成测试。
3. 生产 cutover 是否启动；启动前需备份/还原演练、回滚演练与安全终审。
4. 历史照片是否按腾讯合规规则回填；涉及按次计费，需明确预算与窗口。

## 6. 维护规则

- 本文件是唯一「当前进度」来源；README 只写稳定事实，`docs/PRODUCT.md` 的 `[ ]` 是验收目标而非实时进度。
- 不得无证据写「已可用」；部署/人脸/数据事实必须用上方命令重新查询。
- 人脸、`/admin` 入口、迁移计数等易漂移事实，以最近一次实测为准；旧 runbook 与历史文档不得当现状。
- 不写死运行版本/SHA；不因每次部署补一条 STATUS 提交；版本以 `/healthz` 与 `deployed.sha` 为准。
- 更新 `last_verified` 为实际复核日期；不追加批次历史。
