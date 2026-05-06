#!/usr/bin/env bash
# Correlate governor kicks with queue/MTTR/token outcomes.
# Called at end of each governor tick. Writes outcome records to kick-outcomes.jsonl
# after a cooldown period so we can measure the effect of each kick.
set -euo pipefail

METRICS_DIR="${NOUS_METRICS_DIR:-/var/run/hive-metrics}"
OUTCOMES_FILE="${METRICS_DIR}/kick-outcomes.jsonl"
AUDIT_LOG="${KICK_AUDIT_LOG:-/var/log/kick-audit.jsonl}"
STATE_DIR="${GOVERNOR_STATE_DIR:-/var/run/kick-governor}"
SNAPSHOTS_DIR="${NOUS_SNAPSHOTS_DIR:-/var/run/nous/snapshots}"
PENDING_DIR="${METRICS_DIR}/pending-outcomes"

# How many ticks to wait before measuring outcome (2 ticks ≈ 30min)
COOLDOWN_TICKS=2
TICK_INTERVAL_SEC=900

mkdir -p "$METRICS_DIR" "$PENDING_DIR" "$SNAPSHOTS_DIR"

log() { echo "[outcome-tracker] $(date '+%Y-%m-%d %H:%M:%S') $*"; }

# ── Record current state as a pending outcome seed ──────────────────────────
record_pending() {
  local ts now_epoch queue_depth tokens_hourly mttr_avg mode
  ts=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  now_epoch=$(date +%s)

  queue_depth=$(cat "$STATE_DIR/queue_depth" 2>/dev/null || echo 0)
  mode=$(cat "$STATE_DIR/mode" 2>/dev/null || echo "unknown")

  # Token burn from collector
  tokens_hourly=0
  if [[ -f "${METRICS_DIR}/tokens.json" ]]; then
    tokens_hourly=$(jq -r '.hourly.billableTokens // 0' "${METRICS_DIR}/tokens.json" 2>/dev/null || echo 0)
  fi

  # MTTR from issue-to-merge tracker
  mttr_avg=0
  if [[ -f "${METRICS_DIR}/issue_to_merge.json" ]]; then
    mttr_avg=$(jq -r '.avg_minutes // 0' "${METRICS_DIR}/issue_to_merge.json" 2>/dev/null || echo 0)
  fi

  # Find kicks from this tick in audit log (last 5 minutes of entries)
  local kicks_this_tick=0
  local kicked_agents=""
  if [[ -f "$AUDIT_LOG" ]]; then
    local cutoff=$((now_epoch - TICK_INTERVAL_SEC))
    while IFS= read -r line; do
      local entry_ts entry_action entry_agent
      entry_action=$(echo "$line" | jq -r '.action // empty' 2>/dev/null || true)
      if [[ "$entry_action" == "KICK" ]]; then
        kicks_this_tick=$((kicks_this_tick + 1))
        entry_agent=$(echo "$line" | jq -r '.agent // empty' 2>/dev/null || true)
        kicked_agents="${kicked_agents:+$kicked_agents,}$entry_agent"
      fi
    done < <(tail -50 "$AUDIT_LOG" 2>/dev/null || true)
  fi

  # Only record if there were actual kicks this tick
  if [[ $kicks_this_tick -eq 0 ]]; then
    log "No kicks this tick — skipping outcome seed"
    return
  fi

  local seed_file="${PENDING_DIR}/${now_epoch}.json"
  cat > "$seed_file" <<EOF
{
  "ts": "${ts}",
  "epoch": ${now_epoch},
  "mode": "${mode}",
  "queue_at_kick": ${queue_depth},
  "tokens_hourly_at_kick": ${tokens_hourly},
  "mttr_at_kick": ${mttr_avg},
  "kicks": ${kicks_this_tick},
  "kicked_agents": "${kicked_agents}",
  "measure_after_epoch": $((now_epoch + COOLDOWN_TICKS * TICK_INTERVAL_SEC))
}
EOF
  log "Recorded pending outcome seed: ${kicks_this_tick} kicks [${kicked_agents}] queue=${queue_depth}"
}

# ── Resolve matured pending outcomes ────────────────────────────────────────
resolve_pending() {
  local now_epoch
  now_epoch=$(date +%s)

  for seed_file in "$PENDING_DIR"/*.json; do
    [[ -f "$seed_file" ]] || continue

    local measure_after
    measure_after=$(jq -r '.measure_after_epoch' "$seed_file" 2>/dev/null || echo 999999999999)

    if [[ $now_epoch -lt $measure_after ]]; then
      continue
    fi

    # Measure current state
    local queue_after tokens_after mttr_after
    queue_after=$(cat "$STATE_DIR/queue_depth" 2>/dev/null || echo 0)
    tokens_after=0
    if [[ -f "${METRICS_DIR}/tokens.json" ]]; then
      tokens_after=$(jq -r '.hourly.billableTokens // 0' "${METRICS_DIR}/tokens.json" 2>/dev/null || echo 0)
    fi
    mttr_after=0
    if [[ -f "${METRICS_DIR}/issue_to_merge.json" ]]; then
      mttr_after=$(jq -r '.avg_minutes // 0' "${METRICS_DIR}/issue_to_merge.json" 2>/dev/null || echo 0)
    fi

    # Compute deltas
    local queue_at tokens_at seed_epoch
    queue_at=$(jq -r '.queue_at_kick' "$seed_file")
    tokens_at=$(jq -r '.tokens_hourly_at_kick' "$seed_file")
    seed_epoch=$(jq -r '.epoch' "$seed_file")
    local elapsed=$((now_epoch - seed_epoch))
    local delta_queue=$((queue_after - queue_at))
    local tokens_burned=$((tokens_after > 0 ? tokens_after : 1))

    # Cost-effectiveness: negative delta_queue is good (queue shrank)
    # effectiveness = -delta_queue / tokens_burned (higher = better)
    local effectiveness
    effectiveness=$(awk "BEGIN { printf \"%.6f\", ${delta_queue} * -1.0 / ${tokens_burned} }")

    # Build outcome record
    local outcome
    outcome=$(jq -c \
      --argjson queue_after "$queue_after" \
      --argjson tokens_after "$tokens_after" \
      --argjson mttr_after "$mttr_after" \
      --argjson delta_queue "$delta_queue" \
      --argjson elapsed "$elapsed" \
      --arg effectiveness "$effectiveness" \
      '. + {
        queue_after: $queue_after,
        tokens_hourly_after: $tokens_after,
        mttr_after: $mttr_after,
        delta_queue: $delta_queue,
        elapsed_sec: $elapsed,
        effectiveness: ($effectiveness | tonumber)
      }' "$seed_file")

    echo "$outcome" >> "$OUTCOMES_FILE"
    rm -f "$seed_file"
    log "Resolved outcome: delta_queue=${delta_queue} effectiveness=${effectiveness} elapsed=${elapsed}s"
  done
}

# ── Main ────────────────────────────────────────────────────────────────────
record_pending
resolve_pending
