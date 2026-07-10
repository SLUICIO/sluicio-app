// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Export & import — move an org's configuration between environments
// (docs/config-transfer-design.md). Flat section on Settings →
// Organization. The import flow is deliberately two-step: every upload
// dry-runs first and shows the change report; Apply only appears after
// a clean preview. A failed import changes nothing (single transaction
// server-side), so the scariest button here is still safe.

import { useRef, useState } from "react";
import { api } from "../api/client";
import type { ConfigImportReport } from "../api/types";

export default function ConfigTransfer() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [bundle, setBundle] = useState<unknown | null>(null);
  const [bundleName, setBundleName] = useState("");
  const [mode, setMode] = useState<"strict" | "replace">("strict");
  const [matchMembers, setMatchMembers] = useState(false);
  const [preview, setPreview] = useState<ConfigImportReport | null>(null);
  const [applied, setApplied] = useState<ConfigImportReport | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);

  const exportBundle = async () => {
    setBusy(true);
    setError(null);
    try {
      const data = await api.exportConfigBundle();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      const src = (data as { source?: { org_slug?: string } }).source?.org_slug ?? "org";
      a.download = `sluicio-config-${src}-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const pickFile = async (f: File | undefined) => {
    setPreview(null);
    setApplied(null);
    setError(null);
    if (!f) {
      setBundle(null);
      return;
    }
    try {
      setBundle(JSON.parse(await f.text()));
      setBundleName(f.name);
    } catch {
      setBundle(null);
      setError("That file isn't valid JSON.");
    }
  };

  const run = async (dryRun: boolean) => {
    if (!bundle) return;
    setBusy(true);
    setError(null);
    try {
      const report = await api.importConfigBundle(bundle, { mode, dryRun, matchMembersByEmail: matchMembers });
      if (dryRun) setPreview(report);
      else {
        setApplied(report);
        setPreview(null);
      }
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setPreview(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section style={{ marginTop: 28, borderTop: "1px solid var(--border)", paddingTop: 20 }}>
      <h3 style={{ fontSize: 14, fontWeight: 600, margin: "0 0 4px" }}>Export &amp; import</h3>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 14px", maxWidth: 640 }}>
        Move this organization's configuration between environments — integrations,
        systems, groups &amp; policies, dashboards, health checks, templates, and more.
        Bundles carry no credentials. Imports are all-or-nothing: a failed import
        changes nothing.
      </p>

      {error && <div className="alert alert--error" style={{ marginBottom: 12, maxWidth: 640 }}>{error}</div>}

      <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 18 }}>
        <button type="button" className="btn" disabled={busy} onClick={exportBundle}>
          Export configuration
        </button>
        <span className="muted" style={{ fontSize: 12 }}>Downloads a JSON bundle of this org's configuration.</span>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 640 }}>
        <div style={{ display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap" }}>
          <input ref={fileRef} type="file" accept="application/json,.json" aria-label="Bundle file"
            onChange={(e) => pickFile(e.target.files?.[0])} style={{ fontSize: 13 }} />
          <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13 }}>
            <input type="radio" name="ct-mode" checked={mode === "strict"} onChange={() => setMode("strict")} />
            Strict <span className="muted">(fail on any name collision)</span>
          </label>
          <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13 }}>
            <input type="radio" name="ct-mode" checked={mode === "replace"} onChange={() => setMode("replace")} />
            Replace <span className="muted">(bundle wins; never deletes)</span>
          </label>
        </div>
        <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
          <input type="checkbox" checked={matchMembers} onChange={(e) => setMatchMembers(e.target.checked)} />
          Attach group members by matching email addresses in this environment
        </label>

        {bundle != null && !applied && (
          <div style={{ display: "flex", gap: 8 }}>
            <button type="button" className="btn btn--primary" disabled={busy} onClick={() => run(true)}>
              {busy ? "Working…" : "Preview import (dry run)"}
            </button>
            {preview && (
              <button type="button" className="btn btn--danger" disabled={busy} onClick={() => run(false)}>
                Apply import
              </button>
            )}
          </div>
        )}

        {(preview ?? applied) && <ReportTable report={(applied ?? preview)!} bundleName={bundleName} />}
        {applied && <div className="alert alert--ok" style={{ fontSize: 13 }}>Import applied.</div>}
      </div>
    </section>
  );
}

function ReportTable({ report, bundleName }: { report: ConfigImportReport; bundleName: string }) {
  const rows = Object.entries(report.sections ?? {}).filter(
    ([, s]) => s.created + s.updated + s.skipped > 0,
  );
  return (
    <div>
      <div className="muted" style={{ fontSize: 12.5, margin: "4px 0 6px" }}>
        {report.dry_run ? "Dry run" : "Result"} — {bundleName} · mode: {report.mode}
        {report.dry_run && " · nothing has been changed yet"}
      </div>
      {rows.length === 0 ? (
        <div className="muted" style={{ fontSize: 13 }}>Nothing to import — the bundle is empty or everything was skipped.</div>
      ) : (
        <table className="table" style={{ maxWidth: 480 }}>
          <thead>
            <tr><th>Section</th><th className="num">Created</th><th className="num">Updated</th><th className="num">Skipped</th></tr>
          </thead>
          <tbody>
            {rows.map(([name, s]) => (
              <tr key={name}>
                <td className="mono" style={{ fontSize: 12 }}>{name}</td>
                <td className="num">{s.created}</td>
                <td className="num">{s.updated}</td>
                <td className="num">{s.skipped}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {report.needs_credentials && report.needs_credentials.length > 0 && (
        <div className="alert alert--warn" style={{ marginTop: 8, fontSize: 12.5 }}>
          Channels needing credentials in this environment:{" "}
          <strong>{report.needs_credentials.join(", ")}</strong>
        </div>
      )}
      {report.warnings?.map((w, i) => (
        <div key={i} className="muted" style={{ fontSize: 12.5, marginTop: 4 }}>⚠ {w}</div>
      ))}
    </div>
  );
}
