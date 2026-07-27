-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Proposals (issue #8, WS2): the safe-write primitive for agents.
--
-- An agent may READ everything its token can see, but must never mutate
-- monitoring config unilaterally. Instead it files a proposal — a stored,
-- reviewable change request with a diff and a rationale — which a human
-- with the rights to make that change approves or rejects. Approval
-- applies through the SAME engine a manual edit uses, so nothing becomes
-- un-tunable afterwards.
--
-- The whole loop is Community. Governance ON TOP of it is Enterprise
-- (approval policies, the agent audit trail, per-token budgets): gating
-- approval itself would leave CE users with proposals nobody can approve,
-- which hides the capability from exactly the people evaluating it.
--
-- `changes` stores BEFORE and AFTER per field, which is what separates
-- this from a config-transfer bundle (a desired-state export with no
-- notion of before). Before matters twice: it renders the review diff,
-- and it detects drift — if the target changed between proposal and
-- approval, applying blindly would clobber whoever edited it meanwhile.

CREATE TABLE proposals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,

    -- What the proposal wants to change. target_kind is the entity
    -- family ('alert_rule', 'maintenance_window', 'monitoring_template'…)
    -- and target_id its uuid; NULL target_id means "create a new one".
    target_kind  TEXT NOT NULL,
    target_id    UUID,
    -- Human-facing label for the target, snapshotted at proposal time so
    -- the inbox stays readable even if the target is later renamed or
    -- deleted (mirrors the audit log's actor snapshots).
    target_label TEXT NOT NULL DEFAULT '',

    -- JSON array of {field, before, after}. before is what the agent saw
    -- when it proposed; re-checked at approval to catch drift.
    changes      JSONB NOT NULL,
    -- Why the agent thinks this is right. Shown verbatim in review — a
    -- proposal without a reason is not reviewable, it's just a diff.
    rationale    TEXT NOT NULL DEFAULT '',

    -- Who filed it. Service accounts and users share the members space,
    -- so this is deliberately not a users FK: an agent proposal is filed
    -- by an SA, and the label survives the token being revoked.
    proposed_by_kind  TEXT NOT NULL DEFAULT 'service_account'
                      CHECK (proposed_by_kind IN ('service_account', 'user')),
    proposed_by_id    UUID,
    proposed_by_label TEXT NOT NULL DEFAULT '',
    -- The channel the proposal arrived through, for the "what did agents
    -- do" view. 'mcp' for the agent path, 'api' for a direct call.
    via          TEXT NOT NULL DEFAULT 'mcp',

    state        TEXT NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending', 'approved', 'rejected', 'expired', 'superseded')),
    -- Populated once a human acts. decided_by is a real user: only
    -- people approve.
    decided_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at   TIMESTAMPTZ,
    -- Reviewer's note on reject, or the apply error when approval failed.
    decision_note TEXT NOT NULL DEFAULT '',

    -- Proposals expire so an ignored queue doesn't rot into noise.
    -- Default 14 days, org-configurable; expiry moves state to 'expired'
    -- rather than deleting, because "nobody looked at this for two
    -- weeks" is itself worth seeing and deletion breaks the audit chain.
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The inbox query: an org's pending proposals, newest first.
CREATE INDEX proposals_org_state_idx ON proposals (org_id, state, created_at DESC);
-- The expiry sweep.
CREATE INDEX proposals_expiry_idx ON proposals (state, expires_at)
    WHERE state = 'pending';
-- "What is already proposed against this target" — used to supersede an
-- earlier pending proposal when an agent files a newer one.
CREATE INDEX proposals_target_idx ON proposals (org_id, target_kind, target_id)
    WHERE state = 'pending';
