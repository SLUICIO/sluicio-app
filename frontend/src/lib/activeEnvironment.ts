// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environment an integration page reads (issue #40): one
// integration, read for all environments or for one. It lives in the URL
// (?env=production) so a shared link opens on the same environment, and
// the API client stamps it onto every read of integration data while an
// integration page is on screen (EnvironmentScope sets it). "All
// environments" is the absence of the parameter.

import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

/** How ?env= asks for telemetry that carries no environment. */
export const NO_ENVIRONMENT_PARAM = "(none)";

let current: string | null = null;

/** The environment reads are narrowed to: a name, "" for "(no
 *  environment)", or null for all environments. */
export function getActiveEnvironment(): string | null {
  return current;
}

export function setActiveEnvironment(env: string | null): void {
  current = env;
}

/** How an environment reads to people. */
export function environmentLabel(env: string): string {
  return env === "" ? "(no environment)" : env;
}

export function envToParam(env: string): string {
  return env === "" ? NO_ENVIRONMENT_PARAM : env;
}

export function envFromParam(p: string | null): string | null {
  if (p === null || p === undefined) return null;
  if (p === "") return null;
  return p === NO_ENVIRONMENT_PARAM ? "" : p;
}

/** ?env=, as [environment, set]. set(null) returns to all environments. */
export function useEnvironmentParam(): [string | null, (env: string | null) => void] {
  const [params, setParams] = useSearchParams();
  const env = envFromParam(params.get("env"));
  const set = useCallback(
    (next: string | null) => {
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          if (next === null) p.delete("env");
          else p.set("env", envToParam(next));
          return p;
        },
        { replace: false },
      );
    },
    [setParams],
  );
  return [env, set];
}

/** The ?env= part of a link, so moving between an integration's tabs or
 *  back to the list keeps the environment. "" when reading all. */
export function envQuery(env: string | null): string {
  return env === null ? "" : `?env=${encodeURIComponent(envToParam(env))}`;
}

/** Whether a request reads integration data and so follows the active
 *  environment. Writes never do: an environment narrows what is read,
 *  never what is changed. */
export function readsIntegrationData(path: string, method: string): boolean {
  if (method !== "GET" && !(method === "POST" && path.startsWith("/messages/search"))) return false;
  return (
    path.startsWith("/integrations") ||
    path.startsWith("/messages/search") ||
    path.startsWith("/logs") ||
    path.startsWith("/log-") ||
    path.startsWith("/metric-") ||
    // The Errors tab reads the org feed and filters it to the integration.
    path.startsWith("/errors") ||
    path.includes("integration=")
  );
}
