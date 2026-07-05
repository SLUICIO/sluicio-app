# Sluicio Enterprise Edition (`ee/`)

This directory holds Sluicio's **Enterprise features**. It is licensed
**separately** from the rest of the repository.

- Everything **outside** `ee/`: `FSL-1.1-Apache-2.0` (see the repo-root `LICENSE`).
- Everything **inside** `ee/`: the **Sluicio Enterprise License** (see
  [`ee/LICENSE.md`](./LICENSE.md)). Files here carry the SPDX header
  `LicenseRef-Sluicio-Enterprise`.

## How the open-core split works

Sluicio ships as a **single binary** that *contains* the Enterprise code but
**gates the features at runtime** by license key. With no key:

- the core product runs **fully** — login, telemetry, dashboards, basic RBAC,
  capped retention — and
- the Enterprise features are simply **off** (the UI shows an upgrade prompt;
  gated endpoints return `402`).

A valid key unlocks the entitled features. Verification is **offline** — the
app embeds an Ed25519 **public** key and checks the signed license locally, so
it works air-gapped with no phone-home. **Never** is the core blocked: an
absent, malformed, or expired key only disables EE features; login and admin
always work so an operator can paste a new key.

## Enterprise features

| Feature | Entitlement key | Notes |
|---|---|---|
| SSO (OpenID Connect) | `sso` | Local-password login stays in core. |
| Advanced RBAC | `rbac_advanced` | Fine-grained group access policies / custom roles. Basic admin/editor/viewer stays in core. |
| Audit logs | `audit_log` | Records mutating admin actions. |
| Long retention | `retention_long` | Lifts the free-tier retention cap. |
| MFA policy | `mfa_policy` | Org-wide MFA enforcement (every member must enrol). Per-user MFA stays in core. |

## Layout

- The offline license **verifier** lives in the open core at
  [`pkg/license/`](../pkg/license) (FSL, deliberately inspectable — anyone can
  audit exactly how keys are checked). It embeds the Ed25519 **public** key
  and is the one place that answers "is feature X entitled?".
- [`cmd/sluicio-license/`](./cmd/sluicio-license) — **internal** tool to mint +
  inspect license keys. Not shipped to customers.

## Keys & secrets — never commit

- The Ed25519 **private** key signs licenses. Keep it **out of the repo**
  (e.g. `~/.sluicio/license_ed25519_private.key`, `chmod 600`). It is
  `.gitignore`d.
- The **public** key (`pkg/license/sluicio_license_ed25519.pub`) is committed
  and embedded in the binary.
- Real signed license tokens are secrets too — don't commit them.

## Minting a license

```bash
# One-time: generate a keypair (prints the public key to embed)
go run ./ee/cmd/sluicio-license keygen -out ~/.sluicio/license_ed25519_private.key

# Mint a customer license
go run ./ee/cmd/sluicio-license mint \
  -key ~/.sluicio/license_ed25519_private.key \
  -customer "Acme AB" \
  -features sso,rbac_advanced,audit_log,retention_long \
  -days 365

# Verify a token against the embedded public key
go run ./ee/cmd/sluicio-license inspect -token "sluicio_lic_…"
```

The running app loads a key from `SLUICIO_LICENSE_KEY` (inline token) or
`SLUICIO_LICENSE_FILE` (path).
