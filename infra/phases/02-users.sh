#!/bin/bash
set -euo pipefail

# deploy user runs lyrics-api and owns its data dirs
if ! id deploy > /dev/null 2>&1; then
    useradd --system --create-home --shell /bin/bash deploy
fi

# Working dirs
install -d -o deploy -g deploy -m 755 /opt/lyrics-api
install -d -o deploy -g deploy -m 755 /opt/lyrics-api-previews
install -d -o deploy -g deploy -m 755 /var/lib/lyrics-api
install -d -o deploy -g deploy -m 755 /var/lib/lyrics-api/data
install -d -o deploy -g deploy -m 755 /var/lib/lyrics-api/backups

# Preview infra
install -d -o root -g root -m 755 /etc/lyrics-api-previews
install -d -o root -g root -m 755 /etc/caddy/previews

echo "OK: deploy user + working dirs"
