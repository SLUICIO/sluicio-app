-- users.email was never uniquely indexed, so the add-member flow's
-- get-then-insert could mint duplicate accounts for one address under
-- concurrency (CreateUser even handled a violation of idx_users_email —
-- an index that didn't exist). Dedupe first: keep the earliest row per
-- address (the long-lived account; later twins are newborn artifacts of
-- the race), let FKs cascade / null out the rest. Then close the hole.
DELETE FROM users u
USING users keep
WHERE lower(u.email) = lower(keep.email)
  AND u.id <> keep.id
  AND (keep.created_at < u.created_at
       OR (keep.created_at = u.created_at AND keep.id < u.id));

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (lower(email));
