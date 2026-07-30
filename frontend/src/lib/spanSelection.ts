// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which span a trace view opens on.
//
// This lives outside the components because it is a decision, not a
// rendering concern, and it is the part that can be wrong in ways the
// eye won't catch — selecting a span that isn't in the trace looks
// identical to selecting nothing.

import type { SpanSummary } from "../api/types";

/**
 * Chooses the span to select when a trace is opened, in priority order:
 *
 *   1. the first span that satisfied the search the user arrived from,
 *   2. the first error span,
 *   3. the first span.
 *
 * `matchedSpanIds` is checked against the trace's own spans rather than
 * trusted: the search and the trace fetch are separate reads, and a span
 * can age out of retention between them. Selecting an id that no longer
 * exists would leave an empty attribute pane with no explanation.
 *
 * Returns null only for a trace with no spans.
 */
export function pickInitialSpan(
  spans: SpanSummary[],
  matchedSpanIds?: string[],
): string | null {
  if (spans.length === 0) return null;
  if (matchedSpanIds && matchedSpanIds.length > 0) {
    const inTrace = new Set(spans.map((s) => s.span_id));
    const firstMatch = matchedSpanIds.find((id) => inTrace.has(id));
    if (firstMatch) return firstMatch;
  }
  const firstErr = spans.find((s) => s.status_code === "Error");
  return (firstErr ?? spans[0]).span_id;
}
