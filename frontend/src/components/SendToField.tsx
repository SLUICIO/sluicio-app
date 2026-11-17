// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a check's firings go, the same in every check form. A check
// normally follows the notification profile that applies to it (its
// integration's, else its team's default, else the organisation's), and
// may name channels of its own instead. Forms used to differ: some had a
// channel checklist whose empty state looked like "nowhere", the
// integration's health checks had none at all, and a trace completion
// rule with nothing ticked sent nothing. The caller wraps this in its
// own label, so it sits in both form styles.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { NotificationChannel } from "../api/types";

export default function SendToField({
  value,
  onChange,
  channels: given,
  variant = "field",
}: {
  // The check's own channel ids; empty means the profile routes it.
  value: string[];
  onChange: (ids: string[]) => void;
  // The channel list when the form already has it; fetched otherwise.
  channels?: NotificationChannel[];
  // "field" sits among text fields (search__input); "drawer" among the
  // check editors' compact selects (m-rs-sel).
  variant?: "field" | "drawer";
}) {
  const [fetched, setFetched] = useState<NotificationChannel[]>([]);
  const [own, setOwn] = useState(value.length > 0);
  useEffect(() => {
    if (given) return;
    let active = true;
    api
      .listChannels()
      .then((r) => active && setFetched(r.channels ?? []))
      .catch(() => active && setFetched([]));
    return () => {
      active = false;
    };
  }, [given]);
  const channels = given ?? fetched;
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }} data-testid="send-to">
      <select
        className={variant === "drawer" ? "m-rs-sel" : "search__input"}
        style={variant === "drawer" ? { alignSelf: "flex-start" } : undefined}
        aria-label="Send to"
        value={own ? "channels" : "profile"}
        onChange={(e) => {
          const next = e.target.value === "channels";
          setOwn(next);
          if (!next) onChange([]);
        }}
      >
        <option value="profile">The notification profile</option>
        <option value="channels">Specific channels</option>
      </select>
      {!own ? (
        <span className="muted" style={{ fontSize: 12 }}>
          Sent the way the notification profile that applies says: the integration's, else the team's default, else the
          organisation's. Profiles are set on the integration's Alerting tab, in Settings → Teams and on the{" "}
          <Link to="/alerts">Alerts</Link> page.
        </span>
      ) : channels.length === 0 ? (
        <span className="muted" style={{ fontSize: 12 }}>
          No notification channels yet. <Link to="/alerts">Add one on the Alerts page</Link>.
        </span>
      ) : (
        <>
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {channels.map((c) => (
              <label key={c.id} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13 }}>
                <input type="checkbox" checked={value.includes(c.id)} onChange={() => toggle(c.id)} />
                <span>
                  <b>{c.name}</b> <span className="muted">· {c.kind}</span>
                </span>
              </label>
            ))}
          </div>
          <span className="muted" style={{ fontSize: 12 }}>
            {value.length === 0
              ? "Tick at least one, or the profile is used."
              : "These channels replace the profile's. Grouping and re-notify still follow the profile."}
          </span>
        </>
      )}
    </div>
  );
}
