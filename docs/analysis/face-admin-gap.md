# 人脸/身份证与管理后台差距分析（v1 vs A）

> 分析日期：2026-10-22
> 范围：v1 用户端/管理端现状、A（v2）staging 现状、管理后台功能与信息架构。
> 约束：只读分析，未修改任何产品代码、STATUS/INDEX/Charter。
> 结论以代码与 staging 数据为证据；无法验证的部分明确标「未验证」。

## 0. 结论摘要

1. **v1 的「人脸」不是人脸识别，也不是实名认证。** 它由四块组成：
   - YuNet/Haar **人脸检测**：只用于卡片照片的「人脸对准」和黑边裁切；
   - `has_face` **前端门禁**：至少一张照片检出足够大的人脸，否则只能进「修改资料」；
   - 腾讯云 **图像标签 + 静默活体**：只拦「宠物/卡通且无人脸」的照片，不是身份证核验；
   - 身份证 **本地格式校验**（15/18 位、校验位、地址前缀、日期/年龄），没有公安实名接口。
2. **A 版在人脸/照片相关能力上基本为空；身份证只做了「注册校验 + 加密」，门禁路径不完整：**
   - `has_face` 在三处硬编码 `true`（`legacy_api.go:579`、`:687`、`legacy_profile_helpers.go:386`）；
   - 卡片 `faces` 恒为空数组（`legacy_api.go:514`、`:680`），人脸对准退化为居中；
   - 没有腾讯云配置、依赖或调用；上传只做图片类型/尺寸校验；
   - 身份证在注册时校验并 AES 加密，但「修改资料」没有身份证字段，观察员也没有豁免，迁移用户没有修复入口。
3. **A 版最严重的不是「人脸没做」，而是身份证门禁会把人锁死：**
   - v1 在 `id_valid=false` 时会把身份证字段临时放回「修改资料」（`app.py:7808-7817`）；
   - A 的 `legacyProfileEditable` 没有身份证字段（`legacy_profile_helpers.go:59-90`）；
   - staging 实测 **1105/1115 个用户没有身份证尾号**，另有 2 个有尾号但格式不合法；4 个观察员也全部没有身份证；
   - A 的 `/legacy/api/profile` 对观察员没有豁免（v1 有豁免，`app.py:7922-7925`）。
   - 结果是：这些用户一进 SPA 就触发「身份证信息有误」全屏提示，倒计时跳「修改资料」，但改不了，形成死锁。
4. **管理后台的真实差距是「没有活动工作台」，不是单纯 UI 丑：**
   - A 有用户审核、举报、反馈、角色、活动 CRUD；
   - 缺用户审核详情/照片/身份证状态、报名与签到名单、分组/一对一控制台、麦穗、广播、海报、审计查看；
   - 分组/一对一/聊天总览的后端 API 和 DB 函数已经存在且有测试，但没有任何 SSR 页面入口；
   - staging 已有 6 个活动、244 条有效报名、132 人已签到，其中 A-0016/A-0017 已开启分组/一对一模式——运营现在无法执行匹配。
5. **P0 建议顺序（与任务书指定顺序的差异）：**
   - 先做「可用性 + 上线阻断」：管理导航/活动工作台骨架、身份证门禁死锁修复；
   - **不建议把人脸 gate 放在分组/一对一控制台之前**。人脸 gate 目前被硬编码 `true` 绕过，不会阻塞用户；活动报名后没有执行入口会直接让用户看不到结果，属于上线阻断。
   - 人脸 gate 可与管理工作台并行，但必须等 PM 对「自动检测 vs 人工审核 / 是否用腾讯付费 API」做决定。

---

## 1. 方法与证据

### 1.1 版本与文件

| 对象 | 路径 | 说明 |
|---|---|---|
| v1 前端 | `/root/tmp/yxq_old/index.html` | 8531 行，含完整管理员 SPA 页面 |
| v1 后端 | `/root/yxq-dev/web/backend/app.py` | 8619 行；该目录**没有** `tencent_face.py`、`bitable.py`、`face_detection_yunet.onnx` |
| v1 腾讯逻辑 | `/root/tmp_dl/yx_code/web/backend/tencent_face.py` | 与 `/root/projects/yx/yx-review/web/backend/tencent_face.py` 完全相同；用于还原 v1 行为 |
| v1 身份证校验 | `/root/tmp_dl/yx_code/lib/bitable_client.py:519` | 与 `yx-review` 同源 |
| A 版 | `/root/projects/yixianqian` | Go/PostgreSQL/R2，staging U-0006 |
| staging DB | server1 `yixianqian_staging` | 只读 SELECT |

**版本警示：** `/root/yxq-dev` 是部分快照（缺 `tencent_face.py` 等依赖文件），且比 `/root/tmp_dl/yx_code/web/backend/app.py`（8965 行）旧；人脸/管理相关代码段一致，但具体行号可能有少量漂移。本文引用 `/root/yxq-dev` 的行号；腾讯行为引用同源的 `tencent_face.py`。不能把本文行号当成生产 v1 的唯一事实。

### 1.2 验证方式

- 代码检索：`grep -n` 定位人脸、身份证、管理路由、模板、DB 函数；
- staging 只读 SQL：统计用户/身份证/照片/活动/报名/签到/积分/审计；
- 可行性探针：在 server1 `/tmp/pigo_probe` 用纯 Go `pigo` 对库自带人脸样本与非人脸图片做检测，确认「无 cgo、毫秒级」可行；
- 未做：真实用户登录复现、真实照片准确率评测、腾讯 API 实际计费/可用性验证。

### 1.3 staging 快照（2026-10-22）

```sql
SELECT count(*) FROM users;                                   -- 1115
SELECT count(*) FROM users WHERE account_status='pending';    -- 0
SELECT count(*) FROM users WHERE account_status='active';     -- 1111
SELECT count(*) FROM users WHERE COALESCE(id_card_last4,'')<>'' AND NOT id_card_valid; -- 2
SELECT count(*) FROM users WHERE COALESCE(id_card_last4,'')='';                       -- 1105
SELECT count(DISTINCT user_id) FROM profile_photos;           -- 1010
SELECT count(*) FROM profile_photos;                          -- 1028
SELECT count(DISTINCT user_id) FROM admin_user_roles;         -- 2
SELECT count(*) FROM activities;                              -- 6
SELECT count(*) FROM signups WHERE status='已报名';            -- 244
SELECT count(*) FROM signups WHERE attendance IN ('按时','迟到'); -- 132
SELECT count(*) FROM points_ledger;                           -- 129
SELECT count(*) FROM points_redemptions;                      -- 48
SELECT count(*) FROM invites;                                 -- 2
SELECT count(*) FROM reports;                                 -- 3
SELECT count(*) FROM suggestions;                             -- 6
SELECT count(*) FROM audit_log;                               -- 296
```

关键样本：

| legacy_user_id | 姓名 | 状态 | id_card_valid | 尾号 | 照片数 |
|---|---|---|---:|---|---:|
| U-0001 | 侯冬冬 | active | true | 2913 | 9 |
| U-0006 | 一 | active | true | 3815 | 1 |
| U-10000 | 注册验收用户 | active | false | 0029 | 3 |
| C-001~C-004 | 观察员 | observer | false | 空 | 0 |

活动：

| 活动 | 状态 | 分组 | 一对一 | 签到 | 报名 | 已签到 |
|---|---|---|---:|---:|---:|---:|
| A-0011 | 报名中 | 关 | 开 | 开 | 18 | 18 |
| A-0012 | 已结束 | 关 | 开 | 开 | 100 | 100 |
| A-0013 | 报名中 | 关 | 开 | 开 | 1 | 1 |
| A-0016 | 报名中 | 开 | 关 | 开 | 12 | 6 |
| A-0017 | 报名中 | 关 | 开 | 开 | 13 | 7 |
| A-0033 | 报名中 | 关 | 开 | 关 | 100 | 0 |

> 这些是 testapp 迁移的测试资料，不能直接外推生产比例；但代码路径与数据结构是真实的，「缺身份证 → 前端死锁」不是数据问题，是产品路径问题。

---

## 2. 人脸/身份证

### 2.1 v1 到底有哪几种功能

| # | 功能 | 触发点 | 实现 | 失败行为 | 外部/付费 | 证据 |
|---|---|---|---|---|---|---|
| 1 | 人脸检测 + 卡片照片对准 | 卡片/资料详情渲染时读 `faces`；上传后同步检测；后台批量补齐 | OpenCV YuNet（`face_detection_yunet_2023mar.onnx`，score 0.6），失败回退 Haar；最长边缩到 1080；检测最大「完整可信」脸 | 模型下载/加载失败 → Haar；都失败 → 居中显示；无脸 → 不参与对准 | 本地，无 API 费用 | `app.py:2317-2519`、`format_user_brief` `:1554`、前端 `index.html:3677-3705` |
| 2 | 黑边裁切（不是人脸裁切） | 同上，`ensure_face_center` | 检测上下左右黑边，按比例裁缓存图；人脸附近不裁；最多两轮 | 裁后 <40px 放弃；失败保留原图 | 本地 | `app.py:2521-2545`、`:2560-2636` |
| 3 | `has_face` 门禁 | `/api/home`、`/api/profile`、上传/删除响应；前端每次切页/启动 1s 后检查 | 至少一张未被腾讯拦截的照片，检出脸且脸框面积 ≥ 图像面积 0.1% | 返回 false → 前端全屏提示「照片未通过审核」，5 秒倒计时跳「修改资料」；只允许修改资料页 | 本地 | `app.py:2666-2671`、`:2718-2730`、`:3497`、`:7935`、`:8129`；前端 `index.html:6373-6424`、`:6883-6892` |
| 4 | 腾讯云照片合规（宠物/卡通） | 上传时同步；后台每轮最多 20 张补扫 | 先 `TIIA DetectLabel` 图像标签；命中动物（≥30）或明确卡通（≥80）才调 `IAI DetectLiveFaceAccurate`；只有「标签命中 + 活体判定无人脸」才拦 | 无凭据/SDK 异常/超时/鉴权失败 → **一律放行**并记日志；被拦照片写 `.tc` sidecar，卡片/资料不展示，也不计入 `has_face` | **付费 API**，需 SecretId/Key/Region | `tencent_face.py:1-141`、`app.py:2673-2730`、`:8088-8110`、`:2289-2314` |
| 5 | 身份证格式校验 | 注册提交；资料修改（仅当当前无效时） | 15/18 位；18 位校验位；地址前缀白名单；日期合法、非未来、年龄 0-120；15 位只验日期/前缀 | 注册时 v1 **不拦**格式（只要求必填）；资料读取时计算 `id_valid`；修改时非法 400 | 本地，无公安接口 | `bitable_client.py:519-567`、`app.py:7808-7864` |
| 6 | 身份证门禁 | 前端启动/切页检查 `/api/profile.id_valid`；观察员豁免 | 仅普通用户强验；无效时全屏提示，倒计时跳修改资料；修改资料里临时出现身份证字段 | 用户可自行补填/修正；观察员直接 `id_valid=true` | 本地 | `app.py:3396-3400`、`:7922-7925`；前端 `index.html:6330-6380`、`:6883-6892` |

**必须说清的三件事：**

- 这不是「人脸识别」：没有 1:1 比对、没有人证合一、没有活体开户；腾讯静默活体只是用来判断「图里有没有人脸」，辅助宠物/卡通判定。
- 「实名」在 v1 的业务语义是「实名喜欢/实名资料」，不是公安实名认证；身份证只做格式校验。
- `has_face` 与 `id_valid` 都是**前端门禁**，API 层没有强制；它们不是安全边界，绕过 SPA 直接调 API 仍可操作。

### 2.2 v1 行为细节（失败路径）

**人脸检测：**

- YuNet 模型文件不存在时会从 `opencv_zoo` GitHub 下载，缓存到后端目录；下载失败回退 Haar；两者都不可用则整体静默降级。
- 检测在 `ensure_face_center` 中做，结果写 `.face/<token>.json` sidecar；`has_face` 读取时零检测开销。
- 后台预热线程每轮取「未检测的新图优先 + 旧版本待重检」，每轮有数量上限；所以新上传是同步检测，迁移老照片是逐步补齐。
- 前端上传后如果 `has_face=false`，会轮询 `/api/profile` 最多约 20 秒，显示「照片检测中…」，避免刚上传就红字/弹窗。

**腾讯合规：**

- 先标签、后活体；标签不命中动物/卡通时直接放行，省一次活体调用。
- 失败开放（fail-open）是刻意设计，避免第三方抖动阻塞上传；这意味着腾讯挂掉时宠物/卡通照片会进来，需要人工兜底。
- 被拦照片从 `photos` 列表剔除；如果用户所有照片都被拦，会因「无照片」从公开卡片池消失（`app.py:3430` 附近 `if not brief.get("photos"): continue`）。
- 只对用户照片跑，不扫活动海报；后台补扫每轮最多 20 张。

**身份证：**

- v1 注册提交只做必填与字段白名单，不调用 `validate_id_card`；所以格式错误的人也能提交成「待审核」。
- 读取时 `get_id_valid` 计算格式；无效则前端门禁；「修改资料」临时插入身份证字段（脱敏回显，value 仍是原文），保存时强验格式并联动生日。
- 观察员在 `/api/profile` 被直接置 `id_valid=true`，不要求身份证。
- 没有 OCR、没有身份证照片、没有公安接口；「验证」仅指格式/校验位。

### 2.3 A 版现状

| 能力 | A 现状 | 证据 | 结论 |
|---|---|---|---|
| `has_face` | 三处硬编码 `true` | `legacy_api.go:579`、`:687`；`legacy_profile_helpers.go:386` | 前端人脸红字/全屏提示永远不会出现 |
| `faces` | 恒为空数组 | `legacy_api.go:514`、`:680` | 卡片照片对准退化为居中；不影响主流程 |
| 人脸检测 | 无依赖、无实现 | `go.mod` 仅 pgx/minio/x-crypto/x-image；`internal/media/process.go` 只解码/缩图 | 无人脸数据、无黑边裁切 |
| 腾讯合规 | 无配置、无调用 | `.env.example`、`internal/config/config.go` 没有 Tencent 项 | 宠物/卡通照片可上传并展示 |
| 身份证注册校验 | 有：18/15 位、校验位、日期、生日一致；AES-256-GCM 加密 | `register_validation.go:21-63`、`register_pages.go:438-449`、`register_routes.go:133` | 比 v1 注册更严；但地址前缀/未来日期/年龄 0-120 未校验（v1 有） |
| 身份证读取 | `/legacy/api/profile.id_valid` = `p.IDCardValid` | `legacy_api.go:685` | 没有观察员豁免（v1 有） |
| 身份证修改 | 可编辑字段列表没有身份证 | `legacy_profile_helpers.go:59-90` | `id_valid=false` 的用户无法自救 |
| 迁移数据 | 工具不迁移身份证密文，只存 last4 + valid | `tools/migrate_testapp.py:2661-2665` | 迁移用户即使格式有效也没有密文；不影响门禁，但影响后续补录/审计 |
| 管理员审核 | 通过时若有密文则把 `id_card_valid` 置 true | `internal/db/admin.go:324-331` | 没有校验密文内容；新注册已校验，迁移用户无密文；逻辑可接受但应复核 |

### 2.4 缺口与影响

**P0（用户可见、会锁人）：身份证门禁死锁**

链路：

1. A 的 SPA 在启动 1 秒后与每次切页时检查 `id_valid`；未知时拉 `/legacy/api/profile`；
2. `id_valid = p.IDCardValid`；staging 1105 人没有身份证尾号 → false；2 人格式非法 → false；4 个观察员也没有豁免 → false；
3. `checkIdValidForPage` 发现 false → `startIdInvalidOverlay()`，5 秒后 `openProfileEdit()`；
4. 「修改资料」由 `legacyProfileEditable` 渲染，只有昵称/身高/学历/…/手机号，没有身份证；
5. 用户填不了身份证，返回其他页又弹窗 → 死锁。

补充：

- 观察员本应豁免（v1 `get_profile` 明确置 true），A 的 `legacyProfileMap` 虽然算了 `observer` 变量，但没有用于 `id_valid`；
- A 的 API 层没有 id_valid 强制，所以「安全」价值有限；真正的问题是 UX 死锁，不是越权。

**P1（照片安全/产品一致性）：人脸 gate 与腾讯合规**

- A 没有 `has_face`，用户上传宠物/卡通/风景照不会被提示；v1 会拦「宠物/卡通且无人脸」。
- A 没有管理员照片查看，所以「人工审核兜底」也不存在；等于照片内容审核端到端缺失。
- 这两项是否上线阻断取决于 PM：product-spec C4 明确写「人脸/照片规则（依原规则，可先人工审核）」，即**可以先用人工审核替代自动检测**。前提是管理员能看见照片并标记合格/不合格。

**P2（体验）：人脸对准/黑边裁切**

- `faces=[]` 只影响卡片照片构图；不影响登录、喜欢、报名、分组。
- v1 的黑边裁切还会修改缓存图；A 若要复刻，需要处理 R2 原图/缩略图/缓存一致性，成本高于收益，建议延后。

**P1/P2（照片管线，附带发现）：**

- A `media.ProcessWithLimits` 用 `image.Decode` 后直接缩图，没有 EXIF 方向校正；原图交给浏览器时现代浏览器会按 EXIF 显示，但生成的缩略图没有 EXIF，可能方向错误（未实测，代码可证）。
- A 拒绝 HEIC（`media.ErrHEIC`），v1 支持 HEIC 转 JPEG；product-spec 把 HEIC 列为后续，可接受，但要在上线说明里写清。

### 2.5 方案比较（人脸/照片检测）

| 方案 | 做法 | 成本 | 风险/限制 | 适用 |
|---|---|---|---|---|
| A. 纯 Go `pigo` | 引入 `github.com/esimov/pigo`，内嵌 236KB cascade，在上传解码后直接检测缩略图；结果写 DB | 低；无 cgo、无外部服务、毫秒级 | 只擅长正脸；侧脸/遮挡召回未知；需要标注集验证；新增依赖需 Dev Lead/PM 同意 | **推荐作为默认候选**，先做实验 |
| B. v1 YuNet Python sidecar | 把 v1 `opencv-python-headless` + ONNX 作为独立 worker/CLI，批量检测后写 DB | 中；要维护 Python/OpenCV 运行时、R2 下载、任务队列 | 与 Go 单体部署复杂度冲突；请求路径不宜依赖 sidecar | 仅当必须与 v1 检测结果高度一致时 |
| C. 腾讯云人脸/活体 API | `DetectFace` 或 `DetectLiveFaceAccurate` + `DetectLabel`；上传时同步，结果缓存 | 高；按调用付费，需 SecretId/Key/Region | 外部依赖、隐私（人脸图上传第三方）、失败开放；价格需 PM 确认（未验证） | 若 PM 要求自动合规且接受付费/隐私 |
| D. 浏览器端检测 | FaceDetector API / face-api.js | 低服务器成本 | iOS 14.5/Safari 不支持 FaceDetector；可绕过；资产大 | 只能做上传前提示，不能当 gate |
| E. 纯人工审核 | `has_face` 先保持 true，但管理员详情页展示照片，人工标记合格/不合格 | 最低；当天可做 | 审核人力；无法自动拦宠物/卡通 | **Phase 0 兜底**，也是 product-spec 允许的路径 |

**建议：**

1. Phase 0 先做 E：管理员能看见照片、能标记「照片合格/不合格」，把「完全无人审核」补上；
2. 同时做 A 的可行性实验；若召回达标，Phase 1 用 A 作为 `has_face` 检测器；
3. 腾讯 C 只用于「宠物/卡通」内容合规，不和 `has_face` 混为一谈；是否启用由 PM 决定预算/隐私；
4. B 不作为首选，除非实验证明 pigo 召回不达标且 PM 不接受腾讯。

### 2.6 实验设计（可证伪）

**目标：** 判断纯 Go 检测器能否在「不误伤真人」的前提下替代 v1 YuNet 做 `has_face`。

- 样本：从 staging `profile_photos` 分层抽 200 张（清晰正脸 100、侧脸/遮挡/合照 50、宠物/卡通/风景 50）；只用测试资料，不接生产个资。
- 真值：两名标注者独立标「是否至少一张清晰可见真人脸」；分歧由第三人裁决；记录标注一致性。
- 变体：
  - pigo，MinSize 60/80/100，Q 阈值 0/5/10；
  - v1 YuNet（若能取到同图）作为基线；
  - 腾讯 `DetectFace`（若 PM 同意）作为上限参考。
- 指标：召回（真人被判有脸）、误杀率（真人被判无脸）、误放率（宠物/卡通被判有脸）、单张 p50/p95 耗时、1000 张回填耗时。
- 判准（建议）：
  - 真人召回 ≥ 99%（误杀 ≤ 1%）——门禁宁可放过，不可错杀；
  - 宠物/卡通误放 ≤ 5%（这部分可由腾讯/人工补）；
  - p95 < 300ms/张；1000 张回填 < 10 分钟（server1）；
  - 若达不到，停止自动 gate，转为「人工审核 + 不确定态」。
- 失败处理：三态输出 `pass / review / fail`；`review` 不锁用户，只提示/进管理员队列；只有高置信 `fail` 才触发 v1 式提示。这样避免重演身份证死锁。

**探针结果（仅证明可运行，不证明准确率）：**

```text
sample.jpg 320x400: faces=1 qmax=171.12 elapsed=3.6ms
pay-qr.jpg  469x411: faces=0 qmax=0.00  elapsed=1.9ms
```

> 结论：pigo 纯 Go、无 cgo、毫秒级可行；但样本只有库自带图，**准确率未验证**。

---

## 3. 管理后台逐页对照

### 3.1 对照表

| 能力 | v1 现状 | A 现状 | 差距/严重度 |
|---|---|---|---|
| **活动列表** | SPA `group_admin`：搜索、待执行/已完成统计、模式/状态 chip、进入控制台、现场签到 | `/admin/activities`：表格（名称/编号/状态/报名/签到/分组/一对一/编辑） | A 有，但只能编辑，不能进入工作台；缺分页（活动少，暂不痛） |
| **活动创建/编辑** | v1 SPA 没有创建/编辑；活动在 Bitable/机器人侧维护 | `/admin/activities/new`、`/{id}/edit`：名称/描述/地点/条件/上限/费用/食宿/状态/时间/分组/一对一/签到码 | A 更好；但允许同时开分组+一对一；签到码不校验 4 位；无海报；无广播 |
| **活动海报** | 活动表有「活动海报」，机器人群发用 `img_key` | DB 有 `poster_media_id`、`/activities/{id}/poster` 可串流，但后台无上传/替换字段 | 缺；P1（用户端能显示海报，运营无法换） |
| **活动广播** | 机器人 `活动通知 A-xxxx` → 预览 → `确认发送`，发给单身+观察员 | 无 | 缺；P1（product-spec J4/L3） |
| **报名名单** | 用户端报名名单弹层；管理台有 `group_roster` / 未提交名单 | 用户端 `/legacy/api/activities/{id}/signups` 有；后台无 | 缺；P1/P0（运营看不到谁报名） |
| **现场签到名单** | SPA `checkin`：性别/签到状态筛选、搜索、逐人切换、全部清除、签到开关、签到码 | 用户自助签到 API 有；后台无名单、无手动标记、无清除、无开关（开关在活动编辑里） | 缺；P0（现场运营不可用；分组 gate 依赖签到） |
| **分组控制台** | SPA `group_detail`：模式卡（分组/一对一/关闭）、活动状态、签到码、进度/名单/结果 tabs、preview/run/reset、轮次、未提交名单 | API 有：`/api/admin/group/preview`、`run`、`reset`、`result`、`latest_round`；DB 有 `PreviewGrouping/RunGrouping/ResetGrouping`；无页面 | 缺 UI；P0（A-0016 已开分组，无法执行） |
| **一对一控制台** | 同上；额外「聊天」tab（已聊/零聊天/互聊统计） | API 有：`/api/admin/onetoone/status`、`preview`、`run`、`reset`、`result`、`chat-overview`；DB 有 `ExecuteOnetoone` 等；无页面 | 缺 UI；P0（A-0017/A-0011 已开一对一） |
| **麦穗管理** | SPA `points_admin`：总览、用户、流水、兑换、邀请、配置；**只读**；加减穗在机器人指令 | DB 有 `points_ledger`/`points_redemptions`/`invites`、`GrantAssist/AdjustPoints/RedeemPoints/...`；无 admin HTTP/页面 | 缺；P1（staging 129 流水/48 兑换） |
| **举报管理** | SPA `reported_messages`：列表、原因、状态、处理人、同意/驳回 | `/admin/reports`：列表、状态筛选、备注、删除留言/驳回 | A 基本对齐，且多了备注/审计；缺被举报人资料链接（可后补） |
| **意见反馈** | 用户端有反馈；v1 管理端无独立页 | `/admin/suggestions`：列表、标记处理/忽略 | A 更好 |
| **用户审核** | 机器人指令：`【待审核】`、`【通过 U-xxx】`、`【拒绝】`、`【通知】`、`【用户统计】`；无 Web 审核页 | `/admin/users`：搜索、状态筛选、计数、通过/拒绝/停用/恢复/临时密码 | A 有入口，但**审核是盲审**：看不到资料/照片/身份证状态 |
| **用户详情/照片** | Bitable 里能看到全部字段与附件 | 无 `/admin/users/{id}`；`db.AdminUser` 只有 ID/姓名/手机/状态/角色/时间 | 缺；P0（安全/正确性） |
| **角色/权限** | `ADMIN_OPEN_IDS` 环境变量；无角色/权限/审计 | `/admin/roles`：固定角色、权限列表、指派/移除、最后一位 super_admin 保护 | A 更好；但 spec L1 要求可新增角色/权限，目前只能指派固定角色；P1 |
| **审计** | 无 | 所有管理写操作写 `audit_log`（已 296 行）；无查看/筛选/导出页面 | 缺查看；P1（spec L7） |
| **总览** | SPA 我的页有管理入口；无统一 dashboard | `/admin`：待审核/待举报/待反馈、用户计数、快速入口 | A 有；缺「即将开始活动/待执行分组」等工作流指标 |

### 3.2 入口与数据流是否合理

**入口：**

- A SPA 的「管理」入口已改为「🛠️ 管理后台」→ `window.location.href='/admin'`（`web/static/legacy/index.html:1269`、`:8424-8426`），方向正确；旧 SPA 的 `points_admin/group_admin/checkin/reported_messages` 页面仍在 bundle 里，但入口已移除，属于死代码/潜在 404（旧页面调用的 `/api/admin/...` 在 A 没有注册）。
- A admin 顶栏 6 项：总览/活动/用户/举报/反馈/角色。活动页没有二级导航；用户页没有详情路由。运营要从列表跳到「某个活动的工作台」没有入口。

**数据流：**

- 活动：`/admin/activities` 只能 `GET edit`；活动执行 API 在 `/api/admin/group/*`、`/api/admin/onetoone/*`，要求 `X-CSRF-Token` 且是 JSON/表单；SSR 页面没有调用它们。运营即使知道 API，也要手工构造请求。
- 用户：`/admin/users` 的通过/拒绝直接 POST，页面没有资料/照片；审核员无法核对「照片是否本人、身份证是否有效、资料是否完整」。v1 的审核在 Bitable 里能看到全部字段，A 反而退化了审核上下文。另注意：现有 `/profile/photo/{id}` 对 pending/rejected 用户返回 404（`profileAccountCanBrowse` 只允许 active/matched/observer，`profile_routes.go:248-253`），所以审核详情不能直接复用该路由，必须加 admin 专用照片路由或在 handler 内先判管理员。还有：如果管理员批准一个没有身份证的用户，用户仍会被 SPA 锁死；审批页必须显示 ID 状态，或提供「代验/放行」开关并审计。
- 签到：用户自助签到写 `signups.attendance`；后台没有读名单/改状态的路由。分组算法在 `RunGrouping` 里按 `activity.SigninEnabled` 过滤 `CheckedIn`；如果运营无法补录「迟到/已到」，这些人会被排除在分组池外。
- 麦穗：用户端能看余额/流水/兑换；后台没有聚合查询。`points_ledger` 有 129 行，运营无法核对「穗从哪来、花到哪去」。
- 审计：写得很全（用户/举报/活动/分组/一对一/角色），但没有读接口；出事时只能进 DB 查。

**具体 bug/风险：**

1. **活动页导航不高亮**：`render.go` 的 `canonicalNav` 白名单没有 `"activities"`，`navForPage` 也没有 `admin_activities`；所以 `/admin/activities` 和编辑页的 `aria-current` 永远不出现。这是「UI 乱」的一个可复现原因。
2. **分组/一对一可同时开启**：`parseAdminActivityForm` 分别读两个 checkbox，不互斥；`RunGrouping`/`ExecuteOnetoone` 在 DB 层会报 mode conflict，但运营在表单上可以先存成两个都开，用户端两个入口都出现，执行时才报错。v1 是显式模式卡 + 一键纠偏。
3. **签到码不校验**：`parseAdminActivityForm` 只 trim，不要求 4 位数字；用户端 `validSigninCode` 要求 4 位，管理员存 3 位/字母后用户永远签不上。
4. **用户列表无分页**：`adminListLimit=100`，`ListAdminUsers` 没有 offset；1115 个用户只能靠搜索看前 100。审核待办为 0 时问题不大，但一旦有批量待审核就会漏。
5. **审核无备注**：`handleAdminUserAction` 支持 `note`，`admin_users.tmpl` 的表单没有 note 字段；审计里只有 action，没有理由。
6. **角色页固定**：页面明说「第一版角色固定，只做指派」；spec L1 要求可新增管理员/角色/权限，目前是部分实现。
7. **旧 SPA 管理页死代码**：`points_admin/group_admin/checkin/reported_messages` 仍在 bundle，但对应 `/legacy/api/admin/*` 未注册；若有人通过旧书签/调试进入，会看到 404。建议后续裁剪或加显式跳转。

### 3.3 UI/信息架构诊断（为什么「乱」）

不是配色/皮肤问题，是**没有以运营任务为中心的信息架构**：

- 顶栏把「总览/活动/用户/举报/反馈/角色」平铺，没有分组（用户/内容/运营/系统），也没有待办优先级；
- 活动只有「列表 + 一张 20+ 字段的长表单」，没有「活动工作台」；报名、签到、分组、一对一、广播这些高频动作被拆到不存在的页面；
- 用户只有「一张表 + 一排按钮」，没有详情页；审核、停用、临时密码并排，容易误点；
- 手机端表格会变成卡片，但一行里多个按钮仍然拥挤；没有「主操作」概念；
- 总览只有数字，没有「下一步该做什么」。

**建议 IA：**

```text
/admin
├─ 总览        待审核用户 / 待处理举报 / 待处理反馈 / 即将开始活动 / 待执行匹配
├─ 用户
│  ├─ 列表     状态、搜索、分页
│  └─ 详情     资料 / 照片 / 身份证状态 / 审核记录 / 操作（通过/拒绝/停用/临时密码）
├─ 活动
│  ├─ 列表     状态、报名/签到、模式、进入工作台
│  └─ 工作台   概览 / 报名与签到 / 分组 / 一对一 / 通知
├─ 内容        举报 / 反馈
├─ 运营        麦穗 / 兑换 / 邀请
└─ 系统        角色权限 / 审计日志 / 配置
```

活动工作台按 v1 的 `进度/名单/结果` 迁移，再补「概览/通知」；这样运营不用在列表和编辑页之间来回跳。

### 3.4 目标操作流（验收用）

**活动：**

```text
创建活动（草稿/报名中）→ 上传海报 → 设置签到码/开关 → 预览广播 → 发送
→ 报名名单（搜索/性别/签到状态）→ 现场补签/清除 → 分组预览 → 执行 → 结果/轮次
→ 一键切换到一对一（互斥）→ 预览 → 执行 → 必聊名单/聊天总览
```

**用户审核：**

```text
待审核列表 → 用户详情（资料/照片/身份证状态）→ 备注 → 通过/拒绝 → 站内通知 → 审计
```

**麦穗：**

```text
总览（发放/消耗/待发放/兑换单）→ 用户明细 → 流水（按用户过滤）→ 兑换单 → 邀请
→ 需要时加穗/扣穗（幂等 + 审计）
```

---

## 4. P0/P1 清单

> 排序：用户可见 > 安全/正确性 > 上线必需。P0 = 不做就不能让真实用户进来；P1 = 上线前应完成，但可用人工/临时流程顶住。

| ID | 优先级 | 项目 | 为什么 | 现状证据 | 需 PM 决策 | 可本地实现 |
|---|---|---|---|---|---|---|
| P0-1 | P0 | **身份证门禁死锁** | 1105/1115 staging 用户 `id_valid=false`；SPA 全屏拦截且修改资料无身份证字段；观察员也未豁免；用户完全无法使用 | `legacy_profile_helpers.go:59-90`、`legacy_api.go:685`、v1 `app.py:7808-7817`、staging SQL | 是否继续对迁移用户强制 ID gate？缺失/无效 ID 是「允许先补」还是「管理员代验」？ | 是：补 ID 编辑字段 + 观察员豁免 + 校验；或 PM 批准 grandfather |
| P0-2 | P0 | **活动执行工作台（分组/一对一）** | 报名/志愿已开放，运营没有 preview/run/reset/result/聊天入口；A-0016/A-0017 已开模式；用户看不到结果 | `grouping_admin_routes.go`、`onetoone_admin.go` 有 API/DB；`admin_routes.go:30-47` 无页面；staging 6 活动/244 报名 | 无（必须做）；若要缩减，至少先做分组/一对一 run+result | 是：SSR 页面 + 表单 POST，复用现有 DB 函数 |
| P0-3 | P0 | **用户审核详情/照片/身份证状态** | 现在只能盲审通过/拒绝；看不到照片、资料、ID 有效性；照片内容审核完全缺失。现有 `/profile/photo/{id}` 对 pending 用户 404，不能直接复用 | `db/admin.go:31-44` 无字段；无 `/admin/users/{id}`；`admin_users.tmpl` 只有列表；`profile_routes.go:248-253,815-846` | 审核员是否可以看完整资料/照片？身份证只显示尾号+valid 还是可看更多？ | 是：详情页 + admin 专用照片路由 + ID 状态 + 备注 |
| P0-4 | P0 | **报名/签到名单与手动签到** | 现场运营无法确认谁到了、无法补录/清除；分组 gate 依赖 attendance，错签会把人排除 | v1 `checkin` 页面；A 只有 `SelfSignin`，无 admin roster；staging 132 已签到 | 手动签到是否允许改「未到」？清除全部是否需要二次确认？ | 是：roster 查询 + `SetAttendance/ClearAttendance` + 审计 |
| P1-1 | P1 | **人脸 gate / 照片合规** | A 完全没有人脸/腾讯检查；宠物/卡通可展示；`has_face` 永远 true。product-spec C4 允许先人工审核 | `legacy_api.go:579/687`、`legacy_profile_helpers.go:386`、无 Tencent 配置 | **必须 PM 决定**：自动检测 vs 人工审核；若自动，pigo vs 腾讯；腾讯凭证/费用/隐私 | 部分：人工审核可本地；自动检测需选型/实验 |
| P1-2 | P1 | **麦穗管理** | 用户端有余额/兑换/邀请；后台无聚合视图；129 流水/48 兑换无法核对 | `points_ledger` 等表；`points_award.go`/`points_redeem.go` 有 DB 函数；无 admin 路由 | v1 只读即可，还是首版就要加穗/扣穗/处理兑换？ | 是：先只读总览/流水/兑换/邀请；再加 adjust（幂等+审计） |
| P1-3 | P1 | **活动海报 + 广播** | 用户端能显示海报，运营无法上传；v1 有活动群发（预览→确认） | `activities.poster_media_id` 有，无 admin 上传；无 broadcast；v1 `bot/commands.py:378-545` | 广播是否只发站内通知（不发飞书）？收件人是否单身+观察员？ | 是：海报上传（kind=poster）+ 通知预览/发送 |
| P1-4 | P1 | **审计日志查看/导出** | 所有管理动作已写 `audit_log`（296 行），但无页面；排障/问责只能查 DB | `internal/db/audit.go` 只有 Insert；无 List；`audit.view`/`export.data` 权限已存在但未用 | 是否首版就要导出？保留多久？ | 是：列表/筛选/CSV；权限用 `audit.view` |
| P1-5 | P1 | **角色/权限完整化** | 目前固定角色、只可指派；spec L1 要求可新增角色/权限 | `admin_roles.tmpl` 明说固定；`admin.manage` 只用于指派 | 首版是否需要自定义角色？还是固定 7 角色够用？ | 是：新增角色/权限编辑；或 PM 批准固定角色 |
| P1-6 | P1 | **管理 UI/IA 与分页** | 顶栏无分组、活动页不高亮、用户列表 100 条上限、无活动工作台、长表单无分区 | `render.go` `canonicalNav` 缺 `activities`；`adminListLimit=100`；模板结构 | 无（可用性必修） | 是：导航分组/高亮修复/分页/工作台骨架 |
| P2-1 | P2 | **`faces` 对准/黑边裁切** | 只影响卡片构图；不影响主流程 | `legacy_api.go:514/680` 空数组 | 是否值得复刻 v1 裁切？ | 是，但优先级低 |
| P2-2 | P2 | **EXIF/HEIC** | 缩略图可能方向错误；A 不支持 HEIC；v1 支持 | `media/process.go:60-96` 无 EXIF；`ErrHEIC` | HEIC 是否首版必须？ | 是；EXIF 校正成本低，HEIC 需评估 |
| P2-3 | P2 | **旧 SPA 管理死代码** | 旧页面仍在 bundle，对应 API 未注册；误入会 404 | `web/static/legacy/index.html` 仍有 `points_admin/group_admin/checkin` | 是否允许修改复用前端？ | 是：移除或跳转 |

**P0 摘要（给 PM 的一句话）：**

> 先修「身份证门禁把人锁死」和「活动报名后没有分组/一对一执行入口」；同时补「审核能看到照片/资料」与「现场签到名单」。这四件不做，staging 的 1115 个用户里绝大多数进不去，或活动参与者看不到结果。人脸自动检测可以晚一步，但必须先让管理员能看见照片并人工标记。

---

## 5. 建议实作顺序与验收判准

### 5.1 顺序（与任务书指定的差异）

任务书建议「先 tab/可用性，再人脸 gate，再管理后台」。我同意前半，但建议把「管理后台的 P0 工作台」提前到人脸 gate 之前：

- **Phase 0（可用性 + 上线阻断）**：管理导航/工作台骨架 + 身份证门禁修复；
- **Phase 1（人脸 gate）**：按 PM 决策做人工审核或自动检测；
- **Phase 2（管理后台完整化）**：用户详情、签到、分组/一对一、麦穗、广播、海报、审计、角色。

理由：人脸 gate 目前是 `true`，不会阻塞用户；分组/一对一/签到是活动主流程的**执行端**，不做就是「用户填了志愿永远没有结果」。如果 PM 坚持人脸优先，至少把 P0-2/P0-4 与 Phase 0 并行，不能串行等待。

### 5.2 Phase 0：Tab/可用性 + 门禁（约 1-2 天，粗估）

**范围：**

- 管理顶栏分组：总览 / 用户 / 活动 / 内容 / 运营 / 系统；
- 活动列表增加「进入工作台」；新增 `/admin/activities/{id}` 骨架（概览 + 子 tab 占位）；
- 修复 `canonicalNav` 的 `activities` 高亮；活动页/编辑页有 `aria-current`；
- 用户列表加分页（或「加载更多」），不再只显示前 100；
- 身份证门禁：
  - `/legacy/api/profile` 对观察员 `id_valid=true`；
  - `legacyProfileEditable` 在 `id_valid=false` 时插入身份证字段（脱敏回显 + 强验格式 + 生日一致 + AES 加密写回 + 审计）；
  - 或按 PM 决策对迁移缺 ID 用户 grandfather（打标 + 后台可见），但必须有明确开关与审计。
- 活动表单：分组/一对一互斥；签到码 4 位数字校验；结束时间 ≥ 开始时间。

**验收：**

- 管理员在 375px 宽度下，从 `/admin` 到任一页面无横向滚动，当前 tab 有高亮；
- `/admin/activities/{id}` 返回 200，显示活动名/状态/报名数/签到数/模式，子 tab 可点；
- U-10000（`id_valid=false`）登录后不再被全屏锁死：能进「修改资料」，填写合法 18 位身份证后保存，`id_valid` 变 true，刷新后不再弹窗；观察员 C-001 不弹窗；
- 活动表单同时勾选分组+一对一不能保存；签到码 `123` 不能保存；
- `make test`/`make ci` 绿；新增导航/ID gate 测试。

### 5.3 Phase 1：人脸 gate（约 3-5 天 + PM 决策，粗估）

**范围（按 PM 选择）：**

- 人工审核路线（最快）：`has_face` 先保持 true，但管理员用户详情页展示照片，可标记「照片合格/不合格」；不合格照片不进入卡片池；审计。
- 自动路线：
  - DB 增 `media_face`（或 media 列）：`state(pass/review/fail)`、`box`、`score`、`source`、`checked_at`；
  - 上传后同步/异步检测缩略图；`legacyPhotoResponse`/`legacyProfileMap`/`legacyUserMap` 读真实结果；
  - `faces` 填 `{c,box,tb:[0,0],lr:[0,0]}`；
  - 迁移 1028 张照片回填；
  - 三态输出 + 管理员覆盖；不确定态不锁用户；
  - 腾讯路线另加：`DetectLabel` + `DetectLiveFaceAccurate`，失败开放，凭证从 env，成本日志。
- 观察员豁免；无照片/全无脸用户只能进修改资料，可上传新照片自救。

**验收：**

- 上传一张无脸图 → 响应 `has_face=false`，前端显示「照片检测中…」后红字，切页弹窗；再上传一张清晰正脸 → `has_face=true`，红字/弹窗消失；
- 上传宠物/卡通（若启用腾讯）→ 400 + v1 文案；该照片不出现在卡片/资料；
- 标注集 200 张：真人召回 ≥99%、误杀 ≤1%、p95 <300ms；1000 张回填 <10 分钟；
- 管理员可看到每张照片的 `pass/review/fail`，可覆盖并写审计；
- 没有用户因为检测器错误被永久锁死（可通过上传/管理员覆盖恢复）。

### 5.4 Phase 2：管理后台完整化（约 5-8 天，粗估）

**2A 用户审核（P0-3）：**

- `/admin/users/{id}`：姓名/昵称/手机/会员编号/状态/注册时间/资料分区/照片缩略图/身份证尾号+valid/审核记录；
- admin 专用照片路由（pending/rejected 也能看），不要复用 `/profile/photo/{id}` 的公开状态过滤；
- 通过/拒绝带备注；停用/恢复；临时密码；
- 审计 + 通知；最后一位 super_admin 保护。

**验收：** 审核员不看 DB 就能判断「资料是否完整、照片是否本人、身份证是否有效」；每个动作有审计；待审核列表可点进详情。

**2B 活动工作台（P0-2/P0-4）：**

- 概览：时间/地点/费用/海报/状态/报名数/签到数/模式；
- 报名与签到：名单（搜索/性别/签到状态）、逐人切换、全部清除、签到开关/码；
- 分组：模式互斥、每组人数、预览、执行、轮次、结果、重置、未提交名单；
- 一对一：状态、女生场次、预览、执行、结果、聊天总览、重置；
- 所有写操作幂等/防双击、审计；执行前检查 mode conflict/轮次已存在/人数不足，错误中文化。

**验收：** 从创建活动到分组结果/必聊名单，运营不离开 `/admin`；A-0016 可执行分组、A-0017 可执行一对一；重复执行不会产生重复轮次；签到开关关闭后用户端入口同步消失；手机端可操作。

**2C 运营/内容（P1-2/P1-3）：**

- 麦穗：总览、用户、流水、兑换、邀请；先只读；如 PM 批准，加/扣穗（幂等+审计）、兑换处理；
- 海报：上传 JPEG/PNG，替换/删除，用户端 `/activities/{id}/poster` 可见；
- 广播：预览收件人（单身+观察员）、站内通知发送、去重、审计；不依赖飞书。

**验收：** 129 条流水/48 兑换可核对；海报上传后用户端可见；广播预览与实际收件人一致，重复发送不重复通知。

**2D 系统（P1-4/P1-5）：**

- 审计：列表/筛选（actor/action/target/时间）/CSV 导出；`audit.view` 权限；
- 角色：至少固定角色指派完善；如 PM 要求，支持新增角色/权限；最后一位 super_admin 保护；
- 用户列表分页；旧 SPA 管理死代码清理或跳转。

**验收：** 任一管理动作能在审计页查到；非授权角色看不到审计；角色变更立即生效；分页能浏览全部 1115 用户。

### 5.5 不建议现在做

- 复刻 v1 黑边裁切/缓存图改写（P2；影响面大、收益小）；
- 为旧浏览器做第二套管理 UI（不符合项目边界）；
- 在未做标注实验前，直接把 pigo 设为硬门禁（可能大量误杀）；
- 复用 v1 腾讯凭证（需 PM 明确授权与费用/隐私评估）。

---

## 6. 风险与不确定项

| 风险 | 说明 | 缓解 |
|---|---|---|
| v1 版本漂移 | `/root/yxq-dev` 与 `/root/tmp_dl/yx_code` 行数/内容有差异；腾讯文件不在 yxq-dev | 以行为描述为准；上线前用生产 v1 再核对一次 |
| staging 数据不代表生产 | 1105/1115 缺身份证是 testapp 测试资料；生产比例未知 | 先查生产导出/迁移报告；代码层仍必须处理缺失/无效 |
| pigo 准确率未验证 | 只测了库自带样本；侧脸/遮挡/合照召回未知 | 按 2.6 做 200 张标注实验；不达标转人工/腾讯 |
| 腾讯费用/隐私 | 仓库没有价目；人脸图上传第三方需告知/同意；凭证不能复用未授权账号 | PM 决策；env 注入；成本日志；隐私说明 |
| 身份证密文缺失 | 迁移工具不迁移密文；补录需要重新加密；密钥轮换会影响旧数据 | 新增 ID 写入用当前 `SESSION_SECRET` 派生密钥；不要尝试解密旧数据；审计 |
| 管理后台工作量 | DB 函数大多已有，但页面/表单/权限/审计/移动端适配仍多 | 先 P0 工作台，P1 分阶段；每阶段可独立验收 |
| UI「乱」主观 | 没有设计稿；不同运营习惯不同 | 用 3.4 的操作流做可用性测试；至少 2 名运营按流程走一遍 |
| 前端复用约束 | 用户端 SPA 不重画；管理端可自设计 | 管理端用 SSR；不引入 SPA 框架；旧 SPA 死代码不要扩展 |

---

## 7. 附录

### A. 关键证据命令

```bash
# A 的 has_face / faces
grep -n "has_face\|faces" internal/httpserver/*.go

# A 的管理路由
grep -n "HandleFunc" internal/httpserver/admin_routes.go

# A 的分组/一对一 API（有 API 无页面）
grep -n "HandleFunc" internal/httpserver/grouping_admin_routes.go internal/httpserver/onetoone_admin.go

# v1 的人脸/腾讯/身份证
grep -n "has_face\|user_has_face\|_tokens_has_face" /root/yxq-dev/web/backend/app.py
grep -n "check_photo\|DetectLabel\|DetectLiveFace" /root/tmp_dl/yx_code/web/backend/tencent_face.py
grep -n "validate_id_card" /root/tmp_dl/yx_code/lib/bitable_client.py

# staging 统计（只读）
sudo -u postgres psql -d yixianqian_staging -Atc "SELECT ..."
```

### B. 文件/行号索引

**A：**

- `internal/httpserver/legacy_api.go:514,579,680,685,687` — `faces` 空、`has_face` true、`id_valid` 无观察员豁免；
- `internal/httpserver/legacy_profile_helpers.go:59-90,386` — 可编辑字段无身份证、上传响应 `has_face=true`；
- `internal/httpserver/register_validation.go:21-63` — 身份证格式校验；
- `internal/httpserver/register_pages.go:438-449` — 注册身份证与生日一致；
- `internal/db/admin.go:31-44,308-337` — AdminUser 字段、审核通过逻辑；
- `internal/httpserver/admin_routes.go:30-64` — 现有 admin 页面/API；
- `internal/httpserver/admin_activities.go:120-170` — 活动表单解析（无互斥/签到码校验）；
- `internal/db/admin_activities.go:15-45` — AdminActivityInput（无海报/广播）；
- `internal/httpserver/render.go:35-58` — `canonicalNav` 缺 `activities`；
- `internal/db/grouping_admin.go:216,301,424` — 分组 preview/run/reset；
- `internal/db/onetoone.go:178,286,557,628` — 一对一参与者/执行/重置/轮次；
- `internal/media/process.go:60-96` — 缩图无 EXIF 校正；
- `internal/httpserver/profile_routes.go:248-253,815-846` — 公开照片只允许 active/matched/observer，pending 审核照片需 admin 路由；
- `web/templates/pages/admin_*.tmpl` — 现有管理页面；
- `web/static/legacy/index.html:1269,8424-8426` — 管理入口跳 `/admin`；
- `go.mod` — 无人脸/ONNX/Tencent 依赖；
- `.env.example`、`internal/config/config.go` — 无 Tencent 配置。

**v1：**

- `/root/tmp/yxq_old/index.html:1266-1272` — 管理入口；
- `:1283-1412` — 麦穗管理（只读）；
- `:1413-1873` — 活动管理/控制台；
- `:1875-1970` — 现场签到；
- `:2172-2215` — 举报管理；
- `:3782-3788` — 管理员指令帮助（审核/分组/一对一）；
- `/root/yxq-dev/web/backend/app.py:2666-2730` — has_face；
- `:8088-8129` — 上传腾讯检查 + 人脸检测 + has_face；
- `:7808-7864` — 身份证临时暴露/修改校验；
- `:7922-7935` — profile id_valid/has_face；
- `:3962-4180` — 麦穗 admin API；
- `:6398-7500` — 分组 admin API；
- `:7502-7680` — 签到 admin API；
- `/root/tmp_dl/yx_code/web/backend/tencent_face.py:1-141` — 腾讯判定；
- `/root/tmp_dl/yx_code/lib/bitable_client.py:519-567` — 身份证格式校验；
- `/root/tmp_dl/yx_code/bot/commands.py:199-310` — 机器人审核；
- `:378-545` — 活动群发；
- `:735-762` — 管理员指令清单。

### C. v1 腾讯 API 细节

| 项 | 值 |
|---|---|
| 图像标签 | `tiia.tencentcloudapi.com`，`DetectLabel`，超时 8s |
| 静默活体 | `iai.tencentcloudapi.com`，`DetectLiveFaceAccurate` |
| 动物阈值 | 30；卡通阈值 80 |
| 拦截条件 | 动物/卡通命中 **且** 活体判定 `NoFaceInPhoto` |
| 失败策略 | 无凭据/异常 → 放行（`blocked=false, skipped=...`） |
| 缓存 | `.tc/<token>.json`；被拦照片不对外展示、不计入 `has_face` |
| 付费 | 是（按调用计费；仓库无价目，需 PM/财务确认，未验证） |

---

> 本文只新增 `docs/analysis/face-admin-gap.md`，未修改 STATUS/INDEX/Charter 或产品代码。
