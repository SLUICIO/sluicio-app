// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environment an integration page reads (issue #40): one
// integration, read for all environments or for one. It lives in the URL
// (?env=production) so a shared link opens on the same environment, and
// the API client stamps it onto every read of integration data while an
// integration page is on screen (EnvironmentScope sets it). An
// integration page with no ?env opens on its production environments
// (?env=prod-eu&env=prod-us), unless an operator set pages to open on all;
// "All environments" chosen there is ?env=*. Elsewhere the absence of the
// parameter is all environments.
//
// Which environments are production is a role (the settings list), not a
// name: "@production" stands for all of them in a dashboard's, a check's
// or a notification profile's list of environments.

import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

/** How ?env= asks for telemetry that carries no environment. */
export const NO_ENVIRONMENT_PARAM = "(none)";

/** How ?env= says "all environments" where its absence would mean the
 *  default one. */
export const ALL_ENVIRONMENTS_PARAM = "*";

/** "Every production environment", in a list of environments. */
export const PRODUCTION_TOKEN = "@production";

let current: string[] | null = null;

/** The environments reads are narrowed to: names, "" for "(no
 *  environment)", or null for all environments. */
export function getActiveEnvironments(): string[] | null {
  return current;
}

export function setActiveEnvironments(envs: string[] | null): void {
  current = envs === null || envs.length === 0 ? null : envs;
}

/** How an environment reads to people. */
export function environmentLabel(env: string): string {
  if (env === PRODUCTION_TOKEN) return "production environments";
  return env === "" ? "(no environment)" : env;
}

/** Every ?env= value, as environments; null for all. */
export function envsFromParams(params: URLSearchParams): string[] | null {
  const raw = params.getAll("env");
  if (raw.length === 0 || raw.some((p) => p === "" || p === ALL_ENVIRONMENTS_PARAM)) return null;
  return raw.map((p) => (p === NO_ENVIRONMENT_PARAM ? "" : p));
}

/** A list of environments that may hold PRODUCTION_TOKEN, as names, given
 *  which known environments are production. */
export function expandProduction(list: string[], production: string[]): string[] {
  const out: string[] = [];
  for (const e of list) {
    for (const n of e === PRODUCTION_TOKEN ? production : [e]) if (!out.includes(n)) out.push(n);
  }
  return out;
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

/** The environments an integration page reads, as [environments, set,
 *  explicitAll]. set(null, true) writes ?env=*, so the page does not open
 *  on its production environments again. */
export function useEnvironmentSet(): [string[] | null, (envs: string[] | null, explicitAll?: boolean) => void, boolean] {
  const [params, setParams] = useSearchParams();
  const envs = envsFromParams(params);
  const set = useCallback(
    (next: string[] | null, explicitAll = false) => {
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          p.delete("env");
          if (next !== null && next.length > 0) for (const e of next) p.append("env", envToParam(e));
          else if (explicitAll) p.set("env", ALL_ENVIRONMENTS_PARAM);
          return p;
        },
        { replace: false },
      );
    },
    [setParams],
  );
  return [envs, set, params.get("env") === ALL_ENVIRONMENTS_PARAM];
}

/** The ?env= part of a link, so moving between an integration's tabs or
 *  back to the list keeps the environments. "" when reading all, unless
 *  all was chosen explicitly (then ?env=*, so the next page does not open
 *  on production). */
export function envQuery(env: string | string[] | null, explicitAll = false): string {
  const envs = env === null ? [] : Array.isArray(env) ? env : [env];
  if (envs.length === 0) return explicitAll ? `?env=${ALL_ENVIRONMENTS_PARAM}` : "";
  return "?" + envs.map((e) => `env=${encodeURIComponent(envToParam(e))}`).join("&");
}

/** Whether a request reads integration data and so follows the active
 *  environment. Writes never do: an environment narrows what is read,
 *  never what is changed. */
export function readsIntegrationData(path: string, method: string): boolean {
  if (method !== "GET" && !(method === "POST" && path.startsWith("/messages/search"))) return false;
  return (
    path.startsWith("/integrations") ||
    // A service's page and everything it reads about the service.
    path.startsWith("/services/") ||
    path.startsWith("/messages/search") ||
    path.startsWith("/logs") ||
    path.startsWith("/log-") ||
    path.startsWith("/metric-") ||
    // The Errors tab reads the org feed and filters it to the integration.
    path.startsWith("/errors") ||
    path.includes("integration=")
  );
}
