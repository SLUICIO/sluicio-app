-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- The per-subscription delivery ledger reads newest-first by
-- subscription; on a busy cell the 72h working set is easily tens of
-- thousands of rows, and without this index that read is a scan+sort.
CREATE INDEX event_jobs_ledger_idx ON event_jobs (subscription_id, occurred_at DESC);
