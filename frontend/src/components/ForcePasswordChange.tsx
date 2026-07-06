// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Blocking gate for users flagged must_reset_password (an admin set a
// temporary password, or first-login rotation). The cell-api 403s every
// other endpoint until the change lands (EnforcePasswordReset); this is
// the matching UI — a full-screen card that replaces the app shell and
// takes the temporary password + a new one. On success we hard-reload so
// the app boots fresh with the flag cleared.

import { FormEvent, useState } from "react";
import { api } from "../api/client";

export default function ForcePasswordChange() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setError("The new passwords don't match.");
      return;
    }
    if (next.length < 8) {
      setError("New password must be at least 8 characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.changePassword({ current_password: current, new_password: next });
      // Flag is cleared server-side; reboot the SPA so every gate re-reads /me.
      window.location.assign("/");
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        display: "grid",
        placeItems: "center",
        background: "var(--surface)",
        padding: 16,
      }}
    >
      <div
        className="card"
        style={{ width: "100%", maxWidth: 420, padding: 24, border: "1px solid var(--border)", borderRadius: 12 }}
      >
        <h1 style={{ fontSize: 18, fontWeight: 600, margin: "0 0 4px" }}>Choose a new password</h1>
        <p className="muted" style={{ fontSize: 13, margin: "0 0 16px" }}>
          Your password was set to a temporary one and must be changed before
          you continue. Enter the temporary password, then pick a new one.
        </p>
        <form onSubmit={submit} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <label className="form__label">
            Temporary password
            <input
              className="input"
              type="password"
              autoFocus
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </label>
          <label className="form__label">
            New password
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </label>
          <label className="form__label">
            Confirm new password
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </label>
          {error && <div className="alert alert--error">{error}</div>}
          <button type="submit" className="btn btn--primary" disabled={busy}>
            {busy ? "Saving…" : "Set new password"}
          </button>
        </form>
      </div>
    </div>
  );
}
