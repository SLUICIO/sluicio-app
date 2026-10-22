// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a row's environment is read from (issue #40): the resource
// attribute, and aliases folding several spellings into one name. Read
// at query time, so a change applies to everything at once and rewrites
// nothing.

import { useEffect, useState } from "react";
import { api } from "../api/client";

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

export default function TelemetryEnvironmentSettings() {
  const [loaded, setLoaded] = useState<{ key: string; aliases: Record<string, string>; default_keys: string[] } | null>(null);
  const [key, setKey] = useState("");
  const [text, setText] = useState("");
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
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, []);

  if (!loaded) return error ? <div className="alert alert--error">{error}</div> : null;
  const parsed = textToAliases(text);
  const dirty = key.trim() !== loaded.key || aliasesToText(parsed.aliases) !== aliasesToText(loaded.aliases ?? {});

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const r = await api.setTelemetryEnvironment({ key: key.trim(), aliases: parsed.aliases });
      setLoaded({ ...loaded, key: r.key, aliases: r.aliases });
      setText(aliasesToText(r.aliases));
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
