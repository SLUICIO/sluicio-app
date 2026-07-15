# API & API keys (1.0 bar)

Status: **design agreed, in progress** (2026-06-25).

How programmatic access to Sluicio's management API works, and what it needs to
reach 1.0 grade. The model is already the right one — the work is finishing it.

## Where to read / try the API

- **Interactive reference** (renders the spec + built-in client — paste a
  Bearer token and fire requests against the cell): `/api/docs`
- **OpenAPI 3.1** (canonical machine format): `/api/v1/openapi.json`
- **llms.txt** (compact markdown, one line per endpoint — the token-frugal
  format for AI tools reading the spec): `/api/v1/llms.txt`
- **AI agents**: prefer the MCP endpoint (`POST /api/v1/mcp`, same auth,
  curated read-only tools) over spec-driven calls.

All generated from the route table (`make openapi`, CI-guarded), embedded in
the binary, no CDN — works air-gapped.

## Current state

- **Surface:** a versioned REST API under `/api/v1` (cell-api). Telemetry comes
  in separately over OTLP to cell-ingest.
- **Auth (cell-api):** three credential types, resolved by the auth middleware:
  1. `Sluicio-Session` HTTP-only cookie (browser),
  2. `Authorization: Bearer <token>` — an `api_tokens` row,
  3. (telemetry) cell-ingest uses its own org-scoped **ingest keys**
     (`ingest_keys`, SHA-256 + in-memory cache) — out of scope here.
- **Two token kinds**, one `api_tokens` table:
  - **Personal access token** (`owner_type=user`) — inherits the user's role +
    RBAC policies.
  - **Service-account token** (`owner_type=service_account`) — uses the service
    account's own role (admin/editor/viewer) in its org.
- **Token hygiene (already done):** argon2id hash at rest, plaintext shown once,
  `prefix` kept for display, `last_used_at`, `revoked_at`. Bearer over TLS.
- **What's wired:** `GET/POST/DELETE /api/v1/settings/tokens` (personal tokens
  only); the bearer path in middleware resolves both owner types;
  `service_accounts` table + `GetServiceAccount` exist.

## The model (best practice — keep it)

| | Personal access token | Service-account token |
| --- | --- | --- |
| Identity | a person | a machine identity, no person attached |
| Permissions | the user's role + policies | the service account's own role |
| Use for | personal scripts, CLI, notebooks | CI/CD, integrations, automation that outlives an employee |
| Lifecycle | dies with the user's access | independent; admin-managed |

This mirrors GitHub/GitLab/Datadog and is the right design. No redesign needed.

## Gaps to close for 1.0

1. **Service-account management** — `api_tokens` can be owned by a service
   account, but there's **no CRUD** for service accounts and **no endpoint to
   mint a service-account token** (`createToken` rejects non-user callers and
   only mints personal tokens). Without this you can't create a system identity.
   *Biggest functional gap.*
2. **OpenAPI spec + docs** — no machine-readable spec. A "proper API" for 1.0
   ships a versioned OpenAPI document so the surface is documented and clients
   can be generated.
3. **Scopes / least-privilege** — a PAT inherits the user's *full* role; you
   can't issue a read-only or resource-scoped token. Add scopes (or at least a
   role cap so an admin can mint a viewer token).
4. **Expiry + rotation** — `api_tokens` has no `expires_at`; add optional /
   enforced expiry + a rotate flow.
5. **Polish** — token prefix is still `con_` (Conduit) → rename to `slk_`; add
   basic rate-limiting on bearer auth.

## Phasing

### Phase A — Service-account management (the missing identity) ✅ done (2026-06-25)
- Store: `CreateServiceAccount / ListServiceAccounts / UpdateServiceAccount /
  DeleteServiceAccount` (+ `ListAPITokensForServiceAccount`); delete also clears
  the SA's tokens (owner_id isn't an FK).
- Admin-gated routes under `/api/v1/settings/service-accounts` (CRUD) and
  `.../{id}/tokens` (mint shown-once / list / revoke); minting reuses the
  generic `CreateAPIToken` with the `con_sa_` kind.
- Settings → **Service accounts** tab: create (name, role), issue a token (shown
  once), list + revoke; delete the account.
- Verified end-to-end: create SA → mint token → Bearer-auth (200) → revoke →
  rejected (401) → delete.

### Phase B — OpenAPI spec + docs ✅ done (2026-06-25)
- **Generated from the route table** (`cmd/openapi-gen` AST-parses every
  `mux.HandleFunc("METHOD /path", …)`), so the spec can't drift from the code.
  `make openapi` regenerates `internal/api/openapi_gen.json`; `make
  openapi-check` fails if stale (CI guard).
- Served (public): `GET /api/v1/openapi.json` (embedded OpenAPI 3.1) + `GET
  /api/docs` (Redoc). Security schemes: bearer (PAT / service-account) + session
  cookie.
- *Follow-up:* enrich per-endpoint request/response schemas (the generator emits
  paths/methods/params/tags/security + a generic 200/401 today); wire
  `openapi-check` into CI; optional in-app "API docs" link.

### Phase C — Token scopes (least-privilege) ✅ done (2026-06-25)
- Implemented as a **per-token role cap** (`api_tokens.scope_role`): a token's
  effective role is `min(owner role, scope_role)` — `""` = no cap. Enforced in
  the auth middleware (`Role.Cap`) at the existing role gates, so it works for
  both PATs and service-account tokens and can only *narrow*, never widen.
- Mint UIs (Account → personal tokens, Settings → service accounts) offer an
  **Access** choice (full / editor / read-only); token lists show the cap.
- Verified: a viewer-capped token on an admin account reads (200) but is
  rejected on admin/write routes (403); a full token passes (200).
- *Follow-up:* finer resource:action scopes (read:metrics, …) on top of the
  role cap if/when needed.

### Phase D — Expiry + rotation ✅ done (2026-06-25)
- `api_tokens.expires_at` (nullable = never); `ResolveAPIToken` rejects past
  expiry alongside the revoked check. Mint flows offer an expiry
  (never / 30 / 90 / 365 days).
- Rotation: reissue with the same name + access cap, then revoke the old token
  (surfaces the new secret once) — a "Rotate" action on each token. Token lists
  show expiry.
- Verified: a future-expiry token works; once past expiry it's rejected (401).

### Polish
- `con_` → `slk_` token prefix; rate-limit bearer auth.

## Decisions

- Keep the two-token model; **service-account tokens are admin-issued**, PATs
  are self-service (Account page).
- Tokens stay hash-at-rest + shown-once (existing).

## Open

- Scope vocabulary (resource:action vs coarse read/write).
- Whether a PAT may exceed its owner's role (no) and whether to cap it below
  (yes, via scopes).
