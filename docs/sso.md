# SSO / OIDC (EE)

Status: **in progress**. Gated by the `sso` license entitlement
(`ee/license`, `FeatureSSO`). Per-org OpenID Connect sign-in with claim →
role/team mapping. SAML and SCIM are explicitly out of scope for v1.

## Goal

An org admin configures one or more **OIDC providers** (Entra ID, Okta,
Google, Auth0, Keycloak, …). Users then sign in with "Sign in with
\<provider>", and their **org role** and **team (group) memberships** are
derived from IdP claims so the IdP stays the source of truth for access.

## Flow (authorization code + PKCE)

1. Login page calls `GET /api/v1/auth/sso/providers` (public) → enabled
   providers for the tenant → renders a button per provider.
2. `GET /api/v1/auth/sso/{id}/start` → builds the IdP authorize URL with
   `state` (CSRF), `nonce` (replay), and PKCE `code_challenge`; stashes the
   transient state server-side (short-lived, single-use); 302 to the IdP.
3. IdP authenticates the user → redirects to
   `GET /api/v1/auth/sso/callback?code=…&state=…`.
4. Callback: validate `state`, exchange `code` (+ PKCE verifier) at the
   token endpoint, **verify the ID token** (signature via the issuer JWKS,
   `iss`, `aud`, `exp`, `nonce`) using `github.com/coreos/go-oidc/v3`.
5. Extract claims: email (`claim_email`), name (`claim_name`), subject
   (`claim_sub`), groups (`claim_groups`).
6. **Link or provision** the user (below), **sync role + teams** from the
   claims (below), mint a session cookie, 302 back to the app.

## The callback URL (`redirect_uri`)

The `redirect_uri` Sluicio registers and sends is
`<public-origin>/api/v1/auth/sso/callback`, and it must be byte-identical at
`/start` and `/callback` **and** listed in the IdP's allowed callback URLs.

`<public-origin>` comes from **`SLUICIO_APP_URL`** when set — set it to the URL
users reach Sluicio at (e.g. `https://sluicio.acme.com`). This is required
behind a reverse proxy: the proxy usually rewrites `Host` to the backend, so
reconstructing the origin from request headers is unreliable. When
`SLUICIO_APP_URL` is unset, Sluicio falls back to the request
(`X-Forwarded-Proto` / `X-Forwarded-Host`, then `Host`) — fine for a direct dev
setup, not for production. The admin SSO page shows the exact callback URL to
paste into the IdP.

## Identity linking + JIT provisioning

- Look up `oidc_subjects (provider_id, external_sub)`. If found → that user.
- Else match an existing user by verified email and link (`oidc_subjects`
  row) — so a pre-existing local user adopts SSO cleanly.
- Else, if the provider has `jit_provisioning = true`, create the user
  (no local password — SSO-only) and link. If JIT is off and there's no
  match → deny with a clear "not provisioned" error.

## Claim → access mapping (the requested feature)

A provider names the claim carrying the user's IdP groups/roles via
`claim_groups` (default `groups`). Mappings live in
`auth_provider_claim_mappings`, each row:

| column | meaning |
| --- | --- |
| `claim_value` | the IdP group/role string to match (e.g. `sluicio-admins`, `team-payments`) |
| `org_role` | optional org role to grant (`admin`/`editor`/`viewer`) |
| `group_id` | optional Sluicio **team** (`groups`) to add the user to |
| `group_role` | the role within that team (`admin`/`editor`/`viewer`) |

On each login we resolve **all** mappings whose `claim_value` is present in
the user's groups claim, then:

- **Org role** = the **highest** `org_role` across matched mappings
  (admin > editor > viewer); if none matched, the provider's
  `default_role` (default `viewer`). The org membership is updated to that
  role every login — IdP is authoritative.
- **Teams** = the set of `group_id`s across matched mappings. We sync the
  user's membership **for groups this provider manages** (the distinct
  `group_id`s referenced by the provider's mappings): add the matched ones,
  remove the unmatched ones. Teams the provider doesn't reference are left
  untouched, so manually-managed memberships survive.

This makes "map teams to claims" first-class: an IdP group → a Sluicio
team (which already drives `group_access_policies` visibility + service
scoping) and/or an org-wide role.

## Schema (migration 0054)

- Extend `auth_providers`: `scopes` (default `openid email profile`),
  `claim_groups` (default `groups`), `default_role` (default `viewer`),
  `jit_provisioning` (default true).
- New `auth_provider_claim_mappings (id, provider_id, claim_value,
  org_role, group_id, group_role, created_at)`.
- New `sso_login_states (state, provider_id, nonce, code_verifier,
  redirect_to, expires_at)` — transient PKCE/CSRF state (5-min TTL,
  single-use). (Postgres-backed so it survives multiple cell-api replicas.)

## Security

- PKCE (S256) + `state` + `nonce` on every flow.
- ID-token verification via the issuer JWKS (never trust unverified
  claims); enforce `iss`/`aud`/`exp`.
- `client_secret` stored encrypted with the cell-api data key (the column
  already exists); never returned over the API after create.
- All provider-config endpoints are **admin-only** AND gated by
  `FeatureSSO`. The login/callback endpoints are public (pre-session) but
  only act on enabled providers.

## Endpoints

| Method | Path | Auth |
| --- | --- | --- |
| GET | `/api/v1/auth/sso/providers` | public (login page) |
| GET | `/api/v1/auth/sso/{id}/start` | public |
| GET | `/api/v1/auth/sso/callback` | public |
| GET/POST/PUT/DELETE | `/api/v1/settings/auth-providers…` | admin + `FeatureSSO` |
| GET/POST/PUT/DELETE | `…/auth-providers/{id}/mappings…` | admin + `FeatureSSO` |

## Out of scope (v1)

SAML, SCIM auto-provisioning, full deprovisioning of manually-assigned
teams, multiple-IdP account linking UX. Tracked for later.
