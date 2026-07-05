-- SPDX-License-Identifier: FSL-1.1-Apache-2.0

ALTER TABLE integrations DROP COLUMN IF EXISTS badge_public;
ALTER TABLE systems      DROP COLUMN IF EXISTS badge_public;
ALTER TABLE services     DROP COLUMN IF EXISTS badge_public;
