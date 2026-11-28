// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Applying an Advisor suggestion to collectors (stage 3). Each row says
// where the change would go and where it would not, and, for collectors
// that stamp their data, how much of it came through them; those that
// carry it are ticked to begin with. One collector opens in the usual send
// drawer, on its review. Several are reviewed together and proposed
// together: one change per collector, each approved by the team that owns
// that collector, as the design has it.

import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type {
  AdvisorCollectorChange,
  AdvisorCollectorRow,
  AdvisorSuggestion,
  CollectorValidation,
} from "../../api/types";
import { EditDrawer } from "../primitives";
import ConfigDiff from "./ConfigDiff";
import DiffExport from "./DiffExport";
import SendConfigDrawer from "./SendConfigDrawer";

type Prepared = {
  row: AdvisorCollectorRow;
  change: AdvisorCollectorChange;
  validation: CollectorValidation | null;
  checked: boolean;
  errors: number;
};

type Outcome = { row: AdvisorCollectorRow; ok: boolean; note: string };

const nameOf = (r: AdvisorCollectorRow) =>
  r.collector.description["host.name"] || r.collector.instance_uid;

const applies = (r: AdvisorCollectorRow) =>
  !r.unavailable && !r.already_dropped && r.changed.length > 0;

// A collector whose configuration the team's pipeline owns gets the change
// as a diff instead of a proposal.
const pipelineOwned = (r: AdvisorCollectorRow) =>
  r.collector.config_owner === "pipeline";

const usable = (r: AdvisorCollectorRow) => applies(r) && !pipelineOwned(r);

export default function AdvisorCollectorDrawer({
  suggestion,
  onClose,
}: {
  suggestion: AdvisorSuggestion;
  onClose: () => void;
}) {
  const [rows, setRows] = useState<AdvisorCollectorRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Personal data (T6): deleted by default; hashing keeps "the same value
  // twice" matching, and is pseudonymous rather than anonymous.
  const personal = suggestion.class === "T6";
  const [hash, setHash] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [single, setSingle] = useState<{
    row: AdvisorCollectorRow;
    change: AdvisorCollectorChange;
  } | null>(null);
  const [prepared, setPrepared] = useState<Prepared[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null);
  const [diffFor, setDiffFor] = useState<{
    id: string;
    change: AdvisorCollectorChange;
  } | null>(null);

  const showDiff = async (r: AdvisorCollectorRow) => {
    if (diffFor?.id === r.collector.id) {
      setDiffFor(null);
      return;
    }
    setError(null);
    try {
      const change = await preview(r);
      if (change.unavailable || !change.config) {
        setError(change.unavailable ?? "Nothing to change on this collector.");
        return;
      }
      setDiffFor({ id: r.collector.id, change });
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  useEffect(() => {
    api
      .listAdvisorSuggestionCollectors(suggestion.id)
      .then((r) => {
        setRows(r.collectors);
        // The collectors that carry it, by their stamp, to begin with.
        setPicked(
          new Set(
            r.collectors
              .filter((c) => usable(c) && (c.carried ?? 0) > 0)
              .map((c) => c.collector.id),
          ),
        );
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [suggestion.id]);

  const chosen = useMemo(
    () => (rows ?? []).filter((r) => picked.has(r.collector.id)),
    [rows, picked],
  );

  const toggle = (id: string) =>
    setPicked((p) => {
      const n = new Set(p);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const preview = (r: AdvisorCollectorRow) =>
    api.previewAdvisorCollectorChange(
      suggestion.id,
      r.collector.id,
      personal && hash,
    );

  const review = async () => {
    setBusy(true);
    setError(null);
    try {
      if (chosen.length === 1) {
        const change = await preview(chosen[0]);
        if (change.unavailable || !change.config) {
          setError(
            change.unavailable ?? "Nothing to change on this collector.",
          );
          return;
        }
        setSingle({ row: chosen[0], change });
        return;
      }
      const out: Prepared[] = [];
      for (const row of chosen) {
        const change = await preview(row);
        if (change.unavailable || !change.config) {
          throw new Error(
            `${nameOf(row)}: ${change.unavailable ?? "nothing to change"}`,
          );
        }
        const v = await api.validateCollectorConfig(
          row.collector.id,
          change.config,
        );
        const errors = (v.validation?.diagnostics ?? []).filter(
          (d) => d.severity === "error",
        ).length;
        out.push({
          row,
          change,
          validation: v.validation ?? null,
          checked: v.checked,
          errors,
        });
      }
      setPrepared(out);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const proposeAll = async () => {
    if (!prepared) return;
    setBusy(true);
    const results: Outcome[] = [];
    for (const p of prepared) {
      try {
        await api.sendCollectorConfig(p.row.collector.id, {
          config: p.change.config,
          message,
          force: p.errors > 0,
          suggestion_id: suggestion.id,
          suggestion_hash: personal && hash,
        });
        results.push({
          row: p.row,
          ok: true,
          note: `Proposed to ${p.row.collector.group_name}.`,
        });
      } catch (e) {
        results.push({
          row: p.row,
          ok: false,
          note: String((e as Error).message ?? e),
        });
      }
    }
    setOutcomes(results);
    setBusy(false);
  };

  if (single) {
    const c = single.row.collector;
    return (
      <SendConfigDrawer
        collectorId={c.id}
        collectorName={nameOf(single.row)}
        current={single.change.before}
        initial={single.change.config}
        suggestionId={suggestion.id}
        suggestionHash={personal && hash}
        requireApproval
        onClose={onClose}
        onSent={() => undefined}
      />
    );
  }

  const withErrors = (prepared ?? []).filter((p) => p.errors > 0).length;

  return (
    <EditDrawer
      title={prepared ? "Review the changes" : "Apply to collectors"}
      width={prepared ? "wide" : "medium"}
      onClose={onClose}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {outcomes ? (
          <>
            <div className="mtbl">
              <div className="mtbl-body">
                {outcomes.map((o) => (
                  <div
                    key={o.row.collector.id}
                    className="mtbl-row"
                    style={{
                      display: "grid",
                      gridTemplateColumns: "1fr 2fr",
                      gap: 8,
                    }}
                  >
                    <div>{nameOf(o.row)}</div>
                    <div
                      className={o.ok ? "muted" : undefined}
                      style={{
                        fontSize: 12.5,
                        color: o.ok ? undefined : "var(--err-ink)",
                      }}
                    >
                      {o.note}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <div className="alert alert--info" style={{ margin: 0 }}>
              Each change waits for approval by the team that owns its
              collector. <Link to="/proposals">Open Proposals</Link>
            </div>
            <div>
              <button className="btn" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        ) : prepared ? (
          <>
            <p className="muted" style={{ margin: 0, fontSize: 13 }}>
              <strong>{suggestion.title}.</strong> One change per collector,
              each proposed to the team that owns it.
            </p>
            <div className="mtbl">
              <div className="mtbl-body">
                {prepared.map((p) => (
                  <div key={p.row.collector.id}>
                    <div
                      className="mtbl-row"
                      style={{
                        display: "grid",
                        gridTemplateColumns: "1.2fr 1.8fr auto",
                        gap: 8,
                        alignItems: "center",
                      }}
                    >
                      <div style={{ minWidth: 0 }}>
                        <div className="truncate">{nameOf(p.row)}</div>
                        <div className="muted" style={{ fontSize: 12 }}>
                          {p.row.collector.group_name}
                        </div>
                      </div>
                      <div style={{ fontSize: 12.5 }}>
                        {!p.checked ? (
                          <span className="muted">
                            Not checked here: OTelFlow is not available to this
                            cell.
                          </span>
                        ) : p.errors > 0 ? (
                          <span style={{ color: "var(--err-ink)" }}>
                            OTelFlow found {p.errors} error
                            {p.errors === 1 ? "" : "s"}
                          </span>
                        ) : (
                          <span className="muted">
                            OTelFlow found no errors
                            {p.validation?.checked_version
                              ? ` (collector v${p.validation.checked_version})`
                              : ""}
                            .
                          </span>
                        )}
                      </div>
                      <button
                        className="btn btn--sm"
                        onClick={() =>
                          setOpen(
                            open === p.row.collector.id
                              ? null
                              : p.row.collector.id,
                          )
                        }
                      >
                        {open === p.row.collector.id
                          ? "Hide change"
                          : "Show change"}
                      </button>
                    </div>
                    {open === p.row.collector.id && (
                      <div style={{ padding: "8px 12px 14px" }}>
                        <ConfigDiff
                          before={p.change.before}
                          after={p.change.config}
                        />
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
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
                placeholder="Optional: the Advisor's finding is used when left empty"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
              />
            </label>
            {error && <div className="alert alert--error">{error}</div>}
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <button
                className="btn primary"
                disabled={busy}
                onClick={proposeAll}
              >
                {busy
                  ? "Proposing…"
                  : withErrors > 0
                    ? `Propose ${prepared.length} changes despite errors`
                    : `Propose ${prepared.length} changes`}
              </button>
              <button
                className="btn"
                disabled={busy}
                onClick={() => setPrepared(null)}
              >
                Back
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="muted" style={{ margin: 0, fontSize: 13 }}>
              <strong>{suggestion.title}.</strong> Collectors that stamp their
              data say how much of it came through them, and those that carry it
              are ticked; for the others Sluicio cannot tell. Each change goes
              only into pipelines that send to Sluicio alone, and to the team
              that owns the collector for approval.
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
                    pseudonymisation, not anonymisation: an ID number or an
                    email address can be found again by hashing candidates.
                  </span>
                )}
              </label>
            )}
            {error && <div className="alert alert--error">{error}</div>}
            {!rows && !error && <div className="placeholder">Loading…</div>}
            {rows && rows.length === 0 && (
              <div className="muted" style={{ fontSize: 13 }}>
                No collector runs under the supervisor, so none can be changed
                from Sluicio. Copy the snippet into the collector&apos;s
                configuration instead.
              </div>
            )}
            {rows && rows.length > 0 && (
              <div className="mtbl">
                <div
                  className="mtbl-head"
                  style={{ gridTemplateColumns: "auto 1.2fr 1.6fr", gap: 8 }}
                >
                  <div />
                  <div>Collector</div>
                  <div>Change</div>
                </div>
                <div className="mtbl-body">
                  {rows.map((r) => (
                    <label
                      key={r.collector.id}
                      className="mtbl-row"
                      style={{
                        display: "grid",
                        gridTemplateColumns: "auto 1.2fr 1.6fr",
                        gap: 8,
                        alignItems: "center",
                        cursor: usable(r) ? "pointer" : "default",
                      }}
                    >
                      <input
                        type="checkbox"
                        disabled={!usable(r)}
                        checked={picked.has(r.collector.id)}
                        onChange={() => toggle(r.collector.id)}
                        aria-label={`Apply to ${nameOf(r)}`}
                      />
                      <div style={{ minWidth: 0 }}>
                        <div className="truncate">{nameOf(r)}</div>
                        <div className="muted" style={{ fontSize: 12 }}>
                          {r.collector.group_name}
                        </div>
                      </div>
                      <div className="muted" style={{ fontSize: 12.5 }}>
                        <div>{carriedLine(r)}</div>
                        {r.already_dropped
                          ? "Has this change already."
                          : r.unavailable
                            ? `Not here: ${r.unavailable}.`
                            : `Pipelines: ${r.changed.join(", ")}`}
                        {applies(r) && (r.scrape_jobs?.length ?? 0) > 0 && (
                          <div>
                            Dropped where it is scraped:{" "}
                            {scrapeJobsLine(r.scrape_jobs ?? [])}
                          </div>
                        )}
                        {applies(r) && pipelineOwned(r) && (
                          <div style={{ marginTop: 4 }}>
                            Owned by the team&apos;s pipeline.{" "}
                            <button
                              type="button"
                              className="btn btn--sm"
                              onClick={(e) => {
                                e.preventDefault();
                                void showDiff(r);
                              }}
                            >
                              {diffFor?.id === r.collector.id
                                ? "Hide the diff"
                                : "Get the diff"}
                            </button>
                          </div>
                        )}
                        {diffFor?.id === r.collector.id && (
                          <div style={{ marginTop: 8 }}>
                            <DiffExport
                              before={diffFor.change.before}
                              after={diffFor.change.config}
                              file={`${nameOf(r)}.yaml`}
                            />
                          </div>
                        )}
                        {applies(r) && r.skipped.length > 0 && (
                          <div>
                            Left alone:{" "}
                            {r.skipped
                              .map((s) => `${s.pipeline} (${s.reason})`)
                              .join("; ")}
                          </div>
                        )}
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            )}
            {rows && rows.length > 0 && (
              <div>
                <button
                  className="btn primary"
                  disabled={busy || chosen.length === 0}
                  onClick={review}
                >
                  {busy
                    ? "Preparing…"
                    : chosen.length > 1
                      ? `Review ${chosen.length} changes`
                      : "Review the change"}
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </EditDrawer>
  );
}

// scrapeJobsLine names the scrape jobs a metric is dropped in.
function scrapeJobsLine(jobs: { receiver: string; job: string }[]): string {
  return jobs.map((j) => `${j.job} (${j.receiver})`).join(", ");
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
