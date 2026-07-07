// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Login — the standalone page UserProvider renders when /api/v1/me
// returns 401. Email + password form; on success, calls `onSuccess`
// so the provider re-fetches /me and the rest of the app renders.
// No router involvement (the user isn't authenticated yet, so
// AppShell + Routes haven't mounted).

import { FormEvent, useEffect, useState } from "react";
import { api } from "../api/client";
import type { SsoProviderButton } from "../api/types";
import { LogoMark } from "../components/brand/Logo";
import { usePageTitle } from "../lib/usePageTitle";

interface Props {
  // Called after the cell-api accepts the credentials. UserProvider
  // wires this to its own refetch so /me runs again and the SPA
  // boots into the authenticated app.
  onSuccess: () => Promise<void> | void;
}

export default function Login({ onSuccess }: Props) {
  usePageTitle("Sign in");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Whether to surface the "ships with a default admin" hint at the
  // bottom of the form. Public install-state endpoint tells us; we
  // start undefined so the first paint renders without the hint
  // (better to omit-then-show than flash-then-hide). On install-state
  // fetch failure we leave it undefined → hint stays hidden, which
  // matches the safer default on a non-fresh deploy.
  const [showFreshHint, setShowFreshHint] = useState<boolean | undefined>(undefined);
  // True when the cell advertised demo-login credentials (public demo
  // deployments only) and we seeded the form with them. Gates the
  // "credentials are pre-filled" note under the form.
  const [prefilled, setPrefilled] = useState(false);
  // "login" is the normal form; "forgot" swaps in the password-reset
  // request form (forgotSent flips it to the neutral confirmation);
  // "setup" is the first-run create-your-admin-account screen a pristine
  // install boots into.
  const [mode, setMode] = useState<"login" | "forgot" | "mfa" | "setup">("login");
  // Extra fields for the first-run setup form (email + password reuse the
  // sign-in state so switching modes keeps what was typed).
  const [setupName, setSetupName] = useState("");
  const [setupConfirm, setSetupConfirm] = useState("");
  const [setupBusy, setSetupBusy] = useState(false);
  const [forgotSent, setForgotSent] = useState(false);
  const [forgotBusy, setForgotBusy] = useState(false);
  // MFA second step: set when login returns mfa_required.
  const [mfaToken, setMfaToken] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  // SSO providers (enabled OIDC providers, EE). Empty when none/unlicensed.
  const [ssoProviders, setSsoProviders] = useState<SsoProviderButton[]>([]);

  // On mount, load any SSO buttons and surface an sso_error bounced back from
  // a failed OIDC callback (?sso_error=…).
  useEffect(() => {
    api.listSsoProviders().then((r) => setSsoProviders(r.providers ?? [])).catch(() => {});
    const params = new URLSearchParams(window.location.search);
    const ssoErr = params.get("sso_error");
    if (ssoErr) {
      setError(ssoErr);
      window.history.replaceState({}, "", window.location.pathname);
    }
  }, []);

  const submitMfa = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await api.mfaVerify(mfaToken, mfaCode.trim());
      await onSuccess();
    } catch (e) {
      const msg = String((e as Error).message ?? e);
      setError(msg.startsWith("401") ? "Incorrect code. Try again, or use a backup code." : msg);
      setBusy(false);
    }
  };

  const submitForgot = async (e: FormEvent) => {
    e.preventDefault();
    if (forgotBusy) return;
    setForgotBusy(true);
    try {
      await api.forgotPassword(email.trim());
    } catch {
      // Ignore — the endpoint is intentionally non-revealing; we show the
      // same confirmation regardless so we never leak which emails exist.
    }
    setForgotSent(true);
    setForgotBusy(false);
  };

  useEffect(() => {
    let cancelled = false;
    api
      .installState()
      .then((s) => {
        if (cancelled) return;
        setShowFreshHint(s.fresh);
        // Demo cells advertise public credentials — seed the form so a
        // visitor is one click from signed in. Never overwrite anything
        // the user already typed (the fetch races their first keypress).
        const p = s.prefill;
        if (p?.email) {
          setEmail((cur) => cur || p.email);
          setPassword((cur) => cur || p.password);
          setPrefilled(true);
        } else if (s.fresh) {
          // Pristine install: greet with the first-run setup screen
          // instead of a login form guarding seed credentials. Only if
          // the user hasn't already navigated somewhere else.
          setMode((m) => (m === "login" ? "setup" : m));
        }
      })
      .catch(() => {
        // Network / 500 — leave hint hidden. The hint is only useful
        // info for fresh installs; on a real deployment its absence
        // is the correct silent failure mode.
        if (!cancelled) setShowFreshHint(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // First-run setup: personalize the seeded admin, then log straight in
  // with the new credentials (which also seals the bootstrap endpoint).
  const submitSetup = async (e: FormEvent) => {
    e.preventDefault();
    if (setupBusy) return;
    if (password !== setupConfirm) {
      setError("Passwords don't match.");
      return;
    }
    setSetupBusy(true);
    setError(null);
    try {
      await api.bootstrapAdmin({ name: setupName.trim(), email: email.trim(), password });
      await api.login({ email: email.trim(), password });
      await onSuccess();
    } catch (err) {
      const msg = String((err as Error).message ?? err);
      if (msg.startsWith("409")) {
        // Someone beat us to it (or an API login flipped freshness):
        // the install is set up, so fall back to the sign-in form.
        setMode("login");
        setError("This install is already set up — sign in below.");
      } else {
        setError(msg);
      }
      setSetupBusy(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.login({ email: email.trim(), password });
      // MFA-enabled accounts get a pending token instead of a session.
      if (res.mfa_required && res.mfa_token) {
        setMfaToken(res.mfa_token);
        setMode("mfa");
        setBusy(false);
        return;
      }
      await onSuccess();
    } catch (e) {
      const msg = String((e as Error).message ?? e);
      // 401 from the cell-api → user-friendly message.
      setError(msg.startsWith("401") ? "Invalid email or password." : msg);
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        background: "var(--surface)",
        color: "var(--ink)",
        padding: "24px 16px",
      }}
    >
      <div
        className="card"
        style={{
          width: "100%",
          maxWidth: 380,
          padding: 28,
          background: "var(--surface-2)",
          border: "1px solid var(--border)",
          borderRadius: 12,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 18 }}>
          <LogoMark size={32} style={{ color: "var(--primary)" }} />
          <div style={{ fontSize: 18, fontWeight: 700 }}>
            {mode === "forgot" ? "Reset your password" : mode === "mfa" ? "Two-factor authentication" : mode === "setup" ? "Welcome to Sluicio" : "Sign in to Sluicio"}
          </div>
        </div>

        {mode === "setup" ? (
          <form onSubmit={submitSetup} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <p className="muted" style={{ fontSize: 12.5, lineHeight: 1.55, margin: 0 }}>
              Create your admin account to finish setting up this install.
            </p>
            <label className="form__label">
              Name
              <input className="search__input" value={setupName} onChange={(e) => setSetupName(e.target.value)}
                placeholder="Ada Lovelace" autoComplete="name" autoFocus />
            </label>
            <label className="form__label">
              Email
              <input type="email" className="search__input" value={email} onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com" autoComplete="email" required />
            </label>
            <label className="form__label">
              Password
              <input type="password" className="search__input" value={password} onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password" minLength={8} required />
            </label>
            <label className="form__label">
              Confirm password
              <input type="password" className="search__input" value={setupConfirm} onChange={(e) => setSetupConfirm(e.target.value)}
                autoComplete="new-password" minLength={8} required />
            </label>
            {error && <div className="alert alert--error" role="alert">{error}</div>}
            <button type="submit" className="btn btn--primary"
              disabled={setupBusy || !email.trim() || !password || !setupConfirm} style={{ marginTop: 6 }}>
              {setupBusy ? "Setting up…" : "Create admin account"}
            </button>
            <button type="button" className="btn btn--link"
              onClick={() => { setError(null); setMode("login"); }} style={{ fontSize: 12.5 }}>
              Skip — sign in with an existing account
            </button>
          </form>
        ) : mode === "mfa" ? (
          <form onSubmit={submitMfa} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <p className="muted" style={{ fontSize: 12.5, lineHeight: 1.55, margin: 0 }}>
              Enter the 6-digit code from your authenticator app (or a backup code).
            </p>
            <label className="form__label">
              Code
              <input className="search__input" value={mfaCode} onChange={(e) => setMfaCode(e.target.value)}
                placeholder="123456" inputMode="numeric" autoFocus required />
            </label>
            {error && <div className="alert alert--error" role="alert">{error}</div>}
            <button type="submit" className="btn btn--primary" disabled={busy || !mfaCode.trim()} style={{ marginTop: 6 }}>
              {busy ? "Verifying…" : "Verify"}
            </button>
            <button type="button" className="btn btn--link" onClick={() => { setMode("login"); setMfaCode(""); setMfaToken(""); setError(null); }} style={{ fontSize: 12.5 }}>
              Back to sign in
            </button>
          </form>
        ) : mode === "forgot" ? (
          forgotSent ? (
            <div>
              <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
                If an account exists for <strong>{email.trim() || "that address"}</strong>,
                we've sent a password-reset link. Check your inbox (and spam) — the
                link expires in 1 hour.
              </p>
              <button type="button" className="btn" onClick={() => { setMode("login"); setForgotSent(false); }} style={{ marginTop: 8 }}>
                Back to sign in
              </button>
            </div>
          ) : (
            <form onSubmit={submitForgot} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <p className="muted" style={{ fontSize: 12.5, lineHeight: 1.55, margin: 0 }}>
                Enter your email and we'll send a reset link.
              </p>
              <label className="form__label">
                Email
                <input type="email" className="search__input" value={email}
                  onChange={(e) => setEmail(e.target.value)} placeholder="you@sluicio.com" autoComplete="email" autoFocus required />
              </label>
              <button type="submit" className="btn btn--primary" disabled={forgotBusy || !email.trim()} style={{ marginTop: 6 }}>
                {forgotBusy ? "Sending…" : "Send reset link"}
              </button>
              <button type="button" className="btn btn--link" onClick={() => setMode("login")} style={{ fontSize: 12.5 }}>
                Back to sign in
              </button>
            </form>
          )
        ) : (
        <form onSubmit={submit} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <label className="form__label">
            Email
            <input
              type="email"
              className="search__input"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@sluicio.com"
              autoComplete="email"
              autoFocus
              required
            />
          </label>
          <label className="form__label">
            Password
            <input
              type="password"
              className="search__input"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </label>

          {error && (
            <div className="alert alert--error" role="alert">
              {error}
            </div>
          )}

          <button
            type="submit"
            className="btn btn--primary"
            disabled={busy || !email.trim() || !password}
            style={{ marginTop: 6 }}
          >
            {busy ? "Signing in…" : "Sign in"}
          </button>
          <button
            type="button"
            className="btn btn--link"
            onClick={() => { setError(null); setMode("forgot"); }}
            style={{ fontSize: 12.5, alignSelf: "center" }}
          >
            Forgot password?
          </button>
        </form>
        )}

        {mode === "login" && ssoProviders.length > 0 && (
          <div style={{ marginTop: 18 }}>
            <div className="muted" style={{ textAlign: "center", fontSize: 12, marginBottom: 10 }}>or sign in with</div>
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {ssoProviders.map((p) => (
                <a key={p.id} className="btn" style={{ textAlign: "center" }} href={`/api/v1/auth/sso/${encodeURIComponent(p.id)}/start`}>
                  {p.name}
                </a>
              ))}
            </div>
          </div>
        )}

        {prefilled && mode === "login" && (
          <p className="muted" style={{ fontSize: 12, marginTop: 18, lineHeight: 1.5 }}>
            Public demo environment — the credentials are pre-filled,
            just press <strong>Sign in</strong>.
          </p>
        )}

        {showFreshHint && mode === "login" && (
          <p className="muted" style={{ fontSize: 12, marginTop: 18, lineHeight: 1.5 }}>
            Sluicio ships with a default admin account on first boot:{" "}
            <strong>admin@sluicio.local</strong> / <strong>admin</strong>.
            Once you’re in, change the password from the user menu.
          </p>
        )}
      </div>
    </div>
  );
}
