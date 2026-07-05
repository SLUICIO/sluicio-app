<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Cell operator (super-admin)

| Field | Value |
|-------|-------|
| **Area** | Cell-operator surface: org lifecycle, cross-org member assignment, operator management, cell-wide settings gating |
| **Automation status** | Partial (gating + isolation covered by integration tests; the UI flows manual) |
| **Automated by** | `api/middleware/require_integration_test.go` (gates), `api/tenant_isolation_integration_test.go` (org_id isolation), `identity/groups_integration_test.go` (roles + teams) |
| **Last reviewed** | 2026-07-02 |

## Preconditions

- Stack up (`make dev-up`). Fresh cell: the seeded admin (`admin@sluicio.local`)
  is auto-promoted to **operator** on first boot.
- The operator surface lives at **`/operator`** (nav link visible only to
  operators). All endpoints are under `/api/v1/operator/*` and gated by
  `RequireOperator`.
- Multi-org cases need a **second org** — create it via Case 2 (this is
  now the supported way to make one; there is no self-service org
  creation).

## Operator gating

### Case 1 — Only operators reach the operator surface
- **Actor:** one operator, one non-operator org-admin.
- **Steps:** As the non-operator: confirm no **Operator** nav link; hit
  `GET /api/v1/operator/orgs` directly; force-navigate to `/operator`.
- **Expected:** nav link hidden; API returns **403** `operator access required`;
  the route redirects to `/health`. A scope-capped token (e.g. a viewer PAT)
  is **also** refused even if its owner is an operator.
- **Code:** `middleware/require.go` (`RequireOperator`), `App.tsx` (`RequireOperator`),
  `AppShell.tsx` (`operatorOnly`) · **Automation:** Yes — `require_integration_test.go`
  (`operator/*`, `unauth/*`).

## Organizations

### Case 2 — Org lifecycle (create / rename / delete)
- **Endpoints:** `GET/POST /api/v1/operator/orgs`, `PATCH/DELETE …/{id}`
- **Steps:** Create an org (name + slug) → rename it → create a second so
  there are ≥2 → delete one.
- **Expected:** create rejects a duplicate slug (409) and a bad slug (400);
  list shows member counts; **last-org guard** rejects deleting the only org
  on the cell (400); delete cascades members/groups/policies/tokens.
- **Code:** `handlers_operator.go`, `identity/operator.go` (`CreateOrg`, `ListOrgs`, `DeleteOrg`) · **Automation:** Manual (destructive); `CreateOrg` exercised by the gate/isolation tests.

### Case 3 — Assign a member to any org
- **Endpoints:** `GET/POST /api/v1/operator/orgs/{id}/members`, `PATCH/DELETE …/{user_id}`
- **Steps:** Add a member to org B by email (role; password optional) → change
  their role → remove. Add the **same** email to a second org.
- **Expected:** a new email creates the user; an existing email is **reused**,
  so one person can belong to multiple orgs; omitting the password creates an
  SSO-only user; role must be admin/editor/viewer.
- **Code:** `handlers_operator.go` (`addOperatorOrgMember`) · **Automation:** Manual.

## Operators

### Case 4 — Promote / demote operators (last-operator guard)
- **Endpoints:** `GET /api/v1/operator/users`, `PUT …/{user_id}/operator`
- **Steps:** Promote a second user to operator → demote them → try to demote
  the **last** remaining operator.
- **Expected:** promotion is immediate (their next `/me` shows `is_operator`
  and the Operator nav appears); demoting the last operator is refused (400
  `can't demote the last cell operator`) so the cell never loses its operator.
- **Code:** `handlers_operator.go` (`setOperatorFlag`), `identity/operator.go` (`CountOperators`) · **Automation:** Manual.

### Case 5 — Bootstrap operator on a fresh cell
- **Steps:** Migrate a fresh DB and boot cell-api. Inspect `users.is_operator`.
- **Expected:** exactly the seeded admin is an operator; on a cell that
  already has an operator, boot does **not** re-promote (a prior demotion
  sticks). Idempotent.
- **Code:** `cmd/cell-api/main.go` (`EnsureBootstrapOperator`) · **Automation:** Manual.

## Cell-wide settings

### Case 6 — SMTP / retention / security / license are operator-only
- **Endpoints:** `PATCH /api/v1/cell-settings/{retention,system,smtp,security}`
- **Steps:** As a non-operator org-admin, confirm the Retention / System /
  License tabs are **hidden** in Settings; attempt each `PATCH` directly. As
  an operator, change them from `/operator` → Cell-wide settings.
- **Expected:** non-operator PATCH → **403**; the tabs don't render for
  non-operators; operator edits succeed. Single-org self-hosted is unaffected
  (admin = operator). SMTP password stays encrypted at rest and masked on read.
- **Code:** `handlers.go` (cell-settings routes → `RequireOperator`), `Settings.tsx` (`OPERATOR_TABS`) · **Automation:** Manual.

## Tenant isolation

### Case 7 — A second org is fully isolated
- **Steps:** With two orgs (Case 2) and data in each, confirm one org never
  sees the other's integrations / systems / services on any surface, and
  cross-org get/mutate returns not-found.
- **Expected:** list excludes the other org; get-by-id / mutate of another
  org's resource → 404 / not-found; the `X-Sluicio-Org` header only switches
  among a multi-org user's own memberships.
- **Code:** `pkg/clickhouse/orgfilter.go`, store `WHERE org_id` scoping ·
  **Automation:** Yes — `api/tenant_isolation_integration_test.go`; **still
  walk every release** (see [orgs-access-tenancy.md](orgs-access-tenancy.md) Case 7).

## Notes

- The operator flag is on `users` (org-independent), not `org_members` —
  an operator operates the cell, not a particular org.
- There is no cross-cell control plane here; this is single-cell,
  multi-org. Cross-cell/SaaS provisioning is a separate concern.
