// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ResetPassword — the page the password-reset email link lands on
// (/reset-password?token=…). Rendered by UserProvider (outside the router,
// since the user isn't authenticated). Sets a new password via the token,
// then routes back to sign-in.

import { FormEvent, useState } from "react";
import { api } from "../api/client";
import { LogoMark } from "../components/brand/Logo";
import { usePageTitle } from "../lib/usePageTitle";
import { AuthCard } from "../components/AuthCard";

export default function ResetPassword() {
  usePageTitle("Reset password");
  const token = new URLSearchParams(window.location.search).get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    if (password.length < 8) {
      setError("Password must be at least 8 characters.");
      return;
    }
    if (password !== confirm) {
      setError("Passwords don't match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.resetPassword(token, password);
      setDone(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  if (!token) {
    return (
      <AuthCard>
        <div className="alert alert--error">This reset link is missing its token. Request a new one from the sign-in page.</div>
        <a className="btn" href="/" style={{ marginTop: 12 }}>Back to sign in</a>
      </AuthCard>
    );
  }

  if (done) {
    return (
      <AuthCard>
        <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 14 }}>
          <LogoMark size={32} style={{ color: "var(--primary)" }} />
          <div style={{ fontSize: 18, fontWeight: 700 }}>Password reset</div>
        </div>
        <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
          Your password has been updated and any existing sessions were signed
          out. You can now sign in with your new password.
        </p>
        <a className="btn btn--primary" href="/" style={{ marginTop: 8 }}>Go to sign in</a>
      </AuthCard>
    );
  }

  return (
    <AuthCard>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 18 }}>
        <LogoMark size={32} style={{ color: "var(--primary)" }} />
        <div style={{ fontSize: 18, fontWeight: 700 }}>Choose a new password</div>
      </div>
      <form onSubmit={submit} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <label className="form__label">
          New password
          <input type="password" className="search__input" value={password}
            onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" autoFocus required />
        </label>
        <label className="form__label">
          Confirm password
          <input type="password" className="search__input" value={confirm}
            onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
        </label>
        {error && <div className="alert alert--error" role="alert">{error}</div>}
        <button type="submit" className="btn btn--primary" disabled={busy || !password || !confirm} style={{ marginTop: 6 }}>
          {busy ? "Updating…" : "Reset password"}
        </button>
      </form>
    </AuthCard>
  );
}
