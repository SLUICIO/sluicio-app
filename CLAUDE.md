<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# CLAUDE.md

Guidance for Claude Code (and anyone else) working in this repository. The aim is one product that reads as one product: every feature built the way the features around it were built.

## What this is

Sluicio monitors system integrations from their OpenTelemetry telemetry.

| Path | What lives there |
|---|---|
| `services/cell-api` | Go API: integrations, alerting, access, settings. Postgres migrations in `internal/migrations/sql`. |
| `services/cell-ingest` | OTLP/HTTP ingest into ClickHouse; `cmd/seed-traces` is the synthetic estate (`-estate full` for development and tests, `-estate demo` for the public demo). |
| `pkg/clickhouse` | ClickHouse schema; migrations in `migrations/`, applied by cell-ingest. |
| `pkg/mcp` | The MCP tools and their output schemas. |
| `frontend` | React + TypeScript (Vite). |
| `e2e` | Playwright suite. |
| `deploy` | Quickstart compose, server compose, Helm chart, demo overlay. |
| `ee` | The two proprietary parts (audit persistence, license minting). The core must build and run without it. |

The public documentation site is a separate repository; user-facing changes need a docs change there too.

## Before writing anything: find the existing pattern

Most inconsistency comes from building a new control, helper or query shape when one already exists. Before adding UI, an endpoint, a store function or a test, find the nearest feature that does the same kind of thing and do it the same way. If the existing pattern is wrong for the case, extend it (an option on the shared component, a parameter on the shared helper) rather than writing a parallel one, and say so in the commit.

## Frontend conventions

- **Picking a value from many, or adding a value to a list:** `components/SearchableSelect.tsx`. `withoutAll` drops the "all" entry; `creatable` with `createLabel` offers a value not seen yet. Never a native `<datalist>`: its browser-drawn dropdown looks like nothing else in the app.
- **Size follows the neighbours.** Inside a form of text fields use `variant="field"` (the `search__input` size); beside buttons or in a toolbar use the default (`toolbar__select`, button size). Never mix the two sizes in one row.
- **Short fixed choices:** a native `<select className="search__input">`, like `IntegrationProfileSelect`.
- **A list of values:** chips (`badge`) with a × each, beside the picker, and one primary Save.
- **A settings area with its own Save is a tab,** not a card stacked under another tab's form. System settings uses `SYSTEM_SUBTABS` and `svc-tab`: a muted intro paragraph, `maxWidth` around 720, then the form.
- **Integration pages:** sections are `rounded-lg border bg-surface-2` with a header strip (`border-b border-border px-4 py-3`, an `h2` and a muted line). Tabs come from `IntegrationTabs`; links between an integration's tabs keep `?env=` through `envQuery`.
- **Popovers** are portaled to `document.body` and positioned from the trigger (see `SearchableSelect`, `TagPicker`), so cards with `overflow: hidden` and drawers do not clip them.
- **Self-hosted cells are often plain HTTP.** Secure-context-only browser APIs (`crypto.randomUUID`, the async clipboard API) do not exist there; use `lib/uid.ts` and the existing fallbacks.
- **Check it in a browser.** Run the change, take a screenshot of the new control next to its neighbours, and compare before calling it done. Type-checking proves the code compiles, not that it fits.

## Backend conventions

- **An integration is a slice, not a union of services.** Its members plus its conditions select its telemetry. Any per-integration number (status, counts, latency, attributes) is read from that slice, never from the member services' whole traffic, because two integrations can share a service.
- **Enumerate every reader.** A change to what an integration (or any scope) reads must reach every place that reads it, or one surface silently disagrees with the rest. For an integration that is at least: the list and detail handlers, the integration's tabs (`integrationFilter`, `pickerAccess`), grant-scoped access (`integrationSliceScope`), the alert engine's adapters in `cmd/cell-api/main.go` and the check preview, the MCP tools, the state export and configuration transfer. Grep for the existing helper and re-run the issue's own reproduction.
- **Environments travel in the context.** `?env=` (repeatable) becomes `envctx` in middleware; reads narrow through `withRequestEnvironment` / `store.WithEnvironments`. Do not filter on the environment by hand. "Production" is a role (`internal/envrole`), never a hard-coded name.
- **Metrics and logs of an integration** go through `integrationSignalScope` (members plus its metric or log sources). Sources are evidence, never members: they stay out of the flow, the member list, message counts and the one-trace gate.
- **Access fails closed.** An empty `ServiceIn` reads downstream as "no filter"; when a caller's allowed services intersect to nothing, return empty access explicitly rather than an empty list.
- **Postgres migrations:** the next number in `services/cell-api/internal/migrations/sql`, with both `.up.sql` and `.down.sql`, and a comment saying why the table or column exists.
- **ClickHouse migrations:** a new column needs a typed `DEFAULT` (`CAST([], 'Array(String)')`, not `[]`), because parts written before the migration are read through it. Statements are split on `;` followed by a newline. Extend `TestReadsAcrossUpgrade` when the shape of stored data changes; tests against a fresh database cannot catch this.
- **New or changed routes:** run `make openapi`. CI runs `make openapi-check`.
- **A feature is not done until** it is covered, where it applies, by configuration transfer (export and import), the MCP tools and their output schemas, the integration detail response, and the docs site.

## Testing

| What | Command |
|---|---|
| Go unit tests | `make test` (or `go test ./services/... ./pkg/...`) |
| Go vet | `make lint` |
| Postgres integration tests | `make test-integration` (testcontainers; finds the Podman socket itself) |
| ClickHouse live probes | `go test -tags liveprobe` with the `*_PROBE_CH` variable the test names, against a local ClickHouse |
| Frontend | `cd frontend && npm run typecheck && npm run lint && npm test` |
| API spec | `make openapi-check` |
| End to end | `cd e2e && npm test`, with every `E2E_*` variable from `e2e/README.md` set. `E2E_INGEST_URL` defaults to port 4318, the local dev cell, so set it explicitly or the test telemetry lands in the wrong cell. |

Add a test with each change, in the style of the tests beside it: a unit test for pure logic, an integration test for SQL against Postgres, a live probe for ClickHouse query shapes, Vitest for frontend helpers. Synthetic data that a feature depends on is asserted, not described (see `seed-traces/*_test.go`).

## Writing style

These apply to code comments, commit messages, docs, release notes and UI text alike.

- No em dashes. Use a plain hyphen, a comma or a colon.
- Markdown paragraphs are one line each; do not hard-wrap prose.
- Comments say why, not what, and match the density of the surrounding code.
- New files carry the SPDX header of their neighbours (`FSL-1.1-Apache-2.0` in the open core).
- UI text is short, concrete and says what happens ("Declare “staging”", not "Submit").

## Commits, releases and design

- Conventional commits (`feat(scope): …`, `fix(scope): …`) on `main`, at logical checkpoints, with a body that explains the why.
- No AI attribution in commits, release notes, issues or pull requests: no "Generated with" lines, no co-author trailers.
- Enhancement designs are written as GitHub issues (label `enhancement`), not as committed design documents.
- Ask before pushing to the remote or commenting on an issue or pull request.
- A release: `git tag vX.Y.Z`, `make changelog`, commit the changelog, move the tag onto that commit, push `main` and the tag, wait for the release workflows (ci, release-images, release-verification, mirror), then rewrite the draft GitHub release into user-facing notes and publish it. Images alone do not make a release.

## This repository is public

Keep private infrastructure out of code, comments, commits, issues and docs: hostnames and addresses of real deployments, credentials and keys, customer names, and commercial or hosted-platform plans. Describe a deployment option in neutral terms ("an instance run by a platform on behalf of someone else") rather than by product or business arrangement.
