// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EnvironmentScope wraps the integration pages (issue #40). It hands
// ?env= to the API client before anything below it fetches, and keys the
// pages by it, so choosing another environment refetches every panel
// rather than leaving half the page on the previous one.
//
// An integration's page opened without ?env opens on the default
// environment (production unless an operator changed it) when the
// integration has it, so the first thing read is production rather than
// production and test mixed. One read of its environments decides, before
// anything below fetches; an integration without the default, or without
// environments at all, opens on all of them as before.

import { Fragment, useEffect, useState } from "react";
import { Outlet, useMatch, useSearchParams } from "react-router-dom";
import { envFromParam, envToParam, setActiveEnvironment } from "../lib/activeEnvironment";
import { useTimeWindow } from "../lib/useTimeWindow";
import { integrationEnvironmentsOnce } from "./EnvironmentPicker";

export default function EnvironmentScope() {
  const [params, setParams] = useSearchParams();
  const match = useMatch("/integrations/:id/*");
  const integrationId = match?.params.id;
  const [window] = useTimeWindow();
  const needsDefault = integrationId !== undefined && params.get("env") === null;
  // The integration whose default was looked up and found absent.
  const [settled, setSettled] = useState<string | null>(null);

  useEffect(() => {
    if (!needsDefault || settled === integrationId) return;
    let cancelled = false;
    integrationEnvironmentsOnce(integrationId, window)
      .then((r) => {
        if (cancelled) return;
        const d = r.default ?? "";
        if (d !== "" && (r.environments ?? []).some((e) => e.environment === d)) {
          setParams(
            (prev) => {
              const p = new URLSearchParams(prev);
              p.set("env", envToParam(d));
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

  const env = envFromParam(params.get("env"));
  // During render, not in an effect: the pages below fetch in their own
  // effects, which run before this component's.
  setActiveEnvironment(env);
  useEffect(() => () => setActiveEnvironment(null), []);
  if (needsDefault && settled !== integrationId) return null;
  return (
    <Fragment key={env === null ? "\u0000all" : `env:${env}`}>
      <Outlet />
    </Fragment>
  );
}
