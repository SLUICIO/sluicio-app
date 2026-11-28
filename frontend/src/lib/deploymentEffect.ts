// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The line the Deployments tab shows for an applied Advisor change: the
// volume of what it drops (a metric, or a service's logs below a
// severity) in the same length of time before and after the collector
// applied it.

import type { CollectorDeploymentEffect } from "../api/types";
import { formatDurationSeconds, formatNumber } from "./format";

// Above this it counts as still arriving: the change took effect for
// this collector, but the telemetry also comes some other way.
const STOPPED_PCT = -95;

// collectorShare is the line for what came through this collector alone,
// when it stamps its data; "" otherwise.
export function collectorShare(e: CollectorDeploymentEffect): string {
  const m = e.collector_effect;
  if (!e.applied || !m || !m.ready) return "";
  const rate = (n: number) => `${formatNumber(Math.round(n))}/h`;
  if (m.before.rows === 0)
    return "Through this collector: none of it before the change.";
  return `Through this collector: ${rate(m.before.per_hour)} before, ${rate(m.after.per_hour)} after${
    m.after.rows === 0 ? ", it stopped." : "."
  }`;
}

export function effectSummary(e: CollectorDeploymentEffect): string {
  if (!e.applied || !e.effect) return "";
  const m = e.effect;
  if (!m.ready) {
    return `The effect on ${e.subject} is measured from 15 minutes after the collector applied the change.`;
  }
  const span = formatDurationSeconds(
    (Date.parse(m.after.to) - Date.parse(m.after.from)) / 1000,
  );
  const rate = (n: number) => `${formatNumber(Math.round(n))}/h`;
  const head = `${e.subject}: ${rate(m.before.per_hour)} before, ${rate(m.after.per_hour)} after (${span} each side)`;
  if (m.change_pct === undefined || m.change_pct === null) {
    return `${head}. It was not arriving before the change.`;
  }
  const pct = `${m.change_pct > 0 ? "+" : ""}${Math.round(m.change_pct)}%`;
  if (m.after.rows === 0) return `${head}, ${pct}: it stopped arriving.`;
  if (m.change_pct <= STOPPED_PCT) return `${head}, ${pct}.`;
  return `${head}, ${pct}. It still arrives, probably through another collector or pipeline.`;
}
