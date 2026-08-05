// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Nudging the Errors nav pill to refetch after something changes the
// count.
//
// The pill polls on a slow interval so it stays current without
// hammering the errors feed. That is right for drift — a check firing or
// clearing on its own — but wrong for the actor: acknowledge or resolve
// an error and the number sat stale for up to a minute, which reads as
// "the page needs a reload" rather than "the poll has not come round".
//
// Same shape as announcementsChanged: a window event rather than context
// or prop drilling, because the pages that mutate error state (the
// Errors page, an integration's Errors tab, a service's health list) are
// nowhere near AppShell in the tree, and none of them should have to
// know the shell exists.

const ERROR_COUNTS_CHANGED = "sluicio:error-counts-changed";

/**
 * Call after any mutation that can change the failing-check count —
 * acknowledging, resolving, or deleting a check.
 *
 * Pass the new count when the caller already has it (the Errors page
 * fetches the same feed, so it does) and the listener uses it directly
 * instead of asking the server again. Omit it and the listener refetches;
 * a refetch that returns the same number is harmless.
 */
export function errorCountsChanged(failingChecks?: number) {
  window.dispatchEvent(
    new CustomEvent(ERROR_COUNTS_CHANGED, { detail: { failingChecks } }),
  );
}

/**
 * Subscribes to the nudge; returns the unsubscribe.
 *
 * The handler receives the count when one travelled with the event, and
 * undefined when the caller did not know it.
 */
export function onErrorCountsChanged(
  fn: (failingChecks?: number) => void,
): () => void {
  const handler = (e: Event) => {
    const detail = (e as CustomEvent<{ failingChecks?: number }>).detail;
    fn(detail?.failingChecks);
  };
  window.addEventListener(ERROR_COUNTS_CHANGED, handler);
  return () => window.removeEventListener(ERROR_COUNTS_CHANGED, handler);
}
