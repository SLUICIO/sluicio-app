// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a row's environment is read from (issue #40): the resource
// attribute, and aliases folding several spellings into one name. Read
// at query time, so a change applies to everything at once and rewrites
// nothing. And which environments are production: a role per
// environment, confirmed here and suggested by the name until then, that
// integration pages open on and dashboards, checks and notification
// profiles can name as "production environments".

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { EnvironmentRole, TelemetryEnvironmentSetting } from "../api/types";

function aliasesToText(a: Record<string, string>): string {
  return Object.entries(a)
    .sort(([x], [y]) => x.localeCompare(y))
    .map(([from, to]) => `${from} = ${to}`)
    .join("\n");
}

function textToAliases(t: string): { aliases: Record<string, string>; bad: string[] } {
  const aliases: Record<string, string> = {};
  const bad: string[] = [];
  for (const raw of t.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const i = line.indexOf("=");
    const from = i > 0 ? line.slice(0, i).trim() : "";
    const to = i > 0 ? line.slice(i + 1).trim() : "";
    if (!from || !to) bad.push(line);
    else aliases[from] = to;
  }
  return { aliases, bad };
}

type Role = "production" | "non-production";

export default function TelemetryEnvironmentSettings() {
  const [loaded, setLoaded] = useState<(TelemetryEnvironmentSetting & { default_keys: string[] }) | null>(null);
  const [key, setKey] = useState("");
  const [text, setText] = useState("");
  const [roles, setRoles] = useState<Record<string, Role>>({});
  const [openOn, setOpenOn] = useState<"production" | "all">("production");
  // The environments seen in the last week, with their roles.
  const [seen, setSeen] = useState<EnvironmentRole[] | null>(null);
  const [added, setAdded] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api
      .getTelemetryEnvironment()
      .then((d) => {
        setLoaded(d);
        setKey(d.key);
        setText(aliasesToText(d.aliases ?? {}));
        setRoles({ ...(d.roles ?? {}) });
        setOpenOn(d.open_on ?? "production");
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
    api
      .listEnvironments("7d")
      .then((r) => setSeen(r.roles ?? []))
      .catch(() => setSeen([]));
  }, []);

  if (!loaded) return error ? <div className="alert alert--error">{error}</div> : null;
  const parsed = textToAliases(text);
  const rolesKey = (r: Record<string, string>) =>
    Object.entries(r)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([e, v]) => `${e}=${v}`)
      .join("\n");
  const dirty =
    key.trim() !== loaded.key ||
    openOn !== loaded.open_on ||
    rolesKey(roles) !== rolesKey(loaded.roles ?? {}) ||
    aliasesToText(parsed.aliases) !== aliasesToText(loaded.aliases ?? {});

  // Every environment worth deciding: seen in the last week, or already
  // decided.
  const rows: { environment: string; suggested: Role; seen: boolean }[] = (seen ?? []).map((r) => ({
    environment: r.environment,
    // For an unconfirmed environment the server's role is its suggestion.
    suggested: r.role,
    seen: r.seen,
  }));
  for (const e of Object.keys(roles)) {
    if (!rows.some((r) => r.environment === e)) rows.push({ environment: e, suggested: roles[e], seen: false });
  }
  rows.sort((a, b) => a.environment.localeCompare(b.environment));
  const unconfirmed = rows.filter((r) => !(r.environment in roles));
  const setRole = (env: string, role: Role) => {
    setRoles((cur) => ({ ...cur, [env]: role }));
    setSaved(false);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const r = await api.setTelemetryEnvironment({ key: key.trim(), aliases: parsed.aliases, roles, open_on: openOn });
      setLoaded({ ...loaded, ...r });
      setText(aliasesToText(r.aliases));
      setRoles({ ...(r.roles ?? {}) });
      setOpenOn(r.open_on);
      setSaved(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="card" style={{ padding: 16, marginTop: 20, maxWidth: 760 }} data-testid="telemetry-environment-settings">
      <h3 style={{ margin: "0 0 4px", fontSize: 15 }}>Telemetry environments</h3>
      <p className="muted" style={{ fontSize: 13, margin: "0 0 12px", lineHeight: 1.55 }}>
        Integrations are read and alerted on per environment. By default the environment is the resource
        attribute <code>{loaded.default_keys[0]}</code>, or <code>{loaded.default_keys[1]}</code> where only the
        older name is sent. Telemetry with neither shows as “(no environment)”. Changes apply at once and
        rewrite no data.
      </p>
      <label className="form__label" style={{ display: "block", marginBottom: 12 }}>
        Read the environment from <span className="muted">(optional, replaces the default attributes)</span>
        <input
          className="search__input"
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            setSaved(false);
          }}
          placeholder={loaded.default_keys[0]}
          style={{ display: "block", width: 360, marginTop: 4 }}
        />
      </label>
      <label className="form__label" style={{ display: "block" }}>
        Aliases <span className="muted">(one per line: value = name, case-insensitive)</span>
        <textarea
          className="search__input"
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setSaved(false);
          }}
          placeholder={"prd = production\nprod = production"}
          style={{ display: "block", width: 360, minHeight: 90, marginTop: 4, fontFamily: "var(--font-mono, monospace)" }}
        />
      </label>
      {parsed.bad.length > 0 && (
        <p className="muted" style={{ fontSize: 12, color: "var(--err-ink)" }}>
          Not understood, use “value = name”: {parsed.bad.join("; ")}
        </p>
      )}

      <h4 style={{ margin: "18px 0 4px", fontSize: 14 }}>Production environments</h4>
      <p className="muted" style={{ fontSize: 13, margin: "0 0 10px", lineHeight: 1.55 }}>
        Which environments are production, whatever they are called. An integration's page opens on its
        production environments, and dashboards, checks and notification profiles can name “production
        environments” and follow this list as environments come and go. Until you decide, a name with a word
        like prod, production, prd or live in it is suggested as production.
      </p>
      <div data-testid="environment-roles" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {seen === null && <span className="muted" style={{ fontSize: 13 }}>Reading the environments seen in the last week…</span>}
        {seen !== null && rows.length === 0 && (
          <span className="muted" style={{ fontSize: 13 }}>No environments seen in the last week.</span>
        )}
        {rows.map((r) => {
          const confirmed = r.environment in roles;
          return (
            <div key={r.environment} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13.5 }}>
              <span style={{ minWidth: 200, fontWeight: 500 }}>{r.environment}</span>
              <select
                className="search__input"
                style={{ width: 170, flex: "0 0 170px" }}
                value={roles[r.environment] ?? r.suggested}
                onChange={(e) => setRole(r.environment, e.target.value as Role)}
                aria-label={`Role of ${r.environment}`}
              >
                <option value="production">Production</option>
                <option value="non-production">Not production</option>
              </select>
              {!confirmed && (
                <span className="muted" style={{ fontSize: 12 }} data-testid="role-suggested">
                  suggested
                </span>
              )}
              {!r.seen && seen !== null && <span className="muted" style={{ fontSize: 12 }}>not seen this week</span>}
            </div>
          );
        })}
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 10, flexWrap: "wrap" }}>
        {unconfirmed.length > 0 && (
          <button
            type="button"
            className="btn"
            onClick={() => {
              setRoles((cur) => {
                const next = { ...cur };
                for (const r of unconfirmed) next[r.environment] = r.suggested;
                return next;
              });
              setSaved(false);
            }}
          >
            Confirm {unconfirmed.length} suggestion{unconfirmed.length === 1 ? "" : "s"}
          </button>
        )}
        <input
          className="search__input"
          style={{ width: 200, flex: "0 0 200px" }}
          placeholder="add an environment"
          value={added}
          onChange={(e) => setAdded(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && added.trim()) {
              e.preventDefault();
              setRole(added.trim(), "production");
              setAdded("");
            }
          }}
        />
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 14, fontSize: 13.5 }} data-testid="open-on">
        <span className="form__label">Open an integration on</span>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <input type="radio" name="open-on" checked={openOn === "production"} onChange={() => (setOpenOn("production"), setSaved(false))} />
          its production environments, where it has messages in them
        </label>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <input type="radio" name="open-on" checked={openOn === "all"} onChange={() => (setOpenOn("all"), setSaved(false))} />
          all environments
        </label>
      </div>

      {error && <div className="alert alert--error" style={{ marginTop: 8 }}>{error}</div>}
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 12 }}>
        <button type="button" className="btn btn--primary" disabled={!dirty || saving || parsed.bad.length > 0} onClick={save}>
          {saving ? "Saving…" : "Save"}
        </button>
        {saved && !dirty && <span className="muted" style={{ fontSize: 12 }}>Saved.</span>}
      </div>
    </section>
  );
}
