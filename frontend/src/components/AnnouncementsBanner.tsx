// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Top-of-app announcement stack — persistent messages from org admins
// (or the cell operator) to every user: maintenance notices, known
// issues, "demo resets nightly". Refreshed on a slow poll; dismissal is
// per-user and server-side, so it sticks across devices. Renders in the
// AppShell banner slot next to the MFA-enrollment and integration-limit
// banners, with the same alert styling.

import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Announcement } from "../api/types";

const ANNOUNCEMENTS_CHANGED = "sluicio:announcements-changed";

// announcementsChanged nudges the banner to refetch right away — call it
// after any mutation that creates or removes announcements, so the actor
// sees the effect without waiting for the next poll.
export function announcementsChanged() {
  window.dispatchEvent(new Event(ANNOUNCEMENTS_CHANGED));
}

const severityClass: Record<Announcement["severity"], string> = {
  info: "alert alert--info",
  warning: "alert alert--warn",
  critical: "alert alert--error",
};

export function AnnouncementsBanner() {
  const [items, setItems] = useState<Announcement[]>([]);

  const load = useCallback(() => {
    api
      .listMyAnnouncements()
      .then((r) => setItems(r.announcements ?? []))
      .catch(() => {
        /* banners are best-effort — keep whatever we have */
      });
  }, []);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 60_000);
    // Instant refresh when this session publishes/removes announcements
    // (directly or via a maintenance window) — see announcementsChanged().
    window.addEventListener(ANNOUNCEMENTS_CHANGED, load);
    return () => {
      window.clearInterval(t);
      window.removeEventListener(ANNOUNCEMENTS_CHANGED, load);
    };
  }, [load]);

  const dismiss = async (id: string) => {
    // Optimistic: hide immediately, the server call makes it stick.
    setItems((cur) => cur.filter((a) => a.id !== id));
    try {
      await api.dismissAnnouncement(id);
    } catch {
      load(); // e.g. not dismissible after all — resync
    }
  };

  if (items.length === 0) return null;
  return (
    <>
      {items.map((a) => (
        <div
          key={a.id}
          className={severityClass[a.severity] ?? "alert alert--info"}
          role="status"
          style={{ margin: "0 0 12px", display: "flex", alignItems: "center", gap: 12 }}
        >
          <span style={{ fontSize: 13.5, lineHeight: 1.5 }}>{a.message}</span>
          {a.dismissible && (
            <button
              type="button"
              aria-label="Dismiss announcement"
              title="Dismiss"
              onClick={() => dismiss(a.id)}
              className="btn btn--link"
              style={{ marginLeft: "auto", fontSize: 15, lineHeight: 1, padding: "0 4px" }}
            >
              ×
            </button>
          )}
        </div>
      ))}
    </>
  );
}
