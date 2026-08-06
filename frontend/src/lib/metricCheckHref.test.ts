// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The link has to land on the data the check actually judges, over a
// window that contains the moment it broke. Get either wrong and the
// chart is worse than no link: it shows a healthy-looking series (wrong
// filters) or a flat line with the onset off-screen (wrong window).

import { describe, expect, it } from "vitest";
import { metricCheckHref, windowCovering } from "./metricCheckHref";
import type { AlertInstance, AlertRule } from "../api/types";

const rule = (over: Partial<AlertRule> = {}): AlertRule =>
  ({
    id: "r1",
    signal: "metric",
    source: "telemetry",
    severity: "critical",
    enabled: true,
    spec: { metric_name: "queue.depth", aggregation: "last", operator: "gt", threshold: 1, for_window: "5m" },
    ...over,
  }) as AlertRule;

const firing = (startedAt: string) => ({ started_at: startedAt }) as AlertInstance;

describe("windowCovering", () => {
  const now = Date.parse("2026-08-06T12:00:00Z");
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it("picks the smallest window that reaches past the onset", () => {
    // Smallest on purpose: a month of data flattens a step change into a
    // vertical line at the left edge.
    expect(windowCovering(ago(10 * 60 * 1000), now)).toBe("15m");
    expect(windowCovering(ago(3 * 60 * 60 * 1000), now)).toBe("6h");
    expect(windowCovering(ago(20 * 60 * 60 * 1000), now)).toBe("24h");
  });

  it("leaves headroom so the onset is not on the boundary", () => {
    // 55m old: it fits inside 1h, but only just — the transition would
    // sit on the chart's left edge, which is what you came to look at.
    expect(windowCovering(ago(55 * 60 * 1000), now)).toBe("3h");
  });

  it("clamps to the widest window rather than returning nothing", () => {
    expect(windowCovering(ago(400 * 24 * 60 * 60 * 1000), now)).toBe("30d");
  });

  it("survives a start time it cannot read", () => {
    expect(windowCovering("not-a-date", now)).toBe("1h");
    // A clock-skewed future start must not produce a negative window.
    expect(windowCovering(new Date(now + 60_000).toISOString(), now)).toBe("1h");
  });
});

describe("metricCheckHref", () => {
  it("carries the metric and the rule's own predicates", () => {
    const href = metricCheckHref(
      rule({
        spec: {
          metric_name: "httpcheck.status",
          aggregation: "last",
          operator: "lt",
          threshold: 1,
          for_window: "5m",
          attrs: [{ key: "http.status_class", op: "eq", value: "2xx" }],
        },
      } as Partial<AlertRule>),
    );
    expect(href).toContain("metric=httpcheck.status");
    // Without the predicate the chart shows the metric as a whole, which
    // for a shared series is a different question than the check asks.
    expect(decodeURIComponent(href!)).toContain('"key":"http.status_class"');
  });

  it("drops half-filled predicates rather than sending them", () => {
    const href = metricCheckHref(
      rule({
        spec: {
          metric_name: "m", aggregation: "last", operator: "gt", threshold: 1, for_window: "5m",
          attrs: [{ key: "", op: "eq", value: "" }],
        },
      } as Partial<AlertRule>),
    );
    expect(href).not.toContain("mattr");
  });

  it("sets a range only when there is a firing to cover", () => {
    expect(metricCheckHref(rule())).not.toContain("range=");
    expect(metricCheckHref(rule(), firing(new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString()))).toContain("range=6h");
    // A minutes-old firing gets a minutes-wide window, not an hour that
    // squashes the transition against the axis.
    expect(metricCheckHref(rule(), firing(new Date(Date.now() - 4 * 60 * 1000).toISOString()))).toContain("range=5m");
  });

  it("offers nothing for a check with no single series behind it", () => {
    // A log match, a failed-trace check, and a pushed value have no
    // metric to open — a dead link would be worse than no link.
    expect(metricCheckHref(rule({ signal: "log" }))).toBeNull();
    expect(metricCheckHref(rule({ signal: "trace" }))).toBeNull();
    expect(metricCheckHref(rule({ source: "pushed" }))).toBeNull();
    expect(
      metricCheckHref(rule({ spec: { metric_name: "  ", aggregation: "last", operator: "gt", threshold: 1, for_window: "5m" } } as Partial<AlertRule>)),
    ).toBeNull();
  });
});
