// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Top-of-app banner for the limits the deployment sets on the instance
// (GET /limits): storage, and integrations + systems combined.
//
// The storage half is the important one, and it is shown to EVERYONE
// once the limit is reached: telemetry is being refused, so every page
// stops showing new data, and whoever is looking deserves to know why
// rather than suspect their own systems. The early warning and the
// integration count are for admins, who are the ones who can act.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LimitsStatus } from "../api/types";
import { useCurrentUser } from "../lib/useCurrentUser";

const WARN_AT = 0.8;
const POLL_MS = 5 * 60_000;

export function formatGB(bytes: number): string {
  const gb = bytes / 1e9;
  return gb >= 10 ? `${Math.round(gb)} GB` : `${gb.toFixed(1)} GB`;
}

export function DeploymentLimitsBanner() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [limits, setLimits] = useState<LimitsStatus | null>(null);

  useEffect(() => {
    let live = true;
    const load = () =>
      api
        .getLimits()
        .then((l) => live && setLimits(l))
        .catch(() => {
          /* no banner is the right failure */
        });
    load();
    const t = window.setInterval(load, POLL_MS);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, []);

  if (!limits) return null;
  const st = limits.storage;
  const ent = limits.monitored_entities;
  const used = st?.used_bytes ?? 0;
  const banners: { tone: "error" | "warn"; text: React.ReactNode }[] = [];

  if (st && st.at_limit) {
    banners.push({
      tone: "error",
      text: (
        <>
          <strong>Storage limit reached.</strong> This instance holds {formatGB(used)} of its{" "}
          {formatGB(st.limit_bytes)}, so new telemetry is being refused and recent data will be missing.
          It is accepted again once retention has cleared older data
          {isAdmin ? ", or when the limit is raised" : ""}.
        </>
      ),
    });
  } else if (st && isAdmin && used >= st.limit_bytes * WARN_AT) {
    banners.push({
      tone: "warn",
      text: (
        <>
          <strong>Storage at {Math.round((used / st.limit_bytes) * 100)}%.</strong> {formatGB(used)} of{" "}
          {formatGB(st.limit_bytes)}. When it is full, new telemetry is refused until older data ages out.
        </>
      ),
    });
  }
  if (ent && isAdmin && ent.used >= ent.limit) {
    banners.push({
      tone: "warn",
      text: (
        <>
          <strong>Integration limit reached.</strong> This instance has {ent.used} of {ent.limit}{" "}
          integrations and systems; remove one to add another. Monitoring of the existing ones continues.
        </>
      ),
    });
  }
  if (banners.length === 0) return null;
  return (
    <>
      {banners.map((b, i) => (
        <div
          key={i}
          role={b.tone === "error" ? "alert" : "status"}
          className={b.tone === "error" ? "alert alert--error" : "alert alert--warn"}
          style={{ margin: "0 0 16px", fontSize: 13.5 }}
        >
          {b.text}
        </div>
      ))}
    </>
  );
}
