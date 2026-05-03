#!/bin/bash
set -euo pipefail

LATEST=$(ls -1t /var/lib/lyrics-api/backups/cache_backup_*.db 2>/dev/null | head -1)
[ -z "$LATEST" ] && { echo "No backup file found"; exit 1; }

# Always upload to daily/
/usr/bin/rclone copy "$LATEST" b2:lyrics-api-backups/daily/ --transfers 4

# On Sundays only, also upload to weekly/
if [ "$(date -u +%u)" = "7" ]; then
    /usr/bin/rclone copy "$LATEST" b2:lyrics-api-backups/weekly/ --transfers 4
fi
