-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS message_views_group_idx;
ALTER TABLE message_views DROP COLUMN IF EXISTS group_id;
