# Systems as first-class entities

Status: **design agreed, not yet implemented** (2026-06-25).

This document describes promoting "systems" from a service flag to first-class
entities, and turning "system type" into a managed catalog that owns a type's
detection, starter health checks, and metadata schema.

## Why

Today a "system" is just a **service with a free-text `system_kind`** set. That
has three limits:

1. A real system (a RabbitMQ cluster, a Kafka estate) is usually emitted by
   **several** services/collectors — there's no object that represents the
   cluster itself.
2. **System types** are defined in code (`monitoringTemplates` keyed by `Kind`
   in `system_templates.go`, `SYSTEM_KINDS` in the frontend) plus a free-text
   override. They aren't maintainable by users and aren't the single source of
   truth.
3. **Metadata** can be scoped to integrations and services
   (`metadata_fields.applies_to_integration / applies_to_service`) but not to
   systems — so you can't say "every RabbitMQ system has a `vhost` and a
   `cluster size`."

The agreed direction unifies these: a **system type** becomes a managed catalog
entry that owns *detection rules*, *starter checks*, and a *metadata schema*;
a **system** becomes an instance of a type that **spans member services** and
carries its own metadata, health, and RBAC.

## Decisions (locked)

- **Systems are first-class entities**, not a service flag.
- **System type is a managed catalog** that owns: detection prefixes, starter
  checks (its monitoring template), and its metadata schema.
- **Membership is one-to-many**: a service belongs to **at most one** system
  (`services.system_id`); a system spans many services. (Promote to a join
  table only if a service genuinely needs to belong to multiple systems.)
- **Catalog scope**: **global built-in types** (read-only seeds: RabbitMQ,
  Kafka, ActiveMQ/Artemis, Redis, SQL Server, Postgres, MySQL, MongoDB,
  Elasticsearch, OTel Collector, …) **plus per-org custom types and overrides.**
- **Custom monitoring templates** (the `monitoring_templates` table from the
  user-defined-templates feature) stay as reusable *ad-hoc* check bundles.
  `system_types` own the *per-type* starter checks. They are not force-merged.

## Data model

| Table | Purpose |
| --- | --- |
| `system_types` | The managed catalog. `id`, `organization_id` (NULL = global built-in), `key`, `label`, `detect_prefixes text[]`, `built_in bool`. Owns the type's starter checks + metadata schema. |
| `system_type_checks` | A type's starter checks (its monitoring template), mirroring the existing `systemCheck` shape (metric/log/trace spec). Seeded from today's code catalog. |
| `systems` | A system instance: `id`, `organization_id`, `name`, `system_type_id`. Its own page, metadata, health, RBAC target. |
| `services.system_id` | Membership FK (nullable). One system → many services. |
| `system_metadata` | Field values per system: `system_id`, `field_id`, `value`. |
| `metadata_fields` (extended) | add `applies_to_system bool` and optional `system_type_id` (NULL = all systems; set = only that type). Relax the existing CHECK to allow a system-only field. |

Notes:
- Built-in `system_types` have `organization_id = NULL` and `built_in = true`
  (read-only); orgs add their own rows or override a built-in by `key`.
- `system_type_checks` reuse the metric/log/trace rule specs already used by
  `AlertRule`, so applying a type's checks is the existing
  `createTemplateChecks` path with the catalog as the source.

## Migration from today

- For each distinct `(org, service.system_kind)` currently in use, create a
  `systems` row of the matching type (creating an org `system_types` row if the
  kind isn't a built-in) and set `services.system_id` on the flagged services.
- Keep `services.system_kind` during the transition (derive it from
  `system_id → system_type.key` afterwards), then drop it once the UI is moved
  over.
- Seed `system_types` + `system_type_checks` from the code-defined
  `monitoringTemplates` catalog so detection and starter checks keep working.

## Impact on existing features

- **Monitoring templates / detection** — `detectTemplates` (metric-prefix
  matching) and `createTemplateChecks` read the `system_types` catalog instead
  of the hardcoded `monitoringTemplates` slice. The built-in catalog is just
  the seed.
- **Service kind picker** — the free-text combobox (`78caea0`) becomes "attach
  this service to a system" (pick/create a system; its type comes from the
  catalog). `system_kind` is superseded by `system_id`.
- **RBAC** — the existing `system` access-policy kind's expander changes from
  "services whose `system_kind = X`" to "the system's member services."
- **Digest** — "service identified as <type>" becomes "service looks like a
  <type> system — create one / attach it."
- **Systems page** — lists system **instances** (not flagged services); a new
  **SystemDetail** page shows type, member services, metadata, and a health
  rollup.

## Phasing (each independently shippable)

### Phase 1 — System-types catalog (foundation) ✅ done (2026-06-25)
- `system_types` table (org rows = custom + overrides); built-ins stay
  code-defined. Checks stored as JSON (shape shared with monitoringtemplates).
- `detectTemplates` + `templateByKind` read the **effective** catalog
  (built-ins merged with org rows); shared by suggestions, apply, and digest.
- Management UI (`/system-types`): list built-ins + custom, create (with
  starter checks copied from a built-in), edit identity, delete. Built-ins
  read-only.
- Service kind picker suggestions come from the catalog.
- Delivered the original "maintainable system types" ask.
- *Implementation note:* per-check editing in the catalog UI is deferred (a
  type's checks are copied from a built-in at create time, matching how custom
  monitoring templates work). An in-place check editor is a later add.

### Phase 2 — Systems as entities + membership ✅ done (2026-06-25)
- `systems` table + `services.system_id`; migration groups existing flagged
  services into systems by kind.
- catalog System entity + CRUD + attach/detach; the "mark as system" flag
  find-or-creates a system for the kind and attaches, keeping
  is_system/system_kind in sync — so **RBAC and existing health/templates are
  unchanged** (the expander still reads those fields, which stay accurate).
- `GET /systems` returns entities (visibility-filtered); SystemDetail page
  (type, members with health, attach/detach, rename, delete); Systems page
  lists instances.
- *Note:* membership stays in sync via is_system/system_kind, so the RBAC
  `system` expander needed no change. Dropping services.system_kind in favour
  of deriving it from membership is deferred (kept for compatibility).

### Phase 3 — System metadata schema ✅ done (2026-06-25)
- `metadata_fields.applies_to_system` + `system_type_key` (text, "" = all
  systems; targeting is by type *key*, not id, to match the hybrid catalog);
  `system_metadata` values table.
- metadata store: system scope + SystemFields/SystemValues/SetSystemValues
  (applicable = applies_to_system AND type matches).
- Field editor gains a **Systems** scope + a System-type picker (all / one
  type); SystemDetail renders applicable fields and saves values.
- Verified: a rabbitmq-targeted field shows on a rabbitmq system, not a kafka
  one; values round-trip.

### Phase 4 — System-level health ✅ done (2026-06-25)
Implemented as an **aggregate rollup** (not a new alert-rule target — the
chosen, lower-risk option; revisit if a dedicated target is wanted):
- `GET /systems` returns a rollup `status` per system (worst of its members'
  health, via the shared aggregateStatus); shown as a health pip on the Systems
  list + SystemDetail header.
- `POST /systems/{id}/apply-template` applies the system type's starter checks
  to every member service (idempotent), with an "Apply <type> checks to
  members" action on SystemDetail.
- *Deferred:* a dedicated `system` alert-rule target dimension (members'
  per-service checks already drive the rollup).

## Open / future

- **System health target** (phase 4): whether a system gets its own alert rule
  target or stays a rollup of member-service checks.
- **Cross-service membership** (join table) only if a service must belong to
  multiple systems.
- **Built-in overrides**: exact precedence when an org customises a built-in
  type by `key`.
