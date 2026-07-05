-- Per-user activity statistics surfaced on Settings → Members.
--   login_count        — successful, completed logins (incremented on finishLogin).
--   failed_login_count — consecutive failed password attempts; reset to 0 on a
--                        completed login. A security signal (brute-force / typo).
--   last_active_at     — last authenticated (session) request, throttled to at
--                        most one write per user per 5 min on the auth path.
-- "MFA enabled" is derived live from user_mfa.enabled_at, so it needs no column.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS login_count        BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_login_count INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_active_at      TIMESTAMPTZ;
