// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Comparing collector versions in the browser (issue #16).
//
// The backend owns the naming table; this exists only so a snippet
// rendered client-side can pick the right spelling without a round trip
// per keystroke. It deliberately mirrors the Go implementation's two
// awkward rules, because a disagreement between them would produce YAML
// that the server believes is correct and the user's collector rejects:
//
//   - Compare NUMERICALLY. String comparison puts "0.9.0" above
//     "0.146.0", which is exactly the range the one rename we care about
//     lives in.
//   - An UNPARSEABLE version is not new. Guessing "recent" on something
//     we could not read would emit the newest syntax to the one customer
//     whose setting we failed to understand.

/** Parse "0.146.0", "v0.146.0" or "0.146" into comparable parts. */
function parts(v: string): [number, number, number] | null {
  const raw = v.trim().replace(/^v/, "").split(".");
  if (raw.length < 2) return null;
  const out: [number, number, number] = [0, 0, 0];
  for (let i = 0; i < 3; i++) {
    if (i >= raw.length) continue;
    const n = Number(raw[i]);
    if (!Number.isInteger(n) || n < 0) return null;
    out[i] = n;
  }
  return out;
}

/**
 * Whether `version` is at least `min`.
 *
 * Returns false for anything unparseable: an unreadable version is not
 * evidence of being new, and the older component name is the one that
 * works on a wider range of collectors.
 */
export function versionAtLeast(version: string, min: string): boolean {
  const a = parts(version);
  const b = parts(min);
  if (!a || !b) return false;
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] > b[i];
  }
  return true;
}

/** Whether a version string is well-formed enough to reason about. */
export function isValidVersion(v: string): boolean {
  return parts(v) !== null;
}
