// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environments an integration is expected to run in (issue #40).
// Optional: without any, its environments are the ones its telemetry
// shows. Declaring one turns its silence into a finding: it reads quiet
// on the list and the Overview, and an "each environment" low-traffic
// check watches it.

import { useEffect, useState } from "react";
import { api } from "../../api/client";
import { environmentLabel } from "../../lib/activeEnvironment";
import { useIntegrationEnvironments } from "../EnvironmentPicker";
import SearchableSelect from "../SearchableSelect";

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
  const [busy, setBusy] = useState(false);
  const seen = useIntegrationEnvironments(integrationId);
  useEffect(() => setList(declared), [declared]);

  const dirty = [...list].sort().join("\u0000") !== [...declared].sort().join("\u0000");
  const suggestions = (seen ?? []).map((e) => e.environment).filter((e) => e !== "" && !list.includes(e));

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
      <div className="p-4 flex flex-wrap items-center gap-2">
        {list.length === 0 && !canWrite && <span className="text-sm text-muted">None declared.</span>}
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
        {canWrite && (
          <>
            {/* The app's select, not a native datalist: the environments
                its telemetry reports, or a name typed for one that has not
                sent yet. */}
            <SearchableSelect
              value=""
              onChange={(v) => v && !list.includes(v) && setList([...list, v])}
              options={suggestions}
              withoutAll
              creatable
              allLabel="Add an environment…"
              placeholder="Search or type a name…"
              createLabel={(typed) => `Declare “${typed}”`}
            />
            <button type="button" className="btn btn--primary" disabled={!dirty || busy} onClick={save}>
              {busy ? "Saving…" : "Save"}
            </button>
          </>
        )}
      </div>
    </section>
  );
}
