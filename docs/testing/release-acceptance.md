<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Release acceptance

The gate walked **before tagging a release** (e.g. v0.5.2). Its job is to
**verify the documented use cases** — not to chase coverage. Fast
correctness checks (build, vet, unit, lint, typecheck, component,
integration) already ran on every push via
[ci.yml](../../.github/workflows/ci.yml); this is the use-case pass that
runs at release time, not per build.

Target version: `vX.Y.Z`  ·  Date: `YYYY-MM-DD`  ·  Signed off by: `____`

## 1. Automated use-case suite

- [ ] **Run `release-verification`** — trigger
      [the workflow](../../.github/workflows/release-verification.yml)
      on the tag (automatic on `v*`) or by hand from the Actions tab. It
      stands up a seeded stack and runs the full Playwright use-case
      suite. Confirm green; review the uploaded Playwright report.
- [ ] Or locally: `make dev-up && make seed-traces && make e2e`.

## 2. Walk the use-case catalog

For each area, walk its `Manual` cases and confirm its `Automated` cases
are green. Tick the area once every case in it passes. Full catalog:
[protocols/](protocols/).

- [ ] **[Login](protocols/auth-login.md)** — sign in / out, bad creds, session.
- [ ] **[Account, password & MFA](protocols/auth-account-mfa.md)** — profile, change/reset password, MFA enroll/login/disable.
- [ ] **[Orgs, access & tenancy](protocols/orgs-access-tenancy.md)** — members, roles, tokens, groups/policies, ingest keys, **tenant isolation**.
- [ ] **[Telemetry ingest](protocols/telemetry-ingest.md)** — OTLP traces/logs/metrics in; key auth.
- [ ] **[Health & services](protocols/health-services.md)** — health status, service detail, clear-errors, facets/overrides/mappings, tags, metadata.
- [ ] **[Traces, logs, metrics, search, topology](protocols/traces-logs-metrics.md)** — waterfall, completion/SLA, log & metric filters, search, flow graph.
- [ ] **[Integrations & messages](protocols/integrations-messages.md)** — integration CRUD, matcher routing, messages/views/CSV, errors/acks, schemas, maps.
- [ ] **[Alerts & notifications](protocols/alerts-notifications.md)** — metric/log/trace/pushed rules, preview, ack/resolve, channels & profiles.
- [ ] **[Platform settings](protocols/platform-settings.md)** — tags, metadata fields, dashboards, retention/SMTP/system/security, license, audit.
- [ ] **[Cell operator](protocols/operator.md)** — operator gating, org lifecycle, cross-org member assignment, operator promote/demote guard, cell-wide-settings gating.

## 3. Cross-cutting must-walk (every release, even if automated)

- [ ] **Tenant isolation** — org A never sees org B's data on any surface
      ([orgs-access-tenancy.md](protocols/orgs-access-tenancy.md) Case 7).
      Automated by `api/tenant_isolation_integration_test.go`; still walk it.
- [ ] **Authz gates** — viewer/editor/admin and the operator gate return
      the right 200/403 end-to-end
      (`api/middleware/require_integration_test.go`); teams/policy
      visibility resolves (`identity/groups_integration_test.go`).
- [ ] **Operator surface** — a fresh cell auto-promotes the admin to
      operator; the last operator can't be demoted; cell-wide settings
      (SMTP/retention/security) are operator-only ([operator.md](protocols/operator.md)).
- [ ] **Telemetry round-trip** — `make seed-traces`, then traces/logs/
      metrics appear under a service within seconds.
- [ ] **Upgrade** — migrations apply cleanly over the previous release's
      data volume (no manual SQL). Incl. `0057_operator` over a populated DB.
- [ ] **Community vs EE gating** — EE-only features (notification
      profiles, long retention, MFA policy, audit log) are denied/hidden
      without a license and exposed with one.

## 4. Release mechanics & GHCR image smoke test

Before tagging, verify the **built images** — not just the source tree —
since that's what ships to `ghcr.io/sluicio/*`.

- [ ] **Per-build gates green on the tag commit** — `ci.yml`: build, vet,
      `go test ./...`, `make test-integration`, frontend
      typecheck/lint/test/build, license/SPDX, security
      (govulncheck + gitleaks + npm-audit). Vuln baseline is **0**.
- [ ] **OpenAPI in sync** — `make openapi-check` passes (no drift; the
      operator routes are in `openapi_gen.json`).
- [ ] **Images build + push** — `release-images.yml` pushes
      `cell-api / cell-ingest / controlplane / frontend` to GHCR; Trivy
      scan job reviewed (report-only).
- [ ] **Pull-and-run the pushed images** (not a local `go build`):
      `podman compose` (or the release compose) against the **published**
      tags. Then:
  - [ ] cell-api `/healthz` (or `/readyz`) returns 200; migrations
        (incl. `0057`) applied on first boot; the seeded admin is an operator.
  - [ ] Frontend loads, login works, the app shell renders.
  - [ ] `make seed-traces` → data appears (telemetry round-trip on the image).
  - [ ] A public status badge renders (200 SVG) for an opted-in entity;
        404 for a non-public one.
- [ ] **Secrets check** — no license key / SMTP password / client secret
      in the image or repo (`.env` is gitignored; secrets are encrypted
      at rest).
- [ ] **GHCR packages are Public** (at/after the OSS flip) for the four
      images; `docker pull` works unauthenticated.
- [ ] `CHANGELOG.md` updated (`make changelog`).
- [ ] Version tag pushed; `ci.yml` green on the tag; `release-verification` green.

## Keeping it honest

When you ship a new use case, add it to the relevant protocol in
[protocols/](protocols/) (and its Playwright spec if you automate it) in
the **same change**. A use case that isn't in a protocol won't be
verified at release — so the catalog is the contract.
