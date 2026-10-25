// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments on an integration's or a service's page (issue #40): the
// picker beside its name. It reads the integration or service across ALL
// its environments, whatever the page is narrowed to, so it can always
// offer the others.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { IntegrationEnvironment, ServiceStatus } from "../api/types";
import { environmentLabel, useEnvironmentSet } from "../lib/activeEnvironment";
import { statusLabel } from "../lib/format";
import { useTimeWindow } from "../lib/useTimeWindow";

/** What a page is about: an integration (by id) or a service (by name). */
export interface EnvironmentSubject {
  kind: "integration" | "service";
  id: string;
}

interface EnvironmentsReply {
  environments: IntegrationEnvironment[];
  open_on: "production" | "all";
}
const recent = new Map<string, { at: number; reply: Promise<EnvironmentsReply> }>();

/** One read of an integration's or a service's environments, shared by
 *  the scope that picks what the page opens on and the picker that lists
 *  them, which mount together. Kept briefly, and dropped if it failed. */
export function environmentsOnce(subject: EnvironmentSubject, window: string): Promise<EnvironmentsReply> {
  const key = `${subject.kind}\u0000${subject.id}\u0000${window}`;
  const hit = recent.get(key);
  if (hit && Date.now() - hit.at < 15_000) return hit.reply;
  const reply: Promise<EnvironmentsReply> =
    subject.kind === "integration"
      ? api.integrationEnvironments(subject.id, window)
      : api.serviceEnvironments(subject.id, window);
  recent.set(key, { at: Date.now(), reply });
  reply.catch(() => recent.delete(key));
  return reply;
}

/** The integration's environments with their status, all of them. */
export function useIntegrationEnvironments(integrationId: string | undefined) {
  return useEnvironments(integrationId ? { kind: "integration", id: integrationId } : undefined);
}

/** An integration's or a service's environments, all of them. */
export function useEnvironments(subject: EnvironmentSubject | undefined) {
  const [window] = useTimeWindow();
  const [envs, setEnvs] = useState<IntegrationEnvironment[] | null>(null);
  const kind = subject?.kind;
  const id = subject?.id;
  useEffect(() => {
    if (!kind || !id) return;
    let cancelled = false;
    environmentsOnce({ kind, id }, window)
      .then((r) => !cancelled && setEnvs(r.environments ?? []))
      .catch(() => !cancelled && setEnvs([]));
    return () => {
      cancelled = true;
    };
  }, [kind, id, window]);
  return envs;
}

/** "Is there anything to pick between": more than one environment, or a
 *  single real one. An integration whose telemetry carries no
 *  environment at all shows no picker, so nothing changes for it. */
function worthPicking(envs: IntegrationEnvironment[]): boolean {
  return envs.length > 1 || (envs.length === 1 && envs[0].environment !== "");
}

export default function EnvironmentPicker({
  integrationId,
  serviceName,
}: {
  integrationId?: string;
  serviceName?: string;
}) {
  const envs = useEnvironments(
    integrationId ? { kind: "integration", id: integrationId } : serviceName ? { kind: "service", id: serviceName } : undefined,
  );
  const [chosen, setChosen] = useEnvironmentSet();
  if (envs === null) return null;
  const names = envs.map((e) => e.environment);
  // A link can name environments that are not in this window.
  for (const e of chosen ?? []) if (!names.includes(e)) names.push(e);
  if (!worthPicking(envs) && chosen === null) return null;
  const isProduction = (n: string) => envs.find((e) => e.environment === n)?.production ?? false;
  const production = names.filter((n) => n !== "" && isProduction(n)).sort();
  const other = names.filter((n) => !production.includes(n));
  const same = (a: string[], b: string[]) => a.length === b.length && [...a].sort().join("\u0000") === [...b].sort().join("\u0000");
  // The value of the current choice: all, every production environment,
  // one, or a set a link named.
  const value =
    chosen === null
      ? "\u0000all"
      : production.length > 1 && same(chosen, production)
        ? "\u0000prod"
        : chosen.length === 1
          ? chosen[0]
          : "\u0000set";
  const option = (n: string) => {
    const s = envs.find((e) => e.environment === n);
    return (
      <option key={n} value={n}>
        {environmentLabel(n)}
        {s ? ` · ${statusLabel(s.status as ServiceStatus)}` : ""}
      </option>
    );
  };
  return (
    <label className="flex items-center gap-2 text-sm" data-testid="environment-picker">
      <span className="text-muted">Environment</span>
      {/* Button-sized: it sits beside the page's action buttons. */}
      <select
        className="toolbar__select"
        style={{ minWidth: 180 }}
        value={value}
        // "All environments" is written out (?env=*): without it the
        // page would open on production again.
        onChange={(e) => {
          const v = e.target.value;
          if (v === "\u0000all") setChosen(null, true);
          else if (v === "\u0000prod") setChosen(production);
          else if (v !== "\u0000set") setChosen([v]);
        }}
        aria-label="Environment"
      >
        <option value={"\u0000all"}>All environments</option>
        {production.length > 1 && <option value={"\u0000prod"}>Production ({production.join(", ")})</option>}
        {value === "\u0000set" && <option value={"\u0000set"}>{(chosen ?? []).map(environmentLabel).join(", ")}</option>}
        {production.length > 0 && <optgroup label="Production">{production.map(option)}</optgroup>}
        {other.length > 0 && (
          <optgroup label={production.length > 0 ? "Other environments" : "Environments"}>{other.map(option)}</optgroup>
        )}
      </select>
    </label>
  );
}
