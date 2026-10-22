// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EnvironmentScope wraps the integration pages (issue #40). It hands
// ?env= to the API client before anything below it fetches, and keys the
// pages by it, so choosing another environment refetches every panel
// rather than leaving half the page on the previous one.

import { Fragment, useEffect } from "react";
import { Outlet, useSearchParams } from "react-router-dom";
import { envFromParam, setActiveEnvironment } from "../lib/activeEnvironment";

export default function EnvironmentScope() {
  const [params] = useSearchParams();
  const env = envFromParam(params.get("env"));
  // During render, not in an effect: the pages below fetch in their own
  // effects, which run before this component's.
  setActiveEnvironment(env);
  useEffect(() => () => setActiveEnvironment(null), []);
  return (
    <Fragment key={env === null ? "\u0000all" : `env:${env}`}>
      <Outlet />
    </Fragment>
  );
}
