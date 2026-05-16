#!/bin/bash
# fetch-coverage.sh — pulls coverage metrics from the coverage-hourly workflow
# (Coverage Suite) on ${PROJECT_ORG}/${PROJECT_PRIMARY_REPO} and writes a JSON
# summary to /var/run/hive-metrics/coverage.json for the tester kick message.
#
# Data sources:
#   1. merge-coverage job logs → total lines/statements/branches/functions %
#   2. coverage-summary.json artifact (if available) → per-file breakdown
#
# Called by kick-agents.sh before the tester kick. Caches for 10 min to avoid
# hammering the GitHub API on rapid kicks.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Source project config for org/repo
if [[ -f "${SCRIPT_DIR}/hive-config.sh" ]]; then
  source "${SCRIPT_DIR}/hive-config.sh"
elif [[ -f /usr/local/bin/hive-config.sh ]]; then
  source /usr/local/bin/hive-config.sh
fi

COVERAGE_REPO="${PROJECT_ORG}/${PROJECT_PRIMARY_REPO}"
COVERAGE_WORKFLOW="coverage-hourly.yml"
OUTPUT_FILE="/var/run/hive-metrics/coverage.json"
CACHE_MAX_AGE_SEC=600
COVERAGE_THRESHOLD=91

GH_CMD="gh"
TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -f "$TOKEN_CACHE" ]]; then
  GH_CMD="GH_TOKEN=$(cat "$TOKEN_CACHE") gh"
fi

mkdir -p /var/run/hive-metrics 2>/dev/null || true

# Skip if cache is fresh
if [[ -f "$OUTPUT_FILE" ]]; then
  file_age=$(( $(date +%s) - $(stat -c %Y "$OUTPUT_FILE" 2>/dev/null || stat -f %m "$OUTPUT_FILE" 2>/dev/null || echo 0) ))
  if [[ "$file_age" -lt "$CACHE_MAX_AGE_SEC" ]]; then
    exit 0
  fi
fi

# Find the latest coverage-hourly run that produced a merge-coverage job
run_json=$(eval "$GH_CMD" run list --repo "$COVERAGE_REPO" --workflow="$COVERAGE_WORKFLOW" \
  --limit 5 --json databaseId,conclusion,createdAt 2>/dev/null || echo "[]")

if [[ "$run_json" == "[]" ]]; then
  echo '{"error":"no coverage runs found","lines":0,"statements":0,"branches":0,"functions":0}' > "$OUTPUT_FILE"
  exit 0
fi

# Try each run until we find one with a merge-coverage job that has coverage data
for run_id in $(echo "$run_json" | python3 -c "import sys,json; [print(r['databaseId']) for r in json.load(sys.stdin)]" 2>/dev/null); do
  # Get the merge-coverage job ID
  merge_job_id=$(eval "$GH_CMD" api "repos/${COVERAGE_REPO}/actions/runs/${run_id}/jobs" \
    --jq '.jobs[] | select(.name == "merge-coverage") | .id' 2>/dev/null || echo "")

  [[ -z "$merge_job_id" ]] && continue

  # Pull the job logs and extract the coverage summary table
  logs=$(eval "$GH_CMD" api "repos/${COVERAGE_REPO}/actions/jobs/${merge_job_id}/logs" 2>/dev/null || echo "")
  [[ -z "$logs" ]] && continue

  # Extract "## Coverage Summary" output lines (not script definition — no ANSI codes)
  lines_pct=$(echo "$logs" | grep -E '^\d{4}-.*\| Lines \|' | grep -v '\[36;1m' | tail -1 | grep -oE '[0-9]+\.[0-9]+' || echo "")
  stmts_pct=$(echo "$logs" | grep -E '^\d{4}-.*\| Statements \|' | grep -v '\[36;1m' | tail -1 | grep -oE '[0-9]+\.[0-9]+' || echo "")
  branch_pct=$(echo "$logs" | grep -E '^\d{4}-.*\| Branches \|' | grep -v '\[36;1m' | tail -1 | grep -oE '[0-9]+\.[0-9]+' || echo "")
  funcs_pct=$(echo "$logs" | grep -E '^\d{4}-.*\| Functions \|' | grep -v '\[36;1m' | tail -1 | grep -oE '[0-9]+\.[0-9]+' || echo "")

  [[ -z "$lines_pct" ]] && continue

  # Extract the per-file coverage table from the Vitest text reporter output.
  # Lines look like: "  src/foo/bar.ts  |  45.23 |  30.00 |  50.00 |  42.85 | ..."
  # We want files with lines coverage < threshold.
  low_files=$(echo "$logs" | grep -v '\[36;1m' | \
    grep -E '^\d{4}-.*\|.*\|.*\|.*\|.*\|' | \
    grep -v 'File\|---\|All files' | \
    python3 -c "
import sys
THRESHOLD = $COVERAGE_THRESHOLD
results = []
for line in sys.stdin:
    # Strip timestamp prefix
    parts = line.split('|')
    if len(parts) < 5:
        continue
    # parts[0] has timestamp + file path, parts[1]=stmts, parts[2]=branch, parts[3]=funcs, parts[4]=lines
    try:
        file_part = parts[0].strip()
        # Extract just the file path (after the timestamp)
        file_name = file_part.split('Z ')[-1].strip() if 'Z ' in file_part else file_part
        stmts = float(parts[1].strip())
        branch = float(parts[2].strip())
        funcs = float(parts[3].strip())
        lines = float(parts[4].strip())
        if lines < THRESHOLD:
            results.append({'file': file_name, 'lines': lines, 'statements': stmts, 'branches': branch, 'functions': funcs})
    except (ValueError, IndexError):
        continue
# Sort by lines coverage ascending (worst first), take top 20
results.sort(key=lambda x: x['lines'])
import json
print(json.dumps(results[:20]))
" 2>/dev/null || echo "[]")

  # Extract the run's created_at timestamp
  run_ts=$(echo "$run_json" | python3 -c "
import sys, json
runs = json.load(sys.stdin)
for r in runs:
    if r['databaseId'] == $run_id:
        print(r['createdAt'])
        break
" 2>/dev/null || echo "unknown")

  # Count failing test shards
  failing_shards=$(eval "$GH_CMD" api "repos/${COVERAGE_REPO}/actions/runs/${run_id}/jobs" \
    --jq '[.jobs[] | select(.name | startswith("test-shard")) | select(.conclusion == "failure")] | length' 2>/dev/null || echo "0")

  # Write the output
  python3 -c "
import json
data = {
    'lines': ${lines_pct:-0},
    'statements': ${stmts_pct:-0},
    'branches': ${branch_pct:-0},
    'functions': ${funcs_pct:-0},
    'threshold': $COVERAGE_THRESHOLD,
    'run_id': $run_id,
    'run_ts': '$run_ts',
    'failing_shards': $failing_shards,
    'low_coverage_files': json.loads('''$low_files'''),
    'fetched_at': '$(date -u +%Y-%m-%dT%H:%M:%S+00:00)'
}
print(json.dumps(data, indent=2))
" > "$OUTPUT_FILE" 2>/dev/null

  exit 0
done

# All runs failed to produce coverage data
echo '{"error":"no coverage data in recent runs","lines":0,"statements":0,"branches":0,"functions":0}' > "$OUTPUT_FILE"
