#!/bin/bash
# Per-agent metrics for dashboard — outputs JSON
set +e
unset GITHUB_TOKEN

# ── Scanner: extract issue/PR numbers from recent tmux output ──
scanner_lines=$(tmux capture-pane -t issue-scanner -p 2>/dev/null | tail -80)
# Look for patterns like #1234, repo#1234, issues/1234, pull/1234
scanner_items=$(echo "$scanner_lines" | grep -oP '(?:#|issues/|pull/)\d+' | grep -oP '\d+' | sort -un | tail -10)
scanner_json="[]"
if [ -n "$scanner_items" ]; then
  scanner_json=$(echo "$scanner_items" | jq -R '.' | jq -s '.')
fi

# ── Reviewer: health checks come from health-check.sh separately ──
# Reviewer shows: CI pass rate, brew, helm, nightly, weekly, vllm-d, pokprod
# These are already in healthChecks — just flag that reviewer owns them

# ── Outreach: GA4 data from recent logs + campaign stats ──
outreach_lines=$(tmux capture-pane -t outreach -p 2>/dev/null | tail -100)
# Count PRs opened (look for "PR Opened" or "open_pr" mentions)
outreach_prs=$(echo "$outreach_lines" | grep -ciP 'pr.*open|open.*pr|pull/\d+')
outreach_prs=${outreach_prs:-0}
# Count repos targeted
outreach_repos=$(echo "$outreach_lines" | grep -oP '"[^"]+/[^"]+"' | sort -u | wc -l || echo 0)
# Look for errors
outreach_errors=$(echo "$outreach_lines" | grep -ciP 'error|fail|rate.limit|403|422' || echo 0)

# ── Architect: count of beads/issues being worked ──
architect_lines=$(tmux capture-pane -t feature -p 2>/dev/null | tail -80)
architect_items=$(echo "$architect_lines" | grep -oP '#\d+' | grep -oP '\d+' | sort -un | tail -10)
architect_json="[]"
if [ -n "$architect_items" ]; then
  architect_json=$(echo "$architect_items" | jq -R '.' | jq -s '.')
fi
architect_closed=$(echo "$architect_lines" | grep -ciP 'closed|resolved|stale' || echo 0)

cat <<EOF
{
  "scanner": {"items": $scanner_json},
  "reviewer": {},
  "outreach": {"prsOpened": $outreach_prs, "reposTargeted": $outreach_repos, "errors": $outreach_errors},
  "architect": {"items": $architect_json, "closed": $architect_closed}
}
EOF
