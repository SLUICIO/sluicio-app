-- Deleted duplicate rows are gone for good; only the index is undone.
DROP INDEX IF EXISTS idx_users_email;
