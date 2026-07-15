<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Service-account scoping (design)

Status: **accepted 2026-07-15, implemented** (see "Decisions" at the
bottom). Companion to issue #2.

Today a service-account (SA) token's **role gates writes**, but its **reads are
org-wide**: SA principals carry no user id, and every visibility predicate
treats nil-user principals as internal → allow. A viewer SA reads all services
while an identical viewer *user* without groups reads none. Since the MCP
endpoint inherits REST auth, any SA token handed to an AI tool reads the whole
org. Pinned by rbac.spec (`service-account token`, `MCP surface`) pending this
design.

## Principles this design follows

1. **RBAC v2's own model** (docs/rbac-v2-design.md): policy = scope, role =
   capability, deny-by-default visibility, union of allows, Managed ⊆ Visible.
   The correct fix makes SAs obey the *same* model — not a parallel one.
2. **Least privilege for machine identities.** Machine tokens deserve
   *tighter* defaults than humans, not looser: they get committed to repos,
   pasted into CI, and handed to AI agents that do unexpected things.
3. **One enforcement path.** A second, SA-specific authorization mechanism
   would drift from the group/policy engine and re-create this bug eventually.
   SAs must flow through the existing resolver.
4. **MCP inheritance stays trivial.** MCP forwards the caller's token to REST;
   the token itself must express scope. No MCP-side filtering, ever — the
   REST↔MCP parity test stays the contract.

## The design: service accounts are group members

**Service accounts become first-class principals in the existing group
machinery.** An SA can be a member of groups exactly like a user; the group's
attachments/policies define its *scope*, its role in the group its
*capability*. Everything users already have — CE attach, EE expression
policies, **per-signal grants**, shares — applies to SAs with zero new
authorization concepts.

Per-signal is the quiet payoff: "give the assistant logs + metrics for
team-A's services, but no message payloads" is exactly the analyst-grant
machinery, and it is precisely the shape AI-agent tokens need.

### Scope modes, and the migration that keeps automations alive

A new per-SA field: `scope ∈ { org-wide | scoped }`.

- **`scoped` (default for NEW service accounts):** deny-by-default. A
  group-less scoped SA sees nothing; visibility comes from group membership,
  resolved by the same engine as users.
- **`org-wide`:** today's behaviour — but *explicit, chosen, and loud*. Shown
  as an amber "org-wide read" badge in the SA list and blade; creating or
  switching to it is admin-only and audited (`service_account.scope_changed`).
  Legitimate for single-team installs and trusted platform automation; the
  point of least privilege is defaults and visibility, not prohibition.
- **Backfill:** existing SAs are migrated as `org-wide` — no automation breaks
  on upgrade; the badge makes the standing grant visible so admins can narrow
  deliberately. Release notes call this out.
- Optional later (not v1): a cell setting forbidding org-wide SAs for
  compliance-posture installs.

Admin-*role* SAs remain org-wide by definition (admin is admin); UI copy
discourages them for integrations/agents.

### Implementation sketch

- **Principal**: middleware already resolves SA tokens; `identity.Principal`
  gains `ServiceAccountID *uuid.UUID` + the scope mode. The root cause gets
  named: predicates must distinguish *internal caller* (loopback/engine, no
  principal at all) from *SA principal* — the current conflation of both into
  `UserID == nil` is the bug. Every `UserID == nil` site gets audited
  (canSeeService, canSeeIntegration, resolveServiceFilter[Signal], gates).
- **Membership**: `group_members` gains a nullable `service_account_id` with a
  CHECK that exactly one of (user_id, service_account_id) is set. The resolver
  joins on either. (Alternative — separate table + UNION — rejected: two code
  paths again.)
- **Resolution**: `ResolveVisibleServiceSet` keyed by principal (user id or SA
  id); memoization keyed the same way. Scoped SA → resolve; org-wide SA →
  allow-read; internal → allow.
- **UI**: Service-accounts tab gets a Scope column + badge; the SA blade
  (members-blade pattern) gets a group-membership editor and the scope
  switch with blast-radius copy. Groups blade lists SA members with a
  machine marker.
- **Docs/MCP guidance**: docs/mcp.md + the Developers page recommend one
  *scoped* SA per assistant/automation, per-signal where possible.
- **Tests**: flip the two pinned rbac.spec assertions (scoped default deny;
  org-wide badge path), add: SA-in-group sees exactly the scope; per-signal
  SA grant verified through MCP; scope switch audited.
- **Out of scope v1**: SAs in *shares* (user-kind shares stay user-only);
  SA export in config transfer (tokens never export; SA definitions later).

### Why not the alternatives

- **Document org-wide as designed**: cements a machine-token model looser
  than the human model — backwards under least privilege, and indefensible
  the moment an MCP token leaks.
- **Per-SA service allowlist**: a second authorization language that can't
  express per-signal or expressions, and drifts from the group engine. The
  group model already exists, is tested, and is what admins know.

## Decisions (Robert, 2026-07-15)

1. **Backfill:** moot — no installation has any service accounts, so there
   is nothing to migrate. Every SA starts `scoped`; `org_wide` exists only
   as the explicit opt-in. (The migration still defaults the column to
   `scoped` so any row predating an upgrade would land on the safe side.)
2. **Forbid-org-wide cell setting ships in v1**
   (`rbac.forbid_org_wide_service_accounts`, on the System settings tab):
   rejects creating/switching to `org_wide`, and existing org-wide SAs
   resolve as scoped while enabled — defense in depth.
3. **Group membership editable from both sides:** the SA row's Access
   panel (scope switch + memberships) AND the group blade's Add-member
   picker, where service accounts appear in their own labelled optgroup
   with a machine marker so they can't be mistaken for people.

## Implementation notes (v1, shipped with this doc)

- Migration 0069: `service_accounts.scope`; `group_members` polymorphic
  (nullable `user_id` + `service_account_id`, CHECK exactly-one-of, two
  UNIQUE constraints replacing the PK so SSO's upsert keeps working).
- Resolution: `identity.MemberRef` + `ResolveVisibleServiceSetMember` /
  `ResolveAccessSetsMember` / `ListPoliciesForMember` — one resolver for
  both principal kinds; user-keyed names remain as wrappers. Shares stay
  user-only. The handler chokepoint is `visibilityMember`
  (handlers.go), which also names the internal-caller case explicitly.
- Scoped SAs with a write role manage at most what they can see
  (Managed ⊆ Visible); org-wide SAs keep the old role-gated behaviour.
- SA group role is accepted as viewer/editor in the UI; the SA's own
  role remains the write-capability ceiling enforced by the mux gates.
