-- Self-service password reset. A user requests a reset; we store the
-- SHA-256 of a single-use, time-boxed token and email the raw token in a
-- link. Resetting consumes the token (sets used_at) and rehashes the
-- password. Only the hash is stored — the raw token lives only in the email.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token_hash  TEXT        PRIMARY KEY,                 -- sha256(raw token), hex
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,                             -- NULL until consumed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user ON password_reset_tokens (user_id);
