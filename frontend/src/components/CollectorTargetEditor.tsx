// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which collector Sluicio writes YAML for (issue #16).
//
// Collector configuration is not version-stable — `otlphttp` was removed
// in v0.146.0 and renamed — so a generated snippet is only correct for a
// stated version. The setting existed in the API before this and had no
// way to be set, which meant it was unset for everyone and every snippet
// was written against an assumption.
//
// Two scopes, one component. The org default is what nearly everyone
// needs; the per-service override is what makes it honest for an estate
// running different collector versions on different hosts, because a
// snippet always targets one service's pipeline.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { CollectorTarget } from "../api/types";

interface Props {
  /** Omit for the org default; pass a service name for its override. */
  service?: string;
  /** Called after a successful save, for callers showing a snippet. */
  onChanged?: (t: CollectorTarget) => void;
}

const DISTRIBUTIONS = [
  { value: "contrib", label: "contrib" },
  { value: "core", label: "core" },
];

export default function CollectorTargetEditor({ service, onChanged }: Props) {
  const [target, setTarget] = useState<CollectorTarget | null>(null);
  const [version, setVersion] = useState("");
  const [distribution, setDistribution] = useState("contrib");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const load = () => {
    const p = service ? api.getServiceCollectorTarget(service) : api.getCollectorTarget();
    p.then((t) => {
      setTarget(t);
      // The input starts EMPTY when nothing is set, rather than
      // pre-filled with the assumed version. A pre-filled box says "this
      // is your answer" when what we mean is "we are guessing" — and
      // saving it unchanged would silently turn our assumption into
      // their stated fact.
      const isSet = service ? t.overridden : t.configured;
      setVersion(isSet ? t.version : "");
      setDistribution(t.distribution || "contrib");
    }).catch((e) => setError(String((e as Error).message ?? e)));
  };

  useEffect(load, [service]);

  const save = async (clear = false) => {
    setSaving(true);
    setError(null);
    setSaved(false);
    try {
      const body = clear
        ? { version: null, distribution: null }
        : { version: version.trim(), distribution };
      const t = service
        ? await api.setServiceCollectorTarget(service, body)
        : await api.setCollectorTarget(body as { version?: string; distribution?: string });
      setTarget(t);
      const isSet = service ? t.overridden : t.configured;
      setVersion(isSet ? t.version : "");
      setDistribution(t.distribution || "contrib");
      setSaved(true);
      onChanged?.(t);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  if (!target) {
    return (
      <div className="muted" style={{ fontSize: 12 }}>
        {error ?? "Loading…"}
      </div>
    );
  }

  const isSet = service ? target.overridden : target.configured;

  return (
    <div>
      <p className="muted" style={{ margin: "0 0 12px", fontSize: 13 }}>
        {service ? (
          <>
            Which collector <span className="mono">{service}</span> runs. Set this only
            when it differs from the organization default — a snippet targets one
            service's pipeline, so a host on an older collector needs its own answer.
          </>
        ) : (
          <>
            Which OpenTelemetry Collector this organization runs. Generated
            configuration is written for this version: component names are not stable
            across releases, so YAML that is right for one collector refuses to start on
            another.
          </>
        )}
      </p>

      {error && (
        <div className="alert alert--error" style={{ marginBottom: 12 }}>
          {error}
        </div>
      )}

      {/* What is in force right now, said plainly. A reader has to be
          able to tell a stated version from an assumed one: the snippet
          beside it means something different in each case. */}
      <div className="muted" style={{ fontSize: 12.5, marginBottom: 12 }}>
        {isSet ? (
          <>
            Snippets are written for collector <strong>{target.version}</strong> (
            {target.distribution}).
          </>
        ) : service ? (
          <>
            Following the organization default, collector{" "}
            <strong>{target.org_version ?? target.version}</strong>.
          </>
        ) : (
          <>
            Nothing set, so snippets assume <strong>{target.version}</strong>, the newest
            release this build of Sluicio knows.
          </>
        )}
        {target.beyond_known && (
          <>
            {" "}
            This is newer than this Sluicio release can check ({target.newest_known} is
            the newest it knows), so component names are resolved as of{" "}
            {target.newest_known}.
          </>
        )}
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 320px), 1fr))",
          gap: "14px 18px",
        }}
      >
        <div className="svc-field" style={{ minWidth: 0 }}>
          <label className="svc-field-label" htmlFor={`ct-version-${service ?? "org"}`}>
            Collector version
            <span className="hint">newest known: {target.newest_known}</span>
          </label>
          <input
            id={`ct-version-${service ?? "org"}`}
            className="svc-input mono"
            placeholder={service ? (target.org_version ?? target.version) : target.newest_known}
            value={version}
            onChange={(e) => setVersion(e.target.value)}
          />
        </div>
        <div className="svc-field" style={{ minWidth: 0 }}>
          <label className="svc-field-label" htmlFor={`ct-dist-${service ?? "org"}`}>
            Distribution
            <span className="hint">a component in contrib may be absent from core</span>
          </label>
          <select
            id={`ct-dist-${service ?? "org"}`}
            className="svc-input"
            value={distribution}
            onChange={(e) => setDistribution(e.target.value)}
          >
            {DISTRIBUTIONS.map((d) => (
              <option key={d.value} value={d.value}>
                {d.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div style={{ marginTop: 16, display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
        <button
          className="btn btn--primary"
          onClick={() => save(false)}
          disabled={saving || !version.trim()}
        >
          {saving ? "Saving…" : "Save"}
        </button>
        {/* Clearing is its own action, not "save an empty box". Going
            back to the default is a different intent from pinning the
            default's current value, and the difference shows the next
            time the default moves. */}
        {service && isSet && (
          <button className="btn" onClick={() => save(true)} disabled={saving}>
            Use organization default
          </button>
        )}
        {saved && (
          <span className="muted" style={{ fontSize: 12.5 }}>
            Saved. New snippets use it immediately; suggestions already generated are
            rewritten on the advisor's next pass.
          </span>
        )}
      </div>
    </div>
  );
}
