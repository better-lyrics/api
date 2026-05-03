#!/bin/bash
set -euo pipefail

# UFW: allow ssh + http + https only
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

# fail2ban: defaults are fine (sshd jail enabled out of the box)
systemctl enable --now fail2ban

echo "OK: ufw active (22/80/443), fail2ban active"
