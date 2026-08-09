# Changelog

_Generated from git history by `scripts/changelog.sh` — do not edit by hand._
_Internal: not shown anywhere in the Sluicio product._

## v0.11.94 — 2026-08-09

- fix(traces): stop claiming every hand-off-free message predates the feature (79a9b09)
- docs(testing): a Node-RED fixture for the one #12 case that needed real data (bca8f5a)

## v0.11.93 — 2026-08-09

- release v0.11.93 — refresh internal changelog (1b11302)
- docs(testing): track what has shipped but nobody has watched work (b540f94)

## v0.11.92 — 2026-08-09

- release v0.11.92 — refresh internal changelog (eb3d857)
- feat(traces): draw a trace as a graph of steps, not only a waterfall (87112b1)

## v0.11.91 — 2026-08-09

- release v0.11.91 — refresh internal changelog (1165bf0)
- feat(traces): store span links, so a hand-off stops looking like a stop (f14cfc4)

## v0.11.90 — 2026-08-09

- release v0.11.90 — refresh internal changelog (7c44c00)
- feat(mcp): let an agent see its own proposal queue and the candidates (ed2052b)
- feat(proposals): create-proposal plumbing and cell-surfaced candidates (dac5cb0)
- feat(proposals): find candidate integrations from the call graph (0586c9c)

## v0.11.89 — 2026-08-09

- release v0.11.89 — refresh internal changelog (f2f2b2d)
- feat(collector): generate snippets for the collector the customer runs (1e6d880)
- feat(health): report whether this cell is actually doing its job (a26068e)
- feat(errors): attribute an integration's failures to the flow, not the runtime (e24a006)

## v0.11.88 — 2026-08-09

- release v0.11.88 — refresh internal changelog (50ad62a)
- fix(health): keep every check's actions on one line (79b8a1f)

## v0.11.87 — 2026-08-08

- release v0.11.87 — refresh internal changelog (ada4a8a)
- feat(integrations): a Metrics tab on the integration (6560508)
- fix(metrics): "view metric" carries the scope the check is bound to (3d010ea)
- fix(seed): let the file-transfer demo choose which nights it missed (885ec65)
- fix(integrations): a tag filter no longer reads as "showing all" (cf9170e)

## v0.11.86 — 2026-08-08

- release v0.11.86 — refresh internal changelog (26e1afb)
- fix(onboarding): the collector snippet no longer fails to start on current collectors (9ae16f5)

## v0.11.85 — 2026-08-08

- release v0.11.85 — refresh internal changelog (6510bbf)
- fix(seed): give the file-transfer demo a readable gap as well as a red check (a6e2bbf)
- fix(routing): show a page instead of nothing for an unknown URL (189dc87)

## v0.11.84 — 2026-08-07

- release v0.11.84 — refresh internal changelog (84ed1d5)
- fix(trace): mark the service in the flow when a waterfall row is picked (728fd51)

## v0.11.83 — 2026-08-07

- release v0.11.83 — refresh internal changelog (33225ad)
- feat(flow): show where a message got to, and how to get there (85bef3c)
- feat(flow): project one message onto the integration flow graph (0d14c5d)

## v0.11.82 — 2026-08-07

- release v0.11.82 — refresh internal changelog (1dd8b66)
- feat(system-types): search the catalog by name (7382782)

## v0.11.81 — 2026-08-07

- build(frontend): bump js-yaml to 4.3.1 to clear the audit gate (30f2b33)
- release v0.11.81 — refresh internal changelog (e998b84)
- fix(advisor): say why there is nothing to advise, and answer "Evaluate now" (dad6817)
- fix(advisor): record demand when someone reads traces (82ebedf)

## v0.11.80 — 2026-08-07

- release v0.11.80 — refresh internal changelog (2eea418)
- a11y: clear the WCAG 2.1 AA violations, and keep them cleared (88574e7)
- seed: a file-transfer scenario for demos and screenshots (891f1fd)

## v0.11.79 — 2026-08-07

- release v0.11.79 — refresh internal changelog (baf73a0)
- errors: show checks that recovered on their own (0e60496)

## v0.11.78 — 2026-08-06

- release v0.11.78 — refresh internal changelog (0c6f8fb)
- service page: bring its health list in line with integrations and systems (5a7ece2)

## v0.11.77 — 2026-08-06

- release v0.11.77 — refresh internal changelog (eaed5b5)
- health checks: say how long one has been firing, and link to the metric (5f0f396)

## v0.11.76 — 2026-08-06

- release v0.11.76 — refresh internal changelog (22d7fb4)
- ui: centre a status pill's text when the label wraps (20063dc)

## v0.11.75 — 2026-08-05

- release v0.11.75 — refresh internal changelog (cb5b414)
- mail: bound the SMTP send, give the test button feedback, add a Message-ID (0a9429a)

## v0.11.74 — 2026-08-05

- release v0.11.74 — refresh internal changelog (b019c4c)
- errors: right-align the acknowledge button on an expanded error row (52b12ca)

## v0.11.73 — 2026-08-05

- release v0.11.73 — refresh internal changelog (ab366d9)
- errors: update the nav count immediately, and stop promising silence (ebeb4dc)

## v0.11.72 — 2026-08-05

- release v0.11.72 — refresh internal changelog (d2cdc32)
- health checks: make "edit" open the check's editor, not just its page (bc81e64)
- release v0.11.72 — refresh internal changelog (3deef61)
- integrations: keep Edit on every tab, and let the Errors tab edit a check (0600de5)

## v0.11.71 — 2026-08-05

- release v0.11.71 — refresh internal changelog (419316e)
- alerts: count an integration's OWN traffic in the low-traffic check (21775da)

## v0.11.70 — 2026-08-05

- release v0.11.70 — refresh internal changelog (87225fa)
- search: stop the saved-views load discarding a filter you just added (b5feeb1)

## v0.11.69 — 2026-08-05

- release v0.11.69 — refresh internal changelog (5c283b4)
- e2e: halve the filter test's budget, and stop it racing a vanishing row (b34e4d8)

## v0.11.68 — 2026-08-05

- release v0.11.68 — refresh internal changelog (678c75c)
- e2e: make a missing filter row report the exception that caused it (ee01253)
- release v0.11.68 — refresh internal changelog (7a16c2f)
- e2e: cap the action timeout so a stuck click says what it was waiting for (074f06b)
- release v0.11.68 — refresh internal changelog (fa25ec9)
- e2e: give the rbac visibility tests their own integration (9b82199)
- alerts: let a low-traffic check fire on an integration that has gone silent (173e1c4)

## v0.11.67 — 2026-08-05

- release v0.11.67 — refresh internal changelog (c6a0940)
- e2e: stop the dashboard spec assuming a reload returns to its board (9ad8d5f)
- release v0.11.67 — refresh internal changelog (d0f7b68)
- health checks: colour a check by its state, and refresh the page it changes (97ec502)

## v0.11.66 — 2026-08-04

- release v0.11.66 — refresh internal changelog (ac8f930)
- e2e: fix two test budgets my own specs broke (764e689)
- release v0.11.66 — refresh internal changelog (8b8dc2d)
- dashboards: make "my default" actually decide where you land (1a604eb)
- integrations: keep the tags visible on every tab (2ce3da2)
- integrations: clone an integration under a new name (b689404)

## v0.11.65 — 2026-08-04

- release v0.11.65 — refresh internal changelog (fe70fa1)
- health checks: stop the card clipping its own add-check menu (c94cc0a)

## v0.11.64 — 2026-08-02

- release v0.11.64 — refresh internal changelog (dede9fc)
- alerts: let a metric check fire when the metric goes silent (dd28988)

## v0.11.63 — 2026-08-02

- release v0.11.63 — refresh internal changelog (7379b4e)
- dashboards: put what is broken at the top (c9a97ef)

## v0.11.62 — 2026-08-02

- release v0.11.62 — refresh internal changelog (fa90a5c)
- e2e: stop the protocol spec leaking an integration on every run (3b62522)

## v0.11.61 — 2026-08-02

- release v0.11.61 — refresh internal changelog (b41018e)
- system types: hyphens instead of em dashes in the Paperless-ngx entry (508c5e0)
- system types: link the published Paperless-ngx docs page (fc9f3bf)
- system types: name the real cause of the Paperless-ngx trace blind spot (661ba6c)

## v0.11.60 — 2026-08-02

- release v0.11.60 — refresh internal changelog (3eb6407)
- e2e: stop the dashboard system tests racing the page's systems fetch (567f2a3)
- release v0.11.60 — refresh internal changelog (8908c8b)
- e2e: give the dashboard system tests their own board and system (8118525)
- release v0.11.60 — refresh internal changelog (2c5e407)
- system types: recognise Paperless-ngx (2c93d2f)
- dashboards: a board holds integrations and systems, not services (b839269)

## v0.11.59 — 2026-08-02

- release v0.11.59 — refresh internal changelog (0a87ddb)
- e2e: pick an rbac fixture service that has the signals it withholds (4c40d31)
- dashboards: pin a system, not just a service (3d1b0ba)
- systems: trust the server's health, and stop minting identical names (049d84d)

## v0.11.58 — 2026-08-02

- release v0.11.58 — refresh internal changelog (a67aabb)
- systems: a firing system check must show up everywhere it matters (bcb7197)

## v0.11.57 — 2026-08-02

- release v0.11.57 — refresh internal changelog (38c258c)
- errors: say what a failing check watches, and where to edit it (f3ad7fd)

## v0.11.56 — 2026-08-01

- release v0.11.56 — refresh internal changelog (fac5cfa)
- images: publish arm64 alongside amd64 (c2692c9)
- systems: a system watched only by its own checks isn't "quiet" (cf73c23)
- metrics: tell two rules on the same metric apart (3bb3375)

## v0.11.55 — 2026-07-31

- release v0.11.55 — refresh internal changelog (539a0d6)
- systems: pick a type from the catalog instead of typing its key (7d3d61a)
- health checks: bind one to a system (issue #13) (0fefa6f)

## v0.11.54 — 2026-07-31

- release v0.11.54 — refresh internal changelog (a8f6e7d)
- health checks: bind one to an integration, not just a service (7d8e5c7)
- health checks: the split-by preview counted the wrong noun (dd6830c)

## v0.11.53 — 2026-07-31

- trace drawer: pick the initial span in its own effect (e9bf1d3)
- release v0.11.53 — refresh internal changelog (a3a167d)
- integrations: an integration's numbers must describe the integration (3f91fcd)
- search: open a trace on the span that actually matched (e396551)
- search: "any (ok, warn, err)" was filtering to errors only (be8b4d2)

## v0.11.52 — 2026-07-30

- release v0.11.52 — refresh internal changelog (11887b7)
- test: follow the reports page behind its new tab strip (e41e3f6)
- filters: stop crashing on cells that aren't a secure context (56c9ba5)
- ui: the tab strips had no selected state, and a 402 read as a crash (0431258)

## v0.11.51 — 2026-07-29

- release v0.11.51 — refresh internal changelog (c3c1eca)
- retention: separate "who chose this" from "is it still running" (005fa7b)

## v0.11.50 — 2026-07-29

- release v0.11.50 — refresh internal changelog (e8211fc)
- advisor: cover the decision logic properly, and make CI actually evaluate (7260dac)
- release v0.11.50 — refresh internal changelog (5b242c4)
- readme: show the product (b46becd)
- e2e: an entitlement gate is not a schema failure (8f812a8)
- advisor: fix a scan-type bug and a plural, both found by running it for real (ddf9479)
- advisor: do not judge before the ledger is as old as the window (ecb0979)
- advisor: docs, the manual protocol, and the e2e gate (issue #1) (dcd4bfb)
- advisor: the Usage tab, the MCP tool, and the engagement signal that makes F1 honest (d949fe4)
- advisor: API, engine wiring, and the demand sources the guardrails depend on (ad31fa3)
- advisor: entitlement, suggestion store, and the T/F evaluators (issue #1) (0e8182b)
- seed: let SLUICIO_INGEST_URL redirect the seeder, since the default is a trap (b824bd8)
- seed: an EDI gateway where the message type, not the topology, is the integration boundary (a31b02e)
- e2e: give the filter-UI spec a budget bigger than its own waits (e52b38e)

## v0.11.49 — 2026-07-29

- release v0.11.49 — refresh internal changelog (d925a9b)
- mcp: say what each tool returns, and answer the transport spec properly (issue #8, WS1) (4193151)

## v0.11.48 — 2026-07-29

- release v0.11.48 — refresh internal changelog (67c7044)
- build: move to Node 24 LTS, and state the floor instead of implying it (8a4a0bc)

## v0.11.47 — 2026-07-28

- release v0.11.47 — refresh internal changelog (51e4610)
- api: shed a looping caller before it floods ClickHouse (issue #8, WS5) (7e8b425)

## v0.11.46 — 2026-07-28

- release v0.11.46 — refresh internal changelog (ee94470)
- audit: record the channel a change arrived through (issue #8, WS5) (dafded6)

## v0.11.45 — 2026-07-28

- release v0.11.45 — refresh internal changelog (f1ea733)
- system types: carry the docs link into the payload (issue #8, WS4) (08ce208)

## v0.11.44 — 2026-07-28

- release v0.11.44 — refresh internal changelog (1422515)
- mcp: sluicio_cell_brief, one call to orient (issue #8, WS4) (5234717)

## v0.11.43 — 2026-07-28

- release v0.11.43 — refresh internal changelog (08864da)
- runbooks: an editor field, and proof the text reaches the payload (085ca55)
- runbooks on alert rules and system types (issue #8, WS4) (5efc739)

## v0.11.42 — 2026-07-27

- release v0.11.42 — refresh internal changelog (a857269)
- proposals: run the expiry sweep, and cover the loop end to end (issue #8, WS2) (227ce87)
- proposals: the review inbox, and drop a tunable that never applied (issue #8, WS2) (312019c)
- proposals: apply path, HTTP surface, and the MCP propose tool (issue #8, WS2) (24f2a02)
- proposals: the safe-write primitive for agents (issue #8, WS2) (b59f4f2)
- mcp: annotate the catalogue as read-only (issue #8, WS1) (f45dfdc)
- Service detail: name the facets, and stop collapsing them to one (1fb610b)
- ci: authenticate the licence pre-check (0b2c0c9)
- e2e docs: mint the CI licence from the repo root (bd06373)
- e2e: make the Enterprise suite actually run, and fail when it can't (4e6662e)
- Telemetry Advisor P1: the demand ledger (3bc91f0)
- Notification templates: one channel at a time, Email and Slack on their own tabs (7ff9af2)
- helm: bundled databases run under restricted-v2; drop the anyuid advice (feec02f)
- helm: name the referenced Secrets in NOTES, document CE and CreateContainerConfigError (f693c65)
- feat(templates): start-from-default, schema-checked variables, side-by-side preview (36a5a80)
- feat(templates): variables that write themselves — {{ }} autocomplete, sample values, live preview (23416e5)
- feat(templates): CodeMirror editing for the template bodies (HTML highlighting, line numbers) (8ce3918)
- docs(testing): bring the manual use-case catalog current with everything shipped since v0.11.27 (4060a8e)

## v0.11.41 — 2026-07-25

- release v0.11.41 — refresh internal changelog (0020dc2)
- feat(helm): chart v0.3.0 — CI validation, optional NetworkPolicy/PDB, OCI publishing, appVersion v0.11.40 (#6) (4a91d93)
- test(integration): adapt UpsertServices call sites to the two-value signature (ee2964f)

## v0.11.40 — 2026-07-25

- release v0.11.40 — refresh internal changelog (fb5c714)
- feat(settings): organize System settings into General, Email, Security, Notification templates, and Announcements tabs (f0fee32)
- refactor(alerting): consolidate the cell email template into the org notification-templates set (74f8d23)
- perf(events): index the delivery ledger; state its 50-row/72h semantics in the UI (73c3074)
- feat(events): per-subscription delivery ledger — state, attempts, last error (2075833)
- fix(events): explain custom filter globs; link the drawer to channel management (e8c8371)
- test(e2e): widen usage-report heading waits under full-suite load (9d4ad38)
- feat(events): outbound event subscriptions — domain events to webhook destinations (#4) (a59c398)

## v0.11.39 — 2026-07-24

- release v0.11.39 — refresh internal changelog (9ce4864)
- fix(alerting): maintenance windows also suppress trace-completion firings (04c722c)

## v0.11.38 — 2026-07-24

- release v0.11.38 — refresh internal changelog (79c2c84)
- feat(system-types): WSO2 API Manager built-in type (d0ac7dd)
- feat(alerting): notification message templates — Slack + email, org→team override ladder (#5) (5625cb1)

## v0.11.37 — 2026-07-24

- release v0.11.37 — refresh internal changelog (18de250)
- refactor(announcements): one surface — cell-wide on Settings → System; org section removed (eed2623)

## v0.11.36 — 2026-07-24

- release v0.11.36 — refresh internal changelog (8640e59)
- fix(deps): bump golang.org/x/text to v0.39.0 (GO-2026-5970) (a7a681b)

## v0.11.35 — 2026-07-24

- release v0.11.35 — refresh internal changelog (72ebedc)
- feat(announcements): opt-in login-page banners for cell-wide announcements (1309a1f)

## v0.11.34 — 2026-07-21

- release v0.11.34 — refresh internal changelog (5715a59)
- feat(system-types): built-in rows link to their docs reference page (6468079)
- fix(system-types): whole row toggles the type detail, not just the tiny caret (e05651b)

## v0.11.33 — 2026-07-21

- release v0.11.33 — refresh internal changelog (a704643)
- feat(system-types): built-in Apache Kafka, Confluent Kafka, NATS, and Debezium types (76ae5e5)

## v0.11.32 — 2026-07-21

- release v0.11.32 — refresh internal changelog (e7e13ad)
- feat(mcp): sluicio_usage_report tool — the admin usage report over MCP (ff9b8b5)
- refactor: align the Go module path and residual naming with the Sluicio brand (e69d54e)
- ci(release): auto-draft a GitHub Release on every version tag, seeded from the changelog (a63ab0c)

## v0.11.31 — 2026-07-21

- release v0.11.31 — refresh internal changelog (cb66c2d)
- fix(dashboard): 'needs attention' surfaces unhealthy integrations before noisy-but-passing ones (88fbe23)
- fix(settings): Reports tab subtitle describes the usage report, not unbuilt email summaries (f1b5f12)

## v0.11.30 — 2026-07-20

- release v0.11.30 — refresh internal changelog (69f89d2)
- feat(reports): trim panel spans logs and traces — severity floors, drop/sample, integration guardrail (73df612)

## v0.11.29 — 2026-07-20

- release v0.11.29 — refresh internal changelog (0450328)
- feat(reports): cross-signal usage report with savings nudges; attribute-scoped trim; members Last active (8ffbf0a)
- test(e2e): de-flake two cross-worker races (shared admin prefs, transient required field) (51d8703)
- fix(helm,api): kind smoke-test findings — probe-able /healthz, verifiable non-root UIDs (2251af6)
- test(e2e): harden the two remaining release-verification flakes (b791551)
- test(e2e): fix the two release-verification failures on v0.11.28 (6b96882)

## v0.11.28 — 2026-07-16

- release v0.11.28 — refresh internal changelog (f99fad0)
- fix(messages): the 'payload' filter is labelled what it is — attribute (d85a119)
- feat(helm): production-ready cell chart for self-hosted EE (K8s/OpenShift) (ffff0f2)
- feat(system-types): shareable system types — portable YAML/JSON export + import (db8919f)

## v0.11.27 — 2026-07-16

- release v0.11.27 — refresh internal changelog (5394614)
- fix(messages): search findings — id fragments, observed error types, incomplete-row no-op, picker cross-narrowing (b02291e)
- fix(rbac): reject byte-identical duplicate group policies with 409 (b1c7313)
- fix(auth): header-less requests default to oldest-joined org, not alphabetical (fd53b26)
- feat(alerting): opt-in CloudEvents 1.0 format for webhook channels; outbound-events design (63d36dc)
- feat(alerting): opt-in HMAC request signing for webhook channels (51c8d06)
- fix(alerting): notification-channel hardening + live delivery validation for all four kinds (6f6e481)
- docs: canonical feature matrix — Community vs Enterprise, stable slugs (ce5f760)
- docs: OTelFlow-in-Sluicio integration design — saved, RBAC-scoped collector configs (e97e4f4)

## v0.11.26 — 2026-07-15

- release v0.11.26 — refresh internal changelog (35b7cec)
- test(e2e): dashboards×RBAC + alert-lifecycle suites; fix latent licensed-cell flakes (1dbc58c)
- feat(rbac): scoped service accounts — SAs join groups; org-wide is an explicit opt-in (6ffdb0f)
- docs(rbac): service-account scoping design — SAs as first-class group members (7300019)
- fix(rbac): metric-catalog honors the per-signal metrics tier; gap tests (3e5d607)
- docs(testing): RBAC coverage index — what runs on every tag, and the gaps (dc7077f)
- test(e2e): manual test protocol executed verbatim — group-granted visibility (7730578)
- release v0.11.25 — refresh internal changelog (071023a)

## v0.11.25 — 2026-07-15

- fix(ui): drop the stale Redoc caption on the API & MCP page (2078e9a)
- release v0.11.24 — refresh internal changelog (65703f0)

## v0.11.24 — 2026-07-15

- feat(api-docs): Scalar try-it reference + llms.txt, all embedded (3a56b1e)
- release v0.11.23 — refresh internal changelog (1a70e5e)

## v0.11.23 — 2026-07-14

- feat(mcp): logs search, metric series, integration detail, alert instances (caefc6f)
- chore(github): issue forms — user-story-shaped bugs and feature requests (e56ae08)
- release v0.11.22 — refresh internal changelog (b1cfcf2)

## v0.11.22 — 2026-07-14

- feat(ui): drop the redundant name filter from the Services toolbar (2cb4ac3)
- feat(logs): integration-scoped alert dialog offers an explicit health-impact choice (a040096)
- fix(logs): integration-scoped alert dialog offers only the integration's services (7349ed6)
- feat(logs): filters mirror into the URL — a filtered view is shareable (dd6fecf)
- release v0.11.21 — refresh internal changelog (288e207)

## v0.11.21 — 2026-07-14

- test(e2e): ce-upsell clicks the group row, not the removed name button (2419419)
- release v0.11.20 — refresh internal changelog (3f438b9)

## v0.11.20 — 2026-07-14

- feat(ui): group rows open the blade, matching the members pattern (68de404)
- fix(ui): member blade explains absent admin actions on your own account (a2ed534)
- feat(ui): member details blade; members table slimmed to what you scan for (9227173)
- fix(ui): settings tables and buttons cope with the narrower content pane (e0074d2)
- fix(ui): settings nav no longer wanders between tabs (cb10d04)
- feat(ui): settings page adopts the grouped left-nav design (f9465f6)
- feat(ui): env label lives only in the top nav, loud when non-prod (d6ea4a0)
- feat(ui): ingest-URL nudge links to System settings (5a103b2)
- fix(ui): alert bodies are prose, not flex items (dd526c3)
- docs: telemetry advisor + alert fatigue advisor design (draft for review) (189615d)
- release v0.11.19 — refresh internal changelog (da75ebc)

## v0.11.19 — 2026-07-13

- feat(templates): Azure Service Bus system type; split-by in template checks (9fe4af2)
- release v0.11.18 — refresh internal changelog (0686f8f)

## v0.11.18 — 2026-07-13

- fix(ui): system-types page renders trace checks; shared check formatter (46702f0)
- release v0.11.17 — refresh internal changelog (9205662)

## v0.11.17 — 2026-07-13

- feat(templates): transport-failure checks in the KrakenD template (a3f57dd)
- release v0.11.16 — refresh internal changelog (1e49200)

## v0.11.16 — 2026-07-13

- feat(ui+e2e): render trace-signal template checks; KrakenD template spec (86ecbb3)
- feat(templates): trace-signal checks in monitoring templates (db2c1b2)
- release v0.11.15 — refresh internal changelog (7d77cc3)

## v0.11.15 — 2026-07-13

- feat(ui): 5xx-as-errors toggle + attribute conditions on failed-trace alerts (8dfea7e)
- feat(alerting): attribute predicates on failed-trace rules (e03bc35)
- feat(ingest): opt-in 5xx→Error span-status normalization (5030a94)
- test(ingest): pin both ingest-key auth headers (d40aabc)
- release v0.11.14 — refresh internal changelog (c6568fc)

## v0.11.14 — 2026-07-12

- feat(ui): reorderable integration columns, layout persisted per user (fc8eee6)
- feat(api): per-user UI preferences (GET/PUT /api/v1/me/preferences/{key}) (d06891a)
- release v0.11.13 — refresh internal changelog (e85bf04)

## v0.11.13 — 2026-07-12

- feat(ui): trace page breadcrumb above the title; selected log mirrored into the URL (fd8dd04)
- release v0.11.12 — refresh internal changelog (b9ea357)

## v0.11.12 — 2026-07-12

- feat(ui): full trace view gets an origin-aware breadcrumb (dc97bfc)
- fix(rbac): equals matchers vetted against managed scope, not just the catalog (450dbd1)
- fix(audit): canonicalize metadata before hashing — struct payloads broke verify (1ad5bc7)
- release v0.11.11 — refresh internal changelog (b95b466)

## v0.11.11 — 2026-07-12

- feat(logs): trace ids open the trace blade in place (c35849e)
- fix(demo): seeded logs carry trace context from real spans (6bebd00)
- release v0.11.10 — refresh internal changelog (2d006dd)

## v0.11.10 — 2026-07-12

- feat(ui): multi-level grouping of integrations by metadata fields (6a172aa)
- release v0.11.9 — refresh internal changelog (3ab2ec3)

## v0.11.9 — 2026-07-12

- fix(ui): typeahead focuses on open and stays inside the viewport (dff7d22)
- release v0.11.8 — refresh internal changelog (5798154)

## v0.11.8 — 2026-07-12

- feat(ui): metadata fields are part of integration creation (748451a)
- release v0.11.7 — refresh internal changelog (0e24166)

## v0.11.7 — 2026-07-12

- feat(config): SLUICIO_INGEST_URL — deployment-managed ingest endpoint (182cadd)
- feat(ui): metadata editor on integration Settings; ingest-URL nudge (1ffacba)
- release v0.11.6 — refresh internal changelog (173a18b)

## v0.11.6 — 2026-07-10

- feat(config): export & import org configuration between environments (7f03b68)
- docs: config export/import design (draft for review) (c9f82b3)
- fix(demo): snapshot size guard is overridable (MIN_GOLDEN_BYTES) (8ac3876)
- fix(demo): snapshot/reseed auto-detect podman; note cronie on Fedora (307405a)
- docs(deploy): fix stale pre-rename clone URL (ROMA-IT-AB/Sluicio → SLUICIO/sluicio-app) (9c612ec)
- docs(security): the whole audit log is Enterprise, not just verification (89c2357)
- Change security contact email to support@sluicio.com (96641dc)
- chore: toolchain go1.25.12 — crypto/tls fix (GO-2026-5856) (cfeeea3)
- feat(alerting): notification links highlight the exact alert in the app (7562cea)
- docs: security principles — verifiable claims, code-linked (5f8f58a)
- chore: design guidelines move to the private brand repo (f0ef46d)
- docs: Sluicio design guidelines — brand, tokens, idioms, voice (4280c67)
- docs: add service-facets user guide (ea342e1)
- release v0.11.5 — refresh internal changelog (d0b67ce)

## v0.11.5 — 2026-07-07

- fix(ui): cell-wide announcements move to Settings → System settings (98aa940)
- chore: remove marketing content from the product repo (9c38514)
- release v0.11.4 — refresh internal changelog (3dc1306)

## v0.11.4 — 2026-07-07

- feat(ui): template checks visible + editable in place; version links to GitHub (7757259)
- fix(e2e): settle install state in global setup; scope nav assertions (448fff9)
- feat(alerting): announcements + maintenance windows (fef5adc)
- docs: maintenance windows + announcements design (draft for review) (0b4bab4)
- chore(ui): drop the top-bar theme toggle — Account → Theme owns it (fa539f7)
- fix(ui): setup skip link says what it means — the seeded admin account (0522876)
- feat(ui): sidebar can be hidden from the top bar (850f06c)
- feat(auth): first-run screen — create your admin account (4913140)
- docs+ui: quickstart is clone-free and the seeded admin is discoverable (24a9a1f)
- fix(e2e): EE feature suite skips itself on unlicensed cells (778790a)
- fix(e2e): group-policy upsell test creates its own group (c6c9599)
- release v0.11.3 — refresh internal changelog (44d1a6e)

## v0.11.3 — 2026-07-07

- feat(demo): pre-fill the login form with the demo credentials (67daff5)
- fix(ui): alert email template matches its sibling System-tab sections (566929d)

## v0.11.2 — 2026-07-06

- fix(ui): drop unfinished controls from the message filter editor (2af267a)
- docs(ui): SSO has shipped — drop '(soon)' from the org settings subtitle (fc79493)
- fix(ui): resource-sharing card shows a CE upsell instead of vanishing (cb61ecd)
- fix(ui): gate the group policy editor on rbac_advanced (upsell, not 402) (c2916fe)
- docs(readme): add the hosted demo (demo.sluicio.com) near the top (9c2173d)
- chore: remove marketing drafts from the product repo (ab73fbf)
- feat(auth): admins set a temporary password + force change on next login (bd81b1d)
- docs(license): Apache-2.0 appendix copyright is ROMA IT AB (a8ab5f0)
- docs: finish the Integration Monitor → Sluicio rename; licensing doc knows all three tiers (296d16d)
- docs(license): FSL Licensed Work is Sluicio, (c) ROMA IT AB (a6564a1)
- fix(ci): lint clean + demo e2e tolerates unlicensed cells (4903aa1)
- chore: gitleaks config — e2e fixture passwords are test data (3427825)

## v0.11.1 — 2026-07-06

- docs: DCO sign-off + explicit inbound Apache-2.0 for contributions (c6ec190)
- docs(license): fix the CE licensing story for the real legal entity (e1c717b)
- docs(ee): add ROMA IT AB organisationsnummer to the SEL (42b8e2b)
- docs(ee): SEL identifies ROMA IT AB as a Swedish aktiebolag + support contact (5a1b8c5)
- docs(ee): finalize SEL v1.0 — proper CE grant, redistribution, Swedish law (cd08860)
- docs(ee): SEL v1.0 is final — drop the draft disclaimer (83b090c)
- Sluicio v0.11.1 — initial public release (fa2c4a6)

## v0.11.1 addendum — folded into the initial public commit

- fix(demo): drop demo-seeder depends_on so podman-compose can bring it up

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

