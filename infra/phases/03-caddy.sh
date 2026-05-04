#!/bin/bash
set -euo pipefail

# Install Caddy from official repo (idempotent)
if ! command -v caddy > /dev/null; then
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
        | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
        | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update
    apt-get install -y caddy
fi

# Add cloudflare DNS plugin (idempotent: caddy add-package detects duplicates)
if ! caddy list-modules 2>/dev/null | grep -q 'dns.providers.cloudflare'; then
    caddy add-package github.com/caddy-dns/cloudflare
fi

# Caddyfile (substitute placeholders at install time)
sed -e "s|__ACME_EMAIL__|${ACME_EMAIL}|g" \
    -e "s|__PRIMARY_DOMAIN__|${PRIMARY_DOMAIN}|g" \
    -e "s|__STAGING_DOMAIN__|${STAGING_DOMAIN}|g" \
    -e "s|__LOGS_DOMAIN__|${LOGS_DOMAIN}|g" \
    -e "s|__METRICS_DOMAIN__|${METRICS_DOMAIN}|g" \
    -e "s|__KEEP_DOMAIN__|${KEEP_DOMAIN}|g" \
    -e "s|__PREVIEW_WILDCARD__|${PREVIEW_WILDCARD}|g" \
    "$INFRA_DIR/files/caddy/Caddyfile" > /etc/caddy/Caddyfile
chmod 644 /etc/caddy/Caddyfile
chown root:root /etc/caddy/Caddyfile

# Drop-in to source CF_API_TOKEN from /etc/caddy.env (mode 600)
install -d -m 755 /etc/systemd/system/caddy.service.d
install -m 644 -o root -g root \
    "$INFRA_DIR/files/caddy/caddy.service.d/override.conf" \
    /etc/systemd/system/caddy.service.d/override.conf

# /etc/caddy.env: mode 600, root:caddy. Caddy must exist as a user by now (apt creates it).
install -m 600 -o root -g caddy /dev/null /etc/caddy.env
printf 'CF_API_TOKEN=%s\n' "$CF_API_TOKEN" > /etc/caddy.env
chmod 600 /etc/caddy.env
chown root:caddy /etc/caddy.env

systemctl daemon-reload
systemctl enable --now caddy
systemctl reload caddy

echo "OK: caddy configured (CF_API_TOKEN in /etc/caddy.env, mode 600 root:caddy)"
