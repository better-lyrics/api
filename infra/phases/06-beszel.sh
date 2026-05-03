#!/bin/bash
set -euo pipefail

# Install Beszel agent via official installer (creates beszel user + systemd unit)
if ! systemctl list-unit-files beszel-agent.service > /dev/null 2>&1; then
    curl -sL https://get.beszel.dev/agent -o /tmp/install-agent.sh
    chmod +x /tmp/install-agent.sh
    /tmp/install-agent.sh \
        -p "$BESZEL_AGENT_PORT" \
        -k "$BESZEL_AGENT_KEY" \
        -t "$BESZEL_AGENT_TOKEN" \
        --hub-url "$BESZEL_HUB_URL"
    rm -f /tmp/install-agent.sh
fi

# Reconcile env vars in the unit (in case secrets rotated)
UNIT=/etc/systemd/system/beszel-agent.service
if [ -f "$UNIT" ]; then
    sed -i \
        -e "s|^Environment=\"PORT=.*\"|Environment=\"PORT=${BESZEL_AGENT_PORT}\"|" \
        -e "s|^Environment=\"KEY=.*\"|Environment=\"KEY=${BESZEL_AGENT_KEY}\"|" \
        -e "s|^Environment=\"TOKEN=.*\"|Environment=\"TOKEN=${BESZEL_AGENT_TOKEN}\"|" \
        -e "s|^Environment=\"HUB_URL=.*\"|Environment=\"HUB_URL=${BESZEL_HUB_URL}\"|" \
        "$UNIT"
    systemctl daemon-reload
    systemctl restart beszel-agent
fi

systemctl enable beszel-agent
echo "OK: beszel-agent registered with $BESZEL_HUB_URL"
