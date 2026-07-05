# RBAC v2 — scoped capabilities, sharing, per-signal visibility (design)

Status: **ALL PHASES (1–4) SHIPPED** (2026-07-04) · Owner: Robert

This spec extends the RBAC model from "policies grant *visibility*" to
"groups grant *capability within a scope*", defines the CE/EE boundary,
and adds resource sharing and per-signal visibility. **Integrations and
systems are peers throughout** — every rule that applies to one applies
to the other.

## 1. Goals

- **CE**: an admin can attach a group to an integration **or system** as
  *viewer*; members then see it and its underlying services. View only.
  This is the only visibility-granting mechanism in CE, and it removes
  today's "non-admins see nothing and nothing can be done about it" trap.
- **EE**: full scope language (attributes / compound / **expression**
  policies) *plus* scoped **manage**: group-editors can edit services and
  create/manage integrations & systems **within their scope**, without
  being org-wide editors, and without being able to change the policy
  that defines their scope.
- **EE**: share a single integration **or system** with a user or group —
  viewer rights only.
- **EE (later phase)**: per-signal visibility — grant traces / logs /
  metrics independently.

Non-goals: deny rules / precedence (still a pure union of allows);
cross-org anything; per-user roles beyond sharing.

## 2. Core model: scope × capability

- **Policy = scope.** A policy on a group answers *which services*. The
  scope language is the existing seven kinds; `expression` (arbitrary
  AND/OR/NOT over service names + resource attributes) is the general
  case, already shipped and tested.
- **Group role = capability.** `group_members.role` answers *what you may
  do inside the group's scope*:
  - `viewer` → see the scoped services (+ integrations/systems they
    imply, transitively — current behavior).
  - `editor` → additionally **manage** within scope (§5).
  - `admin` (group) → reserved; behaves as `editor` for now (see §11 Q3).
- **Org role stays the outer ceiling.** Org `admin` = everything.
  Org `editor` = org-wide manage (unchanged). Org `viewer` = nothing
  beyond what groups/shares grant. Scope-capped tokens keep their hard
  ceiling everywhere; a capped token never gains from group roles
  (current `RequireWriteAnywhere` invariant, preserved).

Resolution therefore produces **two service sets** per user per org:

```
Visible = ∪ scopes of ALL my groups  ∪  shared-resource expansions
Managed = ∪ scopes of groups where my role ≥ editor        (⊆ Visible)
```

`manage` implies `view`. Shares (§6) contribute to Visible only, never
Managed. All existing read filtering keys off Visible exactly as today.

## 3. Edition boundary

| Capability | CE | EE (`rbac_advanced`) |
|---|---|---|
| Org roles, operator flag, groups + members | ✅ | ✅ |
| Attach group as **viewer** to an integration / system | ✅ (new) | ✅ |
| Policy kinds beyond those two (service / attributes / compound / all_org / expression / system-by-kind) | ❌ | ✅ |
| Scoped **manage** (group-editor powers) | ❌ — group role >viewer has no effect in CE | ✅ |
| Sharing with user/group | ❌ | ✅ |
| Per-signal visibility | ❌ | ✅ (phase 4) |

Enforcement (deny-by-default, visibility filtering, all gates) is
identical in both editions — the entitlement only gates *configuration*
surfaces, never enforcement. This keeps CE→EE upgrades pure additions.

## 4. Phase 1 — CE: attach groups to integrations & systems

**Schema.** `group_access_policies` gains `target_system_id UUID
REFERENCES systems(id) ON DELETE CASCADE`. The `system` kind becomes:
exactly one of { nothing (all systems), `target_system_kind`,
`target_system_id` }. Existing rows unchanged. (The `integration` kind
already targets an instance by UUID — this gives systems parity.)

**API** (admin-only; NOT `rbac_advanced`-gated — this is the CE surface):

```
GET    /api/v1/integrations/{id}/groups        → [{group, role:'viewer'}]
PUT    /api/v1/integrations/{id}/groups        {group_ids: [...]}   (replace set)
GET    /api/v1/systems/{id}/groups
PUT    /api/v1/systems/{id}/groups             {group_ids: [...]}
```

Implementation: each entry is a `kind=integration` / `kind=system
(target_system_id)` policy on that group. These endpoints are a
restricted façade over policy CRUD; the generic policy endpoints stay
EE-gated. The façade refuses to touch policies of other kinds (so an EE
org's expression policies can't be clobbered from the simple UI).

**UI.** A "Groups" card on IntegrationSettings and SystemDetail
(admin-only): multi-select of groups. Settings → Groups continues to
show all policies read-only in CE with an EE upsell for the rich kinds.

**Semantics.** Members of an attached group see: the integration/system,
its member services (transitive, current behavior), and those services'
telemetry. View only — in CE, group role is forced/ignored to viewer.

## 5. Phase 2 — EE: scoped manage

### 5.1 Resolution

`ResolveEffectiveAccess` returns Visible and Managed (per §2). Cached
per request as today. `HasNoAccess` unchanged (keys off Visible).

### 5.2 Write-gate rework

Two write classes replace today's single `RequireWriteAnywhere`:

**A. Org-global config** — resources with no service scope: tags,
schemas, maps, metadata-field definitions, system types, monitoring
templates, notification channels/profiles, alert email template,
message views.
→ Gate: **org editor/admin only.** Group-editors lose these.
*(Behavior change, deliberate: today any group-editor can create
org-wide config. This tightening is the point of scoped manage. Called
out in migration notes §9.)*

**A′. Dashboards — group-scoped (decision 2026-07-04).** Dashboards
gain an optional `group_id`: NULL = org-wide (class A rules apply);
set = the dashboard belongs to that group — visible to its members,
manageable by its group-editors (and org editors/admins). This makes
dashboards the first resource that is *owned* by a group rather than
merely visible through one.

**B. Scoped resources** — anything that resolves to services:

| Operation | Rule |
|---|---|
| Edit service metadata / settings / facets / schemas-binding / clear-errors / badge / apply-template | service ∈ Managed (org editor+ bypasses, as org-wide Managed=all) |
| **System** create | manage-capable (org editor+ OR group-editor anywhere); starts empty — always allowed |
| System attach service | service ∈ Managed |
| System detach / update / delete / metadata / apply-template-all | **all current member services ∈ Managed** |
| **Integration** create/update (incl. matchers) | **containment**: every service the matchers *currently resolve to* ∈ Managed |
| Integration delete / matcher remove / profile assign / completion rules | all currently-matched services ∈ Managed |
| Alert rule create/update/delete | rule's service ∈ Managed; rules with no service scope → class A |
| Ack/resolve alert instance, mark firing handled | underlying service ∈ Managed |

The strict "**all** members ∈ Managed" rule (not "any") is chosen so a
partially-in-scope resource can't be modified by someone who can't see
its full blast radius. A group whose scope covers half a system can
view it; only someone whose Managed set covers all of it can change it.

### 5.3 The matcher containment caveat (say it out loud)

Integrations are defined by *patterns* (prefix/contains/regex). A
matcher that is in-scope today can match a **future** service outside
the writer's scope. Rules:

1. At create/update time: resolved services ⊆ Managed, else 403 naming
   the offending services.
2. The integration remains owned by the org, not the group — if it later
   matches out-of-scope services, the group-editor **loses** manage over
   it (rule "all members ∈ Managed" now fails) until an org admin
   intervenes. Fail toward less power, never more.
3. UI warns when a group-editor saves a broad matcher ("this pattern may
   match future services outside your scope; you may lose edit access").

Systems have no such caveat — membership is explicit, checked per attach.

### 5.4 What group-editors explicitly cannot do

Change any policy (org admin only, unchanged) · touch class-A org config ·
members/tokens/SSO/settings (org admin, unchanged) · anything via a
scope-capped token beyond the cap.

## 6. Phase 3 — EE: sharing

```
CREATE TABLE resource_shares (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL,
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('integration','system')),
    resource_id   UUID NOT NULL,
    grantee_kind  TEXT NOT NULL CHECK (grantee_kind IN ('user','group')),
    grantee_id    UUID NOT NULL,
    created_by    UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, resource_kind, resource_id, grantee_kind, grantee_id)
);
```

- **Viewer-only by design** — shares expand (integration → matched
  services; system → member services) into **Visible**, never Managed.
  No role column, so it can't creep.
- **Who may share/revoke:** org admin, or anyone whose Managed set covers
  the resource (you can share what you can manage). Grantee user must be
  an org member.
- API: `GET/POST/DELETE /api/v1/{integrations|systems}/{id}/shares`;
  UI: "Share" action on both detail pages. EE-gated.
- Deleting the resource / user / group cascades the share.
- **Notifications (in scope, decision 2026-07-04):** creating a share
  notifies the grantee — a digest entry ("X shared integration Y with
  you") for the user (or every member, for a group grantee), plus email
  when SMTP is configured. Revocations are silent.

## 7. Phase 4 — EE: per-signal visibility

`group_access_policies.signals TEXT[]` (NULL = all signals; else subset
of `{traces, logs, metrics, messages}`). Resolution produces Visible per
signal; **Managed requires full-signal scope** (a policy narrowed by
signal never contributes to Managed — managing a service you can only
partially observe is incoherent).

Enforcement: every telemetry read path declares its signal and filters
by that signal's set. Mixed surfaces (service detail, dashboards,
topology, digest, global search) use the **union** for existence/nav and
per-signal sets for each widget's data.

This phase multiplies enforcement touchpoints (~15–20 handlers) and is
deliberately **last**, landing only after phase 2's gates are stable.
Shares are all-signal.

## 8. Audit events (all phases)

Every new mutation is audited (hash-chained as usual):
`integration_groups.updated`, `system_groups.updated`, `share.created`,
`share.revoked` (metadata: resource kind/id, grantee), and phase-2 403s
on containment violations are **not** audited (noise) but are logged.

## 9. Migration & compatibility

- Phase 1/3 schema changes are additive (new column, new table).
- Existing policies keep exact semantics. Existing group *viewers*: no
  change. Existing group *editors*: gain scoped manage (new powers
  within scope), **lose** class-A org-global writes (tightening). Release
  notes must state this; ops can restore old behavior by making those
  users org-editors.
- CE cells upgrading: nothing to do; attach-groups is new surface.
- EE→CE downgrade: rich policies stop being *editable* but keep
  *enforcing* (consistent with how entitlements gate configuration, not
  enforcement).

## 10. Test matrix (acceptance)

Per phase, at three layers (mirroring the expression-policy suites):

1. **Unit**: resolution (Visible/Managed per role combos), containment
   predicates, share expansion, signal-set math.
2. **Integration (real PG)**: the §5.2 table row-by-row as a
   role × operation matrix; the matcher-containment temporal case (add
   out-of-scope service → manage lost); share grants view not manage;
   cross-org isolation for every new source; CE façade cannot touch
   rich policies.
3. **e2e**: CE story (attach group → member sees integration+services,
   cannot edit); EE story (group-editor edits in-scope service, creates
   in-scope integration, 403 on out-of-scope matcher, cannot create
   tags); share story (user sees exactly the shared system, viewer-only).

## 11. Resolved decisions (Robert, 2026-07-04)

1. **Group `admin` role** — behaves as `editor` for now; delegated
   membership management is a possible later extension.
2. **Dashboards** — become **group-scoped** (see §5.2 A′).
3. **CE group role display** — role selector shown but disabled in CE,
   with an EE upsell.
4. **Share notifications** — **in scope** (see §6): digest entry +
   email when SMTP is configured.

## 12. Build order

Phase 1 (CE attach, small) → Phase 2 (scoped manage — the core; largest;
own PR series: resolution → gates → containment → UI) → Phase 3 (shares)
→ Phase 4 (signals). Each phase lands with its full test tier before the
next starts.
