<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Authentication and authorization

Sluicio is multi-tenant — every resource (service, integration,
schema, map, alert, …) belongs to an **org**. A user can belong to
multiple orgs with potentially different roles in each.

**Sluicio ships with native identity.** Out of the box, the cell-api
runs its own login: users are stored in the `users` table with
argon2id password hashes; sessions live in the `sessions` table behind
an HTTP-only cookie. Your customer gets a working app from the first
`make up` without standing up any external service.

**When a customer is ready to plug into their existing IdP**, an org
admin adds an OIDC provider in *Settings → SSO*. After that, users
can sign in via their corporate IdP (Entra, Okta, Google Workspace,
Keycloak, anything OIDC-conformant). Local password login remains as
a fallback per org admin's choice.

Sluicio does NOT ship its own IdP. The auth code is self-contained in
the cell-api.

## Architecture

```
   ┌────────────┐
   │  Frontend  │  Login form on / → POST /api/v1/auth/login
   │  (browser) │ ─────────────────────────────────────────┐
   └─────┬──────┘  ← Set-Cookie: Sluicio-Session=<opaque>  │
         │                                                  │
         │ Cookie: Sluicio-Session=<opaque>                 │
         │ (HttpOnly, SameSite=Lax, Secure in prod)         │
         ▼                                                  ▼
   ┌──────────────────────────────────────────────────────────┐
   │ cell-api  (auth middleware)                              │
   │   ① session cookie → sessions row → user + org_members   │
   │   ② or Authorization: Bearer con_… → api_tokens lookup   │
   │   ③ or OIDC JWT → JWKS verify → users (by email match)   │
   │       (only if an auth_provider is configured for the org)│
   └──────────────┬───────────────────────────────────────────┘
                  │
                  ▼
            ┌──────────┐  orgs, org_members, users (password_hash, …),
            │ Postgres │  sessions, auth_providers, oidc_subjects,
            └──────────┘  api_tokens, service_accounts
```

## Principles

- **Native auth is the floor.** Login works without any external
  dependency. Customers who are not (yet) ready to do enterprise SSO
  are fully supported.
- **OIDC federation is opt-in, per-org.** Each org admin configures
  zero or more providers. Sluicio talks OIDC directly to whatever
  the customer points us at; we don't proxy through a bundled IdP.
- **Identity is keyed by email.** When a user signs in via OIDC for
  the first time, we match the IdP's claim email against an existing
  `users` row and link the `(provider_id, external_sub)` in
  `oidc_subjects`. Returning users get matched on either the local
  email/password or the linked OIDC subject.
- **Sessions, not self-signed JWTs.** A successful login (local or
  OIDC) creates a row in `sessions` and sets an HTTP-only cookie
  carrying its opaque id. Revoke = delete the row. The frontend
  never sees a JWT; only api_tokens (PATs and service accounts)
  expose bearer tokens.

## Authorization model

Three roles per `(user, org)`:

| Role | Can read | Can mutate resources | Can manage members + tokens + SSO |
|------|----------|---------------------|------------------------------------|
| `viewer` | yes | no | no |
| `editor` | yes | yes | no |
| `admin` | yes | yes | yes |

Service accounts carry the same enum, but it's a per-account (not
per-membership) value — service accounts always belong to exactly one
org.

Handler-level checks call `Role.CanWrite()` / `Role.CanAdmin()`
rather than open-coded comparisons, so widening the model later
(per-resource permissions, custom roles) is a single point of change.

## Tokens

A single `api_tokens` table holds both:

  - **Personal access tokens** (`owner_type='user'`) — token inherits
    the user's role within the targeted org via `org_members`. For
    scripts and CLIs that represent a specific human.
  - **Service-account tokens** (`owner_type='service_account'`) —
    token inherits the service account's role within its single
    owning org. For automation outliving any one human.

The plaintext token is shown once at creation and never persisted.
Verification is argon2id against the stored hash. The first 12 chars
of the encoded token (`con_pat_a1b2c3d4`) are stored as `prefix` for
the user-visible token list.

## Dev setup

The dev stack is `make up` away — Postgres + ClickHouse + Prometheus.
There is **no** identity provider in the stack. On first boot, the
cell-api seeds a Default org and an admin user:

| Email | Password |
|-------|----------|
| `admin@sluicio.local` | `admin` |

(The migration leaves `password_hash` NULL; the cell-api hashes
"admin" on first start via `BootstrapSeedAdminPassword`, flagged
`must_reset_password=true`.)

Then visit `http://localhost:5173/` and log in. That's it.

### Testing OIDC federation locally

Once P5 lands the *Settings → SSO* surface, you can spin up any
OIDC provider alongside this stack to exercise federation. Keycloak,
Dex, Hydra, or even a hosted dev tier of Auth0/Clerk all work. The
provider is configured **inside Sluicio** — Sluicio doesn't need to
know whether it's talking to your customer's Entra tenant or a
locally-running Dex. Whatever conformance with OIDC you can point us
at is fine.

We deliberately do *not* check a local IdP into docker-compose. It
would make people think Sluicio ships with one.

## Customer-side deployment

### "Use Sluicio out of the box"

Customer pulls the helm chart / runs the docker-compose / whatever.
Sluicio comes up. They log in with the seeded admin credentials,
rotate the password, invite their team via email (P5 surface), and
get to work. Total setup: one password change.

### "Connect Sluicio to our existing IdP"

When ready, an org admin goes to *Settings → SSO*, picks "Add OIDC
provider", and fills in:

  - **Provider name** (display label on the login button, e.g.
    "Acme SSO")
  - **Issuer URL** (e.g. `https://login.acme.example/`)
  - **Client ID** (from registering Sluicio as a client in their IdP)
  - **Client secret** (likewise)
  - **Claim mapping** (which claim → email, name, sub — defaults
    match the OIDC standard claims)

Sluicio fetches `${issuer}/.well-known/openid-configuration`,
validates JWTs against the JWKS, and on a successful sign-in either
matches an existing user by email or creates a new one. The
`(provider_id, external_sub)` is linked so subsequent sign-ins are a
direct lookup.

Per-org SSO means tenants on the same Sluicio deployment can each
federate to their own IdP without affecting the others.

## Production checklist

When deploying Sluicio to a real environment:

- [ ] **Change the seeded admin password.** Log in once and rotate;
      the user comes in with `must_reset_password=true` to remind
      you. Or pre-bake a different seed via the
      `BootstrapSeedAdminPassword` call in `main.go` (with a more
      secure default password, or skip the bootstrap entirely and
      use the CLI to provision the first admin).
- [ ] **Set `Secure` on the session cookie.** The middleware ships
      with `SameSite=Lax`; set the `Secure` flag in production via
      the cell-api's auth config so the cookie is HTTPS-only.
- [ ] **Configure SMTP** for password resets + member invitations
      (P5).
- [ ] **Rotate api_token-derived encryption key** before any
      production data lands. The auth_providers.client_secret column
      is encrypted with this key.
- [ ] **(Optional)** Configure OIDC for each tenant org so users
      can sign in via their corporate IdP instead of email/password.
- [ ] **(Optional)** Disable local password sign-in per org once SSO
      is verified working, so all users must go through the IdP.

## Status

| Phase | What | Status |
|------|------|--------|
| P1 | Schema (orgs, users w/ password_hash, members, sessions, api_tokens, service_accounts, auth_providers, oidc_subjects), identity package (types + read/write + argon2id) | ✅ done |
| P2 | Auth middleware on cell-api (session cookie + bearer token paths), `/api/v1/auth/login` + `/logout` + `/me`, one demo handler protected end-to-end | ⏳ next |
| P3 | Auth middleware applied to every cell-api handler; `integrations.DefaultOrgID` removed; role checks on mutating routes | ⏳ |
| P4 | Frontend login page, real `useCurrentUser`, org switcher | ⏳ |
| P5 | Settings UI (member management, token management, SSO/OIDC provider config); OIDC sign-in flow end-to-end | ⏳ |

Until P2 lands the cell-api remains *unauthenticated* — every request
still resolves to `integrations.DefaultOrgID`. The P1-foundation
tables exist but nothing reads from them yet beyond the
`BootstrapSeedAdminPassword` call on startup.

## Where things live

| Path | Owns |
|------|------|
| `services/cell-api/internal/migrations/sql/0017_auth.up.sql` | Original auth schema (orgs, users, members, api_tokens, service_accounts) |
| `services/cell-api/internal/migrations/sql/0018_native_auth.up.sql` | Native-auth refactor: password_hash, sessions, auth_providers, oidc_subjects |
| `services/cell-api/internal/identity/types.go` | `Role`, `Org`, `User`, `Membership`, `Session`, `AuthProvider`, `ServiceAccount`, `Principal` |
| `services/cell-api/internal/identity/password.go` | argon2id `HashPassword` / `VerifyPassword`, session id generation |
| `services/cell-api/internal/identity/store.go` | All read+write paths against the auth tables |
| `services/cell-api/cmd/cell-api/main.go` | `BootstrapSeedAdminPassword` on startup |
| (P2) `services/cell-api/internal/api/middleware/auth.go` | Session-cookie + bearer-token verification, Principal injection |
| (P2) `services/cell-api/internal/api/handlers_auth.go` | `/api/v1/auth/login`, `/logout`, `/me` |
