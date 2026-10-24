-- 每周推荐位名单：把 v1「一人一行」的占位表改成真正的名单表。
--
-- 原表 weekly_recommendations 只有 (week, user_id, position)，主键 (week, user_id)
-- 意味着每个用户每周只能存一个推荐对象，无法表达 v1 的 7 个推荐位；全仓库
-- 0 引用、staging 0 行，属于未接线的死表。这里就地修正：
--
--   user_id           名单所有者（看到推荐位的用户）
--   position          名单内位置，0 起；必显在前、算法位在后
--   target_user_id    被推荐的人
--   pinned_by_user_id 非空表示该位是必显位，值为喜欢名单所有者的那个人
--                     （= target_user_id）。这一列只存服务端，API/管理台都不读，
--                     避免「谁暗恋谁」从管理端或接口泄漏。
--
-- 改主键而不是新建表：表名与语义（每周推荐）不变，避免再多一张死表。
ALTER TABLE weekly_recommendations
    DROP CONSTRAINT IF EXISTS weekly_recommendations_pkey;

ALTER TABLE weekly_recommendations
    ADD COLUMN IF NOT EXISTS target_user_id bigint REFERENCES users (id) ON DELETE CASCADE;

-- 表格在本次改动前无人写入（审计确认 0 行）；若环境里恰好有人手动塞过数据，
-- 缺 target_user_id 的行没有意义，直接清掉，避免 SET NOT NULL 失败。
DELETE FROM weekly_recommendations WHERE target_user_id IS NULL;

ALTER TABLE weekly_recommendations
    ALTER COLUMN target_user_id SET NOT NULL;

ALTER TABLE weekly_recommendations
    ADD PRIMARY KEY (week, user_id, position);

-- 同一个人在同一份名单里只能出现一次；重复会让「每周保留 4 个」失效。
CREATE UNIQUE INDEX IF NOT EXISTS weekly_recommendations_week_user_target_uidx
    ON weekly_recommendations (week, user_id, target_user_id);

COMMENT ON COLUMN weekly_recommendations.user_id IS '名单所有者（推荐位接收者）';
COMMENT ON COLUMN weekly_recommendations.target_user_id IS '被推荐对象';
COMMENT ON COLUMN weekly_recommendations.pinned_by_user_id IS '必显原因：喜欢名单所有者的用户，仅服务端使用，不对外展示';
