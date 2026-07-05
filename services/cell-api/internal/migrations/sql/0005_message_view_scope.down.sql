-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS message_views_scope_integration_idx;
ALTER TABLE message_views
    DROP COLUMN IF EXISTS scope_integration_id,
    DROP COLUMN IF EXISTS scope_service_id;
