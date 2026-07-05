<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Testing & quality — how we work

This folder is the home of Sluicio's **test protocols**: structured,
repeatable test cases used for both **manual** verification (a human
walks the steps before a release) and **automated** verification (the
same steps encoded as code that runs in CI).

The guiding idea: **write a test case once, in one format, and reuse
it.** A manual protocol here and its automated counterpart share the
same case numbers, so they can never quietly drift apart.

## Two cadences: per-build vs per-release

Quality runs at **two cadences**, on purpose:

- **Per build (every push / PR)** — fast correctness gates that catch
  regressions early: build, vet, unit, API, integration, component,
  lint, typecheck. These run in [ci.yml](../../.github/workflows/ci.yml)
  and must stay green and fast.
- **Per release (on a `v*` tag / manual dispatch)** — the **use-case
  verification**: the full Playwright suite against a seeded stack
  ([release-verification.yml](../../.github/workflows/release-verification.yml))
  **plus** a human walk of the [use-case catalog](protocols/) driven by
  [release-acceptance.md](release-acceptance.md). This is where we
  verify the documented use cases — not on every build.

The point isn't coverage percentage; it's that **every documented use
case is verified before a release ships**.

## The layers

| Layer | Tool | Where | Cadence |
|-------|------|-------|---------|
| **Unit** (pure logic) | `go test`, table-driven | `services/*/internal/**/*_test.go`, `pkg/**` | per build |
| **API / handler** | `go test` + `httptest` | `services/cell-api/internal/api/*_test.go` | per build |
| **Integration** (real Postgres via testcontainers) | `go test -tags integration` (`make test-integration`) | `//go:build integration` files | per build |
| **Component** (React) | Vitest + Testing Library | `frontend/src/**/*.{test,spec}.{ts,tsx}` (`cd frontend && npm test`) | per build |
| **Use-case E2E** | **Playwright** | [`/e2e`](../../e2e) | **per release** |
| **Use-case catalog** (manual + automatable) | this folder | [`protocols/`](protocols/) | **per release** |

Integration tests use
[testcontainers-go](https://golang.testcontainers.org/) to spin up a
throwaway Postgres; locally they need Docker or Podman (`make
test-integration` auto-detects the Podman socket on macOS).

## Manual vs automated — when to use which

- **Automate** anything stable and repeatable: login, CRUD on a
  resource, a matcher rule that routes a message, a health
  calculation. If it has a deterministic expected result, it belongs
  in code.
- **Keep manual** the things automation can't yet judge cheaply:
  visual/layout correctness, a brand-new feature still in flux,
  exploratory testing, and the final pre-release smoke on a real
  deployment. Write these as protocols here first; promote the stable
  ones to Playwright later.

Every manual protocol notes its automation status (`Automated`,
`Partially automated`, `Manual only`) so you know whether walking it by
hand is still necessary.

## Folder contents

| File | Purpose |
|------|---------|
| [`protocols/`](protocols/) | The **use-case catalog** — every documented use case, one file per area. Start at [`protocols/README.md`](protocols/README.md). |
| [`release-acceptance.md`](release-acceptance.md) | The per-release gate that walks the whole catalog. |
| [`TEMPLATE.md`](TEMPLATE.md) | Copy this to start a new protocol. |
| [`protocols/auth-login.md`](protocols/auth-login.md) | Worked example, fully automated by [`e2e/tests/auth.spec.ts`](../../e2e/tests/auth.spec.ts). |

## Definition of Done (per change)

A change is "done" when:

1. **New logic has a unit test.** Pure functions get table-driven Go
   tests next to the code.
2. **A bug fix ships with a failing-test-first regression.** Reproduce
   the bug in a test, then fix it, so it can't come back.
3. **Gates are green:** `go vet`, `go test`, frontend `typecheck` +
   `lint` + `build` (the per-build gates).
4. **The matching use case is documented.** If you changed or added a
   user-visible flow, update its case in [`protocols/`](protocols/) (and
   its Playwright spec if automated) **in the same change** — an
   undocumented use case won't be verified at release.
5. **UI changes are verified live**, not assumed — use the `/verify`
   skill or the preview tools to observe the change in a browser.

## Running everything

```bash
# per-build gates
make test          # all Go unit + API tests
make test-integration  # integration tests (needs Docker/Podman)
make lint          # go vet across services
( cd frontend && npm run typecheck && npm run lint && npm test )

# per-release use-case verification
make seed-traces   # give the surfaces data to show
make e2e-up        # bring up the stack + run the Playwright use-case suite
```

[ci.yml](../../.github/workflows/ci.yml) runs the per-build gates on
every push/PR. [release-verification.yml](../../.github/workflows/release-verification.yml)
runs the use-case E2E suite on a `v*` tag or manual dispatch.

## Roadmap

In priority order:

1. **Automate more catalog use cases** — convert high-value `Manual`/
   `Partial` cases in [`protocols/`](protocols/) into Playwright specs
   that run in `release-verification` (matcher routing, facet overrides,
   alert lifecycle, tenant isolation are the highest-value).
2. **ClickHouse integration tests** — extend the testcontainers pattern
   to a ClickHouse-backed store so the telemetry read path is exercised
   against a real CH.
3. **Seeded fixtures for the E2E stack** — richer, deterministic seed
   data (multi-service traces, a second org) so data-dependent use cases
   can be asserted, not just rendered.

> Explicitly **not** chasing coverage percentage — the goal is that
> every documented use case is verified each release.
>
> Done: per-build gates (unit/API/integration/component) + the
> use-case catalog + the per-release E2E workflow. CI was red from
> v0.3.1 until 2026-06-20 — keep it green; a red gate gates nothing.
