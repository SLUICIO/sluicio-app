// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Regression (2026-07-21): "needs attention" showed a noisy-but-passing
// integration (many error traces, status "errors") while a genuinely
// UNHEALTHY one — the one the "N unhealthy" KPI counts — sat unnamed.
// Unhealthy must outrank error volume.

import { describe, expect, it } from "vitest";
import { pickNeedsAttention } from "./needsAttention";
import type { Integration, ServiceStatus } from "../api/types";

function integ(
  name: string,
  status: ServiceStatus,
  errorTraces: number,
  unhealthyServices: number,
): Integration {
  return {
    id: name,
    organization_id: "org",
    slug: name,
    name,
    description: "",
    created_at: "",
    updated_at: "",
    status,
    error_trace_count: errorTraces,
    unhealthy_count: unhealthyServices,
  } as Integration;
}

describe("pickNeedsAttention", () => {
  it("prefers the unhealthy integration over a noisier one that still passes", () => {
    const payment = integ("Payment", "errors", 120, 0);
    const fulfillment = integ("Fulfillment", "unhealthy", 3, 1);
    expect(pickNeedsAttention([payment, fulfillment])?.name).toBe("Fulfillment");
  });

  it("among unhealthy integrations, more failing services wins, then error volume", () => {
    const a = integ("A", "unhealthy", 50, 1);
    const b = integ("B", "unhealthy", 2, 3);
    expect(pickNeedsAttention([a, b])?.name).toBe("B");
    const c = integ("C", "unhealthy", 9, 1);
    expect(pickNeedsAttention([a, c])?.name).toBe("A");
  });

  it("falls back to error volume when nothing is unhealthy", () => {
    const quietish = integ("Quietish", "errors", 4, 0);
    const noisy = integ("Noisy", "errors", 40, 0);
    expect(pickNeedsAttention([quietish, noisy])?.name).toBe("Noisy");
  });

  it("returns undefined when there is nothing to pay attention to", () => {
    expect(pickNeedsAttention([])).toBeUndefined();
    expect(pickNeedsAttention([integ("Ok", "ok", 0, 0), integ("Quiet", "quiet", 0, 0)])).toBeUndefined();
  });

  it("still surfaces an unhealthy integration with zero error traces (e.g. quiet-check failure)", () => {
    const silent = integ("Silent-fail", "unhealthy", 0, 1);
    const ok = integ("Ok", "ok", 0, 0);
    expect(pickNeedsAttention([ok, silent])?.name).toBe("Silent-fail");
  });
});
