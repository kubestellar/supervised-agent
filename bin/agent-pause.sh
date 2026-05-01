#!/bin/bash
# agent-pause.sh — Centralized pause/unpause logic for hive agents.
# Source this file to use the functions, or run directly:
#   agent-pause.sh pause <agent> [--operator]
#   agent-pause.sh resume <agent>
#   agent-pause.sh status <agent>
#   agent-pause.sh list
#
# All pause state lives in PAUSE_DIR (default: /var/run/kick-governor).
# Two file types:
#   paused_<agent>          — agent is paused (checked by governor, kick-agents, dashboard)
#   operator_paused_<agent> — operator explicitly paused (survives rate-limit pullback resume)
#
# Rules:
#   - Operator pause creates BOTH files
#   - System pause (rate-limit pullback) creates only paused_<agent>
#   - Resume removes paused_<agent> ONLY IF operator_paused_<agent> does NOT exist
#   - Operator resume (--force) removes both files
#   - Any component can check is_paused / is_operator_paused

PAUSE_DIR="${PAUSE_DIR:-/var/run/kick-governor}"
VALID_AGENTS="scanner reviewer architect outreach supervisor"

_agent_valid() {
  echo "$VALID_AGENTS" | grep -qw "$1"
}

agent_pause() {
  local agent="$1"
  local operator="${2:-false}"
  _agent_valid "$agent" || { echo "invalid agent: $agent" >&2; return 1; }

  echo "$(date -Iseconds)" > "$PAUSE_DIR/paused_${agent}"
  if [[ "$operator" == "--operator" || "$operator" == "true" ]]; then
    echo "$(date -Iseconds)" > "$PAUSE_DIR/operator_paused_${agent}"
  fi
}

agent_resume() {
  local agent="$1"
  local force="${2:-false}"
  _agent_valid "$agent" || { echo "invalid agent: $agent" >&2; return 1; }

  if [[ "$force" == "--force" || "$force" == "true" ]]; then
    rm -f "$PAUSE_DIR/paused_${agent}"
    rm -f "$PAUSE_DIR/operator_paused_${agent}"
    return 0
  fi

  if [[ -f "$PAUSE_DIR/operator_paused_${agent}" ]]; then
    echo "SKIP: $agent is operator-paused (use --force to override)" >&2
    return 1
  fi

  rm -f "$PAUSE_DIR/paused_${agent}"
}

is_paused() {
  [[ -f "$PAUSE_DIR/paused_${1}" ]]
}

is_operator_paused() {
  [[ -f "$PAUSE_DIR/operator_paused_${1}" ]]
}

agent_pause_status() {
  local agent="$1"
  _agent_valid "$agent" || { echo "invalid agent: $agent" >&2; return 1; }

  if is_operator_paused "$agent"; then
    echo "operator-paused"
  elif is_paused "$agent"; then
    echo "system-paused"
  else
    echo "active"
  fi
}

agent_pause_list() {
  for agent in $VALID_AGENTS; do
    echo "$agent: $(agent_pause_status "$agent")"
  done
}

# Direct invocation
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  cmd="${1:-list}"
  case "$cmd" in
    pause)
      [[ -z "$2" ]] && { echo "usage: $0 pause <agent> [--operator]" >&2; exit 1; }
      agent_pause "$2" "${3:---operator}"
      echo "$(agent_pause_status "$2"): $2"
      ;;
    resume)
      [[ -z "$2" ]] && { echo "usage: $0 resume <agent> [--force]" >&2; exit 1; }
      agent_resume "$2" "$3"
      echo "$(agent_pause_status "$2"): $2"
      ;;
    status)
      [[ -z "$2" ]] && { echo "usage: $0 status <agent>" >&2; exit 1; }
      agent_pause_status "$2"
      ;;
    list)
      agent_pause_list
      ;;
    *)
      echo "usage: $0 {pause|resume|status|list} [agent] [--operator|--force]" >&2
      exit 1
      ;;
  esac
fi
