// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How drift reads: a collector that no longer runs everything last rolled
// out to it, or whose configuration changed outside Sluicio since. The
// server keeps paths only, never values.

import type { CollectorDrift } from "../api/types";

export function isDrifted(d?: CollectorDrift | null): boolean {
  return !!d && d.paths.length > 0;
}

// driftSummary is the sentence for the collector's page, or "" when in
// sync or not applicable.
export function driftSummary(d?: CollectorDrift | null, shown = 5): string {
  if (!isDrifted(d)) return "";
  const words: Record<string, string> = {
    missing: "is missing",
    changed: "differs",
    added: "was added",
    removed: "was removed",
  };
  const parts = d!.paths
    .slice(0, shown)
    .map((p) => `${p.path} ${words[p.kind] ?? "differs"}`);
  const more = d!.paths.length - shown;
  return (
    `The collector's configuration changed since it was last rolled out from Sluicio: ${parts.join("; ")}` +
    (more > 0 ? `, and ${more} more` : "") +
    "."
  );
}
