#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Reset the demo cell to its golden snapshot: restore the Postgres
# control plane (erasing visitor changes), truncate the telemetry
# tables, and bounce cell-api. The continuous demo-seeder immediately
# starts refilling fresh telemetry.
#
# Schedule it (cron, as root — hourly on the hour here):
#   0 * * * * /usr/local/bin/sluicio-demo-reseed >> /var/log/sluicio-reseed.log 2>&1
#
# KEEP_TELEMETRY=1 skips the ClickHouse truncate (config-only reset).

set -euo pipefail

DEMO_DIR="${DEMO_DIR:-/var/lib/sluicio/demo}"
ENV_FILE="${ENV_FILE:-/etc/sluicio/sluicio.env}"
GOLDEN="$DEMO_DIR/golden.sql.gz"

if [[ ! -f "$GOLDEN" ]]; then
    echo "reseed: no golden snapshot at $GOLDEN — run snapshot.sh first" >&2
    exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
    echo "reseed: missing $ENV_FILE" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

# Container runtime: honor SLUICIO_RUNTIME (bootstrap.sh sets it), else
# auto-detect — hand-rolled hosts (e.g. Fedora + Podman) rarely set it.
RUNTIME="${SLUICIO_RUNTIME:-}"
if [[ -z "$RUNTIME" ]]; then
    if command -v podman >/dev/null 2>&1; then RUNTIME=podman
    elif command -v docker >/dev/null 2>&1; then RUNTIME=docker
    else
        echo "$(basename "$0"): neither podman nor docker on PATH" >&2
        exit 1
    fi
fi
find_ctr() { "$RUNTIME" ps --filter "name=$1" --format '{{.Names}}' | head -n1; }
PG="$(find_ctr postgres)"
CH="$(find_ctr clickhouse)"
API="$(find_ctr cell-api)"

if [[ -z "$PG" || -z "$API" ]]; then
    echo "reseed: postgres/cell-api containers not found — is the stack up?" >&2
    exit 1
fi

echo "reseed: $(date -u +%FT%TZ) stopping cell-api"
"$RUNTIME" stop "$API" >/dev/null

echo "reseed: restoring golden control plane"
gunzip -c "$GOLDEN" | "$RUNTIME" exec -i -e PGPASSWORD="$POSTGRES_PASSWORD" "$PG" \
    psql -q -U "$POSTGRES_USER" -d controlplane -v ON_ERROR_STOP=0 >/dev/null

if [[ "${KEEP_TELEMETRY:-0}" != 1 && -n "$CH" ]]; then
    echo "reseed: truncating telemetry (traces/logs/metrics)"
    for t in traces logs metrics; do
        "$RUNTIME" exec "$CH" clickhouse-client \
            --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" \
            --query "TRUNCATE TABLE IF EXISTS telemetry.$t"
    done
fi

echo "reseed: starting cell-api"
"$RUNTIME" start "$API" >/dev/null
echo "reseed: done"
