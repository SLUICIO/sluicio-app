// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The copy is the feature. These tests exist mostly to stop it drifting
// into claims the data cannot support.

import { describe, expect, it } from "vitest";
import { traceNodeVisual, traceStatesByService, traceSummaryLine } from "./traceNodeVisual";
import type { TraceNodeState } from "../api/types";

const node = (o: Partial<TraceNodeState> & { state: TraceNodeState["state"] }): TraceNodeState => ({
  service_name: "svc",
  span_count: 1,
  error_count: 0,
  ...o,
});

describe("traceNodeVisual", () => {
  it("returns nothing when no message is projected", () => {
    // The aggregate graph must be unaffected by this feature.
    expect(traceNodeVisual(undefined)).toBeNull();
  });

  it("never says the message is waiting or in progress", () => {
    // The core design constraint: spans only exist once ended, so any
    // wording implying present-tense activity is a guess.
    for (const state of ["reached", "failed", "next", "not_reached"] as const) {
      const v = traceNodeVisual(node({ state, after_service: "order-api" }))!;
      const text = `${v.label} ${v.detail}`.toLowerCase();
      expect(text, `state=${state}`).not.toMatch(/waiting|in progress|currently|stuck in/);
    }
  });

  it("names what the frontier follows, since that is the actionable part", () => {
    const v = traceNodeVisual(node({ state: "next", after_service: "order-api" }))!;
    expect(v.label).toBe("expected after order-api");
    expect(v.tone).toBe("next");
    expect(v.dimmed).toBe(false);
  });

  it("still reads sensibly when the frontier has no named upstream", () => {
    const v = traceNodeVisual(node({ state: "next" }))!;
    expect(v.label).toBe("expected next");
  });

  it("spells out the three possible causes of an absent span", () => {
    // Otherwise "expected after X" reads as an accusation against X.
    const v = traceNodeVisual(node({ state: "next", after_service: "order-api" }))!;
    expect(v.detail).toMatch(/has not run/);
    expect(v.detail).toMatch(/has not finished/);
    expect(v.detail).toMatch(/never reported/);
  });

  it("dims only the nodes that are not part of the story", () => {
    expect(traceNodeVisual(node({ state: "not_reached" }))!.dimmed).toBe(true);
    expect(traceNodeVisual(node({ state: "next" }))!.dimmed).toBe(false);
    expect(traceNodeVisual(node({ state: "reached" }))!.dimmed).toBe(false);
  });

  it("marks a failure as the likely stopping point", () => {
    const v = traceNodeVisual(node({ state: "failed", error_count: 2 }))!;
    expect(v.tone).toBe("err");
    expect(v.label).toContain("failed here");
    expect(v.detail).toContain("2 spans");
  });

  it("singularises a single failed span", () => {
    expect(traceNodeVisual(node({ state: "failed", error_count: 1 }))!.detail).toContain("1 span");
  });

  it("omits the clock when there is no timestamp", () => {
    expect(traceNodeVisual(node({ state: "reached" }))!.label).toBe("reached");
  });

  it("ignores an unparseable timestamp rather than printing Invalid Date", () => {
    const v = traceNodeVisual(node({ state: "reached", first_seen: "not-a-date" }))!;
    expect(v.label).toBe("reached");
  });
});

describe("traceSummaryLine", () => {
  it("says plainly when the message touched nothing here", () => {
    // A trace id pasted from another integration, or one the caller
    // cannot see any of.
    expect(traceSummaryLine(undefined, [])).toMatch(/no spans on any service/);
  });

  it("leads with the failure when there is one", () => {
    const line = traceSummaryLine("order-api", [
      node({ service_name: "order-api", state: "failed", error_count: 1 }),
      node({ service_name: "warehouse-sync", state: "next", after_service: "order-api" }),
    ]);
    expect(line).toMatch(/^Failed at order-api/);
  });

  it("points at the frontier when nothing failed", () => {
    // The headline sentence of the whole feature.
    const line = traceSummaryLine("order-api", [
      node({ service_name: "order-api", state: "reached" }),
      node({ service_name: "warehouse-sync", state: "next", after_service: "order-api" }),
    ]);
    expect(line).toBe(
      "Last seen on order-api. Not yet on warehouse-sync. That is where to look.",
    );
  });

  it("does not invent a frontier when the message completed the flow", () => {
    const line = traceSummaryLine("ledger", [
      node({ service_name: "gateway", state: "reached" }),
      node({ service_name: "ledger", state: "reached" }),
    ]);
    expect(line).toMatch(/end of the flow as drawn/);
  });

  it("lists every frontier node, not just the first", () => {
    const line = traceSummaryLine("gateway", [
      node({ service_name: "gateway", state: "reached" }),
      node({ service_name: "a", state: "next", after_service: "gateway" }),
      node({ service_name: "b", state: "next", after_service: "gateway" }),
    ]);
    expect(line).toContain("a, b");
  });
});

describe("traceStatesByService", () => {
  it("indexes by service name", () => {
    const idx = traceStatesByService([
      node({ service_name: "a", state: "reached" }),
      node({ service_name: "b", state: "next" }),
    ]);
    expect(idx.a.state).toBe("reached");
    expect(idx.b.state).toBe("next");
  });

  it("survives an absent projection", () => {
    expect(traceStatesByService(undefined)).toEqual({});
  });
});
