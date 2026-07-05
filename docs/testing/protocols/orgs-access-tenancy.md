<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Orgs, access control & tenant isolation

| Field | Value |
|-------|-------|
| **Area** | Org membership, roles, groups & policies, API tokens, ingest keys, multi-tenancy |
| **Automation status** | Partial (seed membership covered by integration test; the rest manual) |
| **Automated by** | `identity/store_integration_test.go` (seed membership/role) |
| **Last reviewed** | 2026-06-20 |

## Preconditions
- Stack up; admin for member/token/group mutations. Tenant-isolation cases need a **second org**.

## Members & roles

### Case 1 — List / add / update / remove members
- **Endpoints:** `GET /api/v1/settings/members`, `POST`, `PATCH …/{user_id}`, `DELETE …/{user_id}`
- **Steps:** Add a member (email, name, initial password ≥8, role) → change role → remove.
- **Expected:** Add creates-or-reuses the user, sets `must_reset_password`, adds membership; **last-admin guard** rejects a downgrade/removal that would leave the org with no admin.
- **Code:** `handlers_settings.go:26,51,125,164` · **Automation:** Partial.

### Case 2 — Role capabilities (viewer / editor / admin)
- **Actor:** one user per role
- **Steps:** As each role, attempt: read a service; mutate a resource (create integration); manage members/tokens/SSO.
- **Expected:** viewer = read only; editor = read + mutate resources; admin = + manage members/tokens/SSO/settings. Forbidden actions return 403; admin-only pages redirect non-admins (e.g. Settings → /health).
- **Code:** `identity/types.go:34` (roles), `middleware/require.go` · **Automation:** Partial.

## API tokens

### Case 3 — Personal access token lifecycle
- **Endpoints:** `GET /api/v1/settings/tokens`, `POST`, `DELETE …/{id}`
- **Steps:** Create a PAT (name) → copy the plaintext (shown **once**) → use it as `Authorization: Bearer con_…` → revoke.
- **Expected:** Only prefix+hash stored; the token authenticates API calls until revoked; a user sees only their own tokens; revoke checks ownership. Human users only (not service accounts).
- **Code:** `handlers_settings.go:223,241,284` · **Automation:** yes (capture token from create response).

## Groups & policies

### Case 4 — Groups & service visibility policies
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/settings/groups[/{id}]`, `…/members`, `…/policies`; per-service `GET/PUT /api/v1/services/{name}/groups`
- **Steps:** Create a group → add members → attach a service policy (basic RBAC `kind=service`; advanced ABAC attribute policies are EE) → confirm a non-admin in the group sees only the allowed services.
- **Expected:** A user's visible services = admin-all ∪ group memberships ∪ matching attribute policies. Lists/feeds everywhere are filtered to this set.
- **Code:** `handlers.go:243` (`resolveServiceFilter`), `:870` (group routes) · **Automation:** Partial.

## Orgs

### Case 5 — Org get / update / delete
- **Endpoints:** `GET/PATCH/DELETE /api/v1/orgs/{id}`
- **Steps:** View org; rename; (carefully) delete.
- **Expected:** Update reflects in the org switcher; delete removes the org and its scoped data. Multi-org users can switch org and see each org's resources separately.
- **Code:** `handlers_org.go` · **Automation:** Manual (destructive).

## Ingest keys

### Case 6 — Ingest key lifecycle
- **Endpoints:** `GET/POST/DELETE /api/v1/ingest-keys[/{id}]`
- **Steps:** Create an ingest key for the org → copy it → send OTLP with `INGEST_ALLOW_ANONYMOUS=false` using the key → revoke → confirm telemetry is now rejected.
- **Expected:** Valid key → accepted and attributed to the org; revoked/absent key → rejected (with anonymous disabled). See [telemetry-ingest.md](telemetry-ingest.md) Cases 5–6.
- **Code:** `handlers_ingest_keys.go` · **Automation:** Partial.

## Tenant isolation (critical)

### Case 7 — Org A cannot see org B's data
- **Actor:** admin of org A and admin of org B
- **Steps:** Seed telemetry + create resources (integrations, services, tags, alerts) under each org. As A, browse every surface (services, integrations, logs, metrics, search, alerts, messages).
- **Expected:** A sees **only** A's data — never B's. Postgres enforces hard per-org isolation; ClickHouse is logically isolated by org filter (unit-tested in [orgfilter_test.go](../../../pkg/clickhouse/orgfilter_test.go)). No cross-org leak via search or any list.
- **Code:** `pkg/clickhouse/orgfilter.go`, `middleware` org scoping · **Automation:** Partial — **must be walked every release** even where automated.

## Notes
- See memory `multi-tenancy-and-ingest-auth` for the isolation model and the cell-api single-replica constraint.
