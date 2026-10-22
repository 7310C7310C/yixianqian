# 变更摘要

> 本文件用简体中文汇总已交付内容；commit 历史中早期英文标题不回写，后续提交一律中文。


## 2026-10-22

### 人脸检测
- 恢复 v1 的本地人脸检测（YuNet 优先、Haar 回退）：`tools/face_probe.py` 输出 `has_face`、人脸中心与框。
- 新增 `internal/face` 与 `FACE_PROBE_CMD`/`FACE_PROBE_TIMEOUT` 配置；上传后检测，超时或失败只记日志、不阻断上传。
- `profile_photos` 新增 `face_checked`/`has_face`/`face_center_x,y`/`face_box_w,h`（migration 0005，只增列）。
- `has_face` 与卡片 `faces` 改为真实查库；没有 checked 照片时不再默认 true，观察员账号保持 v1 的 `has_face` 放行。
- 观察员身份证**不豁免**：`id_valid` 按真实审核结果返回，不做 grandfather（PM 2026-10-22 裁定：身份证该拦者拦）。
- 注册照片写入后补 probe：按上传顺序检测，首张有脸即停；15 秒预算内未完成的照片在注册响应后后台补齐，12MP/9 张照片最终也会 `face_checked=true`。probe 使用独立 `faceSem`，不占用 `mediaSem`，慢检测不再让并发上传 429；失败不阻断注册。
- 人脸检测脚本在 JPEG 解码阶段用 Pillow draft 缩到最长边 1080，12MP 单张检测耗时与内存显著下降。
- `yxq_view_role=observer` 仅影响观察员视角，不再能绕过 `has_face`；检测命令不存在/不可执行统一返回 `probe_unavailable`，不记录命令路径。
- 卡片/资料 `faces` 补 `tb`/`lr` 为 `[0,0]`；v1 的黑边裁切未移植，黑边照片按原图构图。
- 新增 `cmd/facebackfill` 批量补齐迁移照片；用 `pg_try_advisory_lock` 保证单实例，部署顺序要求先 migration + 全量回填、切换前验证 `unchecked=0`、U-0006 true/U-0003 false，再切 binary，详见 `docs/runbooks/staging.md`。
- 人脸检测 venv 固定 `numpy==1.26.4` + `opencv-python-headless==4.11.0.86`（server1 无 x86-64-v2，numpy 2.x 无法 import cv2）。
- 腾讯云照片内容合规（宠物/卡通判定、活体）未实现，本轮不做。

### 架构
- 用户端沿用 v1 Vue 3 + Vant SPA；Go 提供 legacy API、session、媒体代理；管理端使用 Go SSR。
- 新增 ADR 0007，记录用户端复用 v1 前端的决策与兼容边界。
- 用户端浏览器范围为 iOS 14.5+/Safari 14.1+、Chrome/WebView 84+、Firefox 74+。
- 移除旧前端 Feishu SDK 外链，v1 SPA 改走 `/legacy/api/*`；新增 legacy 社交、留言、活动、分组、一对一、资料、照片、麦穗兼容 API。
- v1 SPA 恢复账号退出登录与修改密码入口；登录页/入口继续由 Go 提供。

## 2026-10-21

### 基础设施与迁移
- 新建 Go/PostgreSQL/R2 项目，完成 staging `next.nantou.love`：用户 → server2（香港）→ server1（洛杉矶）→ 8093。
- 建立 R2 测试/生产桶，媒体只经应用代理，不直连 R2。
- 完成 testapp 全量迁移：1114 个账号、6 个活动、132 条喜欢、245 条报名、1024 张照片映射、129 笔麦穗流水；U-0006 使用真实数据。

### 用户端
- 登录/激活/退出、临时密码强制修改、disabled 账号拦截。
- 个人资料查看/编辑、照片上传/删除/封面、他人公开资料。
- 卡池/左右滑卡/筛选、匿名/实名喜欢、相互喜欢、点赞。
- 留言/回复/举报、站内通知、未读/已读。
- 活动列表/详情/报名/取消/我的活动/自助签到。
- 分组志愿/结果、一对一必聊结果/已聊标记。
- 麦穗余额/流水/邀请/兑换/兑换记录。
- 非飞书网页注册、待审核页、公开引流页。

### 管理端
- RBAC 角色/权限、用户审核/停用/临时密码、举报/反馈处理。

### 界面与规范
- 四 tab 品牌 shell（牵线/消息/活动/我的），简体中文全项目统一。
- 用户端界面/操作以原版为基准；管理端可自行设计。
