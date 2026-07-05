-- Per-user TOTP multi-factor auth. The TOTP secret is stored encrypted
-- (AES-GCM, key from SLUICIO_MFA_KEY or an auto-generated cell setting).
-- Backup codes are stored as SHA-256 hashes (single-use, removed on use).
-- A row with enabled_at NULL is mid-enrollment (secret issued, not yet
-- confirmed with a valid code); enabled_at set means MFA is active.
CREATE TABLE IF NOT EXISTS user_mfa (
    user_id            UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc         BYTEA       NOT NULL,                 -- AES-GCM(nonce||ciphertext) of the base32 secret
    enabled_at         TIMESTAMPTZ,                          -- NULL = enrollment pending
    backup_code_hashes JSONB       NOT NULL DEFAULT '[]'::jsonb, -- array of sha256 hex, single-use
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
