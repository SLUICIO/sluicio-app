// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Editing a collector's configuration and sending it (stage 2). The text
// is the masked configuration: a secret that stays masked is restored by
// the server from the stored value, so nobody reads one to change
// something next to it. OTelFlow checks the text first; the review step
// shows the change line by line before anything is sent. In Enterprise a
// change can be sent for a limited time, after which the server sends the
// collector its base again.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type { CollectorValidation } from "../../api/types";
import { useFeature } from "../../lib/useLicense";
import { EditDrawer } from "../primitives";
import ConfigDiff from "./ConfigDiff";

const CodeEditor = lazy(() => import("../CodeEditor"));

// How long a temporary change can run; the server accepts 5 minutes to 7
// days.
const DURATIONS = [
  { minutes: 0, label: "Permanently" },
  { minutes: 15, label: "15 minutes" },
  { minutes: 60, label: "1 hour" },
  { minutes: 240, label: "4 hours" },
  { minutes: 1440, label: "1 day" },
  { minutes: 10080, label: "7 days" },
];

export default function SendConfigDrawer({
  collectorId,
  collectorName,
  current,
  requireApproval,
  initial,
  suggestionId,
  suggestionHash,
  stamp,
  onClose,
  onSent,
}: {
  collectorId: string;
  collectorName: string;
  // The configuration as written, masked, as the tab shows it.
  current: string;
  requireApproval: boolean;
  // A change prepared elsewhere (an Advisor suggestion): the drawer opens
  // on its review rather than on an unchanged editor.
  initial?: string;
  // Sends the change as this Advisor suggestion, which always waits for
  // the owner team's approval.
  suggestionId?: string;
  // With suggestionId: a personal-data value is hashed, not deleted.
  suggestionHash?: boolean;
  // The collector stamp: always proposed to the collector's team.
  stamp?: boolean;
  onClose: () => void;
  onSent: () => void;
}) {
  const [text, setText] = useState(initial ?? current);
  const [message, setMessage] = useState("");
  const [step, setStep] = useState<"edit" | "review">("edit");
  const [checking, setChecking] = useState(false);
  const [validation, setValidation] = useState<CollectorValidation | null>(
    null,
  );
  const [checked, setChecked] = useState(false);
  const [keptByLocal, setKeptByLocal] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{
    proposal?: string;
    delivered?: boolean;
    held?: boolean;
  } | null>(null);
  const temporaryEntitled = useFeature("advisor");
  const [minutes, setMinutes] = useState(0);
  const durationLabel = DURATIONS.find((d) => d.minutes === minutes)?.label;
  const dirty = text !== current;
  const errors = useMemo(
    () => (validation?.diagnostics ?? []).filter((d) => d.severity === "error"),
    [validation],
  );

  const check = async () => {
    setChecking(true);
    setError(null);
    try {
      const r = await api.validateCollectorConfig(collectorId, text);
      setChecked(r.checked);
      setValidation(r.validation ?? null);
      setKeptByLocal(r.kept_by_local ?? []);
      setStep("review");
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setChecking(false);
    }
  };

  // A prepared change goes straight to its check and review, once.
  const reviewed = useRef(false);
  useEffect(() => {
    if (initial === undefined || reviewed.current) return;
    reviewed.current = true;
    void check();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial]);

  const send = async (force: boolean) => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.sendCollectorConfig(collectorId, {
        config: text,
        message,
        force,
        suggestion_id: suggestionId,
        suggestion_hash: suggestionHash,
        duration_minutes: minutes || undefined,
        stamp: stamp || undefined,
      });
      setDone({
        proposal: r.proposal?.id,
        delivered: r.delivered,
        held: r.deployment?.status === "held",
      });
      onSent();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <EditDrawer
      title={`Change configuration: ${collectorName}`}
      width="wide"
      onClose={onClose}
      dirty={dirty && !done}
    >
      {done ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          {done.proposal ? (
            <div className="alert alert--info" style={{ margin: 0 }}>
              {suggestionId || stamp
                ? "The change waits for approval by the collector's team."
                : "The change waits for approval by someone else in the team."}{" "}
              <Link to="/proposals">Open Proposals</Link>
            </div>
          ) : (
            <div className="alert alert--info" style={{ margin: 0 }}>
              {done.held
                ? "Stored. A temporary change is running on this collector; this one takes effect when it ends."
                : done.delivered
                  ? minutes
                    ? `Sent for ${durationLabel?.toLowerCase()}, counted from when the collector applies it. Then it is sent its base configuration again.`
                    : "Sent. The Deployments tab shows when the collector has applied it."
                  : "Stored. The collector gets it when it next connects or polls."}
            </div>
          )}
          <div>
            <button className="btn" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
      ) : step === "edit" ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            Masked values (<code>********</code>) keep the collector&apos;s
            current secret. Type a new value to replace one. Settings from the
            supervisor&apos;s local configuration files stay in effect whatever
            is sent.
          </p>
          <Suspense
            fallback={<div className="placeholder">Loading editor…</div>}
          >
            <CodeEditor
              value={text}
              onChange={setText}
              format="yaml"
              height={460}
              showToolbar={false}
            />
          </Suspense>
          {error && <div className="alert alert--error">{error}</div>}
          <div style={{ display: "flex", gap: 8 }}>
            <button
              className="btn primary"
              onClick={check}
              disabled={!dirty || checking}
            >
              {checking ? "Checking…" : "Check and review"}
            </button>
            <button className="btn" onClick={onClose}>
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          {!checked && (
            <div className="alert alert--warn" style={{ margin: 0 }}>
              OTelFlow is not available to this cell, so the change was not
              checked here. The collector still checks it before running it.
            </div>
          )}
          {checked && validation && (
            <div
              className={`alert ${errors.length ? "alert--error" : "alert--info"}`}
              style={{ margin: 0 }}
            >
              {errors.length
                ? `OTelFlow found ${errors.length} error${errors.length === 1 ? "" : "s"}`
                : "OTelFlow found no errors"}
              {validation.checked_version
                ? ` (collector v${validation.checked_version})`
                : " (against OTelFlow's default version)"}
              .
              {errors.length > 0 && (
                <ul style={{ margin: "6px 0 0", paddingLeft: 18 }}>
                  {errors.map((d, i) => (
                    <li key={i}>
                      {d.line ? `Line ${d.line}: ` : ""}
                      {d.message}
                      {d.hint ? ` ${d.hint}` : ""}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
          {keptByLocal.length > 0 && (
            <div className="alert alert--warn" style={{ margin: 0 }}>
              {keptByLocal.length === 1
                ? "This setting is removed here but comes from the collector's local configuration files, so it stays in effect whatever is sent: "
                : "These settings are removed here but come from the collector's local configuration files, so they stay in effect whatever is sent: "}
              {keptByLocal.slice(0, 8).join(", ")}
              {keptByLocal.length > 8
                ? `, and ${keptByLocal.length - 8} more`
                : ""}
              . Remove {keptByLocal.length === 1 ? "it" : "them"} from those
              files on the host.
            </div>
          )}
          <ConfigDiff before={current} after={text} />
          <label
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 4,
              fontSize: 13,
            }}
          >
            Why
            <input
              className="search__input"
              placeholder={
                suggestionId
                  ? "Optional: the Advisor's finding is used when left empty"
                  : "e.g. drop debug logs from the payments pipeline"
              }
              value={message}
              onChange={(e) => setMessage(e.target.value)}
            />
          </label>
          {temporaryEntitled && !suggestionId && !stamp && (
            <label
              style={{
                display: "flex",
                flexDirection: "column",
                gap: 4,
                fontSize: 13,
              }}
            >
              For how long
              <select
                className="search__input"
                value={minutes}
                onChange={(e) => setMinutes(Number(e.target.value))}
              >
                {DURATIONS.map((d) => (
                  <option key={d.minutes} value={d.minutes}>
                    {d.label}
                  </option>
                ))}
              </select>
              {minutes > 0 && (
                <span className="muted" style={{ fontSize: 12 }}>
                  Counted from when the collector applies it. Then the server
                  sends it its base again: a permanent change made meanwhile,
                  else the last permanent one, else its local configuration.
                </span>
              )}
            </label>
          )}
          {error && <div className="alert alert--error">{error}</div>}
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <button
              className="btn primary"
              onClick={() => send(errors.length > 0)}
              disabled={busy}
            >
              {busy
                ? "Sending…"
                : (requireApproval
                    ? errors.length
                      ? "Propose despite errors"
                      : "Propose for approval"
                    : errors.length
                      ? "Send despite errors"
                      : "Send to collector") +
                  (minutes ? ` for ${durationLabel?.toLowerCase()}` : "")}
            </button>
            <button
              className="btn"
              onClick={() => setStep("edit")}
              disabled={busy}
            >
              Back to editing
            </button>
          </div>
        </div>
      )}
    </EditDrawer>
  );
}
