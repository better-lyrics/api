#!/bin/bash
set -euo pipefail

PR="${1:-}"
[[ "$PR" =~ ^[0-9]+$ ]] || { echo "Usage: $0 <pr-number>"; exit 2; }

UNIT="lyrics-api@${PR}.service"

systemctl stop "$UNIT" 2>/dev/null || true
systemctl disable "$UNIT" 2>/dev/null || true

rm -f "/etc/lyrics-api-previews/pr-${PR}.env"
rm -f "/etc/caddy/previews/pr-${PR}.caddy"
rm -rf "/opt/lyrics-api-previews/pr-${PR}"
rm -rf "/tmp/lyrics-api-pr-${PR}-backups"

systemctl reload caddy 2>/dev/null || true

echo "Preview pr-${PR} torn down"
