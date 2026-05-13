#!/bin/bash
set -euo pipefail

SESSION=${1:-supervisor}
PREV_MOUSE=$(tmux show-option -t "$SESSION" -v mouse 2>/dev/null || echo "on")
tmux set-option -t "$SESSION" mouse off 2>/dev/null || true
EXIT_CODE=0
tmux attach-session -t "$SESSION" || EXIT_CODE=$?
tmux set-option -t "$SESSION" mouse "$PREV_MOUSE" 2>/dev/null || true
exit $EXIT_CODE
