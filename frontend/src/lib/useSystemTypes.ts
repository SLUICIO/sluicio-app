// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The system types this cell actually has.
//
// SYSTEM_KINDS is a table compiled into the bundle, and it drifted: it
// offered eight kinds no system could be, and omitted two that exist. It
// also cannot know about a type an org defined for its own estate, which
// is the one somebody is most likely to be looking at.
//
// Fetched once and cached in module scope, because it is the same answer
// for everyone in the cell and it changes only when an admin edits the
// system types.
//
// The static list stays as the fallback rather than being deleted: a
// label is chrome, and a failed request should degrade to the label it
// used to show rather than to a raw key like `azure-servicebus`.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import { SYSTEM_KINDS, systemKindLabel } from "./systemKinds";

export interface SystemKindOption {
  value: string;
  label: string;
}

let cached: SystemKindOption[] | null = null;
let inflight: Promise<SystemKindOption[]> | null = null;
const listeners = new Set<(v: SystemKindOption[]) => void>();

function load(): Promise<SystemKindOption[]> {
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api
      .listSystemTypes()
      .then((r) => {
        const rows = (r.system_types ?? []).map((t) => ({ value: t.key, label: t.label }));
        rows.sort((a, b) => a.label.localeCompare(b.label));
        // An empty list means the cell genuinely has no types, which is
        // possible but indistinguishable here from a half-answer. Keep
        // the built-ins rather than emptying a picker.
        const v = rows.length > 0 ? rows : SYSTEM_KINDS;
        cached = v;
        listeners.forEach((fn) => fn(v));
        return v;
      })
      .catch(() => SYSTEM_KINDS)
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** The cell's system types, starting from the built-ins while loading. */
export function useSystemTypes(): SystemKindOption[] {
  const [v, setV] = useState<SystemKindOption[]>(cached ?? SYSTEM_KINDS);
  useEffect(() => {
    let live = true;
    const fn = (next: SystemKindOption[]) => {
      if (live) setV(next);
    };
    listeners.add(fn);
    load().then(fn);
    return () => {
      live = false;
      listeners.delete(fn);
    };
  }, []);
  return v;
}

/**
 * A labelling function for a system kind.
 *
 * A hook rather than a plain function because the answer arrives from the
 * server: the static systemKindLabel returns the raw key for anything it
 * has not heard of, so a service of a newer or org-defined type was
 * badged `dotnet-service` instead of ".NET service".
 */
export function useSystemKindLabel(): (kind: string | undefined) => string {
  const types = useSystemTypes();
  return (kind) => {
    if (!kind) return "System";
    return types.find((t) => t.value === kind)?.label ?? systemKindLabel(kind);
  };
}
