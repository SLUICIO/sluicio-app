// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// pickInitialSpan decides which span a trace opens on. The failure this
// guards against is quiet: selecting a span that isn't in the trace
// renders exactly like selecting nothing, so a regression here shows up
// as "the details pane is empty sometimes" rather than as an error.

import { describe, expect, it } from "vitest";
import { pickInitialSpan } from "./spanSelection";
import type { SpanSummary } from "../api/types";

function span(id: string, status: "Ok" | "Error" = "Ok"): SpanSummary {
  return {
    span_id: id,
    parent_span_id: "",
    span_name: `span-${id}`,
    service_name: "svc",
    timestamp: "2026-07-30T10:00:00Z",
    duration_ms: 1,
    status_code: status,
  } as SpanSummary;
}

describe("pickInitialSpan", () => {
  it("selects the matching span over the trace's own default", () => {
    // c is an error span, so without a match it would win. The whole
    // point of the feature is that the user's query outranks it.
    const spans = [span("a"), span("b"), span("c", "Error")];
    expect(pickInitialSpan(spans, ["b"])).toBe("b");
  });

  it("selects the FIRST match when several spans matched", () => {
    const spans = [span("a"), span("b"), span("c")];
    expect(pickInitialSpan(spans, ["b", "c"])).toBe("b");
  });

  it("ignores matched ids that are not in this trace", () => {
    // The search and the trace fetch are separate reads; a span can age
    // out of retention between them. Honouring a stale id would select
    // nothing at all and show an empty attribute pane.
    const spans = [span("a"), span("b", "Error")];
    expect(pickInitialSpan(spans, ["gone"])).toBe("b");
  });

  it("skips stale ids but still honours a live one further down the list", () => {
    const spans = [span("a"), span("b")];
    expect(pickInitialSpan(spans, ["gone", "b"])).toBe("b");
  });

  it("falls back to the first error span when there is no match", () => {
    const spans = [span("a"), span("b", "Error"), span("c", "Error")];
    expect(pickInitialSpan(spans, undefined)).toBe("b");
    expect(pickInitialSpan(spans, [])).toBe("b");
  });

  it("falls back to the first span when nothing errored", () => {
    expect(pickInitialSpan([span("a"), span("b")], [])).toBe("a");
  });

  it("returns null for a trace with no spans", () => {
    expect(pickInitialSpan([], ["a"])).toBeNull();
  });
});
