# 资料模型与 testapp 迁移对照（M1）

> 目的：把 testapp 的 Bitable/Drive/SQLite 资料，无损转换到 v2 PostgreSQL + R2。
> 本文件是 schema 与 migration 的设计依据；实作以 migration SQL 为准。

## 1. 来源盘点（testapp）

| 表 | 笔数 | 主要字段 |
|---|---:|---|
| users | 1110 | 用户ID、姓名、昵称、性别、生日、身高、学历、手机号、微信号、身份证号、工作/教会/家庭/理想型、账号状态、飞书用户ID、个人照片 |
| observers | 4 | 编号、姓名、昵称、账号状态、邀请码、飞书用户ID、个人照片 |

同一 `飞书用户ID` 同时出现在 users 与 observers 时，不建立重复用户：保留 users row，
并设 `observer_enabled=true`；`legacy_user_id` 使用 U-xxx，C-xxx 仅作观察员记录对账。
管理员身份由 `admin_user_roles` 迁移（U-0006 为 `super_admin`），不由 observers 表推导。
| likes | 132 | 发起/目标 open_id、昵称、用户ID、喜欢类型、状态、附言、归属月份 |
| activities | 6 | 活动ID、名称、描述、地点、条件、上限/当前人数、费用、食宿、状态、海报、分组/一对一/签到开关与状态、签到码、时间 |
| signups | 248 | 活动ID、报名人 open_id/昵称、状态、签到状态、报名时间 |
| group_select | 238 | 活动ID、选择人 open_id/昵称/性别、第1–7志愿、轮次 |
| group_result | 324 | 活动ID、组号、用户 open_id/昵称/性别、轮次 |
| onetoone_result | 2972 | 活动ID、排名、用户/目标 open_id/昵称/性别、轮次 |
| messages | 112 | 目标/留言人 open_id、昵称、用户ID、父留言ID、内容、状态、创建时间 |
| reports | 3 | 举报人/被举报人昵称、原因、状态、处理时间 |
| suggestions | 3 | 反馈人、用户ID、类型、内容、提交时间 |
| praise | 19 | 点赞人/被点赞人 open_id/昵称/性别、状态、对象类型/ID、归属日/周 |

**重要发现**：testapp 的 users 有 **1100 笔没有手机号**，只有 9 个唯一手机号，且有 3 组重复（`13800138000` 等）。U-0006 手机是 `13800138000`，与另一笔重复。

- staging 迁移时：为没有手机的用户分配**确定的测试手机号**（例如 `1390000xxxx`）；重复手机只保留一笔（优先 U-0006/较小 legacy ID），其余分配新测试号。
- production 迁移时：使用真实手机号；重复/缺失需人工处理报告，不能自动乱配。
- 所有测试手机分配必须写入 migration report，不能静默。

## 2. 目标 schema

### 2.1 用户与认证

`users`（扩充现有表）：
- legacy_user_id, legacy_open_id, legacy_record_id
- phone UNIQUE, name, nickname, gender, birthday, height_cm, education, baptismal_name
- church, church_location, church_group, native_place, city, industry, position, income
- house, driving, house_note_have/none, driving_note_have/none, marriage, family
- personality, self_traits jsonb, self_hobbies jsonb, self_sports jsonb, mbti jsonb
- partner_criteria, partner_traits/hobbies/sports jsonb
- wechat, id_card_ciphertext bytea, id_card_last4, id_card_valid
- account_kind (`user`/`observer`), account_status
  - `pending` 待审核, `active` 单身, `matched` 已脱单, `rejected` 审核不通过,
    `left` 已退出, `observer` 村情六处, `disabled` 管理员停用
- status (`active`/`disabled`) 只控制能否登录；`account_status` 做业务门禁
- contact_consent, referral_source, is_for_child, wechat_payment
- reviewed_by, reviewed_at, review_note, last_active_at
- created_at, updated_at

既有 `auth_credentials`、`sessions`、`activation_tokens` 沿用；credentials 增加：
- must_change_password, temp_expires_at, session_version, created_by

### 2.2 媒体

`media`（扩充）：id, user_id, kind (`profile`/`poster`/`other`), object_key, thumb_key,
content_type, byte_size, thumb_size, width, height, legacy_file_token, created_at

`profile_photos`：user_id, media_id, sort_order, is_cover；`UNIQUE(user_id, sort_order)`。
活动海报用 `activities.poster_media_id`。

### 2.3 社交

`likes`：id, legacy_record_id, initiator_id, target_id, initiator_name, target_name,
status, like_type (`匿名`/`实名`), message, month (`YYYY-MM`), created_at。
唯一性用业务规则（有效喜欢）＋ partial unique index 防重。

`praises`：id, initiator_id, target_id, status, object_type, object_id,
day (`YYYY-MM-DD`), week (`YYYY-Www`), created_at。

`messages`：id, legacy_record_id, target_user_id, author_id, author_name,
parent_id, content, status, reply_to_id, created_at。

`reports`：reporter_id, target_user_id, target_message_id, reason, status,
handled_by, handled_at, created_at, legacy_record_id。

`suggestions`：author_id, type, content, status, created_at, legacy_record_id。

### 2.4 活动

`activities`：id, legacy_record_id, activity_no (A-0001), name, description,
location, condition, max_signup, current_signup, fee, food, status,
poster_media_id, group_status, male_per_group, female_per_group, group_flag,
onetoone_status, onetoone_flag, signin_flag, signin_code,
publish_at, start_at, end_at, created_at, updated_at。

`signups`：id, legacy_record_id, activity_id, user_id, status, attendance,
created_at；`UNIQUE(activity_id, user_id)`。

`group_selections`：id, legacy_record_id, activity_id, selector_id, gender,
choices jsonb (长度 7), round int, updated_at；`UNIQUE(activity_id, selector_id)`。

`group_results`：id, activity_id, group_no, user_id, user_name, gender, round,
created_at；`UNIQUE(activity_id, round, group_no, user_id)`。

`onetoone_results`：id, activity_id, rank, user_id, user_name, gender,
target_id, target_name, round, created_at；
`UNIQUE(activity_id, round, user_id, target_id)`。

`group_jobs` / `onetoone_jobs`：或统一 `jobs` 表（kind, activity_id, round,
status, payload jsonb, created_at, started_at, finished_at, error）。

### 2.5 麦穗

`points_ledger`：id, user_id, delta, balance_after, kind, reason,
ref_type, ref_id, operator_id, created_at, idempotency_key UNIQUE。

`points_redemptions`：id, user_id, item, cost, status, params jsonb,
idempotency_key UNIQUE, ledger_id, refund_ledger_id, refund_key UNIQUE,
created_at, resolved_at, note。

`invites`：id, inviter_id, invitee_phone, invitee_id, status, gender, source,
created_at, updated_at, bound_at, confirm_due_at, confirmed_at,
reward_ledger_id, note；`UNIQUE(invitee_phone)`。

`historical_participants`：phone PK, name, source, added_at。

`app_config`：key PK, value text, updated_at, operator_id。

### 2.6 通知/管理/RBAC

`notifications`：id, user_id, kind, title, body, link, payload jsonb,
read_at, created_at。

`admin_roles`：id, code UNIQUE, name；`admin_permissions`：code PK, description；
`admin_role_permissions`：role_id, permission；`admin_user_roles`：user_id, role_id。

`audit_log`（沿用/扩充）：actor_user_id, action, target_type, target_id,
ip, user_agent, metadata jsonb, created_at。

`observer_codes`：code PK, used, used_by, used_at（或并入 invites）。

`track_events`：id, event, source, ip, created_at。

`jobs`：id, kind, payload jsonb, status, attempts, run_at, locked_at,
locked_by, last_error, created_at。

## 3. 状态映射

| Bitable | v2 account_status | 可登录 | 业务 |
|---|---|---|---|
| 待审核 | pending | 是 | 只可看审核提示 |
| 单身 | active | 是 | 完整 |
| 已脱单 | matched | 是 | 浏览/留言限制依原规则 |
| 审核不通过 | rejected | 是 | 只可看提示 |
| 已退出 | left | 是 | 只可看提示/恢复 |
| 村情六处 | observer | 是 | 只读 |
| （管理员停用） | disabled | 否 | 401 |

## 4. 迁移流程

1. **汇出**：testapp Bitable 全表 + 字段 metadata + record_id；Drive 附件 token。
2. **转换**：
   - 文字/单选/多选/日期/电话/附件依型别转换；多选存 jsonb。
   - 用户状态映射；`legacy_*` 保留。
   - 手机：唯一化策略（staging 专用）＋ report。
3. **媒体**：下载 attachment → 产生 variants/缩图 → R2 `media/...`；`profile_photos` 保留顺序/封面。
4. **积分**：SQLite `ledger/redemptions/invites/historical_participants/config` → Postgres；验证 `SUM(delta)`。
5. **汇入**：单一 transaction per table/batch；idempotent（legacy key unique）。
6. **对账**：每表来源/目标笔数、checksum、抽样、照片数/大小、积分余额。
7. **启用**：对 users 产生 activation token（或小天使临时密码）；U-0006 用 testapp 真资料。
8. **演练**：先 staging，再 production；保留 rollback snapshot。

## 5. 安全

- ID 证号只存 ciphertext + last4 + valid；密钥在 env，不进 repo。
- 媒体私有；R2 URL 不给浏览器。
- migration report 含手机/身份证时要遮罩；原始 export 加密存放。
- 不在日志/audit 印密码/token/身份证全号。

## 6. 待办

- [ ] 建 `0002_domain.sql` schema。
- [ ] migration export tool（testapp → JSON/NDJSON）。
- [ ] import tool（JSON → Postgres + R2）。
- [ ] 手机唯一化 report。
- [ ] 对账报告。
- [ ] activation token / 小天使临时密码。
- [ ] U-0006 staging 登录验收。
