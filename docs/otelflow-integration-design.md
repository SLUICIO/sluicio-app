<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# OTelFlow inside Sluicio (design)

Status: **draft for review** (2026-07-15).

[OTelFlow](https://github.com/SLUICIO/otelflow) is our standalone, Apache-2.0
visual designer for OpenTelemetry Collector configurations: live pipeline
graph, version-aware in-browser (WASM) validation, click-to-configure
components. Today it persists to the browser's localStorage and shares via
URL fragments. This design brings it into the Sluicio ecosystem — saved,
org-owned, RBAC-scoped collector configurations, editable from inside the
app — **without** coupling the OTelFlow repository to Sluicio in any way.

## The boundary (the part that must never blur)

Two repos, one direction of dependency:

- **SLUICIO/otelflow stays exactly what it is**: Apache-2.0, no accounts, no
  IAM, no Sluicio imports, works identically for everyone. Anything it gains
  from this project is a *generic embedding capability*, documented for any
  host product, useful without Sluicio, and reviewed against one question:
  "would we merge this if a stranger PR'd it for their own product?"
- **sluicio-app consumes OTelFlow** the way it would consume any third-party
  tool: pinned version, attribution in our third-party notices, all
  user management / persistence / RBAC / ecosystem glue on OUR side of the
  fence. FSL code never flows upstream; the Apache-2.0 tool never learns
  Sluicio exists beyond its own README.

The contract between them is a small, **versioned postMessage protocol** —
not shared code.

## What OTelFlow gains (generic, upstreamable)

**A Host Bridge: an embedding API for document storage.** OTelFlow already
embeds as a read-only iframe canvas. We extend that to the full editor with
an opt-in postMessage protocol (`?host=1`): when embedded by a host page,
OTelFlow swaps its localStorage persistence for host-provided storage.

- `otelflow:ready` → host — editor loaded, protocol version announced.
- host → `host:load {name, yaml, readOnly, collectorVersion}` — open a doc.
- `otelflow:dirty` / `otelflow:save {yaml, collectorVersion}` → host — the
  Save action hands the document to the host instead of localStorage.
- host → `host:saved | host:save-failed {message}` — round-trip result.

Properties that keep it honest open source: no auth in the protocol (the
host owns identity), no network calls added (validation stays in-page WASM —
the privacy story is untouched), documented in OTelFlow's README as "embed
OTelFlow in your own product", semver'd so hosts pin against breakage.
Anyone — including competitors — can build what we're building. That's the
point of the license.

## What Sluicio adds

### The entity: saved collector configurations

New Postgres table `collector_configs`:

    id, org_id, name (unique per org), description,
    yaml TEXT, collector_version TEXT,
    group_id UUID NULL,          -- team scoping, same semantics as dashboards
    created_by, updated_by, created_at, updated_at

plus append-only `collector_config_revisions` (config_id, revision_no, yaml,
collector_version, saved_by, saved_at): collector configs are
infrastructure-as-text; every save keeps the previous body. Restore = save
an old revision as the new head. No pruning in v1 (they're small).

### RBAC — the dashboards model, verbatim

`group_id NULL` = org-wide (every member sees it; org editors manage);
`group_id` set = the team's config (members see it; team editors + org
editors manage — team-editor manage is EE like everywhere else; CE demotes
group roles to viewer). Scope-capped tokens never gain manage. Invisible
team configs read as 404. This reuses `canSeeDashboard`/`canManageDashboard`
logic generalized into a shared helper rather than a third copy —
`callerGroupRoles` already covers users AND scoped service accounts, so
machine tokens get correct visibility for free (an MCP assistant with a
scoped SA can read exactly its team's collector configs).

Deliberately NOT in v1: EE resource shares (integrations/systems have them;
extending shares to a third resource kind is mechanical but real work — do
it when someone asks) and per-signal tiers (meaningless for config text).

### API + surfaces

- `GET/POST /api/v1/collector-configs`, `GET/PUT/DELETE …/{id}`,
  `GET …/{id}/revisions`, `POST …/{id}/revisions/{n}/restore` — all audited
  (`collector_config.created/updated/deleted/restored`), team-gated as
  above. Config-transfer export gains a `collector_configs` section
  (natural key: name; revisions do not transfer).
- **UI: a "Collectors" page** (nav placement open below). List view in the
  house style (name, team badge, collector version, updated-by/at) → opening
  one loads the embedded OTelFlow editor (iframe + Host Bridge) with Save
  wired to the API. Viewers get `readOnly` — OTelFlow's existing read-only
  canvas mode. "New collector" opens an empty editor or one of OTelFlow's
  examples.
- **The ecosystem hook that makes it feel native**: a "Send to this cell"
  action that inserts/updates an `otlphttp` exporter block pointing at the
  cell's ingest base URL (Settings → System, same source as the Ingestion
  page snippets) with `Authorization: Bearer ${env:SLUICIO_INGEST_KEY}` — an
  **environment placeholder, never a literal key**. Configs are shared,
  exported, and revision-kept; secrets don't belong in them, and the
  collector expands env vars natively. The Ingestion page links here
  ("design your collector config") next to the copy-paste snippets.
- On save, a soft lint warns when the YAML appears to contain literal
  credentials (`api_key:`, `password:`, `Authorization: Bearer <long
  literal>`, …) and suggests `${env:…}` — warn, never block.

### Deployment

The OTelFlow app itself ships as its own container
(`ghcr.io/sluicio/otelflow`, ~15 MB, stateless) added to the quickstart
compose and Helm chart next to the frontend; the frontend proxies
`/otelflow/` to it so the iframe is same-origin (no third-party-cookie or
CSP contortions, works air-gapped from a registry mirror). A cell setting
(`otelflow.base_url`) lets custom deployments point elsewhere. We do NOT
default to otelflow.sluicio.com — a self-hosted Sluicio must not leak
config-editing sessions to our public instance; that would betray both
products' privacy story. Sluicio pins the OTelFlow image version and bumps
it deliberately (protocol is semver'd).

### Later hooks (explicitly out of v1, designed not to be precluded)

- **Telemetry Advisor** (docs/telemetry-advisor-design.md): advisor
  suggestions ("this metric is ingested but never read — drop it at the
  collector") open the relevant saved config in the editor with a proposed
  diff. The revisions model and Host Bridge `host:load` already carry
  everything needed.
- MCP read tool (`sluicio_list_collector_configs`) once someone wants
  assistants to reason over collector topology.
- Validation against real collector binaries (OTelFlow roadmap item) —
  arrives in Sluicio automatically via image bump.

## Work plan

1. **OTelFlow repo**: Host Bridge (protocol doc + implementation + example
   host page + e2e). Independent release; useful standalone.
2. **sluicio-app**: migration + store + handlers + RBAC helper reuse
   (Go tests), Collectors UI + embed, "Send to this cell", audit + config
   transfer, Playwright suite (CRUD, team visibility, viewer read-only,
   revision restore — mirroring dashboards-rbac.spec patterns).
3. **Deploy**: compose + Helm + docs; THIRD-PARTY attribution entry.

1 and 2 are independent until the embed lands; the API/list UI can ship
first with a "paste YAML" textarea fallback, embed following.

## Open questions

1. **Nav placement**: its own sidebar entry "Collectors" (proposed — it's a
   first-class artifact, peers with Integrations/Systems) vs a tab under
   Developers/Ingestion.
2. **CE vs EE**: proposed = dashboards parity (team visibility CE,
   team-editor manage EE). Agree?
3. Should "Send to this cell" also offer picking an existing ingest KEY
   NAME (still emitting only the env placeholder, but naming which key the
   deployer should export)?
4. Revision retention: unlimited (proposed) or cap per config?
5. Is the public otelflow.sluicio.com instance ever an acceptable fallback
   for the embed (proposed: no — bundled container only)?
