-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE collector_deployments
    DROP COLUMN IF EXISTS advisor_metric,
    DROP COLUMN IF EXISTS advisor_suggestion_id;
