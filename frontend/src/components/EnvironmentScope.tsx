// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EnvironmentScope wraps the integration pages (issue #40). It hands
// ?env= to the API client before anything below it fetches, and keys the
// pages by it, so choosing other environments refetches every panel
// rather than leaving half the page on the previous ones.
//
// An integration's page opened without ?env opens on its production
// environments (the role, not the name: prod-eu and prod-us together),
// so the first thing read is production rather than production and test
// mixed. Only those its messages run in, or that it declares: one that
// only logs or metrics come from would open on an empty Messages tab.
// One read of its environments decides, before anything below fetches;
// an integration without any, or a cell set to open on all, opens on all
// environments.

import { Fragment, useEffect, useState } from "react";
import { Outlet, useMatch, useSearchParams } from "react-router-dom";
import type { IntegrationEnvironment } from "../api/types";
import { envToParam, envsFromParams, setActiveEnvironments } from "../lib/activeEnvironment";
import { useTimeWindow } from "../lib/useTimeWindow";
import { integrationEnvironmentsOnce } from "./EnvironmentPicker";

/** The environments an integration's page opens on: its production
 *  environments with messages or declared, or, for an integration with
 *  no messages anywhere (metrics only), any production one. */
export function productionToOpen(envs: IntegrationEnvironment[]): string[] {
  const anyMessages = envs.some((e) => e.trace_count > 0);
  return envs
    .filter((e) => e.production && e.environment !== "" && (e.trace_count > 0 || e.declared || !anyMessages))
    .map((e) => e.environment)
    .sort();
}

export default function EnvironmentScope() {
  const [params, setParams] = useSearchParams();
  const match = useMatch("/integrations/:id/*");
  const integrationId = match?.params.id;
  const [window] = useTimeWindow();
  const needsDefault = integrationId !== undefined && params.get("env") === null;
  // The integration whose production environments were looked up and
  // found to be none.
  const [settled, setSettled] = useState<string | null>(null);

  useEffect(() => {
    if (!needsDefault || settled === integrationId) return;
    let cancelled = false;
    integrationEnvironmentsOnce(integrationId, window)
      .then((r) => {
        if (cancelled) return;
        const open = r.open_on === "all" ? [] : productionToOpen(r.environments ?? []);
        if (open.length > 0) {
          setParams(
            (prev) => {
              const p = new URLSearchParams(prev);
              p.delete("env");
              for (const e of open) p.append("env", envToParam(e));
              return p;
            },
            { replace: true },
          );
        } else {
          setSettled(integrationId);
        }
      })
      .catch(() => !cancelled && setSettled(integrationId));
    return () => {
      cancelled = true;
    };
  }, [needsDefault, integrationId, window, settled, setParams]);

  const envs = envsFromParams(params);
  // During render, not in an effect: the pages below fetch in their own
  // effects, which run before this component's.
  setActiveEnvironments(envs);
  useEffect(() => () => setActiveEnvironments(null), []);
  if (needsDefault && settled !== integrationId) return null;
  return (
    <Fragment key={envs === null ? "\u0000all" : `env:${envs.join("\u0000")}`}>
      <Outlet />
    </Fragment>
  );
}
