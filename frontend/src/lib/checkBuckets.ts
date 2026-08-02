// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Sorting failing health checks into the groups the Errors page renders.
//
// This exists because a check that matches no group DISAPPEARS. The page
// counts checks from the API but renders them from these buckets, so a
// dropped one leaves the tile saying "3" above an empty list — the user
// is told something is wrong and shown nothing, with no error to explain
// it. That is exactly what happened when system-bound checks stopped
// being labelled "global": every bucket tested for a specific kind, and
// the new kind matched none of them.
//
// So the rule is: `global` is the CATCH-ALL, not a fourth equal case.
// Anything that isn't service-, integration- or system-bound lands there
// and stays visible, whatever future target kinds arrive.

import type { FailingCheck } from "../api/types";

export interface CheckBuckets {
  /** Service name → its checks. */
  byService: Map<string, FailingCheck[]>;
  /** Integration id → its checks. */
  byIntegration: Map<string, FailingCheck[]>;
  /** System id → its checks. */
  bySystem: Map<string, FailingCheck[]>;
  /** Everything else, including anything with an unrecognised target. */
  global: FailingCheck[];
}

function push<K>(m: Map<K, FailingCheck[]>, k: K, c: FailingCheck) {
  const arr = m.get(k) ?? [];
  arr.push(c);
  m.set(k, arr);
}

/**
 * Buckets every check exactly once. The ordering mirrors the evaluators'
 * scope precedence (system → integration → service) so the page groups a
 * check under the thing it actually watches.
 */
export function bucketChecks(checks: FailingCheck[]): CheckBuckets {
  const out: CheckBuckets = {
    byService: new Map(),
    byIntegration: new Map(),
    bySystem: new Map(),
    global: [],
  };
  for (const c of checks) {
    if (c.target_kind === "system" && c.system_id) {
      push(out.bySystem, c.system_id, c);
    } else if (c.target_kind === "integration" && c.integration_id) {
      push(out.byIntegration, c.integration_id, c);
    } else if (c.target_kind === "service" && c.service_name) {
      push(out.byService, c.service_name, c);
    } else {
      // Catch-all on purpose — see the file comment. Includes a check
      // whose kind says "system" but carries no id, which would
      // otherwise vanish on a malformed payload.
      out.global.push(c);
    }
  }
  return out;
}

/** Total checks held across all buckets — used to assert nothing is lost. */
export function bucketedCount(b: CheckBuckets): number {
  let n = b.global.length;
  for (const arr of b.byService.values()) n += arr.length;
  for (const arr of b.byIntegration.values()) n += arr.length;
  for (const arr of b.bySystem.values()) n += arr.length;
  return n;
}
