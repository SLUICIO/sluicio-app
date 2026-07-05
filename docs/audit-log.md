# Audit log (Enterprise)

A tamper-evident, searchable record of who did what on the cell: sign-ins,
membership and role changes, token lifecycle, org changes, alerting and
catalog configuration, operator actions, and access to the audit log
itself. Gated by the `audit_log` license entitlement — Community builds
wire a no-op recorder and the Settings tab shows an upgrade notice.

## What gets recorded

- **Auth events** — `login.succeeded` (password + SSO), `login.failed`
  (known accounts only; an unknown email has no org to attach to),
  `mfa.verify_failed`, `session.logout`, `password.changed`,
  `password.reset_requested`, `password.reset_completed`. Written once per
  org membership, since a sign-in isn't scoped to one org.
- **Org + access control** — members, roles, groups, group policies,
  service accounts, tokens, ingest keys, SSO providers, org profile.
- **Configuration** — integrations, matchers, alert rules, notification
  channels/profiles (names only; channel configs hold webhook secrets),
  completion rules, schemas, maps, tags, metadata fields, system types,
  monitoring templates, systems, badges, facets, cell settings.
- **Operator actions** — dual-written: once to the operator's own org log
  and once to the target org's log, so tenant admins see cell-operator
  changes to their org.
- **Audit access** — every CSV export (`audit_log.exported`, with the
  filter scope in metadata); views (`audit_log.viewed`) throttled to one
  entry per admin per org per hour.

## Searching

Settings → Audit log, or `GET /api/v1/audit-log` (admin + entitlement):

| Param | Meaning |
|---|---|
| `actor` | case-insensitive substring of actor name or email |
| `actor_id` | exact user UUID |
| `action` | prefix — `member.` matches `member.added`, `member.removed`, … |
| `target_type`, `target` | exact resource match |
| `from`, `to` | RFC3339 bounds on `occurred_at` (from ≤ t < to) |
| `limit`, `before` | page size + keyset cursor |
| `format=csv` | stream the filtered entries as CSV (cap 50k rows) |

"What did user X do between 08:00 and 10:00" is one query:
`?actor=x@corp.com&from=2026-07-03T08:00:00Z&to=2026-07-03T10:00:00Z`.

**Renames:** entries keep the actor name that was true when they were
written — they're hash-chained, so rewriting them would be tampering. The
rename itself is audited (`user.profile_updated`, with old→new in
metadata), and clicking an actor in the table filters by their stable
user id, which matches their entries across every name they've had. The
`actor` text filter only matches the name/email as recorded at the time;
use the id filter (or `actor_id=`) for a person's full history.

## Tamper evidence

Each entry stores `entry_hash = sha256(prev_hash ∥ entry fields)`, chained
per org. Deleting or editing any row breaks every later link, detectably.
`GET /api/v1/audit-log/verify` (or the **Verify integrity** button) walks
the chain and reports `ok`, entries checked, and the first broken id if
the chain fails. Entries written before chaining shipped carry no hash and
are reported as an unverifiable legacy prefix, not a failure.

The chain proves *integrity*, not *availability* offsite — for that, add
the sink below.

## Off-box sink (opt-in, deploy-time config)

Set on the cell-api container:

```
SLUICIO_AUDIT_SINK_URL=https://siem.example.com/ingest/sluicio-audit
SLUICIO_AUDIT_SINK_SECRET=<hmac key>          # optional but recommended
```

Every recorded entry is also POSTed as JSON to the URL (async,
best-effort, one retry; a down sink never blocks the audited action).
With the secret set, requests carry
`X-Sluicio-Signature: sha256=<hex hmac of body>` so the receiver can
authenticate the sender. **Off by default** and deliberately not
configurable through the API: a runtime knob would let a compromised
admin session redirect or drain the security log. The local Postgres
chain stays the source of truth; the sink is the off-box witness that
survives database-level tampering.

## Retention

Settings → Retention → "Audit log", or `audit_days` on
`PATCH /api/v1/cell-settings/retention`. Default **14 days**; raising it
requires the `audit_log` entitlement (cap: 10 years). Pruning runs on the
hourly retention cycle and is **chain-safe**: the newest pruned entry's
hash is kept as an anchor so verification of the surviving log still
works. Pick your number deliberately — deleted audit evidence is gone,
and compliance regimes often expect 1–7 years.
