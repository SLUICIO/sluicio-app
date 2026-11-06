// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Top-of-app banner for the limits the deployment sets on the instance
// (GET /limits): storage, and integrations + systems combined.
//
// The storage half is the important one, and a pausing instance at its
// limit is shown to EVERYONE: telemetry is being refused, so every page
// stops showing new data, and whoever is looking deserves to know why
// rather than suspect their own systems. The early warnings (80%, as the
// Acceptable Use Policy 4.3 sets) and the integration count are for
// admins, who are the ones who can act, with a link to change plan when
// the deployment names one.

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
  // The plan sets these limits, so the way past one is a bigger plan.
  // Offered to admins, who are the ones who can order it.
  const changePlan =
    isAdmin && limits.plan_url ? (
      <>
        {" "}
        <a href={limits.plan_url} target="_blank" rel="noreferrer">
          Change plan
        </a>
      </>
    ) : null;
  const st = limits.storage;
  const ent = limits.monitored_entities;
  const used = st?.used_bytes ?? 0;
  const banners: { tone: "error" | "warn"; text: React.ReactNode }[] = [];

  const pauses = st?.action === "pause";
  if (st && st.at_limit && !pauses) {
    // Deleting the oldest day makes room within a minute, so this is
    // for admins only; telemetry keeps arriving unless today's data alone
    // fills the limit.
    if (isAdmin) {
      banners.push({
        tone: "warn",
        text: (
          <>
            <strong>Storage limit reached.</strong> This instance holds {formatGB(used)} of its{" "}
            {formatGB(st.limit_bytes)}, so the oldest telemetry is being deleted to make room. If today&rsquo;s
            data alone fills the limit, new telemetry is refused until tomorrow or until the limit is raised.
            {changePlan}
          </>
        ),
      });
    }
  } else if (st && st.at_limit) {
    banners.push({
      tone: "error",
      text: (
        <>
          <strong>Storage limit reached.</strong> This instance holds {formatGB(used)} of its{" "}
          {formatGB(st.limit_bytes)}, so new telemetry is being refused and recent data will be missing.
          It is accepted again once retention has cleared older data
          {isAdmin ? ", or when the limit is raised" : ""}.{changePlan}
        </>
      ),
    });
  } else if (st && isAdmin && used >= st.limit_bytes * WARN_AT) {
    banners.push({
      tone: "warn",
      text: (
        <>
          <strong>Storage at {Math.round((used / st.limit_bytes) * 100)}%.</strong> {formatGB(used)} of{" "}
          {formatGB(st.limit_bytes)}.{" "}
          {pauses
            ? "When it is full, new telemetry is refused until older data ages out."
            : "When it is full, the oldest telemetry is deleted to make room."}
          {changePlan}
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
          {changePlan}
        </>
      ),
    });
  } else if (ent && isAdmin && ent.used >= ent.limit * WARN_AT) {
    banners.push({
      tone: "warn",
      text: (
        <>
          <strong>Integrations at {Math.round((ent.used / ent.limit) * 100)}%.</strong> This instance has{" "}
          {ent.used} of {ent.limit} integrations and systems. At the limit no new one can be added.
          {changePlan}
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
