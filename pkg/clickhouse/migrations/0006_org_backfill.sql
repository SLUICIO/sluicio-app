-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Backfill the OrganizationId added in 0005 onto pre-existing rows so the
-- Phase-2 read filter (every cell-api telemetry query gains
-- "AND OrganizationId = <caller's org>") doesn't hide historical data.
-- Untagged rows ('') are attributed to the default org (the value of
-- integrations.DefaultOrgID). On a fresh install these UPDATEs match
-- nothing. ClickHouse mutations run in the background but are effectively
-- instant at pre-launch volumes.
ALTER TABLE traces  UPDATE OrganizationId = '00000000-0000-0000-0000-000000000001' WHERE OrganizationId = '';
ALTER TABLE logs    UPDATE OrganizationId = '00000000-0000-0000-0000-000000000001' WHERE OrganizationId = '';
ALTER TABLE metrics UPDATE OrganizationId = '00000000-0000-0000-0000-000000000001' WHERE OrganizationId = '';
