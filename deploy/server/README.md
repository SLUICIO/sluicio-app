<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Sluicio · server bootstrap

One-shot setup for a single-server Sluicio cell on a fresh Ubuntu
24.04 LTS box. Covers: Podman (or Docker) stack, Caddy reverse
proxy + TLS, firewall, SSH hardening, daily Postgres backups,
systemd integration.

This is the right shape for **solo developer to small team** scale.
At larger scale you'll want to split the database tier off the
application tier and adopt managed services or Kubernetes — but
none of that is needed to get to the first real users.

## Recommended hardware

| Resource | Minimum | Comfortable |
|---|---|---|
| vCPU | 4 dedicated | 8 dedicated |
| RAM | 16 GB | 32 GB |
| Disk | 200 GB NVMe SSD | 500 GB NVMe SSD |
| Network | 1 Gbit | 1 Gbit |

ClickHouse compresses telemetry well (5–20×), but NVMe matters more
than raw capacity — spinning disks make merge cycles painful.

Hetzner CCX23 (4 vCPU, 16 GB, 240 GB NVMe) is the cheapest box that
runs Sluicio comfortably (~€25/month at time of writing). AWS / GCP
equivalents work at ~3–5× the cost.

## Prerequisites (do these FIRST)

1. Fresh Ubuntu 24.04 LTS server, sudo access, public IP
2. **Two DNS A records**, both pointing at the server's IP:
   - `sluicio.example.com` (the UI + API)
   - `ingest.sluicio.example.com` (the OTLP ingest endpoint)
3. **SSH key access already working** — the script disables password
   SSH and will lock you out otherwise. The script bails before
   touching SSH if no `~/.ssh/authorized_keys` is detected, but make
   sure yours is in place first.
4. The repo cloned to `/opt/sluicio`:
   ```sh
   sudo mkdir -p /opt
   sudo git clone https://github.com/SLUICIO/sluicio-app.git /opt/sluicio
   ```

## Run it

```sh
sudo /opt/sluicio/deploy/server/bootstrap.sh \
    --domain sluicio.example.com \
    --email  admin@example.com
```

Container runtime defaults to **Podman**. To switch to Docker (more
familiar tooling, slightly more `compose` compatibility on edge
cases) pass `--runtime=docker`.

```sh
# Same install but with Docker:
sudo /opt/sluicio/deploy/server/bootstrap.sh \
    --domain sluicio.example.com \
    --email  admin@example.com \
    --runtime=docker
```

## Choosing a database setup

Sluicio needs a **Postgres** (integrations, rules, audit) and a **ClickHouse**
(traces, logs, metrics). Pick the compose files for what you already have — the
services are identical; only where the databases come from changes. Copy the
matching env example to `sluicio.env` and fill it in.

| You have… | Compose files (`-f …`) | Env example |
|---|---|---|
| **Neither** (bundle both — simplest) | `docker-compose.registry.yml` | `sluicio.env.example` |
| **Both** (your own Postgres + ClickHouse) | `docker-compose.registry.external-db.yml` | `sluicio.env.external.example` |
| **Postgres only** (bundle ClickHouse) | `…external-db.yml` + `docker-compose.bundled-clickhouse.yml` | `sluicio.env.external.example` |
| **ClickHouse only** (bundle Postgres) | `…external-db.yml` + `docker-compose.bundled-postgres.yml` | `sluicio.env.external.example` |

```sh
# Example — you run your own databases already:
cp sluicio.env.external.example sluicio.env   # then edit POSTGRES_DSN + CLICKHOUSE_*
docker compose --env-file sluicio.env \
  -f docker-compose.registry.external-db.yml up -d

# Example — you have Postgres but want a bundled ClickHouse:
docker compose --env-file sluicio.env \
  -f docker-compose.registry.external-db.yml \
  -f docker-compose.bundled-clickhouse.yml up -d
```

`bootstrap.sh` uses the all-in-one (`docker-compose.registry.yml`) — the right
default for a fresh box with nothing else on it. cell-api migrates Postgres and
creates the ClickHouse tables on startup, so external databases only need to
exist and be reachable with the credentials you provide. On Kubernetes, use the
[Helm chart](../helm/cell/) (external databases only — see its README).

### Why Podman is the default

| Concern | Docker | Podman |
|---|---|---|
| Daemon | `dockerd` always running as root | None — `podman` is a normal CLI |
| Group privilege | `sluicio` must be in the `docker` group, which is effectively root-equivalent | No group needed |
| `compose` support | `docker compose` plugin (mature) | `podman compose` via `podman-compose` (works for our compose file) |
| Restart story | Daemon owns the containers; daemon crash = all containers down | Each `podman run` is a separate child of systemd |

For a single-server install on the public internet, the "no docker
group" win is the deciding factor. The container surface is one less
privilege-escalation vector. Switch to Docker if you have existing
tooling that assumes it.

The script:

1. Installs **Podman + podman-compose** (or `docker.io` +
   `docker-compose-plugin` with `--runtime=docker`), plus `caddy`,
   `ufw`, `fail2ban`, `nodejs` (20.x via NodeSource),
   `postgresql-client`, `rsync`, `jq`, `git`
2. Creates a `sluicio` system user (with docker-group access only
   when `--runtime=docker`)
3. Sets up the data layout under `/var/lib/sluicio/{postgres,clickhouse,backups}`
4. Generates strong random passwords for Postgres + ClickHouse,
   writes them to `/etc/sluicio/sluicio.env` (mode 600, root-owned)
5. Renders `Caddyfile.template` with your domain + email into
   `/etc/caddy/Caddyfile`, reloads Caddy
6. Runs `npm ci && npm run build` for the frontend, deploys the
   bundle to `/var/www/sluicio/`
7. Installs the `sluicio.service` systemd unit + enables it
8. Installs the `pg-backup` cron at 03:00 UTC daily
9. Configures `ufw` (allow 22, 80, 443; deny everything else),
   enables `fail2ban`
10. Disables password SSH (`PasswordAuthentication no`)
11. Starts the stack, waits for `cell-api` healthcheck

Total wall-clock: 5–15 minutes depending on apt cache + container
image pulls.

## What gets installed where

| Path | Contents |
|---|---|
| `/opt/sluicio` | The repo. Git-controlled, owned by `sluicio` user. |
| `/var/lib/sluicio/postgres` | Postgres data volume (bind-mounted into the container) |
| `/var/lib/sluicio/clickhouse` | ClickHouse data volume |
| `/var/lib/sluicio/backups` | Nightly `pg_dump` output, 30-day rolling |
| `/var/www/sluicio` | Built frontend bundle, served by Caddy |
| `/etc/sluicio/sluicio.env` | DB passwords. **Mode 600, root-owned. Do not widen.** |
| `/etc/caddy/Caddyfile` | Reverse proxy config |
| `/etc/systemd/system/sluicio.service` | systemd unit (rendered from `sluicio.service.template` with your chosen runtime) |
| `/usr/local/bin/sluicio-pg-backup` | Daily backup script |
| `/etc/cron.d/sluicio-pg-backup` | Cron entry that calls the above |

## Verifying the install

```sh
# Stack status
systemctl status sluicio

# Stack logs
journalctl -u sluicio -f

# Cell-api health
curl https://sluicio.example.com/api/v1/auth/install-state
# → {"fresh":true}   (or false once anyone has logged in)

# Caddy access logs
tail -f /var/log/caddy/access.log

# Backup ran?
ls -la /var/lib/sluicio/backups/
```

## First login

```
URL:      https://sluicio.example.com/
Email:    admin@sluicio.local
Password: admin
```

**Change the password immediately** via Account → Password. The Login
page hides the "ships with a default admin" hint once anyone has
signed in (see issue #92), but the credentials remain working until
you rotate them.

## Updating

Pull new code + rebuild + restart:

```sh
sudo /opt/sluicio/deploy/server/update.sh
```

What `update.sh` does, in order:

1. **Pre-flight**: refuses to run on a dirty working tree (would
   half-update if `git pull` failed mid-flight)
2. **`git fetch`** + prints the commits + files changed
3. **Asks for confirmation** (skip with `--yes`)
4. **Detects which deploy artifacts changed** between the current
   HEAD and origin:
   - `Caddyfile.template`   → re-renders `/etc/caddy/Caddyfile`,
     validates with `caddy validate`, reloads
   - `sluicio.service`      → reinstalls + `systemctl daemon-reload`
   - `pg-backup.sh`         → reinstalls `/usr/local/bin/sluicio-pg-backup`
   - `frontend/*`           → `npm ci && npm run build` + rsync to
     `/var/www/sluicio`
   - `services/*` / `pkg/*` → handled implicitly by
     `${SLUICIO_RUNTIME} compose up -d --build`
5. **Pulls** (`git pull --ff-only` — refuses to merge)
6. **Reloads sluicio** (which is `${SLUICIO_RUNTIME} compose up -d --build`)
7. **Waits for cell-api healthcheck** with rollback hint on failure

Flags:

| Flag | What it does |
|---|---|
| `--yes`, `-y` | Skip the confirmation prompt (for cron / CI) |
| `--dry-run`, `-n` | Print every command instead of running it |
| `--snapshot` | Run `pg-backup` BEFORE pulling so you have a fresh known-good restore point |
| `--no-frontend` | Skip the npm rebuild (useful when only Go code changed) |

### Rollback

If `update.sh` reports the healthcheck didn't come up, the output
includes the exact rollback command:

```sh
cd /opt/sluicio
sudo git reset --hard <previous-sha>
sudo /opt/sluicio/deploy/server/update.sh --yes
```

The data volumes are never touched by an update, so a rollback is
purely code + container revert. Postgres schema migrations are
forward-only — if a release adds a column, the rollback works fine
because the old code just doesn't read it; if a release renames or
drops a column, a roll-forward is the recovery path, not a rollback.

### A typical update session

```
$ sudo /opt/sluicio/deploy/server/update.sh

==> Pre-flight
==> Fetching origin
==> Changes coming from 14f272d → 5a8b1c2
      5a8b1c2 alerts: surface PagerDuty severity in the channel list
      ...
    Files changed: 7 total
      frontend/src/pages/Alerts.tsx
      services/cell-api/internal/alerting/channels.go
      ...
    Artifacts that will be redeployed:
      ✓ Go services (container rebuild)
      ✓ Frontend (npm rebuild)

Proceed with update? [y/N] y
==> Pulling code
==> Rebuild frontend
==> Rebuild + restart containers
==> Waiting for cell-api healthcheck (up to 60s)
==> Update complete.
```

## Rotating secrets

```sh
sudo /opt/sluicio/deploy/server/bootstrap.sh \
    --domain sluicio.example.com \
    --email  admin@example.com \
    --regen-secrets
```

Generates new DB passwords + restarts the stack. **Doesn't help if
the prior secrets were exfiltrated** — at that point you also need to
consider whether the data is compromised — but it stops the bleeding
going forward.

## Backups: what to do off-machine

The cron writes nightly dumps to `/var/lib/sluicio/backups/` and keeps
30 days. **That's the durability of the disk you're on.** For real
durability:

- Set up `rclone` to sync that directory to S3 / Backblaze B2 /
  Hetzner Storage Box nightly
- Or scp the dumps to a second box on a schedule

ClickHouse data is NOT backed up — telemetry is regenerable as long
as ingest keeps flowing. If you start using Sluicio as a system of
record, look at [Altinity/clickhouse-backup](https://github.com/Altinity/clickhouse-backup).

## What it doesn't do

- **Self-instrumentation** — Sluicio's own services don't emit metrics
  yet. Run `netdata` on the host (one-line install) until OTLP self-
  instrumentation lands.
- **High availability** — single server, single CH, single Postgres.
  If the box dies, you're rebuilding.
- **Multi-tenant isolation** — all orgs share one ClickHouse cell;
  the policy filter narrows visibility at the query layer
  (`docs/performance-audit.md` → P2-2 covers the row-level path).

## Recovering from a bad bootstrap

The script is idempotent — re-running with the same args fixes most
problems by reapplying the desired state. If you need to wipe and
start over:

```sh
sudo systemctl stop sluicio
source /etc/sluicio/sluicio.env  # picks up SLUICIO_RUNTIME
sudo "$SLUICIO_RUNTIME" compose -f /opt/sluicio/deploy/server/docker-compose.yml down -v
sudo rm -rf /var/lib/sluicio
sudo rm /etc/sluicio/sluicio.env
sudo /opt/sluicio/deploy/server/bootstrap.sh --domain ... --email ...
```

(That `-v` drops the data volumes — you keep nothing from before.)

## Files in this directory

- `bootstrap.sh` — the one-shot script
- `update.sh` — pull + rebuild + restart
- `docker-compose.yml` — build-from-source stack (postgres + clickhouse +
  cell-api + cell-ingest; no prometheus)
- `docker-compose.registry.yml` — **all-in-one** from prebuilt images (bundles
  Postgres + ClickHouse) — for a box with nothing else on it
- `docker-compose.registry.external-db.yml` — prebuilt images against **your
  own** Postgres + ClickHouse (no bundled databases)
- `docker-compose.bundled-postgres.yml` / `docker-compose.bundled-clickhouse.yml`
  — overlays to bundle just one database (see "Choosing a database setup")
- `sluicio.env.example` — env template for the all-in-one setup
- `sluicio.env.external.example` — env template for external databases
- `Caddyfile.template` — placeholders for domain + email + frontend path
- `sluicio.service` — systemd unit
- `pg-backup.sh` — daily Postgres dump
- `README.md` — you're reading it
