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

1. The login page is email-first. As the user types, it asks
   `POST /api/v1/auth/sso/discover {email}` (public). When an enabled
   provider holds the address's **email domain**, the answer is that
   provider's start URL and the page offers "Continue with single sign-on"
   instead of the password field; otherwise the password field stays. The
   answer depends on the domain alone, never on whether an account exists,
   so the page never lists the organizations on the cell.
   `GET /api/v1/auth/sso/providers` still returns the enabled providers as
   buttons, but only on a cell with a single org; with several it is
   empty, because the list would name every tenant.
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

## Email domains (migration 0109)

An IdP asserts whatever email address it likes, and on a cell with several
orgs every org admin can configure one. So each provider has the **email
domains it speaks for** (`auth_provider_domains`, one provider per domain
on the whole cell). They decide two things: which provider the login page
sends an address to, and which accounts the provider may link to or
create (below).

Domains are assigned by a **cell operator**
(`PUT /api/v1/operator/auth-providers/{id}/domains`), not by the org admin
who configures the provider: the assignment decides whose sign-in page a
domain's users are sent to, so an org admin who could set it could claim
another org's domain. On a single-org self-hosted cell the bootstrap admin
is the operator. Org admins see the domains read-only on the SSO tab.

## Identity linking + JIT provisioning

- Look up `oidc_subjects (provider_id, external_sub)`. If found → that user.
- Otherwise the asserted email is only used if the provider **speaks for**
  it: its domain is one of the provider's domains, or the cell has a single
  org (where the only IdP belongs to the only org, as before domains
  existed). An `email_verified: false` claim counts as no email. Outside
  that, the sign-in is refused with the same message whether or not an
  account with the address exists, so whoever runs the IdP learns nothing.
- Within it: match an existing user by email and link (`oidc_subjects`
  row), so a pre-existing local user adopts SSO cleanly; else, if the
  provider has `jit_provisioning = true`, create the user (no local
  password, SSO-only) and link. If JIT is off and there's no match → deny
  with a clear "not provisioned" error.

Without this rule, org B's IdP asserting `alice@customer-a.example` signed
in as Alice, with every role she holds in every org, an operator's
included. `sso_account_link_integration_test.go` is that reproduction.

## Moving between orgs

A user in several orgs switches with the org switcher; every request
carries the org in `X-Sluicio-Org`. After an SSO sign-in the callback adds
`?org=<slug>` to the return URL, so the user lands in the org whose IdP
they used. A cell operator can also open any org they are not a member of:
there they act as a **viewer** (`operator_visit` on the principal), and
the switcher lists those orgs under "Other organizations, read-only". To
change anything they add themselves as a member from the operator
console. Nobody else ever sees which orgs exist.

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
| GET | `/api/v1/auth/sso/providers` | public (login page; single-org cells only) |
| POST | `/api/v1/auth/sso/discover` | public (login page) |
| PUT | `/api/v1/operator/auth-providers/{id}/domains` | operator + `FeatureSSO` |
| GET | `/api/v1/auth/sso/{id}/start` | public |
| GET | `/api/v1/auth/sso/callback` | public |
| GET/POST/PUT/DELETE | `/api/v1/settings/auth-providers…` | admin + `FeatureSSO` |
| GET/POST/PUT/DELETE | `…/auth-providers/{id}/mappings…` | admin + `FeatureSSO` |

## Out of scope (v1)

SAML, SCIM auto-provisioning, full deprovisioning of manually-assigned
teams, multiple-IdP account linking UX. Tracked for later.
