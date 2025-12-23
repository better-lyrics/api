#!/bin/bash
#
# migrate-data.sh - Migrate data from Railway preview environment to production using API
#
# This script uses the safe API-based backup/restore approach:
# 1. Download backup files from preview (created via /cache/backup API)
# 2. Upload to production's backup folder
# 3. Restore via /cache/restore API
#
# Usage:
#   ./scripts/migrate-data.sh <preview-env-name> [prod-env-name] [options]
#
# Options:
#   --dry-run              Show what would be done without doing it
#   --local                Use existing files from ./migration folder
#   --cache=<path>         Use specific cache backup file
#   --stats=<path>         Use specific stats backup file
#   --skip-restore         Only upload, don't call restore API
#
# Prerequisites:
#   - Railway CLI installed (npm install -g @railway/cli)
#   - Logged in to Railway (railway login)
#   - Linked to project (railway link)
#   - Backup created on preview via: curl -H "Authorization: <token>" <preview-url>/cache/backup
#
# What this script does:
#   1. Downloads latest backup files from preview environment via SSH
#   2. Uploads backup files to production's backup folder
#   3. Calls /cache/restore API on production to restore
#   4. Redeploys to reload everything
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
DATA_DIR="/data"
BACKUP_PATH="$DATA_DIR/backups"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIGRATION_DIR="$SCRIPT_DIR/../migration"
SERVICE_NAME="${SERVICE_NAME:-}"

# Parse arguments
PREVIEW_ENV=""
PROD_ENV="production"
DRY_RUN=false
LOCAL_CACHE_FILE=""
LOCAL_STATS_FILE=""
SKIP_RESTORE=false
SKIP_CACHE=false
SKIP_STATS=false
PROD_API_URL="${PROD_API_URL:-}"
CACHE_ACCESS_TOKEN="${CACHE_ACCESS_TOKEN:-}"
CACHE_DOWNLOAD_URL=""

for arg in "$@"; do
    case $arg in
        --dry-run)
            DRY_RUN=true
            ;;
        --cache=*)
            LOCAL_CACHE_FILE="${arg#*=}"
            ;;
        --stats=*)
            LOCAL_STATS_FILE="${arg#*=}"
            ;;
        --local)
            LOCAL_CACHE_FILE="auto"
            LOCAL_STATS_FILE="auto"
            ;;
        --skip-restore)
            SKIP_RESTORE=true
            ;;
        --skip-cache)
            SKIP_CACHE=true
            ;;
        --skip-stats)
            SKIP_STATS=true
            ;;
        --url=*)
            PROD_API_URL="${arg#*=}"
            ;;
        --token=*)
            CACHE_ACCESS_TOKEN="${arg#*=}"
            ;;
        --download-url=*)
            CACHE_DOWNLOAD_URL="${arg#*=}"
            ;;
        *)
            if [ -z "$PREVIEW_ENV" ]; then
                PREVIEW_ENV="$arg"
            else
                PROD_ENV="$arg"
            fi
            ;;
    esac
done

# Validate arguments
if [ -z "$PREVIEW_ENV" ] && [ -z "$LOCAL_CACHE_FILE" ]; then
    echo -e "${RED}Error: Preview environment name or local file required${NC}"
    echo ""
    echo "Usage: $0 <preview-env-name> [prod-env-name] [options]"
    echo ""
    echo "Options:"
    echo "  --dry-run              Show what would be done"
    echo "  --local                Use most recent files from ./migration folder"
    echo "  --cache=<path>         Use specific cache backup file"
    echo "  --stats=<path>         Use specific stats backup file"
    echo "  --skip-cache           Skip cache migration (only migrate stats)"
    echo "  --skip-stats           Skip stats migration (only migrate cache)"
    echo "  --skip-restore         Only upload, don't call restore API"
    echo "  --download-url=<url>   Direct download URL for cache file (upload manually first)"
    echo "  --url=<url>            Production API URL for restore calls"
    echo "  --token=<token>        CACHE_ACCESS_TOKEN for API auth"
    echo ""
    echo "Environment variables:"
    echo "  PROD_API_URL           Production API URL (or use --url)"
    echo "  CACHE_ACCESS_TOKEN     API auth token (or use --token)"
    echo "  SERVICE_NAME           Railway service name (if multiple services)"
    echo ""
    echo "Examples:"
    echo "  # First, create backup on preview:"
    echo "  curl -H 'Authorization: <token>' https://preview.example.com/cache/backup"
    echo ""
    echo "  # Then run migration:"
    echo "  $0 pr-123"
    echo "  $0 pr-123 --url=https://api.example.com --token=secret"
    echo "  $0 --local --url=https://api.example.com --token=secret"
    exit 1
fi

# Check if railway CLI is installed
if ! command -v railway &> /dev/null; then
    echo -e "${RED}Error: Railway CLI is not installed${NC}"
    echo "Install with: npm install -g @railway/cli"
    exit 1
fi

# Check if logged in
if ! railway whoami &> /dev/null; then
    echo -e "${RED}Error: Not logged in to Railway${NC}"
    echo "Run: railway login"
    exit 1
fi

# Helper function for railway ssh with optional service name
railway_ssh() {
    local env="$1"
    shift
    if [ -n "$SERVICE_NAME" ]; then
        railway ssh -e "$env" -s "$SERVICE_NAME" "$@"
    else
        railway ssh -e "$env" "$@"
    fi
}

format_size() {
    local bytes=$1
    bytes=${bytes:-0}
    if [ "$bytes" -ge 1073741824 ] 2>/dev/null; then
        echo "$((bytes / 1073741824)) GB"
    elif [ "$bytes" -ge 1048576 ] 2>/dev/null; then
        echo "$((bytes / 1048576)) MB"
    elif [ "$bytes" -ge 1024 ] 2>/dev/null; then
        echo "$((bytes / 1024)) KB"
    else
        echo "$bytes bytes"
    fi
}

# Get MD5 checksum (works on both macOS and Linux)
get_local_md5() {
    local file=$1
    if command -v md5sum &> /dev/null; then
        md5sum "$file" 2>/dev/null | awk '{print $1}'
    else
        md5 -q "$file" 2>/dev/null
    fi
}

get_remote_md5() {
    local env=$1
    local file=$2
    railway_ssh "$env" "md5sum $file 2>/dev/null | awk '{print \$1}'" 2>/dev/null | tr -d '[:space:]'
}

# Handle --local mode: find most recent files
if [ "$LOCAL_CACHE_FILE" = "auto" ]; then
    LOCAL_CACHE_FILE=$(ls -t "$MIGRATION_DIR"/cache_backup_*.db 2>/dev/null | head -1)
    if [ -z "$LOCAL_CACHE_FILE" ]; then
        echo -e "${YELLOW}Warning: No cache backup files found in $MIGRATION_DIR${NC}"
    fi
fi
if [ "$LOCAL_STATS_FILE" = "auto" ]; then
    LOCAL_STATS_FILE=$(ls -t "$MIGRATION_DIR"/stats_backup_*.db 2>/dev/null | head -1)
fi

# Determine mode
USE_LOCAL_FILES=false
if [ -n "$LOCAL_CACHE_FILE" ] || [ -n "$LOCAL_STATS_FILE" ]; then
    USE_LOCAL_FILES=true
fi

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║      Railway Data Migration: Preview → Production          ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

if [ "$USE_LOCAL_FILES" = true ]; then
    echo -e "${YELLOW}Mode:${NC}        Using local files"
    [ -n "$LOCAL_CACHE_FILE" ] && echo -e "${YELLOW}Cache:${NC}       $LOCAL_CACHE_FILE"
    [ -n "$LOCAL_STATS_FILE" ] && echo -e "${YELLOW}Stats:${NC}       $LOCAL_STATS_FILE"
else
    echo -e "${YELLOW}Source:${NC}      $PREVIEW_ENV"
fi
echo -e "${YELLOW}Destination:${NC} $PROD_ENV"
[ -n "$SERVICE_NAME" ] && echo -e "${YELLOW}Service:${NC}     $SERVICE_NAME"
echo -e "${YELLOW}Dry run:${NC}     $DRY_RUN"
echo ""

if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}[DRY RUN] Would perform the following steps:${NC}"
    echo "  1. Create migration directory: $MIGRATION_DIR"
    if [ "$USE_LOCAL_FILES" = false ]; then
        echo "  2. Download latest backup files from $PREVIEW_ENV:$BACKUP_PATH/"
    else
        echo "  2. Use local files (skip download)"
    fi
    echo "  3. Upload backup files to $PROD_ENV:$BACKUP_PATH/"
    if [ "$SKIP_RESTORE" = false ]; then
        echo "  4. Call /cache/restore API on production"
        echo "  5. Redeploy $PROD_ENV to reload"
    fi
    exit 0
fi

# Create migration directory
echo -e "${BLUE}[1/5]${NC} Creating migration directory..."
mkdir -p "$MIGRATION_DIR"
TIMESTAMP=$(date +%Y-%m-%d_%H-%M-%S)

# Check if pv is available for progress display
HAS_PV=$(command -v pv &> /dev/null && echo "yes" || echo "no")

if [ "$USE_LOCAL_FILES" = true ]; then
    echo -e "${BLUE}[2/5]${NC} Using local files (skipping download)..."

    if [ -n "$LOCAL_CACHE_FILE" ] && [ -f "$LOCAL_CACHE_FILE" ]; then
        LOCAL_CACHE="$LOCAL_CACHE_FILE"
        CACHE_SIZE=$(stat -f%z "$LOCAL_CACHE" 2>/dev/null || stat -c%s "$LOCAL_CACHE" 2>/dev/null)
        CACHE_BACKUP_NAME=$(basename "$LOCAL_CACHE")
        echo -e "      ${GREEN}✓${NC} cache: $CACHE_BACKUP_NAME ($(format_size $CACHE_SIZE))"
    else
        echo -e "      ${YELLOW}⚠${NC} cache: not found"
        LOCAL_CACHE=""
    fi

    if [ -n "$LOCAL_STATS_FILE" ] && [ -f "$LOCAL_STATS_FILE" ]; then
        LOCAL_STATS="$LOCAL_STATS_FILE"
        STATS_SIZE=$(stat -f%z "$LOCAL_STATS" 2>/dev/null || stat -c%s "$LOCAL_STATS" 2>/dev/null)
        STATS_BACKUP_NAME=$(basename "$LOCAL_STATS")
        echo -e "      ${GREEN}✓${NC} stats: $STATS_BACKUP_NAME ($(format_size $STATS_SIZE))"
    else
        echo -e "      ${YELLOW}⚠${NC} stats: not found"
        LOCAL_STATS=""
    fi
else
    # Test SSH connection
    echo -e "${BLUE}[1.5/5]${NC} Testing SSH connection to ${YELLOW}$PREVIEW_ENV${NC}..."
    if ! railway_ssh "$PREVIEW_ENV" "echo 'SSH OK'" 2>/dev/null | grep -q "OK"; then
        echo -e "${RED}Error: Cannot connect to preview environment '$PREVIEW_ENV'${NC}"
        exit 1
    fi
    echo -e "      ${GREEN}✓${NC} Connected"

    # Download backup files from preview
    echo -e "${BLUE}[2/5]${NC} Downloading backup files from ${YELLOW}$PREVIEW_ENV${NC}..."

    # Find latest cache backup
    if [ "$SKIP_CACHE" = true ]; then
        echo "      Skipping cache (--skip-cache)"
        LOCAL_CACHE=""
        CACHE_BACKUP_NAME=""
    else
        echo "      Looking for latest cache backup..."
        CACHE_BACKUP_NAME=$(railway_ssh "$PREVIEW_ENV" "ls -t $BACKUP_PATH/cache_backup_*.db 2>/dev/null | head -1 | xargs basename 2>/dev/null" 2>/dev/null | tr -d '[:space:]')

    CHUNK_SIZE=104857600  # 100MB chunks (100 * 1024 * 1024, aligned to 1MB blocks)

    if [ -n "$CACHE_BACKUP_NAME" ] && [ "$CACHE_BACKUP_NAME" != "" ]; then
        REMOTE_CACHE="$BACKUP_PATH/$CACHE_BACKUP_NAME"
        REMOTE_CACHE_SIZE=$(railway_ssh "$PREVIEW_ENV" "stat -c%s $REMOTE_CACHE 2>/dev/null || echo 0" 2>/dev/null | tr -d '[:space:]' | grep -oE '[0-9]+' | head -1)
        REMOTE_CACHE_SIZE=${REMOTE_CACHE_SIZE:-0}

        if [ "$REMOTE_CACHE_SIZE" -gt 0 ] 2>/dev/null; then
            LOCAL_CACHE="$MIGRATION_DIR/$CACHE_BACKUP_NAME"

            # Use chunked download for large files
            if [ "$REMOTE_CACHE_SIZE" -gt "$CHUNK_SIZE" ]; then
                echo -e "      ${BLUE}↓${NC} $CACHE_BACKUP_NAME ($(format_size $REMOTE_CACHE_SIZE)) - chunked download..."

                NUM_CHUNKS=$(( (REMOTE_CACHE_SIZE + CHUNK_SIZE - 1) / CHUNK_SIZE ))
                echo -e "      ${YELLOW}→${NC} $NUM_CHUNKS chunks of ~$(format_size $CHUNK_SIZE) each"

                # Clear/create the output file
                rm -f "$LOCAL_CACHE"
                touch "$LOCAL_CACHE"

                START_TIME=$(date +%s)
                DOWNLOADED=0
                DOWNLOAD_FAILED=false

                for i in $(seq 0 $((NUM_CHUNKS - 1))); do
                    SKIP=$((i * CHUNK_SIZE))
                    CHUNK_NUM=$((i + 1))

                    REMAINING=$((REMOTE_CACHE_SIZE - SKIP))
                    if [ "$REMAINING" -lt "$CHUNK_SIZE" ]; then
                        THIS_CHUNK_SIZE=$REMAINING
                    else
                        THIS_CHUNK_SIZE=$CHUNK_SIZE
                    fi

                    BEFORE_SIZE=$(stat -f%z "$LOCAL_CACHE" 2>/dev/null || stat -c%s "$LOCAL_CACHE" 2>/dev/null)

                    # Use dd with base64 encoding for safe binary transfer through SSH
                    SKIP_BLOCKS=$((SKIP / 1048576))
                    COUNT_BLOCKS=$(( (THIS_CHUNK_SIZE + 1048575) / 1048576 ))

                    # Base64 encode on remote, transfer as ASCII, decode locally
                    # Base64 adds ~33% overhead, so adjust pv size estimate
                    BASE64_SIZE=$(( THIS_CHUNK_SIZE * 4 / 3 ))
                    if [ "$HAS_PV" = "yes" ]; then
                        railway_ssh "$PREVIEW_ENV" "dd if=$REMOTE_CACHE bs=1M skip=$SKIP_BLOCKS count=$COUNT_BLOCKS 2>/dev/null | base64" 2>/dev/null | pv -s "$BASE64_SIZE" -N "      [$CHUNK_NUM/$NUM_CHUNKS]" | base64 -d >> "$LOCAL_CACHE"
                    else
                        echo -ne "      [$CHUNK_NUM/$NUM_CHUNKS] Downloading $(format_size $THIS_CHUNK_SIZE)..."
                        railway_ssh "$PREVIEW_ENV" "dd if=$REMOTE_CACHE bs=1M skip=$SKIP_BLOCKS count=$COUNT_BLOCKS 2>/dev/null | base64" 2>/dev/null | base64 -d >> "$LOCAL_CACHE"
                    fi

                    AFTER_SIZE=$(stat -f%z "$LOCAL_CACHE" 2>/dev/null || stat -c%s "$LOCAL_CACHE" 2>/dev/null)
                    CHUNK_ACTUAL=$((AFTER_SIZE - BEFORE_SIZE))

                    if [ "$CHUNK_ACTUAL" -ge "$((THIS_CHUNK_SIZE * 95 / 100))" ] 2>/dev/null; then
                        DOWNLOADED=$((DOWNLOADED + CHUNK_ACTUAL))
                        PERCENT=$((DOWNLOADED * 100 / REMOTE_CACHE_SIZE))
                        echo -e "      [$CHUNK_NUM/$NUM_CHUNKS] ${GREEN}✓${NC} $(format_size $CHUNK_ACTUAL) - Total: ${PERCENT}%"
                    else
                        echo -e "      [$CHUNK_NUM/$NUM_CHUNKS] ${RED}✗${NC} Incomplete (got $(format_size $CHUNK_ACTUAL) of $(format_size $THIS_CHUNK_SIZE))"
                        DOWNLOAD_FAILED=true
                        break
                    fi
                done

                if [ "$DOWNLOAD_FAILED" = false ]; then
                    END_TIME=$(date +%s)
                    DURATION=$((END_TIME - START_TIME))
                    CACHE_SIZE=$(stat -f%z "$LOCAL_CACHE" 2>/dev/null || stat -c%s "$LOCAL_CACHE" 2>/dev/null)
                    SPEED=$((CACHE_SIZE / (DURATION + 1) / 1024))

                    # Verify checksum
                    echo -ne "      Verifying checksum..."
                    REMOTE_MD5=$(get_remote_md5 "$PREVIEW_ENV" "$REMOTE_CACHE")
                    LOCAL_MD5=$(get_local_md5 "$LOCAL_CACHE")

                    if [ -n "$REMOTE_MD5" ] && [ "$REMOTE_MD5" = "$LOCAL_MD5" ]; then
                        echo -e "\r      ${GREEN}✓${NC} cache: $(format_size $CACHE_SIZE) in ${DURATION}s (~${SPEED} KB/s) - checksum verified"
                    elif [ -n "$REMOTE_MD5" ]; then
                        echo -e "\r      ${RED}✗${NC} CHECKSUM MISMATCH!"
                        echo -e "         Local:  $LOCAL_MD5"
                        echo -e "         Remote: $REMOTE_MD5"
                        LOCAL_CACHE=""
                    else
                        echo -e "\r      ${GREEN}✓${NC} cache: $(format_size $CACHE_SIZE) in ${DURATION}s (~${SPEED} KB/s)"
                    fi
                else
                    echo -e "      ${RED}✗${NC} Download failed"
                    LOCAL_CACHE=""
                fi
            else
                # Small file - direct download with base64
                echo -e "      ${BLUE}↓${NC} $CACHE_BACKUP_NAME ($(format_size $REMOTE_CACHE_SIZE))..."

                START_TIME=$(date +%s)
                BASE64_SIZE=$(( REMOTE_CACHE_SIZE * 4 / 3 ))
                if [ "$HAS_PV" = "yes" ]; then
                    railway_ssh "$PREVIEW_ENV" "cat $REMOTE_CACHE | base64" 2>/dev/null | pv -s "$BASE64_SIZE" -N "      cache" | base64 -d > "$LOCAL_CACHE"
                else
                    railway_ssh "$PREVIEW_ENV" "cat $REMOTE_CACHE | base64" 2>/dev/null | base64 -d > "$LOCAL_CACHE"
                fi
                END_TIME=$(date +%s)

                CACHE_SIZE=$(stat -f%z "$LOCAL_CACHE" 2>/dev/null || stat -c%s "$LOCAL_CACHE" 2>/dev/null)
                DURATION=$((END_TIME - START_TIME))

                REMOTE_MD5=$(get_remote_md5 "$PREVIEW_ENV" "$REMOTE_CACHE")
                LOCAL_MD5=$(get_local_md5 "$LOCAL_CACHE")

                if [ -n "$REMOTE_MD5" ] && [ "$REMOTE_MD5" = "$LOCAL_MD5" ]; then
                    echo -e "      ${GREEN}✓${NC} cache: $(format_size $CACHE_SIZE) in ${DURATION}s (checksum verified)"
                elif [ -n "$REMOTE_MD5" ]; then
                    echo -e "      ${RED}✗${NC} Checksum mismatch!"
                    LOCAL_CACHE=""
                else
                    echo -e "      ${GREEN}✓${NC} cache: $(format_size $CACHE_SIZE) in ${DURATION}s"
                fi
            fi
        else
            echo -e "      ${YELLOW}⚠${NC} cache backup: empty or not found"
            LOCAL_CACHE=""
        fi
    else
        echo -e "      ${YELLOW}⚠${NC} No cache backup found in $BACKUP_PATH/"
        echo -e "      ${YELLOW}→${NC} Create one first: curl -H 'Authorization: <token>' <preview-url>/cache/backup"
        LOCAL_CACHE=""
    fi
    fi  # End SKIP_CACHE else

    # Find latest stats backup
    if [ "$SKIP_STATS" = true ]; then
        echo "      Skipping stats (--skip-stats)"
        LOCAL_STATS=""
        STATS_BACKUP_NAME=""
    else
        echo "      Looking for latest stats backup..."
        STATS_BACKUP_NAME=$(railway_ssh "$PREVIEW_ENV" "ls -t $BACKUP_PATH/stats_backup_*.db 2>/dev/null | head -1 | xargs basename 2>/dev/null" 2>/dev/null | tr -d '[:space:]')

    if [ -n "$STATS_BACKUP_NAME" ] && [ "$STATS_BACKUP_NAME" != "" ]; then
        REMOTE_STATS="$BACKUP_PATH/$STATS_BACKUP_NAME"
        REMOTE_STATS_SIZE=$(railway_ssh "$PREVIEW_ENV" "stat -c%s $REMOTE_STATS 2>/dev/null || echo 0" 2>/dev/null | tr -d '[:space:]' | grep -oE '[0-9]+' | head -1)
        REMOTE_STATS_SIZE=${REMOTE_STATS_SIZE:-0}

        if [ "$REMOTE_STATS_SIZE" -gt 0 ] 2>/dev/null; then
            LOCAL_STATS="$MIGRATION_DIR/$STATS_BACKUP_NAME"
            echo -e "      ${BLUE}↓${NC} $STATS_BACKUP_NAME ($(format_size $REMOTE_STATS_SIZE))..."

            START_TIME=$(date +%s)
            BASE64_SIZE=$(( REMOTE_STATS_SIZE * 4 / 3 ))
            if [ "$HAS_PV" = "yes" ]; then
                railway_ssh "$PREVIEW_ENV" "cat $REMOTE_STATS | base64" 2>/dev/null | pv -s "$BASE64_SIZE" -N "      stats" | base64 -d > "$LOCAL_STATS"
            else
                railway_ssh "$PREVIEW_ENV" "cat $REMOTE_STATS | base64" 2>/dev/null | base64 -d > "$LOCAL_STATS"
            fi
            END_TIME=$(date +%s)

            STATS_SIZE=$(stat -f%z "$LOCAL_STATS" 2>/dev/null || stat -c%s "$LOCAL_STATS" 2>/dev/null)
            DURATION=$((END_TIME - START_TIME))
            echo -e "      ${GREEN}✓${NC} stats: $(format_size $STATS_SIZE) in ${DURATION}s"
        else
            echo -e "      ${YELLOW}⚠${NC} stats backup: empty or not found"
            LOCAL_STATS=""
        fi
    else
        echo -e "      ${YELLOW}⚠${NC} No stats backup found"
        LOCAL_STATS=""
    fi
    fi  # End SKIP_STATS else
fi  # End USE_LOCAL_FILES else

# Check if we have anything to migrate
if [ -z "$LOCAL_CACHE" ] && [ -z "$LOCAL_STATS" ]; then
    echo -e "${RED}Error: No backup files found to migrate${NC}"
    echo ""
    echo "Make sure you've created backups first:"
    echo "  curl -H 'Authorization: <token>' <preview-url>/cache/backup"
    exit 1
fi

# Upload to production's backup folder
echo -e "${BLUE}[3/5]${NC} Uploading to ${YELLOW}$PROD_ENV${NC}..."

# Ensure backup directory exists on production
railway_ssh "$PROD_ENV" "mkdir -p $BACKUP_PATH" 2>/dev/null || true

UPLOAD_FAILED=false

# Function to download from URL to production
# Usage: download_to_prod <url> <remote_path> <local_file_for_checksum> <description>
download_to_prod() {
    local URL="$1"
    local REMOTE_PATH="$2"
    local LOCAL_FILE="$3"
    local FILE_DESC="$4"

    local LOCAL_MD5=$(get_local_md5 "$LOCAL_FILE")

    echo -e "      ${BLUE}↓${NC} Downloading $FILE_DESC to production..."
    echo -e "         URL: $URL"

    # Download on production - install curl if needed, or use wget
    echo -ne "         [1/2] Downloading..."
    DOWNLOAD_START=$(date +%s)

    # Try to download, installing curl if needed
    railway_ssh "$PROD_ENV" "command -v curl >/dev/null || command -v wget >/dev/null || (apt-get update -qq && apt-get install -qq -y curl >/dev/null 2>&1); if command -v curl >/dev/null; then curl -sL '$URL' -o '$REMOTE_PATH'; elif command -v wget >/dev/null; then wget -q '$URL' -O '$REMOTE_PATH'; else echo 'No download tool available' && exit 1; fi" 2>/dev/null
    DOWNLOAD_STATUS=$?
    DOWNLOAD_END=$(date +%s)
    DOWNLOAD_DURATION=$((DOWNLOAD_END - DOWNLOAD_START))

    if [ $DOWNLOAD_STATUS -ne 0 ]; then
        echo -e "\r         [1/2] ${RED}✗${NC} Download failed (exit: $DOWNLOAD_STATUS)"
        return 1
    fi
    echo -e "\r         [1/2] ${GREEN}✓${NC} Downloaded in ${DOWNLOAD_DURATION}s"

    # Verify checksum
    echo -ne "         [2/2] Verifying checksum..."
    REMOTE_MD5=$(get_remote_md5 "$PROD_ENV" "$REMOTE_PATH")

    if [ -z "$REMOTE_MD5" ]; then
        echo -e "\r         [2/2] ${YELLOW}⚠${NC} Checksum unavailable"
    elif [ "$LOCAL_MD5" = "$REMOTE_MD5" ]; then
        echo -e "\r         [2/2] ${GREEN}✓${NC} Checksum verified ($LOCAL_MD5)"
    else
        echo -e "\r         [2/2] ${RED}✗${NC} CHECKSUM MISMATCH!"
        echo -e "            Local:  $LOCAL_MD5"
        echo -e "            Remote: $REMOTE_MD5"
        return 1
    fi

    return 0
}

# Check if user provided a download URL
if [ -n "$CACHE_DOWNLOAD_URL" ]; then
    echo -e "      ${YELLOW}→${NC} Using provided download URL"
    if ! download_to_prod "$CACHE_DOWNLOAD_URL" "$BACKUP_PATH/$CACHE_BACKUP_NAME" "$LOCAL_CACHE" "$CACHE_BACKUP_NAME"; then
        echo -e "      ${RED}✗${NC} Cache download failed"
        UPLOAD_FAILED=true
        LOCAL_CACHE=""
    fi
elif [ -n "$LOCAL_CACHE" ]; then
    echo -e "      ${YELLOW}⚠${NC} No --download-url provided"
    echo ""
    echo -e "      ${BLUE}Please upload this file manually:${NC}"
    echo -e "         $LOCAL_CACHE"
    echo ""
    echo -e "      ${BLUE}Upload to any of these services:${NC}"
    echo "         • https://www.dropbox.com (get direct link)"
    echo "         • https://drive.google.com (share → anyone with link)"
    echo "         • https://wetransfer.com"
    echo "         • Any file hosting that gives a direct download URL"
    echo ""
    echo -e "      ${BLUE}Then run again with:${NC}"
    echo "         --download-url=<paste-url-here>"
    echo ""
    UPLOAD_FAILED=true
    LOCAL_CACHE=""
fi

if [ -n "$LOCAL_STATS" ] && [ -z "$UPLOAD_FAILED" ]; then
    echo -e "      ${YELLOW}⚠${NC} Stats upload not implemented yet"
fi

if [ "$UPLOAD_FAILED" = true ]; then
    echo ""
    echo -e "${RED}ERROR: Upload/download failed. See error above for details.${NC}"
    echo -e "${RED}Restore will be skipped to prevent data corruption.${NC}"
    echo ""
fi

# Restore via API
if [ "$SKIP_RESTORE" = true ]; then
    echo -e "${BLUE}[4/5]${NC} Skipping restore (--skip-restore)"
    echo -e "${BLUE}[5/5]${NC} Skipping redeploy"
elif [ "$UPLOAD_FAILED" = true ]; then
    echo -e "${BLUE}[4/5]${NC} Skipping restore (upload failed)"
    echo -e "${BLUE}[5/5]${NC} Skipping redeploy"
    echo ""
    echo -e "${RED}Migration aborted due to upload errors.${NC}"
    exit 1
else
    echo -e "${BLUE}[4/5]${NC} Restoring via API..."

    if [ -z "$PROD_API_URL" ] || [ -z "$CACHE_ACCESS_TOKEN" ]; then
        echo -e "      ${YELLOW}⚠${NC} Missing PROD_API_URL or CACHE_ACCESS_TOKEN"
        echo -e "      ${YELLOW}→${NC} Manual restore required:"
        [ -n "$LOCAL_CACHE" ] && echo "         curl -H 'Authorization: <token>' '<prod-url>/cache/restore?backup=$CACHE_BACKUP_NAME'"
        echo ""
    else
        RESTORE_FAILED=false

        if [ -n "$LOCAL_CACHE" ]; then
            echo -ne "      Restoring cache..."
            RESTORE_RESULT=$(curl -s -H "Authorization: $CACHE_ACCESS_TOKEN" "$PROD_API_URL/cache/restore?backup=$CACHE_BACKUP_NAME" 2>/dev/null)
            if echo "$RESTORE_RESULT" | grep -q "restored successfully"; then
                KEYS_RESTORED=$(echo "$RESTORE_RESULT" | grep -oE '"keys_restored":[0-9]+' | grep -oE '[0-9]+')
                echo -e "\r      ${GREEN}✓${NC} Cache restored ($KEYS_RESTORED keys)"
            else
                echo -e "\r      ${RED}✗${NC} Cache restore failed!"
                echo -e "         Response: $RESTORE_RESULT"
                RESTORE_FAILED=true
            fi
        fi

        # Stats restore would go here once endpoint is added
        if [ -n "$LOCAL_STATS" ]; then
            echo -e "      ${YELLOW}⚠${NC} Stats restore: manual restore required (endpoint not yet implemented)"
        fi

        if [ "$RESTORE_FAILED" = true ]; then
            echo ""
            echo -e "${RED}ERROR: Restore failed. Check the error above.${NC}"
            echo -e "${YELLOW}The backup file is on production at: $BACKUP_PATH/$CACHE_BACKUP_NAME${NC}"
            echo -e "${YELLOW}You can retry manually: curl -H 'Authorization: <token>' '$PROD_API_URL/cache/restore?backup=$CACHE_BACKUP_NAME'${NC}"
            exit 1
        fi
    fi

    # Redeploy
    echo -e "${BLUE}[5/5]${NC} Redeploying ${YELLOW}$PROD_ENV${NC}..."

    REDEPLOY_CMD="railway redeploy -e $PROD_ENV"
    [ -n "$SERVICE_NAME" ] && REDEPLOY_CMD="$REDEPLOY_CMD -s $SERVICE_NAME"

    if eval "$REDEPLOY_CMD" 2>/dev/null; then
        echo -e "      ${GREEN}✓${NC} Service redeployed"
    else
        echo -e "${YELLOW}Warning: Could not redeploy automatically${NC}"
        echo "      Please manually redeploy in Railway dashboard"
    fi
fi

echo ""
echo -e "${BLUE}Migration files saved in:${NC} $MIGRATION_DIR"

echo ""
echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║                    Migration Complete!                      ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Summary:"
[ "$USE_LOCAL_FILES" = false ] && echo "  - Source: $PREVIEW_ENV"
echo "  - Destination: $PROD_ENV"
echo "  - Uploaded to: $BACKUP_PATH/"
[ -n "$LOCAL_CACHE" ] && echo "    • $CACHE_BACKUP_NAME ($(format_size $CACHE_SIZE))"
[ -n "$LOCAL_STATS" ] && echo "    • $STATS_BACKUP_NAME ($(format_size $STATS_SIZE))"
echo ""
