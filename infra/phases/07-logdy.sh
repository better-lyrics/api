#!/bin/bash
set -euo pipefail

# Install pinned Logdy binary
INSTALLED_VERSION=""
if [ -x /usr/local/bin/logdy ]; then
    INSTALLED_VERSION=$(/usr/local/bin/logdy --version 2>/dev/null | awk '{print $NF}')
fi

if [ "$INSTALLED_VERSION" != "$LOGDY_VERSION" ]; then
    ARCH=$(dpkg --print-architecture)  # arm64 or amd64
    case "$ARCH" in
        arm64) LOGDY_ARCH=arm64 ;;
        amd64) LOGDY_ARCH=amd64 ;;
        *) echo "ERROR: unsupported arch $ARCH"; exit 1 ;;
    esac
    URL="https://github.com/logdyhq/logdy-core/releases/download/v${LOGDY_VERSION}/logdy_linux_${LOGDY_ARCH}"
    curl -fsSL "$URL" -o /usr/local/bin/logdy
    chmod 755 /usr/local/bin/logdy
fi

# /etc/logdy.env (mode 640, deploy can read)
install -m 640 -o root -g deploy /dev/null /etc/logdy.env
printf 'LOGDY_UI_PASS=%s\n' "$LOGDY_UI_PASS" > /etc/logdy.env
chmod 640 /etc/logdy.env
chown root:deploy /etc/logdy.env

install -m 644 -o root -g root \
    "$INFRA_DIR/files/logdy/logdy.service" \
    /etc/systemd/system/logdy.service

systemctl daemon-reload
systemctl enable --now logdy

echo "OK: logdy v${LOGDY_VERSION} running on localhost:8888"
