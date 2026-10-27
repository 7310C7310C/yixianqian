-- 照片上传每日配额（PM 2026-10-28，决策 44）：每账号每自然日最多 N 张。
-- 只增不改；计数按 (user_id, day) 单行原子累加，超限不增量。
-- 账号删除时级联清理；v1 prune 语义只保留今天+昨天，由 cleanupLoop 每轮删除更早的行。
CREATE TABLE IF NOT EXISTS photo_upload_daily (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day date NOT NULL,
    used integer NOT NULL CHECK (used >= 0),
    PRIMARY KEY (user_id, day)
);
