# Maintenance windows & announcements (design)

Status: **v1 implemented** (2026-07-07). The five open questions at the
bottom were decided as proposed. One deviation from the draft: the
windows list endpoint returns all of the org's windows to every member
(viewer+) rather than scope-filtering — windows are operational
communication like announcements, and hiding them would just make
"why is this alert silent?" harder to answer. Revisit if scope contents
ever become sensitive.

Two features with one seam, motivated by the demo-cell outage (2026-07-07):
when an operator knows something is wrong or planned, there is today no way
to (a) tell the users, or (b) stop the alert engine from paging everyone
about it.

- **Announcements** — human-facing: a persistent, dismissible message every
  user in the org (or on the cell) sees until it expires.
- **Maintenance windows** — machine-facing: a bounded time range in which
  alert *delivery* is suppressed for a chosen scope.

They compose (a window can publish its own announcement) but are
independent primitives: you can announce without silencing ("degraded
performance, we're watching") and silence without announcing (routine
nightly job).

---

## 1. Announcements

### Model

```
announcements
  id            uuid PK
  org_id        uuid NULL      -- NULL = cell-wide (operator-created)
  message       text           -- plain text, ≤500 chars; no markdown in v1
  severity      text           -- info | warning | critical (reuses alert palette)
  starts_at     timestamptz    -- default now()
  ends_at       timestamptz NULL  -- NULL = until deleted
  dismissible   boolean        -- critical maintenance can be forced sticky
  created_by    uuid           -- users.id
  created_at    timestamptz

announcement_dismissals
  announcement_id uuid FK
  user_id         uuid FK
  dismissed_at    timestamptz
  PK (announcement_id, user_id)
```

Dismissal is **server-side per user** (multi-device, survives cache
clears), not localStorage.

### RBAC

- **Read**: every authenticated principal in the org (+ cell-wide rows).
  Deliberately *not* filtered by group visibility policies — announcements
  are organizational communication, not entity data; the whole point is
  broadcast. This is consistent with RBAC v2's framing: policies scope
  *services*, not org-level comms.
- **Write**: org announcements need `org.manage` (org admins). Cell-wide
  announcements are operator-only, managed from the Operator page.
- **Demo accounts** (`is_demo`): read + dismiss yes; create/delete no
  (falls under org administration, already blocked by the demo guard).
- **Service accounts**: no announcements in API responses they consume
  (they have no UI); endpoint simply isn't relevant — no special casing.
- Writes are **audited** (`announcement.created` / `.deleted`).

### API

```
GET    /api/v1/announcements                     any authed user; active,
                                                 not-yet-dismissed rows
POST   /api/v1/announcements/{id}/dismiss        any authed user
POST   /api/v1/settings/announcements            admin
DELETE /api/v1/settings/announcements/{id}       admin
POST   /api/v1/operator/announcements            operator (org_id = NULL)
DELETE /api/v1/operator/announcements/{id}       operator
```

### UI

A banner stack in the AppShell banner slot (where the MFA-enrollment and
integration-limit banners already render), styled by severity, `×` when
dismissible. Admin CRUD on Settings → Organization; operator CRUD on the
Operator page. Not shown pre-login in v1 (keeps the surface authenticated;
a `show_on_login` flag for cell-wide rows is a possible v2).

---

## 2. Maintenance windows

### Semantics — suppress delivery, keep evaluating

During an active window that covers a rule, the engine **still evaluates
and still records** alert instances; what it skips is **notification
dispatch** (email + webhook jobs) and the digest-bell ping. Instances
created or resolved inside a window carry `suppressed_by = window_id`.

Why not pause evaluation: you'd lose incident history, and on window end
you'd have no idea what state anything is in. With suppress-at-dispatch,
anything *still firing* when the window ends notifies naturally on the next
evaluation/renotify cycle — no resurrection logic.

UI honesty rule: suppressed ≠ healthy. Entities in scope show a
"maintenance" badge; alert-history rows show a "suppressed" tag with a link
to the window. The bell count excludes suppressed instances (bell semantics
otherwise unchanged, per the existing notification-model decisions).

### Scope — reuse what exists, don't invent a matcher language

```
maintenance_windows
  id          uuid PK
  org_id      uuid FK
  name        text            -- "July release", shown on badges
  reason      text            -- optional detail
  starts_at   timestamptz     -- may be in the future (scheduled)
  ends_at     timestamptz NOT NULL   -- bounded BY DESIGN, max 7 days
  scope       jsonb           -- see below
  announce    boolean         -- auto-create a linked announcement
  created_by  uuid
  created_at  timestamptz
```

Scope is one of (v1):

| kind | meaning | mechanism |
| --- | --- | --- |
| `all_org` | everything in the org | trivial |
| `entities` | explicit lists: `integration_ids`, `system_ids`, `service_names` | matches `AlertRule.IntegrationID` / `ServiceName` (and rules bound to services of listed systems) |
| `group` | **a team's alerts** | matches `AlertRule.GroupID` — rule ownership already exists |

That last row is the "per team" answer: alert rules already carry an owning
team (`group_id`, nil = org-wide), so *"my team's maintenance mode"* =
suppress rules owned by group X. No new ownership concept. Note what it
deliberately does **not** mean: it does not silence org-wide rules that
happen to cover the team's services — those belong to the org. A team that
wants broader silence during their deploy scopes by `entities` instead.

Windows are **hard-bounded** (`ends_at` required, ≤7 days out): forgotten
silences are the classic self-inflicted outage. "Extend" is a fresh audited
write.

Deferred to v2 (EE, `rbac_advanced`): policy-style matcher scopes ("same
scope as policy P" / attribute matchers), and recurring windows (cron
shape). The jsonb scope column accommodates both without migration.

### Engine touchpoint

The engine already ticks (`evaluateOnce`). Each tick refreshes a tiny
in-memory list of the org's active windows (they number in the units, not
thousands). In `enqueue` / `enqueueFiring` (and the resolve-notification
path), a `suppressedBy(rule) *uuid` check runs before job creation:
integration/service/group match against the active windows. Suppressed →
set `suppressed_by` on the instance, skip the notification job, done. The
delivery layer (`delivery.go`) is untouched — suppression happens before
jobs exist, so there is nothing half-sent to reconcile.

### RBAC for window CRUD

- **CE**: editors and admins create/edit/delete windows for `entities` and
  `group` scopes; `all_org` windows require admin. (Editors can already
  edit the alert rules themselves — being able to silence them is not an
  escalation.)
- **EE** (`rbac_advanced`): group-editors may create windows whose scope
  resolves to a subset of their **managed** set (the existing
  `managedServiceFilter` machinery); same fail-toward-less-power rules.
- **Viewers**: see maintenance badges and the windows covering entities
  they can see; no CRUD.
- **Demo accounts**: may create/delete windows (product config, same class
  as alert rules; the reseed wipes them).
- **Service accounts**: org role decides, same as alert-rule endpoints
  (viewer SA: read-only; editor SA: CRUD). No group scope applies (SAs
  never gain from group roles).
- All writes **audited** (`maintenance_window.created` / `.updated` /
  `.deleted`), with the scope snapshot in the audit detail.

### API

```
GET    /api/v1/maintenance-windows           list (viewer+; scope-filtered for non-admins)
POST   /api/v1/maintenance-windows           create (editor+; all_org → admin)
PATCH  /api/v1/maintenance-windows/{id}      edit/extend (creator, group-editor in scope, admin)
DELETE /api/v1/maintenance-windows/{id}      end early (same as edit)
```

### UI

- Alerts page: "Schedule maintenance" button + an "Active maintenance"
  strip when any window is live.
- Integration/system/service detail: maintenance badge while covered.
- `announce = true` creates a linked announcement ("Maintenance: <name>
  until <ends_at>") that auto-deletes when the window ends or is ended
  early.

---

## 3. CE / EE split

| Capability | Edition |
| --- | --- |
| Announcements (org + cell-wide) | CE |
| Windows: `all_org`, `entities`, `group` scopes | CE |
| Windows: policy-matcher scopes, recurring schedules | EE (`rbac_advanced`) |
| Scoped-manage window creation by group-editors | EE (`rbac_advanced`) |

Suppression itself stays CE deliberately: it's an operational-safety
feature, and gating safety feels hostile. EE gets the *scope language*,
consistent with where the rest of RBAC v2 draws the line.

---

## 4. Open questions (decide before building)

1. **Bell behavior**: exclude suppressed instances from the bell count
   entirely (proposed), or show them in a muted "in maintenance" section?
2. **`announce` default**: on or off for new windows? (Proposed: on for
   `all_org`, off otherwise.)
3. **Resolve-during-window emails**: an alert that fires *before* the
   window and resolves *inside* it — send the resolve notification
   (proposed: yes, resolves are good news and close the loop) or suppress
   symmetrically?
4. **Max window length**: 7 days proposed — right bound?
5. **CE editor rights**: comfortable with editors silencing `entities`
   scopes they can't manage in the EE sense? (CE has no manage scoping, so
   the alternative is admin-only windows in CE.)
