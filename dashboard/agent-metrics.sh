#!/bin/bash
# Per-agent metrics for dashboard — outputs JSON
# Uses REST API (not GraphQL) to avoid rate limit exhaustion
set +e
unset GITHUB_TOKEN

# ── Scanner: map issues → fix PRs from branch names ──
# REST API: get open PRs with fix/ prefix
scanner_json="[]"
fix_prs=$(gh api "repos/kubestellar/console/pulls?state=open&per_page=100" \
  --jq '.[] | select(.head.ref | startswith("fix/")) | "\(.number) \(.head.ref)"' 2>/dev/null || echo "")
if [ -n "$fix_prs" ]; then
  scanner_json=$(echo "$fix_prs" | while read -r pr branch; do
    issue=$(echo "$branch" | grep -oP '\d+$' || echo "")
    [ -n "$issue" ] && echo "{\"issue\":$issue,\"pr\":$pr}"
  done | jq -s '.')
fi

# ── Reviewer: health checks come from health-check.sh separately ──

# ── Outreach: GA4 errors, adopter PRs, adoption stats ──
# REST API for issues with ga4-error label
ga4_errors=$(gh api "repos/kubestellar/console/issues?state=open&labels=ga4-error&per_page=1" \
  --jq 'length' 2>/dev/null || echo 0)
ga4_errors=${ga4_errors:-0}
# REST API for outreach/adopter PRs
adopter_prs=$(gh api "repos/kubestellar/console/pulls?state=open&per_page=100" \
  --jq '[.[] | select(.head.ref | test("outreach|adopter")) | .number]' 2>/dev/null || echo "[]")
adopter_prs=${adopter_prs:-"[]"}
adopter_count=$(echo "$adopter_prs" | jq 'length' 2>/dev/null || echo 0)
# Read agent-authored summary
outreach_summary=$(cat /var/run/hive-metrics/outreach_summary.txt 2>/dev/null || echo "")
outreach_summary=${outreach_summary:-"no summary yet"}
outreach_summary_json=$(echo "$outreach_summary" | head -1 | head -c 120 | jq -Rs '.')
# Count current adopters in ADOPTERS.MD (merged lines)
adopters_total=$(gh api repos/kubestellar/console/contents/ADOPTERS.MD \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -cP '^\|.*\|.*\|' || echo 0)
# Subtract header rows (2)
adopters_total=$(( adopters_total > 2 ? adopters_total - 2 : 0 ))

# ── Architect: exec summary from status file + PR/proposal counts ──
architect_lines=$(tmux capture-pane -t feature -p -S -500 2>/dev/null)
architect_prs=$(echo "$architect_lines" | grep -oP 'pull/\d+' | sort -u | wc -l)
architect_prs=${architect_prs:-0}
architect_closed=$(echo "$architect_lines" | grep -ciP 'closed|resolved|stale')
architect_closed=${architect_closed:-0}
# Read agent-authored summary (agents write this themselves each pass)
architect_summary=$(cat /var/run/hive-metrics/architect_summary.txt 2>/dev/null || echo "")
architect_summary=${architect_summary:-"no summary yet"}
architect_summary_json=$(echo "$architect_summary" | head -1 | head -c 120 | jq -Rs '.')

cat <<EOF
{
  "scanner": {"pairs": $scanner_json},
  "reviewer": {},
  "outreach": {"ga4Errors": $ga4_errors, "adopterPrs": $adopter_prs, "adopterPending": $adopter_count, "adoptersTotal": $adopters_total, "summary": $outreach_summary_json},
  "architect": {"prs": $architect_prs, "closed": $architect_closed, "summary": $architect_summary_json}
}
EOF
