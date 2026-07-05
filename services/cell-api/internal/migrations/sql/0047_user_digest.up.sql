-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Per-user "since last visit" watermark for the activity digest. Bumped when
-- the user marks the digest seen; the digest shows what changed after it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS digest_seen_at TIMESTAMPTZ;
