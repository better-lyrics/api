#!/bin/bash
# Better Lyrics API - bootstrap a fresh Ubuntu 24.04 box from zero to production.
# Usage: sudo ./bootstrap.sh [--phase NN]
#   With --phase NN, runs only that single phase (e.g. --phase 03 to reconfigure Caddy).
# Source: secrets.env (must exist next to this script).
# Logs: /var/log/bli-bootstrap.log

set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG=/var/log/bli-bootstrap.log
SECRETS="${INFRA_DIR}/secrets.env"

if [ "$EUID" -ne 0 ]; then
    echo "ERROR: bootstrap.sh must run as root (use sudo)" >&2
    exit 1
fi

if [ ! -f "$SECRETS" ]; then
    echo "ERROR: secrets.env not found at $SECRETS" >&2
    echo "Copy secrets.env.example to secrets.env and fill in values." >&2
    exit 1
fi

# shellcheck disable=SC1090
set -a; source "$SECRETS"; set +a

REQUIRED_VARS=(
    CF_API_TOKEN ACME_EMAIL
    B2_KEY_ID B2_APP_KEY B2_BUCKET
    BESZEL_HUB_URL BESZEL_AGENT_KEY BESZEL_AGENT_TOKEN BESZEL_AGENT_PORT
    LOGDY_UI_PASS LOGDY_VERSION
    PUBLIC_IP PRIMARY_DOMAIN STAGING_DOMAIN LOGS_DOMAIN METRICS_DOMAIN KEEP_DOMAIN PREVIEW_WILDCARD
)
missing=()
for v in "${REQUIRED_VARS[@]}"; do
    if [ -z "${!v:-}" ]; then missing+=("$v"); fi
done
if [ ${#missing[@]} -gt 0 ]; then
    echo "ERROR: missing required secrets: ${missing[*]}" >&2
    exit 1
fi

export INFRA_DIR

mkdir -p "$(dirname "$LOG")"
exec > >(tee -a "$LOG") 2>&1

ONLY_PHASE=""
if [ "${1:-}" = "--phase" ]; then
    ONLY_PHASE="${2:?--phase requires a number}"
fi

run_phase() {
    local script="$1"
    local name
    name=$(basename "$script")
    echo
    echo "===> $name $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    bash "$script"
    echo "<=== $name OK"
}

echo "=== Better Lyrics API bootstrap ==="
echo "Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "Host: $(hostname)"
echo "Infra dir: $INFRA_DIR"
echo

for script in "$INFRA_DIR"/phases/[0-9][0-9]-*.sh; do
    if [ -n "$ONLY_PHASE" ]; then
        case "$(basename "$script")" in
            "${ONLY_PHASE}-"*) run_phase "$script" ;;
        esac
    else
        run_phase "$script"
    fi
done

echo
echo "=== bootstrap complete ==="
echo "Verify with:"
echo "  systemctl is-active caddy lyrics-api infisical-agent beszel-agent logdy fail2ban"
echo "  curl -sI https://${PRIMARY_DOMAIN}/health"
