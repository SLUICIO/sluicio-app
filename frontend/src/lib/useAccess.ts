// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useAccess — the frontend mirror of the scoped-manage model (RBAC v2
// §5). Where useCurrentUser answers "what does my ORG ROLE allow", this
// answers "which services do I MANAGE" for org-viewers who are editors
// in a group. The server gates stay authoritative; this only drives
// which affordances render.
//
// Fetched once per session (module-level cache) — group membership
// changes take a reload to reflect, same as role changes.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { MeAccess } from "../api/types";

let cached: MeAccess | null = null;
let inflight: Promise<MeAccess> | null = null;

function fetchAccess(): Promise<MeAccess> {
  if (cached) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api
      .meAccess()
      .then((a) => {
        cached = a;
        return a;
      })
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

export interface Access {
  loading: boolean;
  // May create scoped resources (integrations/systems/dashboards…) at
  // all — org editor+ OR editor in any group.
  writeAnywhere: boolean;
  // Manages every service (org editor/admin or all_org editor policy).
  manageAll: boolean;
  // Groups where the caller holds an editor+ role (dashboard picker etc.).
  editorGroups: { id: string; slug: string; name: string }[];
  // Scoped-manage check for one service.
  canManageService: (name: string) => boolean;
}

export function useAccess(): Access {
  const [state, setState] = useState<MeAccess | null>(cached);

  useEffect(() => {
    if (state) return;
    let cancelled = false;
    fetchAccess()
      .then((a) => !cancelled && setState(a))
      .catch(() => !cancelled && setState({ write_anywhere: false, manage_all: false, managed_services: [], editor_groups: [] }));
    return () => {
      cancelled = true;
    };
  }, [state]);

  const managed = new Set(state?.managed_services ?? []);
  return {
    loading: state === null,
    writeAnywhere: state?.write_anywhere ?? false,
    manageAll: state?.manage_all ?? false,
    editorGroups: state?.editor_groups ?? [],
    canManageService: (name: string) => (state?.manage_all ?? false) || managed.has(name),
  };
}
