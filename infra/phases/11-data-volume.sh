#!/bin/bash
set -euo pipefail

# Optional: attach a Hetzner Cloud Volume as the data dir for lyrics-api.
# Set LYRICS_API_VOLUME_ID in secrets.env (just the numeric ID from the Hetzner
# console). The volume must already be created, attached to this server, and
# auto-mounted by Hetzner at /mnt/HC_Volume_<id>. This phase bind-mounts a
# subdir of the volume over /var/lib/lyrics-api so all DB + backup writes land
# on the volume instead of the root disk. Safe to omit entirely if you don't
# need extra storage.

if [ -z "${LYRICS_API_VOLUME_ID:-}" ]; then
    echo "OK: LYRICS_API_VOLUME_ID unset, skipping data-volume phase"
    exit 0
fi

VOL_ID="$LYRICS_API_VOLUME_ID"
VOL_MOUNT="/mnt/HC_Volume_${VOL_ID}"
VOL_SUBDIR="${VOL_MOUNT}/lyrics-api"
APP_DIR="/var/lib/lyrics-api"
FSTAB_LINE="${VOL_SUBDIR} ${APP_DIR} none bind,nofail,x-systemd.requires-mounts-for=${VOL_MOUNT} 0 0"
DROPIN=/etc/systemd/system/lyrics-api.service.d/data-volume.conf

# Volume must be attached and mounted before we can bind onto it
if ! mountpoint -q "$VOL_MOUNT"; then
    echo "ERROR: Hetzner volume $VOL_ID is not mounted at $VOL_MOUNT" >&2
    echo "       Attach it to this server in the Hetzner console with auto-mount enabled," >&2
    echo "       confirm it appears in 'df -h', then re-run this phase." >&2
    exit 1
fi

install -d -o deploy -g deploy -m 755 "$VOL_SUBDIR"

# If already bind-mounted to our source, nothing to migrate
if mountpoint -q "$APP_DIR" && findmnt -no SOURCE "$APP_DIR" | grep -qE "(^|\W)${VOL_SUBDIR}(\W|$)"; then
    echo "OK: $APP_DIR already bind-mounted from $VOL_SUBDIR"
else
    # Migrate existing data if APP_DIR is a regular dir with content
    if [ -d "$APP_DIR" ] && ! mountpoint -q "$APP_DIR" && [ -n "$(ls -A "$APP_DIR" 2>/dev/null)" ]; then
        echo "Migrating existing data from $APP_DIR to $VOL_SUBDIR"
        WAS_RUNNING=0
        if systemctl is-active --quiet lyrics-api; then
            WAS_RUNNING=1
            systemctl stop lyrics-api
            # Arm a restart trap before any operation that could fail mid-migration
            # (rsync, mv, mount, etc.). The trap fires on every script exit path,
            # so the service always comes back up, even if a later step blows up.
            # Idempotent: systemctl start is a no-op if the service is already
            # running by the time the trap fires on a successful run.
            trap 'systemctl start lyrics-api' EXIT
        fi
        rsync -aHAX --info=progress2 "${APP_DIR}/" "${VOL_SUBDIR}/"
        BACKUP_NAME="${APP_DIR}.pre-volume.$(date -u +%Y%m%d-%H%M%S)"
        mv "$APP_DIR" "$BACKUP_NAME"
        echo "Original preserved at $BACKUP_NAME (delete once you've verified the migration)"
    fi

    install -d -o deploy -g deploy -m 755 "$APP_DIR"

    if ! grep -qE "^\S+\s+${APP_DIR}\s+none\s+bind" /etc/fstab; then
        echo "$FSTAB_LINE" >> /etc/fstab
    fi

    mount "$APP_DIR"
fi

# Refuse to start the service if the bind mount is missing, so a failed mount
# can never silently let lyrics-api write a fresh empty cache.db on root.
install -d -m 755 /etc/systemd/system/lyrics-api.service.d
cat > "$DROPIN" <<CONF
[Unit]
RequiresMountsFor=${APP_DIR}
CONF
systemctl daemon-reload

# Bring the service back if we stopped it for migration
if [ "${WAS_RUNNING:-0}" = "1" ]; then
    systemctl start lyrics-api
fi

findmnt "$APP_DIR"
echo "OK: data volume $VOL_ID bind-mounted at $APP_DIR"
