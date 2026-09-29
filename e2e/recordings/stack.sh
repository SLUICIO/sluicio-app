#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# The recording stack: a throwaway Sluicio on its own ports with a
# continuous seeder, an on-camera account and a named org. Nothing on it
# is real, which is the point - a recording must never show a real cell.
#
#   recordings/stack.sh up       start it (first run also sets it up)
#   recordings/stack.sh status   what is running
#   recordings/stack.sh down     stop it and DELETE its data
#
# The account, the stack URL and the ingest key go to e2e/.env.recording
# (gitignored, mode 600). Nothing secret is printed.
#
# Knobs (env): RECORDING_TAG (image tag, default latest), RECORDING_PORT
# (UI, 8095), RECORDING_INGEST_PORT (4325), RECORDING_ORG ("Acme
# Logistics"), RECORDING_USER_NAME ("Alex Morgan"), RECORDING_USER_EMAIL
# (alex@acme.example).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
envfile="$here/../.env.recording"

project=sluicio-recording
# A key that is not there yet is an empty answer, not a failure: under
# pipefail a grep with no match would end the script at the first lookup.
mq() {
  RABBITMQ_ADMIN_PASSWORD="$(getenv RABBITMQ_ADMIN_PASSWORD)" \
    RABBITMQ_MONITOR_PASSWORD="$(getenv RABBITMQ_MONITOR_PASSWORD)" \
    SLUICIO_INGEST_KEY="$(getenv RECORDING_INGEST_KEY)" \
    "$engine" compose -p "$project-mq" -f "$here/rabbitmq/docker-compose.yml" "$@"
}

setenv_once() { [ -n "$(getenv "$1")" ] || printf '%s=%s\n' "$1" "$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-20)" >>"$envfile"; }

getenv() { { grep -E "^$1=" "$envfile" 2>/dev/null || true; } | head -1 | cut -d= -f2-; }
# The tag the stack was created with is remembered and reused. A re-run
# without RECORDING_TAG used to fall back to whatever `latest` image sat on
# the machine - weeks old, once - and quietly put an old UI on camera.
tag="${RECORDING_TAG:-$(getenv RECORDING_TAG)}"
tag="${tag:-latest}"
port="${RECORDING_PORT:-8095}"
ingest_port="${RECORDING_INGEST_PORT:-4325}"
org_name="${RECORDING_ORG:-Acme Logistics}"
user_name="${RECORDING_USER_NAME:-Alex Morgan}"
user_email="${RECORDING_USER_EMAIL:-alex@acme.example}"
base="http://localhost:$port"

engine="$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)"
compose() {
  COMPOSE_PROJECT_NAME=$project PORT=$port CELL_INGEST_PORT=$ingest_port TAG=$tag \
    SLUICIO_APP_URL=$base SLUICIO_INGEST_URL="http://localhost:$ingest_port" \
    "$engine" compose -f "$repo/deploy/quickstart/docker-compose.yml" "$@"
}


up() {
  # `latest` means the newest release, not the newest image on this machine.
  [ "$tag" = latest ] && compose pull -q cell-api cell-ingest frontend
  compose up -d
  printf 'waiting for the stack'
  until curl -sf -o /dev/null "$base/api/v1/auth/install-state"; do printf .; sleep 2; done
  echo

  jar="$(mktemp)"
  trap 'rm -f "$jar"' RETURN

  if curl -s "$base/api/v1/auth/install-state" | grep -q '"fresh":true'; then
    # First run: the on-camera account is the first user, so it is the
    # admin and nothing called "admin@sluicio.local" appears on screen.
    pw="$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-20)"
    umask 077
    printf 'RECORDING_BASE_URL=%s\nRECORDING_EMAIL=%s\nRECORDING_PASSWORD=%s\n' "$base" "$user_email" "$pw" >"$envfile"
    python3 -c 'import json,sys; print(json.dumps({"name":sys.argv[1],"email":sys.argv[2],"password":sys.argv[3]}))' \
      "$user_name" "$user_email" "$pw" |
      curl -sf -o /dev/null -H 'Content-Type: application/json' --data @- "$base/api/v1/auth/bootstrap-admin"
    echo "created the on-camera account ($user_email)"
  fi

  email="$(getenv RECORDING_EMAIL)"
  pw="$(getenv RECORDING_PASSWORD)"
  if [ -z "$email" ] || [ -z "$pw" ]; then
    echo "the stack is already set up, but $envfile has no account for it." >&2
    echo "run 'recordings/stack.sh down' to start over." >&2
    exit 1
  fi
  python3 -c 'import json,sys; print(json.dumps({"email":sys.argv[1],"password":sys.argv[2]}))' "$email" "$pw" |
    curl -sf -o /dev/null -c "$jar" -H 'Content-Type: application/json' --data @- "$base/api/v1/auth/login"

  org_id="$(curl -sf -b "$jar" "$base/api/v1/me" | python3 -c 'import json,sys; print(json.load(sys.stdin)["memberships"][0]["org"]["id"])')"
  python3 -c 'import json,sys; print(json.dumps({"name":sys.argv[1]}))' "$org_name" |
    curl -sf -o /dev/null -b "$jar" -X PATCH -H 'Content-Type: application/json' --data @- "$base/api/v1/orgs/$org_id"

  key="$(getenv RECORDING_INGEST_KEY)"
  if [ -z "$key" ]; then
    key="$(curl -sf -b "$jar" -H 'Content-Type: application/json' -d '{"name":"walkthrough seeder"}' \
      "$base/api/v1/ingest-keys" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
    printf 'RECORDING_INGEST_KEY=%s\n' "$key" >>"$envfile"
  fi

  # A seeder from another tag is replaced, not restarted: moving the stack
  # to a new release used to leave the seeder on the old one.
  running_image="$("$engine" inspect -f '{{.Config.Image}}' "$project-seeder" 2>/dev/null || true)"
  if [ -n "$running_image" ] && [ "$running_image" != "ghcr.io/sluicio/demo-seeder:$tag" ]; then
    "$engine" rm -f "$project-seeder" >/dev/null
  fi
  if ! "$engine" inspect "$project-seeder" >/dev/null 2>&1; then
    "$engine" run -d --name "$project-seeder" --network "${project}_default" --restart unless-stopped \
      -e SLUICIO_INGEST_KEY="$key" "ghcr.io/sluicio/demo-seeder:$tag" \
      -continuous -interval=15s -batch=50 \
      -endpoint=http://cell-ingest:4318/v1/traces \
      -logs-endpoint=http://cell-ingest:4318/v1/logs \
      -metrics-endpoint=http://cell-ingest:4318/v1/metrics >/dev/null
  else
    "$engine" start "$project-seeder" >/dev/null
  fi

  # RabbitMQ, its three queues and the Collector streaming their metrics.
  setenv_once RABBITMQ_ADMIN_PASSWORD
  setenv_once RABBITMQ_MONITOR_PASSWORD
  mq up -d
  printf 'waiting for rabbitmq'
  until "$engine" exec "$project-mq-rabbitmq-1" rabbitmq-diagnostics -q ping >/dev/null 2>&1; do printf .; sleep 2; done
  echo
  # The monitoring user, exactly as the RabbitMQ guide sets it up: tagged
  # monitoring, no rights on any queue. Already there on a re-run.
  "$engine" exec "$project-mq-rabbitmq-1" rabbitmqctl add_user monitoring "$(getenv RABBITMQ_MONITOR_PASSWORD)" >/dev/null 2>&1 || true
  "$engine" exec "$project-mq-rabbitmq-1" rabbitmqctl set_user_tags monitoring monitoring >/dev/null
  "$engine" exec "$project-mq-rabbitmq-1" rabbitmqctl set_permissions -p / monitoring "^$" "^$" ".*" >/dev/null

  { grep -v '^RECORDING_TAG=' "$envfile" || true; printf 'RECORDING_TAG=%s\n' "$tag"; } >"$envfile.tmp" && mv "$envfile.tmp" "$envfile"
  chmod 600 "$envfile"

  echo "recording stack up at $base (images :$tag), org \"$org_name\", seeder running."
  echo "rabbitmq management UI at http://localhost:15680 (user admin), metrics flowing through the collector."
  echo "give it 30 minutes of traffic before a take, so the counts and charts have history."
}

down() {
  # The broker's containers sit on the stack's network: gone first.
  mq down -v 2>/dev/null || true
  "$engine" rm -f "$project-seeder" >/dev/null 2>&1 || true
  compose down -v
  rm -f "$envfile"
  echo "recording stack removed, with its data and $envfile."
}

status() {
  "$engine" ps --filter "name=$project" --format '{{.Names}}\t{{.Status}}'
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  *) echo "usage: $0 up|status|down" >&2; exit 2 ;;
esac
