-- A matcher group can select spans AND everything below them in the
-- trace: "the trace carries abc = 123 on some span, so take that span's
-- children too, although they do not carry it". Stored per matcher and
-- read per match group - any matcher in a group that sets it makes the
-- group's conditions pick the anchor spans, and the children follow.
ALTER TABLE integration_matchers
    ADD COLUMN IF NOT EXISTS include_descendants boolean NOT NULL DEFAULT false;
