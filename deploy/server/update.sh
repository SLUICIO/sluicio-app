#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Update a previously-bootstrapped Sluicio server to the latest commit
# on origin/main. Idempotent and safe to run on a no-op (clean
# working tree, already-up-to-date repo).
#
#   sudo /opt/sluicio/deploy/server/update.sh
#
# What it does, in order:
#
#   1. Pre-flight: root, repo present, working tree clean
#   2. git fetch + show what's coming (commits, files changed)
#   3. Confirm prompt (skip with --yes)
#   4. Optional pg snapshot (--snapshot) — runs sluicio-pg-backup
#      BEFORE pulling so you have a known-good restore point
#   5. git pull --ff-only (refuses to merge; bails on divergence)
#   6. For each deploy artifact whose source has changed, re-deploy:
#         deploy/server/Caddyfile.template  → /etc/caddy/Caddyfile + reload
#         deploy/server/sluicio.service     → /etc/systemd/system/ + daemon-reload
#         deploy/server/pg-backup.sh        → /usr/local/bin/sluicio-pg-backup
#      Detected by comparing pre-pull HEAD vs post-pull HEAD diff.
#   7. Rebuild frontend (npm ci + npm run build), rsync dist/
#   8. systemctl reload sluicio (docker compose up -d --build)
#   9. Wait for cell-api healthcheck. Rollback hint on failure.
#
# Flags:
#
#   --yes           skip the confirmation prompt
#   --dry-run       show what would happen; touch nothing
#   --snapshot      run pg-backup before pulling
#   --no-frontend   skip the npm rebuild (use when only Go code changed)
#
# This does NOT touch:
#   - /etc/sluicio/sluicio.env       (the secrets — bootstrap-time concern)
#   - /var/lib/sluicio/*             (the data volumes)
#   - the firewall / ssh / fail2ban  (those are bootstrap-time too)
#
# To rotate secrets, run bootstrap.sh --regen-secrets. To re-render
# Caddy with a new domain, run bootstrap.sh again (idempotent).

set -euo pipefail

# ── args ──────────────────────────────────────────────────────────────

REPO_DIR="${REPO_DIR:-/opt/sluicio}"
FRONTEND_ROOT="${FRONTEND_ROOT:-/var/www/sluicio}"
ENV_FILE="${ENV_FILE:-/etc/sluicio/sluicio.env}"
ASSUME_YES=0
DRY_RUN=0
SNAPSHOT=0
SKIP_FRONTEND=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --yes|-y)      ASSUME_YES=1; shift ;;
        --dry-run|-n)  DRY_RUN=1; shift ;;
        --snapshot)    SNAPSHOT=1; shift ;;
        --no-frontend) SKIP_FRONTEND=1; shift ;;
        -h|--help) sed -n '1,40p' "$0"; exit 0 ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

# ── helpers ───────────────────────────────────────────────────────────

step() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
say()  { printf '    %s\n' "$*"; }
fail() { printf '\033[1;31m!!\033[0m %s\n' "$*" >&2; exit 1; }
run()  {
    # All side-effects flow through here so --dry-run flips them off.
    # We use `eval "$*"` (not just "$@") so callers can pass full
    # command strings including pipes and redirects — the Caddyfile
    # rendering needs `sed ... > /etc/caddy/Caddyfile`. All call sites
    # are constructed from script-local variables, never user input,
    # so this is safe.
    if (( DRY_RUN )); then
        printf '    \033[2m[dry-run] $ %s\033[0m\n' "$*"
    else
        # shellcheck disable=SC2294
        eval "$@"
    fi
}

# ── pre-flight ────────────────────────────────────────────────────────

step "Pre-flight"

if [[ $EUID -ne 0 ]]; then
    fail "Run as root (sudo)."
fi

if [[ ! -d "$REPO_DIR/.git" ]]; then
    fail "No repo at $REPO_DIR. Have you run bootstrap.sh?"
fi

cd "$REPO_DIR"

# Working tree must be clean. If someone edited /opt/sluicio in place,
# `git pull --ff-only` would bail mid-update and leave things half-
# updated. Catching it up front gives a cleaner error.
if [[ -n "$(git status --porcelain)" ]]; then
    say "Working tree at $REPO_DIR is dirty:"
    git status --short | sed 's/^/      /'
    fail "Stash or commit local changes first. Refusing to update over a dirty tree."
fi

# ── show what's coming ────────────────────────────────────────────────

step "Fetching origin"

run git fetch --quiet origin

CURRENT_SHA=$(git rev-parse HEAD)
REMOTE_SHA=$(git rev-parse '@{upstream}' 2>/dev/null || git rev-parse origin/main)

if [[ "$CURRENT_SHA" == "$REMOTE_SHA" ]]; then
    say "Already up to date at $(git log -1 --oneline)."
    exit 0
fi

step "Changes coming from $(git log -1 --format='%h' "$CURRENT_SHA") → $(git log -1 --format='%h' "$REMOTE_SHA")"

# Commits the update will pick up
git log --oneline "$CURRENT_SHA..$REMOTE_SHA" | sed 's/^/      /' || true

# Files changed — used both to show the operator and to decide which
# deploy artifacts need redeploying.
CHANGED_FILES=$(git diff --name-only "$CURRENT_SHA..$REMOTE_SHA")
echo
echo "    Files changed: $(printf '%s\n' "$CHANGED_FILES" | wc -l | tr -d ' ') total"
if [[ -n "$CHANGED_FILES" ]]; then
    echo "$CHANGED_FILES" | head -20 | sed 's/^/      /'
    if [[ $(printf '%s\n' "$CHANGED_FILES" | wc -l) -gt 20 ]]; then
        echo "      … and $(($(printf '%s\n' "$CHANGED_FILES" | wc -l) - 20)) more"
    fi
fi

# Detect deploy-artifact touches up front so the operator can see them
# in the confirm prompt.
TOUCHED_CADDY=0; TOUCHED_SYSTEMD=0; TOUCHED_PGBACKUP=0; TOUCHED_FRONTEND=0; TOUCHED_GO=0
echo "$CHANGED_FILES" | grep -q '^deploy/server/Caddyfile.template$' && TOUCHED_CADDY=1
echo "$CHANGED_FILES" | grep -q '^deploy/server/sluicio\.service\(\.template\)\?$' && TOUCHED_SYSTEMD=1
echo "$CHANGED_FILES" | grep -q '^deploy/server/pg-backup.sh$' && TOUCHED_PGBACKUP=1
echo "$CHANGED_FILES" | grep -q '^frontend/' && TOUCHED_FRONTEND=1
echo "$CHANGED_FILES" | grep -qE '^(services/|pkg/|go\.)' && TOUCHED_GO=1

echo
echo "    Artifacts that will be redeployed:"
(( TOUCHED_GO ))       && echo "      ✓ Go services (docker rebuild)"
(( TOUCHED_FRONTEND )) && (( ! SKIP_FRONTEND )) && echo "      ✓ Frontend (npm rebuild)"
(( TOUCHED_FRONTEND )) && (( SKIP_FRONTEND ))   && echo "      ⊗ Frontend (--no-frontend, skipped)"
(( TOUCHED_CADDY ))    && echo "      ✓ Caddyfile (re-render + reload)"
(( TOUCHED_SYSTEMD ))  && echo "      ✓ sluicio.service (reinstall + daemon-reload)"
(( TOUCHED_PGBACKUP )) && echo "      ✓ pg-backup script (reinstall)"
true

# ── confirm ───────────────────────────────────────────────────────────

if (( ! ASSUME_YES )) && (( ! DRY_RUN )); then
    echo
    read -r -p "Proceed with update? [y/N] " confirm
    case "$confirm" in
        y|Y|yes|YES) ;;
        *) fail "Aborted." ;;
    esac
fi

# ── optional snapshot ─────────────────────────────────────────────────

if (( SNAPSHOT )); then
    step "Pre-update Postgres snapshot"
    if [[ -x /usr/local/bin/sluicio-pg-backup ]]; then
        run /usr/local/bin/sluicio-pg-backup
    else
        say "WARNING: sluicio-pg-backup not installed; skipping snapshot."
        say "         If the update fails, you'll only have last night's dump."
    fi
fi

# ── pull ──────────────────────────────────────────────────────────────

step "Pulling code"
run git pull --ff-only

# ── re-deploy artifacts that changed ──────────────────────────────────

if (( TOUCHED_CADDY )); then
    step "Re-render Caddyfile (template changed)"
    # bootstrap.sh already stored DOMAIN / EMAIL in the rendered Caddyfile;
    # we recover them by re-extracting and re-applying with the new template.
    if [[ ! -f /etc/caddy/Caddyfile ]]; then
        fail "No /etc/caddy/Caddyfile to update from — has bootstrap.sh run?"
    fi
    # Extract the first 'foo.example.com {' line and the 'email …' line
    # from the live config. Fragile if you've hand-edited the rendered
    # Caddyfile heavily; print a warning if extraction fails.
    DOMAIN=$(grep -oE '^[a-z0-9.-]+\.[a-z]{2,} \{' /etc/caddy/Caddyfile | head -1 | sed 's/ .*//')
    EMAIL=$(grep -oE 'email [^ ]+' /etc/caddy/Caddyfile | head -1 | awk '{print $2}')
    if [[ -z "$DOMAIN" || -z "$EMAIL" ]]; then
        say "WARNING: couldn't recover DOMAIN / email from the existing Caddyfile."
        say "         Re-run bootstrap.sh --domain ... --email ... to apply template changes."
    else
        say "Re-rendering for $DOMAIN ($EMAIL)"
        run "sed -e 's|__DOMAIN__|$DOMAIN|g' \
                 -e 's|__LETSENCRYPT_EMAIL__|$EMAIL|g' \
                 -e 's|__FRONTEND_ROOT__|$FRONTEND_ROOT|g' \
                 '$REPO_DIR/deploy/server/Caddyfile.template' > /etc/caddy/Caddyfile"
        # Validate before reloading so a broken template doesn't take
        # Caddy offline mid-update.
        if ! caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1; then
            fail "Generated Caddyfile fails validation; not reloading. Check /etc/caddy/Caddyfile."
        fi
        run systemctl reload caddy
    fi
fi

if (( TOUCHED_SYSTEMD )); then
    step "Reinstall systemd unit (sluicio.service template changed)"
    # The unit is templated on the container runtime. Recover the
    # runtime from the env file the bootstrap wrote; fall back to
    # docker for back-compat with pre-podman bootstraps.
    SLUICIO_RUNTIME=""
    if [[ -f "$ENV_FILE" ]]; then
        # shellcheck disable=SC1090
        SLUICIO_RUNTIME=$(grep -E '^SLUICIO_RUNTIME=' "$ENV_FILE" | head -1 | cut -d= -f2)
    fi
    RUNTIME="${SLUICIO_RUNTIME:-docker}"
    say "Rendering for runtime: $RUNTIME"
    if [[ "$RUNTIME" == "docker" ]]; then
        RUNTIME_REQUIRES="Requires=docker.service"
        RUNTIME_AFTER="docker.service network-online.target"
    else
        RUNTIME_REQUIRES=""
        RUNTIME_AFTER="network-online.target"
    fi
    run "sed -e 's|__RUNTIME__|$RUNTIME|g' \
             -e 's|__RUNTIME_REQUIRES__|$RUNTIME_REQUIRES|g' \
             -e 's|__RUNTIME_AFTER__|$RUNTIME_AFTER|g' \
             '$REPO_DIR/deploy/server/sluicio.service.template' \
         > /etc/systemd/system/sluicio.service"
    run chmod 644 /etc/systemd/system/sluicio.service
    run systemctl daemon-reload
fi

if (( TOUCHED_PGBACKUP )); then
    step "Reinstall pg-backup script"
    run install -m 755 \
        "$REPO_DIR/deploy/server/pg-backup.sh" \
        /usr/local/bin/sluicio-pg-backup
fi

# ── frontend ──────────────────────────────────────────────────────────

if (( TOUCHED_FRONTEND )) && (( ! SKIP_FRONTEND )); then
    step "Rebuild frontend"
    # Run npm as the sluicio user to keep ownership consistent with the
    # bootstrap-time build. Without this the node_modules end up
    # root-owned and the next update fails with EACCES.
    if id sluicio >/dev/null 2>&1; then
        run "sudo -u sluicio bash -c \"cd '$REPO_DIR/frontend' && npm ci && npm run build\""
    else
        say "WARNING: no 'sluicio' user found; building frontend as root."
        run "cd '$REPO_DIR/frontend' && npm ci && npm run build"
    fi
    run rsync -a --delete "$REPO_DIR/frontend/dist/" "$FRONTEND_ROOT/"
fi

# ── restart the stack ─────────────────────────────────────────────────

step "Rebuild + restart containers"
# systemctl reload sluicio runs `docker compose up -d --build` under the
# hood (see sluicio.service's ExecReload). Compose then picks up any Go
# source changes via the build context.
run systemctl reload sluicio

# ── wait + report ─────────────────────────────────────────────────────

if (( DRY_RUN )); then
    step "Dry run complete. No changes applied."
    exit 0
fi

step "Waiting for cell-api healthcheck (up to 60s)"

for _ in {1..30}; do
    if curl -fsS http://127.0.0.1:8081/api/v1/auth/install-state >/dev/null 2>&1; then
        step "Update complete."
        echo
        echo "    Now at $(git log -1 --oneline)"
        echo "    Tail logs:   journalctl -u sluicio -f"
        echo "    Rollback:    cd $REPO_DIR && sudo git reset --hard $CURRENT_SHA && sudo $0 --yes"
        echo "    (rollback re-runs this script against the PRIOR commit; data is unchanged)"
        echo
        exit 0
    fi
    sleep 2
done

echo
fail "cell-api didn't come up in 60s.
    Check:    journalctl -u sluicio -n 200 --no-pager
    Rollback: cd $REPO_DIR && sudo git reset --hard $CURRENT_SHA && sudo $0 --yes
    (data volumes are untouched; rollback is just a code revert + restart)"
