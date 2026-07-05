-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Add a unique per-row id to logs so the read layer can keyset-paginate
-- deterministically. The logs table has no natural unique key, and a
-- (Timestamp, content-hash) cursor collides for byte-identical rows at
-- the same timestamp — paging would silently drop one. A UUID per row
-- is a true tiebreaker.
--
-- DEFAULT generateUUIDv4() means inserts that list columns explicitly
-- (cell-ingest's InsertLogs does) backfill it automatically with no
-- ingest change. MATERIALIZE computes and stores it for rows that
-- predate this migration, so their ids are stable across reads too
-- (a non-materialized non-deterministic default would differ per read).

ALTER TABLE logs ADD COLUMN IF NOT EXISTS LogId UUID DEFAULT generateUUIDv4();
ALTER TABLE logs MATERIALIZE COLUMN LogId;
