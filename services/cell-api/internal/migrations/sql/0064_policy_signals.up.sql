-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- RBAC v2 phase 4 (docs/rbac-v2-design.md §7): per-signal visibility.
-- A policy may be narrowed to a subset of telemetry signals: NULL means
-- all signals (every existing row — behaviour unchanged); a non-empty
-- array grants only those signals for the policy's scope. Signal-
-- narrowed policies NEVER contribute to the Managed tier — managing a
-- service you can only partially observe is incoherent.
ALTER TABLE group_access_policies
    ADD COLUMN IF NOT EXISTS signals TEXT[]
    CHECK (signals IS NULL OR (array_length(signals, 1) >= 1 AND signals <@ ARRAY['traces','logs','metrics','messages']));
