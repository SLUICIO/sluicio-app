// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Advisor, as a tab under Usage (design §8.4). Usage is where
// someone already goes to ask what telemetry costs; this answers the
// next question — what of it is anybody using.
//
// Each card is deliberately ordered: finding, then EVIDENCE, then what
// you LOSE, and only then the snippet and the buttons. The temptation
// is to lead with the saving, because that is the persuasive number.
// But a reader who accepts a suggestion without seeing its cost is a
// reader who will eventually accept a bad one, and the first time that
// happens they stop trusting the whole feature. The loss statement is
// not a disclaimer here; it is the part that makes accepting safe.
//
// Nothing on this page changes a collector. Accepting records a
// decision and leaves a paper trail — the operator still has to paste
// the snippet. That gap is honest: v1 cannot know the collector
// changed, so the card says "accepted", and only a later evaluation
// that stops finding the problem promotes it to "verified".

import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { AdvisorSuggestion } from "../api/types";
import { formatBytes } from "../lib/format";

type Tab = "telemetry" | "alerting";

/** Human labels for the evaluator classes, so a card never shows "T3". */
const CLASS_LABEL: Record<string, string> = {
  T1: "Unused metric",
  T2: "Unread logs",
  T3: "Dead-weight attribute",
  T4: "High cardinality",
  T5: "Echoed request data",
  T6: "Possible personal data",
  F1: "Ignored alert",
  F2: "Always firing",
  F3: "Flapping",
  F4: "Reaches nobody",
  F5: "Duplicate rules",
};

export default function AdvisorPanel() {
  const [tab, setTab] = useState<Tab>("telemetry");
  const [items, setItems] = useState<AdvisorSuggestion[]>([]);
  const [windowDays, setWindowDays] = useState(30);
  const [ledger, setLedger] = useState<{ ready: boolean; days: number; needs_days: number }>({
    ready: true,
    days: 0,
    needs_days: 30,
  });
  const [loading, setLoading] = useState(true);
  const [entitled, setEntitled] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [running, setRunning] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.listAdvisorSuggestions(tab);
      setItems(res.suggestions ?? []);
      setWindowDays(res.window_days ?? 30);
      if (res.ledger) setLedger(res.ledger);
      setEntitled(true);
    } catch (e) {
      // 402 is the entitlement gate, not a failure — show what the
      // feature is rather than an error nobody can act on.
      const status = (e as { status?: number })?.status;
      if (status === 402) setEntitled(false);
      else setError(e instanceof Error ? e.message : "Loading suggestions failed");
    } finally {
      setLoading(false);
    }
  }, [tab]);

  useEffect(() => {
    void load();
  }, [load]);

  const decide = async (s: AdvisorSuggestion, action: "accept" | "dismiss") => {
    setBusy(s.id);
    try {
      if (action === "accept") await api.acceptAdvisorSuggestion(s.id);
      else await api.dismissAdvisorSuggestion(s.id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Recording the decision failed");
    } finally {
      setBusy(null);
    }
  };

  const runNow = async () => {
    setRunning(true);
    setError(null);
    try {
      await api.runAdvisor();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Evaluation failed");
    } finally {
      setRunning(false);
    }
  };

  if (!entitled) {
    return (
      <div className="card" style={{ padding: 20 }}>
        <h3 style={{ marginTop: 0 }}>Advisor</h3>
        <p className="muted" style={{ fontSize: 13 }}>
          The Telemetry and Alert Fatigue advisors are an Enterprise feature. They contrast what this
          cell <strong>ingests</strong> against what anything actually <strong>consumes</strong> — alert rules,
          facets, dashboards, saved views, and people looking — then suggest exactly what to stop
          collecting, with a ready-to-paste collector config for each.
        </p>
        <p className="muted" style={{ fontSize: 13 }}>
          The measurement underneath runs on every cell, Community included, so the history is already
          accumulating: enabling a licence gives you advice from day one rather than in a month.
        </p>
      </div>
    );
  }

  return (
    <div>
      <div
        style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, flexWrap: "wrap", marginBottom: 14 }}
      >
        <div className="level-seg" role="tablist" aria-label="Advisor">
          {(["telemetry", "alerting"] as Tab[]).map((t) => (
            <button
              key={t}
              role="tab"
              aria-selected={tab === t}
              className={`level-seg__btn${tab === t ? " is-active" : ""}`}
              onClick={() => setTab(t)}
            >
              {t === "telemetry" ? "Telemetry" : "Alerting"}
            </button>
          ))}
        </div>
        <button className="btn" onClick={runNow} disabled={running || loading}>
          {running ? "Evaluating…" : "Evaluate now"}
        </button>
      </div>

      <p className="muted" style={{ fontSize: 12.5, marginTop: 0 }}>
        {tab === "telemetry"
          ? `Telemetry with no consumer over the last ${windowDays} days. Anything newer than that is not judged — it has not had a fair chance to be used.`
          : `Alert rules nobody acts on, over the last ${windowDays} days. The advisor deliberately suggests no threshold: it shows what it observed and leaves the number to you.`}
      </p>

      {error && <div className="alert alert--error" style={{ marginBottom: 12 }}>{error}</div>}
      {loading && <p className="muted">Loading…</p>}

      {!loading && !ledger.ready && (
        <div className="card" style={{ padding: 20 }}>
          <h4 style={{ marginTop: 0 }}>Still watching</h4>
          <p className="muted" style={{ fontSize: 13, marginBottom: 8 }}>
            The advisor has {ledger.days} {ledger.days === 1 ? "day" : "days"} of consumption history and
            needs {ledger.needs_days}. It will start advising in about{" "}
            {Math.max(ledger.needs_days - ledger.days, 1)} days.
          </p>
          <p className="muted" style={{ fontSize: 13, margin: 0 }}>
            This wait is deliberate. "Nobody used this" is only true if we were watching the whole time —
            judging {ledger.needs_days} days of telemetry against {ledger.days} would call anything you
            charted last month unused, and recommend deleting it. Config you already have (alert rules,
            facet mappings, dashboards) is protected from day one; it is people's queries that need the
            history.
          </p>
        </div>
      )}

      {!loading && ledger.ready && items.length === 0 && (
        <div className="card" style={{ padding: 20 }}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            Nothing to suggest — everything this cell collects has a consumer, and no alert rule is
            firing unattended.
          </p>
        </div>
      )}

      {items.map((s) => (
        <SuggestionCard key={s.id} s={s} busy={busy === s.id} onDecide={decide} />
      ))}
    </div>
  );
}

function SuggestionCard({
  s,
  busy,
  onDecide,
}: {
  s: AdvisorSuggestion;
  busy: boolean;
  onDecide: (s: AdvisorSuggestion, action: "accept" | "dismiss") => void;
}) {
  const [copied, setCopied] = useState(false);
  const decided = s.state !== "open";
  const compliance = s.evidence?.compliance === true;

  const copy = async () => {
    await navigator.clipboard.writeText(s.snippet ?? "");
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div className="card" style={{ padding: 18, marginBottom: 12 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10, flexWrap: "wrap" }}>
        <span className={`chip${compliance ? " chip--warn" : ""}`}>
          {CLASS_LABEL[s.class] ?? s.class}
        </span>
        <strong style={{ fontSize: 15 }}>{s.title}</strong>
        {s.advisor === "telemetry" && s.weight > 0 && !compliance && (
          <span className="muted" style={{ fontSize: 12.5 }}>
            ≈ {formatBytes(s.weight)}/day
          </span>
        )}
        {decided && <span className="chip">{s.state}</span>}
      </div>

      <Evidence evidence={s.evidence} />

      {s.loss && (
        <p style={{ fontSize: 13, marginBottom: 10 }}>
          <strong>What you lose: </strong>
          <span className="muted">{s.loss}</span>
        </p>
      )}

      {s.snippet && (
        <details style={{ marginBottom: 10 }}>
          <summary style={{ cursor: "pointer", fontSize: 13 }}>Collector config</summary>
          <pre
            style={{ fontSize: 12, overflowX: "auto", padding: 12, borderRadius: 6, marginTop: 8 }}
            className="code-block"
          >
            {s.snippet}
          </pre>
          <button className="btn btn--sm" onClick={copy}>
            {copied ? "Copied" : "Copy"}
          </button>
        </details>
      )}

      {!decided && (
        <div style={{ display: "flex", gap: 8 }}>
          <button className="btn btn--primary btn--sm" disabled={busy} onClick={() => onDecide(s, "accept")}>
            Accept
          </button>
          <button className="btn btn--sm" disabled={busy} onClick={() => onDecide(s, "dismiss")}>
            Dismiss
          </button>
        </div>
      )}
      {s.state === "accepted" && (
        <p className="muted" style={{ fontSize: 12, margin: 0 }}>
          Accepted — paste the config into your collector. This will show as verified once the
          telemetry actually stops arriving.
        </p>
      )}
      {s.state === "verified" && (
        <p className="muted" style={{ fontSize: 12, margin: 0 }}>
          Verified — the telemetry stopped arriving, so the change took effect.
        </p>
      )}
    </div>
  );
}

/** Renders the counted facts. Keys are snake_case from the evaluator. */
function Evidence({ evidence }: { evidence: Record<string, unknown> }) {
  const entries = Object.entries(evidence ?? {}).filter(
    ([k, v]) => k !== "compliance" && k !== "review_required" && v !== null && v !== undefined && v !== "",
  );
  if (entries.length === 0) return null;
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(190px, 1fr))",
        gap: "4px 16px",
        margin: "10px 0",
        fontSize: 12.5,
      }}
    >
      {entries.map(([k, v]) => (
        <div key={k}>
          <dt className="muted" style={{ display: "inline" }}>
            {k.replace(/_/g, " ")}:{" "}
          </dt>
          <dd style={{ display: "inline", margin: 0 }}>
            {Array.isArray(v) ? v.join(", ") : String(v)}
          </dd>
        </div>
      ))}
    </dl>
  );
}
