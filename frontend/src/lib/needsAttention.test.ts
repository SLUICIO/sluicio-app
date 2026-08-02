// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Regression (2026-07-21): "needs attention" showed a noisy-but-passing
// integration (many error traces, status "errors") while a genuinely
// UNHEALTHY one — the one the "N unhealthy" KPI counts — sat unnamed.
// Unhealthy must outrank error volume.

import { describe, expect, it } from "vitest";
import { pickAttentionTarget, pickNeedsAttention } from "./needsAttention";
import type { Integration, ServiceStatus, System } from "../api/types";

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

describe("pickAttentionTarget", () => {
  const sys = (over: Partial<System>): System =>
    ({ id: "s1", name: "Sluicio Web", type_key: "", description: "", members: [], member_count: 0, ...over }) as System;

  it("surfaces an unhealthy SYSTEM when no integration is in trouble", () => {
    // The reported bug: a cell whose only trouble was unhealthy systems
    // showed "All clear" beside a tile reading "3 of 3 unhealthy".
    const got = pickAttentionTarget([], [sys({ status: "unhealthy" })]);
    expect(got?.kind).toBe("system");
    expect(got?.name).toBe("Sluicio Web");
    expect(got?.href).toBe("/systems/s1");
  });

  it("still says all clear when nothing is wrong", () => {
    expect(pickAttentionTarget([], [sys({ status: "ok" })])).toBeUndefined();
    expect(pickAttentionTarget([], [sys({ status: "quiet" })])).toBeUndefined();
    expect(pickAttentionTarget([], [])).toBeUndefined();
  });

  it("prefers the more severe side", () => {
    const healthyIsh = { id: "i1", name: "Orders", status: "errors" } as unknown as Integration;
    // System is unhealthy (2) vs integration "errors" (1) — system wins.
    expect(pickAttentionTarget([healthyIsh], [sys({ status: "unhealthy" })])?.kind).toBe("system");
    // Equal severity: the integration wins, since it carries counts the
    // reader can act on.
    const bad = { id: "i2", name: "Billing", status: "unhealthy" } as unknown as Integration;
    expect(pickAttentionTarget([bad], [sys({ status: "unhealthy" })])?.kind).toBe("integration");
  });
});
