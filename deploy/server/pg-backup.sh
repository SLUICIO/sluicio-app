#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Daily Postgres dump for the Sluicio control plane. Installed to
# /usr/local/bin/sluicio-pg-backup and scheduled via cron by
# bootstrap.sh.
#
# What we DON'T back up here: ClickHouse. Telemetry is regenerable —
# as long as ingest keeps flowing, the recent window rebuilds. If you
# need historical-record durability for ClickHouse, look at
# https://github.com/Altinity/clickhouse-backup (out of scope for the
# first-deploy script; that's a "once a customer asks" addition).
#
# Postgres is small (auth, integrations, dashboards, alerts, settings)
# and IS critical — losing it loses every user's config. Daily gzip'd
# dump, kept for 30 days.

set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/var/lib/sluicio/backups}"
KEEP_DAYS="${KEEP_DAYS:-30}"
ENV_FILE="${ENV_FILE:-/etc/sluicio/sluicio.env}"

# Pull POSTGRES_USER, POSTGRES_PASSWORD, etc. from the env file the
# bootstrap wrote.  `set -a` exports everything sourced afterwards.
if [[ ! -f "$ENV_FILE" ]]; then
    echo "pg-backup: missing $ENV_FILE — has bootstrap.sh run?" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

mkdir -p "$BACKUP_DIR"
OUT="$BACKUP_DIR/pg-$(date -u +%FT%H%M%SZ).sql.gz"

# Use the in-container pg_dump so the host doesn't need a matching
# Postgres client. The compose binds Postgres to 127.0.0.1:5432, so
# `<runtime> exec` against the container avoids any TCP path entirely.
# SLUICIO_RUNTIME comes from the env file (defaults to docker for
# back-compat with bootstraps that pre-date the runtime flag).
RUNTIME="${SLUICIO_RUNTIME:-docker}"
"$RUNTIME" exec \
    -e PGPASSWORD="$POSTGRES_PASSWORD" \
    "$("$RUNTIME" ps --filter 'name=postgres' --format '{{.Names}}' | head -n1)" \
    pg_dump -U "$POSTGRES_USER" -d controlplane --no-owner --clean --if-exists \
    | gzip -9 > "$OUT"

# Sanity check: a real dump is at least 50 KB even on an empty schema
# (the migrations themselves are >50 KB of DDL). Anything smaller
# means pg_dump emitted nothing or errored mid-stream.
SIZE=$(wc -c < "$OUT")
if (( SIZE < 50000 )); then
    echo "pg-backup: dump suspiciously small ($SIZE bytes), keeping but warning" >&2
fi

# Rotation: delete dumps older than KEEP_DAYS. find's -mtime is "days
# since modified" — +30 means "more than 30 days old."
find "$BACKUP_DIR" -name 'pg-*.sql.gz' -mtime "+$KEEP_DAYS" -delete

echo "pg-backup: wrote $OUT ($((SIZE / 1024)) KB)"
