#!/usr/bin/env bash
# Build a dashboard snapshot and push it to the docs repo.
# Designed to run as a cron job on the hive server.
#
# Usage: ./publish-snapshot.sh
# Env vars:
#   HIVE_DASHBOARD_URL  — dashboard URL (default: http://localhost:3001)
#   DOCS_REPO_DIR       — local clone of kubestellar/docs (default: /tmp/kubestellar-docs-snapshot)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DASHBOARD_URL="${HIVE_DASHBOARD_URL:-http://localhost:3001}"
DOCS_REPO="${DOCS_REPO_DIR:-/tmp/kubestellar-docs-snapshot}"
DOCS_REMOTE="https://github.com/kubestellar/docs.git"
OUTPUT_DIR="${DOCS_REPO}/public/live/hive"
BRANCH="main"

# Ensure docs repo clone exists
if [ ! -d "$DOCS_REPO/.git" ]; then
  git clone --depth 1 --single-branch -b "$BRANCH" "$DOCS_REMOTE" "$DOCS_REPO"
fi

cd "$DOCS_REPO"
git fetch origin "$BRANCH"
git reset --hard "origin/$BRANCH"

# Build snapshot
mkdir -p "$OUTPUT_DIR"
node "${SCRIPT_DIR}/build-snapshot.mjs" "$DASHBOARD_URL" "${OUTPUT_DIR}/index.html"

# Check if anything changed
if git diff --quiet -- public/live/hive/; then
  echo "No changes to snapshot — skipping commit."
  exit 0
fi

# Commit and push
git add public/live/hive/index.html
git commit -s -m "chore: update hive dashboard snapshot $(date -u '+%Y-%m-%d %H:%M UTC')"
git push origin "$BRANCH"
echo "Snapshot published."
