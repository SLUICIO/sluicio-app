# Changelog

_Generated from git history by `scripts/changelog.sh` — do not edit by hand._
_Internal: not shown anywhere in the Sluicio product._

## v0.11.1 — 2026-07-05

- ci(release): build + publish the demo-seeder image (2c6ac6b)
- feat(demo): deploy/demo overlay — continuous seeder + golden-snapshot reseed (25e0f27)
- feat(auth): org deletion is operator-only (35b94a0)
- fix(auth): demo accounts can't touch org lifecycle, members, SSO, or ingest keys (8f9eb67)
- release v0.11.0 — refresh internal changelog (6914e37)

## v0.11.0 — 2026-07-04

- feat(ui): demo-account toggle on the Operator page (0864b54)
- fix(ui): explain traces vs messages in the policy signals picker (fc6c590)
- fix(ui): disambiguate the two role dimensions in SSO claim mappings (13ae21e)
- feat(ui): Edit button on group rows opens the same blade as the name (6fd6a00)
- feat(ui): group detail opens as a blade (EditDrawer) (6837aed)
- feat(ui): dashboard × only in edit mode; positive dependency filters (2ef48b4)
- feat(ui): mark the focal service in the dependency graph with a primary glow (466fd2c)
- fix(ui): widget picker offered system_health; stale group panel self-heals (4026ddc)
- fix(rbac): dependency graph must not leak invisible neighbors (14cc458)
- feat(ui): paged operator user list; service deps become a flow graph (a988912)
- feat(facets): built-in stream-input / stream-output facets (io.kind=stream) (9b961a5)
- fix(e2e): stop borrowing integs[0] — it can be a doomed fixture (77adcb0)
- chore(license): drop the unused max_users limit (a3acbff)
- feat(ee): enforce MFA policy server-side; honor license retention cap (45815e1)
- feat(auth): demo accounts (is_demo) block self-service; unique user emails (4f04d66)
- fix(rbac): enforce the CE/EE edition boundary on scoped capabilities (a9ff895)
- feat(rbac): phase 4 — per-signal visibility (traces/logs/metrics/messages) (54e5cbb)
- feat(rbac): phase 3 — share integrations & systems (viewer-only, notified) (3ee9515)
- feat(rbac): phase 2 — scoped manage (capability = group role × policy scope) (17ae6b0)
- feat(rbac): phase 1 — attach groups to integrations & systems (CE visibility grant) (91e43b8)
- docs(rbac): RBAC v2 design spec — scoped capabilities, sharing, per-signal visibility (e432f11)
- test(rbac): full-stack coverage for expression access policies (d3d40d5)
- feat(rbac): tree editor UI for expression access policies (aa3d6f9)
- feat(rbac): boolean-expression access policies (arbitrary AND/OR/NOT) (3d9f253)
- fix(rbac): close trace-detail visibility leak, gate member list + message views (8e34b32)
- feat(audit): audit profile renames; actor-id filter spans name changes (7ca39a8)
- feat(audit): lazy-load audit entries on scroll (e30db4a)
- feat(audit): tamper evidence, access logging, retention, opt-in off-box sink (72f69a5)
- feat(audit): enterprise-grade gaps — CSV export, detail rows, operator dual-write, e2e coverage (f534a88)
- feat(audit): searchable audit log + full mutation coverage (df87876)
- feat(auth): org switcher for users with multiple memberships (50b9ed6)
- release v0.10.1 — refresh internal changelog (a0a48df)

## v0.10.1 — 2026-07-03

- fix(ingest): register sources without service.name as unknown_service (b8f3faa)
- release v0.10.0 — refresh internal changelog (673aea6)

## v0.10.0 — 2026-07-03

- feat(alerts): manage team notification channels inline, not via Settings (59f6c0d)
- fix(alerts): drop 'Firing now' section; badge firing checks inline (4e4103a)
- fix(alerts): sent-notifications filters as a horizontal bar like the other filters (3dedd3c)
- feat(alerts): tabs instead of columns (matching the integration/service tab style) (1fb739d)
- feat(health): Edit a health check straight from its result drawer (0d17165)
- fix(metrics): compact/byte-aware value formatting so big values don't overflow (8e09ace)
- feat(mcp): sluicio_error_report — access-scoped errors since a time + the causing check (277976f)
- feat(metrics): server-side search + cap so the explorer doesn't load everything (2e72a19)
- feat(alerts): three-column redesign with filtered/virtualized lists (343a9ae)
- feat(metrics): "age" aggregation + standardize textarea styling (3aa8b63)
- fix(system): request the max 500 alert instances for failing-checks (1f8645a)
- feat(system): list failing health checks on the system page (b71db8c)
- fix(system): order Add member service before the badge; drop no-op focus (f5f50aa)
- feat(dashboard): click the health KPIs to drill into the unhealthy list (0b53116)
- feat(system): open edit blade focused on the attach picker from get-started (8b86318)
- feat(system): actionable get-started empty state for a memberless system (2d47181)
- feat(system): edit via a right-side blade (name, description, badge, add member) (d5f2f0a)
- feat(service): move public status badge to a Settings tab (10f507c)
- fix(badges): gate public-badge toggles like the entity's other edits (7aca078)
- docs(architecture): add as-built cell diagram (Mermaid) (1e54209)
- docs(testing): operator protocol + GHCR pre-release checklist (f7b783a)
- feat(operator): operator UI — org management, members, settings gating (e3bda4c)
- feat(operator): cell-operator role + org management API (285490a)
- test(identity): role + team authorization integration guard (fd98041)
- test(api): multi-org tenant isolation guard (integrations/systems/services) (0d510a6)
- feat(badges): opt-in public status badge UI (integration/system/service) (0a42e4c)
- feat(badges): public opt-in status badges (backend) (1949727)
- fix(ui): confirm before deleting a channel / removing service from integration (4eb6b86)
- fix(sso): confirm before removing a claim/group mapping (475ce55)
- fix(sso): make the claim-mapping Add button primary (blue) (87af5b1)
- feat(service): typeahead for the "add to integration" picker (08fcc2d)
- fix(types): back out accidentally-committed WIP (AlertAggregation "age") (0bb2a69)
- feat(members): login method column + fix SSO last-login + hide org settings (927f472)
- fix(sso): build redirect_uri from SLUICIO_APP_URL, not raw request (ab938d4)
- chore: pre-public hygiene — scrub internal infra from changelog + add CoC (792a8b0)
- fix(ci): unbreak image publishing — decouple Trivy into its own job (bb0d213)
- feat(mcp): sluicio_health — unhealthy integrations/systems + why (7216e1a)
- feat(license): count systems toward the plan cap (integrations + systems) (ccf6e8f)
- fix(frontend): patch js-yaml + react-router prod advisories (82f3303)
- test: make secretcrypto TestTamperFails deterministic (6e0852a)
- ci: add security scanning (govulncheck, gitleaks, npm audit, Trivy) (92164f8)
- deps: bump Go toolchain to 1.25.11 + clickhouse-go to v2.47.0 (78b809e)
- secure: encrypt SMTP password + SSO client secret at rest (AES-256-GCM) (1ac6054)
- helm: optional bundled Postgres + ClickHouse (turnkey all-in-one) (e481ea4)
- deploy: one-command zero-config quickstart (8e8627d)
- deploy: startup packages for bundled vs bring-your-own databases (5024650)
- README: swap BizTalk for Apache Camel in the estate examples (8c8848f)
- README: add CI, release-images, release, and license badges (14964e9)
- Fix react-hooks/exhaustive-deps lint warning in SsoSettings (3009830)
- Don't track .claude/launch.json (Claude Code local config) (658d5f7)
- Open-source: genericize infra, add ghcr image publish + sovereign mirror CI (4c75730)
- Open-source prep: decouple core from ee/, license verification to core, OSS docs (2737f05)
- Service: show applied template checks immediately + scroll to them (6a53d21)
- License: signed integration cap + Usage KPI + admin limit notice (d533654)
- Service: make the 'Errors cleared' banner dismissible (77f7b85)
- UI: open the New facet / New system type forms as a right-hand blade (b2edc28)
- UI: stop the Name/Label field outgrowing its paired Slug/Key field (1f5165a)
- Logs: clickable service + integration, and show a service's integration (1a17e87)
- Message search: filter by trace ID or span ID (93f2729)
- Add Usage report (org-admin): telemetry volume, size, and series per service (0ba25cb)
- API & MCP page: show the remote connector URL from the live origin (7ae63a5)
- Fix metric drawer attributes reloading on every filter add (5f1f827)
- Metrics explorer: default service for health checks + drill-down by attribute value (837831f)
- UI: service Metrics tab shows only the metrics explorer (e360c27)
- UI: move Logs refresh to the page header (match Metrics) (c865631)
- UI: explain why a trace with no trace ID can't be opened (2a8c084)
- UI: add a Refresh button to Logs, Metrics, and Messages pages (f6f6015)
- Service facets: user-defined custom facets (create / edit / delete) (2c213a9)
- Topology: add Systems perspective + expandable drill-down trees (b31c2a5)
- Topology: metadata field picker is a typeahead (SearchableSelect) (8ce1cbb)
- Topology: multi-perspective graph (services / integrations / metadata) (1a63d0f)
- Topology: use the standard TimeWindowPicker (was a plain select) (7c52c8b)
- deploy: pass SLUICIO_LICENSE_KEY + SLUICIO_MFA_KEY to cell-api (registry compose) (9a6067f)
- EE license: rotate signing keypair (b394458)
- Metadata relationship graph: integrations ↔ metadata values + tags (fa6445a)
- Topology: org-wide service dependency graph (was a placeholder) (fe0a473)
- EE SSO/OIDC: frontend (Settings → SSO config + claim/team mapping, login buttons) (3d1671d)
- Integrations: always report persisted member services (not only with traffic) (40b8b49)
- EE SSO/OIDC: backend (providers, OIDC login flow, claim→role/team mapping) (17056c4)
- MCP: default traffic/error tools to a 24h window (was 1h) (9cb5465)
- UI: show date + time in trace lists + trace detail (was time-only) (ad8bc42)
- MCP: add sluicio_search_traces + sluicio_get_trace (32e54db)
- MCP: OAuth 2.1 authorization server for the remote endpoint (b1f2853)
- MCP: remote HTTP transport on cell-api (/api/v1/mcp) (25532ed)
- Nav: regroup into Monitor / Configure / Admin; Settings + Account tabs deep-link (1301d00)
- EE audit log: wire recordAudit into security-relevant mutations (9a427d9)
- Service system picker = catalog select (+ add type); filter services by system (f6357ef)
- Service accounts: non-admin guidance + admin-credential audit surfacing (e42a407)
- Add an in-app "API & MCP" getting-started page (2cb8d7f)
- MCP server (cell-mcp): read-only Sluicio tools over stdio (6b6c831)
- API phase D: token expiry + rotation (386b16c)
- API phase C: per-token least-privilege (role cap) (757324f)
- API phase B: OpenAPI spec generated from the route table + served (78af0af)
- API phase A (UI): Settings → Service accounts tab (85aad99)
- API phase A (backend): service-account management + token issuance (c0c246f)
- docs: API & API-keys 1.0 design (two-token model + phasing) (f82e16c)
- e2e: integration + metadata lifecycle (create, search, annotate, delete) (3f272ac)
- e2e: add Systems coverage (catalog, entity list, dashboard KPI) (a751898)
- Dashboard: add a "systems running" KPI card (b7115a6)
- SystemDetail: add-member uses a searchable-select typeahead (c0f0a99)
- Systems phase 4: system-level health rollup + apply-to-members (217c8cb)
- Systems phase 3 (UI): system-scoped metadata fields + editor (d9240ca)
- Systems phase 3 (backend): system metadata schema (42ecabd)
- Systems phase 2 (UI): Systems list of entities + SystemDetail (ada2baf)
- Systems phase 2 (backend): systems as first-class entities (68b3f1e)
- Systems phase 1: back the service kind picker with the catalog (7b0112d)
- Systems phase 1: managed system-types catalog (9629a6f)
- docs: systems-as-entities design (managed type catalog + phasing) (5b2d22a)
- README: rename to Sluicio + full rewrite (overview, features, architecture) (d6fbae3)

## v0.9.0 — 2026-06-25

- release v0.9.0 — refresh internal changelog (788a560)
- Fix 400 saving a service that has a log/trace health check (aeb9d24)
- Digest panel: right-align the severity badge in each row (fbbedfc)
- Hide the overview detection banner once the template is applied (49ef022)
- Templates: detect applied + allow remove (no more silent re-apply no-op) (2516a46)
- Service Metrics tab: reuse the Metrics explorer, scoped to the service (6df60dd)
- Service kind: free-text combobox instead of fixed dropdown (78caea0)
- Service settings: "System identification" box + harden check preview (5fa3ac3)
- Drop the stray top border on first list rows (85a15bd)
- Distinct nav icons for the Config section (c930349)
- "Since last visit" activity digest (RBAC-aware) (5002ec5)
- User-defined monitoring templates (frontend) (0202186)
- User-defined monitoring templates (backend) (d8936be)
- Templates: log-signal checks + broader collector coverage (6be6614)
- Fix metric health-check bugs found in audit (8d2812d)
- Brand the UI with more Sluicio blue (f993266)
- Service detail: prominent detected-template banner (03e5b41)
- Fix log histogram brush offset (viewBox letterboxing) (d1e3dfe)
- Logs: trim ingestion (smart rule builder from a log) (74102af)
- Traces: trim ingestion (advisory collector config) (3d3b854)
- Errors page + nav polish (23656c9)
- Dashboards: pin systems as system-health cards (ec5bc9c)
- RBAC: add a 'system' group access-policy kind (6f905da)
- Metrics: per-chart transform picker (raw/increase/rate) + interval (f20225d)
- Rate-based health checks (increase/rate) + per-service metric scoping (b60e731)
- Service-type templates: auto-suggest + manual, beyond systems (0b7fb14)
- Errors page: regroup by system/integration with service drill-down (24e6862)
- Systems: route alert channels when applying a template (00f43ba)
- Service metrics chart: span X-axis over the selected window (bf38f55)
- CI: reclaim docker disk before building images (f40ba12)
- Errors page: surface systems in trouble separately (4cecf2f)
- Fix dead padding right of the health-check status badge (a95aaee)
- Systems (P2): built-in per-kind monitoring templates (b90453c)
- Systems (P1): mark a service as a monitored system (36b0277)
- Services list: report count of services not in any integration (0d7428e)
- ci(gitea): only rebuild images on pushes that touch image source (08a0d8a)
- ci(gitea): also build + push images on every push to main (dd4fab6)
- Services list: group by integration (and namespace/status/tag/metadata) (02dfe52)
- Service dependencies: gap between the center node and caller columns (06c8ec5)
- Metrics: drop the metric-name suggestions dropdown (5194a85)
- ci/deploy: lowercase the registry namespace (org transfer) (9a56826)
- ServiceDetail: toggle golden signals to metric-type health checks (7b47dc5)
- SearchableSelect: render the popover in a portal so it isn't clipped (84255bf)
- Services list: metadata filter + dependency (upstream/downstream) filter (fa28da5)
- deploy: pull images from Gitea's registry (internal-registry) (de79b18)
- ci(gitea): revert to internal-registry over HTTPS (trust CA on runner) (fd1c5a1)
- ci(gitea): push images to the LAN registry over HTTP (internal-host) (3ccf3cf)
- ci(gitea): build + push images to Gitea's registry on tag (c1dbba8)

## v0.8.0 — 2026-06-22

- release v0.8.0 — per-user activity stats on Settings → Members (ecf6021)
- Members: per-user activity stats (logins, failed, last active, MFA) (a028bbc)

## v0.7.1 — 2026-06-22

- release v0.7.1 — per-user Last login + Member since on Settings → Members (7bb6c85)
- Members: show per-user Last login + Member since (3222812)

## v0.7.0 — 2026-06-22

- release v0.7.0 — integration Messages attribute filter + UX persistence (2141b09)
- Integration Messages: scope the attribute filter to the integration (70be484)
- Dashboards: Enter saves a new dashboard (d4bbe7a)
- Integrations: persist the user's visible-column choice (f752c89)

## v0.6.1 — 2026-06-21

- release v0.6.1 — Reports tab: lazy-render metrics + inline Trim ingestion (083ce6d)
- Settings → Reports: lazy-render the metrics list + open Trim ingestion inline (03309ee)

## v0.6.0 — 2026-06-21

- release v0.6.0 — configurable alert templates (rich context, Liquid email, content toggles, preview) (27b1211)
- Alerts: channel Kind picker uses the SearchableSelect typeahead (de99346)
- Use-case catalog + split build vs release verification (0a35877)
- Alert templates (Phase 1, frontend): content toggles + inline email + preview (2e4ae0a)
- Alert templates (Phase 1, backend): rich context, Liquid email, content toggles, preview (3915a58)

## v0.5.7 — 2026-06-21

- release v0.5.7 — style the service Edit checks button as a standard link button (430ec39)
- ServiceDetail: style the "Edit checks" button as a standard link button (68a1fb9)

## v0.5.6 — 2026-06-21

- release v0.5.6 — nav Errors pill + integration Metadata tab + Overview reorder (042a6fd)
- Nav Errors pill + integration Overview restructure (Metadata tab, error box up) (e1b8b55)

## v0.5.5 — 2026-06-21

- release v0.5.5 — Errors-tab badge reflects failing checks + open errors (62a01f1)
- Integration Errors tab badge: reflect failing checks + open errors, not just failed traces (bf3a200)

## v0.5.4 — 2026-06-20

- release v0.5.4 — message search opens traces in a blade; drop trace download button (5739198)
- Message search: open traces in the right-side blade; drop trace download button (bcbb2ec)

## v0.5.3 — 2026-06-20

- release v0.5.3 — integration Errors tab error-count pill (bf4595a)
- Integration Errors tab: error-count pill matching service-detail tab counts (37fe968)
- Add Postgres integration tests (testcontainers-go) + CI job (d3fc13b)
- docs/testing: component layer now exists; note CI was red until 2026-06-20 (d2d7aa3)
- Fix the two pre-existing CI failures (red since v0.3.1) (d3ff679)
- Add frontend unit + component tests (Vitest + Testing Library) (4d18fe2)
- Add test protocols: manual docs + Playwright e2e, wired into CI (5f02a07)

## v0.5.2 — 2026-06-20

- release v0.5.2 — searchable typeahead for the matcher field picker (97d5cc8)
- Matcher rules: field picker is a searchable typeahead (like the service picker) (09178a5)

## v0.5.1 — 2026-06-20

- release v0.5.1 — matcher attribute field: select of stored keys + custom + help (ec7518c)
- Matcher rules: attribute field is a select of stored keys + custom + help (4501f49)

## v0.5.0 — 2026-06-20

- release v0.5.0 — per-service integration matcher rules (4c03259)
- Integration matchers: per-service rules (service-scoped attribute predicates) (4459a2e)

## v0.4.1 — 2026-06-20

- release v0.4.1 — trim-modal filter-input height + most-specific prefix suggestions (51589b4)
- Trim ingestion modal: fix filter-input height + prefer most-specific prefix (95d07a3)

## v0.4.0 — 2026-06-20

- release v0.4.0 — OR-capable integration matching + persisted attribute-key catalog (9a49eca)
- Integration matcher UI: OR condition groups (a470bef)
- Integrations: OR matching via condition groups (DNF attribute predicate) (02cea92)
- Persist attribute-key catalog for the matcher / filter pickers (135cf5d)

## v0.3.1 — 2026-06-20

- release v0.3.1 — trim-ingestion modal: full screen, resizable columns, lazy list (a416dbb)
- Trim ingestion modal: full screen, resizable columns, lazy-rendered list (077b42d)

## v0.3.0 — 2026-06-20

- release v0.3.0 — message-view filtering + attribute-based integrations (a46fb46)
- Integrations B3: attribute predicate on flow graph + aggregate counts (e03462e)
- Integrations B2: apply attribute predicate to Messages/Logs/Metrics/Span names (e33da66)
- Integrations B1: attribute matchers — foundation (model, reconciler, UI) (fec44c0)
- Message views: pick services by name + every live attribute as a filter field (a8da848)

## v0.2.20 — 2026-06-19

- release v0.2.20 — refresh internal changelog (810b817)
- Hide the Config nav section from read-only viewers (d0e47c3)

## v0.2.19 — 2026-06-19

- release v0.2.19 — refresh internal changelog (bf6c98a)
- Security: scope alert-rule preview to the caller's visible services (25c1d84)
- Security: gate alert/health-check feeds by service visibility, not just team (513be92)
- Security: show "not found" (no name) when a service/integration is inaccessible (0b86ffc)

## v0.2.18 — 2026-06-19

- release v0.2.18 — refresh internal changelog (72c2316)
- Security: enforce integration access + viewer read-only; drop integration Settings tab (9cbf82c)

## v0.2.17 — 2026-06-19

- release v0.2.17 — refresh internal changelog (fcf4d4e)
- UI: confirm before deleting a health check; auto-slug + name hint on new integration (b721c14)

## v0.2.16 — 2026-06-19

- release v0.2.16 — refresh internal changelog (9d63b52)
- UI: say "error traces" instead of "errors" for trace error counts (f6e7590)

## v0.2.15 — 2026-06-19

- release v0.2.15 — refresh internal changelog (700fce9)
- Health: service status is health-check-driven only; graph shows health, not errors (02fc3e0)

## v0.2.14 — 2026-06-19

- release v0.2.14 — refresh internal changelog (cc3dfb3)
- Health: "Clear errors" also clears a service's failed-trace health check (48d1896)
- Health checks: fix "remove" button overflow in the edit list (c32a30c)

## v0.2.13 — 2026-06-19

- release v0.2.13 — refresh internal changelog (3bd0bbf)
- Health checks: low-traffic trace check + fewer-than logs + day windows (fc0bed6)
- Service inspector: wrap long check conditions; carry attrs to Metrics (9d7fc9f)
- Integration tabs: red ✕ error count on the Errors tab (14fafcf)
- Integration overview: explain WHY a service is unhealthy (failing checks) (00b594b)
- Integration flow + inspector: color by real service health, not window errors (c149329)

## v0.2.12 — 2026-06-19

- release v0.2.12 — refresh internal changelog (c1a1cd8)
- Integration detail: "Edit integration" button → tab-less settings view (f9d327c)
- Health checks: click a check to open a result blade with live evidence (e0ff61d)

## v0.2.11 — 2026-06-19

- release v0.2.11 — refresh internal changelog (3b75349)
- Alerts: lead every notification with environment + company, keep the link (f099d3d)

## v0.2.10 — 2026-06-19

- release v0.2.10 — refresh internal changelog (f2d3b68)
- Service health: widen the window when viewing open-error traces (5a5750a)
- ErrorBreakdown: make the "errors come from <service>" name a real link (6f9707a)

## v0.2.9 — 2026-06-19

- release v0.2.9 — refresh internal changelog (0da5a5d)
- Integration list: reflect a firing health check on a quiet member service (8c4cc1b)

## v0.2.8 — 2026-06-19

- release v0.2.8 — refresh internal changelog (f3bd32b)
- Metrics: scope the attribute-filter picker to the focused metric (e018825)

## v0.2.7 — 2026-06-18

- release v0.2.7 — refresh internal changelog (9517a63)
- Dashboard KPI: "integrations unhealthy", not "down" (d97dde3)

## v0.2.6 — 2026-06-18

- release v0.2.6 — refresh internal changelog (5162d2f)
- Dashboard tabs: status pip rolled up from each dashboard's integrations (628fda7)

## v0.2.5 — 2026-06-18

- release v0.2.5 — refresh internal changelog (a209d9a)
- Service health tab: show WHY a service is unhealthy, link to the traces (34515e1)

## v0.2.4 — 2026-06-18

- release v0.2.4 — refresh internal changelog (e5191a6)
- Alert emails: failed-trace notifications deep-link to the Errors page (7ee93bd)

## v0.2.3 — 2026-06-18

- release v0.2.3 — refresh internal changelog (21625b1)
- Health checks: response-time (trace latency) checks + built-in error-span check (687a9e1)
- Health checks: per-check resolve mode (auto-resolve vs require-ack) (d86eb89)
- Trace drawer: open an errored trace focused on the failing span (bd54044)
- Email alert channels reuse the system SMTP server by default (f574e1f)

## v0.2.2 — 2026-06-18

- release v0.2.2 — refresh internal changelog (d693ea1)
- Service health checks: see + manage all signal types (metric/log/trace) (626e01c)
- Service page: make the unhealthy status pill clickable to show what's wrong (547c726)

## v0.2.1 — 2026-06-18

- release v0.2.1 — refresh internal changelog (62c0790)
- deploy: drop the no-op cell-alerting service from the registry compose (0a4168e)
- Unacknowledged trace errors make a service (and its integration) unhealthy (5da2dbb)

## v0.2.0 — 2026-06-18

- release v0.2.0 — refresh internal changelog (0653a3c)
- deploy: wire SLUICIO_APP_URL so notification deep links render (c85daeb)
- Notification profiles: act on grouping + re-notify; sticky log/trace alerts (ab1637a)
- Dashboard: red "N of M down" tile + error-only failed-traces link (19b7fa0)
- Notification profiles: per-team delivery profiles + deep links (d3e1051)
- Per-team notification channels in Settings → Groups (18a3022)
- Notification routing: global-default + per-integration/team channels, and errors auto-notify (70a42c1)
- Persisted, window-independent "unacknowledged errors" until acknowledged (b78829c)
- Edit existing health checks (not just add/remove) (da181cb)
- Acknowledge / resolve failing health checks from the Errors surfaces (11ec370)
- Integration shows "unhealthy" when an integration-scoped health check is firing (8cdc624)
- Service-scoped failed-trace alerts (create from a service's Traces tab) (6521d19)
- Unified alert management + fix trace-signal collision (failed-trace vs trace-completion) (4ddb275)
- Health-check name field styling + matcher-aware dependency suggestions (623b884)
- Integration Overview: link the errors/unhealthy tiles to the Errors tab (1acc0de)
- Fix double-v in sidebar version + add one-step release wrapper (a44dcf2)

## v0.1.0 — 2026-06-17

- Establish build version (SemVer from git tags) + internal changelog (a642be5)
- MatcherConfig: default to equals + stop clipping the service picker (81022cb)
- Trace page: keep flow controls in their box + always show errors-only (c89e4c4)
- ErrorBreakdown: fix links + add failed-trace alerting (5c38652)
- Integration Errors tab: consolidated "what's wrong" view (f52d3c1)
- TraceWaterfall: add an "Errors only" filter (cea3bd0)
- TraceDrawer: dock selected-span details to the bottom (794c1a7)
- Trace detail: show selected-span details in a sticky side panel (9fd235b)
- Edit an integration's name + description from its Settings tab (2422e0c)
- Guide users from a new integration to services + ingestion (72f642b)
- Ingest keys: show the real ingest URL, not a placeholder (07f4d04)
- Drop internal `make seed-traces` hint from empty-state copy (d250b22)
- Viewer RBAC: team-gated telemetry + read-only config catalogs (00ba405)
- Hide + block Settings for non-admin roles (7341d30)
- Retention: cap the free tier at 2 weeks (was 30 days) (2044034)
- deploy: parameterize host ports so multiple cells run on one host (1b9cd32)
- Top bar: org name links to the dashboard, not settings (b55cf47)
- Top bar: show the current organization name next to the brand (b7cb8f3)
- Settings: "unused metrics" report + trim-ingestion prefix collapsing (81fd679)
- Service facets list: explain detection + how to remap your attributes (4823d69)
- Facets: show the OTel attributes that trigger detection; fix Alerts spacing (80a39c6)
- Discover services from logs + metrics too; filter the services list by name (dae4fdd)
- Logs alert rule: pick the health-check service from a dropdown (8125f77)
- Trim-ingestion modal: render the OTel config in a read-only editor (15752b8)
- AlertBuilder: bind a metric health check to ANY service (a47ec8d)
- Unify custom metrics into health checks (one concept) (eabeade)
- Trace-completion editor: suggest span names from the integration's traces (e01b309)
- dev: run cell-api + cell-ingest as containers in the local stack (bf8f0ba)
- feat(service): remove service from an integration inline (ca5eaa0)
- fix(service): refresh after add-to-integration + inline create (d65fe31)
- fix(login): use you@sluicio.com placeholder instead of example.com (94d8cf9)
- feat(ee): org-wide MFA enforcement (Enterprise mfa_policy) (58d06f5)
- feat(auth/frontend): TOTP MFA enrollment + login second step (fcd7b9d)
- feat(auth): TOTP multi-factor authentication (backend) (587bb72)
- feat(auth/frontend): SMTP settings form + forgot/reset-password UI (f1042f5)
- feat(auth): global SMTP + self-service password reset (2b1ad2d)
- ee(frontend): license surface + Enterprise upsell (8a8993b)
- ee: gate long retention, advanced RBAC, and add audit logs (3f78ee4)
- ee: enterprise edition scaffold + offline license-key gating (34eb1e4)
- feat(errors): real Errors page — failing health checks + affected services (b646103)
- chore(nav): reorder Monitoring group (Services up, Errors last) (af349c7)
- feat(services): clear-errors acknowledgement + safeFloat NaN-in-JSON fix (8ab1ff3)
- feat(metrics): make 'add as a health check' a first-class action (15dd933)
- fix(metrics): scroll suggestion list to keep active item in view (f364589)
- custom metrics: query-backed health metrics + builder UI (0277bb8)
- alerts: delivery history — show what notifications were sent (ec595e4)
- alerts: per-rule notification title + body templates (9d8f74e)
- alerts: attach rules to a team with member-only access control (6b66d2a)
- alerting: refactor channel delivery into a Notifier registry (9c37a1d)
- feat(onboarding): gate dashboard guide on missing ingest key; move exporter config to key page (5ce29d3)
- feat(messages): implement export-to-CSV (was a 'coming soon' stub) (8ef0968)
- feat(ingest-keys): show paste-ready collector config at mint time (5a2ca03)
- fix(frontend): proxy /api to cell-api in nginx (5da3109)
- fix(deploy): use POSTGRES_DSN in build-based server compose (0f9b0f3)
- fix: registry compose — services need POSTGRES_DSN (not discrete vars) (1d22272)
- deploy: make frontend host port configurable (FRONTEND_PORT) (46a02a0)
- feat: adaptive onboarding guide on the dashboard (9de3a78)
- deploy: run a full cell from registry images (compose + env example) (24daac6)
- deploy: local-CA wildcard TLS cert script + nginx template (98252f8)
- fix: drop nonexistent plugins/go.sum from service Dockerfiles (4d2f135)
- chore: gitignore local publish.docker.sh helper (4557b69)
- build: image build/push tooling + Dockerfiles for all services + frontend (4e49686)
- servicetypes: fix breakdown widget arg/placeholder mismatch (79a6c9a)
- docs: capture v1.1 metadata-faceted relationship graph idea (acd384a)
- fix: service golden-signal sparklines use real per-bucket series (9ef3fa9)
- feat: overlay schemas + maps on the integration flow graph (9db18e1)
- feat: delete saved message views from the rail (with confirm) (222b7f7)
- fix: consistent integration header on Messages tab + open scoped views inline (868490b)
- feat: full favicon/icon set from the Block-S logo package (dddd344)
- fix: sidebar footer shows real env + version, not hardcoded values (7610bd3)
- feat: new "Block-S" logo mark + favicon (logo handoff) (07dfc87)
- fix: dashboard "messages / window" sparkline uses real traffic, not a seeded shape (27d2da4)
- fix: top-nav search hints Enter, not ⌘K (c15f7fa)
- fix: surface errors in the top-nav search palette instead of failing silently (5605982)
- chore: remove dead ServiceLogsSection component (4a22fcf)
- fix: global search phase 2 — facets, tags, metadata, maps, schemas (#28) (367930b)
- feat: global top-nav search (phase 1) — integrations, services, messages, logs, metrics (#28) (07e0fc6)
- feat: service Messages filter parity, share permalinks, drop hardcoded scope banner (d6c4849)
- feat: service Logs tab uses full LogsView (filter parity) (59184a6)
- fix: service Traces tab — working "only failed" + deep links (6b489bb)
- chore: drop Topology from nav (post-1.0); confirm on alert ack/resolve (754fbb3)
- fix: red delete buttons on admin pages + tags color popover not clipped (d9e3925)
- fix: real per-route breadcrumbs (drop hardcoded "Integrations" prefix) (2c6e650)
- feat: real per-integration traffic sparkline on the dashboard (70a9ee9)
- fix: dashboard counts quiet integrations as quiet, not warnings (3e15e4e)
- fix: dashboard subtitle reflects the selected window + environment (#29) (b3e5aaa)
- test: access-level test framework — role + policy decision matrix (#31) (c36bb7e)
- feat: System settings tab with a configurable environment label (#27) (5d73bd9)
- fix: right-align the dashboard integration picker dropdown (#30) (1310b70)
- fix: acknowledge + resolve states for alerts (#32) (e4e7c0f)
- fix: searchable integration picker on the dashboard (#30) (0626f38)
- ci: install Go from go.work (1.25) so setup-go@v6 GOTOOLCHAIN=local passes (3c957c9)
- ci: clean up frontend lint warnings, enforce lint, bump actions to Node 24 (17c4347)
- facetmappings: fix stale order test (keys are parameterized, not inlined) (a97869a)
- Metric alerts: split-by-attribute breakdown (which values breach) (8077cd7)
- Metrics alerts: add "last value" aggregation for point-in-time gauges (e24dcc3)
- Logs: make the "(no integration)" group expandable (4b93127)
- Settings: box Trace completion rules to match Configuration · matchers (417b1d0)
- Integration tabs: split Overview into Services + Settings, delayed→Messages (c775052)
- Logs: deep-link the drawer "Copy link" to the selected log (fa49f02)
- settings: scope the delayed-traces panel to the header time window (b59bdd7)
- fix: window-consistent delayed count so success rate adds up (2e4b3b2)
- ingest/tenancy Phase 2: enforce OrganizationId on telemetry reads (65dd530)
- ingest: authenticate OTLP by per-org API key + stamp OrganizationId (3cdde2a)
- docs: cost model for one Azure cell (5 GB/mo, 14-day retention) (0c3727e)
- docs: AKS hosting (ClickHouse on block storage + cell chart) (51a9c9f)
- docs: Azure Container Apps deployment (Bicep + guide) (78c55d9)
- tags: keep inline "Create" button inside the picker popover (e60ddd6)
- alerts: log-based alert rules (log health checks) (491b8a5)
- logs: keep the log list pinned while the details drawer scrolls (a9ca5b4)
- alerts: "Send test" button for notification channels (dcb5642)
- alerts: email notification channel + contributor-gated channel/rule mgmt (f6cda13)
- delayed traces: let operators mark a delayed trace as "handled" (961c1d9)
- delayed traces: source dashboard/detail counts from open firings (d5b43e2)
- dashboard: dedicated delayed-traces KPI tile (53d0100)
- trace completion: start-gated multi-stage pipelines + delayed-in-success-rate (836db3d)
- Open Map/Schema content in a drawer instead of inline table rows (215a0b3)
- tracecompletion: sweep open firings against ClickHouse each tick (adba262)
- trace completion: auto-resolve + delayed tile + 'delivered with delay' (3f5badf)
- trace status: scope SLA breaches to integration context (4d7f8e9)
- trace status: SLA breaches drive per-trace pip colour (1717bde)
- IntegrationMessages: mark delayed traces in the row list (34e8199)
- tracecompletion: surface delayed firings in the UI (e1a480f)
- tracecompletion: channel_ids returns [] not null on the wire (+ FE guard) (3d72e20)
- Integration pages: shared header across Overview / Logs / Settings (48304fc)
- Trace-completion SLA: per-integration rules + delayed-trace firings (946e378)
- cell-ingest: log every accept + reject, expose counters on /healthz (c537820)
- deploy/server: make container runtime configurable, default Podman (ef2835d)
- deploy/server/update.sh: real update tool, not just git pull (7632938)
- deploy/server: one-shot bootstrap for single-server Sluicio (14f272d)
- Rebrand Conduit → Sluicio: repo + wire contracts + identifiers (#23) (1f1d45a)
- Cell settings: per-telemetry-type retention policy + enforcer (ff9f933)
- ClickHouse perf: pagination audit + P0 fixes (904d8da)
- ClickHouse perf: ship P0 fixes + audit doc (19c9eba)
- Integration Logs tab + HealthChecks slide-in editor (6754648)
- TraceDetail: Logs for this trace section (c6d3623)
- Login: hide default-admin hint after first successful sign-in (542120c)
- Split Account ↔ Organization settings (a543191)
- Rebrand Conduit → Sluicio: new color scheme + Flow-S logo (a80059b)
- Settings: wrap admin mutation forms in EditDrawer (5f1160a)
- groups G5: enforce policy filter on per-service routes + cross-service queries (afb9f2e)
- groups G3+G4+G6: ABAC policies + catalog attribute capture + group-role write gate (b4827ce)
- groups G1+G2: second access-control axis under org (services scoped, manual assignment) (8f591f4)
- auth P5: Settings page (members CRUD, PATs, SSO preview) + bearer-token verify (f45b5ec)
- auth P4: real login page + UserProvider gating the SPA on /me (c424db6)
- auth P3: gate every cell-api endpoint, drop DefaultOrgID (2779716)
- auth P2: cookie-session middleware, login/logout/me, demo protected endpoint (8f039b4)
- auth: drop Keycloak; ship native email+password with optional per-org OIDC (56923ba)
- auth P1: fix Keycloak account-console flow on fresh installs (1053506)
- auth P1: drop _comment from realm-export.json so Keycloak imports it (4777190)
- auth P1: Keycloak, schema, identity package, docs (no middleware yet) (f5156fc)
- metadata: switch multi-column from CSS columns to grid (predictable, symmetric) (4dac2e1)
- metadata: flow into 2 columns on wide layouts (KVTable columns="auto") (5e9401c)
- logs, alerts: stack title + subtitle in the page header (match convention) (a7e4d1a)
- styles: tighten form-context inputs (.form__label) — they're oversized in EditDrawers (782262a)
- admin pages: roll EditDrawer out to Metadata fields, Schemas, Maps (3c95280)
- tags: open Create-tag in a right-side overlay drawer (EditDrawer primitive) (dc05f13)
- tags: match SchemasPage/MapsPage flow on the create form (09031fa)
- maps: drop .form__row wrap so Test panel sample-input editor goes full width (4710bbc)
- maps: don't wrap the Test panel's sample-input CodeEditor in <label> (24cc7a3)
- Wrap Maps / Metadata fields / Schemas tables in .card (546c49f)
- logs: refetch grouped entries when filters change (b897c38)
- maps: Test panel now uses CodeMirror for input + output (b8eb462)
- docs: autonomous worker now drains the queue per firing (not one issue) (7716b36)
- Align integration metadata + trace attribute panels via shared KVTable (904f2d7)
- Render service metadata + schemas panels as proper tables (e8b3c14)
- go.work: bump to 1.25 to match deps merged with the Map Test panel (5aeb52e)
- maps: in-editor Test panel — run XSLT / Liquid + validate against pinned schemas (c5d5626)
- docs: autonomous issue worker — label conventions and contract (0d9eece)
- maps: split data transformations out of schemas into first-class entity (818a7c8)
- frontend: extend Format document to XML / XSLT / HTML and Protobuf (c716e64)
- frontend: portal TagPicker popover so it escapes overflow:hidden ancestors (8f50f7d)
- frontend: add Format document button to schema editor (ce798bc)
- fix(services): list now shows every known service, even in empty windows (944be3e)
- fix(schemas): editor Name and Version inputs now share the same font (14942ff)
- feat(schemas): CodeMirror 6 editor for schema content (lazy-loaded) (d123a5b)
- feat(schemas): list "Used by" column now lists clickable service chips (89e5cd7)
- feat(schemas): kind + version + syntax highlighting (b3e3878)
- feat(schemas): data schemas + per-service In-Schema / Out-Schema links (74767ec)
- feat(catalog): persist services + integration↔service membership in Postgres (ccfddf2)
- fix(integration-detail): services list mirrors the flow graph's historical fallback (b98574c)
- fix(time-window): selected range now follows internal navigation (8cad832)
- fix(dashboard): "needs attention" no longer flags integrations with 0 errors (3d37049)
- feat(integrations): filter value picker is now the shared SearchableSelect (d51b1cc)
- fix(integrations): filter input no longer loses focus after each keystroke (b8f1028)
- fix(integration-flow): historical fallback no longer reports stale errors (d460b70)
- feat(integrations): per-field filters + column picker on the list page (65b6612)
- feat(integrations): list page now shows description + a column per metadata field (4a8fbdf)
- fix: empty-window service detail returned 200 with no body (39933f1)
- fix(metrics): keep the metric list sticky while the drawer scrolls (bdca5a0)
- feat(metadata): user-defined typed fields for integrations and services (4ac1f3f)
- Integration detail UX: count, trace drawer, matcher picker, time-filter, historical-flow fallback (4127113)
- fix(frontend): make Service detail layout consistent with Integration detail (0272d54)
- feat(service): rebuild Service detail page — viewer + edit (S2/S3) (1c26d56)
- feat(service): editable service metadata store + serviceDetail status (S1) (2f56fe6)
- refactor(health): refocus metric health binding on services (4beedf0)
- feat(health): per-service & per-integration metric-formula health checks (cfe8a1d)
- feat(metrics): Trim ingestion — generate an OTel exclude config (dee902c)
- copy(metrics): nudge reviewing + keeping only metrics you act on (6a8cd23)
- fix(alerts): make the integration-health picker filterable (a613e56)
- feat(alerts): bind a metric threshold to an integration's health (8003386)
- chore(ui): drop trace replay/retry/drop actions (not a control plane) (f840e2e)
- chore(ui): drop runtime-control actions (Conduit isn't a control plane) (7c86b80)
- fix(groups): right-align the attribute key picker popover (ba3e78f)
- fix(groups): searchable attribute key picker + group-by feedback (8a0f56c)
- feat(metrics+logs): group-by rollups in the UI (G2) (2c72235)
- feat(metrics+logs): group-by rollup backend (G2) (732de28)
- feat(metrics): per-metric attributes in the drawer (G1) (83d09b2)
- feat(metrics): alert-builder drawer + Alerts page (M5) (7bea19e)
- feat(alerts): evaluator + delivery pipeline (M4) (12a7389)
- feat(alerts): alert-rules + notification-channels backend (M3) (8e3c9cb)
- refactor(metrics): rename "dimension" to "attribute" (OTLP semantics) (e8ca6db)
- feat(metrics): explorer frontend — search, dimension filters, sparkline table, drawer (aafb174)
- feat(metrics): explorer backend — rich catalog + dimension filters (6708d7a)
- feat(seed): OTLP semantic-convention attributes on logs and metrics (4e65f2f)
- fix(logs): informative empty state when no attribute keys indexed (61cc271)
- fix(logs): typed attribute key goes to value step, not auto-exists (fe7abf6)
- fix(logs): attribute popover clipped by filter card overflow (e3678e7)
- feat(logs): volume histogram (Phase 2) (321f043)
- feat(logs): attribute filtering + design-handoff redesign (filter bar, table, drawer) (153b975)
- feat: paginate + virtualize logs and message search at scale (0d8f223)
- feat: browse ingested OTLP logs and metrics (6fa1a5e)
- feat(cell-ingest): ingest OTLP logs and metrics into ClickHouse (5836179)
- feat(frontend): edit service facets + tag services like integrations (741232a)
- feat(cell-api): manual service-facet overrides + tags on service responses (5c47639)
- chore: backfill go.work.sum module checksums (2d7608c)
- first commit (8ac9b3f)

