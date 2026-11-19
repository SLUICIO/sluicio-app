// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Two services in one flow usually share a long prefix -
// product-consolidation-aggregator-service beside
// product-consolidation-extractor-service - and a node that cuts the
// name at its edge renders both as "product-consolidati…". The graph
// then stops distinguishing the things it exists to distinguish, which
// is how this was reported.

import { describe, expect, it } from "vitest";
import { isBackEdge, layout, maxNameLines } from "./IntegrationFlow";
import type { FlowEdge, FlowNode } from "../api/types";

describe("maxNameLines", () => {
  it("keeps short names on one line, so a simple flow stays compact", () => {
    expect(maxNameLines(["checkout-api", "erp-adapter"])).toBe(1);
  });

  it("gives the reported names a second line", () => {
    expect(
      maxNameLines([
        "product-consolidation-aggregator-service",
        "product-consolidation-extractor-service",
      ]),
    ).toBe(2);
  });

  it("is decided by the longest name, since every node is the same height", () => {
    expect(maxNameLines(["a", "product-consolidation-aggregator-service"])).toBe(2);
  });

  it("stops at two lines: a node has to stay a node", () => {
    expect(maxNameLines(["x".repeat(400)])).toBe(2);
  });

  it("handles an empty flow without asking for half a line", () => {
    expect(maxNameLines([])).toBe(1);
  });

  // 8px a character is about 14px semibold in the app's font; the node
  // has 152px for the name.
  const measured = (t: string) => t.length * 8;

  it("breaks after a hyphen the way the browser does", () => {
    // 20 characters, 160px: the browser breaks after "part-", which a
    // count of 24 characters a line missed and the trace count spilled
    // out of the node.
    expect(maxNameLines(["wms-loader-part-sync"], measured)).toBe(2);
    expect(maxNameLines(["erp-loader-sync"], measured)).toBe(1);
  });

  it("breaks a part wider than a line anywhere", () => {
    expect(maxNameLines(["a-" + "x".repeat(25)], measured)).toBe(2);
  });
});

// Two services calling each other drew the return leg along the same row
// as the forward one: on top of it and behind the boxes, so the flow
// showed an arrow from nowhere and a line running off into the canvas.
describe("isBackEdge", () => {
  const node = (service_name: string): FlowNode => ({ service_name, trace_count: 1, error_trace_count: 0 });
  const edge = (source: string, target: string): FlowEdge => ({ source, target, call_count: 1, error_count: 0 });

  it("marks the return leg of two services calling each other, and only that", () => {
    const nodes = [node("idle"), node("a"), node("b")];
    const edges = [edge("a", "b"), edge("b", "a")];
    const pos = layout(nodes, edges, 76);
    expect(edges.filter((e) => isBackEdge(pos, e))).toHaveLength(1);
  });

  it("leaves a chain without cycles drawn forward", () => {
    const nodes = [node("a"), node("b"), node("c")];
    const edges = [edge("a", "b"), edge("b", "c"), edge("a", "c")];
    const pos = layout(nodes, edges, 76);
    expect(edges.some((e) => isBackEdge(pos, e))).toBe(false);
  });

  it("marks one hop of a longer loop, wherever the layout breaks it", () => {
    const nodes = [node("a"), node("b"), node("c")];
    const edges = [edge("a", "b"), edge("b", "c"), edge("c", "a")];
    const pos = layout(nodes, edges, 76);
    expect(edges.filter((e) => isBackEdge(pos, e))).toHaveLength(1);
  });
});
