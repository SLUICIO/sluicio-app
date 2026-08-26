// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where this cell's telemetry should be sent, resolved once and shared.
//
// Three surfaces already worked this out for themselves, and a fourth —
// the Services empty state — did not: it told every reader to point their
// collector at `http://localhost:4318/v1/traces`. True on a laptop, and
// actively misleading on every deployed cell, where it is advice that
// cannot possibly work and that the reader has no way to know is wrong.
//
// Precedence, matching what the Settings page documents:
//   1. ingest_base_url — SLUICIO_INGEST_URL, or the admin-set cell setting
//   2. the browser origin — a guess, but a reachable one, and it is at
//      least this deployment rather than someone else's laptop
//
// Cached in module scope: it is the same answer for every consumer, it
// changes only when an admin edits the setting, and an empty state should
// not cost a request per render.

import { useEffect, useState } from "react";
import { api } from "../api/client";

let cached: string | null = null;
let inflight: Promise<string> | null = null;
const listeners = new Set<(v: string) => void>();

function originFallback(): string {
  return typeof window !== "undefined" ? window.location.origin : "";
}

function load(): Promise<string> {
  if (cached !== null) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api
      .getSystemSettings()
      .then((s) => {
        // Trailing slashes are stripped so callers can append a path
        // without producing "https://host//v1/traces", which some
        // collectors send verbatim and some servers reject.
        const v = (s.ingest_base_url || originFallback()).replace(/\/+$/, "");
        cached = v;
        listeners.forEach((fn) => fn(v));
        return v;
      })
      .catch(() => originFallback())
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/**
 * The cell's OTLP/HTTP base URL, e.g. "https://ingest.acme.example.com".
 *
 * Starts at the browser origin rather than empty: a snippet that renders
 * blank and then fills in reads as broken, and the origin is the same
 * answer this hook falls back to anyway.
 */
export function useIngestBaseUrl(): string {
  const [v, setV] = useState<string>(cached ?? originFallback());
  useEffect(() => {
    let live = true;
    const fn = (next: string) => {
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

/** The full OTLP/HTTP endpoint for one signal, e.g. base + "/v1/traces". */
export function otlpEndpoint(base: string, signal: "traces" | "logs" | "metrics"): string {
  return `${base}/v1/${signal}`;
}
