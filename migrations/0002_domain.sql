-- 0002: full domain schema for the de-Feishu product.
-- Additive only. Existing P0 columns/tables are preserved.

-- ---------------------------------------------------------------------------
-- Users: profile, business status, review, observer kind, PII.
-- status remains the login-enabled flag ('active'/'disabled').
-- account_status is the business state used by the product gates.
-- ---------------------------------------------------------------------------
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS legacy_open_id text,
    ADD COLUMN IF NOT EXISTS legacy_record_id text,
    ADD COLUMN IF NOT EXISTS nickname text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gender text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS birthday date,
    ADD COLUMN IF NOT EXISTS height_cm integer,
    ADD COLUMN IF NOT EXISTS education text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS baptismal_name text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS church text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS church_location text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS church_group text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS native_place text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS industry text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS position text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS income text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS house text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS driving text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS house_note_have text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS house_note_none text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS driving_note_have text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS driving_note_none text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS marriage text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS family text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS live_with_parents text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS personality text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS self_traits jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS self_hobbies jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS self_sports jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS mbti jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS partner_criteria text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS partner_traits jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS partner_hobbies jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS partner_sports jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS wechat text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS legacy_phone text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS id_card_ciphertext bytea,
    ADD COLUMN IF NOT EXISTS id_card_last4 text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS id_card_valid boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS account_kind text NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS account_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS contact_consent boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS referral_source text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS is_for_child text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS wechat_payment text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reviewed_by bigint REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS reviewed_at timestamptz,
    ADD COLUMN IF NOT EXISTS review_note text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS registered_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_active_at timestamptz;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_account_kind_check;
ALTER TABLE users ADD CONSTRAINT users_account_kind_check
    CHECK (account_kind IN ('user', 'observer'));
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_account_status_check;
ALTER TABLE users ADD CONSTRAINT users_account_status_check
    CHECK (account_status IN ('pending', 'active', 'matched', 'rejected', 'left', 'observer', 'disabled'));

CREATE INDEX IF NOT EXISTS users_legacy_open_id_idx ON users (legacy_open_id);
CREATE UNIQUE INDEX IF NOT EXISTS users_legacy_record_id_uidx
    ON users (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS users_status_idx ON users (account_status, status);
CREATE INDEX IF NOT EXISTS users_gender_city_idx ON users (gender, city);

-- Existing P0 seed account was created before account_status existed.
UPDATE users SET account_status = 'active'
 WHERE account_status = 'pending' AND status = 'active' AND legacy_user_id IS NOT NULL;

ALTER TABLE auth_credentials
    ADD COLUMN IF NOT EXISTS must_change_password boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS temp_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS session_version integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS created_by bigint REFERENCES users (id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- Media: add kind + legacy token mapping. Browser only sees /media/{id}.
-- ---------------------------------------------------------------------------
ALTER TABLE media
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'profile',
    ADD COLUMN IF NOT EXISTS legacy_file_token text;

ALTER TABLE media DROP CONSTRAINT IF EXISTS media_kind_check;
ALTER TABLE media ADD CONSTRAINT media_kind_check
    CHECK (kind IN ('profile', 'poster', 'other'));
CREATE UNIQUE INDEX IF NOT EXISTS media_legacy_file_token_uidx
    ON media (legacy_file_token) WHERE legacy_file_token IS NOT NULL;
CREATE INDEX IF NOT EXISTS media_kind_idx ON media (kind, created_at DESC);

CREATE TABLE IF NOT EXISTS profile_photos (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    media_id   text NOT NULL REFERENCES media (id) ON DELETE CASCADE,
    sort_order integer NOT NULL,
    is_cover   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT profile_photos_order_check CHECK (sort_order >= 0),
    CONSTRAINT profile_photos_user_media_uniq UNIQUE (user_id, media_id),
    CONSTRAINT profile_photos_user_order_uniq UNIQUE (user_id, sort_order)
);
CREATE UNIQUE INDEX IF NOT EXISTS profile_photos_one_cover_uidx
    ON profile_photos (user_id) WHERE is_cover;

-- ---------------------------------------------------------------------------
-- Social graph.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS likes (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id  text,
    initiator_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_id         bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    initiator_name    text NOT NULL DEFAULT '',
    target_name       text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT '单向喜欢',
    like_type         text NOT NULL DEFAULT '匿名',
    message           text NOT NULL DEFAULT '',
    month             text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT likes_no_self CHECK (initiator_id <> target_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS likes_legacy_record_uidx
    ON likes (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS likes_initiator_month_idx ON likes (initiator_id, month);
CREATE INDEX IF NOT EXISTS likes_target_status_idx ON likes (target_id, status);
CREATE INDEX IF NOT EXISTS likes_pair_idx ON likes (initiator_id, target_id);

CREATE TABLE IF NOT EXISTS praises (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    initiator_id     bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_id        bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status           text NOT NULL DEFAULT '有效',
    object_type      text NOT NULL DEFAULT 'profile',
    object_id        text NOT NULL DEFAULT '',
    day              text NOT NULL DEFAULT '',
    week             text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT praises_no_self CHECK (initiator_id <> target_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS praises_legacy_record_uidx
    ON praises (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS praises_initiator_day_idx ON praises (initiator_id, day);
CREATE INDEX IF NOT EXISTS praises_target_week_idx ON praises (target_id, week);
CREATE INDEX IF NOT EXISTS praises_pair_idx ON praises (initiator_id, target_id);

CREATE TABLE IF NOT EXISTS messages (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    target_user_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    author_id        bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    author_name      text NOT NULL DEFAULT '',
    parent_id        bigint REFERENCES messages (id) ON DELETE SET NULL,
    reply_to_id      bigint REFERENCES messages (id) ON DELETE SET NULL,
    content          text NOT NULL,
    status           text NOT NULL DEFAULT '正常',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS messages_legacy_record_uidx
    ON messages (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS messages_target_idx ON messages (target_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS messages_author_idx ON messages (author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS messages_parent_idx ON messages (parent_id);

CREATE TABLE IF NOT EXISTS reports (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id  text,
    reporter_id       bigint REFERENCES users (id) ON DELETE SET NULL,
    target_user_id    bigint REFERENCES users (id) ON DELETE SET NULL,
    target_message_id bigint REFERENCES messages (id) ON DELETE SET NULL,
    reporter_name     text NOT NULL DEFAULT '',
    target_name       text NOT NULL DEFAULT '',
    reason            text NOT NULL,
    status            text NOT NULL DEFAULT '待处理',
    handled_by        bigint REFERENCES users (id) ON DELETE SET NULL,
    handled_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS reports_legacy_record_uidx
    ON reports (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS reports_status_idx ON reports (status, created_at DESC);

CREATE TABLE IF NOT EXISTS suggestions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    author_id        bigint REFERENCES users (id) ON DELETE SET NULL,
    author_name      text NOT NULL DEFAULT '',
    type             text NOT NULL DEFAULT '',
    content          text NOT NULL,
    status           text NOT NULL DEFAULT '待处理',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS suggestions_legacy_record_uidx
    ON suggestions (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS suggestions_created_idx ON suggestions (created_at DESC);

-- ---------------------------------------------------------------------------
-- Activities, signups, matching.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS activities (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id  text,
    activity_no       text NOT NULL,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    location          text NOT NULL DEFAULT '',
    condition         text NOT NULL DEFAULT '',
    max_signup        integer NOT NULL DEFAULT 0,
    current_signup    integer NOT NULL DEFAULT 0,
    fee               text NOT NULL DEFAULT '',
    food              text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT '报名中',
    poster_media_id   text REFERENCES media (id) ON DELETE SET NULL,
    group_status      text NOT NULL DEFAULT '',
    male_per_group    integer NOT NULL DEFAULT 0,
    female_per_group  integer NOT NULL DEFAULT 0,
    group_enabled     boolean NOT NULL DEFAULT false,
    onetoone_status   text NOT NULL DEFAULT '',
    onetoone_enabled  boolean NOT NULL DEFAULT false,
    signin_enabled    boolean NOT NULL DEFAULT false,
    signin_code       text NOT NULL DEFAULT '',
    publish_at        timestamptz,
    start_at          timestamptz,
    end_at            timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS activities_activity_no_uidx ON activities (activity_no);
CREATE UNIQUE INDEX IF NOT EXISTS activities_legacy_record_uidx
    ON activities (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activities_status_idx ON activities (status, start_at DESC);

CREATE TABLE IF NOT EXISTS signups (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    activity_id      bigint NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    nickname         text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT '已报名',
    attendance       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT signups_unique_user UNIQUE (activity_id, user_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS signups_legacy_record_uidx
    ON signups (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS signups_activity_status_idx ON signups (activity_id, status);

CREATE TABLE IF NOT EXISTS group_selections (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    activity_id      bigint NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    selector_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    gender           text NOT NULL DEFAULT '',
    choices          jsonb NOT NULL DEFAULT '[]'::jsonb,
    round            integer NOT NULL DEFAULT 1,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT group_selections_unique_user UNIQUE (activity_id, selector_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS group_selections_legacy_record_uidx
    ON group_selections (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS group_selections_activity_round_idx
    ON group_selections (activity_id, round);

CREATE TABLE IF NOT EXISTS group_results (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    activity_id      bigint NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    group_no         integer NOT NULL,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_name        text NOT NULL DEFAULT '',
    gender           text NOT NULL DEFAULT '',
    round            integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT group_results_unique_member UNIQUE (activity_id, round, group_no, user_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS group_results_legacy_record_uidx
    ON group_results (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS group_results_activity_round_idx
    ON group_results (activity_id, round, group_no);

CREATE TABLE IF NOT EXISTS onetoone_results (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_record_id text,
    activity_id      bigint NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    rank             integer NOT NULL,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_name        text NOT NULL DEFAULT '',
    gender           text NOT NULL DEFAULT '',
    target_id        bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_name      text NOT NULL DEFAULT '',
    round            integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT onetoone_results_unique_pair UNIQUE (activity_id, round, user_id, target_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS onetoone_results_legacy_record_uidx
    ON onetoone_results (legacy_record_id) WHERE legacy_record_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS onetoone_results_activity_round_idx
    ON onetoone_results (activity_id, round, user_id, rank);

-- ---------------------------------------------------------------------------
-- Points / invites / redemptions (migrated from the old SQLite ledger).
-- Balance is always SUM(delta); balance_after is audit only.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS points_ledger (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_id        text,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    delta            integer NOT NULL,
    balance_after    integer NOT NULL,
    kind             text NOT NULL,
    reason           text NOT NULL DEFAULT '',
    ref_type         text NOT NULL DEFAULT '',
    ref_id           text NOT NULL DEFAULT '',
    operator_id      bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    idempotency_key  text,
    CONSTRAINT points_ledger_nonzero CHECK (delta <> 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS points_ledger_legacy_id_uidx
    ON points_ledger (legacy_id) WHERE legacy_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS points_ledger_idem_uidx
    ON points_ledger (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS points_ledger_user_idx ON points_ledger (user_id, id);

CREATE TABLE IF NOT EXISTS points_redemptions (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_id         text,
    user_id           bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    item              text NOT NULL,
    cost              integer NOT NULL,
    status            text NOT NULL,
    params            jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key   text NOT NULL,
    ledger_id         bigint REFERENCES points_ledger (id) ON DELETE SET NULL,
    refund_ledger_id  bigint REFERENCES points_ledger (id) ON DELETE SET NULL,
    refund_key        text,
    ref_id            text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    resolved_at       timestamptz,
    note              text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS points_redemptions_legacy_id_uidx
    ON points_redemptions (legacy_id) WHERE legacy_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS points_redemptions_idem_uidx
    ON points_redemptions (idempotency_key);
CREATE UNIQUE INDEX IF NOT EXISTS points_redemptions_refund_uidx
    ON points_redemptions (refund_key) WHERE refund_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS points_redemptions_user_idx ON points_redemptions (user_id, id);
CREATE INDEX IF NOT EXISTS points_redemptions_ref_idx ON points_redemptions (ref_id, item, status);

CREATE TABLE IF NOT EXISTS invites (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_id        text,
    inviter_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    invitee_phone    text NOT NULL,
    invitee_id       bigint REFERENCES users (id) ON DELETE SET NULL,
    status           text NOT NULL,
    gender           text NOT NULL DEFAULT '',
    source           text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    bound_at         timestamptz,
    confirm_due_at   timestamptz,
    confirmed_at     timestamptz,
    reward_ledger_id bigint REFERENCES points_ledger (id) ON DELETE SET NULL,
    note             text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS invites_phone_uidx ON invites (invitee_phone);
CREATE UNIQUE INDEX IF NOT EXISTS invites_invitee_uidx
    ON invites (invitee_id) WHERE invitee_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS invites_legacy_id_uidx
    ON invites (legacy_id) WHERE legacy_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS invites_inviter_idx ON invites (inviter_id);
CREATE INDEX IF NOT EXISTS invites_status_due_idx ON invites (status, confirm_due_at);

CREATE TABLE IF NOT EXISTS historical_participants (
    phone    text PRIMARY KEY,
    name     text NOT NULL DEFAULT '',
    source   text NOT NULL DEFAULT '',
    added_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app_config (
    key         text PRIMARY KEY,
    value       text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    operator_id bigint REFERENCES users (id) ON DELETE SET NULL
);

-- ---------------------------------------------------------------------------
-- Notifications, tracking, observer codes, weekly recommendations.
-- ---------------------------------------------------------------------------
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS link text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS payload jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS notifications_user_unread_idx
    ON notifications (user_id, read_at, created_at DESC);

CREATE TABLE IF NOT EXISTS observer_codes (
    code       text PRIMARY KEY,
    used       boolean NOT NULL DEFAULT false,
    used_by    text NOT NULL DEFAULT '',
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS track_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event      text NOT NULL,
    source     text NOT NULL DEFAULT '',
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS track_events_created_idx ON track_events (created_at DESC);

CREATE TABLE IF NOT EXISTS weekly_recommendations (
    week             text NOT NULL,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    position         integer NOT NULL,
    pinned_by_user_id bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (week, user_id)
);
CREATE INDEX IF NOT EXISTS weekly_recommendations_week_pos_idx
    ON weekly_recommendations (week, position);

-- ---------------------------------------------------------------------------
-- Admin RBAC + jobs.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS admin_roles (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_permissions (
    code        text PRIMARY KEY,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS admin_role_permissions (
    role_id    bigint NOT NULL REFERENCES admin_roles (id) ON DELETE CASCADE,
    permission text NOT NULL REFERENCES admin_permissions (code) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE IF NOT EXISTS admin_user_roles (
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id bigint NOT NULL REFERENCES admin_roles (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX IF NOT EXISTS admin_user_roles_user_idx ON admin_user_roles (user_id);

CREATE TABLE IF NOT EXISTS jobs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind        text NOT NULL,
    activity_id bigint REFERENCES activities (id) ON DELETE CASCADE,
    round       integer,
    status      text NOT NULL DEFAULT 'pending',
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts    integer NOT NULL DEFAULT 0,
    run_at      timestamptz NOT NULL DEFAULT now(),
    locked_at   timestamptz,
    locked_by   text NOT NULL DEFAULT '',
    last_error  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS jobs_pending_idx ON jobs (status, run_at);
CREATE INDEX IF NOT EXISTS jobs_activity_idx ON jobs (activity_id, kind, round);

-- Permission catalogue. Keep this list small and stable.
INSERT INTO admin_permissions (code, description) VALUES
    ('user.review', '审核用户与照片'),
    ('user.manage', '管理用户状态与资料'),
    ('activity.manage', '建立/修改活动与广播'),
    ('matching.run', '执行分组/一对一'),
    ('points.adjust', '加减麦穗与处理兑换'),
    ('content.moderate', '处理留言/举报/反馈'),
    ('settings.manage', '系统设定'),
    ('admin.manage', '管理员与角色'),
    ('export.data', '汇出资料'),
    ('audit.view', '查看审计日志')
ON CONFLICT (code) DO NOTHING;

INSERT INTO admin_roles (code, name) VALUES
    ('super_admin', '最高管理员'),
    ('admin', '管理员'),
    ('reviewer', '审核员'),
    ('activity_manager', '活动管理'),
    ('content_moderator', '内容管理'),
    ('points_manager', '麦穗管理'),
    ('auditor', '审计')
ON CONFLICT (code) DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r CROSS JOIN admin_permissions p
WHERE r.code = 'super_admin'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'user.review','user.manage','activity.manage','matching.run',
    'points.adjust','content.moderate','export.data','audit.view'
) WHERE r.code = 'admin'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'user.review','audit.view'
) WHERE r.code = 'reviewer'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'activity.manage','matching.run','export.data'
) WHERE r.code = 'activity_manager'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'content.moderate','audit.view'
) WHERE r.code = 'content_moderator'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'points.adjust','export.data','audit.view'
) WHERE r.code = 'points_manager'
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission)
SELECT r.id, p.code FROM admin_roles r JOIN admin_permissions p ON p.code IN (
    'audit.view','export.data'
) WHERE r.code = 'auditor'
ON CONFLICT DO NOTHING;
