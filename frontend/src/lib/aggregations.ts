// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The aggregation functions a metric rule can use, and how they read.
//
// There were two copies of this list. The metrics-page builder offered
// all nine; the health-check editor offered seven, silently missing
// `last` and `age`. So an HTTP check on `httpcheck.status` — a
// point-in-time gauge where `last` is the only correct choice — could
// not be built from the health-check editor at all, and templates that
// ship with `last` (the paperless ones) rendered a picker with no
// matching option.
//
// One list now, ordered by the labels record. Because AGG_LABELS is
// typed Record<AlertAggregation, string>, adding an aggregation to the
// union fails to compile until it has a label — and it then appears in
// every picker automatically, rather than in whichever one someone
// remembered to update.

import type { AlertAggregation } from "../api/types";

export const AGG_LABELS: Record<AlertAggregation, string> = {
  // "last" is first because it is the right answer for gauges — queue
  // depth, status codes, up/down — which is most of what a health check
  // watches.
  last: "last value",
  max: "max",
  avg: "avg",
  min: "min",
  sum: "sum",
  p95: "p95",
  increase: "increase (counter Δ)",
  rate: "rate (per sec)",
  // "age" treats the metric's value as a Unix timestamp and thresholds
  // now − value in SECONDS — e.g. "file.mtime age > 3600" = file untouched
  // for over an hour. Pair with gt for a staleness health check.
  age: "age / time since (sec)",
};

/**
 * Every aggregation, in picker order. Derived from AGG_LABELS so the two
 * cannot disagree — string keys iterate in declaration order.
 */
export const ALERT_AGGREGATIONS = Object.keys(AGG_LABELS) as AlertAggregation[];
