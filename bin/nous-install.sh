#!/usr/bin/env bash
set -euo pipefail

NOUS_DIR="${NOUS_DIR:-/opt/nous}"
NOUS_VENV="${NOUS_DIR}/venv"
NOUS_RUN_DIR="${NOUS_RUN_DIR:-/var/run/nous}"
NOUS_REPO="https://github.com/AI-native-Systems-Research/agentic-strategy-evolution"

if [ -d "$NOUS_DIR/.git" ]; then
  echo "Nous already installed at $NOUS_DIR — pulling latest"
  cd "$NOUS_DIR" && git pull --ff-only
else
  echo "Cloning Nous framework to $NOUS_DIR"
  git clone "$NOUS_REPO" "$NOUS_DIR"
fi

cd "$NOUS_DIR"

if [ ! -d "$NOUS_VENV" ]; then
  echo "Creating virtual environment at $NOUS_VENV"
  python3 -m venv "$NOUS_VENV"
fi

echo "Installing Nous into venv"
"$NOUS_VENV/bin/pip" install -e . 2>&1

mkdir -p "$NOUS_RUN_DIR"/{governor,repo,snapshots}
chown -R dev:dev "$NOUS_RUN_DIR" "$NOUS_DIR"

echo "Verifying installation…"
"$NOUS_VENV/bin/python3" -c "from run_campaign import run_campaign; print('Nous framework installed OK')"

echo ""
echo "Use $NOUS_VENV/bin/python3 to run Nous scripts"
