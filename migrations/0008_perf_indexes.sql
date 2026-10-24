-- 性能批次1：卡片页/通知热路径索引。只增不改、不改数据。
--
-- likes：卡片状态按 initiator_id + status 过滤；现有
-- likes_initiator_month_idx (initiator_id, month) 不能高效过滤 status。
-- notifications：未读数按 user_id + read_at IS NULL 查询；现有
-- notifications_user_created_idx 不覆盖未读谓词。
--
-- 不使用 CREATE INDEX CONCURRENTLY：migration runner 在事务内执行
-- （internal/db/migrate.go），且当前两张表数据量很小，普通 CREATE INDEX
-- 的锁窗口可忽略。生产大表另走手工 CONCURRENTLY。
CREATE INDEX IF NOT EXISTS likes_initiator_status_idx
    ON likes (initiator_id, status);

CREATE INDEX IF NOT EXISTS notifications_unread_idx
    ON notifications (user_id) WHERE read_at IS NULL;

-- 候选池只取 id：部分索引让「活跃普通用户」的 id 扫描走 index-only，
-- staging 实测候选查询 8.5ms -> 3.5ms（1000+ 用户）。
CREATE INDEX IF NOT EXISTS users_card_pool_idx
    ON users (id) WHERE status = 'active' AND account_kind = 'user' AND account_status = 'active';
