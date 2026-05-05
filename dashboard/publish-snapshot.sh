#!/usr/bin/env bash
# Build a dashboard snapshot and push it to the docs repo.
# Designed to run as a cron job on the hive server.
#
# Pushes directly to main (no PR) to avoid branch protection check delays.
#
# Usage: ./publish-snapshot.sh
# Env vars:
#   HIVE_DASHBOARD_URL  — dashboard URL (default: http://localhost:3001)
#   DOCS_REPO_DIR       — local clone of kubestellar/docs (default: /tmp/kubestellar-docs-snapshot)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DASHBOARD_URL="${HIVE_DASHBOARD_URL:-http://localhost:3001}"
DOCS_REPO="${DOCS_REPO_DIR:-/tmp/kubestellar-docs-snapshot}"
OUTPUT_DIR="${DOCS_REPO}/public/live/hive"
BRANCH="main"
DOCS_REPO_SLUG="kubestellar/docs"

GH_APP_TOKEN_FILE="/var/run/hive-metrics/gh-app-token.cache"
if [ -f "$GH_APP_TOKEN_FILE" ]; then
  GH_APP_TOKEN=$(cat "$GH_APP_TOKEN_FILE")
  DOCS_REMOTE="https://x-access-token:${GH_APP_TOKEN}@github.com/${DOCS_REPO_SLUG}.git"
  export GH_TOKEN="$GH_APP_TOKEN"
else
  echo "ERROR: no GitHub App token at $GH_APP_TOKEN_FILE"
  exit 1
fi

# Ensure docs repo clone exists
if [ ! -d "$DOCS_REPO/.git" ]; then
  git clone --depth 1 --single-branch -b "$BRANCH" "$DOCS_REMOTE" "$DOCS_REPO"
fi

cd "$DOCS_REPO"
git remote set-url origin "$DOCS_REMOTE"
git fetch origin "$BRANCH"
git checkout "$BRANCH" 2>/dev/null || git checkout -b "$BRANCH" "origin/$BRANCH"
git reset --hard "origin/$BRANCH"

# Build snapshot
mkdir -p "$OUTPUT_DIR"
node "${SCRIPT_DIR}/build-snapshot.mjs" "$DASHBOARD_URL" "${OUTPUT_DIR}/index.html"

# Check if anything changed
if git diff --quiet -- public/live/hive/; then
  echo "No changes to snapshot — skipping commit."
  exit 0
fi

TIMESTAMP=$(date -u '+%Y-%m-%d %H:%M UTC')

# Commit and push directly to main
git add public/live/hive/index.html
git commit -s -m "chore: update hive dashboard snapshot $TIMESTAMP"

MAX_RETRIES=3
RETRY_DELAY_SECONDS=5
for i in $(seq 1 $MAX_RETRIES); do
  if git pull --rebase origin "$BRANCH" && git push origin "$BRANCH"; then
    echo "Snapshot published directly to main."
    exit 0
  fi
  echo "Push attempt $i/$MAX_RETRIES failed, retrying in ${RETRY_DELAY_SECONDS}s..."
  sleep "$RETRY_DELAY_SECONDS"
done

echo "ERROR: all $MAX_RETRIES push attempts failed."
exit 1
