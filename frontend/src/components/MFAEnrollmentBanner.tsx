// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Companion to the server-side MFA-policy gate (api/mfa_enforce.go): when
// org-wide MFA enforcement (Enterprise) is on and the signed-in user hasn't
// enrolled, the backend 403s everything outside the enrollment surface. This
// component funnels the user there: it redirects any other route to the
// Account MFA tab and shows the banner while they're enrolling. While the
// flag is set it re-checks /me on each navigation so the gate lifts the
// moment enrollment completes.

import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { api } from "../api/client";

export function MFAEnrollmentBanner() {
  const [required, setRequired] = useState(false);
  const checked = useRef(false);
  const loc = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    // First mount always checks; afterwards only re-check while the gate is
    // up (so enrolled users pay a single /me call at boot).
    if (checked.current && !required) return;
    let active = true;
    api
      .me()
      .then((m) => {
        checked.current = true;
        if (active) setRequired(Boolean(m.mfa_enrollment_required));
      })
      .catch(() => {});
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loc.pathname]);

  useEffect(() => {
    if (required && !loc.pathname.startsWith("/account")) {
      navigate("/account?tab=mfa", { replace: true });
    }
  }, [required, loc.pathname, navigate]);

  if (!required) return null;

  return (
    <div
      className="alert alert--warn"
      style={{ margin: "0 0 16px", display: "flex", alignItems: "center", gap: 12 }}
    >
      <span style={{ fontSize: 13.5 }}>
        <strong>Two-factor authentication is required</strong> by your
        organization. Finish setting it up below to continue using Sluicio.
      </span>
    </div>
  );
}
