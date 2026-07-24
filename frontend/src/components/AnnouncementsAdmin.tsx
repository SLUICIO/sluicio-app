// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Announcement management — shared by Settings → Organization (org
// announcements, admin) and the Operator page (cell-wide announcements).
// Styled as a flat section to match its System-tab siblings: top-border
// divider, h3 title, muted intro.

import { FormEvent, useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Announcement, AnnouncementInput } from "../api/types";
import { formatRelative } from "../lib/format";
import { announcementsChanged } from "./AnnouncementsBanner";

interface Props {
  // "org" manages the org's announcements; "cell" the operator's
  // cell-wide ones (shown to every org on this install).
  scope: "org" | "cell";
}

export default function AnnouncementsAdmin({ scope }: Props) {
  const [items, setItems] = useState<Announcement[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const [severity, setSeverity] = useState<"info" | "warning" | "critical">("info");
  const [endsIn, setEndsIn] = useState(""); // hours, "" = until deleted
  const [dismissible, setDismissible] = useState(true);
  // Cell-wide only: surface on the unauthenticated login page. The org
  // form never offers it (the server refuses it there too).
  const [showOnLogin, setShowOnLogin] = useState(false);
  const [busy, setBusy] = useState(false);

  const listFn = scope === "org" ? api.listOrgAnnouncements : api.listCellAnnouncements;
  const createFn = scope === "org" ? api.createOrgAnnouncement : api.createCellAnnouncement;
  const deleteFn = scope === "org" ? api.deleteOrgAnnouncement : api.deleteCellAnnouncement;

  const load = useCallback(() => {
    listFn().then((r) => setItems(r.announcements ?? [])).catch((e) => setError(String((e as Error).message ?? e)));
  }, [listFn]);
  useEffect(() => { load(); }, [load]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    const body: AnnouncementInput = { message: message.trim(), severity, dismissible };
    if (scope === "cell" && showOnLogin) body.show_on_login = true;
    if (endsIn) body.ends_at = new Date(Date.now() + Number(endsIn) * 3600_000).toISOString();
    try {
      await createFn(body);
      setMessage("");
      setEndsIn("");
      setSeverity("info");
      setDismissible(true);
      setShowOnLogin(false);
      announcementsChanged();
      load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (a: Announcement) => {
    if (!window.confirm("Remove this announcement? It disappears for everyone immediately.")) return;
    try {
      await deleteFn(a.id);
      announcementsChanged();
      load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    }
  };

  const active = (a: Announcement) =>
    new Date(a.starts_at) <= new Date() && (!a.ends_at || new Date(a.ends_at) > new Date());

  return (
    <section style={{ marginTop: 28, borderTop: "1px solid var(--border)", paddingTop: 20 }}>
      <h3 style={{ fontSize: 14, fontWeight: 600, margin: "0 0 4px" }}>
        {scope === "org" ? "Announcements" : "Cell-wide announcements"}
      </h3>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 14px" }}>
        {scope === "org"
          ? "A persistent banner every member of this organization sees until it expires or they dismiss it — maintenance notices, known issues, heads-ups."
          : "A persistent banner every user on this cell sees, across all organizations. Use sparingly — cell-wide is loud by design."}
      </p>

      {error && <div className="alert alert--error" style={{ marginBottom: 12 }}>{error}</div>}

      <form onSubmit={submit} style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 640, marginBottom: 16 }}>
        <label className="form__label">
          Message
          <input className="search__input" value={message} onChange={(e) => setMessage(e.target.value)}
            placeholder="Planned maintenance tonight 22:00–23:00 CET — expect brief gaps." maxLength={500} required />
        </label>
        <div style={{ display: "flex", gap: 12, alignItems: "flex-end", flexWrap: "wrap" }}>
          <label className="form__label">
            Severity
            <select className="search__input" value={severity}
              onChange={(e) => setSeverity(e.target.value as typeof severity)}>
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </select>
          </label>
          <label className="form__label">
            Expires
            <select className="search__input" value={endsIn} onChange={(e) => setEndsIn(e.target.value)}>
              <option value="">When deleted</option>
              <option value="4">In 4 hours</option>
              <option value="24">In 24 hours</option>
              <option value="72">In 3 days</option>
              <option value="168">In 7 days</option>
            </select>
          </label>
          <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5, paddingBottom: 8 }}>
            <input type="checkbox" checked={dismissible} onChange={(e) => setDismissible(e.target.checked)} />
            Users can dismiss it
          </label>
          {scope === "cell" && (
            <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5, paddingBottom: 8 }}
              title="Also show this banner on the sign-in page, before login — for maintenance notices users must see even when they can't get in.">
              <input type="checkbox" checked={showOnLogin} onChange={(e) => setShowOnLogin(e.target.checked)} />
              Show on login page
            </label>
          )}
          <button type="submit" className="btn btn--primary" disabled={busy || !message.trim()} style={{ marginBottom: 2 }}>
            {busy ? "Publishing…" : "Publish"}
          </button>
        </div>
      </form>

      {items.length > 0 && (
        <table className="table" style={{ maxWidth: 760 }}>
          <thead>
            <tr><th>Message</th><th>Severity</th><th>Status</th><th></th></tr>
          </thead>
          <tbody>
            {items.map((a) => (
              <tr key={a.id}>
                <td style={{ fontSize: 13, maxWidth: 380 }}>{a.message}</td>
                <td>
                  <span className="mono" style={{ fontSize: 11 }}>{a.severity}</span>
                  {a.show_on_login && (
                    <span className="badge-brand" style={{ marginLeft: 6 }} title="Also visible on the sign-in page, before login">login page</span>
                  )}
                </td>
                <td className="muted" style={{ fontSize: 12.5 }}>
                  {active(a)
                    ? a.ends_at ? `active — expires ${formatRelative(a.ends_at)}` : "active"
                    : "expired"}
                </td>
                <td className="num">
                  <button type="button" className="btn btn--link" onClick={() => remove(a)}>Remove</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
