// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a trace-completion stage reads and how the rule editor round-trips
// it. A stage waits for spans, each optionally from one service; with
// require "all" every one must arrive (a fan-out to several subscribers
// that emit the same standardised span name).

import type { TraceCompletionSpan, TraceCompletionStage } from "../api/types";

// spansOf reads a stage's spans, falling back to span_names (any
// service) for a stage saved before spans existed.
export function spansOf(st: TraceCompletionStage): TraceCompletionSpan[] {
  if (st.spans && st.spans.length > 0) return st.spans;
  return (st.span_names ?? []).map((span_name) => ({ span_name }));
}

export function spanLabel(sp: TraceCompletionSpan): string {
  return sp.service ? `${sp.service}: ${sp.span_name}` : sp.span_name;
}

export function sameSpan(a: TraceCompletionSpan, b: TraceCompletionSpan): boolean {
  return (a.service ?? "") === (b.service ?? "") && a.span_name === b.span_name;
}

// addSpan adds a span to a stage's list unless it is already there.
export function addSpan(spans: TraceCompletionSpan[], sp: TraceCompletionSpan): TraceCompletionSpan[] {
  const name = sp.span_name.trim();
  if (!name) return spans;
  const next: TraceCompletionSpan = sp.service ? { service: sp.service, span_name: name } : { span_name: name };
  return spans.some((x) => sameSpan(x, next)) ? spans : [...spans, next];
}
