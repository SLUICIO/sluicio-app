<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Authentication — local password login

| Field | Value |
|-------|-------|
| **Area** | Authentication (native email + password) |
| **Owner** | Robert Mayer |
| **Automation status** | Automated |
| **Automated by** | [`e2e/tests/auth.spec.ts`](../../../e2e/tests/auth.spec.ts) |
| **Last reviewed** | 2026-06-20 |

This is the worked example that pairs a manual protocol with its
Playwright spec. Each case number below matches a `test()` in the spec.

## Preconditions

- Local stack up: `make dev-up` (Postgres, ClickHouse, cell-api,
  cell-ingest).
- A **fresh** cell — cell-api seeds the admin account on first boot
  (`admin@sluicio.local` / `admin`, see
  `services/cell-api/cmd/cell-api/main.go`).
- Frontend reachable at <http://localhost:5173> (`make frontend-dev`,
  or Playwright starts it).

## Cases

### Case 1 — Sign-in form shows when logged out

| | |
|--|--|
| **Goal** | An unauthenticated visitor sees the login form. |
| **Steps** | 1. Open <http://localhost:5173> in a clean session. |
| **Expected** | "Sign in to Sluicio" heading, Email + Password fields, and a "Sign in" button are visible. |
| **Automated** | Yes → "shows the sign-in form when not logged in" |

### Case 2 — Valid login lands on Health

| | |
|--|--|
| **Goal** | Correct seed-admin credentials authenticate and route to the app. |
| **Steps** | 1. Enter `admin@sluicio.local` / `admin`.<br>2. Click **Sign in**. |
| **Expected** | URL becomes `/health`; the login card is gone; the app shell renders. |
| **Automated** | Yes → "logs in with the seed admin and lands on Health" |

### Case 3 — Wrong password is rejected

| | |
|--|--|
| **Goal** | Bad credentials fail closed with a generic message (no user/password distinction leaked). |
| **Steps** | 1. Enter `admin@sluicio.local` / `definitely-wrong`.<br>2. Click **Sign in**. |
| **Expected** | "Invalid email or password." is shown; still on the login page; no session created. |
| **Automated** | Yes → "rejects an invalid password" |

### Case 4 — Session survives a reload

| | |
|--|--|
| **Goal** | The session cookie persists; a reload doesn't bounce the user back to login. |
| **Steps** | 1. Log in (Case 2).<br>2. Reload the page. |
| **Expected** | Still on `/health`; login form does not reappear. |
| **Automated** | Yes → "keeps the session across a reload" |

## Notes

- **MFA:** the seed admin has no second factor, so login is single-step.
  Accounts with MFA enabled get a pending token and a second `/auth/mfa-verify`
  step — that path is **not yet** covered here (TODO: separate protocol +
  spec once a deterministic TOTP fixture exists).
- The "ships with a default admin account" hint on the login page only
  shows while the install is *fresh* (no user has ever logged in); it
  disappears after the first successful sign-in. Don't assert on it.
