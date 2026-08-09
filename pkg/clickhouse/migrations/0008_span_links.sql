-- Span links (issue #19).
--
-- 0001 said "no Events / Links arrays yet" and that absence has a
-- consequence bigger than a missing column. Links are how ASYNCHRONOUS
-- HAND-OFFS are expressed under the OpenTelemetry messaging
-- conventions: a queue, a scheduled retry, a delayed delivery. Node-RED
-- emits exactly this shape for `delay`, `trigger` and `catch` retries,
-- deliberately, as a linked second trace.
--
-- Without them a message handed off to a delayed second trace looks, to
-- Sluicio, like a message that stopped. That is the core claim of the
-- product failing quietly, not merely a diagram we cannot draw.
--
-- WHAT IS STORED, AND WHAT IS NOT
--
-- Only the reference: the linked trace and span. A link in the protocol
-- also carries an arbitrary attribute set, and that is where unbounded
-- growth actually lives. No case designed for so far reads those
-- attributes, so they are dropped rather than stored speculatively.
--
-- Capped at 32 per span by the ingest side, with the true count kept in
-- LinksTotal. The cap is asymmetric on purpose: raising it later does
-- not recover links already discarded, while lowering it is free. Pure
-- cost analysis argues for a smaller number; the asymmetry argues for a
-- larger one, and the asymmetry is the stronger argument. The median
-- span has no links at all, so 32 costs almost nothing in practice and
-- keeps a small batch whole.
--
-- LinksTotal is stored SEPARATELY from the array so a truncated span can
-- say "linked to 500 traces, showing 32" rather than quietly presenting
-- 32 as the whole story.
--
-- MIGRATION SAFETY
--
-- Nullable columns added to the largest table on any cell. ClickHouse
-- ALTER ... ADD COLUMN is metadata-only and does not rewrite parts, so
-- this does not need a maintenance window.
--
-- Existing rows get empty arrays, which must be read as UNKNOWN and not
-- as "this span had no links". Links not captured are gone: there is no
-- backfill, so a trace ingested before this migration will never show
-- its hand-offs. Anything drawing them has to say so.

ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS LinkTraceIds Array(String) CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS LinkSpanIds  Array(String) CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS LinksTotal   UInt32        CODEC(ZSTD(1));

-- Finding what links TO a given trace is the reverse lookup behind
-- "where is my message now": the hand-off is recorded on the span that
-- received it, so following a message forward means searching by the
-- trace it came from.
ALTER TABLE traces
    ADD INDEX IF NOT EXISTS idx_link_trace_ids LinkTraceIds TYPE bloom_filter(0.01) GRANULARITY 1;
