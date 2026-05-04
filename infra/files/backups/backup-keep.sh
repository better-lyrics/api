#!/bin/bash
# Online backup of keep.db: SQLite .backup as the keep user (the only one with read on /var/lib/keep/keep.db),
# then rclone push as the deploy user (the only one with the B2 credentials).
# The DB is age-encrypted at rest, so mode 644 on the backup file is fine.

set -euo pipefail

DEST=/var/backups/keep
install -d -o keep -g keep -m 0755 "$DEST"

TS=$(date -u +%Y-%m-%d_%H-%M-%S)
FILE="$DEST/keep_backup_$TS.db"

runuser -u keep -- sqlite3 /var/lib/keep/keep.db ".backup '$FILE'"
chmod 644 "$FILE"

runuser -u deploy -- /usr/bin/rclone copy "$FILE" b2:lyrics-api-backups/keep/daily/ --transfers 4
if [ "$(date -u +%u)" = "7" ]; then
    runuser -u deploy -- /usr/bin/rclone copy "$FILE" b2:lyrics-api-backups/keep/weekly/ --transfers 4
fi

# Local retention: 7 days
find "$DEST" -name 'keep_backup_*.db' -mtime +7 -delete
