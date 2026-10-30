-- 消息中心改版：动态可清空、收到留言的已读游标、管理员全员图文消息。

-- 动态清空是「用户自己界面不再显示」，不是删除审计记录；保留行以便
-- dedupe_key 继续生效，避免用户清空后又被同一条广播补回。
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS cleared_at timestamptz;

CREATE INDEX IF NOT EXISTS notifications_user_feed_idx
    ON notifications (user_id, created_at DESC, id DESC)
    WHERE cleared_at IS NULL;

-- 收到留言的已读状态：一条留言可能同时是「卡主收到」与「被回复者收到」，
-- 所以不能把已读放在 messages 行上，必须按 (用户, 留言) 记录。
CREATE TABLE IF NOT EXISTS message_reads (
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    message_id bigint NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    read_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, message_id)
);

CREATE INDEX IF NOT EXISTS message_reads_message_idx
    ON message_reads (message_id);

-- 收件箱判定包含「回复我的留言」，按 reply_to_id 反查；
-- 0002 只有 parent_id 索引，回复目标没有索引。
CREATE INDEX IF NOT EXISTS messages_reply_to_idx
    ON messages (reply_to_id)
    WHERE reply_to_id IS NOT NULL;

-- 管理员全员消息：广播正文与可选配图。图片走 media + 本表引用，
-- /media/{id} 只对「已被广播引用」的媒体放宽到登录用户。
CREATE TABLE IF NOT EXISTS broadcasts (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_by      bigint REFERENCES users (id) ON DELETE SET NULL,
    title           text NOT NULL,
    body            text NOT NULL DEFAULT '',
    media_id        text REFERENCES media (id) ON DELETE SET NULL,
    audience        text NOT NULL DEFAULT 'all_active',
    recipient_count integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS broadcasts_media_idx ON broadcasts (media_id);
CREATE INDEX IF NOT EXISTS broadcasts_created_idx ON broadcasts (created_at DESC, id DESC);
