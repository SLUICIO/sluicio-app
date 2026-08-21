// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What the caller can reach (issue #30), shared.
//
// Fetched once per page load and cached in module scope: it is the same
// answer for every component that asks, and several ask on the first
// paint. A hook that refetched per consumer would put a request behind
// every service name in a list.
//
// A HINT, never a gate. The server decides access; this decides what is
// worth offering. Its only jobs here are to stop the navigation
// advertising empty pages and to stop a list rendering a link that leads
// to "service not found" — a dead link is a worse answer than plain
// text, because it invites the click that produces the error.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { NavigationReach } from "../api/types";

let cached: NavigationReach | null = null;
let inflight: Promise<NavigationReach | null> | null = null;
const listeners = new Set<(r: NavigationReach | null) => void>();

function load(): Promise<NavigationReach | null> {
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api
      .getNavigationReach()
      .then((r) => {
        cached = r;
        listeners.forEach((fn) => fn(r));
        return r;
      })
      // A failure leaves it null, which every consumer reads as "offer
      // everything". The pages are gated server-side, so the cost is an
      // empty page rather than anything getting out.
      .catch(() => null)
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** The caller's reach, or null while loading / on failure. */
export function useNavigationReach(): NavigationReach | null {
  const [r, setR] = useState<NavigationReach | null>(cached);
  useEffect(() => {
    let live = true;
    const fn = (next: NavigationReach | null) => {
      if (live) setR(next);
    };
    listeners.add(fn);
    load().then(fn);
    return () => {
      live = false;
      listeners.delete(fn);
    };
  }, []);
  return r;
}

/**
 * Whether a service page is worth linking to.
 *
 * Defaults to true while unknown: a link that briefly appears and then
 * settles is better than a name that flickers into a link, and the
 * server refuses the page either way.
 */
export function useCanOpenServices(): boolean {
  const reach = useNavigationReach();
  return reach ? reach.services : true;
}
