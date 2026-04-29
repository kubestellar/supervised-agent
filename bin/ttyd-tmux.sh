#!/bin/bash
# Wrapper for ttyd → tmux that disables mouse mode so the browser
# handles text selection and clipboard (Cmd+C / Cmd+V).
# Mouse mode is restored when the ttyd session disconnects.
SESSION=${1:-supervisor}
PREV_MOUSE=$(tmux show-option -t "$SESSION" -v mouse 2>/dev/null || echo "on")
tmux set-option -t "$SESSION" mouse off 2>/dev/null
tmux attach-session -t "$SESSION"
EXIT_CODE=$?
tmux set-option -t "$SESSION" mouse "$PREV_MOUSE" 2>/dev/null
exit $EXIT_CODE
