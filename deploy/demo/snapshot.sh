#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Capture the demo cell's GOLDEN SNAPSHOT: a full dump of the Postgres
# control plane (orgs, users incl. demo flags, integrations, systems,
# groups, policies, dashboards, alert rules, ingest keys).
#
# Workflow: build the demo org by hand in the UI until it's exactly the
# demo you want, then run this once. reseed.sh restores it on a schedule,
# erasing whatever visitors changed. Improving the demo = edit in the UI,
# snapshot again.
#
#   sudo deploy/demo/snapshot.sh          # writes $DEMO_DIR/golden.sql.gz
#
# Same conventions as pg-backup.sh: env from /etc/sluicio/sluicio.env,
# in-container pg_dump so the host needs no Postgres client.

set -euo pipefail

DEMO_DIR="${DEMO_DIR:-/var/lib/sluicio/demo}"
ENV_FILE="${ENV_FILE:-/etc/sluicio/sluicio.env}"

if [[ ! -f "$ENV_FILE" ]]; then
    echo "snapshot: missing $ENV_FILE — has bootstrap.sh run?" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

RUNTIME="${SLUICIO_RUNTIME:-docker}"
PG="$("$RUNTIME" ps --filter 'name=postgres' --format '{{.Names}}' | head -n1)"
if [[ -z "$PG" ]]; then
    echo "snapshot: no running postgres container found" >&2
    exit 1
fi

mkdir -p "$DEMO_DIR"
OUT="$DEMO_DIR/golden.sql.gz"
TS="$DEMO_DIR/golden-$(date -u +%FT%H%M%SZ).sql.gz"

# --clean --if-exists so the restore drops + recreates every object —
# vandalized rows can't survive as leftovers.
"$RUNTIME" exec -e PGPASSWORD="$POSTGRES_PASSWORD" "$PG" \
    pg_dump -U "$POSTGRES_USER" -d controlplane --no-owner --clean --if-exists \
    | gzip -9 > "$TS"

SIZE=$(wc -c < "$TS")
if (( SIZE < 50000 )); then
    echo "snapshot: dump suspiciously small ($SIZE bytes) — refusing to promote it to golden" >&2
    exit 1
fi
cp "$TS" "$OUT"
echo "snapshot: golden updated ($OUT, $SIZE bytes); timestamped copy kept at $TS"
