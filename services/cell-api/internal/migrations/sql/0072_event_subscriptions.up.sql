-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Outbound event subscriptions (issue #4, part 2): "tell my platform
-- when things happen in Sluicio". A subscription filters the
-- com.sluicio.<entity>.<verb> vocabulary with globs and fans matching
-- events out to an existing WEBHOOK notification channel (the channel's
-- format knob decides CloudEvents vs canonical JSON; HMAC composes).
-- group_id scopes a subscription to a team (NULL = org-wide,
-- admin-only) — teams only receive events on entities they can see.
--
-- event_jobs is the durable outbound queue — same claim/backoff shape
-- as notification_jobs, separate table because notification_jobs is
-- welded to alert instances. Events are best-effort notifications;
-- the audit log remains the tamper-evident record.

CREATE TABLE event_subscriptions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- NULL = org-wide (admin-only); else the owning team.
    group_id      UUID REFERENCES groups(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    -- JSON array of type globs: exact, trailing-*, or bare *.
    event_filters JSONB NOT NULL,
    channel_id    UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX event_subscriptions_org_idx ON event_subscriptions (org_id);

CREATE TABLE event_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES event_subscriptions(id) ON DELETE CASCADE,
    -- The CE envelope fields, computed at emit time so delivery is a
    -- pure send (no re-derivation on retry).
    event_id        TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    subject         TEXT NOT NULL DEFAULT '',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload         JSONB NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX event_jobs_due_idx ON event_jobs (state, next_attempt_at);
