// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments on a list page (issue #40): one status chip per
// environment on each row, worst first, and the list-wide picker that
// narrows the whole list to one environment. Shared by the Integrations
// and Services lists so both read and behave the same.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { EnvironmentStatus } from "../api/types";
import { environmentLabel } from "../lib/activeEnvironment";
import { statusLabel } from "../lib/format";

/** One small chip per environment, worst first, so "test is failing,
 *  production is fine" reads straight off the list. Hidden for a row
 *  whose telemetry carries no environment. linkFor makes each chip a
 *  link to the row read in that environment, where there is one. */
export function EnvironmentChips({
  envs,
  linkFor,
}: {
  envs?: EnvironmentStatus[];
  linkFor?: (env: string) => string;
}) {
  if (!envs || envs.length === 0 || (envs.length === 1 && envs[0].environment === "")) return null;
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 4, marginTop: 4 }} data-testid="environment-chips">
      {envs.map((e) => {
        const title = `${environmentLabel(e.environment)}: ${statusLabel(e.status)}${
          e.declared && e.trace_count === 0 ? " (declared, no traffic)" : ""
        }`;
        const style = { fontSize: 10.5, padding: "1px 6px" };
        return linkFor ? (
          <Link key={e.environment} to={linkFor(e.environment)} className={`pill pill--${e.status}`} style={style} title={title}>
            {environmentLabel(e.environment)}
          </Link>
        ) : (
          <span key={e.environment} className={`pill pill--${e.status}`} style={style} title={title}>
            {environmentLabel(e.environment)}
          </span>
        );
      })}
    </div>
  );
}

/** The list-wide environment picker, beside the page's toolbar buttons.
 *  Hidden while nothing carries an environment. */
export function ListEnvironmentPicker({
  env,
  setEnv,
  window,
}: {
  env: string | null;
  setEnv: (e: string | null) => void;
  window: string;
}) {
  const [envs, setEnvs] = useState<string[] | null>(null);
  useEffect(() => {
    let cancelled = false;
    api
      .listEnvironments(window)
      .then((r) => !cancelled && setEnvs(r.environments ?? []))
      .catch(() => !cancelled && setEnvs([]));
    return () => {
      cancelled = true;
    };
  }, [window]);
  if (envs === null) return null;
  const names = [...envs];
  if (env !== null && !names.includes(env)) names.push(env);
  if (env === null && (names.length === 0 || (names.length === 1 && names[0] === ""))) return null;
  return (
    <select
      className="toolbar__select"
      aria-label="Environment"
      data-testid="list-environment-picker"
      value={env === null ? "\u0000all" : env}
      onChange={(e) => setEnv(e.target.value === "\u0000all" ? null : e.target.value)}
    >
      <option value={"\u0000all"}>All environments</option>
      {names.map((n) => (
        <option key={n} value={n}>
          {environmentLabel(n)}
        </option>
      ))}
    </select>
  );
}
