// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Errors page (nav: "Errors", route: /stuck). One org-wide view of
// everything currently wrong, organised around the two things you care about
// first: Systems in trouble and Integrations in trouble. Each expands to the
// services that are failing and, under them, their failing health checks and
// unacknowledged errors — so you can find the culprit at a glance. A failure
// that belongs to neither an integration nor a system falls into "Other
// services in trouble" so nothing is hidden. Everything here is already scoped
// by the backend to what the signed-in user is allowed to see, and respects
// the "clear errors" acknowledgements.

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useTraceHref } from "../lib/traceHref";
import { api } from "../api/client";
import AlertInstanceActions from "../components/AlertInstanceActions";
import type {
  ErrorsFeedResponse,
  FailingCheck,
  IntegrationRef,
  OpenServiceError,
  ServiceSummary,
} from "../api/types";
import { formatNumber, formatRelative } from "../lib/format";
import { systemKindLabel } from "../lib/systemKinds";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useAccess } from "../lib/useAccess";
import { useInstanceHighlight } from "../lib/useInstanceHighlight";

interface IntegrationGroup {
  ref: IntegrationRef;
  serviceNames: string[]; // troubled member services
  checks: FailingCheck[]; // checks bound to the integration itself
}

export default function StuckMessages() {
  usePageTitle("Errors");
  const { can } = useCurrentUser();
  const access = useAccess();
  // Scoped manage (RBAC v2): ack/clear affordances are per SERVICE — a
  // group-editor may act only on services in their managed scope.
  const orgWrite = can("integration.write");
  const canWriteService = (name: string) => orgWrite || access.canManageService(name);
  const [windowVal] = useTimeWindow();
  const [data, setData] = useState<ErrorsFeedResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    api
      .errorsFeed(windowVal)
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [windowVal]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const checks = useMemo(() => data?.failing_checks ?? [], [data]);
  const openErrors = useMemo(() => data?.open_errors ?? [], [data]);
  // Total unacknowledged error *traces* across services — the tile counts
  // traces (the unit users expect), not the number of affected services.
  const unackedTraces = useMemo(
    () => openErrors.reduce((sum, e) => sum + (e.error_traces ?? 0), 0),
    [openErrors],
  );
  const services = useMemo(() => data?.services ?? [], [data]);
  const counts = data?.counts;

  // Index the raw lists by service so each service block can pull its own
  // failing checks + unacknowledged error in one lookup.
  const checksByService = useMemo(() => {
    const m = new Map<string, FailingCheck[]>();
    for (const c of checks) {
      if (c.target_kind === "service" && c.service_name) {
        const arr = m.get(c.service_name) ?? [];
        arr.push(c);
        m.set(c.service_name, arr);
      }
    }
    return m;
  }, [checks]);

  const openByService = useMemo(() => {
    const m = new Map<string, OpenServiceError>();
    for (const e of openErrors) m.set(e.service_name, e);
    return m;
  }, [openErrors]);

  // Group everything in trouble into systems / integrations / other.
  const grouped = useMemo(() => {
    const summaryByName = new Map(services.map((s) => [s.service_name, s]));

    // The universe of troubled service names: anything with an error status,
    // a service-bound failing check, or an unacknowledged error.
    const troubled = new Set<string>([
      ...services.map((s) => s.service_name),
      ...checksByService.keys(),
      ...openByService.keys(),
    ]);

    const systemNames: string[] = [];
    const integrations = new Map<string, IntegrationGroup>();
    const otherNames: string[] = [];

    for (const name of troubled) {
      const summary = summaryByName.get(name);
      if (summary?.is_system) {
        systemNames.push(name);
        continue;
      }
      const ints = summary?.integrations ?? [];
      if (ints.length > 0) {
        for (const ig of ints) {
          const g =
            integrations.get(ig.id) ?? { ref: ig, serviceNames: [], checks: [] };
          g.serviceNames.push(name);
          integrations.set(ig.id, g);
        }
      } else {
        otherNames.push(name);
      }
    }

    // Integration-bound checks: surface the integration even if no member
    // service is independently in trouble, and attach the check to it.
    for (const c of checks) {
      if (c.target_kind === "integration" && c.integration_id) {
        const g =
          integrations.get(c.integration_id) ?? {
            ref: { id: c.integration_id, slug: "", name: c.integration_name || "integration" },
            serviceNames: [],
            checks: [],
          };
        g.checks.push(c);
        integrations.set(c.integration_id, g);
      }
    }

    const globalChecks = checks.filter((c) => c.target_kind === "global");

    const byName = (a: string, b: string) => a.localeCompare(b);
    systemNames.sort(byName);
    otherNames.sort(byName);
    const integrationGroups = [...integrations.values()].sort((a, b) =>
      a.ref.name.localeCompare(b.ref.name),
    );
    integrationGroups.forEach((g) => {
      g.serviceNames = [...new Set(g.serviceNames)].sort(byName);
    });

    return {
      summaryByName,
      systemNames,
      integrationGroups,
      otherNames,
      globalChecks,
    };
  }, [services, checks, checksByService, openByService]);

  const allClear =
    !loading &&
    !error &&
    checks.length === 0 &&
    openErrors.length === 0 &&
    services.length === 0;

  const renderBlock = (name: string, i: number) => (
    <ServiceBlock
      key={name}
      name={name}
      first={i === 0}
      summary={grouped.summaryByName.get(name)}
      checks={checksByService.get(name) ?? []}
      openErr={openByService.get(name)}
      canWrite={canWriteService(name)}
      onChanged={refresh}
      onError={setError}
    />
  );

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Errors</h1>
          <p className="page__subtitle">
            What's failing right now, grouped by the systems and integrations it
            belongs to — expand to the services and checks underneath. An error
            stays here until it's acknowledged.
          </p>
        </div>
        <div className="toolbar">
          <button className="btn" onClick={refresh} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      <div className="tiles">
        <Tile label="Systems in trouble" value={formatNumber(grouped.systemNames.length)} tone={grouped.systemNames.length > 0 ? "errors" : "ok"} />
        <Tile label="Integrations in trouble" value={formatNumber(grouped.integrationGroups.length)} tone={grouped.integrationGroups.length > 0 ? "errors" : "ok"} />
        <Tile label="Failing health checks" value={formatNumber(counts?.failing_checks ?? 0)} tone={(counts?.failing_checks ?? 0) > 0 ? "errors" : "ok"} />
        <Tile label="Unacked error traces" value={formatNumber(unackedTraces)} tone={unackedTraces > 0 ? "errors" : "ok"} />
      </div>

      {error && <div className="alert alert--error">Failed to load errors: {error}</div>}

      {allClear && (
        <div className="placeholder">
          All clear — nothing failing in the selected window. Anything you've
          cleared won't reappear until new errors arrive.
        </div>
      )}

      {grouped.systemNames.length > 0 && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card__header">Systems in trouble · {grouped.systemNames.length}</div>
          <div style={{ padding: "4px 16px 8px" }}>
            {grouped.systemNames.map(renderBlock)}
          </div>
        </div>
      )}

      {grouped.integrationGroups.length > 0 && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card__header">Integrations in trouble · {grouped.integrationGroups.length}</div>
          <div style={{ padding: "4px 16px 8px" }}>
            {grouped.integrationGroups.map((g, i) => (
              <IntegrationGroupRow
                key={g.ref.id}
                group={g}
                first={i === 0}
                canWrite={orgWrite || access.writeAnywhere}
                onChanged={refresh}
                onError={setError}
                renderBlock={renderBlock}
              />
            ))}
          </div>
        </div>
      )}

      {grouped.otherNames.length > 0 && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card__header">
            Other services in trouble · {grouped.otherNames.length}
            <span className="muted" style={{ marginLeft: 8, fontWeight: 400, fontSize: 13 }}>
              · not part of an integration
            </span>
          </div>
          <div style={{ padding: "4px 16px 8px" }}>
            {grouped.otherNames.map(renderBlock)}
          </div>
        </div>
      )}

      {grouped.globalChecks.length > 0 && (
        <div className="card">
          <div className="card__header">Org-wide checks · {grouped.globalChecks.length}</div>
          <div className="m-existing" style={{ padding: "8px 12px 12px" }}>
            {grouped.globalChecks.map((c) => (
              <CheckRow key={c.id} check={c} canWrite={orgWrite} onChanged={refresh} onError={setError} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// One integration in trouble: a collapsible row (name + affected-service
// count) that expands to its failing member services + integration-level
// checks. Mirrors the single "Integrations in trouble" box like Systems.
function IntegrationGroupRow({
  group,
  first,
  canWrite,
  onChanged,
  onError,
  renderBlock,
}: {
  group: IntegrationGroup;
  first?: boolean;
  canWrite: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
  renderBlock: (name: string, i: number) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const n = group.serviceNames.length;
  return (
    <div style={{ borderTop: first ? undefined : "1px solid var(--border)" }}>
      <div
        role="button"
        tabIndex={0}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setOpen((o) => !o);
          }
        }}
        style={{ display: "flex", alignItems: "center", gap: 8, padding: "10px 0", cursor: "pointer" }}
      >
        <span style={{ width: 12, color: "var(--muted)" }}>{open ? "▾" : "▸"}</span>
        <span style={{ fontWeight: 600 }}>{group.ref.name}</span>
        <span className="muted" style={{ fontSize: 12 }}>
          {n} service{n === 1 ? "" : "s"} in trouble
          {group.checks.length > 0 ? ` · ${group.checks.length} integration check${group.checks.length === 1 ? "" : "s"}` : ""}
        </span>
        <Link
          to={`/integrations/${group.ref.id}`}
          className="m-ex-tgt"
          style={{ marginLeft: "auto" }}
          onClick={(e) => e.stopPropagation()}
        >
          open →
        </Link>
      </div>
      {open && (
        <div style={{ paddingLeft: 20, paddingBottom: 8 }}>
          {group.checks.length > 0 && (
            <div className="m-existing" style={{ marginBottom: 4 }}>
              {group.checks.map((c) => (
                <CheckRow key={c.id} check={c} canWrite={canWrite} onChanged={onChanged} onError={onError} showTarget={false} />
              ))}
            </div>
          )}
          {group.serviceNames.map(renderBlock)}
        </div>
      )}
    </div>
  );
}

// One troubled service: a header (name + system/status badges + a one-line
// count) with its failing checks and unacknowledged error nested underneath.
function ServiceBlock({
  name,
  first,
  summary,
  checks,
  openErr,
  canWrite,
  onChanged,
  onError,
}: {
  name: string;
  first?: boolean;
  summary?: ServiceSummary;
  checks: FailingCheck[];
  openErr?: OpenServiceError;
  canWrite: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
}) {
  const detail: string[] = [];
  if (checks.length) detail.push(`${checks.length} failing check${checks.length === 1 ? "" : "s"}`);
  if (openErr) detail.push(`${formatNumber(openErr.error_traces)} unacked error${openErr.error_traces === 1 ? "" : "s"}`);
  if (!checks.length && !openErr && summary && summary.error_trace_count > 0) {
    detail.push(`${formatNumber(summary.error_trace_count)} error trace${summary.error_trace_count === 1 ? "" : "s"} in window`);
  }
  const hasRows = checks.length > 0 || !!openErr;

  return (
    <div style={{ borderTop: first ? undefined : "1px solid var(--border)", padding: "10px 0" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", marginBottom: hasRows ? 6 : 0 }}>
        <Link to={`/services/${encodeURIComponent(name)}`} style={{ fontWeight: 600 }}>{name}</Link>
        {summary?.is_system && (
          <span className="badge-brand">⚙ {systemKindLabel(summary.system_kind)}</span>
        )}
        {summary && (
          <span className={`m-rule-badge ${summary.status === "unhealthy" ? "sev-critical" : ""}`}>{summary.status}</span>
        )}
        {detail.length > 0 && (
          <span className="muted" style={{ fontSize: 12 }}>{detail.join(" · ")}</span>
        )}
      </div>
      {hasRows && (
        <div className="m-existing">
          {checks.map((c) => (
            <CheckRow key={c.id} check={c} canWrite={canWrite} onChanged={onChanged} onError={onError} showTarget={false} />
          ))}
          {openErr && (
            <OpenErrorRow err={openErr} canWrite={canWrite} onChanged={onChanged} onError={onError} showName={false} />
          )}
        </div>
      )}
    </div>
  );
}

function CheckRow({
  check,
  canWrite,
  onChanged,
  onError,
  showTarget = true,
}: {
  check: FailingCheck;
  canWrite: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
  showTarget?: boolean;
}) {
  // Notification deep links land here with ?instance=<id> — pulse the row.
  const highlight = useInstanceHighlight();
  return (
    <div {...highlight.props(check.id, "m-existing-row")}>
      <div className={`m-ex-bar sev-${check.severity}`} />
      <div className="m-ex-mid">
        <div className="m-ex-name">{check.rule_name}</div>
        <div className="m-ex-cond">
          {showTarget && (
            <>
              <Target check={check} />
              {" · "}
            </>
          )}
          {check.summary ? `${check.summary} · ` : ""}
          {"firing "}
          {formatRelative(check.started_at)}
        </div>
      </div>
      {check.handled_at && (
        <span className="m-rule-badge" title={`Acknowledged ${formatRelative(check.handled_at)}`}>
          acknowledged
        </span>
      )}
      <span className={`m-rule-badge sev-${check.severity}`}>{check.severity}</span>
      {canWrite && (
        <AlertInstanceActions
          instanceId={check.id}
          acknowledged={!!check.handled_at}
          onChanged={onChanged}
          onError={onError}
        />
      )}
    </div>
  );
}

// OpenErrorRow — a persisted unacknowledged error for one service, with an
// Acknowledge action. Acknowledge clears the service's errors (bumps the
// watermark); the row drops off until new errors arrive after that point.
function OpenErrorRow({
  err,
  canWrite,
  onChanged,
  onError,
  showName = true,
}: {
  err: OpenServiceError;
  canWrite: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
  showName?: boolean;
}) {
  const traceHref = useTraceHref();
  const [busy, setBusy] = useState(false);
  const acknowledge = async () => {
    if (!window.confirm(`Acknowledge ${formatNumber(err.error_traces)} error trace${err.error_traces === 1 ? "" : "s"} on ${err.service_name}? It clears the service's error traces; new error traces after this re-open it.`)) {
      return;
    }
    setBusy(true);
    try {
      await api.clearServiceErrors(err.service_name, "Acknowledged from Errors");
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="m-existing-row">
      <div className="m-ex-bar sev-critical" />
      <div className="m-ex-mid">
        <div className="m-ex-name">
          {showName ? (
            <Link to={`/services/${encodeURIComponent(err.service_name)}`}>{err.service_name}</Link>
          ) : (
            "Unacknowledged errors"
          )}
        </div>
        <div className="m-ex-cond">
          {formatNumber(err.error_traces)} unacknowledged error{err.error_traces === 1 ? "" : "s"}
          {" · latest "}
          {formatRelative(err.last_error_at)}
          {" · since "}
          {formatRelative(err.first_error_at)}
        </div>
      </div>
      {err.sample_trace_id && (
        <Link className="m-ex-tgt" to={traceHref(err.sample_trace_id)}>
          view trace →
        </Link>
      )}
      {canWrite && (
        <button type="button" className="btn btn--sm" disabled={busy} onClick={acknowledge}>
          Acknowledge
        </button>
      )}
    </div>
  );
}

// The entity a failing check guards, linked to its detail page.
function Target({ check }: { check: FailingCheck }) {
  if (check.target_kind === "service" && check.service_name) {
    return (
      <Link to={`/services/${encodeURIComponent(check.service_name)}`}>
        {check.service_name}
      </Link>
    );
  }
  if (check.target_kind === "integration" && check.integration_id) {
    return (
      <Link to={`/integrations/${check.integration_id}`}>
        {check.integration_name || "integration"}
      </Link>
    );
  }
  return <span className="muted">Org-wide</span>;
}

function Tile({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "ok" | "errors" | "neutral";
}) {
  return (
    <div className={`tile tile--${tone}`}>
      <div className="tile__value">{value}</div>
      <div className="tile__label">{label}</div>
    </div>
  );
}
