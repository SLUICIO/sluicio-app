<!-- SPDX-License-Identifier: Apache-2.0 -->

# End-to-end tests

Playwright tests that drive the real Sluicio UI against a live stack.
Each spec is the executable form of a manual protocol in
[`docs/testing/protocols/`](../docs/testing/protocols/) — when you change a
flow, update both.

## What it talks to

```
Playwright (chromium)
   └─ http://localhost:5173   Vite dev server (started automatically)
        └─ /api  →  http://localhost:8081   cell-api  ┐
                                                       ├─ Postgres :5433
                                                       └─ ClickHouse :8123
```

The backend (cell-api + its data stores) must be up. Playwright starts the
frontend itself.

## Run locally

```bash
# 1. backend stack (Postgres, ClickHouse, cell-api, cell-ingest)
make dev-up

# 2. install once
cd e2e
npm ci
npm run install:browsers

# 3. run
npm test            # headless
npm run test:headed # watch it drive a browser
npm run test:ui     # Playwright's interactive UI
npm run report      # open the last HTML report
```

Or from the repo root: `make e2e` (assumes the stack is up) or
`make e2e-up` (brings the stack up, runs, leaves it up).

## Credentials

Tests sign in as the seed admin every fresh cell ships with:
`admin@sluicio.local` / `admin`. Override for a non-seed environment:

```bash
E2E_ADMIN_EMAIL=me@corp.com E2E_ADMIN_PASSWORD=… npm test
```

## Point at a different environment

```bash
# Test an already-served frontend (skips starting Vite):
E2E_BASE_URL=https://cell.example.com E2E_API_URL=https://cell.example.com npm test
```

### Against a containerised stack (compose / the throwaway cell)

`E2E_BASE_URL` alone is **not enough** when cell-api runs in a container.
Several specs host an HTTP sink inside the Playwright process and assert
that cell-api delivered to it; from inside a container `localhost` is the
container, so those tests fail in seconds on `testChannel(...).ok()`.
`E2E_INGEST_URL` matters just as much — it defaults to **4318**, which is
the normal local dev cell, so leaving it unset points telemetry at the
wrong cell rather than failing.

```bash
E2E_BASE_URL=http://localhost:8090 \
E2E_INGEST_URL=http://localhost:4319 \
E2E_SINK_HOST=host.containers.internal \
E2E_MAILPIT_API=http://localhost:8025 \
E2E_SMTP_HOST=host.containers.internal \
E2E_SMTP_PORT=1025 \
npm test
```

Seed the cell first (`go run ./services/cell-ingest/cmd/seed-traces
--endpoint http://localhost:4319/v1/traces …`) or the metrics-attribute
and usage-report specs skip for want of data.

| Variable | Default | Needed when |
|---|---|---|
| `E2E_BASE_URL` | `http://localhost:5173` | Frontend is already served (skips Vite) |
| `E2E_INGEST_URL` | `http://localhost:4318` | Ingest is not on the default port |
| `E2E_SINK_HOST` | `localhost` | cell-api is containerised — use `host.containers.internal` |
| `E2E_MAILPIT_API` / `E2E_SMTP_*` | `…:8025` / `:1025` | Email specs against a non-default mailpit |
| `E2E_EXPECT_EE` | unset | See below |

## Enterprise coverage and `E2E_EXPECT_EE`

Roughly 26 tests need a licensed cell: the whole audit log, the EE half
of RBAC (expression policies, per-signal visibility, scoped manage,
resource sharing), team dashboards and the MFA policy. Against an
unlicensed cell they skip, because the features genuinely aren't there.

That is safe but treacherous: a skip is indistinguishable from a pass in
the summary, and for a long stretch these tests ran in **no** environment
at all — unlicensed locally, and CI supplied no key either. The suite was
green and said nothing whatsoever about Enterprise.

So set `E2E_EXPECT_EE=1` wherever a licence is supposed to exist. Every
entitlement gate then **fails** instead of skipping, naming the missing
entitlement. `release-verification` sets it automatically whenever the
`E2E_LICENSE_KEY` secret is present.

```bash
# Fails loudly if this cell isn't actually licensed:
E2E_BASE_URL=… E2E_EXPECT_EE=1 npm test
```

### The CI licence

`release-verification` reads the repository secret **`E2E_LICENSE_KEY`**
and injects it as `SLUICIO_LICENSE_KEY` on the cell-api start step only —
deliberately not job-wide, so it is absent while `npm ci` / `go mod
download` execute third-party code.

Mint a dedicated, short-lived token rather than reusing a customer key
(the signing tool is in-repo; only the private key is kept out of it):

```bash
go run ./ee/cmd/sluicio-license mint \
  -key /path/to/sluicio_license_ed25519.key \
  -customer ci-e2e \
  -days 90
```

`-features` already defaults to all five entitlements, which is what the
EE specs assert. Paste the printed token into the repository secret
`E2E_LICENSE_KEY` (Settings → Secrets and variables → Actions). Verify
one with `go run ./ee/cmd/sluicio-license inspect`.

Licences are offline Ed25519 tokens with **no revocation list** — a leak
can only be outlived, not cancelled, so the expiry *is* the containment.
Rotate before it lapses; the 14-day grace window means an expired key
keeps EE alive briefly and would otherwise mask the lapse. If the key
goes missing or stops parsing, the run fails at the "Confirm the cell
picked up the licence" step rather than reverting to 26 silent skips.

## Layout

| File | Purpose |
|------|---------|
| `playwright.config.ts` | Base URL, the auto-started Vite server, reporters, retries. |
| `tests/fixtures.ts`    | Shared constants + the `logIn()` helper. |
| `tests/auth.spec.ts`   | Login flow — mirrors `auth-login.md`. |
| `tests/smoke.spec.ts`  | Stack-alive / routes-render checks. |
