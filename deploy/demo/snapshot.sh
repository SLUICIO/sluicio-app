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

# Guard against promoting a broken/empty dump. A curated demo org gzips
# well past this; a sparse just-bootstrapped cell may not — override with
# MIN_GOLDEN_BYTES if you knowingly want a minimal golden.
MIN_GOLDEN_BYTES="${MIN_GOLDEN_BYTES:-50000}"
SIZE=$(wc -c < "$TS")
if (( SIZE < MIN_GOLDEN_BYTES )); then
    echo "snapshot: dump is $SIZE bytes (< $MIN_GOLDEN_BYTES) — refusing to promote it to golden." >&2
    echo "snapshot: if the demo org really is this small, rerun with MIN_GOLDEN_BYTES=$SIZE." >&2
    echo "snapshot: timestamped dump kept at $TS" >&2
    exit 1
fi
cp "$TS" "$OUT"
echo "snapshot: golden updated ($OUT, $SIZE bytes); timestamped copy kept at $TS"
