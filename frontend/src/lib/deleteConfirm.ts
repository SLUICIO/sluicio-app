// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What a delete confirmation says about the health checks bound to the
// thing being deleted.
//
// They are deleted with it (left behind unbound, a check would evaluate
// over every service in the org), so the confirmation has to say so, and
// say how many: "delete this integration" reads as one thing, and the
// click also removes alerting someone set up and may be relying on.
//
// The count comes from the server before the dialog opens. When it could
// not be fetched the dialog still has to warn, just without the number,
// rather than quietly falling back to the old wording.

/** The sentence about bound health checks, or "" when there are none. */
export function boundChecksSentence(count: number | null): string {
  if (count === null) return "Any health checks bound to it are deleted with it.";
  if (count === 0) return "";
  if (count === 1) return "Its 1 health check is deleted with it.";
  return `Its ${count} health checks are deleted with it.`;
}

/** The full confirmation for deleting an integration. */
export function integrationDeleteConfirm(name: string, checks: number | null): string {
  return joinSentences(`Delete integration "${name}"? Its matchers are removed.`, boundChecksSentence(checks));
}

/**
 * The full confirmation for deleting a system. Its member services are
 * only detached, and THEIR checks stay: only the checks bound to the
 * system itself go.
 */
export function systemDeleteConfirm(name: string, checks: number | null): string {
  return joinSentences(
    `Delete system "${name}"? Its services are detached and keep their own health checks.`,
    boundChecksSentence(checks),
  );
}

function joinSentences(...parts: string[]): string {
  return parts.filter(Boolean).join(" ");
}
