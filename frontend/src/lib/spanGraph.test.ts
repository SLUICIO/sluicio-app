// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The graph's failure mode is being confidently unreadable, or
// confidently incomplete. These pin the three rules that prevent both.

import { describe, expect, it } from "vitest";
import { MAX_DRAWABLE_NODES, buildSpanGraph, graphRefusal } from "./spanGraph";
import type { SpanSummary } from "../api/types";

const T0 = "2026-08-09T12:00:00.000Z";
const at = (offsetMs: number) => new Date(Date.parse(T0) + offsetMs).toISOString();

function span(o: Partial<SpanSummary> & { span_id: string }): SpanSummary {
  return {
    timestamp: T0,
    trace_id: "t",
    service_name: "svc",
    span_name: "work",
    span_kind: "Internal",
    status_code: "Unset",
    duration_ms: 10,
    ...o,
  };
}

describe("collapsing", () => {
  it("folds identical siblings into one node with a count", () => {
    // A loop body that ran nineteen times is one node marked x19. Not
    // only a rendering convenience: that it ran nineteen times is
    // itself the fact worth showing.
    const spans = [span({ span_id: "root", span_name: "run" })];
    for (let i = 0; i < 19; i++) {
      spans.push(span({ span_id: `c${i}`, parent_span_id: "root", span_name: "step" }));
    }
    const g = buildSpanGraph(spans);
    expect(g.nodes).toHaveLength(2);
    const loop = g.nodes.find((n) => n.name === "step")!;
    expect(loop.count).toBe(19);
    expect(loop.spanIds).toHaveLength(19);
    expect(g.rawSpans).toBe(20);
  });

  it("does not fold same-named spans under different parents", () => {
    // Same name under different callers is different work that happens
    // to be named alike, not a repetition.
    const g = buildSpanGraph([
      span({ span_id: "a" }),
      span({ span_id: "b" }),
      span({ span_id: "x", parent_span_id: "a", span_name: "save" }),
      span({ span_id: "y", parent_span_id: "b", span_name: "save" }),
    ]);
    expect(g.nodes.filter((n) => n.name === "save")).toHaveLength(2);
  });

  it("does not fold across services", () => {
    const g = buildSpanGraph([
      span({ span_id: "root" }),
      span({ span_id: "a", parent_span_id: "root", service_name: "one" }),
      span({ span_id: "b", parent_span_id: "root", service_name: "two" }),
    ]);
    expect(g.nodes).toHaveLength(3);
  });

  it("draws one edge per parent/child node pair, not per folded span", () => {
    const spans = [span({ span_id: "root" })];
    for (let i = 0; i < 5; i++) {
      spans.push(span({ span_id: `c${i}`, parent_span_id: "root", span_name: "step" }));
    }
    expect(buildSpanGraph(spans).edges).toHaveLength(1);
  });

  it("marks a node failed when any folded span failed", () => {
    const g = buildSpanGraph([
      span({ span_id: "a", span_name: "retry" }),
      span({ span_id: "b", span_name: "retry", status_code: "Error" }),
    ]);
    expect(g.nodes[0].failed).toBe(true);
    expect(g.nodes[0].count).toBe(2);
  });
});

describe("concurrency", () => {
  it("reports overlap as measured, for siblings in flight together", () => {
    const g = buildSpanGraph([
      span({ span_id: "root" }),
      span({ span_id: "a", parent_span_id: "root", span_name: "fan", timestamp: at(0), duration_ms: 100 }),
      span({ span_id: "b", parent_span_id: "root", span_name: "fan", timestamp: at(10), duration_ms: 100 }),
    ]);
    expect(g.nodes.find((n) => n.name === "fan")!.overlapping).toBe(true);
  });

  it("does not claim overlap for work that ran in sequence", () => {
    // A retry looks identical to a fan-out from the outside, so the
    // only honest signal is whether the time ranges actually overlap.
    const g = buildSpanGraph([
      span({ span_id: "root" }),
      span({ span_id: "a", parent_span_id: "root", span_name: "try", timestamp: at(0), duration_ms: 10 }),
      span({ span_id: "b", parent_span_id: "root", span_name: "try", timestamp: at(20), duration_ms: 10 }),
    ]);
    expect(g.nodes.find((n) => n.name === "try")!.overlapping).toBe(false);
  });

  it("spans the full range of the folded work", () => {
    const g = buildSpanGraph([
      span({ span_id: "a", span_name: "w", timestamp: at(0), duration_ms: 10 }),
      span({ span_id: "b", span_name: "w", timestamp: at(50), duration_ms: 25 }),
    ]);
    const n = g.nodes[0];
    expect(n.endMs - n.startMs).toBe(75);
  });
});

describe("hand-offs", () => {
  it("draws a link as a hand-off, not as nesting", () => {
    const g = buildSpanGraph(
      [span({ span_id: "a", links: [{ trace_id: "other", span_id: "far" }] })],
      at(0),
    );
    expect(g.handoffs).toEqual([{ from: "a", traceId: "other", spanId: "far" }]);
    expect(g.edges).toHaveLength(0);
    expect(g.handoffsUnknown).toBe(false);
  });

  it("says hand-offs are UNKNOWN only for a trace that predates link storage", () => {
    // The critical distinction, and the one the first version got
    // wrong. An empty link array is ambiguous: the migration gave every
    // pre-existing row one, so "had none" and "was never recorded" are
    // byte-identical. Only the trace's own age can tell them apart.
    const old = buildSpanGraph([span({ span_id: "a", timestamp: at(0) })], at(1000));
    expect(old.handoffsUnknown).toBe(true);
  });

  it("does NOT caveat a modern trace that simply has no hand-offs", () => {
    // The bug this replaces: every ordinary message claimed to predate
    // the feature, which is both false and the kind of caveat that
    // teaches people to ignore caveats.
    const fresh = buildSpanGraph([span({ span_id: "a", timestamp: at(5000) })], at(1000));
    expect(fresh.handoffsUnknown).toBe(false);
    expect(fresh.handoffs).toHaveLength(0);
  });

  it("treats everything as unknown when the cell never recorded links", () => {
    const g = buildSpanGraph([span({ span_id: "a" })]);
    expect(g.handoffsUnknown).toBe(true);
  });

  it("never caveats a trace that HAS hand-offs", () => {
    const g = buildSpanGraph(
      [span({ span_id: "a", timestamp: at(0), links: [{ trace_id: "o", span_id: "f" }] })],
      at(1000),
    );
    expect(g.handoffsUnknown).toBe(false);
  });
});

describe("refusing to draw", () => {
  it("draws a huge trace that collapses small", () => {
    // The case the rule exists for: 5000 spans whose loop ran 400 times
    // is a dozen nodes and draws perfectly, and it is exactly where the
    // picture explains the most.
    const spans = [span({ span_id: "root" })];
    for (let i = 0; i < 5000; i++) {
      spans.push(span({ span_id: `c${i}`, parent_span_id: "root", span_name: "step" }));
    }
    const g = buildSpanGraph(spans);
    expect(g.rawSpans).toBe(5001);
    expect(g.nodes).toHaveLength(2);
    expect(graphRefusal(g)).toBeNull();
  });

  it("refuses a trace that is genuinely wide after collapsing", () => {
    const spans = [span({ span_id: "root" })];
    for (let i = 0; i < MAX_DRAWABLE_NODES + 5; i++) {
      spans.push(span({ span_id: `c${i}`, parent_span_id: "root", span_name: `distinct-${i}` }));
    }
    const r = graphRefusal(buildSpanGraph(spans))!;
    expect(r).not.toBeNull();
    // Interprets the number rather than reporting it, the same way the
    // waterfall's truncation notice already does.
    expect(r.reason).toMatch(/span per item rather than per stage/);
    // And points somewhere that works, instead of a dead end.
    expect(r.fallback).toMatch(/waterfall/);
  });

  it("handles an empty trace without claiming anything", () => {
    const g = buildSpanGraph([]);
    expect(g.nodes).toHaveLength(0);
    expect(g.handoffsUnknown).toBe(false);
    expect(graphRefusal(g)).toBeNull();
  });
});
