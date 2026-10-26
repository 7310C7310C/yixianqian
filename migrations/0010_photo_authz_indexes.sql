-- 批次10：跨用户照片授权（/cards/photo、/profile/photo）的索引。
-- 只增不改、不改数据；普通 CREATE INDEX（migration runner 在事务内执行），
-- 当前表规模小，生产大表如需 CONCURRENTLY 另走手工窗口。
--
-- profile_photos 现有唯一索引以 user_id 为前导；照片端点按 media_id 单点取行，
-- 没有该索引时会退化为全表扫描（staging 克隆实测 14.3ms → 1.9ms）。
CREATE INDEX IF NOT EXISTS profile_photos_media_idx
    ON profile_photos (media_id);

-- 喜欢关系按两个方向判断；正向已有 (initiator_id,target_id)，补反向前缀。
CREATE INDEX IF NOT EXISTS likes_target_initiator_idx
    ON likes (target_id, initiator_id);

-- 同活动判断按 activity_id 找报名者；补 (activity_id,user_id) 让 s_owner 侧
-- 走 index-only，不再随报名总数放大。
CREATE INDEX IF NOT EXISTS signups_activity_user_idx
    ON signups (activity_id, user_id);

-- 消息对手方判断按 author_id + target_user_id；现有 author 索引不覆盖 target。
CREATE INDEX IF NOT EXISTS messages_author_target_idx
    ON messages (author_id, target_user_id);
