// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Applying an Advisor suggestion to a collector (stage 3). Sluicio sees
// the telemetry, not the collector it came through, so the admin picks the
// collector; each row says where the change would go and where it would
// not. The chosen change opens in the usual send drawer, on its review,
// and is filed for the owner team's approval.

import { useEffect, useState } from "react";
import { api } from "../../api/client";
import type {
  AdvisorCollectorChange,
  AdvisorCollectorRow,
  AdvisorSuggestion,
} from "../../api/types";
import { EditDrawer } from "../primitives";
import SendConfigDrawer from "./SendConfigDrawer";

export default function AdvisorCollectorDrawer({
  suggestion,
  onClose,
}: {
  suggestion: AdvisorSuggestion;
  onClose: () => void;
}) {
  const [rows, setRows] = useState<AdvisorCollectorRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  // Personal data (T6): deleted by default; hashing keeps "the same value
  // twice" matching, and is pseudonymous rather than anonymous.
  const personal = suggestion.class === "T6";
  const [hash, setHash] = useState(false);
  const [chosen, setChosen] = useState<{
    row: AdvisorCollectorRow;
    change: AdvisorCollectorChange;
  } | null>(null);

  useEffect(() => {
    api
      .listAdvisorSuggestionCollectors(suggestion.id)
      .then((r) => setRows(r.collectors))
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [suggestion.id]);

  const review = async (row: AdvisorCollectorRow) => {
    setBusy(row.collector.id);
    setError(null);
    try {
      const change = await api.previewAdvisorCollectorChange(
        suggestion.id,
        row.collector.id,
        personal && hash,
      );
      if (change.unavailable || !change.config) {
        setError(change.unavailable ?? "Nothing to change on this collector.");
        return;
      }
      setChosen({ row, change });
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(null);
    }
  };

  if (chosen) {
    const c = chosen.row.collector;
    return (
      <SendConfigDrawer
        collectorId={c.id}
        collectorName={c.description["host.name"] || c.instance_uid}
        current={chosen.change.before}
        initial={chosen.change.config}
        suggestionId={suggestion.id}
        suggestionHash={personal && hash}
        requireApproval
        onClose={onClose}
        onSent={() => undefined}
      />
    );
  }

  return (
    <EditDrawer title="Apply to a collector" width="medium" onClose={onClose}>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <p className="muted" style={{ margin: 0, fontSize: 13 }}>
          <strong>{suggestion.title}.</strong> Collectors that stamp their data
          say how much of it came through them, most first; for the others
          Sluicio cannot tell, so choose the one that sends it. The change goes
          only into pipelines that send to Sluicio alone, and goes to the
          collector&apos;s team for approval.
        </p>
        {personal && (
          <label
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 4,
              fontSize: 13,
            }}
          >
            What to do with the value
            <select
              className="search__input"
              value={hash ? "hash" : "delete"}
              onChange={(e) => setHash(e.target.value === "hash")}
            >
              <option value="delete">Delete the attribute</option>
              <option value="hash">Hash the value (SHA-256)</option>
            </select>
            {hash && (
              <span className="muted" style={{ fontSize: 12 }}>
                The same value still matches itself, but this is
                pseudonymisation, not anonymisation: an ID number or an email
                address can be found again by hashing candidates.
              </span>
            )}
          </label>
        )}
        {error && <div className="alert alert--error">{error}</div>}
        {!rows && !error && <div className="placeholder">Loading…</div>}
        {rows && rows.length === 0 && (
          <div className="muted" style={{ fontSize: 13 }}>
            No collector runs under the supervisor, so none can be changed from
            Sluicio. Copy the snippet into the collector&apos;s configuration
            instead.
          </div>
        )}
        {rows && rows.length > 0 && (
          <div className="mtbl">
            <div
              className="mtbl-head"
              style={{ gridTemplateColumns: "1.2fr 1.6fr auto", gap: 8 }}
            >
              <div>Collector</div>
              <div>Change</div>
              <div />
            </div>
            <div className="mtbl-body">
              {rows.map((r) => {
                const usable =
                  !r.unavailable && !r.already_dropped && r.changed.length > 0;
                return (
                  <div
                    key={r.collector.id}
                    className="mtbl-row"
                    style={{
                      display: "grid",
                      gridTemplateColumns: "1.2fr 1.6fr auto",
                      gap: 8,
                      alignItems: "center",
                    }}
                  >
                    <div style={{ minWidth: 0 }}>
                      <div className="truncate">
                        {r.collector.description["host.name"] ||
                          r.collector.instance_uid}
                      </div>
                      <div className="muted" style={{ fontSize: 12 }}>
                        {r.collector.group_name}
                      </div>
                    </div>
                    <div className="muted" style={{ fontSize: 12.5 }}>
                      <div>{carriedLine(r)}</div>
                      {r.already_dropped
                        ? "Drops this metric already."
                        : r.unavailable
                          ? `Not here: ${r.unavailable}.`
                          : `Pipelines: ${r.changed.join(", ")}`}
                      {usable && r.skipped.length > 0 && (
                        <div>
                          Left alone:{" "}
                          {r.skipped
                            .map((s) => `${s.pipeline} (${s.reason})`)
                            .join("; ")}
                        </div>
                      )}
                    </div>
                    <div>
                      {usable && (
                        <button
                          className="btn btn--sm"
                          disabled={busy !== null}
                          onClick={() => review(r)}
                        >
                          {busy === r.collector.id
                            ? "Preparing…"
                            : "Review change"}
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </EditDrawer>
  );
}

// carriedLine says whether the collector carries this telemetry, from the
// stamp on the data.
function carriedLine(r: AdvisorCollectorRow): string {
  if (!r.stamped) return "Not stamped: Sluicio cannot tell what it carries.";
  if (r.carried === undefined) return "";
  return r.carried > 0
    ? `Carried ${r.carried.toLocaleString()} in the last 24 hours.`
    : "None of it came through here in the last 24 hours.";
}
