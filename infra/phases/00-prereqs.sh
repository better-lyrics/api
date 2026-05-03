#!/bin/bash
set -euo pipefail

# Verify Ubuntu 24.04 (or compatible) and basic network
. /etc/os-release
case "$ID" in
    ubuntu|debian) ;;
    *) echo "WARN: untested distro $ID, proceeding anyway" ;;
esac

if ! ping -c 1 -W 2 1.1.1.1 > /dev/null 2>&1; then
    echo "ERROR: no network connectivity"
    exit 1
fi

if ! command -v curl > /dev/null; then
    apt-get update
    apt-get install -y curl
fi

echo "OK: $PRETTY_NAME, network reachable"
