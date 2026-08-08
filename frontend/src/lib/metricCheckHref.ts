// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The deep link from a metric health check to the telemetry behind it.
//
// A failing check told you THAT something was wrong and nothing else:
// not when it started, not what the numbers did. Answering "when did it
// break, and for how long" meant opening Metrics, finding the metric by
// name, re-typing the rule's attribute filters from memory, and guessing
// a window wide enough to contain the onset.
//
// This builds that link instead: the metric, the rule's own predicates,
// and a window that provably covers the firing.

import type { AlertInstance, AlertRule } from "../api/types";

/**
 * The presets TimeWindowPicker offers, smallest first.
 *
 * Deliberately the same vocabulary: a range the picker does not know
 * would land the user on a window its own control cannot show as
 * selected. Keep in step with PRESETS in TimeWindowPicker.tsx.
 */
const MIN = 60 * 1000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const WINDOWS: { param: string; ms: number }[] = [
  { param: "5m", ms: 5 * MIN },
  { param: "15m", ms: 15 * MIN },
  { param: "30m", ms: 30 * MIN },
  { param: "1h", ms: HOUR },
  { param: "3h", ms: 3 * HOUR },
  { param: "6h", ms: 6 * HOUR },
  { param: "12h", ms: 12 * HOUR },
  { param: "24h", ms: DAY },
  { param: "2d", ms: 2 * DAY },
  { param: "7d", ms: 7 * DAY },
  { param: "30d", ms: 30 * DAY },
];

/**
 * Smallest offered window that still reaches back past `startedAt`.
 *
 * Smallest, not largest: the point is to SEE the onset, and a month of
 * data flattens a step change into a vertical line at the left edge.
 * Padded so the transition is not sitting exactly on the boundary, and
 * clamped to the largest window when the check has been firing longer
 * than any of them.
 */
export function windowCovering(startedAt: string, now = Date.now()): string {
  const age = now - new Date(startedAt).getTime();
  if (!Number.isFinite(age) || age < 0) return "1h";
  // A little headroom so the onset sits inside the chart rather than on
  // its edge. Deliberately small: at 25% a check firing for 20h skipped
  // 24h and landed on 7d, which buries the transition it was meant to
  // show. 10% clears a boundary case without jumping a whole step.
  const needed = age * 1.1;
  return (WINDOWS.find((w) => w.ms >= needed) ?? WINDOWS[WINDOWS.length - 1]).param;
}

/**
 * Which telemetry the check is bound to, in the vocabulary the metric
 * catalogue understands: a service name, or an integration's display
 * name (whose services the catalogue resolves for us).
 *
 * Supplied by the caller because a rule carries ids, not names, and the
 * page rendering the link already knows what it is looking at.
 */
export interface CheckLinkScope {
  service?: string;
  integration?: string;
}

/**
 * The metrics-page URL for a rule, or null when there is no single
 * series to open — a log match or a failed-trace check has no metric,
 * and a pushed check's value never came from one.
 */
export function metricCheckHref(
  rule: AlertRule,
  instance?: AlertInstance,
  scope?: CheckLinkScope,
): string | null {
  if (rule.signal !== "metric" || rule.source === "pushed") return null;
  const metric = rule.spec?.metric_name?.trim();
  if (!metric) return null;

  const params = new URLSearchParams();
  params.set("metric", metric);
  // The same predicates the evaluator applies, so the chart shows the
  // slice the check judges rather than the metric as a whole.
  const attrs = (rule.spec.attrs ?? []).filter((a) => a.key && a.value);
  if (attrs.length > 0) {
    params.set("mattr", JSON.stringify(attrs.map((a) => ({ key: a.key, op: a.op, value: a.value }))));
  }
  // The rule's BINDING, which is scope the predicates never carried. An
  // integration-bound check on file.mtime judges that integration's
  // services; without this the link opened every service reporting
  // file.mtime, which for a fleet of file transfers is every transfer at
  // once — the one view that cannot answer "is THIS one late".
  if (scope?.service) params.set("service", scope.service);
  else if (scope?.integration) params.set("integration", scope.integration);
  if (instance?.started_at) params.set("range", windowCovering(instance.started_at));
  return `/metrics?${params.toString()}`;
}

/**
 * Tooltip for the link, honest about how far the filtering goes.
 *
 * The catalogue narrows by service and by integration but has no system
 * dimension, so a system-bound check can only open the metric with its
 * own predicates. Saying "filtered as the check sees it" there would
 * promise a narrowing the URL does not carry.
 */
export function metricCheckLinkTitle(scope?: CheckLinkScope): string {
  return scope?.service || scope?.integration
    ? "Open this metric, filtered as the check sees it"
    : "Open this metric with the check's own filters, across every service reporting it";
}
