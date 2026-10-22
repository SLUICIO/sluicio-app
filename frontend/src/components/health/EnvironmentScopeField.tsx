// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which environments a check bound to an integration watches (issue
// #40). By default each environment separately, each with its own alert,
// so a backlog in test neither pages as production nor hides behind an
// empty production queue; or only the environments listed.

import { useState } from "react";
import type { AlertRule } from "../../api/types";
import { environmentLabel } from "../../lib/activeEnvironment";
import { useIntegrationEnvironments } from "../EnvironmentPicker";
import type { CheckScope } from "./HealthChecks";

export interface EnvScope {
  mode: "each" | "only";
  environments: string[];
}

export function initialEnvScope(rule: AlertRule | null | undefined): EnvScope {
  return rule?.environment_mode === "only"
    ? { mode: "only", environments: rule.environments ?? [] }
    : { mode: "each", environments: [] };
}

/** The rule fields, for a check bound to an integration only. */
export function envScopePayload(scope: CheckScope, s: EnvScope) {
  if (scope !== "integration") return {};
  return s.mode === "only"
    ? { environment_mode: "only" as const, environments: s.environments }
    : { environment_mode: "each" as const, environments: [] };
}

export default function EnvironmentScopeField({
  scope,
  integrationId,
  value,
  onChange,
}: {
  scope: CheckScope;
  integrationId: string;
  value: EnvScope;
  onChange: (v: EnvScope) => void;
}) {
  const envs = useIntegrationEnvironments(scope === "integration" ? integrationId : undefined);
  const [draft, setDraft] = useState("");
  if (scope !== "integration") return null;
  const known = (envs ?? []).map((e) => e.environment);
  for (const e of value.environments) if (!known.includes(e)) known.push(e);
  const toggle = (e: string) =>
    onChange({
      ...value,
      environments: value.environments.includes(e) ? value.environments.filter((x) => x !== e) : [...value.environments, e],
    });
  return (
    <div className="m-field" data-testid="check-environment-scope">
      <label className="m-field-label">Environments</label>
      <div style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 13.5 }}>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <input type="radio" name="check-env-mode" checked={value.mode === "each"} onChange={() => onChange({ ...value, mode: "each" })} />
          Each environment separately
        </label>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <input type="radio" name="check-env-mode" checked={value.mode === "only"} onChange={() => onChange({ ...value, mode: "only" })} />
          Only these environments
        </label>
      </div>
      {value.mode === "only" && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 10, marginTop: 8, alignItems: "center" }}>
          {known.map((e) => (
            <label key={e} style={{ display: "flex", alignItems: "center", gap: 4, fontSize: 13.5 }}>
              <input type="checkbox" checked={value.environments.includes(e)} onChange={() => toggle(e)} />
              {environmentLabel(e)}
            </label>
          ))}
          <input
            className="search__input"
            style={{ width: 170, flex: "0 0 170px" }}
            placeholder="other environment"
            value={draft}
            onChange={(ev) => setDraft(ev.target.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && draft.trim()) {
                ev.preventDefault();
                if (!value.environments.includes(draft.trim())) {
                  onChange({ ...value, environments: [...value.environments, draft.trim()] });
                }
                setDraft("");
              }
            }}
          />
        </div>
      )}
      <div className="muted" style={{ fontSize: 11.5, marginTop: 4 }}>
        {value.mode === "each"
          ? "Evaluated once per environment of the integration, each with its own alert naming its environment. Telemetry with no environment is its own group."
          : value.environments.length === 0
            ? "Pick at least one environment."
            : "Evaluated for the listed environments only, each with its own alert."}
      </div>
    </div>
  );
}
