// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useKeyAttributes is a process-wide cache of the union of "key
// attributes" declared by every service facet. It loads once via
// /service-facets and returns an ordered list (preserving each facet's
// declared order, but with each key only appearing once).
//
// Hooks consume it to decide which attribute chips to surface on
// search results, recent-spans rows, and the trace tree — without
// having to look up each span's facet classification.

import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";

type Cache = { keys: string[]; bySlug: Record<string, string[]> } | null;

let cache: Cache = null;
let inflight: Promise<Cache> | null = null;

async function load(): Promise<Cache> {
  if (cache) return cache;
  if (inflight) return inflight;
  inflight = api
    .listServiceFacets()
    .then((r) => {
      const seen = new Set<string>();
      const keys: string[] = [];
      const bySlug: Record<string, string[]> = {};
      for (const t of r.facets ?? []) {
        const tKeys = t.key_attributes ?? [];
        bySlug[t.slug] = tKeys;
        for (const k of tKeys) {
          if (!seen.has(k)) {
            seen.add(k);
            keys.push(k);
          }
        }
      }
      cache = { keys, bySlug };
      return cache;
    })
    .catch(() => {
      cache = { keys: [], bySlug: {} };
      return cache;
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

/**
 * useKeyAttributes returns the ordered union of key attribute keys
 * declared by every service type, cached after first call. Returns
 * an empty list until the fetch resolves.
 */
export function useKeyAttributes(): string[] {
  const [keys, setKeys] = useState<string[]>(cache?.keys ?? []);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    if (cache) {
      setKeys(cache.keys);
      return () => {
        mounted.current = false;
      };
    }
    load().then((c) => {
      if (mounted.current && c) setKeys(c.keys);
    });
    return () => {
      mounted.current = false;
    };
  }, []);
  return keys;
}

/**
 * pickKeyEntries returns the (key, value) pairs from a span's
 * attribute map whose keys appear in the registered key-attribute
 * list. Preserves the registry's declared order so the most
 * important attribute (e.g. file.name) shows first.
 */
export function pickKeyEntries(
  attributes: Record<string, string> | undefined,
  keys: string[]
): [string, string][] {
  if (!attributes) return [];
  const out: [string, string][] = [];
  for (const k of keys) {
    const v = attributes[k];
    if (v) out.push([k, v]);
  }
  return out;
}
