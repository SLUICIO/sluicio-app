-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Advisor suggestions (issue #1, docs/telemetry-advisor-design.md §3.3).
--
-- Each row is one finding the advisor stands behind: "this telemetry or
-- this alert rule costs you X and nobody consumes it, here is the exact
-- change to stop paying". Findings are RECOMPUTED from counted facts on
-- every run — this table exists for the parts that cannot be recomputed:
-- what a human already decided about them, and when.
--
-- The fingerprint is what makes that possible. It identifies a finding
-- by what it is ABOUT (class + scope + key), never by its numbers, so a
-- re-run recognises the suggestion it made yesterday and leaves a
-- dismissal in place instead of resurfacing it every night. Dismissing
-- something is a judgement about the subject, not about the volume it
-- had on the day.
--
-- The counterpart matters too: dismissal must not be permanent silence.
-- The evaluator re-opens a dismissed suggestion when the underlying
-- facts change materially (volume roughly doubles, or demand appears
-- and then stops again). `dismissed_facts` snapshots what was true when
-- the human decided, which is the only way to answer "materially" later
-- without keeping a second history table.
CREATE TABLE advisor_suggestions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,

    -- Stable identity of the FINDING, not of this row's numbers:
    -- e.g. 'T1|metric|http.server.duration' or 'F1|rule|<uuid>'.
    fingerprint  TEXT NOT NULL,
    -- Which evaluator produced it (T1…T6, F1…F5). Kept as text rather
    -- than an enum so adding a class is a code change, not a migration.
    class        TEXT NOT NULL,
    -- 'telemetry' | 'alerting' — which advisor tab it belongs to.
    advisor      TEXT NOT NULL,
    -- What it is about, for display and for filtering: the scope kind
    -- ('metric', 'service', 'attribute', 'rule') and its identifier.
    scope_kind   TEXT NOT NULL DEFAULT '',
    scope_id     TEXT NOT NULL DEFAULT '',
    -- Human-facing one-liner, rendered from a deterministic template.
    -- Stored rather than recomputed so an accepted suggestion still
    -- reads correctly years later, when the facts behind it are gone —
    -- this text is the operator's paper trail for "why did we drop
    -- that attribute".
    title        TEXT NOT NULL,
    -- What you lose by acting, in the same terms as what you save. A
    -- suggestion that only states the upside is an argument, not advice.
    loss         TEXT NOT NULL DEFAULT '',
    -- Ready-to-paste OTel collector processor config. Empty for classes
    -- where the action is a config change inside Sluicio (the alerting
    -- advisor), which is the honest signal that there is nothing to
    -- paste.
    snippet      TEXT NOT NULL DEFAULT '',
    -- Counted facts behind the finding: volumes, last-demand dates,
    -- firing counts. Rendered as the evidence block, and compared
    -- against dismissed_facts to decide whether to re-open.
    evidence     JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Ranking key: estimated bytes/day saved (telemetry) or firings in
    -- the window (alerting). One number so both tabs can sort by "how
    -- much is this worth" without the UI knowing which advisor it is.
    weight       BIGINT NOT NULL DEFAULT 0,

    -- open      → the advisor currently stands behind it
    -- accepted  → a human said yes; not proof the collector changed
    -- verified  → supply actually dropped afterwards, so it took effect
    -- dismissed → a human said no; stays quiet until the facts move
    state        TEXT NOT NULL DEFAULT 'open'
                 CHECK (state IN ('open', 'accepted', 'verified', 'dismissed')),
    -- Snapshot of `evidence` at the moment of dismissal — see above.
    dismissed_facts JSONB,
    decided_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at   TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',

    -- first_seen_at survives re-evaluation, so the UI can say "we have
    -- been telling you this for three weeks" — which is far more
    -- persuasive than any single night's numbers.
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live row per finding per org: the upsert key.
CREATE UNIQUE INDEX advisor_suggestions_fingerprint_idx
    ON advisor_suggestions (org_id, fingerprint);

-- The page query: an org's open suggestions for one advisor, most
-- valuable first.
CREATE INDEX advisor_suggestions_board_idx
    ON advisor_suggestions (org_id, advisor, state, weight DESC);

-- The digest asks "anything new since the user last looked".
CREATE INDEX advisor_suggestions_new_idx
    ON advisor_suggestions (org_id, first_seen_at DESC);
