-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- The demand ledger (docs/telemetry-advisor-design.md §2): which
-- telemetry anyone actually CONSUMES, so the advisor can contrast it
-- with what the cell ingests. Supply needs no table — per-(service,
-- signal) volume is already answerable from traces/logs/metrics.
--
-- Deliberately aggregate-only: a daily counter per org, and nothing
-- else. No user id, no query text, no row-level "who looked at what".
-- The advisor needs "was this consumed", never "by whom" — and a
-- ledger that cannot answer "by whom" cannot be repurposed to.
--
-- SummingMergeTree collapses same-key rows on merge, so writers just
-- append counters and never read-modify-write. Key holds a metric
-- name, an attribute key, a span name (completion rules), or '' for
-- whole-signal demand; ConsumerKind disambiguates those namespaces.
--
-- Retention is a fixed 400 days: long enough to reason about "unused
-- for a year", a few MB per org per year at daily grain. NOT wired to
-- the retention enforcer — that enforcer rewrites TTLs as
-- toDate(Timestamp) over the telemetry tables, and this table has no
-- Timestamp. Its retention is a property of the feature, not an
-- operator setting.
CREATE TABLE IF NOT EXISTS telemetry_demand
(
    Day             Date,
    OrganizationId  LowCardinality(String) DEFAULT '',
    Signal          LowCardinality(String),
    ServiceName     String,
    Key             String,
    ConsumerKind    LowCardinality(String),
    Hits            UInt64
)
ENGINE = SummingMergeTree(Hits)
PARTITION BY toYYYYMM(Day)
ORDER BY (OrganizationId, Day, Signal, ServiceName, Key, ConsumerKind)
TTL Day + INTERVAL 400 DAY
SETTINGS index_granularity = 8192;
