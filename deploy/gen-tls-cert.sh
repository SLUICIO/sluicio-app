#!/usr/bin/env bash
# Generate a local-CA wildcard TLS cert for a LAN-only domain.
#
# For internal use where you DON'T own the public domain: a private CA
# you trust on your own clients can sign a cert for any name — domain
# ownership only matters for public CAs (Let's Encrypt). Default domain
# is example.com; override for any name.
#
#   ./deploy/gen-tls-cert.sh                  # → *.example.com
#   DOMAIN=lab.internal ./deploy/gen-tls-cert.sh
#
# Env:
#   DOMAIN   apex domain (default example.com); cert covers DOMAIN + *.DOMAIN
#   OUTDIR   output directory (default ./certs)
#   DAYS     leaf validity in days (default 825 — the max some clients accept)
#   CA_DAYS  CA validity in days (default 3650)
#
# Re-running REUSES an existing CA in OUTDIR so already-distributed client
# trust stays valid, and only re-issues the leaf. Delete OUTDIR/rootCA.*
# to rotate the CA (then re-distribute it to clients).
set -euo pipefail

DOMAIN="${DOMAIN:-example.com}"
OUTDIR="${OUTDIR:-./certs}"
DAYS="${DAYS:-825}"
CA_DAYS="${CA_DAYS:-3650}"

mkdir -p "$OUTDIR"
cd "$OUTDIR"

# 1. Root CA — created once, reused on re-run so client trust survives.
if [ -f rootCA.crt ] && [ -f rootCA.key ]; then
  echo "==> reusing existing CA (rootCA.crt)"
else
  echo "==> creating local CA (rootCA.crt, ${CA_DAYS}d)"
  openssl genrsa -out rootCA.key 4096
  openssl req -x509 -new -nodes -key rootCA.key -sha256 -days "$CA_DAYS" \
    -subj "/CN=${DOMAIN} Local CA" -out rootCA.crt
fi

# 2. Leaf wildcard cert. SAN is mandatory for modern clients, and must be
#    in the issued cert — so we pass it via -extfile at signing time
#    (a SAN on the CSR alone would not carry over).
ext="$(mktemp)"; trap 'rm -f "$ext"' EXIT
cat > "$ext" <<EXT
subjectAltName=DNS:${DOMAIN},DNS:*.${DOMAIN}
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EXT

echo "==> issuing leaf for ${DOMAIN} + *.${DOMAIN} (${DAYS}d)"
openssl genrsa -out "${DOMAIN}.key" 2048
openssl req -new -key "${DOMAIN}.key" -subj "/CN=*.${DOMAIN}" -out "${DOMAIN}.csr"
openssl x509 -req -in "${DOMAIN}.csr" -CA rootCA.crt -CAkey rootCA.key \
  -CAcreateserial -days "$DAYS" -sha256 -extfile "$ext" -out "${DOMAIN}.crt"

# nginx should serve leaf + CA for a complete chain.
cat "${DOMAIN}.crt" rootCA.crt > "${DOMAIN}.fullchain.crt"
chmod 600 "${DOMAIN}.key" rootCA.key 2>/dev/null || true
rm -f "${DOMAIN}.csr"

cat <<DONE

Done — files in $(pwd):
  ${DOMAIN}.fullchain.crt   ${DOMAIN}.key   → nginx ssl_certificate / ssl_certificate_key
  rootCA.crt                                → install in every client's trust store

nginx: see deploy/nginx/tls.conf.example (registry-aware).

Trust the CA on clients:
  Debian/RPi : sudo cp rootCA.crt /usr/local/share/ca-certificates/${DOMAIN}.crt && sudo update-ca-certificates
  macOS      : open rootCA.crt -> Keychain -> Always Trust
  podman VM  : copy rootCA.crt to /etc/containers/certs.d/docker.${DOMAIN}/ca.crt
               (or keep --tls-verify=false in publish.docker.sh)
DONE
