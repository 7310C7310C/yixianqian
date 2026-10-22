# 当前状态（唯一进度来源）

- 更新日期：2026-10-22
- 维护者：Lead
- 下一更新点：完成真机/生产前验收，或进入新的里程碑

## 已可用（staging）

- A 方案用户端：v1 Vue 3 + Vant SPA，由 Go `/legacy/api/*` 提供数据；四 tab、卡片、喜欢/点赞/留言、活动/分组/一对一、资料、麦穗、观察员切换已接入。
- 登录/启用/注册/密码：Go SSR；`/` 由旧 SPA 入口。
- 管理端：用户审核/管理、举报/反馈、角色权限、活动列表与创建/编辑（状态、签到、分组、一对一设置）。
- 数据迁移：testapp 用户/活动/社交/照片/积分已导入 staging；U-0006 为 `super_admin` 且有观察员身份（`observer_enabled=true`）；U-0001 为 `admin`。
- CI：push run 全绿；staging 以当前 main 构建。
- 媒体：R2 私有，浏览器只走应用代理。

## 人脸检测（本地，未部署）

- 代码状态：本地 YuNet/Haar 检测、`has_face` 门禁、注册后补检测、`cmd/facebackfill` 已完成（`feat/face`）；**尚未部署到 staging**，线上仍是旧 binary。
- 部署顺序：旧 binary 运行时先 `migration 0005` + 全量回填，验证 `unchecked=0`、U-0006 true / U-0003 false，再切 binary，切后立即跑增量；后台 probe 会因重启丢失，需按 runbook 配置定时增量兜底。
- **staging 全量回填尚未执行**。在回填完成前不得切换人脸 binary，否则老照片 `face_checked=false` 会让全站被 `has_face` 门禁锁死。
- nginx：server1 next vhost 与 server2 relay 需 `client_max_body_size 96m`（9 张 12MP 注册请求可达 ~92MB）；repo 已改，线上待部署时同步，20m 会直接 413。
- 观察员账号豁免 `has_face`；**身份证 `id_valid` 不豁免**（PM 2026-10-22 裁定，不做 grandfather）。
- 未实现：腾讯云照片内容合规（宠物/卡通判定、活体）；v1 黑边裁切未移植，`tb/lr` 固定 `[0,0]`。
- 回填候选按 `media_id` 去重，共用 media 的批量账号只检测一次；staging 的 1000 张压测账号共用 media 时不会 mass probe。

## 已知缺口（不影响 A 主路径，但投产前要确认）

- 管理端已具备（对照 v1）：用户审核/管理、举报/反馈、角色权限、活动列表与创建/编辑（状态、签到、分组、一对一设置）。
- 管理端尚缺（对照 v1）：活动海报上传、活动广播、报名/签到名单/现场签到管理、分组/一对一控制台、麦穗管理、用户审核详情/照片、审计导出。
- 旧 SPA 的部分 Feishu 文案/认领入口仍可能存在但正常路径不显示；投产前需再扫一次用户文案。
- 真机 iOS/Android/微信、旧设备宽度、图片/首屏性能、备份还原演练、安全最终审查尚未完成。
- v2 未切生产；`app.nantou.love` 仍是 v1。
- v1 `ADMIN_OPEN_IDS` 不在 Bitable 导出中；其他管理员需按 `docs/data-migration.md` 人工核对。

## 状态维护规则

本文件是唯一“当前进度”来源；功能完成/未完成、已知缺口写在这里。README 只写稳定事实，`product-spec.md` 的 `[ ]` 是验收目标而非实时状态。里程碑/发布时由 Lead 更新本文件并写日期；不得由 agent 在未派工时自行修改。
