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

## Layout

| File | Purpose |
|------|---------|
| `playwright.config.ts` | Base URL, the auto-started Vite server, reporters, retries. |
| `tests/fixtures.ts`    | Shared constants + the `logIn()` helper. |
| `tests/auth.spec.ts`   | Login flow — mirrors `auth-login.md`. |
| `tests/smoke.spec.ts`  | Stack-alive / routes-render checks. |
