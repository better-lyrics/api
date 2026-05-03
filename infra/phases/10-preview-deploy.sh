#!/bin/bash
set -euo pipefail

# Preview deploy/teardown scripts (referenced by .github/workflows/preview.yml)
install -m 755 -o root -g root \
    "$INFRA_DIR/../scripts/preview/preview-deploy.sh" \
    /usr/local/bin/preview-deploy.sh
install -m 755 -o root -g root \
    "$INFRA_DIR/../scripts/preview/preview-teardown.sh" \
    /usr/local/bin/preview-teardown.sh

# Config consumed by preview-deploy.sh: derive the wildcard base from secrets.env's PREVIEW_WILDCARD (strip leading "*.")
PREVIEW_BASE="${PREVIEW_WILDCARD#\*.}"
install -m 644 -o root -g root /dev/null /etc/preview-deploy.env
printf 'PREVIEW_BASE=%s\n' "$PREVIEW_BASE" > /etc/preview-deploy.env

# Sudoers entry: deploy user can run preview scripts NOPASSWD
install -m 440 -o root -g root \
    "$INFRA_DIR/files/lyrics-api/sudoers.deploy-preview" \
    /etc/sudoers.d/deploy-preview

# Validate sudoers syntax
visudo -c -f /etc/sudoers.d/deploy-preview > /dev/null

echo "OK: preview deploy/teardown scripts + sudoers entry + preview-deploy.env"
