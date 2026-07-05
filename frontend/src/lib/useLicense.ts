// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useLicense exposes the Enterprise license status to the UI so components
// can gate features + show upgrade prompts. The status is fetched once and
// shared across every caller (module-level cache) — it changes rarely, so a
// single fetch per page load is plenty. Call refreshLicense() after pasting a
// new key to invalidate.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LicenseFeature, LicenseStatus } from "../api/types";

let cache: LicenseStatus | null = null;
let inflight: Promise<LicenseStatus> | null = null;
const subscribers = new Set<(s: LicenseStatus) => void>();

function load(): Promise<LicenseStatus> {
  if (inflight) return inflight;
  inflight = api
    .license()
    .then((s) => {
      cache = s;
      subscribers.forEach((cb) => cb(s));
      return s;
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

// refreshLicense forces a re-fetch (e.g. after a key is saved) and notifies
// every mounted useLicense().
export function refreshLicense(): Promise<LicenseStatus> {
  cache = null;
  return load();
}

export function useLicense(): { status: LicenseStatus | null; loading: boolean } {
  const [status, setStatus] = useState<LicenseStatus | null>(cache);
  const [loading, setLoading] = useState(cache === null);

  useEffect(() => {
    let active = true;
    const cb = (s: LicenseStatus) => {
      if (active) {
        setStatus(s);
        setLoading(false);
      }
    };
    subscribers.add(cb);
    if (cache) {
      setStatus(cache);
      setLoading(false);
    } else {
      load().catch(() => active && setLoading(false));
    }
    return () => {
      active = false;
      subscribers.delete(cb);
    };
  }, []);

  return { status, loading };
}

// useFeature is the common case: "is this Enterprise feature on?".
export function useFeature(feature: LicenseFeature): boolean {
  const { status } = useLicense();
  return status?.features?.[feature] ?? false;
}
