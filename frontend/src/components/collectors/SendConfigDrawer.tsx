// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Editing a collector's configuration and sending it (stage 2). The text
// is the masked configuration: a secret that stays masked is restored by
// the server from the stored value, so nobody reads one to change
// something next to it. OTelFlow checks the text first; the review step
// shows the change line by line before anything is sent.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type { CollectorValidation } from "../../api/types";
import { EditDrawer } from "../primitives";
import ConfigDiff from "./ConfigDiff";

const CodeEditor = lazy(() => import("../CodeEditor"));

export default function SendConfigDrawer({
  collectorId,
  collectorName,
  current,
  requireApproval,
  initial,
  suggestionId,
  suggestionHash,
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{
    proposal?: string;
    delivered?: boolean;
  } | null>(null);
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
      });
      setDone({ proposal: r.proposal?.id, delivered: r.delivered });
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
              {suggestionId
                ? "The change waits for approval by the collector's team."
                : "The change waits for approval by someone else in the team."}{" "}
              <Link to="/proposals">Open Proposals</Link>
            </div>
          ) : (
            <div className="alert alert--info" style={{ margin: 0 }}>
              {done.delivered
                ? "Sent. The Deployments tab shows when the collector has applied it."
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
          {error && <div className="alert alert--error">{error}</div>}
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <button
              className="btn primary"
              onClick={() => send(errors.length > 0)}
              disabled={busy}
            >
              {busy
                ? "Sending…"
                : requireApproval
                  ? errors.length
                    ? "Propose despite errors"
                    : "Propose for approval"
                  : errors.length
                    ? "Send despite errors"
                    : "Send to collector"}
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
