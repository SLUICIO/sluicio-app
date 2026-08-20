// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The cell's mark (issue #29).
//
// Fetched once per page load and cached in module scope, because it is
// the same answer for every user in the cell and it is needed by the
// very first thing that renders. A hook that refetched per component
// would put a request behind the logo.
//
// The server returns the Sluicio default whenever the cell is not
// entitled, whatever is stored, so nothing here checks a licence: if a
// mark comes back, it is licensed.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Branding } from "../api/types";

let cached: Branding | null = null;
let inflight: Promise<Branding | null> | null = null;
const listeners = new Set<(b: Branding | null) => void>();

function load(): Promise<Branding | null> {
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api
      .getBranding()
      .then((b) => {
        cached = b;
        listeners.forEach((fn) => fn(b));
        return b;
      })
      // A failure leaves the Sluicio lockup in place, which is the right
      // fallback: it is ours, and it is never wrong in the way a
      // half-loaded partner brand would be.
      .catch(() => null)
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** The cell's branding, or null while loading / when unbranded. */
export function useBranding(): Branding | null {
  const [b, setB] = useState<Branding | null>(cached);
  useEffect(() => {
    let live = true;
    const fn = (next: Branding | null) => {
      if (live) setB(next);
    };
    listeners.add(fn);
    load().then(fn);
    return () => {
      live = false;
      listeners.delete(fn);
    };
  }, []);
  return b;
}

/** Invalidate after an operator saves, so the shell repaints without a reload. */
export function refreshBranding(): void {
  cached = null;
  load();
}

/**
 * The mark to draw for the current colour scheme.
 *
 * The dark variant is optional because a mark that reads on both is
 * common, and requiring two would make the simple case harder for no
 * gain. Falls back to the light one.
 */
export function brandingLogoFor(b: Branding): string {
  if (!b.logo_dark) return b.logo;
  const dark =
    document.documentElement.dataset.theme === "dark" ||
    (!document.documentElement.dataset.theme &&
      window.matchMedia?.("(prefers-color-scheme: dark)").matches);
  return dark ? b.logo_dark : b.logo;
}

/**
 * Apply the favicon and the tab-title product name.
 *
 * Done imperatively against <head> rather than through a component,
 * because both live outside the React tree and there is exactly one of
 * each.
 */
export function applyBrandingToDocument(b: Branding | null): void {
  if (!b) return;
  if (b.favicon) {
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    link.href = b.favicon;
  }
  if (b.wordmark) {
    // Titles are "<page> · Sluicio"; swap only the product half so the
    // page's own name survives.
    document.title = document.title.replace(/Sluicio$/, b.wordmark);
  }
}
