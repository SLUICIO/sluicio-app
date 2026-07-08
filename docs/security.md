<!-- SPDX-License-Identifier: Apache-2.0 -->

# Security principles

How Sluicio protects the things it holds: credentials, sessions,
telemetry ingestion, API access, and the audit trail. Two rules govern
everything here:

1. **No secret is stored in a recoverable form.** Passwords, API
   tokens, and ingest keys are hashed; the one secret that must be
   recoverable (TOTP seeds) is encrypted, never plaintext.
2. **Every claim below is checkable.** Each section names the
   mechanism, the code that implements it, and — where it matters —
   how to verify it on a running cell, not just read about it.

---

## Tamper-evident audit log (hash chain)

Every audited action (logins, config changes, member/role changes,
announcements, maintenance windows, …) is appended to a per-org
**hash chain**:

```
entry_hash = SHA-256( prev_hash ∥ canonical(entry) )
```

Each record embeds its predecessor's hash, so the log is one welded
sequence ([`ee/audit/store.go`](../ee/audit/store.go)):

- **Editing** any historical row invalidates its hash and every hash
  after it.
- **Deleting** a middle row breaks the chain at the gap.
- **Truncating the tail** — the classic "delete the evidence" move —
  is caught by a separate anchor table that independently remembers
  the newest hash.
- Appends are serialized per org with an advisory lock so concurrent
  writes cannot fork the chain, and the entry ID is generated
  application-side because the hash must cover it.

### Verified continuously, not theoretically

Verification is a first-class API, not a design note:

```
GET /api/v1/audit-log/verify        (org admin, Enterprise)
```

It replays the entire chain and reports either a clean result or the
exact entry where integrity breaks. The same check backs the
**Verify** action on *Settings → Audit log*. Because it is a plain
authenticated endpoint, you can make it *continuous* with your
existing monitoring — e.g. a cron/uptime check that calls it hourly
(a read-only service-account token is enough) and alerts on anything
but a clean result. Tampering then has a detection latency of one
schedule interval, not "whenever someone happens to look".

Honest scope: a hash chain makes tampering **evident**, not
impossible — an attacker with unrestricted database access and time
could rewrite the whole chain from the falsified point forward.
Continuous verification shrinks the window in which that goes
unnoticed; externally anchoring the head hash is the escalation path
for regimes that need more.

## Passwords

Passwords are stored as **argon2id** hashes
([`identity/password.go`](../services/cell-api/internal/identity/password.go))
in PHC format — `$argon2id$v=19$m=65536,t=3,p=4$…` — i.e. 64 MiB
memory-hard, 3 iterations, 4 lanes, with a per-user random salt.
Nothing anywhere in the system can recover a password; login recomputes
the hash and compares.

Around the hash:

- Admin-set temporary passwords force a change on next login; until
  then the session is locked to the change-password surface
  (server-enforced 403, not just UI).
- Password-reset links are single-use and expire after 1 hour; the
  request endpoint is deliberately non-revealing (the same response
  whether or not the email exists).
- Sessions are HttpOnly, SameSite=Lax cookies with a bounded TTL —
  JavaScript can never read them.

## Multi-factor authentication (TOTP)

MFA is standard TOTP (RFC 6238 — Google Authenticator, 1Password,
etc.) with backup codes
([`identity/mfa.go`](../services/cell-api/internal/identity/mfa.go)):

- TOTP seeds are **encrypted at rest with AES-GCM** under a key the
  operator supplies (`SLUICIO_MFA_KEY`) — never plaintext in the
  database. Without the key configured, enrollment is refused rather
  than degraded.
- Login with MFA is a two-step exchange: correct password yields a
  short-lived, HMAC-signed pending token; only a valid TOTP or backup
  code turns it into a session.
- **Org-wide enforcement** (Enterprise): admins can require MFA for
  everyone. Enforcement is server-side — an unenrolled user is locked
  to the enrollment surface by the API, not by a dismissible banner —
  and the switch refuses to turn on until the enabling operator is
  enrolled themselves (no locking yourself out).

## OTLP ingestion: per-org ingest keys

Telemetry ingestion (`cell-ingest`, OTLP/HTTP) authenticates every
batch with an **ingest key** minted per organization
(*Settings → Ingestion*):

- The full key is displayed **exactly once** at creation. The database
  stores only its SHA-256 hash
  ([`ingestkeys/store.go`](../services/cell-api/internal/ingestkeys/store.go));
  the UI thereafter shows a masked prefix.
- cell-ingest hashes the presented key per batch and looks it up —
  a leaked database dump contains no usable ingest credentials.
- Keys are individually revocable, and the org a key resolves to is
  the org the telemetry lands in — tenant isolation starts at ingest.
- Anonymous ingestion exists only as an explicit opt-in flag for
  air-gapped labs; production guidance is: never on a public host.

## API & MCP access: hashed bearer tokens

The REST API and the MCP endpoint (`/api/v1/mcp`) share one token
model ([`identity/tokens.go`](../services/cell-api/internal/identity/tokens.go)):

- Personal tokens (`con_…`) and service-account tokens (`con_sa_…`)
  are random 32-byte values. The database stores the **SHA-256 hash**
  plus a 12-character display prefix — like ingest keys, the token
  itself is shown once and is unrecoverable afterwards.
- Tokens carry **least-privilege controls**: an optional expiry and a
  scope cap (`scope_role`) that can pin a token below its owner's
  role — an admin's read-only token can never write, even though the
  admin can. Scoped tokens never gain from group roles.
- Service accounts are org-owned machine identities with their own
  role, revocable centrally by admins — CI and AI assistants (MCP)
  get viewer tokens that can observe but never change anything.
- Every request resolves to a principal with a role; endpoints are
  deny-by-default (RBAC), and demo accounts are structurally blocked
  from self-service and org administration.

## The boundary: what the deployment owns

Sluicio's images terminate plain HTTP; **TLS is the reverse proxy's
job** (the shipped Caddy config does this with automatic certificates).
Database credentials, the MFA key, and license keys are supplied by
the operator's environment — the [server deployment
guide](../deploy/server/) is the reference for running all of it with
real secrets. Vulnerability reports: **support@sluicio.com**.
