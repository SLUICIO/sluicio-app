-- Duplicate suppression for CREATE proposals (issue #10).
--
-- An update proposal supersedes the pending one for the same target, so
-- a re-proposing agent replaces its earlier suggestion. A create has no
-- target to key on, so nothing stopped the same suggestion arriving on
-- every run and burying the inbox.
--
-- dedup_key is derived from what the proposal WOULD CREATE (for an
-- integration, its sorted member services, hashed) rather than from
-- anything the proposer chose. That way the same grouping is recognised
-- even when an agent names it differently on a later pass, which is
-- exactly what a language model does.
--
-- The uniqueness is PARTIAL, on pending rows only, and that is the
-- important part. Once a human decides -- approves or rejects -- the row
-- stops blocking, so a rejected grouping can be proposed again later
-- when the evidence has changed. A permanent constraint would silently
-- make one "no" final for ever, which is not what rejecting a suggestion
-- means.

ALTER TABLE proposals ADD COLUMN IF NOT EXISTS dedup_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS proposals_pending_dedup_key
  ON proposals (org_id, target_kind, dedup_key)
  WHERE state = 'pending' AND dedup_key IS NOT NULL;
