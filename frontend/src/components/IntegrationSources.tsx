// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Supporting telemetry (issue #43): an integration's metric and log
// sources. Edited under Edit integration → Services beside the members,
// like everything else that configures an integration; its Services tab
// lists them read-only. A source is telemetry from a service that
// is NOT a member - a broker serving many integrations, where this one
// owns the orders queue - that is evidence about the integration. It
// feeds the integration's Metrics and Logs and its metric and log checks,
// and never its flow, its members or its message counts, which is why it
// is listed apart from the services above.

import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { envQuery, useEnvironmentSet } from "../lib/activeEnvironment";
import { api } from "../api/client";
import type { IntegrationSource, IntegrationSourceView, MatcherOperator, System } from "../api/types";
import SearchableSelect from "./SearchableSelect";

const OPERATORS: { value: MatcherOperator; label: string }[] = [
  { value: "equals", label: "=" },
  { value: "not_equals", label: "≠" },
  { value: "prefix", label: "starts with" },
  { value: "suffix", label: "ends with" },
  { value: "contains", label: "contains" },
  { value: "not_contains", label: "does not contain" },
  { value: "regex", label: "matches regex" },
  { value: "exists", label: "exists" },
  { value: "not_exists", label: "does not exist" },
];

const valueless = (op: MatcherOperator) => op === "exists" || op === "not_exists";

function opLabel(op: MatcherOperator): string {
  return OPERATORS.find((o) => o.value === op)?.label ?? op;
}

/** "metrics of rabbitmq-prod where rabbitmq.queue.name = orders, named rabbitmq.queue.*" */
export function describe(s: IntegrationSourceView, withSignal = true) {
  return (
    <>
      {withSignal && (
        <>
          <span className="font-medium">{s.signal === "metrics" ? "Metrics" : "Logs"}</span> of{" "}
        </>
      )}
      {s.origin_kind === "system" ? (
        <>
          system <span className="font-medium">{s.origin_name}</span>
          <span className="text-muted">
            {" "}
            ({s.origin_services.length === 0 ? "no member services yet" : s.origin_services.join(", ")})
          </span>
        </>
      ) : (
        <span className="font-mono">{s.origin}</span>
      )}
      {s.conditions.length > 0 && (
        <>
          {" "}
          where{" "}
          {s.conditions.map((c, i) => (
            <span key={i}>
              {i > 0 && " and "}
              <span className="font-mono">{c.attribute}</span> {opLabel(c.operator)}
              {!valueless(c.operator) && <span className="font-mono"> {c.value}</span>}
            </span>
          ))}
        </>
      )}
      {s.metric_names.length > 0 && (
        <>
          , metrics <span className="font-mono">{s.metric_names.join(", ")}</span>
        </>
      )}
    </>
  );
}

/** ?addSource=metrics (or logs) on Edit integration → Services opens the
 *  editor with a new source of that signal started: the Metrics and Logs
 *  tabs link here. */
export const ADD_SOURCE_PARAM = "addSource";

/** Where an integration's sources are edited. */
export function sourcesEditHref(integrationId: string, add?: IntegrationSource["signal"]): string {
  return `/integrations/${encodeURIComponent(integrationId)}/settings?tab=services${add ? `&${ADD_SOURCE_PARAM}=${add}` : ""}`;
}

const blank = (signal: IntegrationSource["signal"] = "metrics"): IntegrationSource => ({
  signal,
  origin_kind: "service",
  origin: "",
  conditions: [{ attribute: "", operator: "equals", value: "" }],
  metric_names: [],
});

export default function IntegrationSources({
  integrationId,
  sources,
  canManage,
  onSaved,
  readOnly = false,
}: {
  integrationId: string;
  sources: IntegrationSourceView[];
  canManage: boolean;
  onSaved?: (sources: IntegrationSourceView[]) => void;
  /** The integration's Services tab: the list, and for someone who may
   *  edit the integration a link to where it is edited. */
  readOnly?: boolean;
}) {
  const [draft, setDraft] = useState<IntegrationSource[] | null>(null);
  const [services, setServices] = useState<string[]>([]);
  const [systems, setSystems] = useState<System[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [params, setParams] = useSearchParams();
  const sectionRef = useRef<HTMLElement | null>(null);

  // Arrived from the Metrics or Logs tab to add a source: open the
  // editor with one started, and bring it into view.
  useEffect(() => {
    const signal = params.get(ADD_SOURCE_PARAM);
    if (readOnly || (signal !== "metrics" && signal !== "logs")) return;
    if (canManage) {
      setDraft([
        ...sources.map(({ signal, origin_kind, origin, conditions, metric_names }) => ({
          signal,
          origin_kind,
          origin,
          conditions: conditions.map((c) => ({ ...c })),
          metric_names: [...metric_names],
        })),
        blank(signal),
      ]);
      setTimeout(() => sectionRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
    }
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        p.delete(ADD_SOURCE_PARAM);
        return p;
      },
      { replace: true },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Origins to pick from, only once editing starts.
  useEffect(() => {
    if (draft === null) return;
    api
      .listServices("24h")
      .then((r) => setServices((r.services ?? []).map((s) => s.service_name).sort()))
      .catch(() => setServices([]));
    api
      .listSystems()
      .then((r) => setSystems(r.systems ?? []))
      .catch(() => setSystems([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft === null]);

  const update = (i: number, patch: Partial<IntegrationSource>) =>
    setDraft((d) => (d ?? []).map((s, j) => (j === i ? { ...s, ...patch } : s)));

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      const clean = draft.map((s) => ({
        ...s,
        origin: s.origin.trim(),
        conditions: s.conditions.filter((c) => c.attribute.trim() !== ""),
        metric_names: s.signal === "metrics" ? s.metric_names : [],
      }));
      const r = await api.setIntegrationSources(integrationId, clean);
      onSaved?.(r.sources ?? []);
      setDraft(null);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  if (!canManage && sources.length === 0) return null;

  return (
    <section
      ref={sectionRef}
      className="overflow-hidden rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
      data-testid="integration-sources"
    >
      <div className="flex items-baseline justify-between border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">Supporting telemetry ({sources.length})</h2>
        {canManage && readOnly && (
          <Link className="btn btn--sm" to={sourcesEditHref(integrationId)} data-testid="edit-sources-link">
            {sources.length > 0 ? "Edit" : "Add a source"}
          </Link>
        )}
        {canManage && !readOnly && draft === null && (
          <button
            type="button"
            className="btn btn--sm"
            onClick={() =>
              setDraft(
                sources.length > 0
                  ? sources.map(({ signal, origin_kind, origin, conditions, metric_names }) => ({
                      signal,
                      origin_kind,
                      origin,
                      conditions: conditions.map((c) => ({ ...c })),
                      metric_names: [...metric_names],
                    }))
                  : [blank()],
              )
            }
            data-testid="edit-sources"
          >
            {sources.length > 0 ? "Edit" : "Add a source"}
          </button>
        )}
      </div>
      <p className="px-4 pt-3 text-sm text-muted" style={{ lineHeight: 1.55 }}>
        Metrics and logs from services that are not members, read as part of this integration: a broker's queue,
        a runtime's logs for this flow. They feed the Metrics and Logs tabs and the metric and log checks, never the
        flow, the members or the message counts.
      </p>

      {draft === null ? (
        <ul className="flex flex-col gap-1 px-4 py-3 text-sm">
          {sources.length === 0 && <li className="text-muted">None yet.</li>}
          {sources.map((s) => (
            <li key={s.id}>{describe(s)}</li>
          ))}
        </ul>
      ) : (
        <div className="flex flex-col gap-3 px-4 py-3 text-sm">
          {draft.map((s, i) => (
            <div
              key={i}
              className="flex flex-col gap-2 rounded-md border p-3"
              style={{ borderColor: "var(--border)" }}
              data-testid="source-row"
            >
              <div className="flex flex-wrap items-center gap-2">
                <select
                  className="search__input"
                  style={{ width: 110, flex: "0 0 110px" }}
                  value={s.signal}
                  onChange={(e) => update(i, { signal: e.target.value as IntegrationSource["signal"] })}
                  aria-label="Signal"
                >
                  <option value="metrics">Metrics</option>
                  <option value="logs">Logs</option>
                </select>
                <span className="text-muted">of</span>
                <select
                  className="search__input"
                  style={{ width: 110, flex: "0 0 110px" }}
                  value={s.origin_kind}
                  onChange={(e) => update(i, { origin_kind: e.target.value as IntegrationSource["origin_kind"], origin: "" })}
                  aria-label="Origin kind"
                >
                  <option value="service">service</option>
                  <option value="system">system</option>
                </select>
                {s.origin_kind === "service" ? (
                  <SearchableSelect
                    value={s.origin}
                    onChange={(v) => update(i, { origin: v })}
                    options={services}
                    withoutAll
                    creatable
                    variant="field"
                    allLabel="choose a service…"
                    placeholder="Search or type a service…"
                    createLabel={(typed) => `Use “${typed}”`}
                  />
                ) : (
                  <SearchableSelect
                    value={s.origin}
                    onChange={(v) => update(i, { origin: v })}
                    options={systems.map((sy) => sy.id)}
                    labelFor={(id) => systems.find((sy) => sy.id === id)?.name ?? id}
                    withoutAll
                    variant="field"
                    allLabel="choose a system…"
                    placeholder="Search systems…"
                  />
                )}
                <button
                  type="button"
                  className="btn btn--sm"
                  style={{ marginLeft: "auto" }}
                  onClick={() => setDraft((d) => (d ?? []).filter((_, j) => j !== i))}
                >
                  remove
                </button>
              </div>
              {s.conditions.map((c, ci) => (
                <div key={ci} className="flex flex-wrap items-center gap-2">
                  <span className="text-muted" style={{ width: 40 }}>
                    {ci === 0 ? "where" : "and"}
                  </span>
                  <input
                    className="search__input"
                    style={{ width: 220, flex: "0 0 220px" }}
                    placeholder="rabbitmq.queue.name"
                    value={c.attribute}
                    onChange={(e) =>
                      update(i, { conditions: s.conditions.map((x, k) => (k === ci ? { ...x, attribute: e.target.value } : x)) })
                    }
                    aria-label="Attribute"
                  />
                  <select
                    className="search__input"
                    style={{ width: 150, flex: "0 0 150px" }}
                    value={c.operator}
                    onChange={(e) =>
                      update(i, {
                        conditions: s.conditions.map((x, k) =>
                          k === ci ? { ...x, operator: e.target.value as MatcherOperator } : x,
                        ),
                      })
                    }
                    aria-label="Operator"
                  >
                    {OPERATORS.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                  {!valueless(c.operator) && (
                    <input
                      className="search__input"
                      style={{ width: 180, flex: "0 0 180px" }}
                      placeholder="orders"
                      value={c.value}
                      onChange={(e) =>
                        update(i, { conditions: s.conditions.map((x, k) => (k === ci ? { ...x, value: e.target.value } : x)) })
                      }
                      aria-label="Value"
                    />
                  )}
                  <button
                    type="button"
                    className="btn btn--sm"
                    onClick={() => update(i, { conditions: s.conditions.filter((_, k) => k !== ci) })}
                    aria-label="Remove condition"
                  >
                    ×
                  </button>
                </div>
              ))}
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  className="btn btn--sm"
                  onClick={() => update(i, { conditions: [...s.conditions, { attribute: "", operator: "equals", value: "" }] })}
                >
                  + condition
                </button>
                {s.signal === "metrics" && (
                  <label className="flex items-center gap-2">
                    <span className="text-muted">metrics</span>
                    <input
                      className="search__input"
                      style={{ width: 260, flex: "0 0 260px" }}
                      placeholder="all; or rabbitmq.queue.*, comma separated"
                      value={s.metric_names.join(", ")}
                      onChange={(e) =>
                        update(i, {
                          metric_names: e.target.value
                            .split(",")
                            .map((x) => x.trim())
                            .filter(Boolean),
                        })
                      }
                      aria-label="Metric names"
                    />
                  </label>
                )}
              </div>
            </div>
          ))}
          {error && <div className="alert alert--error">{error}</div>}
          <div className="flex items-center gap-2">
            <button type="button" className="btn btn--sm" onClick={() => setDraft((d) => [...(d ?? []), blank()])}>
              + source
            </button>
            <button
              type="button"
              className="btn btn--primary btn--sm"
              disabled={saving || draft.some((s) => s.origin.trim() === "")}
              onClick={save}
              data-testid="save-sources"
            >
              {saving ? "Saving…" : "Save"}
            </button>
            <button type="button" className="btn btn--sm" onClick={() => (setDraft(null), setError(null))}>
              Cancel
            </button>
          </div>
        </div>
      )}
    </section>
  );
}

/** The line above an integration's Metrics or Logs: which sources the
 *  tab includes, or, for someone who may edit the integration, how to
 *  add one. Nothing for a reader with no sources to show. */
export function SourcesNote({
  integrationId,
  signal,
  sources,
  canManage,
}: {
  integrationId: string;
  signal: IntegrationSource["signal"];
  sources: IntegrationSourceView[];
  canManage: boolean;
}) {
  const mine = sources.filter((s) => s.signal === signal);
  const noun = signal === "metrics" ? "metrics" : "logs";
  // Keep the environments being read on the way to the Services tab.
  const [envs, , explicitAll] = useEnvironmentSet();
  const q = envQuery(envs, explicitAll);
  const services = `/integrations/${encodeURIComponent(integrationId)}/services${q}`;
  const addLink = sourcesEditHref(integrationId, signal);
  if (mine.length === 0 && !canManage) return null;
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm text-muted" data-testid={`sources-note-${signal}`}>
      {mine.length === 0 ? (
        <span>
          {signal === "metrics"
            ? "Metrics from a service that is not a member, such as its queue on a shared broker, can be added as a source."
            : "Logs from a service that is not a member, such as a runtime that serves many flows, can be added as a source."}
        </span>
      ) : (
        <span>
          Includes {noun} from {mine.length === 1 ? "a source" : `${mine.length} sources`}:{" "}
          {mine.map((s, i) => (
            <span key={s.id}>
              {i > 0 && "; "}
              {describe(s, false)}
            </span>
          ))}
          .
        </span>
      )}
      {canManage && (
        <Link className="btn btn--link" style={{ padding: 0 }} to={addLink}>
          Add a {signal === "metrics" ? "metric" : "log"} source
        </Link>
      )}
      {mine.length > 0 && !canManage && (
        <Link className="btn btn--link" style={{ padding: 0 }} to={services}>
          See sources
        </Link>
      )}
    </div>
  );
}
