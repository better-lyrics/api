#!/bin/bash
set -euo pipefail

# Install systemd units. The binary itself is deployed separately
# (out of band: scp from CI, or rebuild from source on the box).
# bootstrap places a placeholder if no binary exists, so systemctl can validate the unit.

install -m 644 -o root -g root \
    "$INFRA_DIR/files/lyrics-api/lyrics-api.service" \
    /etc/systemd/system/lyrics-api.service

install -d -m 755 /etc/systemd/system/lyrics-api.service.d
install -m 644 -o root -g root \
    "$INFRA_DIR/files/lyrics-api/memory.conf" \
    /etc/systemd/system/lyrics-api.service.d/memory.conf

# Preview template unit (used by .github/workflows/preview.yml via preview-deploy.sh)
install -m 644 -o root -g root \
    "$INFRA_DIR/../scripts/preview/lyrics-api@.service" \
    /etc/systemd/system/lyrics-api@.service

systemctl daemon-reload

# Don't start lyrics-api here: the binary may not exist yet on a fresh box.
# After this phase, deploy the binary to /opt/lyrics-api/lyrics-api-go and
# Infisical (phase 05) will write /etc/lyrics-api.env, which triggers the first start.

if [ -x /opt/lyrics-api/lyrics-api-go ] && [ -f /etc/lyrics-api.env ]; then
    systemctl enable --now lyrics-api
    echo "OK: lyrics-api started (binary + env present)"
else
    systemctl enable lyrics-api || true
    echo "OK: lyrics-api unit installed (deferred start - binary or /etc/lyrics-api.env not present yet)"
fi
