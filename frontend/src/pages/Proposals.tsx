// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Proposals inbox (issue #8, WS2) — where agent-filed change
// requests meet a human.
//
// The review question is "should this change happen?", so the page leads
// with the diff and the rationale and keeps everything else quiet. Two
// things are load-bearing:
//
//   - Drift is shown BEFORE the decision, not discovered in an approve
//     error. If someone edited the target since the proposal was filed,
//     approving would overwrite them, and the reviewer deserves to know
//     while they still have a choice.
//   - Approving a drifted proposal requires a second, explicit
//     confirmation. Overriding a colleague's edit should cost one more
//     click than the ordinary case.

import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Proposal, ProposalDetail, ProposalState } from "../api/types";

/** Renders a stored JSON value compactly for the diff. */
function val(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "string") return v;
  return JSON.stringify(v);
}

/** Field names are stored as API keys; show them as prose. */
function fieldLabel(f: string): string {
  switch (f) {
    case "for_window":
      return "sustain window";
    case "evaluation_seconds":
      return "evaluation interval";
    default:
      return f.replace(/_/g, " ");
  }
}

const STATE_STYLE: Record<ProposalState, { bg: string; fg: string }> = {
  pending: { bg: "var(--primary-soft)", fg: "var(--primary-ink)" },
  approved: { bg: "var(--ok-soft)", fg: "var(--ok-ink)" },
  rejected: { bg: "var(--surface-3)", fg: "var(--muted)" },
  expired: { bg: "var(--surface-3)", fg: "var(--muted)" },
  superseded: { bg: "var(--surface-3)", fg: "var(--muted)" },
};

function StatePill({ state }: { state: ProposalState }) {
  const s = STATE_STYLE[state] ?? STATE_STYLE.pending;
  return (
    <span
      style={{
        background: s.bg,
        color: s.fg,
        font: "600 10.5px 'Inter', sans-serif",
        letterSpacing: "0.06em",
        textTransform: "uppercase",
        padding: "2px 8px",
        borderRadius: 999,
      }}
    >
      {state}
    </span>
  );
}

function relativeDays(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  const days = Math.round(ms / 86_400_000);
  if (Number.isNaN(days)) return "";
  if (days < 0) return "expired";
  if (days === 0) return "expires today";
  return `expires in ${days} day${days === 1 ? "" : "s"}`;
}

export default function Proposals() {
  const [items, setItems] = useState<Proposal[]>([]);
  const [detail, setDetail] = useState<Record<string, ProposalDetail>>({});
  const [filter, setFilter] = useState<"pending" | "">("pending");
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string>("");
  const [confirmForce, setConfirmForce] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(() => {
    setLoading(true);
    api
      .listProposals(filter)
      .then((r) => setItems(r.proposals ?? []))
      .catch((e) => setErr(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, [filter]);

  useEffect(load, [load]);

  // Pending proposals get their detail fetched so drift is visible in the
  // list itself — a reviewer shouldn't have to open each one to find out
  // whether approving is safe.
  useEffect(() => {
    let cancelled = false;
    const pending = items.filter((p) => p.state === "pending");
    Promise.all(
      pending.map((p) =>
        api
          .getProposal(p.id)
          .then((d) => [p.id, d] as const)
          .catch(() => null),
      ),
    ).then((pairs) => {
      if (cancelled) return;
      const next: Record<string, ProposalDetail> = {};
      for (const pair of pairs) if (pair) next[pair[0]] = pair[1];
      setDetail(next);
    });
    return () => {
      cancelled = true;
    };
  }, [items]);

  const decide = async (id: string, action: "approve" | "reject", force = false) => {
    setBusy(id);
    setErr("");
    try {
      if (action === "approve") await api.approveProposal(id, { force });
      else await api.rejectProposal(id, {});
      setConfirmForce(null);
      load();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Proposals</h1>
          <p className="page__subtitle">
            Change requests filed by agents. Nothing here has been applied — approving runs the
            same update you would make by hand.
          </p>
        </div>
        <div className="toolbar" role="group" aria-label="Filter proposals">
          <button type="button" className={filter === "pending" ? "btn btn--primary" : "btn"} onClick={() => setFilter("pending")}>
            Pending
          </button>
          <button type="button" className={filter === "" ? "btn btn--primary" : "btn"} onClick={() => setFilter("")}>
            All
          </button>
        </div>
      </div>

      {err && <div className="alert alert--error">{err}</div>}

      {loading ? (
        <div className="placeholder">Loading…</div>
      ) : items.length === 0 ? (
        <div className="placeholder">
          <b>{filter === "pending" ? "No proposals waiting" : "No proposals yet"}</b>
          <p className="muted" style={{ marginTop: 6 }}>
            When an agent suggests a change — tuning a noisy check, for instance — it appears
            here for review instead of being applied.
          </p>
        </div>
      ) : (
        <div className="prop-list">
          {items.map((p) => {
            const d = detail[p.id];
            const drifted = d?.drifted_fields ?? [];
            const targetGone = d?.target_missing;
            const isPending = p.state === "pending";
            return (
              <div key={p.id} className="prop-card">
                <div className="prop-card__head">
                  <div>
                    <div className="prop-card__title">{p.target_label || p.target_kind}</div>
                    <div className="prop-card__meta">
                      <StatePill state={p.state} />
                      <span>
                        {p.proposed_by_kind === "service_account" ? "agent" : "user"} ·{" "}
                        {p.proposed_by_label}
                      </span>
                      {p.via === "mcp" && <span className="prop-chip">via MCP</span>}
                      {isPending && <span>{relativeDays(p.expires_at)}</span>}
                    </div>
                  </div>
                </div>

                <div className="prop-diff">
                  {p.changes.map((c) => {
                    const isDrifted = drifted.includes(c.field);
                    return (
                      <div key={c.field} className={`prop-diff__row${isDrifted ? " is-drifted" : ""}`}>
                        <span className="prop-diff__field">{fieldLabel(c.field)}</span>
                        <span className="prop-diff__before">{val(c.before)}</span>
                        <span className="prop-diff__arrow" aria-hidden>
                          →
                        </span>
                        <span className="prop-diff__after">{val(c.after)}</span>
                        {isDrifted && (
                          <span className="prop-diff__warn" title="This field changed after the proposal was filed">
                            changed since proposed
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>

                <div className="prop-rationale">
                  <span className="prop-rationale__label">Why</span>
                  <p>{p.rationale}</p>
                </div>

                {targetGone && (
                  <div className="alert alert--warn">
                    The target no longer exists — this proposal can't be applied.
                  </div>
                )}
                {drifted.length > 0 && !targetGone && (
                  <div className="alert alert--warn">
                    {drifted.length === 1 ? "This field has" : "These fields have"} changed since the
                    proposal was filed: <b>{drifted.map(fieldLabel).join(", ")}</b>. Approving will
                    overwrite that edit.
                  </div>
                )}

                {p.state !== "pending" && p.decision_note && (
                  <div className="prop-note">
                    <span className="prop-rationale__label">Reviewer</span>
                    <p>{p.decision_note}</p>
                  </div>
                )}

                {isPending && (
                  <div className="prop-actions">
                    {confirmForce === p.id ? (
                      <>
                        <span className="muted">
                          Overwrite the newer change and apply this anyway?
                        </span>
                        <button
                          type="button"
                          className="btn btn--danger"
                          disabled={busy === p.id}
                          onClick={() => decide(p.id, "approve", true)}
                        >
                          Yes, overwrite
                        </button>
                        <button type="button" className="btn" onClick={() => setConfirmForce(null)}>
                          Cancel
                        </button>
                      </>
                    ) : (
                      <>
                        <button
                          type="button"
                          className="btn btn--primary"
                          disabled={busy === p.id || targetGone}
                          onClick={() =>
                            drifted.length > 0 ? setConfirmForce(p.id) : decide(p.id, "approve")
                          }
                        >
                          {busy === p.id ? "Applying…" : "Approve & apply"}
                        </button>
                        <button
                          type="button"
                          className="btn"
                          disabled={busy === p.id}
                          onClick={() => decide(p.id, "reject")}
                        >
                          Reject
                        </button>
                      </>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
