// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Why a trace id leads nowhere. A log names the trace it was written
// in whether or not that trace was ever sent here, and traces can be
// kept for less time than logs. The answer is read from the 404 the
// trace endpoint gives, so it costs nothing until a trace is missing.

/** How long traces are kept, from a failed trace read, when it says. */
export function retentionDaysOf(err: unknown): number | undefined {
  const e = err as { status?: number; body?: { trace_retention_days?: unknown } } | null;
  if (!isMissingTrace(err)) return undefined;
  const days = e?.body?.trace_retention_days;
  return typeof days === "number" && days > 0 ? days : undefined;
}

/** Whether a failed trace read means the trace is not there, as
 *  against a trace with no spans of the integration it was opened in. */
export function isMissingTrace(err: unknown): boolean {
  const e = err as { status?: number; body?: { missing?: unknown } } | null;
  return e?.status === 404 && e.body?.missing === true;
}

/**
 * "expired" when the thing that named the trace (a log) is older than
 * traces are kept, so the trace has most likely been deleted; otherwise
 * "unknown", and the page lists the likely reasons.
 */
export function missingTraceReason(seenAt: string | undefined, retentionDays: number | undefined, now = new Date()): "expired" | "unknown" {
  if (!seenAt || !retentionDays) return "unknown";
  const seen = new Date(seenAt).getTime();
  if (Number.isNaN(seen)) return "unknown";
  return now.getTime() - seen > retentionDays * 24 * 60 * 60 * 1000 ? "expired" : "unknown";
}
