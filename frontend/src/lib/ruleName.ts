// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The default name for a metric alert / health check.
//
// It used to be just `<metric> alert`, so every check on one metric was
// born with the same name. Three checks on httpcheck.status differing
// only by url arrived as three rows called "httpcheck.status alert" —
// identical in every list that shows a rule by name, and impossible to
// tell apart without opening each one.
//
// Rendering the filters alongside the name (which the Errors page and
// the metric drawer now do) makes them distinguishable. Seeding the name
// stops them being identical in the first place — including in the
// places that only ever show a name: notification subjects, the alert
// list, audit entries.
//
// The name stays fully editable; this only decides what it starts as.

import type { RuleAttrFilter } from "../api/types";

/** How many filters to name before falling back to a count. */
const NAMED = 2;

function one(a: RuleAttrFilter): string {
  // "eq" is the overwhelmingly common case and reads better as "=".
  return a.op === "eq" ? `${a.key}=${a.value}` : `${a.key} ${a.op} ${a.value}`;
}

/**
 * Seeds a rule name from the metric and the attribute filters scoping it.
 *
 * No filters keeps the original `<metric> alert`, because there is
 * nothing to disambiguate and the shorter name reads better.
 *
 * Values are NOT truncated. A long URL makes a long name, but shortening
 * it risks two checks colliding again on their shared prefix — which is
 * the whole problem this exists to avoid.
 */
export function defaultRuleName(metricName: string, attrs?: RuleAttrFilter[]): string {
  const list = (attrs ?? []).filter((a) => a.key && a.value);
  if (list.length === 0) return `${metricName} alert`;
  const shown = list.slice(0, NAMED).map(one).join(", ");
  const rest = list.length - NAMED;
  return rest > 0 ? `${metricName} · ${shown} +${rest}` : `${metricName} · ${shown}`;
}
