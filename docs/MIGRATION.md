# 资料迁移（testapp → v2）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-24
sources:
  - migrations/**
  - tools/migrate_testapp.py
-->

> schema 以 `migrations/` 为准，本文只记来源、工具、对账口径与已知限制。
> 当前 staging 状态与 P0 见 `docs/STATUS.md`；部署与回填见 `docs/runbooks/staging.md`。

## 0. schema 变更（2026-10-24）

- `0006_weekly_recommendations_list.sql`：把 v1 遗留的占位表 `weekly_recommendations`
  就地修正为名单表——主键 `(week, user_id, position)`、新增 `target_user_id`，
  `user_id` 表示名单所有者、`pinned_by_user_id` 只存必显原因。旧表在本次改动前
  全仓库 0 引用、staging 0 行，无数据迁移；迁移会清掉缺 `target_user_id` 的孤儿行。
- 该表属于派生数据，可由生成任务重建：`DELETE FROM weekly_recommendations WHERE week='<week>'`
  后由启动补跑或管理端「重新生成本周名单」重算，不需要回填历史周。
- 不迁移 v1 的多维表格推荐表（原实现只写本地 JSON 文件，且 web 侧从未读取）。

## 1. 来源盘点（2026-10-19 导出基线）

| 表 | 笔数 | 说明 |
|---|---:|---|
| users | 1110 | 用户主档、身份证号、账号状态、飞书 ID、个人照片 |
| observers | 4 | 观察员记录；管理员身份由 `admin_user_roles` 迁移，不由本表推导 |
| likes | 132 | 喜欢/状态/附言/归属月份 |
| activities | 6 | 活动、海报、分组/一对一/签到开关与签到码 |
| signups | 248 | 活动报名与签到状态 |
| group_select | 238 | 7 个志愿与轮次 |
| group_result | 324 | 分组结果与轮次 |
| onetoone_result | 2972 | 一对一结果、排名与轮次 |
| messages | 112 | 留言/回复/状态 |
| reports | 3 | 举报与处理状态 |
| suggestions | 3 | 意见反馈 |
| praise | 19 | 点赞对象与归属日/周 |

- 同一 `飞书用户ID` 同时出现在 users 与 observers 时，保留 users 行并设 `observer_enabled=true`；`legacy_user_id` 用 U-xxx，C-xxx 仅作观察员对账。
- 旧 `ADMIN_OPEN_IDS` 不在 Bitable 导出中，需按 v1 `local_config` 人工映射并核对 U-0006（`super_admin`）、U-0001（`admin`）。
- users 有 1100 笔没有手机号，只有 9 个唯一手机号、3 组重复（含 U-0006 的 `13800138000`）。

## 2. staging 当前（2026-10-23 复核）

- `users=1115`（`active=1111`、`observer=4`）；`activities=6`；`signups=245`（状态“已报名” 244）；`profile_photos=1028`；`points_ledger=131`。
- 来源盘点 users 1110 + observers 4 = 1114；staging 多出的 1 个来源未核对（可能是测试/seed，需查），不要直接把 1114 与 1115 相减当迁移误差。
- `phone` 1115/1115 唯一且非空，其中 1108 个为 `1390000` 测试号。

## 3. 关键限制

### 3.1 手机号

- staging 用确定的测试号（`1390000xxxx`）；重复手机只保留一笔（优先 U-0006 / 较小 legacy ID），其余分配新测试号。
- `testapp` 的 `1390000xxxx` 为压测/测试数据，不代表生产存量；生产导出需抽查手机号非空率（不写具体用户信息）。
- production 必须使用真实手机号；缺失/重复人工处理并写入报告，不能自动乱配。

### 3.2 身份证（P0）

- 迁移只写 `id_card_last4` 与 `id_card_valid`，**不写 `id_card_ciphertext`**；密文只有注册流程会写入。
- 结果：1103 个 active + 4 个 observer `id_card_valid=false`，用户端资料页没有身份证补录字段，形成门禁死锁。
- PM 2026-10-22 已裁定不 grandfather、不豁免 observer；补录/放行方式未决。处置见 `docs/STATUS.md` §4，不得用部署或文档绕过。

### 3.3 对账与媒体口径

- **对账报告未归档**：工具支持 `--report-out`，但仓库没有历史报告路径/生成物；需补逐表来源→目标笔数、checksum、抽样、照片数与大小、积分余额，并存档。
- **照片映射**：按 `<photos-dir>/<file_token>.<ext>` 递归匹配；缺失的 token 会报告并继续，不阻断文字导入。`profile_photos` 行数 ≠ `media` 数（多行可引用同一 media；压测账号会共用），回填与对账按 `media_id` 去重。
- **历史原图元数据**：2026-10-24 起迁移工具在上传前用 Pillow 重编码（JPEG q90 / PNG），按 EXIF/eXIf 方向 auto-orient 并去除 EXIF/GPS/XMP/文本；像素上限 12.5MP（4032×3024=12,192,768 可过）。重编码后 R2 字节与源文件不同，迁移报告以重编码后的 `byte_size` 为准。**已入库的存量 R2 原图不批量重写**，仍可能带元数据；需要清洗时另起一次性任务：先备份/清单（media_id、sha256），按用户维度灰度，失败可回滚。
- **积分对账**：`points_ledger` 以 `SUM(delta)` 对余额；`balance_after` 不作唯一依据；`redemptions`/`invites`/`historical_participants` 分开核对。

## 4. 工具

```bash
python3 tools/migrate_testapp.py \
  --export-dir /tmp/yxq_export \
  --database-url "$DATABASE_URL" \
  --dry-run \
  --photos-dir /tmp/yxq_photos \
  --report-out /tmp/migration-report.json
# 正式导入把 --dry-run 换成 --apply
```

- 依赖：`tools/requirements.txt`；R2 凭证从 `R2_*` 环境变量读取，不写入报告。
- `--dry-run` 只读 PostgreSQL，不上传 R2、不写行；`--apply` 才落库。工具幂等（legacy record id 为键）。
- schema 不在本文复制，以 `migrations/` 为唯一权威；迁移报告需遮罩手机号与身份证。

## 5. 相关

- 当前状态与 P0：`docs/STATUS.md`
- 部署与回填：`docs/runbooks/staging.md`
- 决策记录：`docs/DECISIONS.md`
