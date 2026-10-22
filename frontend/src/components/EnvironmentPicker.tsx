// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments on an integration (issue #40): the picker beside the
// integration's name. It reads the integration across ALL its
// environments, whatever the page is narrowed to, so it can always offer
// the others.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { IntegrationEnvironment, ServiceStatus } from "../api/types";
import { environmentLabel, useEnvironmentParam } from "../lib/activeEnvironment";
import { statusLabel } from "../lib/format";
import { useTimeWindow } from "../lib/useTimeWindow";

type EnvironmentsReply = Awaited<ReturnType<typeof api.integrationEnvironments>>;
const recent = new Map<string, { at: number; reply: Promise<EnvironmentsReply> }>();

/** One read of an integration's environments shared by the scope that
 *  picks the default and the picker that lists them, which mount
 *  together. Kept briefly, and dropped if it failed. */
export function integrationEnvironmentsOnce(id: string, window: string): Promise<EnvironmentsReply> {
  const key = `${id}\u0000${window}`;
  const hit = recent.get(key);
  if (hit && Date.now() - hit.at < 15_000) return hit.reply;
  const reply = api.integrationEnvironments(id, window);
  recent.set(key, { at: Date.now(), reply });
  reply.catch(() => recent.delete(key));
  return reply;
}

/** The integration's environments with their status, all of them. */
export function useIntegrationEnvironments(integrationId: string | undefined) {
  const [window] = useTimeWindow();
  const [envs, setEnvs] = useState<IntegrationEnvironment[] | null>(null);
  useEffect(() => {
    if (!integrationId) return;
    let cancelled = false;
    integrationEnvironmentsOnce(integrationId, window)
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
  // "All environments" is written out (?env=*): without it the page
  // would open on the default environment again.
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
        onChange={(e) => setEnv(e.target.value === "\u0000all" ? null : e.target.value, true)}
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
