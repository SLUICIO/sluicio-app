-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Operator "handled" / acknowledge marker for alert instances. Used by
-- trace-completion delayed firings: when a message is manually resent or
-- otherwise dealt with, an operator marks the delayed trace handled so it
-- stops counting as delayed (warning/error) without resolving it as
-- "delivered". The firing stays state='firing' (so the evaluator's
-- ActiveInstanceByFingerprint check still finds it and never re-fires the
-- same delay); handled_at IS NULL is the "still counts" predicate.

ALTER TABLE alert_instances ADD COLUMN IF NOT EXISTS handled_at TIMESTAMPTZ;
