#!/bin/bash
# gh wrapper — enforces per-agent + global restrictions and injects App token.
# Installed at /usr/local/bin/gh (ahead of /usr/bin/gh in PATH).
#
# Per-agent restrictions live at /etc/hive/restrictions/<agent-id>.json.
# The wrapper reads HIVE_AGENT_ID to find the right file.
#
# Restriction file format:
#   { "rules": [
#       { "pattern": "gh issue list*", "reason": "Use actionable.json", "enabled": true },
#       { "pattern": "gh api repos/*/issues*", "reason": "Enumeration disabled", "enabled": true }
#   ]}
#
# Pattern matching: the full command ("gh issue list --repo foo") is checked
# against each pattern using bash glob matching. Patterns support * wildcards.

set -euo pipefail

REAL_GH="/usr/bin/gh"
RESTRICTIONS_DIR="/etc/hive/restrictions"

# Inject GitHub App token for agent gh calls (15k/hr vs PAT's 5k/hr).
GH_APP_TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -f "$GH_APP_TOKEN_CACHE" ]]; then
  export GH_TOKEN="$(cat "$GH_APP_TOKEN_CACHE")"
elif [[ -n "${HIVE_GITHUB_TOKEN:-}" ]]; then
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
fi

# Build the full command string for pattern matching
FULL_CMD="gh $*"

# Check per-agent restrictions
AGENT_ID="${HIVE_AGENT_ID:-}"
if [[ -n "$AGENT_ID" ]]; then
  RESTRICTION_FILE="${RESTRICTIONS_DIR}/${AGENT_ID}.json"
  if [[ -f "$RESTRICTION_FILE" ]]; then
    while IFS='|' read -r pattern reason; do
      [[ -z "$pattern" ]] && continue
      # Use bash extglob for pattern matching
      # shellcheck disable=SC2254
      case "$FULL_CMD" in
        $pattern)
          echo "⛔ BLOCKED: ${reason:-command not allowed for ${AGENT_ID}}" >&2
          exit 1
          ;;
      esac
    done < <(python3 -c "
import json, sys
try:
    with open('${RESTRICTION_FILE}') as f:
        data = json.load(f)
    for r in data.get('rules', []):
        if r.get('enabled', True):
            print(r.get('pattern','') + '|' + r.get('reason',''))
except Exception:
    pass
" 2>/dev/null)
  fi
fi

# Global defaults — always enforced for all agents regardless of restriction file
args=("$@")
subcmd=""
action=""
for arg in "${args[@]}"; do
  case "$arg" in
    -*) continue ;;
    *)
      if [ -z "$subcmd" ]; then
        subcmd="$arg"
      elif [ -z "$action" ]; then
        action="$arg"
        break
      fi
      ;;
  esac
done

# Block gh issue list and gh pr list (global)
if { [ "$subcmd" = "issue" ] || [ "$subcmd" = "pr" ]; } && [ "$action" = "list" ]; then
  echo "⛔ BLOCKED: gh $subcmd list is disabled for agents." >&2
  echo "Read /var/run/hive-metrics/actionable.json instead." >&2
  exit 1
fi

# Enforce merge gate — only PRs in merge-eligible.json can be merged
MERGE_ELIGIBLE_FILE="/var/run/hive-metrics/merge-eligible.json"
if [ "$subcmd" = "pr" ] && [ "$action" = "merge" ]; then
  pr_num=""
  pr_repo=""
  skip_next=false
  past_merge=false
  for arg in "${args[@]}"; do
    if $skip_next; then skip_next=false; continue; fi
    case "$arg" in
      pr|merge) past_merge=true; continue ;;
      --repo) skip_next=true; continue ;;
      --repo=*) pr_repo="${arg#--repo=}"; continue ;;
      -*) continue ;;
      *)
        if $past_merge && [ -z "$pr_num" ]; then
          pr_num="$arg"
        fi
        ;;
    esac
  done

  if [ -z "$pr_repo" ]; then
    for i in "${!args[@]}"; do
      if [ "${args[$i]}" = "--repo" ] && [ -n "${args[$((i+1))]:-}" ]; then
        pr_repo="${args[$((i+1))]}"
        break
      fi
    done
  fi

  if [ -n "$pr_num" ] && [ -f "$MERGE_ELIGIBLE_FILE" ]; then
    is_eligible=$(python3 -c "
import json, sys
try:
    with open('${MERGE_ELIGIBLE_FILE}') as f:
        data = json.load(f)
    repo_filter = '${pr_repo}'
    for pr in data.get('merge_eligible', []):
        if str(pr.get('number')) == '${pr_num}':
            if not repo_filter or pr.get('repo','') == repo_filter:
                print('yes')
                sys.exit(0)
    print('no')
except Exception as e:
    print('error:' + str(e), file=sys.stderr)
    print('no')
" 2>/dev/null)

    if [ "$is_eligible" != "yes" ]; then
      echo "⛔ BLOCKED: PR #${pr_num} is NOT in merge-eligible.json." >&2
      echo "The merge gate requires all CI checks to pass before merging." >&2
      echo "Run 'cat ${MERGE_ELIGIBLE_FILE} | python3 -m json.tool' to see eligible PRs." >&2
      exit 1
    fi
  elif [ -n "$pr_num" ] && [ ! -f "$MERGE_ELIGIBLE_FILE" ]; then
    echo "⛔ BLOCKED: ${MERGE_ELIGIBLE_FILE} not found — cannot verify merge eligibility." >&2
    echo "Run merge-gate.sh first, or wait for the next pipeline cycle." >&2
    exit 1
  fi
fi

# Block gh api calls that list issues or pulls (global)
if [ "$subcmd" = "api" ]; then
  for arg in "${args[@]}"; do
    case "$arg" in
      repos/*/issues\?*|repos/*/issues|repos/*/pulls\?*|repos/*/pulls)
        echo "⛔ BLOCKED: gh api issue/PR listing is disabled for agents." >&2
        echo "Read /var/run/hive-metrics/actionable.json instead." >&2
        exit 1
        ;;
    esac
  done
fi

exec "$REAL_GH" "$@"
