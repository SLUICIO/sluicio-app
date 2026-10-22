// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environments an integration is expected to run in (issue #40).
// Optional: without any, its environments are the ones its telemetry
// shows. Declaring one turns its silence into a finding: it reads quiet
// on the list and the Overview, and an "each environment" low-traffic
// check watches it.

import { FormEvent, useEffect, useState } from "react";
import { api } from "../../api/client";
import { environmentLabel } from "../../lib/activeEnvironment";
import { useIntegrationEnvironments } from "../EnvironmentPicker";

export default function DeclaredEnvironments({
  integrationId,
  declared,
  canWrite,
  onSaved,
  onError,
}: {
  integrationId: string;
  declared: string[];
  canWrite: boolean;
  onSaved: () => void;
  onError: (msg: string) => void;
}) {
  const [list, setList] = useState<string[]>(declared);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const seen = useIntegrationEnvironments(integrationId);
  useEffect(() => setList(declared), [declared]);

  const dirty = [...list].sort().join("\u0000") !== [...declared].sort().join("\u0000");
  const suggestions = (seen ?? []).map((e) => e.environment).filter((e) => e !== "" && !list.includes(e));

  const add = (e?: FormEvent) => {
    e?.preventDefault();
    const v = draft.trim();
    if (v && !list.includes(v)) setList([...list, v]);
    setDraft("");
  };
  const save = async () => {
    setBusy(true);
    try {
      await api.setDeclaredEnvironments(integrationId, list);
      onSaved();
    } catch (err) {
      onError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="overflow-hidden rounded-lg border bg-surface-2" style={{ borderColor: "var(--border)" }} data-testid="declared-environments">
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">Environments</h2>
        <p className="text-xs text-muted mt-1">
          The environments this integration is expected to run in. Optional: without any, its environments
          are the ones its telemetry reports. A declared environment with no traffic reads as quiet rather
          than not appearing, and checks set to each environment watch it for silence.
        </p>
      </div>
      <div className="p-4 flex flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          {list.length === 0 && <span className="text-sm text-muted">None declared.</span>}
          {list.map((e) => (
            <span key={e} className="badge" style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
              {environmentLabel(e)}
              {canWrite && (
                <button
                  type="button"
                  aria-label={`Remove ${environmentLabel(e)}`}
                  onClick={() => setList(list.filter((x) => x !== e))}
                  style={{ background: "none", border: 0, cursor: "pointer", color: "var(--muted)", padding: 0 }}
                >
                  ×
                </button>
              )}
            </span>
          ))}
        </div>
        {canWrite && (
          <form onSubmit={add} className="flex flex-wrap items-center gap-2">
            <input
              className="search__input"
              list="declared-env-suggestions"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="production"
              aria-label="Environment to declare"
              style={{ width: 220, flex: "0 0 220px" }}
            />
            <datalist id="declared-env-suggestions">
              {suggestions.map((s) => <option key={s} value={s} />)}
            </datalist>
            <button type="submit" className="btn" disabled={!draft.trim()}>Add</button>
            <button type="button" className="btn btn--primary" disabled={!dirty || busy} onClick={save}>
              {busy ? "Saving…" : "Save"}
            </button>
          </form>
        )}
        {canWrite && suggestions.length > 0 && (
          <p className="text-xs text-muted">
            Seen in its telemetry:{" "}
            {suggestions.map((s, i) => (
              <span key={s}>
                {i > 0 && ", "}
                <button type="button" className="btn btn--link" style={{ padding: 0, fontSize: 12 }} onClick={() => setList([...list, s])}>
                  {s}
                </button>
              </span>
            ))}
          </p>
        )}
      </div>
    </section>
  );
}
