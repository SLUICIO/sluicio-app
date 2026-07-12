// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useUserPreference — a small JSON preference persisted per (user, org)
// on the server, so layouts follow the account across browsers and
// machines. Reads once on mount; writes fire-and-forget (a failed save
// costs a preference, never the page). `loaded` tells the caller when
// the initial fetch settled so defaults don't flash-overwrite a stored
// value.

import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";

export function useUserPreference<T>(
  key: string,
): { value: T | null; save: (v: T | null) => void; loaded: boolean } {
  const [value, setValue] = useState<T | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api
      .getPreference<T>(key)
      .then((r) => {
        if (!cancelled) setValue(r.value);
      })
      .catch(() => {
        /* no preference / offline — defaults apply */
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [key]);

  const save = useCallback(
    (v: T | null) => {
      setValue(v);
      api.putPreference(key, v).catch(() => {
        /* fire-and-forget — the in-memory value still applies */
      });
    },
    [key],
  );

  return { value, save, loaded };
}
