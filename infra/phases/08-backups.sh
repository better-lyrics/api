#!/bin/bash
set -euo pipefail

# Backup scripts
install -m 755 -o root -g root \
    "$INFRA_DIR/files/backups/backup-cache.sh" \
    /usr/local/bin/backup-cache.sh
install -m 755 -o root -g root \
    "$INFRA_DIR/files/backups/upload-backup.sh" \
    /usr/local/bin/upload-backup.sh
install -m 755 -o root -g root \
    "$INFRA_DIR/files/backups/backup-keep.sh" \
    /usr/local/bin/backup-keep.sh

# System cron for keep.db backup (runs as root, drops to keep + deploy via runuser)
install -m 644 -o root -g root \
    "$INFRA_DIR/files/backups/keep-backup.cron" \
    /etc/cron.d/keep-backup

# rclone config for deploy user (mode 600)
install -d -o deploy -g deploy -m 700 /home/deploy/.config
install -d -o deploy -g deploy -m 700 /home/deploy/.config/rclone
sed -e "s|__B2_KEY_ID__|${B2_KEY_ID}|g" \
    -e "s|__B2_APP_KEY__|${B2_APP_KEY}|g" \
    "$INFRA_DIR/files/rclone/rclone.conf.example" > /home/deploy/.config/rclone/rclone.conf
chmod 600 /home/deploy/.config/rclone/rclone.conf
chown deploy:deploy /home/deploy/.config/rclone/rclone.conf

# Crontab: install fragment for deploy user (idempotent: replaces wholesale)
crontab -u deploy "$INFRA_DIR/files/backups/crontab.fragment"

echo "OK: backup scripts + rclone B2 config + deploy crontab"
