-- Give every account a stable display ID. Migrated records already have
-- legacy_user_id (U-0006...); web registrations previously had none.
CREATE SEQUENCE IF NOT EXISTS user_no_seq START WITH 10000;

UPDATE users
   SET legacy_user_id = 'U-' || nextval('user_no_seq')
 WHERE legacy_user_id IS NULL OR legacy_user_id = '';
