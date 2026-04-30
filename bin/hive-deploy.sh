#!/bin/bash
# hive-deploy.sh — pull latest hive repo and sync scripts to /usr/local/bin.
# Called by systemd timer every 60 seconds. Ensures the installed scripts
# always match the repo without manual SCP or copy steps.

set -euo pipefail

HIVE_REPO="${HIVE_REPO_DIR:-/tmp/hive}"
INSTALL_DIR="/usr/local/bin"
LOG="/var/log/hive-deploy.log"
TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

log() { echo "[$TIMESTAMP] $*" >> "$LOG" 2>/dev/null || true; }

if [ ! -d "$HIVE_REPO/.git" ]; then
  log "ERROR: $HIVE_REPO is not a git repo"
  exit 1
fi

cd "$HIVE_REPO"

BEFORE=$(git rev-parse HEAD)
git pull --rebase origin main --quiet 2>/dev/null || {
  log "WARN: git pull failed, skipping deploy"
  exit 0
}
AFTER=$(git rev-parse HEAD)

SYNCED=""

if [ "$BEFORE" != "$AFTER" ]; then
  CHANGED_FILES=$(git diff --name-only "$BEFORE" "$AFTER")
  SCRIPTS_CHANGED=$(echo "$CHANGED_FILES" | grep '^bin/' || true)
  for script in $SCRIPTS_CHANGED; do
    filename=$(basename "$script")
    src="$HIVE_REPO/$script"
    dst="$INSTALL_DIR/$filename"
    if [ -f "$src" ] && [ -f "$dst" ]; then
      cp "$src" "$dst"
      chmod +x "$dst"
      SYNCED="$SYNCED $filename"
    fi
  done
fi

# Drift check: even if HEAD unchanged, installed files may be stale
for src in "$HIVE_REPO"/bin/*.sh; do
  filename=$(basename "$src")
  dst="$INSTALL_DIR/$filename"
  [ -f "$dst" ] || continue
  if ! cmp -s "$src" "$dst"; then
    cp "$src" "$dst"
    chmod +x "$dst"
    SYNCED="$SYNCED $filename(drift)"
  fi
done

if [ -n "$SYNCED" ]; then
  log "DEPLOY ${BEFORE:0:7}→${AFTER:0:7} — synced:$SYNCED"
fi
