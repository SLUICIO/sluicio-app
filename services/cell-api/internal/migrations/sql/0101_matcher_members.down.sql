-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP TRIGGER IF EXISTS integration_matchers_member_default ON integration_matchers;
DROP FUNCTION IF EXISTS integration_matcher_member_default();
ALTER TABLE integration_matchers DROP COLUMN IF EXISTS member;
