// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Small formatting helpers shared across pages.

export function formatRelative(iso: string): string {
  const d = new Date(iso);
  const then = d.getTime();
  const now = Date.now();
  const diffSec = Math.max(0, Math.round((now - then) / 1000));
  if (diffSec < 5) return "just now";
  if (diffSec < 60) return `${diffSec} seconds ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin} min ago`;
  const diffHour = Math.round(diffMin / 60);
  if (diffHour < 24) return `${diffHour} h ago`;
  const diffDay = Math.round(diffHour / 24);
  if (diffDay < 30) return `${diffDay} d ago`;
  // Beyond a month, an absolute date reads more honestly than
  // "180 d ago". Same year drops the year for compactness.
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  });
}

// formatDateTime is a compact absolute timestamp for trace/message LISTS —
// date + time, dropping the year unless it differs from the current one, in
// 24-hour form for density. Lists span days, so a bare clock time is
// ambiguous; within a single trace, time-only is fine (see TraceDetail).
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function formatNumber(n: number): string {
  return n.toLocaleString();
}

// Human-readable byte size (base-1024): 0 B, 4.2 KB, 1.3 GB, …
export function formatBytes(n: number): string {
  if (!n || n < 1) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return `${v.toFixed(i === 0 ? 0 : v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`;
}

// Byte-valued metric units — OTLP "By", plain "bytes", or the annotation
// form "{bytes}" (RabbitMQ's disk_free/mem_used use this). Lowercase "b"
// alone is bits, so we deliberately don't match it.
const BYTE_UNITS = new Set(["by", "byte", "bytes"]);
export function isByteUnit(unit?: string): boolean {
  if (!unit) return false;
  const u = unit.trim().toLowerCase().replace(/^\{|\}$/g, "");
  return BYTE_UNITS.has(u);
}

// Compact value for a metric stat cell. Byte-unit metrics render as sizes
// (16.9 GB); other large values use compact notation (18.1M) so a big
// number like rabbitmq.node.disk_free doesn't overflow the panel. Small
// values keep two decimals.
export function formatMetricValue(n: number, unit?: string): string {
  if (!Number.isFinite(n)) return "—";
  if (isByteUnit(unit)) return formatBytes(n);
  if (Math.abs(n) >= 100_000) {
    return n.toLocaleString(undefined, { notation: "compact", maximumFractionDigits: 2 });
  }
  return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

export function formatDurationMs(ms: number): string {
  if (ms < 1) return `${ms.toFixed(2)} ms`;
  if (ms < 1000) return `${ms.toFixed(0)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

// Human-readable duration from a count of SECONDS: "45 s", "12 min",
// "3 h 20 min", "5 d 2 h". Used for the "age" aggregation (seconds since a
// file was last modified) so a staleness reading reads as "2 h" not "7200".
export function formatDurationSeconds(totalSec: number): string {
  const s = Math.max(0, Math.round(totalSec));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) {
    const rs = s % 60;
    return rs ? `${m} min ${rs} s` : `${m} min`;
  }
  const h = Math.floor(m / 60);
  if (h < 24) {
    const rm = m % 60;
    return rm ? `${h} h ${rm} min` : `${h} h`;
  }
  const d = Math.floor(h / 24);
  const rh = h % 24;
  return rh ? `${d} d ${rh} h` : `${d} d`;
}

// A metric whose VALUE is a Unix-epoch-seconds timestamp — e.g. file.mtime
// from the filestats receiver: unit is seconds and the name ends in a *time
// field. Such a value should render as a date, not a raw epoch number.
const TIMESTAMP_METRIC_NAME = /\.(mtime|ctime|atime|time)$/i;
export function isTimestampMetric(unit?: string, name?: string): boolean {
  if (!name || !TIMESTAMP_METRIC_NAME.test(name)) return false;
  const u = (unit ?? "").toLowerCase();
  return u === "s" || u === "sec" || u === "seconds";
}

// isSecondsUnit reports whether a display unit is seconds — used to render a
// value (e.g. an "age" health-check reading) as a duration.
export function isSecondsUnit(unit?: string): boolean {
  const u = (unit ?? "").toLowerCase();
  return u === "s" || u === "sec" || u === "seconds";
}

// Render a Unix-epoch-seconds value as "Jul 1, 23:16:42 · 2 min ago". Falls
// back to the raw number when it's outside a plausible epoch range, so a
// mis-tagged metric never shows a nonsensical date. Use in roomy contexts
// (detail drawer, tooltips); for dense rows use the compact variant below.
export function formatTimestampSeconds(epochSec: number): string {
  // ~2001-09-09 (1e9) … ~2200 (7258118400); outside this it's not an epoch.
  if (!(epochSec >= 1_000_000_000 && epochSec <= 7_258_118_400)) {
    return epochSec.toLocaleString();
  }
  const iso = new Date(epochSec * 1000).toISOString();
  return `${formatDateTime(iso)} · ${formatRelative(iso)}`;
}

// Compact epoch rendering for dense lists — just the relative "2 min ago" /
// "3 h ago" / short date. Short enough to fit a narrow value cell; the full
// absolute timestamp belongs in a tooltip / the detail drawer.
export function formatTimestampSecondsCompact(epochSec: number): string {
  if (!(epochSec >= 1_000_000_000 && epochSec <= 7_258_118_400)) {
    return epochSec.toLocaleString();
  }
  return formatRelative(new Date(epochSec * 1000).toISOString());
}

export function statusLabel(status: "ok" | "errors" | "quiet" | "unhealthy"): string {
  switch (status) {
    case "ok":
      return "Receiving data";
    case "errors":
      return "Errors detected";
    case "quiet":
      return "Quiet";
    case "unhealthy":
      return "Unhealthy";
  }
}
