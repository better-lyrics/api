#!/bin/bash
set -euo pipefail

PR="${1:-}"
[[ "$PR" =~ ^[0-9]+$ ]] || { echo "Usage: $0 <pr-number>"; exit 2; }

# PREVIEW_BASE is the wildcard zone, e.g. "preview.api.example.com" so PR 42 lands at "pr-42.preview.api.example.com".
# Written by infra/phases/10-preview-deploy.sh from $PREVIEW_WILDCARD in secrets.env.
[ -f /etc/preview-deploy.env ] && source /etc/preview-deploy.env
: "${PREVIEW_BASE:?PREVIEW_BASE not set; expected /etc/preview-deploy.env to define it}"

UNIT="lyrics-api@${PR}.service"
PORT=$((9000 + PR))
DIR="/opt/lyrics-api-previews/pr-${PR}"
ENV_FILE="/etc/lyrics-api-previews/pr-${PR}.env"
CADDY_FILE="/etc/caddy/previews/pr-${PR}.caddy"
BIN_SRC="/tmp/lyrics-api-pr-${PR}"

if [ ! -f "$BIN_SRC" ]; then
    echo "ERROR: binary not found at $BIN_SRC"
    exit 1
fi

ACTIVE=$(systemctl list-units --state=active 'lyrics-api@*.service' --no-legend | wc -l)
if ! systemctl is-active --quiet "$UNIT" && [ "$ACTIVE" -ge 2 ]; then
    echo "ERROR: preview cap reached ($ACTIVE/2 active). Close another preview PR first."
    rm -f "$BIN_SRC"
    exit 1
fi

mkdir -p "$DIR" /etc/lyrics-api-previews /etc/caddy/previews

mv "$BIN_SRC" "$DIR/lyrics-api-go"
chmod +x "$DIR/lyrics-api-go"
chown -R deploy:deploy "$DIR"

cat > "$ENV_FILE" <<EOF
PORT=${PORT}
CACHE_DB_PATH=${DIR}/cache.db
CACHE_BACKUP_PATH=/tmp/lyrics-api-pr-${PR}-backups
STATS_DB_PATH=${DIR}/stats.db
EOF
chmod 640 "$ENV_FILE"
chown root:deploy "$ENV_FILE"

cat > "$CADDY_FILE" <<EOF
@pr${PR} host pr-${PR}.${PREVIEW_BASE}
handle @pr${PR} {
    reverse_proxy localhost:${PORT}
}
EOF

systemctl daemon-reload
systemctl restart "$UNIT"
systemctl reload caddy

for i in $(seq 1 30); do
    if curl -sf "http://localhost:${PORT}/health" > /dev/null 2>&1; then
        echo "Preview pr-${PR} healthy on port ${PORT}"
        exit 0
    fi
    sleep 1
done

echo "ERROR: health check timed out after 30s"
journalctl -u "$UNIT" --no-pager -n 30
exit 1
