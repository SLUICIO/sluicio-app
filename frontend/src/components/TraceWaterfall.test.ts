// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reported: a trace with twenty steps starting in the same millisecond
// is unreadable, because you cannot tell what hangs off what. Sorting by
// start time alone leaves simultaneous spans in whatever order the
// database returned, so children of different parents interleave.

import { describe, expect, it } from "vitest";
import { orderSpans } from "./TraceWaterfall";
import type { SpanSummary } from "../api/types";

const span = (id: string, parent: string | undefined, atMs: number): SpanSummary =>
  ({
    span_id: id,
    parent_span_id: parent,
    timestamp: new Date(atMs).toISOString(),
    trace_id: "t",
    service_name: "svc",
    span_name: id,
    span_kind: "SPAN_KIND_INTERNAL",
    status_code: "Unset",
    duration_ms: 1,
  }) as SpanSummary;

const shape = (rows: { span: SpanSummary; depth: number }[]) =>
  rows.map((r) => `${"  ".repeat(r.depth)}${r.span.span_id}`);

describe("orderSpans", () => {
  it("keeps a subtree together even when everything starts at once", () => {
    // Two parents, two children each, all at the same instant, handed
    // over interleaved the way a database returns them.
    const spans = [
      span("a", undefined, 1000),
      span("b", undefined, 1000),
      span("a1", "a", 1000),
      span("b1", "b", 1000),
      span("a2", "a", 1000),
      span("b2", "b", 1000),
    ];
    expect(shape(orderSpans(spans))).toEqual(["a", "  a1", "  a2", "b", "  b1", "  b2"]);
  });

  it("orders siblings by start time", () => {
    const spans = [span("a", undefined, 1000), span("late", "a", 1200), span("early", "a", 1100)];
    expect(shape(orderSpans(spans))).toEqual(["a", "  early", "  late"]);
  });

  it("nests deeply", () => {
    const spans = [
      span("a", undefined, 1000),
      span("c", "b", 1002),
      span("b", "a", 1001),
    ];
    expect(shape(orderSpans(spans))).toEqual(["a", "  b", "    c"]);
  });

  // A trace fetched in part, or the first span of a message that
  // continues another one: the parent is simply not here.
  it("treats a span whose parent is absent as a root", () => {
    const spans = [span("orphan", "not-in-this-trace", 1000), span("a", undefined, 1001)];
    expect(shape(orderSpans(spans))).toEqual(["orphan", "a"]);
  });

  // Malformed telemetry is somebody else's bug and must not be ours to
  // hang on. Every span still has to appear.
  it("survives a parent cycle without losing a span", () => {
    const spans = [span("x", "y", 1000), span("y", "x", 1001), span("z", undefined, 1002)];
    const out = orderSpans(spans);
    expect(out).toHaveLength(3);
    expect(out.map((r) => r.span.span_id).sort()).toEqual(["x", "y", "z"]);
  });

  it("returns nothing for nothing", () => {
    expect(orderSpans([])).toEqual([]);
  });
});
