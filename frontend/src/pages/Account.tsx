// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Account — the per-user settings surface. Distinct from /settings
// which is org-level admin. Tabs:
//
//   Profile  — name + email, editable
//   Password — change-password form (current + new + confirm)
//   Tokens   — personal access tokens (mint / list / revoke)
//   Theme    — light / dark / auto display preference
//
// All sections operate on the authenticated user only — there's no
// admin-on-behalf-of path here. Mutations call /api/v1/me* endpoints
// that key entirely off the cookie / bearer.
//
// Tokens were previously under /settings; they belong here because
// they're personal credentials, not org-level admin surfaces.

import { FormEvent, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../api/client";
import type { ApiToken, CreateTokenResponse, MFAStatusResponse } from "../api/types";
import { EditDrawer } from "../components/primitives";
import { useTheme, type Theme } from "../lib/useTheme";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";

type TabKey = "profile" | "password" | "mfa" | "tokens" | "theme";

const TABS: { key: TabKey; label: string }[] = [
  { key: "profile", label: "Profile" },
  { key: "password", label: "Password" },
  { key: "mfa", label: "Two-factor" },
  { key: "tokens", label: "Tokens" },
  { key: "theme", label: "Theme" },
];

export default function Account() {
  usePageTitle("Account");
  const { user } = useCurrentUser();
  const isDemo = user.isDemo ?? false;
  // Tab in the URL (?tab=) so it's deep-linkable + copy-paste-able.
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get("tab");
  const tab: TabKey = TABS.some((x) => x.key === tabParam) ? (tabParam as TabKey) : "profile";
  const setTab = (key: TabKey) =>
    setSearchParams(
      (prev) => {
        prev.set("tab", key);
        return prev;
      },
      { replace: true },
    );
  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Account</h1>
          <p className="page__subtitle">
            Your personal profile, password, tokens, and display preferences.
          </p>
        </div>
      </div>

      <div
        className="card"
        style={{ padding: 0, border: "1px solid var(--border)", borderRadius: 8 }}
      >
        <div
          style={{
            display: "flex",
            borderBottom: "1px solid var(--border)",
            background: "var(--surface-2)",
            padding: "4px 4px 0 4px",
          }}
        >
          {TABS.map((t) => (
            <button
              key={t.key}
              type="button"
              onClick={() => setTab(t.key)}
              style={{
                padding: "10px 16px",
                background: "transparent",
                border: 0,
                borderBottom: tab === t.key ? "2px solid var(--primary)" : "2px solid transparent",
                color: tab === t.key ? "var(--ink)" : "var(--muted)",
                fontWeight: tab === t.key ? 600 : 500,
                cursor: "pointer",
                fontSize: 13,
              }}
            >
              {t.label}
            </button>
          ))}
        </div>
        <div style={{ padding: 18 }}>
          {isDemo && tab !== "theme" ? (
            // The backend rejects these endpoints with 403 for demo
            // accounts anyway; this is the friendly explanation.
            <div className="alert" style={{ maxWidth: 560 }}>
              This is a <strong>shared demo account</strong> — profile,
              password, two-factor and token settings are disabled so the
              login keeps working for every visitor. Explore the product
              freely; nothing here can be broken.
            </div>
          ) : (
            <>
              {tab === "profile" && <ProfileTab />}
              {tab === "password" && <PasswordTab />}
              {tab === "mfa" && <MFATab />}
              {tab === "tokens" && <TokensTab />}
            </>
          )}
          {tab === "theme" && <ThemeTab />}
        </div>
      </div>
    </div>
  );
}

// ── Profile ────────────────────────────────────────────────────────────

function ProfileTab() {
  const { user } = useCurrentUser();
  const [name, setName] = useState(user.name ?? "");
  const [email, setEmail] = useState(user.email);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState(false);

  const dirty = name !== (user.name ?? "") || email.trim().toLowerCase() !== user.email.trim().toLowerCase();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setOk(false);
    try {
      const updated = await api.updateMe({
        // Only send fields that actually changed — the backend
        // treats empty as "no change", but sending what hasn't moved
        // is just noise.
        ...(name !== (user.name ?? "") ? { name } : {}),
        ...(email.trim().toLowerCase() !== user.email.trim().toLowerCase()
          ? { email: email.trim() }
          : {}),
      });
      setOk(true);
      // Page is alive; reload /me so the rest of the app picks up
      // the change without a hard refresh. The simplest tool here is
      // a full window reload — we don't have a global "refresh me"
      // signal exposed from UserProvider.
      void updated;
      window.setTimeout(() => window.location.reload(), 400);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="form" onSubmit={submit} style={{ maxWidth: 480 }}>
      <label className="form__label">
        Display name
        <input
          className="search__input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Your name"
        />
        <span className="form__hint">Shown in the user menu + member lists.</span>
      </label>
      <label className="form__label">
        Email
        <input
          type="email"
          className="search__input"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
        <span className="form__hint">
          Changing email also changes how you sign in. Other devices stay signed in.
        </span>
      </label>
      {error && <div className="alert alert--error">{error}</div>}
      {ok && (
        <div className="alert alert--ok">
          Saved. Reloading…
        </div>
      )}
      <div className="form__actions">
        <button type="submit" className="btn btn--primary" disabled={busy || !dirty || !email.trim()}>
          {busy ? "Saving…" : "Save changes"}
        </button>
      </div>
    </form>
  );
}

// ── Password ───────────────────────────────────────────────────────────

function PasswordTab() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState(false);

  const mismatch = next.length > 0 && confirm.length > 0 && next !== confirm;
  const tooShort = next.length > 0 && next.length < 8;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (mismatch || tooShort) return;
    setBusy(true);
    setError(null);
    setOk(false);
    try {
      await api.changePassword({ current_password: current, new_password: next });
      setOk(true);
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="form" onSubmit={submit} style={{ maxWidth: 480 }}>
      <label className="form__label">
        Current password
        <input
          type="password"
          className="search__input"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          autoComplete="current-password"
          required
        />
      </label>
      <label className="form__label">
        New password
        <input
          type="password"
          className="search__input"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          autoComplete="new-password"
          minLength={8}
          required
        />
        <span className="form__hint">Min 8 characters.</span>
      </label>
      <label className="form__label">
        Confirm new password
        <input
          type="password"
          className="search__input"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="new-password"
          required
        />
        {mismatch && <span className="form__hint" style={{ color: "var(--err)" }}>Passwords don't match.</span>}
      </label>
      {error && <div className="alert alert--error">{error}</div>}
      {ok && (
        <div className="alert alert--ok">
          Password changed. Sessions on other devices keep working — sign out from each one if you're rotating because of compromise.
        </div>
      )}
      <div className="form__actions">
        <button
          type="submit"
          className="btn btn--primary"
          disabled={busy || !current || !next || !confirm || mismatch || tooShort}
        >
          {busy ? "Updating…" : "Change password"}
        </button>
      </div>
    </form>
  );
}

// ── Two-factor (TOTP) ──────────────────────────────────────────────────

function MFATab() {
  const [status, setStatus] = useState<MFAStatusResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Enrollment state.
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null);
  // Disable state.
  const [disabling, setDisabling] = useState(false);
  const [disableCode, setDisableCode] = useState("");

  const load = () =>
    api.mfaStatus().then(setStatus).catch((e) => setError(String((e as Error).message ?? e)));
  useEffect(() => { load(); }, []);

  if (error) return <div className="alert alert--error">{error}</div>;
  if (!status) return <div className="placeholder">Loading…</div>;

  const startSetup = async () => {
    setError(null);
    try {
      setSetup(await api.mfaSetup());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const confirmEnable = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await api.mfaEnable(code.trim());
      setBackupCodes(r.backup_codes);
      setSetup(null);
      setCode("");
      await load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const disable = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.mfaDisable(disableCode.trim());
      setDisabling(false);
      setDisableCode("");
      setBackupCodes(null);
      await load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  // Just-enabled: show the one-time backup codes.
  if (backupCodes) {
    return (
      <div style={{ maxWidth: 460 }}>
        <h2 style={{ marginTop: 0, fontSize: 16 }}>Save your backup codes</h2>
        <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
          Two-factor authentication is on. Store these one-time backup codes
          somewhere safe — each works once if you lose your authenticator.
          <strong> They won't be shown again.</strong>
        </p>
        <div className="card" style={{ padding: 14, margin: "12px 0", display: "grid", gridTemplateColumns: "1fr 1fr", gap: 6 }}>
          {backupCodes.map((c) => (
            <span key={c} className="mono" style={{ fontSize: 13 }}>{c}</span>
          ))}
        </div>
        <button className="btn btn--primary" onClick={() => setBackupCodes(null)}>I've saved them</button>
      </div>
    );
  }

  if (!status.available) {
    return (
      <div style={{ maxWidth: 480 }}>
        <h2 style={{ marginTop: 0, fontSize: 16 }}>Two-factor authentication</h2>
        <div className="alert alert--warn">
          MFA isn't available on this server — no encryption key is configured.
          Set <code>SLUICIO_MFA_KEY</code> (or let the server generate one) and restart.
        </div>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 480 }}>
      <h2 style={{ marginTop: 0, fontSize: 16 }}>Two-factor authentication</h2>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
        Protect your account with a time-based code from an authenticator app
        (1Password, Google Authenticator, Authy, …). Strongly recommended for
        password accounts.
      </p>

      {status.enabled && !disabling && (
        <div>
          <div className="alert alert--info" style={{ margin: "10px 0" }}>
            ✓ Two-factor authentication is <strong>enabled</strong> on your account.
          </div>
          <button className="btn" onClick={() => setDisabling(true)}>Disable two-factor</button>
        </div>
      )}

      {status.enabled && disabling && (
        <form onSubmit={disable} style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 10 }}>
          <label className="form__label">
            Enter a current code (or a backup code) to disable
            <input className="search__input" value={disableCode} onChange={(e) => setDisableCode(e.target.value)} autoFocus placeholder="123456" />
          </label>
          <div style={{ display: "flex", gap: 8 }}>
            <button className="btn btn--danger" type="submit" disabled={busy || !disableCode.trim()}>
              {busy ? "Disabling…" : "Disable"}
            </button>
            <button className="btn" type="button" onClick={() => { setDisabling(false); setDisableCode(""); }}>Cancel</button>
          </div>
        </form>
      )}

      {!status.enabled && !setup && (
        <button className="btn btn--primary" onClick={startSetup} style={{ marginTop: 8 }}>
          Set up two-factor
        </button>
      )}

      {!status.enabled && setup && (
        <div style={{ marginTop: 12 }}>
          <p className="muted" style={{ fontSize: 13 }}>1. Scan this with your authenticator app:</p>
          <div style={{ background: "#fff", padding: 12, borderRadius: 8, width: "fit-content" }}>
            <QRCodeSVG value={setup.otpauth_uri} size={160} />
          </div>
          <p className="muted" style={{ fontSize: 12.5, marginTop: 10 }}>
            Or enter this key manually: <span className="mono" style={{ userSelect: "all" }}>{setup.secret}</span>
          </p>
          <form onSubmit={confirmEnable} style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 10 }}>
            <label className="form__label">
              2. Enter the 6-digit code to confirm
              <input className="search__input" value={code} onChange={(e) => setCode(e.target.value)} autoFocus placeholder="123456" inputMode="numeric" />
            </label>
            <div style={{ display: "flex", gap: 8 }}>
              <button className="btn btn--primary" type="submit" disabled={busy || code.trim().length < 6}>
                {busy ? "Verifying…" : "Enable two-factor"}
              </button>
              <button className="btn" type="button" onClick={() => { setSetup(null); setCode(""); }}>Cancel</button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}

// ── Tokens ─────────────────────────────────────────────────────────────
//
// Identical surface to the previous Settings → Tokens tab — moved here
// since tokens are personal, not org-level.

function TokensTab() {
  const [tokens, setTokens] = useState<ApiToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [justMinted, setJustMinted] = useState<CreateTokenResponse | null>(null);

  const refresh = () => {
    api
      .listTokens()
      .then((r) => setTokens(r.tokens))
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, []);

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!tokens) return <div className="placeholder">Loading…</div>;

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
        <div className="muted" style={{ fontSize: 13 }}>
          Personal access tokens for programmatic access. Inherit your role
          in the org you target via the <span className="mono">X-Sluicio-Org</span> header.
        </div>
        <button type="button" className="btn btn--primary" onClick={() => setShowCreate(true)}>
          + New token
        </button>
      </div>

      {showCreate && (
        <EditDrawer title="New token" width="narrow" onClose={() => setShowCreate(false)}>
          <CreateTokenForm
            onClose={() => setShowCreate(false)}
            onMinted={(r) => {
              setJustMinted(r);
              setShowCreate(false);
              refresh();
            }}
          />
        </EditDrawer>
      )}

      {justMinted && (
        <TokenPlaintextDialog token={justMinted} onDismiss={() => setJustMinted(null)} />
      )}

      {tokens.length === 0 ? (
        <div className="placeholder">
          No tokens yet. Click <b>+ New token</b> to mint one.
        </div>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Prefix</th>
              <th>Last used</th>
              <th>Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((t) => (
              <tr key={t.id}>
                <td>{t.name}</td>
                <td className="mono">{t.prefix}…</td>
                <td>
                  {t.last_used_at
                    ? new Date(t.last_used_at).toLocaleString()
                    : <span className="muted">never</span>}
                </td>
                <td>{new Date(t.created_at).toLocaleDateString()}</td>
                <td className="num">
                  <button
                    type="button"
                    className="btn btn--link"
                    style={{ color: "var(--err-ink, #ef4444)" }}
                    onClick={async () => {
                      if (!confirm(`Revoke "${t.name}"? Any caller using this token will start failing.`)) return;
                      try {
                        await api.revokeToken(t.id);
                        refresh();
                      } catch (e) {
                        alert(String((e as Error).message ?? e));
                      }
                    }}
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function CreateTokenForm({ onClose, onMinted }: { onClose: () => void; onMinted: (r: CreateTokenResponse) => void }) {
  const [name, setName] = useState("");
  const [scope, setScope] = useState("");
  const [exp, setExp] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const resp = await api.createToken(name, scope, exp);
      onMinted(resp);
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };
  return (
    <form onSubmit={submit} className="form">
      <label className="form__label">
        Token name
        <input
          className="search__input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. ci, my-laptop, automation"
          required
          autoFocus
        />
        <span className="form__hint">Just a label so you remember what this token is for.</span>
      </label>
      <label className="form__label">
        Access
        <select className="toolbar__select" value={scope} onChange={(e) => setScope(e.target.value)}>
          <option value="">Full — your role</option>
          <option value="editor">Editor — read + write, no admin</option>
          <option value="viewer">Read-only</option>
        </select>
        <span className="form__hint">Cap the token below your own role — it can never exceed your permissions.</span>
      </label>
      <label className="form__label">
        Expiry
        <select className="toolbar__select" value={exp} onChange={(e) => setExp(Number(e.target.value))}>
          <option value={0}>Never</option>
          <option value={30}>30 days</option>
          <option value={90}>90 days</option>
          <option value={365}>1 year</option>
        </select>
        <span className="form__hint">After this, the token stops working and must be re-created.</span>
      </label>
      {error && <div className="alert alert--error">{error}</div>}
      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className="btn btn--primary" disabled={busy || !name.trim()}>
          {busy ? "Minting…" : "Create token"}
        </button>
      </div>
    </form>
  );
}

function TokenPlaintextDialog({ token, onDismiss }: { token: CreateTokenResponse; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard.writeText(token.plaintext);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div
      role="dialog"
      aria-modal="true"
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.4)",
        display: "grid",
        placeItems: "center",
        zIndex: 1000,
      }}
    >
      <div
        className="card"
        style={{
          width: "min(560px, 92vw)",
          background: "var(--surface)",
          border: "1px solid var(--border)",
          borderRadius: 12,
          padding: 20,
          boxShadow: "0 12px 36px rgba(0,0,0,0.24)",
        }}
      >
        <h2 style={{ marginTop: 0, fontSize: 16 }}>Token created — copy it now</h2>
        <p className="muted" style={{ fontSize: 13 }}>
          This is the only time you'll see the full token. Sluicio stores only
          a hash. If you lose it, revoke it and mint a new one.
        </p>
        <div
          className="mono"
          style={{
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 6,
            padding: "10px 12px",
            fontSize: 12,
            wordBreak: "break-all",
            margin: "12px 0",
          }}
        >
          {token.plaintext}
        </div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button type="button" className="btn" onClick={copy}>
            {copied ? "✓ Copied" : "Copy"}
          </button>
          <button type="button" className="btn btn--primary" onClick={onDismiss}>
            I've copied it
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Theme ──────────────────────────────────────────────────────────────

function ThemeTab() {
  const [theme, setTheme] = useTheme();
  const options: { value: Theme; label: string; hint: string }[] = [
    { value: "light", label: "Light", hint: "Warm off-white. Best in daylight." },
    { value: "dark", label: "Dark", hint: "Deep navy. Easier in dim rooms; control-room feel, not OLED-pitch." },
    { value: "auto", label: "Auto", hint: "Follow your OS preference. Switches automatically." },
  ];
  return (
    <div style={{ maxWidth: 480 }}>
      <p className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
        The theme toggle in the top bar is the quick-switch — this is the same
        setting in its full form. Stored on this device.
      </p>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {options.map((o) => (
          <label
            key={o.value}
            style={{
              display: "flex",
              gap: 12,
              padding: "10px 12px",
              borderRadius: 8,
              border: `1px solid ${theme === o.value ? "var(--primary)" : "var(--border)"}`,
              background: theme === o.value ? "var(--primary-soft)" : "var(--surface-2)",
              cursor: "pointer",
            }}
          >
            <input
              type="radio"
              name="theme"
              value={o.value}
              checked={theme === o.value}
              onChange={() => setTheme(o.value)}
              style={{ marginTop: 4 }}
            />
            <div>
              <div style={{ fontWeight: 600, fontSize: 13 }}>{o.label}</div>
              <div className="muted" style={{ fontSize: 12 }}>{o.hint}</div>
            </div>
          </label>
        ))}
      </div>
    </div>
  );
}
