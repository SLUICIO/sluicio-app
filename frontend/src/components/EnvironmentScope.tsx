// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EnvironmentScope wraps the integration pages and a service's page
// (issue #40). It hands
// ?env= to the API client before anything below it fetches, and keys the
// pages by it, so choosing other environments refetches every panel
// rather than leaving half the page on the previous ones.
//
// An integration's or a service's page opened without ?env opens on its
// production
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
import { environmentsOnce, type EnvironmentSubject } from "./EnvironmentPicker";

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
  const integration = useMatch("/integrations/:id/*");
  const service = useMatch("/services/:name");
  const subject: EnvironmentSubject | undefined = integration?.params.id
    ? { kind: "integration", id: integration.params.id }
    : service?.params.name
      ? { kind: "service", id: service.params.name }
      : undefined;
  const subjectKey = subject ? `${subject.kind}:${subject.id}` : undefined;
  const [window] = useTimeWindow();
  const needsDefault = subject !== undefined && params.get("env") === null;
  // The page whose production environments were looked up and found to
  // be none.
  const [settled, setSettled] = useState<string | null>(null);

  useEffect(() => {
    if (!needsDefault || !subject || settled === subjectKey) return;
    let cancelled = false;
    environmentsOnce(subject, window)
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
          setSettled(subjectKey ?? null);
        }
      })
      .catch(() => !cancelled && setSettled(subjectKey ?? null));
    return () => {
      cancelled = true;
    };
    // subject is rebuilt every render; subjectKey stands for it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needsDefault, subjectKey, window, settled, setParams]);

  const envs = envsFromParams(params);
  // During render, not in an effect: the pages below fetch in their own
  // effects, which run before this component's.
  setActiveEnvironments(envs);
  useEffect(() => () => setActiveEnvironments(null), []);
  if (needsDefault && settled !== subjectKey) return null;
  return (
    <Fragment key={envs === null ? "\u0000all" : `env:${envs.join("\u0000")}`}>
      <Outlet />
    </Fragment>
  );
}
