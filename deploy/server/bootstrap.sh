#!/usr/bin/env bash
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Bootstrap a fresh Ubuntu 24.04 LTS box into a single-server Sluicio
# install. Idempotent — safe to re-run.
#
# Prerequisites (do these BEFORE running the script):
#
#   1. Fresh Ubuntu 24.04 LTS server with sudo access
#   2. A DNS A record pointing your domain at this server's public IP,
#      AND a second A record for ingest.<domain> at the same IP
#   3. SSH key access working — DO NOT skip; the script disables
#      password SSH and you'll lock yourself out
#   4. This repo cloned to /opt/sluicio (or pass --repo-dir)
#
# Usage:
#
#   sudo deploy/server/bootstrap.sh \
#       --domain sluicio.example.com \
#       --email  admin@example.com
#
# Re-running the script with the same args is a no-op for most steps;
# regenerating secrets or rewriting the Caddyfile only happens when
# the relevant flag is passed explicitly.

set -euo pipefail

# ── argument parsing ──────────────────────────────────────────────────

DOMAIN=""
LETSENCRYPT_EMAIL=""
REPO_DIR="/opt/sluicio"
FRONTEND_ROOT="/var/www/sluicio"
DATA_ROOT="/var/lib/sluicio"
ENV_FILE="/etc/sluicio/sluicio.env"
SSH_PORT="22"
FORCE_REGEN_SECRETS=0
# Container runtime. Podman is the default because it runs without a
# privileged daemon and doesn't require a docker-group that's
# effectively root-equivalent. Pass --runtime=docker if you'd rather
# stay on Docker (more familiar tooling, slightly more compose
# compatibility on edge cases).
RUNTIME="podman"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --domain)        DOMAIN="$2"; shift 2 ;;
        --email)         LETSENCRYPT_EMAIL="$2"; shift 2 ;;
        --repo-dir)      REPO_DIR="$2"; shift 2 ;;
        --frontend-root) FRONTEND_ROOT="$2"; shift 2 ;;
        --data-root)     DATA_ROOT="$2"; shift 2 ;;
        --ssh-port)      SSH_PORT="$2"; shift 2 ;;
        --regen-secrets) FORCE_REGEN_SECRETS=1; shift ;;
        --runtime)       RUNTIME="$2"; shift 2 ;;
        --runtime=*)     RUNTIME="${1#*=}"; shift ;;
        -h|--help)
            sed -n '1,40p' "$0"
            exit 0
            ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$DOMAIN" || -z "$LETSENCRYPT_EMAIL" ]]; then
    echo "Required: --domain and --email" >&2
    exit 1
fi

case "$RUNTIME" in
    podman|docker) ;;
    *) echo "--runtime must be 'podman' or 'docker' (got: $RUNTIME)" >&2; exit 1 ;;
esac

if [[ $EUID -ne 0 ]]; then
    echo "Run as root (sudo)." >&2
    exit 1
fi

# ── helpers ────────────────────────────────────────────────────────────

step() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
say()  { printf '    %s\n' "$*"; }
fail() { printf '\033[1;31m!!\033[0m %s\n' "$*" >&2; exit 1; }

random_password() {
    # 32 chars from a URL-safe set. openssl is in coreutils on
    # Ubuntu, no extra install needed.
    openssl rand -base64 24 | tr -d '/+=' | head -c 32
}

# ── pre-flight ─────────────────────────────────────────────────────────

step "Pre-flight checks"

. /etc/os-release
if [[ "$ID" != "ubuntu" ]]; then
    say "WARNING: tested on Ubuntu only — you're on $ID $VERSION_ID."
    say "         The script may still work; bail if anything breaks."
fi

if [[ ! -d "$REPO_DIR/.git" ]]; then
    fail "Repo not found at $REPO_DIR. Clone it first: git clone https://github.com/ROMA-IT-AB/Sluicio.git $REPO_DIR"
fi

# ── apt packages ───────────────────────────────────────────────────────

step "Install system packages"

# Caddy isn't in the default repos; add its official one.
if ! command -v caddy >/dev/null 2>&1; then
    say "Adding Caddy apt repo"
    apt-get update -qq
    apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl
    curl -fsSL "https://dl.cloudsmith.io/public/caddy/stable/gpg.key" \
        | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -fsSL "https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt" \
        > /etc/apt/sources.list.d/caddy-stable.list
fi

# Node for the frontend build. Use NodeSource for a current LTS; the
# Ubuntu-shipped node is too old for Vite 5.
if ! command -v node >/dev/null 2>&1 || (( $(node -v | tr -d 'v' | cut -d. -f1) < 20 )); then
    say "Adding NodeSource apt repo"
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
fi

apt-get update -qq

# Common packages first.
apt-get install -y -qq \
    caddy \
    ufw fail2ban \
    jq git rsync \
    nodejs \
    postgresql-client

# Runtime-specific packages. Both runtimes provide a `compose`
# subcommand on the targeted distros (Docker via the official
# docker-compose-plugin, Podman via the podman-compose package that
# ships with Ubuntu 24.04 / Debian 12+ and registers as
# `podman compose`).
case "$RUNTIME" in
    docker)
        say "Installing Docker"
        apt-get install -y -qq docker.io docker-compose-plugin
        systemctl enable --now docker
        ;;
    podman)
        say "Installing Podman"
        # podman: the engine. podman-compose: the compose-file
        # parser, registered as `podman compose`. uidmap: required
        # for the unprivileged user namespaces Podman uses even when
        # running as root (it still leverages userns for isolation).
        apt-get install -y -qq podman podman-compose uidmap
        # No daemon to enable — podman is daemonless. The systemd
        # unit we install below just exec's podman directly.
        ;;
esac

# ── system user + directories ─────────────────────────────────────────

step "System user + data directories"

if ! id sluicio >/dev/null 2>&1; then
    useradd --system --create-home --shell /bin/bash sluicio
    say "Created user 'sluicio'"
fi

# Docker requires the user to be in the 'docker' group to run
# unprivileged. Podman doesn't (it's daemonless), so the user gets no
# special group on the podman path — one fewer privilege-escalation
# surface.
if [[ "$RUNTIME" == "docker" ]]; then
    usermod -aG docker sluicio
    say "Added 'sluicio' to the docker group"
fi

# Repo can be owned by sluicio so update.sh runs without root.
chown -R sluicio:sluicio "$REPO_DIR"

mkdir -p "$DATA_ROOT"/{postgres,clickhouse,backups}
chown -R sluicio:sluicio "$DATA_ROOT"
# Postgres + ClickHouse containers run as their own users inside;
# the bind-mount maps the right ownership through. The 'sluicio'
# parent ownership lets backup scripts traverse without sudo.
chmod 750 "$DATA_ROOT"

mkdir -p "$FRONTEND_ROOT"
chown -R sluicio:sluicio "$FRONTEND_ROOT"

# Caddy needs to read the frontend root. It runs as the 'caddy' user
# on Ubuntu; give it group read.
chmod 755 "$FRONTEND_ROOT"

mkdir -p /var/log/caddy
chown -R caddy:caddy /var/log/caddy

# ── secrets ───────────────────────────────────────────────────────────

step "Generate / load secrets"

mkdir -p "$(dirname "$ENV_FILE")"
chmod 700 "$(dirname "$ENV_FILE")"

if [[ ! -f "$ENV_FILE" ]] || (( FORCE_REGEN_SECRETS )); then
    # On first install, mint random passwords for Postgres + ClickHouse.
    # If --regen-secrets, overwrite — useful for "this box was compromised,
    # rotate everything." Won't help if the data was leaked, but at least
    # closes the door going forward.
    POSTGRES_USER="sluicio"
    POSTGRES_PASSWORD=$(random_password)
    CLICKHOUSE_USER="sluicio"
    CLICKHOUSE_PASSWORD=$(random_password)

    cat > "$ENV_FILE" <<EOF
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Sluicio cell secrets + runtime config. Generated by
# deploy/server/bootstrap.sh. Mode 600 — DO NOT widen permissions.
# Container env_file= reads this directly; no copying needed.
POSTGRES_USER=$POSTGRES_USER
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
CLICKHOUSE_USER=$CLICKHOUSE_USER
CLICKHOUSE_PASSWORD=$CLICKHOUSE_PASSWORD

# Container runtime — read by sluicio.service, update.sh, and
# pg-backup.sh. Don't edit this by hand; re-run bootstrap.sh with
# --runtime=<docker|podman> to switch (which also handles uninstalling
# the other runtime's apt packages).
SLUICIO_RUNTIME=$RUNTIME
EOF
    chmod 600 "$ENV_FILE"
    chown root:root "$ENV_FILE"
    say "Wrote new secrets + runtime ($RUNTIME) to $ENV_FILE"
else
    # Update just the runtime line if it changed (don't rotate secrets
    # on a re-run without --regen-secrets).
    if grep -q '^SLUICIO_RUNTIME=' "$ENV_FILE"; then
        sed -i "s|^SLUICIO_RUNTIME=.*|SLUICIO_RUNTIME=$RUNTIME|" "$ENV_FILE"
    else
        echo "SLUICIO_RUNTIME=$RUNTIME" >> "$ENV_FILE"
    fi
    say "Reusing existing secrets in $ENV_FILE (runtime: $RUNTIME)"
fi

# ── Caddyfile ─────────────────────────────────────────────────────────

step "Configure Caddy"

CADDYFILE_TEMPLATE="$REPO_DIR/deploy/server/Caddyfile.template"
if [[ ! -f "$CADDYFILE_TEMPLATE" ]]; then
    fail "Missing $CADDYFILE_TEMPLATE — broken repo state?"
fi

# Template substitution: simple sed because all our placeholders are
# safe characters (domain, email, path).
sed -e "s|__DOMAIN__|$DOMAIN|g" \
    -e "s|__LETSENCRYPT_EMAIL__|$LETSENCRYPT_EMAIL|g" \
    -e "s|__FRONTEND_ROOT__|$FRONTEND_ROOT|g" \
    "$CADDYFILE_TEMPLATE" > /etc/caddy/Caddyfile

# Don't restart Caddy if the cert dance is mid-flight; reload reads
# the new config without dropping connections.
if systemctl is-active --quiet caddy; then
    systemctl reload caddy
else
    systemctl enable --now caddy
fi
say "Caddy reloaded with config for $DOMAIN (+ ingest.$DOMAIN)"

# ── build frontend ────────────────────────────────────────────────────

step "Build frontend"

# Run npm as the sluicio user so package-lock + node_modules ownership
# matches subsequent update.sh runs.
sudo -u sluicio bash -c "
    cd '$REPO_DIR/frontend' &&
    npm ci &&
    npm run build
"
rsync -a --delete "$REPO_DIR/frontend/dist/" "$FRONTEND_ROOT/"
chown -R sluicio:sluicio "$FRONTEND_ROOT"
say "Frontend bundle deployed to $FRONTEND_ROOT"

# ── systemd unit ──────────────────────────────────────────────────────

step "Install systemd unit"

# The unit is templated on the runtime so Podman doesn't drag in a
# Requires=docker.service line (there's no daemon to require).
SERVICE_TEMPLATE="$REPO_DIR/deploy/server/sluicio.service.template"
if [[ ! -f "$SERVICE_TEMPLATE" ]]; then
    fail "Missing $SERVICE_TEMPLATE — broken repo state?"
fi

if [[ "$RUNTIME" == "docker" ]]; then
    RUNTIME_REQUIRES="Requires=docker.service"
    RUNTIME_AFTER="docker.service network-online.target"
else
    # Podman: daemonless, so no Requires=. We still want to come up
    # AFTER network-online so the container's network setup doesn't
    # race with the host's interfaces.
    RUNTIME_REQUIRES=""
    RUNTIME_AFTER="network-online.target"
fi

sed -e "s|__RUNTIME__|$RUNTIME|g" \
    -e "s|__RUNTIME_REQUIRES__|$RUNTIME_REQUIRES|g" \
    -e "s|__RUNTIME_AFTER__|$RUNTIME_AFTER|g" \
    "$SERVICE_TEMPLATE" > /etc/systemd/system/sluicio.service
chmod 644 /etc/systemd/system/sluicio.service
systemctl daemon-reload
systemctl enable sluicio
say "sluicio.service installed (runtime: $RUNTIME) and enabled"

# ── backup cron ───────────────────────────────────────────────────────

step "Install pg-backup cron"

install -m 755 "$REPO_DIR/deploy/server/pg-backup.sh" /usr/local/bin/sluicio-pg-backup
# Daily at 03:00 server time, output captured by journald.
cat > /etc/cron.d/sluicio-pg-backup <<EOF
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
# Daily Postgres dump for Sluicio.
0 3 * * * root /usr/local/bin/sluicio-pg-backup 2>&1 | logger -t sluicio-pg-backup
EOF
say "pg-backup scheduled daily at 03:00 (logs to journal: journalctl -t sluicio-pg-backup)"

# ── firewall ──────────────────────────────────────────────────────────

step "Configure ufw"

ufw default deny incoming
ufw default allow outgoing
ufw allow "$SSH_PORT"/tcp comment 'ssh'
ufw allow 80/tcp comment 'http'
ufw allow 443/tcp comment 'https'
ufw --force enable
say "ufw active: allow $SSH_PORT, 80, 443; deny everything else"

# ── fail2ban ──────────────────────────────────────────────────────────

step "Configure fail2ban"

# Ubuntu's default jail.conf enables sshd already; we just make sure
# the service is running. Custom jails go in /etc/fail2ban/jail.d/.
systemctl enable --now fail2ban

# ── SSH hardening ─────────────────────────────────────────────────────
#
# CAUTION: this disables password auth. The script bails if no
# authorized_keys file exists for any non-system user — running it on
# a key-less box would lock the operator out.

step "SSH hardening"

if ! find /home -mindepth 2 -name authorized_keys -size +0 2>/dev/null | grep -q .; then
    fail "No /home/*/.ssh/authorized_keys with content found. Set up SSH key access BEFORE running this step, or it'll lock you out."
fi

SSHD_CONF="/etc/ssh/sshd_config.d/99-sluicio.conf"
cat > "$SSHD_CONF" <<EOF
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
# Installed by Sluicio bootstrap.sh — disables password auth, locks down
# common foot-guns. Edit at your own risk.
PasswordAuthentication no
PermitRootLogin prohibit-password
ChallengeResponseAuthentication no
UsePAM yes
EOF
chmod 644 "$SSHD_CONF"
# Validate before restarting so a typo doesn't lock SSH down.
if ! sshd -t; then
    fail "sshd config invalid — review $SSHD_CONF"
fi
systemctl reload ssh
say "Password SSH disabled; keys only from now on"

# ── start the stack ────────────────────────────────────────────────────

step "Start Sluicio"

systemctl start sluicio
sleep 5

# Wait for the cell-api healthcheck endpoint (install-state is public).
say "Waiting for cell-api to come up..."
for _ in {1..30}; do
    if curl -fsS http://127.0.0.1:8081/api/v1/auth/install-state >/dev/null 2>&1; then
        break
    fi
    sleep 2
done
if ! curl -fsS http://127.0.0.1:8081/api/v1/auth/install-state >/dev/null 2>&1; then
    say "cell-api didn't come up in 60s — check: journalctl -u sluicio -n 200"
    say "(continuing — the rest of the bootstrap is independent)"
fi

# ── done ──────────────────────────────────────────────────────────────

step "Done"

cat <<EOF

  Sluicio is bootstrapped.

  Web UI:        https://$DOMAIN/
  OTLP ingest:   https://ingest.$DOMAIN/v1/{traces,logs,metrics}

  First login:   admin@sluicio.local
                 password: admin
                 — CHANGE THIS IMMEDIATELY via Account → Password.

  Logs:          journalctl -u sluicio -f
  Caddy logs:    journalctl -u caddy -f
                 + /var/log/caddy/{access,ingest}.log

  Update later:  sudo $REPO_DIR/deploy/server/update.sh
  Rotate secrets: sudo $0 --domain $DOMAIN --email $LETSENCRYPT_EMAIL --regen-secrets

  pg-backup runs nightly at 03:00 UTC.
  Dumps are at $DATA_ROOT/backups/ — copy them off-machine on a schedule.

EOF
