# Demo cell

Run a public, self-resetting Sluicio demo: visitors log in with a shared
demo account, explore (and vandalize) freely, and a scheduled reseed puts
everything back.

Three moving parts:

1. **Demo accounts** (`is_demo`) — product access per their role, but
   self-service (password/MFA/tokens) and org administration (org
   lifecycle, members, SSO, ingest keys) are locked. Flag accounts on the
   Operator page.
2. **Continuous seeder** — the `demo-seeder` service emits synthetic
   traces/logs/metrics through the real OTLP pipeline forever, so the
   demo always shows live, moving data.
3. **Golden snapshot + scheduled reseed** — the demo org's config lives
   in a `pg_dump` you take once; a cron job restores it and truncates
   telemetry, erasing visitor changes.

The overlay also pre-fills the login form with the demo credentials
(`DEMO_LOGIN_EMAIL` / `DEMO_LOGIN_PASSWORD`, default
`demo@sluicio.com` / `demodemo`), so visitors are one click from signed
in. It works by setting `SLUICIO_LOGIN_PREFILL_*` on cell-api — never
set those on a non-demo deployment.

## Setup

Start from a working [server deployment](../server/README.md), then:

```sh
# 1. Build the demo org by hand in the UI: integrations, systems, groups
#    + policies, dashboards, alert rules. Create the demo user(s), mark
#    them demo on the Operator page. Mint an ingest key (Settings →
#    Ingestion) for the seeder.

# 2. Add to /etc/sluicio/sluicio.env:
#      DEMO_INGEST_KEY=<the key from step 1>
#      DEMO_SEED_INTERVAL=15s        # optional
#    Keep ingest auth ON — never INGEST_ALLOW_ANONYMOUS on a public host.

# 3. Bring the stack up with the demo overlay:
docker compose \
  -f deploy/server/docker-compose.registry.yml \
  -f deploy/demo/docker-compose.demo.yml \
  --env-file /etc/sluicio/sluicio.env up -d

# 4. Let the seeder run a few minutes so services register, then finish
#    any config that needed live services (health checks, facets). Take
#    the golden snapshot:
sudo deploy/demo/snapshot.sh

# 5. Schedule the reseed (hourly here):
sudo install -m 755 deploy/demo/reseed.sh /usr/local/bin/sluicio-demo-reseed
echo '0 * * * * root /usr/local/bin/sluicio-demo-reseed >> /var/log/sluicio-reseed.log 2>&1' \
  | sudo tee /etc/cron.d/sluicio-demo-reseed
```

## Day-2

- **Improve the demo**: edit in the UI, run `snapshot.sh` again. The next
  reseed serves the new golden state. No code, no seed scripts to maintain.
- **Reseed by hand**: `sudo sluicio-demo-reseed` any time (cell-api is
  down for a few seconds; sessions survive — they're part of the snapshot).
- **Keep telemetry across a reseed**: `KEEP_TELEMETRY=1 sluicio-demo-reseed`
  (config-only reset).
- Set the demo org's **retention short** (2–3 days) so ClickHouse stays
  small between reseeds.
- The reseed restores *everything* in Postgres — including users, ingest
  keys, and the license state — so the golden snapshot must be taken on
  the demo cell itself, in the state you want to return to.
