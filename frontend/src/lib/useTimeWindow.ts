// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useTimeWindow is the app-wide hook for the time-range selector that
// lives in the AppShell header. The value is a string in one of two
// shapes:
//
//   - Relative: a duration like "5m", "15m", "1h", "24h", "7d".
//   - Absolute: two ISO 8601 timestamps separated by a slash, e.g.
//     "2026-05-12T05:15:00.000Z/2026-05-12T16:15:00.000Z".
//
// Sources, in priority order:
//   1) ?range= in the URL — the canonical, shareable source of truth.
//   2) sessionStorage (per-tab fallback) so the range you picked on
//      page A carries to page B when you click an internal Link that
//      doesn't include ?range=. Cleared when you reset to default.
//   3) DEFAULT_WINDOW.
//
// The legacy ?window=... param is read as a back-compat fallback for
// bookmarks created before the rename.

import { useSearchParams } from "react-router-dom";

export const DEFAULT_WINDOW = "1h";
const STORAGE_KEY = "conduit:range";

function readSession(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeSession(next: string | null) {
  if (typeof window === "undefined") return;
  try {
    if (next === null) window.sessionStorage.removeItem(STORAGE_KEY);
    else window.sessionStorage.setItem(STORAGE_KEY, next);
  } catch {
    /* private mode / disabled storage — silently fine */
  }
}

export function useTimeWindow(): [string, (next: string) => void] {
  const [params, setParams] = useSearchParams();
  const fromUrl = params.get("range") || params.get("window");
  const current = fromUrl || readSession() || DEFAULT_WINDOW;

  const setWindow = (next: string) => {
    const updated = new URLSearchParams(params);
    updated.delete("window");
    if (!next || next === DEFAULT_WINDOW) {
      updated.delete("range");
      writeSession(null);
    } else {
      updated.set("range", next);
      writeSession(next);
    }
    setParams(updated, { replace: false });
  };

  return [current, setWindow];
}

// isAbsolute reports whether the range value is an ISO/ISO interval
// rather than a relative duration like "1h".
export function isAbsolute(value: string): boolean {
  return value.includes("/");
}
