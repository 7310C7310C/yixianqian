-- 性能批次7：按 EXPLAIN 证据补通知/赞美/留言/报名热路径索引。
--
-- 证据（server1 临时库 yxq_b7_idx，staging schema + 20 万 likes/messages、
-- 10 万 notifications/praises、5.6 万 signups，ANALYZE 后实测）：
--   praises initiator+active      1.03ms/9 buffers -> 0.66ms/6 buffers（Index Only）
--   praises target+week+active    4.62ms/93 buffers -> 0.44ms/2 buffers（Index Only）
--   messages target+未删除        11.67ms/198 buffers -> 0.42ms/10 buffers（Index Only）
--   signups by user               22.63ms/578 buffers -> 0.27ms/7 buffers
--   notifications latest          5.01ms/99 buffers -> 2.05ms/4 buffers（Index Only）
--
-- 只增不改；普通 CREATE INDEX（migration runner 在事务内执行）。当前 staging
-- 表很小，生产大表如锁窗口不可接受，另走手工 CREATE INDEX CONCURRENTLY。
CREATE INDEX IF NOT EXISTS praises_active_initiator_idx
    ON praises (initiator_id, target_id) WHERE status = '有效';

CREATE INDEX IF NOT EXISTS praises_active_target_week_idx
    ON praises (target_id, week) WHERE status = '有效';

CREATE INDEX IF NOT EXISTS messages_target_active_idx
    ON messages (target_user_id) WHERE status <> '已删除';

CREATE INDEX IF NOT EXISTS signups_user_activity_idx
    ON signups (user_id, activity_id) WHERE status <> '已取消';

-- notifications_user_created_idx 缺 id 列，最新一条要 Incremental Sort；
-- 新索引让 ORDER BY created_at DESC, id DESC LIMIT 1 直接 Index Only Scan。
CREATE INDEX IF NOT EXISTS notifications_user_created_id_idx
    ON notifications (user_id, created_at DESC, id DESC);
