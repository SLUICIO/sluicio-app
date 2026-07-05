# Tags

Tags are a flat, org-scoped label vocabulary that can be attached to
**integrations** and to individual **services**. They exist so teams can
group monitored things along axes that aren't naturally captured by the
matcher model — department (`HR`, `Finance`), environment (`prod`,
`staging`), owning team, criticality, and so on.

The model is deliberately small in v1. We're betting it's better to
ship something with clear semantics and grow it than to ship a
sophisticated facet system nobody fills out.

## Data model

```
tags
 ├─ id, organization_id
 ├─ slug (unique per org)        ← URL- and filter-safe identifier
 ├─ name                         ← display label
 └─ color                        ← lowercase hex, "#rgb" or "#rrggbb"

integration_tags (M:N)
 ├─ integration_id  → integrations(id) ON DELETE CASCADE
 └─ tag_id          → tags(id)         ON DELETE CASCADE

service_tags (M:N, keyed by name)
 ├─ organization_id
 ├─ service_name                  ← OTel service.name
 └─ tag_id          → tags(id)    ON DELETE CASCADE
```

### Why two separate join tables

Integrations are first-class rows in the cell's Postgres. Tagging them
through `integration_tags` is the obvious choice — a normal M:N with
cascading deletes.

Services, however, are *not* rows in Postgres. They're discovered from
telemetry that lands in ClickHouse, and they come and go as workloads
move around. We deliberately key `service_tags` by `(organization_id,
service_name)` rather than by a foreign key, because:

* The tag should survive a service going quiet for a few hours.
* New services that appear with a known name should immediately pick
  up any tags previously applied (a renamed deployment that returns to
  its old name shouldn't lose its `prod` label).
* We don't want a write path from ingest into Postgres just so we can
  hang tags off a row.

The cost is that orphaned `service_tags` rows can accumulate over time
if a service is renamed for good. That's a fine background-janitor
problem for later — not a correctness issue.

### Why a dedicated store rather than tags inferred from integration membership

We considered making service tags purely derived ("a service inherits
the tags of every integration whose matcher catches it"). That keeps
the model simpler but it gives up two things teams actually want:

* Tagging a *single* service inside an integration (e.g. one HR
  service that runs in the `pci` zone) without splintering the
  integration.
* Tagging services that don't belong to any integration yet — useful
  when someone is still classifying a new pile of workloads.

So service tags get their own store. Integration tags and service tags
are independent; the UI may union them when rendering a service's
chips, but the storage stays orthogonal.

## API

All endpoints are org-scoped via the active organization (currently
`integrations.DefaultOrgID` until auth is wired up).

```
GET    /api/v1/tags                                  list tags in the org
POST   /api/v1/tags                                  create a tag
GET    /api/v1/tags/{id}                             one tag
PATCH  /api/v1/tags/{id}                             update name / color
DELETE /api/v1/tags/{id}                             delete tag (links cascade)

GET    /api/v1/integrations/{id}/tags                tags on the integration
POST   /api/v1/integrations/{id}/tags/{tagId}        attach (idempotent)
DELETE /api/v1/integrations/{id}/tags/{tagId}        detach

GET    /api/v1/services/{name}/tags                  tags on the service
POST   /api/v1/services/{name}/tags/{tagId}          attach (idempotent)
DELETE /api/v1/services/{name}/tags/{tagId}          detach
```

Attach calls use `ON CONFLICT DO NOTHING` so the frontend can fire the
same request on every chip add without local de-duplication.

## What v1 does *not* cover

* **Categories / namespaces.** Tags are flat. If we need facets later
  (`department:hr`, `env:prod`), we'll add a `category` column rather
  than overloading the slug — but we'd rather see real usage first.
* **Tag-scoped permissions.** Tags don't gate access in v1. RBAC is
  still on the membership level.
* **Tag-based alert routing.** Alert rules currently route by
  integration. A natural next step is letting a rule fire on "all
  services tagged `prod`", which only needs an extra `tag_id` column
  on `alert_rules` or a new `alert_rule_tags` join.
* **Bulk tagging via matchers.** No "auto-tag every service matching
  `^hr-`" rule yet. Today that's expressed by creating an HR
  integration with that matcher and tagging the integration.
