// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments on an integration (issue #40): the picker beside the
// integration's name, and the strip of per-environment tiles on its
// Overview. Both read the integration across ALL its environments,
// whatever the page is narrowed to, so they can always offer the others.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { IntegrationEnvironment, ServiceStatus } from "../api/types";
import { environmentLabel, useEnvironmentParam } from "../lib/activeEnvironment";
import { formatNumber, statusLabel } from "../lib/format";
import { useTimeWindow } from "../lib/useTimeWindow";
import { StatusPip, pipForStatus } from "./primitives";

/** The integration's environments with their status, all of them. */
export function useIntegrationEnvironments(integrationId: string | undefined) {
  const [window] = useTimeWindow();
  const [envs, setEnvs] = useState<IntegrationEnvironment[] | null>(null);
  useEffect(() => {
    if (!integrationId) return;
    let cancelled = false;
    api
      .integrationEnvironments(integrationId, window)
      .then((r) => !cancelled && setEnvs(r.environments ?? []))
      .catch(() => !cancelled && setEnvs([]));
    return () => {
      cancelled = true;
    };
  }, [integrationId, window]);
  return envs;
}

/** "Is there anything to pick between": more than one environment, or a
 *  single real one. An integration whose telemetry carries no
 *  environment at all shows no picker, so nothing changes for it. */
function worthPicking(envs: IntegrationEnvironment[]): boolean {
  return envs.length > 1 || (envs.length === 1 && envs[0].environment !== "");
}

export default function EnvironmentPicker({ integrationId }: { integrationId: string | undefined }) {
  const envs = useIntegrationEnvironments(integrationId);
  const [env, setEnv] = useEnvironmentParam();
  if (envs === null) return null;
  const names = envs.map((e) => e.environment);
  // A link can name an environment that is not in this window.
  if (env !== null && !names.includes(env)) names.push(env);
  if (!worthPicking(envs) && env === null) return null;
  return (
    <label className="flex items-center gap-2 text-sm" data-testid="environment-picker">
      <span className="text-muted">Environment</span>
      <select
        className="search__input"
        style={{ minWidth: 180 }}
        value={env === null ? "\u0000all" : env}
        onChange={(e) => setEnv(e.target.value === "\u0000all" ? null : e.target.value)}
        aria-label="Environment"
      >
        <option value={"\u0000all"}>All environments</option>
        {names.map((n) => {
          const s = envs.find((e) => e.environment === n);
          return (
            <option key={n} value={n}>
              {environmentLabel(n)}
              {s ? ` · ${statusLabel(s.status as ServiceStatus)}` : ""}
            </option>
          );
        })}
      </select>
    </label>
  );
}

/** One tile per environment, worst first, so "test is failing,
 *  production is fine" is visible without switching. Clicking one reads
 *  the page in it. */
export function EnvironmentStrip({ integrationId }: { integrationId: string | undefined }) {
  const envs = useIntegrationEnvironments(integrationId);
  const [env, setEnv] = useEnvironmentParam();
  if (envs === null || !worthPicking(envs)) return null;
  return (
    <section className="mt-4" data-testid="environment-strip">
      <div className="mb-2 text-xs uppercase tracking-wide text-muted">Environments</div>
      <div className="flex flex-wrap gap-2">
        {envs.map((e) => {
          const selected = env === e.environment;
          return (
            <button
              key={e.environment}
              type="button"
              onClick={() => setEnv(selected ? null : e.environment)}
              className="rounded-lg border px-3 py-2 text-left text-sm transition-colors hover:bg-surface-elevated"
              style={{
                borderColor: selected ? "var(--primary)" : "var(--border)",
                background: "var(--surface-2)",
                minWidth: 150,
              }}
              title={selected ? "Show all environments" : `Read this integration in ${environmentLabel(e.environment)}`}
              aria-pressed={selected}
            >
              <div className="flex items-center gap-2">
                <StatusPip kind={pipForStatus(e.status)} label={statusLabel(e.status as ServiceStatus)} />
                <span className="font-medium">{environmentLabel(e.environment)}</span>
              </div>
              <div className="mt-1 text-xs text-muted">
                {e.trace_count > 0 ? (
                  <>
                    {formatNumber(e.trace_count)} message{e.trace_count === 1 ? "" : "s"}
                    {e.error_trace_count > 0 && <> · {formatNumber(e.error_trace_count)} failed</>}
                  </>
                ) : e.metric_rows > 0 || e.log_records > 0 ? (
                  "metrics or logs only"
                ) : e.declared ? (
                  "declared, no traffic"
                ) : (
                  "no traffic"
                )}
              </div>
            </button>
          );
        })}
      </div>
    </section>
  );
}
