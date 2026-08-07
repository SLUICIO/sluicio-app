// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The link to "where is this message right now" (issue #15).
//
// One function rather than a template literal at each call site, because
// there are three entry points (the trace drawer, a firing completion
// alert, and anything pasted into a ticket) and they must agree exactly.
// A link that differs by one encoded character still loads the page and
// silently projects nothing, which reads as the feature being broken.

/**
 * The integration flow view with one message projected onto it.
 *
 * Returns null when either id is missing: the projection is a position
 * on a specific integration's graph, so without an integration there is
 * nothing to project onto and the caller should render no link at all
 * rather than one that leads somewhere useless.
 */
export function traceOnFlowHref(
  integrationId: string | undefined | null,
  traceId: string | undefined | null,
): string | null {
  const integ = (integrationId ?? "").trim();
  const trace = (traceId ?? "").trim();
  if (!integ || !trace) return null;
  return `/integrations/${encodeURIComponent(integ)}?trace=${encodeURIComponent(trace)}`;
}
