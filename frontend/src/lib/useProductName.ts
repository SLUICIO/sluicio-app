// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What this deployment calls itself.
//
// A white-label partner replaces the mark and the wordmark, and then met
// "Sluicio" in ninety-odd sentences of help text: "How Sluicio detects
// facets", "Choose how Sluicio looks", "searchable across Sluicio". The
// logo was theirs and every explanation of what the logo stood for was
// ours.
//
// Reads the branding the shell already fetched, so this costs nothing
// beyond the request that draws the mark.

import { useBranding, useLoginBranding } from "./useBranding";

/** The Sluicio default, and the fallback whenever nothing is branded. */
export const DEFAULT_PRODUCT_NAME = "Sluicio";

/**
 * The product's name in running text.
 *
 * Deliberately NOT used for the Enterprise licence notices. "This is a
 * Sluicio Enterprise feature" cannot become "This is an Acme Enterprise
 * feature": that names a product nobody sells and nobody can buy. Those
 * strings drop the vendor instead and say "an Enterprise feature", which
 * is true whoever is reading and leaks nothing either way.
 */
export function useProductName(): string {
  // Both sources, because there are two and the page-title hook runs on
  // BOTH sides of sign-in. The authenticated read is the shell's; the
  // public one is the login screen's. Reading only the first left the
  // browser tab saying "Sign in · Sluicio" over a page that said "Sign in
  // to Maxbo Insight" — the one place the two disagreed, and the first
  // thing a visitor sees.
  //
  // Costs one extra cached GET per page load inside the app. Hooks cannot
  // be called conditionally, so the alternative is a race against
  // whichever cache happened to fill first.
  const shell = useBranding();
  const login = useLoginBranding();
  return shell?.wordmark || login?.wordmark || DEFAULT_PRODUCT_NAME;
}
