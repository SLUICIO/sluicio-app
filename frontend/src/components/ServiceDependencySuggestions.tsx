// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceDependencySuggestions surfaces the trace-graph neighbors of a
// focal service so the user can fold them into an integration with a
// single click. It powers the "if you add document-intake, you almost
// certainly want these too" affordance on both the new-integration
// form and the integration-detail configuration panel.
//
// Mental model: the trace graph already knows who talks to whom — we
// just have to show that to the user at the moment they're declaring
// which services belong to a flow. Each suggestion, when accepted,
// becomes an `equals` matcher on the integration (created or staged).
//
// The component owns its own data fetch (so the parent only has to
// pass the focal service name and the already-covered list) and
// emits `onAdd(names[])` when the user confirms a selection. The
// caller decides how to turn names into matchers — staging them in
// a draft form (new integration) or POSTing them as matchers
// individually (existing integration).

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import { formatNumber } from "../lib/format";
import type { NeighborsResponse, ServiceNeighbor } from "../api/types";

interface Props {
  // The focal service whose neighbors we're suggesting. When null/
  // empty the component renders nothing — saves the parent from a
  // conditional render.
  serviceName: string | null;
  // The current time window from the page (e.g. "1h", "24h", or an
  // ISO/ISO absolute range). Reused as-is — the suggestions track
  // whatever traffic window the rest of the page is showing.
  window: string;
  // Service names already covered by the integration's existing
  // matchers. These are filtered out of both lists so the user only
  // sees actionable suggestions. Pass an empty array when nothing is
  // covered yet (new-integration flow before submit).
  alreadyCovered: string[];
  // Optional coverage predicate: returns true when a candidate service
  // is already matched by one of the integration's matcher rows (equals,
  // prefix, suffix, contains, regex). This catches services that are
  // covered by a matcher but have no traffic in the window — so they're
  // absent from `alreadyCovered` (which is the resolved/active set) yet
  // shouldn't be suggested. Filtered out in addition to alreadyCovered.
  covers?: (serviceName: string) => boolean;
  // Called when the user confirms a selection. Receives the chosen
  // service names — both directions are merged because the caller
  // turns each into an `equals` matcher anyway.
  onAdd: (names: string[]) => void;
  // Surface text customizable per caller — "Add to integration" reads
  // naturally on edit; "Include in this integration" reads better on
  // the new-integration form where there's no integration yet.
  addButtonLabel?: string;
}

/**
 * ServiceDependencySuggestions fetches the focal service's upstream
 * (callers) and downstream (callees) neighbors from the trace graph
 * and renders them as two checkable lists with trace counts. The
 * user picks any subset and hits Add; the chosen names are returned
 * to the parent via onAdd.
 *
 * Behaviour notes:
 *   - Both lists are shown in full (no relevance threshold) per the
 *     product call; the trace count is printed next to each row so
 *     the user can deprioritize visually.
 *   - already-covered services are filtered out client-side using
 *     the pre-resolved name list the parent already has.
 *   - An empty response (orphan service, quiet window) shows a
 *     muted "no dependencies detected in this window" line so users
 *     understand the absence is real rather than a load failure.
 */
export default function ServiceDependencySuggestions({
  serviceName,
  window,
  alreadyCovered,
  covers,
  onAdd,
  addButtonLabel = "Add to integration",
}: Props) {
  const [data, setData] = useState<NeighborsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Selected service names, irrespective of direction. Cleared when the
  // focal service changes so we don't carry stale selections between
  // services.
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // Stable string key for the alreadyCovered list — JSON.stringify
  // gives a value-equality dep so the effect doesn't re-fire on every
  // parent render that hands us a new array identity.
  const coveredKey = useMemo(
    () => alreadyCovered.slice().sort().join("|"),
    [alreadyCovered],
  );

  // Fetch neighbors whenever the focal service or window changes.
  // We don't refetch when alreadyCovered changes — that's purely a
  // post-fetch filter applied during render.
  useEffect(() => {
    if (!serviceName) {
      setData(null);
      setError(null);
      setSelected(new Set());
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setSelected(new Set());
    api
      .serviceNeighbors(serviceName, window)
      .then((r) => {
        if (!cancelled) setData(r);
      })
      .catch((e) => {
        if (!cancelled) setError(String((e as Error).message ?? e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [serviceName, window]);

  const coveredSet = useMemo(() => new Set(alreadyCovered), [coveredKey]); // eslint-disable-line react-hooks/exhaustive-deps

  // Drop services that are already covered by existing matchers and
  // (defensively) the focal service itself — the SQL drops self-hops,
  // but the focal service should never be a suggestion against itself.
  const isCovered = (name: string) =>
    coveredSet.has(name) || (covers?.(name) ?? false);
  const visibleUpstream = useMemo(
    () =>
      (data?.upstream ?? []).filter(
        (n) => n.service_name !== serviceName && !isCovered(n.service_name),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, coveredSet, serviceName, covers],
  );
  const visibleDownstream = useMemo(
    () =>
      (data?.downstream ?? []).filter(
        (n) => n.service_name !== serviceName && !isCovered(n.service_name),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, coveredSet, serviceName, covers],
  );

  // Don't render at all when there's no focal service. The parent can
  // render this component unconditionally and rely on this short-circuit.
  if (!serviceName) return null;

  const toggle = (name: string) => {
    setSelected((curr) => {
      const next = new Set(curr);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };

  const onConfirm = () => {
    if (selected.size === 0) return;
    onAdd(Array.from(selected));
    setSelected(new Set());
  };

  const upstreamTotal = visibleUpstream.length;
  const downstreamTotal = visibleDownstream.length;
  const totalShown = upstreamTotal + downstreamTotal;

  return (
    <div
      className="rounded-lg border bg-surface-2 p-3"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-baseline justify-between gap-2">
        <div>
          <div className="text-sm font-semibold">
            Suggested dependencies for{" "}
            <span className="mono">{serviceName}</span>
          </div>
          <p className="text-xs text-muted">
            Pulled from traces in the current window. Pick the ones that
            should be part of this integration — each becomes an{" "}
            <span className="mono">equals</span> matcher.
          </p>
        </div>
        <button
          type="button"
          className="btn btn--primary"
          onClick={onConfirm}
          disabled={selected.size === 0}
        >
          {addButtonLabel}
          {selected.size > 0 ? ` (${selected.size})` : ""}
        </button>
      </div>

      {loading && (
        <div className="muted" style={{ fontSize: 12, marginTop: 8 }}>
          Looking at traces…
        </div>
      )}
      {error && (
        <div className="alert alert--error" style={{ marginTop: 8 }}>
          Couldn't load dependency suggestions: {error}
        </div>
      )}
      {!loading && !error && totalShown === 0 && (
        <div className="muted" style={{ fontSize: 12, marginTop: 8 }}>
          {data && (data.upstream.length > 0 || data.downstream.length > 0)
            ? "Every direct neighbor of this service is already covered by an existing matcher."
            : "No service-to-service hops observed in this window. Try a wider time range, or this service may be a true leaf."}
        </div>
      )}

      {totalShown > 0 && (
        <div
          className="grid grid-cols-1 gap-3 md:grid-cols-2"
          style={{ marginTop: 10 }}
        >
          <NeighborList
            title="Calls into this service (upstream)"
            empty="Nothing calls into it directly in this window."
            neighbors={visibleUpstream}
            selected={selected}
            onToggle={toggle}
          />
          <NeighborList
            title="Called by this service (downstream)"
            empty="It doesn't call other services in this window."
            neighbors={visibleDownstream}
            selected={selected}
            onToggle={toggle}
          />
        </div>
      )}
    </div>
  );
}

interface NeighborListProps {
  title: string;
  empty: string;
  neighbors: ServiceNeighbor[];
  selected: Set<string>;
  onToggle: (name: string) => void;
}

function NeighborList({
  title,
  empty,
  neighbors,
  selected,
  onToggle,
}: NeighborListProps) {
  return (
    <div>
      <div
        className="muted"
        style={{
          fontSize: 11,
          textTransform: "uppercase",
          letterSpacing: 0.5,
          marginBottom: 4,
        }}
      >
        {title}
      </div>
      {neighbors.length === 0 ? (
        <div className="muted" style={{ fontSize: 12 }}>
          {empty}
        </div>
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {neighbors.map((n) => {
            const isSelected = selected.has(n.service_name);
            return (
              <li
                key={n.service_name}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "4px 6px",
                  borderRadius: 4,
                  background: isSelected ? "var(--surface-3, transparent)" : "transparent",
                }}
              >
                <label
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 8,
                    cursor: "pointer",
                    flex: 1,
                    minWidth: 0,
                  }}
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => onToggle(n.service_name)}
                  />
                  <span
                    className="mono"
                    style={{
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      flex: 1,
                    }}
                    title={n.service_name}
                  >
                    {n.service_name}
                  </span>
                </label>
                <span
                  className="muted"
                  style={{ fontSize: 11, fontVariantNumeric: "tabular-nums" }}
                  title={`${formatNumber(n.trace_count)} traces · ${formatNumber(
                    n.error_count,
                  )} with errors`}
                >
                  {formatNumber(n.trace_count)}
                  {n.error_count > 0 && (
                    <span
                      style={{ color: "var(--err)", marginLeft: 6 }}
                      title={`${formatNumber(n.error_count)} error traces`}
                    >
                      ⚠ {formatNumber(n.error_count)}
                    </span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
