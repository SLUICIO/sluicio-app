// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Collectors (Beta): the OpenTelemetry Collectors that connect to this
// cell over OpAMP, team by team. Read only in this stage: what each
// collector is, whether it is up and healthy, and the configuration it
// reports. A team's admins create the tokens that put a collector in
// their team.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { Collector, CollectorTeam, CollectorToken } from "../api/types";
import { formatDateTime, formatRelative } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useFeature } from "../lib/useLicense";
import { EnterpriseBadge } from "../components/EnterpriseUpsell";
import { copyText } from "../lib/clipboard";
import StatusPip from "../components/primitives/StatusPip";
import type { PipKind } from "../components/primitives/StatusPip";
import { EditDrawer } from "../components/primitives";
import BetaBadge from "../components/BetaBadge";

export function collectorName(c: Collector): string {
  return c.description["host.name"] || c.instance_uid;
}

export function collectorStatus(c: Collector): {
  kind: PipKind;
  label: string;
} {
  if (!c.online) return { kind: "muted", label: "Offline" };
  if (c.healthy === false) return { kind: "err", label: "Unhealthy" };
  if (c.healthy === true) return { kind: "ok", label: "Healthy" };
  return { kind: "ok", label: "Online" };
}

export function KindBadge({ kind }: { kind: Collector["kind"] }) {
  return kind === "supervisor" ? (
    <span
      className="badge-brand"
      title="Runs under the OpAMP supervisor and accepts remote configuration"
    >
      Supervisor
    </span>
  ) : (
    <span
      className="m-rule-badge sev-info"
      title="The OpAMP extension: reports status and configuration, takes none (read only)"
    >
      Extension
    </span>
  );
}

const COLS = "2fr 1.2fr 1fr 1fr 1fr";

export default function Collectors() {
  usePageTitle("Collectors");
  const { collectorsEnabled } = useCurrentUser();
  const [collectors, setCollectors] = useState<Collector[] | null>(null);
  const [teams, setTeams] = useState<CollectorTeam[]>([]);
  const [configStorage, setConfigStorage] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tokensFor, setTokensFor] = useState<CollectorTeam | null>(null);

  const refresh = useCallback(() => {
    setError(null);
    Promise.all([api.listCollectors(), api.listCollectorTeams()])
      .then(([c, t]) => {
        setCollectors(c.collectors);
        setConfigStorage(c.config_storage);
        setTeams(t.teams);
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, []);
  useEffect(() => {
    if (collectorsEnabled) refresh();
  }, [refresh, collectorsEnabled]);

  const byTeam = useMemo(() => {
    const m = new Map<string, Collector[]>();
    for (const c of collectors ?? [])
      m.set(c.group_id, [...(m.get(c.group_id) ?? []), c]);
    return m;
  }, [collectors]);

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">
            Collectors
            <BetaBadge />
          </h1>
          <p className="page__subtitle">
            OpenTelemetry Collectors that report to this cell over OpAMP, by
            team. A collector joins the team whose token it connects with.
            Configurations are shown as the collector reports them, with secrets
            masked.
          </p>
        </div>
        <div className="toolbar">
          <button className="btn" onClick={refresh}>
            Refresh
          </button>
        </div>
      </div>

      {!collectorsEnabled && (
        <div className="placeholder">
          Collector management is not turned on for this cell. Whoever runs it
          sets <code>SLUICIO_COLLECTORS_ENABLED</code>.
        </div>
      )}
      {collectorsEnabled && !configStorage && (
        <div className="alert alert--warn" style={{ marginBottom: 12 }}>
          Configurations are not stored on this cell:{" "}
          <code>SLUICIO_COLLECTOR_CONFIG_KEY</code> is not set. Collectors still
          register and report their status.
        </div>
      )}
      {error && (
        <div className="alert alert--error">
          Failed to load collectors: {error}
        </div>
      )}
      {collectorsEnabled && !error && collectors === null && (
        <div className="placeholder">Loading…</div>
      )}
      {collectors !== null && teams.length === 0 && (
        <div className="placeholder">
          You are not in a team yet. Every collector belongs to a team; ask an
          admin to add you to one.
        </div>
      )}

      <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
        {collectors !== null &&
          // Teams with collectors first, so an org with many teams does
          // not open on a page of empty ones.
          [...teams]
            .sort(
              (a, b) =>
                Number(byTeam.has(b.id)) - Number(byTeam.has(a.id)) ||
                a.name.localeCompare(b.name),
            )
            .map((team) => {
              const list = byTeam.get(team.id) ?? [];
              const online = list.filter((c) => c.online).length;
              return (
                <section
                  key={team.id}
                  className="rounded-lg border bg-surface-2"
                >
                  <div
                    className="border-b border-border px-4 py-3"
                    style={{ display: "flex", alignItems: "center", gap: 12 }}
                  >
                    <div>
                      <h2 style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>
                        {team.name}
                      </h2>
                      <div className="muted" style={{ fontSize: 12.5 }}>
                        {list.length === 0
                          ? "No collectors yet"
                          : `${list.length} collector${list.length === 1 ? "" : "s"}, ${online} online`}
                      </div>
                    </div>
                    {team.can_manage && (
                      <div style={{ marginLeft: "auto" }}>
                        <button
                          className="btn"
                          onClick={() => setTokensFor(team)}
                        >
                          Team settings
                        </button>
                      </div>
                    )}
                  </div>
                  {list.length === 0 ? (
                    <div
                      className="muted"
                      style={{ padding: "12px 16px", fontSize: 13 }}
                    >
                      {team.can_manage
                        ? "Create a token, then add the OpAMP extension or the supervisor to a collector with it."
                        : "A team admin creates the token a collector connects with."}
                    </div>
                  ) : (
                    <div className="mtbl">
                      <div
                        className="mtbl-head"
                        style={{ gridTemplateColumns: COLS }}
                      >
                        <div>Collector</div>
                        <div>Type</div>
                        <div>Status</div>
                        <div>Version</div>
                        <div>Last seen</div>
                      </div>
                      <div className="mtbl-body">
                        {list.map((c) => {
                          const st = collectorStatus(c);
                          return (
                            <Link
                              key={c.id}
                              to={`/collectors/${c.id}`}
                              className="mtbl-row"
                              style={{
                                display: "grid",
                                gridTemplateColumns: COLS,
                                alignItems: "center",
                                textDecoration: "none",
                                color: "inherit",
                              }}
                            >
                              <div style={{ minWidth: 0 }}>
                                <div
                                  style={{ fontWeight: 600 }}
                                  className="truncate"
                                >
                                  {collectorName(c)}
                                </div>
                                <div
                                  className="mono muted truncate"
                                  style={{ fontSize: 11.5 }}
                                >
                                  {c.instance_uid}
                                </div>
                              </div>
                              <div>
                                <KindBadge kind={c.kind} />
                              </div>
                              <div>
                                <StatusPip kind={st.kind} label={st.label} />
                              </div>
                              <div className="mono" style={{ fontSize: 12.5 }}>
                                {c.description["service.version"] || "-"}
                              </div>
                              <div title={formatDateTime(c.last_seen_at)}>
                                {formatRelative(c.last_seen_at)}
                              </div>
                            </Link>
                          );
                        })}
                      </div>
                    </div>
                  )}
                </section>
              );
            })}
      </div>

      {tokensFor && (
        <TokensDrawer team={tokensFor} onClose={() => setTokensFor(null)} />
      )}
    </div>
  );
}

// opampEndpoint is where collectors connect: this page's own host, under
// /api, which every deployment's proxy already routes to cell-api.
function opampEndpoint(): string {
  const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${wsScheme}//${window.location.host}/api/v1/opamp`;
}

// Whether a change to the team's collectors waits for approval by someone
// other than its author before it is sent (Enterprise).
function ApprovalSetting({ teamId }: { teamId: string }) {
  const entitled = useFeature("advisor");
  const [value, setValue] = useState<boolean | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api
      .getCollectorTeamSettings(teamId)
      .then((r) => setValue(r.require_approval))
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [teamId]);
  const toggle = async (v: boolean) => {
    setError(null);
    try {
      await api.putCollectorTeamSettings(teamId, v);
      setValue(v);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };
  return (
    <div>
      <h3 style={{ margin: "0 0 6px", fontSize: 14 }}>
        Changes {!entitled && <EnterpriseBadge />}
      </h3>
      <label
        style={{
          display: "flex",
          gap: 8,
          alignItems: "flex-start",
          fontSize: 13,
        }}
      >
        <input
          type="checkbox"
          checked={!!value}
          disabled={!entitled || value === null}
          onChange={(e) => toggle(e.target.checked)}
          style={{ marginTop: 2 }}
        />
        <span>
          Require approval before a configuration is sent to this team&apos;s
          collectors. A change is then proposed, and someone other than its
          author approves it in Proposals.
        </span>
      </label>
      {error && (
        <div className="alert alert--error" style={{ marginTop: 6 }}>
          {error}
        </div>
      )}
    </div>
  );
}

function TokensDrawer({
  team,
  onClose,
}: {
  team: CollectorTeam;
  onClose: () => void;
}) {
  const [tokens, setTokens] = useState<CollectorToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<{ token: string; name: string } | null>(
    null,
  );
  const [copied, setCopied] = useState(false);

  const refresh = useCallback(() => {
    api
      .listCollectorTokens(team.id)
      .then((r) => setTokens(r.tokens))
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [team.id]);
  useEffect(refresh, [refresh]);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const create = () =>
    run(async () => {
      const res = await api.createCollectorToken(team.id, name.trim());
      setShown({ token: res.token, name: res.meta.name });
      setCopied(false);
      setName("");
      refresh();
    });

  const regenerate = (t: CollectorToken) => {
    if (
      !confirm(
        `Regenerate "${t.name}"? The current token stops working now, and collectors using it disconnect.`,
      )
    )
      return;
    run(async () => {
      const res = await api.regenerateCollectorToken(team.id, t.id);
      setShown({ token: res.token, name: res.meta.name });
      setCopied(false);
      refresh();
    });
  };

  const revoke = (t: CollectorToken) => {
    if (
      !confirm(
        `Revoke "${t.name}"? Collectors using it disconnect and cannot reconnect with it.`,
      )
    )
      return;
    run(async () => {
      await api.revokeCollectorToken(team.id, t.id);
      refresh();
    });
  };

  const snippet = (token: string) =>
    [
      "extensions:",
      "  opamp:",
      "    server:",
      "      ws:",
      `        endpoint: ${opampEndpoint()}`,
      "        headers:",
      `          Authorization: "Bearer ${token}"`,
      "    reports_raw_config: true",
      "    capabilities:",
      "      reports_effective_config: true",
      "      reports_health: true",
      "",
      "service:",
      "  extensions: [opamp]",
    ].join("\n");

  return (
    <EditDrawer
      title={`Collector settings: ${team.name}`}
      width="medium"
      onClose={onClose}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        <ApprovalSetting teamId={team.id} />
        <h3 style={{ margin: "4px 0 0", fontSize: 14 }}>Tokens</h3>
        <p className="muted" style={{ margin: 0, fontSize: 13 }}>
          A collector sends its token as{" "}
          <code>Authorization: Bearer &lt;token&gt;</code> to the OpAMP
          endpoint, and joins this team. Keep several tokens to rotate without
          an outage: create a new one, switch the collectors, then revoke the
          old one.
        </p>

        {shown && (
          <div
            className="card"
            style={{ padding: 14, borderColor: "var(--primary)" }}
          >
            <div style={{ fontWeight: 600, marginBottom: 4 }}>
              Token “{shown.name}”: copy it now
            </div>
            <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
              This is the only time the full token is shown.
            </div>
            <div
              className="mono"
              style={{
                wordBreak: "break-all",
                background: "var(--surface)",
                padding: "8px 10px",
                borderRadius: 6,
                border: "1px solid var(--border)",
              }}
            >
              {shown.token}
            </div>
            <div
              style={{
                display: "flex",
                gap: 8,
                marginTop: 8,
                alignItems: "center",
              }}
            >
              <button
                type="button"
                className="btn"
                onClick={async () => setCopied(await copyText(shown.token))}
              >
                {copied ? "Copied" : "Copy token"}
              </button>
              <button
                type="button"
                className="btn btn--link"
                style={{ marginLeft: "auto" }}
                onClick={() => setShown(null)}
              >
                Dismiss
              </button>
            </div>
            <div
              className="muted"
              style={{ fontSize: 12, margin: "12px 0 6px" }}
            >
              The OpAMP extension in the collector's configuration (read only).
              Better: set the token as an environment variable and reference it
              with <code>{"${env:SLUICIO_COLLECTOR_TOKEN}"}</code>.
            </div>
            <pre
              className="mono"
              style={{
                margin: 0,
                fontSize: 12,
                background: "var(--surface)",
                padding: 10,
                borderRadius: 6,
                overflowX: "auto",
              }}
            >
              {snippet(shown.token)}
            </pre>
          </div>
        )}

        {error && <div className="alert alert--error">{error}</div>}

        <form
          style={{ display: "flex", gap: 8 }}
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) create();
          }}
        >
          <input
            className="search__input"
            placeholder="Token name, e.g. production hosts"
            value={name}
            onChange={(e) => setName(e.target.value)}
            style={{ flex: 1 }}
          />
          <button
            className="btn primary"
            type="submit"
            disabled={busy || !name.trim()}
          >
            Create token
          </button>
        </form>

        {tokens === null ? (
          <div className="placeholder">Loading…</div>
        ) : tokens.length === 0 ? (
          <div className="muted" style={{ fontSize: 13 }}>
            No tokens yet.
          </div>
        ) : (
          <div className="mtbl">
            <div
              className="mtbl-head"
              style={{ gridTemplateColumns: "1.4fr 1fr 1fr auto", gap: 8 }}
            >
              <div>Name</div>
              <div>Created</div>
              <div>Last used</div>
              <div />
            </div>
            <div className="mtbl-body">
              {tokens.map((t) => (
                <div
                  key={t.id}
                  className="mtbl-row"
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1.4fr 1fr 1fr auto",
                    alignItems: "center",
                    gap: 8,
                  }}
                >
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 600 }} className="truncate">
                      {t.name}
                    </div>
                    <div className="mono muted" style={{ fontSize: 11.5 }}>
                      {t.prefix}…
                    </div>
                  </div>
                  <div title={formatDateTime(t.created_at)}>
                    {formatRelative(t.created_at)}
                  </div>
                  <div>
                    {t.last_used_at ? formatRelative(t.last_used_at) : "Never"}
                  </div>
                  <div style={{ display: "flex", gap: 6 }}>
                    <button
                      className="btn btn--sm"
                      disabled={busy}
                      onClick={() => regenerate(t)}
                    >
                      Regenerate
                    </button>
                    <button
                      className="btn btn--sm"
                      disabled={busy}
                      onClick={() => revoke(t)}
                    >
                      Revoke
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </EditDrawer>
  );
}
