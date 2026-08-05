// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a failing health check is actually edited.
//
// A check's editor lives on the page of the thing it watches — a
// service, an integration or a system — and nowhere else. "Where do I
// change this?" was not answerable from a list of failing checks, so the
// Errors page grew an explicit `edit` link rather than making people
// infer it from the target link, which reads as provenance rather than
// as an action.
//
// Shared so every list of failing checks offers the same affordance to
// the same place. The integration's own Errors tab listed checks with no
// way to edit them at all, which meant the more specific page was the
// less useful one.

import type { FailingCheck } from "../api/types";

/**
 * The route where this check can be edited or disabled.
 *
 * A check bound to nothing has no owning entity, so it goes to Alerts,
 * where org-wide rules are managed.
 */
export function checkEditHref(check: FailingCheck): string {
  if (check.target_kind === "system" && check.system_id) return `/systems/${check.system_id}`;
  if (check.target_kind === "integration" && check.integration_id) {
    return `/integrations/${check.integration_id}/settings`;
  }
  if (check.target_kind === "service" && check.service_name) {
    return `/services/${encodeURIComponent(check.service_name)}?tab=health`;
  }
  return "/alerts";
}
