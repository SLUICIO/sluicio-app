-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Per-entity opt-in for a PUBLIC (unauthenticated) status badge — the
-- shields-style "healthy/unhealthy" SVG at /api/v1/badges/<kind>/<id>. Off by
-- default; each integration/system/service is flipped on individually by an
-- admin. No global setting: a badge is only ever reachable for a row that has
-- explicitly set this true.

ALTER TABLE integrations ADD COLUMN IF NOT EXISTS badge_public BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE systems      ADD COLUMN IF NOT EXISTS badge_public BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE services     ADD COLUMN IF NOT EXISTS badge_public BOOLEAN NOT NULL DEFAULT false;
