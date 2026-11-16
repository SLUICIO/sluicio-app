// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { isMissingTrace, missingTraceReason, retentionDaysOf } from "./missingTrace";

describe("missing trace", () => {
  const notFound = { status: 404, body: { error: { message: "no spans found for this trace" }, missing: true, trace_retention_days: 14 } };

  it("reads the retention from a 404 and nothing else", () => {
    expect(isMissingTrace(notFound)).toBe(true);
    expect(retentionDaysOf(notFound)).toBe(14);
    expect(retentionDaysOf({ status: 500, body: { trace_retention_days: 14 } })).toBeUndefined();
    expect(retentionDaysOf({ status: 404, body: { missing: true } })).toBeUndefined();
    expect(isMissingTrace(new Error("network"))).toBe(false);
    // A trace with no spans of the integration it was opened in exists.
    expect(isMissingTrace({ status: 404, body: { error: { message: "no spans of this integration in this trace" } } })).toBe(false);
  });

  it("calls a trace expired only when what named it is older than traces are kept", () => {
    const now = new Date("2026-11-16T12:00:00Z");
    expect(missingTraceReason("2026-11-01T12:00:00Z", 14, now)).toBe("expired");
    expect(missingTraceReason("2026-11-10T12:00:00Z", 14, now)).toBe("unknown");
    expect(missingTraceReason(undefined, 14, now)).toBe("unknown");
    expect(missingTraceReason("2026-11-01T12:00:00Z", undefined, now)).toBe("unknown");
  });
});
