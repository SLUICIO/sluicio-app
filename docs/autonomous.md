<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Autonomous issue worker

Sluicio has an opt-in workflow where Claude Code picks up well-scoped
GitHub issues, implements them, and opens a PR for human review — no
chat interaction needed. The intent is to let day-to-day "small thing
that needs doing" tickets land while you focus on harder work.

This document is the contract: what the labels mean, what the worker
will and won't do, and how to opt out.

## TL;DR

1. File a GitHub issue. Make the body **self-contained** — what to
   build, where it lives in the codebase, how to verify it works.
2. When the issue is spec-complete, label it `auto-ready`.
3. A scheduled Claude run picks the oldest `auto-ready` issue, swaps
   the label to `auto-in-progress`, implements the change in a
   feature branch, runs the full build + smoke gate, and opens a PR
   with `Closes #N`.
4. You review and merge. If the PR is wrong, comment on it; the next
   autonomous run treats it as a normal PR and can iterate.

## Labels

| Label                  | Meaning                                                                 |
|------------------------|-------------------------------------------------------------------------|
| `auto-ready`           | Issue body is spec-complete; safe for autonomous implementation         |
| `auto-in-progress`     | A run is currently working on this — do not edit the issue or branch   |
| `auto-in-review`       | Autonomous PR is open and linked; waiting for human review              |
| `auto-skip`            | Don't touch this autonomously even if it looks ready                    |
| `needs-clarification`  | The worker started, found the spec ambiguous, and stopped               |

The label state is the queue. Anything *not* labelled `auto-ready` is
invisible to the worker. Removing `auto-ready` cancels a pick before
the next run.

## What "spec-complete" actually means

The worker has no humans to ask between firings. A good issue tells
it everything it needs:

- **What** — the user-visible change in one or two sentences.
- **Where** — pointers to the relevant files / packages / pages, or
  enough hints that a focused search will find them.
- **How to verify** — what to check manually, or which tests to run,
  or what the success state looks like in the UI.

A *bad* issue says "make filtering on the Logs page faster" with no
profiling target, no specific filter, and no acceptance criterion.
The worker will leave that as `needs-clarification` and stop.

## What the worker will refuse to do

The following categories are hard-coded scope-outs. If an issue
touches them, the worker comments and swaps the label to `auto-skip`
without implementing anything:

- **SQL migrations that ALTER or DROP existing tables** — anything
  in `services/cell-api/internal/migrations/sql/` that mutates a
  table created by an earlier migration. Pure additive migrations
  (new tables, new columns with safe defaults, new indexes) are
  fine; destructive or contract-breaking ones need human review.
- **Security / auth surface** — anything that changes how requests
  are authenticated, authorised, or how secrets are handled.
- **Modifying the shape of an existing public HTTP endpoint** in a
  backwards-incompatible way. Adding new endpoints is explicitly
  allowed and encouraged — that's how features land. Changing the
  request/response shape of an existing endpoint that callers rely
  on is the scope-out.

There is no fixed LOC cap. Land what the issue requires. The
implicit cap is "stays focused on the issue you're closing" — if
you find yourself adding three unrelated improvements, stop and
split. Otherwise size yourself to the feature.

These rules are intentionally conservative. Bend them by hand-editing
the relevant files; don't try to widen them in the worker prompt
without thinking about why they're there.

## How the worker behaves

Each firing **drains the queue** — it keeps picking the next
`auto-ready` issue until none remain (or it hits a per-run safety
cap of 10 issues). Each issue gets its own branch + PR, processed
sequentially. Between issues the queue is refetched so label
changes made by humans take effect immediately.

For each issue it executes this loop:

1. **Pick** — `gh issue list -l auto-ready --json … | jq 'sort_by(.number)|.[0]'`
   (the oldest auto-ready issue). If empty, exit silently.
2. **Lock** — comment "🤖 Picking this up — starting now." on the
   issue, swap label `auto-ready` → `auto-in-progress`.
3. **Sanity** — if the spec fails the "spec-complete" check above, or
   the change is in a refused category, comment the reason and swap
   to `needs-clarification` or `auto-skip`. Move on to the next issue.
4. **Implement** — branch `auto/<issue-number>-<short-slug>` from a
   fresh `main` (refetched at the start of every issue), make the
   change.
5. **Gate** — run `go vet ./...` + `go build ./...` in
   `services/cell-api/`, `npx tsc --noEmit` + `npm run build` in
   `frontend/`. Live smoke-test if the change has an HTTP surface.
6. **Ship** — commit (message ends with the Claude co-author
   trailer), push the branch, open a PR with `Closes #N` in the body
   and a short summary of what changed and how it was verified.
7. **Report** — comment on the issue with the PR link, swap label
   `auto-in-progress` → `auto-in-review`.
8. **Continue** — refetch the queue and start the next issue. Loop
   exits when the queue is empty.

If anything in steps 4–6 fails (build error, test failure, push
rejected, anything), the worker:

- Posts the failure to the issue with the relevant log excerpt
- Swaps label back to `auto-ready` (so a future run can retry after
  the underlying cause is fixed) OR to `needs-clarification` if the
  failure points at a spec problem
- Does **not** push partial work to main and does **not** open a half-
  finished PR

## Direct-to-main is for humans only

The repo convention for human work is direct-to-main (solo-dev, no
PR overhead). The autonomous worker never does that. Every
autonomous change goes through a PR you review and merge yourself.
That review is the entire safety net.

## Cadence

The worker is scheduled to fire every 2 hours by default. Adjust by
finding the cron job in the active Claude session and editing the
schedule. If you need it to wait, just don't apply `auto-ready` to
anything — the worker checks the label state, not the issue list.

Each firing drains the queue (up to 10 issues per firing, for cost
safety). Queueing five `auto-ready` issues will normally process
all five in the next firing, one after the other; only the safety
cap or an unrecoverable failure ends a run early.

## When to opt out

- The issue requires judgment calls — leave it unlabelled
- You're actively working on the same area — apply `auto-skip`
- The branch worker would touch is in mid-rebase or has stale state
  — apply `auto-skip` until your local situation settles
- You filed it as a discussion / design doc rather than a build task
  — leave it unlabelled
