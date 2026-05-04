#!/bin/bash
set -euo pipefail

# keep system user + data dir
if ! id keep >/dev/null 2>&1; then
    useradd -r -s /usr/sbin/nologin -d /var/lib/keep keep
fi
install -d -o keep -g keep -m 0750 /var/lib/keep

# systemd unit (substitute KEEP_DOMAIN into KEEP_PUBLIC_URL)
sed -e "s|__KEEP_DOMAIN__|${KEEP_DOMAIN}|g" \
    "$INFRA_DIR/files/keep/keep.service" > /etc/systemd/system/keep.service
chmod 644 /etc/systemd/system/keep.service
systemctl daemon-reload

# Defer start until the binary exists. Build from sibling repo and scp /usr/local/bin/keep separately.
if [ -x /usr/local/bin/keep ]; then
    systemctl enable --now keep
    echo "OK: keep enabled and running"
else
    echo "OK: keep unit installed (deferred start: /usr/local/bin/keep not present)"
    echo "    Build keep, scp to /usr/local/bin/keep on this box, then: systemctl enable --now keep"
fi

# First-run setup, all in the keep UI:
#   1. Browse to https://${KEEP_DOMAIN}/setup. Set master password, scan TOTP, save the 8 recovery codes.
#   2. Create project lyrics-api / env prod, bulk-import secrets via the .env paste UI.
#   3. Mint a token for lyrics-api/prod with OUTPUT=/etc/lyrics-api.env, RELOAD_CMD="systemctl restart lyrics-api",
#      and REQUIRED_KEYS = every key currently in /etc/lyrics-api.env.
#   4. Paste the bootstrap install command keep generates in a root shell on this box.
#
# Reminder: keep starts sealed on every host boot. Log into the UI once after a reboot to unseal it,
# otherwise the keep-agent gets 503s and /etc/lyrics-api.env stops refreshing.
