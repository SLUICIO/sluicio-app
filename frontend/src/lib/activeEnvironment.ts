// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environment an integration page reads (issue #40): one
// integration, read for all environments or for one. It lives in the URL
// (?env=production) so a shared link opens on the same environment, and
// the API client stamps it onto every read of integration data while an
// integration page is on screen (EnvironmentScope sets it). An
// integration page with no ?env opens on the default environment
// (production unless an operator changed it) when the integration has
// it; "All environments" chosen there is ?env=*. Elsewhere the absence of
// the parameter is all environments.

import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

/** How ?env= asks for telemetry that carries no environment. */
export const NO_ENVIRONMENT_PARAM = "(none)";

/** How ?env= says "all environments" where its absence would mean the
 *  default one. */
export const ALL_ENVIRONMENTS_PARAM = "*";

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
  if (p === "" || p === ALL_ENVIRONMENTS_PARAM) return null;
  return p === NO_ENVIRONMENT_PARAM ? "" : p;
}

/** ?env=, as [environment, set, explicitAll]. set(null) returns to all
 *  environments by dropping the parameter; set(null, true) writes ?env=*,
 *  which an integration page needs so it does not open on the default. */
export function useEnvironmentParam(): [string | null, (env: string | null, explicitAll?: boolean) => void, boolean] {
  const [params, setParams] = useSearchParams();
  const env = envFromParam(params.get("env"));
  const set = useCallback(
    (next: string | null, explicitAll = false) => {
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          if (next !== null) p.set("env", envToParam(next));
          else if (explicitAll) p.set("env", ALL_ENVIRONMENTS_PARAM);
          else p.delete("env");
          return p;
        },
        { replace: false },
      );
    },
    [setParams],
  );
  return [env, set, params.get("env") === ALL_ENVIRONMENTS_PARAM];
}

/** The ?env= part of a link, so moving between an integration's tabs or
 *  back to the list keeps the environment. "" when reading all, unless
 *  all was chosen explicitly (then ?env=*, so the next page does not open
 *  on the default). */
export function envQuery(env: string | null, explicitAll = false): string {
  if (env === null) return explicitAll ? `?env=${ALL_ENVIRONMENTS_PARAM}` : "";
  return `?env=${encodeURIComponent(envToParam(env))}`;
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
