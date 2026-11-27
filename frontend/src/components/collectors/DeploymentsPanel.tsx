// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What was sent to a collector and what it said (stage 2), with the
// change each deployment made and, in Enterprise, a one-step restore of a
// configuration the collector applied.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type {
  CollectorDeployment,
  CollectorDeploymentDiff,
} from "../../api/types";
import { formatDateTime, formatRelative } from "../../lib/format";
import { useFeature } from "../../lib/useLicense";
import StatusPip from "../primitives/StatusPip";
import { deploymentStatus } from "../../lib/deploymentStatus";
import { effectSummary } from "../../lib/deploymentEffect";
import ConfigDiff from "./ConfigDiff";

export default function DeploymentsPanel({
  collectorId,
  canPush,
  refreshKey,
}: {
  collectorId: string;
  canPush: boolean;
  refreshKey: number;
}) {
  const restoreEntitled = useFeature("advisor");
  const [list, setList] = useState<CollectorDeployment[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [diff, setDiff] = useState<CollectorDeploymentDiff | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(() => {
    api
      .listCollectorDeployments(collectorId)
      .then((r) => setList(r.deployments))
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [collectorId]);
  useEffect(refresh, [refresh, refreshKey]);
  // While something is on its way, follow it.
  useEffect(() => {
    if (!list?.some((d) => ["pending", "sent", "applying"].includes(d.status)))
      return;
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [list, refresh]);

  const toggle = async (d: CollectorDeployment) => {
    if (open === d.id) {
      setOpen(null);
      return;
    }
    setOpen(d.id);
    setDiff(null);
    try {
      setDiff(await api.collectorDeploymentDiff(collectorId, d.id));
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const restore = async (d: CollectorDeployment) => {
    if (!confirm("Send this configuration to the collector again?")) return;
    setBusy(true);
    try {
      await api.restoreCollectorDeployment(collectorId, d.id);
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  if (error) return <div className="alert alert--error">{error}</div>;
  if (!list) return <div className="placeholder">Loading…</div>;
  if (list.length === 0) {
    return (
      <div className="muted" style={{ fontSize: 13 }}>
        No configuration has been sent to this collector from Sluicio.
      </div>
    );
  }
  return (
    <div className="mtbl">
      <div
        className="mtbl-head"
        style={{ gridTemplateColumns: "1.3fr 1.6fr 1fr auto", gap: 8 }}
      >
        <div>Status</div>
        <div>Change</div>
        <div>When</div>
        <div />
      </div>
      <div className="mtbl-body">
        {list.map((d) => {
          const st = deploymentStatus(d);
          return (
            <div key={d.id}>
              <div
                className="mtbl-row"
                style={{
                  display: "grid",
                  gridTemplateColumns: "1.3fr 1.6fr 1fr auto",
                  gap: 8,
                  alignItems: "center",
                }}
              >
                <div>
                  <StatusPip kind={st.kind} label={st.label} />
                  {d.error_message && (
                    <div
                      className="mono"
                      style={{
                        fontSize: 11.5,
                        color: "var(--err-ink)",
                        marginTop: 2,
                        wordBreak: "break-word",
                      }}
                    >
                      {d.error_message}
                    </div>
                  )}
                </div>
                <div style={{ minWidth: 0 }}>
                  <div className="truncate">
                    {d.message ||
                      (d.rolled_back_from
                        ? "Restore of an earlier configuration"
                        : "No reason given")}
                  </div>
                  <div className="muted" style={{ fontSize: 12 }}>
                    {d.created_by_name || "unknown"} ·{" "}
                    {d.via === "api"
                      ? "API"
                      : d.via === "rollback"
                        ? "restore"
                        : d.via === "advisor"
                          ? "Advisor"
                          : "UI"}
                    {st.waiting && (
                      <>
                        {" "}
                        · <Link to="/proposals">review in Proposals</Link>
                      </>
                    )}
                  </div>
                  {restoreEntitled &&
                    canPush &&
                    d.advisor_class &&
                    d.status === "applied" && (
                      <EffectLine
                        collectorId={collectorId}
                        deploymentId={d.id}
                      />
                    )}
                </div>
                <div title={formatDateTime(d.created_at)}>
                  {formatRelative(d.created_at)}
                </div>
                <div style={{ display: "flex", gap: 6 }}>
                  <button className="btn btn--sm" onClick={() => toggle(d)}>
                    {open === d.id ? "Hide change" : "Show change"}
                  </button>
                  {canPush &&
                    restoreEntitled &&
                    d.status === "applied" &&
                    list[0].id !== d.id && (
                      <button
                        className="btn btn--sm"
                        disabled={busy}
                        onClick={() => restore(d)}
                      >
                        Restore
                      </button>
                    )}
                </div>
              </div>
              {open === d.id && (
                <div style={{ padding: "8px 12px 14px" }}>
                  {diff && diff.deployment.id === d.id ? (
                    <ConfigDiff
                      before={diff.before}
                      after={diff.after}
                      maxHeight={360}
                    />
                  ) : (
                    <div className="placeholder">Loading…</div>
                  )}
                  <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>
                    Compared with the configuration the collector reports now.
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// The measured effect of an applied Advisor change, fetched per row: there
// are few of them, and each is one query over a day of one metric.
function EffectLine({
  collectorId,
  deploymentId,
}: {
  collectorId: string;
  deploymentId: string;
}) {
  const [text, setText] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    api
      .collectorDeploymentEffect(collectorId, deploymentId)
      .then((e) => !cancelled && setText(effectSummary(e)))
      .catch(() => !cancelled && setText(""));
    return () => {
      cancelled = true;
    };
  }, [collectorId, deploymentId]);
  if (!text) return null;
  return (
    <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
      Effect: {text}
    </div>
  );
}
