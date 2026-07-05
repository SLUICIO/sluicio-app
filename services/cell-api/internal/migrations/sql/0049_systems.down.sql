-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE services DROP COLUMN IF EXISTS system_id;
DROP TABLE IF EXISTS systems;
