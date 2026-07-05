ALTER TABLE users
    DROP COLUMN IF EXISTS login_count,
    DROP COLUMN IF EXISTS failed_login_count,
    DROP COLUMN IF EXISTS last_active_at;
