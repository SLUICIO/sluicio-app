// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The invariant that matters here is TOTALITY: every check lands in a
// bucket. The Errors page counts checks from the API but renders them
// from these buckets, so a check that matches no bucket is counted and
// not shown — the tile says "3" above an empty list, with no error to
// explain it.
//
// That is not hypothetical. It shipped: system-bound checks stopped
// being labelled "global", every bucket tested for a specific kind, and
// the new kind matched none of them. This file exists so the next new
// target kind fails a test instead of silently vanishing from the page.

import { describe, expect, it } from "vitest";
import { bucketChecks, bucketedCount } from "./checkBuckets";
import type { FailingCheck } from "../api/types";

function check(over: Partial<FailingCheck>): FailingCheck {
  return {
    id: Math.random().toString(36).slice(2),
    rule_id: "r",
    rule_name: "a check",
    severity: "warning",
    started_at: "2026-08-02T00:00:00Z",
    target_kind: "global",
    ...over,
  } as FailingCheck;
}

describe("bucketChecks", () => {
  it("keeps every check — nothing may vanish", () => {
    const checks = [
      check({ target_kind: "service", service_name: "svc-a" }),
      check({ target_kind: "integration", integration_id: "i1" }),
      check({ target_kind: "system", system_id: "s1" }),
      check({ target_kind: "global" }),
    ];
    expect(bucketedCount(bucketChecks(checks))).toBe(checks.length);
  });

  it("files each kind under the right bucket", () => {
    const b = bucketChecks([
      check({ target_kind: "service", service_name: "svc-a" }),
      check({ target_kind: "integration", integration_id: "i1" }),
      check({ target_kind: "system", system_id: "s1" }),
      check({ target_kind: "global" }),
    ]);
    expect(b.byService.get("svc-a")).toHaveLength(1);
    expect(b.byIntegration.get("i1")).toHaveLength(1);
    expect(b.bySystem.get("s1")).toHaveLength(1);
    expect(b.global).toHaveLength(1);
  });

  it("groups several checks on one target together", () => {
    const b = bucketChecks([
      check({ target_kind: "system", system_id: "s1" }),
      check({ target_kind: "system", system_id: "s1" }),
      check({ target_kind: "system", system_id: "s2" }),
    ]);
    expect(b.bySystem.get("s1")).toHaveLength(2);
    expect(b.bySystem.get("s2")).toHaveLength(1);
  });

  it("does not drop a check whose target id is missing", () => {
    // A malformed payload must still render. Losing the row would hide a
    // FIRING check — the page's whole reason to exist.
    const checks = [
      check({ target_kind: "system" }),
      check({ target_kind: "integration" }),
      check({ target_kind: "service" }),
    ];
    const b = bucketChecks(checks);
    expect(bucketedCount(b)).toBe(3);
    expect(b.global).toHaveLength(3);
  });

  it("does not drop a target kind it has never heard of", () => {
    // The exact shape of the shipped bug, generalised: a kind added on
    // the server before the page knows about it must stay visible.
    const c = check({ target_kind: "fleet" as FailingCheck["target_kind"] });
    const b = bucketChecks([c]);
    expect(bucketedCount(b)).toBe(1);
    expect(b.global).toContain(c);
  });
});
