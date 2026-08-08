// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The page for a URL that matches nothing.
//
// Until this existed there was no catch-all route at all, so an unknown
// path rendered the shell's outlet with nothing in it: a blank white
// page, no message, and nothing in the console. That is the worst
// possible failure mode, because it is indistinguishable from the app
// being broken. A stale bookmark, a renamed route, or a typo all landed
// there.
//
// It shows the path it could not match. Someone reporting "the page is
// blank" can then say which page, and someone who mistyped can see the
// typo without opening devtools.

import { Link, useLocation } from "react-router-dom";
import { usePageTitle } from "../lib/usePageTitle";

export default function NotFound() {
  const { pathname } = useLocation();
  usePageTitle("Page not found");

  return (
    <div className="card" style={{ padding: 28, maxWidth: 560 }}>
      <h1 className="page__title" style={{ marginTop: 0 }}>
        Page not found
      </h1>
      <p className="muted" style={{ fontSize: 14 }}>
        Nothing is served at <code className="mono">{pathname}</code>.
      </p>
      <p className="muted" style={{ fontSize: 13 }}>
        If you followed a link from inside Sluicio, that is a bug worth reporting: the address is
        shown above so it can go straight into the report.
      </p>
      <Link className="btn primary" to="/">
        Go to the dashboard
      </Link>
    </div>
  );
}
