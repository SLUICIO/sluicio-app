// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Recharts wants resolved color strings (it sets SVG attributes that
// don't honor CSS var() references), so we can't just sprinkle
// var(--text-muted) into the chart props. useChartTheme reads the
// computed CSS variable values from <html> and re-runs when the
// global theme changes, returning the same shape of style objects
// the chart components used to import as constants.

import { useMemo } from "react";
import { useTheme } from "../../lib/useTheme";

interface ResolvedColors {
  text: string;
  textMuted: string;
  border: string;
  surfaceElevated: string;
  accent: string;
  warning: string;
  critical: string;
  ok: string;
}

function readColors(): ResolvedColors {
  if (typeof window === "undefined") {
    // SSR / test fallback — light defaults.
    return {
      text: "#1e293b",
      textMuted: "#64748b",
      border: "#e2e8f0",
      surfaceElevated: "#f1f5f9",
      accent: "#2563eb",
      warning: "#d97706",
      critical: "#dc2626",
      ok: "#16a34a",
    };
  }
  const cs = getComputedStyle(document.documentElement);
  const get = (name: string, fallback: string) =>
    (cs.getPropertyValue(name).trim() || fallback);
  return {
    text: get("--text", "#1e293b"),
    textMuted: get("--text-muted", "#64748b"),
    border: get("--border", "#e2e8f0"),
    surfaceElevated: get("--surface-elevated", "#f1f5f9"),
    accent: get("--accent", "#2563eb"),
    warning: get("--warning", "#d97706"),
    critical: get("--critical", "#dc2626"),
    ok: get("--ok", "#16a34a"),
  };
}

export interface ChartTheme {
  axisStyle: Record<string, unknown>;
  gridStyle: Record<string, unknown>;
  tooltipStyle: Record<string, unknown>;
  legendStyle: Record<string, unknown>;
  colors: ResolvedColors;
}

export function useChartTheme(): ChartTheme {
  // useTheme as a re-render trigger — the hook value changes when the
  // user switches themes, which causes useMemo below to recompute.
  const [theme] = useTheme();
  return useMemo<ChartTheme>(() => {
    const c = readColors();
    return {
      colors: c,
      axisStyle: {
        stroke: c.textMuted,
        fontSize: 11,
        tickLine: false,
        axisLine: { stroke: c.border },
      },
      gridStyle: {
        stroke: c.border,
        strokeDasharray: "3 3",
        vertical: false,
      },
      tooltipStyle: {
        contentStyle: {
          background: c.surfaceElevated,
          border: `1px solid ${c.border}`,
          borderRadius: 6,
          color: c.text,
          fontSize: 12,
        },
        labelStyle: { color: c.textMuted, marginBottom: 4 },
        cursor: { stroke: c.accent, strokeDasharray: "3 3" },
      },
      legendStyle: {
        wrapperStyle: { paddingTop: 8, fontSize: 12, color: c.textMuted },
      },
    };
    // theme is the dependency that triggers recompute on theme change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [theme]);
}

// hhmm formats an ISO timestamp as "HH:MM" for compact X-axis labels.
export function hhmm(iso: string): string {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
