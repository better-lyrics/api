#!/bin/bash
set -euo pipefail

# Install Infisical CLI from official apt repo
if ! command -v infisical > /dev/null; then
    curl -1sLf 'https://artifacts-cli.infisical.com/setup.deb.sh' | bash
    apt-get install -y infisical
fi

install -d -m 755 /etc/infisical-agent

# Auth files (mode 600, root only)
printf '%s' "$INFISICAL_CLIENT_ID"     > /etc/infisical-agent/client-id
printf '%s' "$INFISICAL_CLIENT_SECRET" > /etc/infisical-agent/client-secret
chmod 600 /etc/infisical-agent/client-id /etc/infisical-agent/client-secret
chown root:root /etc/infisical-agent/client-id /etc/infisical-agent/client-secret

# Agent config
install -m 644 -o root -g root "$INFRA_DIR/files/infisical/agent.yaml" /etc/infisical-agent/agent.yaml

# Template - substitute project id + env from secrets.env
sed -e "s|__INFISICAL_PROJECT_ID__|${INFISICAL_PROJECT_ID}|g" \
    -e "s|__INFISICAL_ENV__|${INFISICAL_ENV}|g" \
    "$INFRA_DIR/files/infisical/lyrics-api.env.tpl" > /etc/infisical-agent/lyrics-api.env.tpl
chmod 644 /etc/infisical-agent/lyrics-api.env.tpl

# Reload script + systemd unit
install -m 755 -o root -g root \
    "$INFRA_DIR/files/infisical/lyrics-api-reload" \
    /usr/local/bin/lyrics-api-reload

install -m 644 -o root -g root \
    "$INFRA_DIR/files/infisical/infisical-agent.service" \
    /etc/systemd/system/infisical-agent.service

systemctl daemon-reload
systemctl enable --now infisical-agent

# Wait briefly for first sync, then trigger reload script if staging file exists
sleep 5
if [ -s /etc/lyrics-api.env.staging ]; then
    /usr/local/bin/lyrics-api-reload || echo "WARN: initial reload failed, check journalctl -t infisical-agent"
fi

echo "OK: infisical-agent running and synced"
