#!/bin/bash
set -euo pipefail
source /etc/lyrics-api.env
curl -s -H "Authorization: $CACHE_ACCESS_TOKEN" http://localhost:8080/cache/backup > /dev/null
