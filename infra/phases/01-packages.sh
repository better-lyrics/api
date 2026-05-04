#!/bin/bash
set -euo pipefail

# Base packages: build/network/security tooling
DEBIAN_FRONTEND=noninteractive apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    curl wget ca-certificates gnupg lsb-release \
    ufw fail2ban \
    rclone jq git sqlite3 \
    apt-transport-https debian-archive-keyring

echo "OK: base packages installed"
